package dispatch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

func testIntent(id model.OperationID) Intent {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	return Intent{
		OperationID:   id,
		ProjectID:     "0123456789abcdef",
		RunID:         "run_a001",
		Instance:      "ti_abcdef",
		Generation:    1,
		RequestDigest: "digest-1",
		Progress:      ProgressQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
		Transitions:   []Progress{ProgressQueued},
	}
}

func TestFileStoreRoundTripAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(dir)
	intent := testIntent("op_a001")
	if err := store.Save(&intent); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("op_a001")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil || loaded.OperationID != intent.OperationID || loaded.Progress != ProgressQueued {
		t.Fatalf("读取结果不符合预期: %+v", loaded)
	}

	// 覆盖写：新进度必须完整替换旧文件。
	loaded.Progress = ProgressPreparing
	loaded.Transitions = append(loaded.Transitions, ProgressPreparing)
	if err := store.Save(loaded); err != nil {
		t.Fatalf("Save 覆盖: %v", err)
	}
	again, err := store.Load("op_a001")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if again.Progress != ProgressPreparing || len(again.Transitions) != 2 {
		t.Fatalf("覆盖写结果不符合预期: %+v", again)
	}

	items, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].OperationID != "op_a001" {
		t.Fatalf("List 结果不符合预期: %+v", items)
	}

	// 临时文件不能残留。
	entries, err := os.ReadDir(store.Dir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".json" {
		t.Fatalf("意图目录存在残留文件: %v", entries)
	}
}

func TestFileStoreMissingReturnsNil(t *testing.T) {
	store := NewFileStore(t.TempDir())
	intent, err := store.Load("op_none")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if intent != nil {
		t.Fatalf("不存在的意图应返回 nil，实际 %+v", intent)
	}
	items, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("空目录应返回空列表，实际 %+v", items)
	}
}

func TestFileStoreRejectsInvalidIntent(t *testing.T) {
	store := NewFileStore(t.TempDir())
	intent := testIntent("op_a001")
	intent.Progress = ProgressLaunching // 缺 nonce
	if err := store.Save(&intent); err == nil {
		t.Fatal("launching 缺 nonce 应被拒绝")
	}

	bad := testIntent("op_a001")
	bad.OperationID = "../escape"
	if err := store.Save(&bad); err == nil {
		t.Fatal("非法 operation_id 应被拒绝")
	}
}

func TestIntentStateMachine(t *testing.T) {
	cases := []struct {
		from, to Progress
		ok       bool
	}{
		{ProgressQueued, ProgressPreparing, true},
		{ProgressQueued, ProgressWithdrawn, true},
		{ProgressQueued, ProgressLaunching, false},
		{ProgressPreparing, ProgressLaunching, true},
		{ProgressPreparing, ProgressFailed, true},
		{ProgressPreparing, ProgressWithdrawn, true},
		{ProgressLaunching, ProgressConfirmed, true},
		{ProgressLaunching, ProgressWithdrawn, false},
		{ProgressConfirmed, ProgressFailed, false},
		{ProgressFailed, ProgressQueued, false},
		{ProgressWithdrawn, ProgressPreparing, false},
	}
	for _, tc := range cases {
		if got := CanTransition(tc.from, tc.to); got != tc.ok {
			t.Fatalf("CanTransition(%s, %s)=%v，期望 %v", tc.from, tc.to, got, tc.ok)
		}
	}
}

func TestWithdrawUnknownOperationIsTooLate(t *testing.T) {
	clock := newFakeClock()
	q := newFakeQueue(clock.Now)
	c := newTestCoordinator(t, Config{Queue: q, Launcher: &fakeLauncher{}}, clock)
	intent, err := c.Withdraw(nil, "op_z999", "不存在")
	if !errors.Is(err, ErrTooLate) {
		t.Fatalf("期望 ErrTooLate，实际 %v", err)
	}
	_ = intent
}
