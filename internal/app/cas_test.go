package app

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

// promote 走一次生产同款的状态推进（orchestrator 路径），返回错误而不吞掉。
func (f *fixture) promote() error {
	f.t.Helper()
	return db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return promoteReady(tx, f.pc.ID())
	})
}

// TestPromoteReadyCASRevisionMonotonic 覆盖成功路径：状态推进一次，revision 恰好 +1；
// 再推一次不重复加版本（没有变化就不写）。
func TestPromoteReadyCASRevisionMonotonic(t *testing.T) {
	f := newFixture(t)
	seeded := f.seedTask("T-A", "backlog", "", []string{"internal/a/**"})
	if seeded.Revision != 0 {
		t.Fatalf("新题 revision 应为 0，得到 %d", seeded.Revision)
	}

	if err := f.promote(); err != nil {
		t.Fatalf("promoteReady: %v", err)
	}
	got := f.task("T-A")
	if got.Status != "ready" {
		t.Fatalf("无依赖题应被放行为 ready，得到 %s", got.Status)
	}
	if got.Revision != 1 {
		t.Fatalf("推进后 revision 应恰好 +1（=%d），得到 %d", 1, got.Revision)
	}

	// 状态已是 ready 且没有变化：不得再写、也不得再加版本。
	if err := f.promote(); err != nil {
		t.Fatalf("二次 promoteReady: %v", err)
	}
	again := f.task("T-A")
	if again.Revision != 1 {
		t.Fatalf("无变化时不应前进 revision，得到 %d", again.Revision)
	}
}

// TestTaskCASMismatchInAppPath 覆盖冲突路径：拿着旧 revision 改归属必须失败，
// 且错误码稳定为 revision_mismatch、owner 不改。
func TestTaskCASMismatchInAppPath(t *testing.T) {
	f := newFixture(t)
	f.seedTask("T-B", "ready", "alice", []string{"internal/b/**"})

	// 先由别人合法改派一次：0 → 1。
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, e := db.UpdateTaskCAS(tx, f.pc.ID(), "T-B", 0, func(task *db.Task) error {
			task.Owner = "bob"
			return nil
		})
		return e
	}); err != nil {
		t.Fatalf("第一次改派: %v", err)
	}

	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, e := db.UpdateTaskCAS(tx, f.pc.ID(), "T-B", 0, func(task *db.Task) error {
			task.Owner = "carol"
			return nil
		})
		return e
	})
	if err == nil {
		t.Fatal("过期 revision 必须被拒绝")
	}
	if !db.IsRevisionMismatch(err) {
		t.Fatalf("错误应为 revision_mismatch，得到 %v", err)
	}
	var mm *db.RevisionMismatch
	if !errors.As(err, &mm) || mm.Code() != "revision_mismatch" || mm.Actual != 1 {
		t.Fatalf("冲突错误形状不对：%v", err)
	}

	got := f.task("T-B")
	if got.Owner != "bob" || got.Revision != 1 {
		t.Fatalf("冲突后 owner/revision 应保持 bob/1，得到 %q/%d", got.Owner, got.Revision)
	}
}
