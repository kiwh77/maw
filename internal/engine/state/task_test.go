package state_test

import (
	"errors"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/state"
)

func TestTaskVocabularyIsPreserved(t *testing.T) {
	want := []string{
		"backlog", "ready", "claimed", "in_progress", "self_check",
		"in_review", "merging", "conflict", "blocked", "done", "cancelled",
	}
	got := state.TaskStatuses()
	if len(got) != len(want) {
		t.Fatalf("业务状态应为 %d 个，实际 %d 个：%v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个状态漂移：want %s got %s", i, want[i], got[i])
		}
	}
}

func TestLegalTransitions(t *testing.T) {
	legal := [][2]string{
		{"backlog", "ready"},
		{"ready", "in_progress"},
		{"claimed", "in_progress"},
		{"in_progress", "in_review"},
		{"in_progress", "self_check"},
		{"in_review", "merging"},
		{"merging", "done"},
		{"merging", "conflict"},
		{"merging", "in_review"}, // amend 后再交
		{"conflict", "ready"},
	}
	for _, pair := range legal {
		if err := state.AssertTaskTransition(pair[0], pair[1]); err != nil {
			t.Errorf("%s → %s 应合法：%v", pair[0], pair[1], err)
		}
	}
}

func TestSameStatusIsRejected(t *testing.T) {
	for _, s := range []string{"ready", "in_progress", "in_review", "done"} {
		err := state.AssertTaskTransition(s, s)
		if !errors.Is(err, state.ErrSameStatus) {
			t.Errorf("%s → %s 应报同态，得到 %v", s, s, err)
		}
	}
}

func TestTerminalStatusesAreClosed(t *testing.T) {
	// 同态另算一类（ErrSameStatus）；这里只验终态没有出边。
	for _, dst := range []string{"ready", "in_progress", "in_review", "merging", "cancelled"} {
		if err := state.AssertTaskTransition("done", dst); !errors.Is(err, state.ErrIllegalTransition) {
			t.Errorf("done → %s 应被拒（终态），得到 %v", dst, err)
		}
	}
	for _, dst := range []string{"ready", "in_progress", "in_review", "merging", "done"} {
		if err := state.AssertTaskTransition("cancelled", dst); !errors.Is(err, state.ErrIllegalTransition) {
			t.Errorf("cancelled → %s 应被拒（终态），得到 %v", dst, err)
		}
	}
	for _, s := range []string{"done", "cancelled"} {
		if err := state.AssertTaskTransition(s, s); !errors.Is(err, state.ErrSameStatus) {
			t.Errorf("%s → %s 应报同态，得到 %v", s, s, err)
		}
	}
	if !state.IsTerminalTaskStatus("done") || !state.IsTerminalTaskStatus("cancelled") {
		t.Fatal("done/cancelled 应判终态")
	}
	if state.IsTerminalTaskStatus("in_review") {
		t.Fatal("in_review 不是终态")
	}
}

func TestCannotSkipAcceptance(t *testing.T) {
	// ready 不能直达 done：必须经过 in_review → merging。
	if err := state.AssertTaskTransition("ready", "done"); err == nil {
		t.Fatal("ready → done 必须被拒")
	}
	if err := state.AssertTaskTransition("in_progress", "done"); err == nil {
		t.Fatal("in_progress → done 必须被拒")
	}
}

func TestBlockedUnblocksToAnyStatus(t *testing.T) {
	for _, dst := range state.TaskStatuses() {
		if err := state.AssertTaskTransition("blocked", dst); err != nil {
			t.Fatalf("blocked → %s 应允许恢复：%v", dst, err)
		}
	}
}

func TestUnknownStatusRejected(t *testing.T) {
	if err := state.AssertTaskTransition("ready", "shipped"); err == nil {
		t.Fatal("未知目标状态必须被拒")
	}
	if err := state.AssertTaskTransition("archived", "ready"); err == nil {
		t.Fatal("未知来源状态必须被拒")
	}
}

func TestActiveOwnerSet(t *testing.T) {
	want := map[string]bool{
		"claimed": true, "in_progress": true, "self_check": true,
		"in_review": true, "merging": true, "conflict": true,
	}
	got := state.ActiveOwnerSet()
	if len(got) != len(want) {
		t.Fatalf("占用态应为 %d 个，实际 %v", len(want), got)
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("%s 应计入占用", k)
		}
	}
	if got["done"] || got["blocked"] {
		t.Fatal("done/blocked 不占用写盘序位")
	}
	// 返回副本：调用方改不到内部规则。
	got["done"] = true
	if state.IsActiveOwner("done") {
		t.Fatal("外部 map 篡改不得影响判定")
	}
}

func TestStateVocabularyMatchesModel(t *testing.T) {
	for _, s := range model.AllTaskStatuses() {
		if !state.IsActiveOwner(string(s)) && s.IsActiveOwner() {
			t.Fatalf("%s 的占用判定在 state 与 model 之间不一致", s)
		}
	}
}
