package machine

import (
	"testing"

	"github.com/kiwh77/maw/internal/engine/state"
)

func TestAssertTransitionRejectsSameStatus(t *testing.T) {
	if err := AssertTransition("done", "done"); err == nil {
		t.Fatal("done→done must fail")
	}
	if err := AssertTransition("ready", "ready"); err == nil {
		t.Fatal("ready→ready must fail")
	}
	if err := AssertTransition("blocked", "ready"); err != nil {
		t.Fatalf("blocked→ready should allow: %v", err)
	}
	if err := AssertTransition("merging", "done"); err != nil {
		t.Fatalf("merging→done should allow: %v", err)
	}
}

func TestPreserveOnPlanApply(t *testing.T) {
	keep := []string{"done", "cancelled", "blocked", "claimed", "in_progress", "self_check", "in_review", "merging", "conflict"}
	for _, s := range keep {
		if !PreserveOnPlanApply(s) {
			t.Fatalf("%s 应保留", s)
		}
	}
	drop := []string{"backlog", "ready", ""}
	for _, s := range drop {
		if PreserveOnPlanApply(s) {
			t.Fatalf("%s 不应保留（idle 可被蓝图替换/剔除）", s)
		}
	}
}

func TestTerminalStatusesCannotMove(t *testing.T) {
	for _, dst := range []string{"ready", "in_progress", "in_review", "merging", "done"} {
		if err := AssertTransition("done", dst); err == nil {
			t.Fatalf("done→%s 必须被拒（终态不再出发）", dst)
		}
		if err := AssertTransition("cancelled", dst); err == nil {
			t.Fatalf("cancelled→%s 必须被拒（终态不再出发）", dst)
		}
	}
}

func TestBlockedUnblocksToAnyStatus(t *testing.T) {
	// 解阻塞走 PrevStatus 恢复，因此 blocked 的出边不设限。
	for _, dst := range state.TaskStatuses() {
		if err := AssertTransition("blocked", dst); err != nil {
			t.Fatalf("blocked→%s 应允许恢复：%v", dst, err)
		}
	}
}

func TestActiveOwnerIsSingleSourced(t *testing.T) {
	want := state.ActiveOwnerSet()
	if len(ActiveOwner) != len(want) {
		t.Fatalf("ActiveOwner 与 state 规则不一致：%d vs %d", len(ActiveOwner), len(want))
	}
	for status, active := range want {
		if ActiveOwner[status] != active {
			t.Fatalf("%s 在 machine 与 state 中的判定不同", status)
		}
	}
}

func TestUnknownStatusRejected(t *testing.T) {
	if err := AssertTransition("ready", "shipped"); err == nil {
		t.Fatal("未知目标状态必须被拒")
	}
	if err := AssertTransition("archived", "ready"); err == nil {
		t.Fatal("未知来源状态必须被拒")
	}
}
