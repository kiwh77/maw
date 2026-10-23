package dispatch

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// openTestStore 在临时目录建库并迁移，返回 Store 句柄。
func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := db.OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

// seedDispatchOperation 插入一个 run 和一个归属它的 dispatch 操作。
func seedDispatchOperation(t *testing.T, store *db.Store, runID model.RunID, opID model.OperationID, instance model.InstanceID, generation int) {
	t.Helper()
	now := db.UTCNow()
	err := store.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO runs(run_id, project_id, instance_id, version_no, generation,
    phase, reconciliation_state, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, 'gate', 'idle', ?, ?)`,
			string(runID), "0123456789abcdef", string(instance), 1, generation, now, now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO operations(operation_id, project_id, kind, run_id, instance_id,
    idempotency_key, request_digest, result, created_at, updated_at)
VALUES(?, ?, 'dispatch', ?, ?, ?, ?, 'pending', ?, ?)`,
			string(opID), "0123456789abcdef", string(runID), string(instance), "idem-"+string(opID),
			"digest-"+string(opID), now, now)
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestDBQueuePendingClaimsAndCompletes(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	seedDispatchOperation(t, store, "run_a001", "op_a001", "ti_abcdef", 1)
	seedDispatchOperation(t, store, "run_b002", "op_b002", "ti_abcdef", 2)

	q := NewDBQueue(store)
	ctx := context.Background()

	pending, err := q.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("期望 2 个待办，实际 %d", len(pending))
	}
	if pending[0].OperationID != "op_a001" || pending[1].OperationID != "op_b002" {
		t.Fatalf("待办未按创建顺序排序: %v", pending)
	}
	if err := pending[0].Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	ok, err := q.Claim(ctx, "op_a001", "coordinator-1", 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}
	// 第二个协调者拿不到同一操作。
	ok, err = q.Claim(ctx, "op_a001", "coordinator-2", 30*time.Second)
	if err != nil || ok {
		t.Fatalf("重复领取应失败: ok=%v err=%v", ok, err)
	}
	// 错误协调者不能写结果。
	if err := q.Complete(ctx, "op_a001", "coordinator-2", model.ResultApplied); err == nil {
		t.Fatal("非当前协调者完成应报错")
	}
	// 当前协调者写 applied。
	if err := q.Complete(ctx, "op_a001", "coordinator-1", model.ResultApplied); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// 完成后的操作不再出现在待办里。
	pending, err = q.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].OperationID != "op_b002" {
		t.Fatalf("待办剔除失败: %+v", pending)
	}
}

func TestDBQueueRejectPending(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	seedDispatchOperation(t, store, "run_a001", "op_a001", "ti_abcdef", 1)
	q := NewDBQueue(store)
	ctx := context.Background()

	// 无人持有可撤回。
	ok, err := q.RejectPending(ctx, "op_a001")
	if err != nil || !ok {
		t.Fatalf("RejectPending: ok=%v err=%v", ok, err)
	}
	// 已撤回后不能再撤回。
	ok, err = q.RejectPending(ctx, "op_a001")
	if err != nil || ok {
		t.Fatalf("重复撤回应返回 false: ok=%v err=%v", ok, err)
	}
	// 被持有期间不能撤回。
	seedDispatchOperation(t, store, "run_b002", "op_b002", "ti_abcdef", 2)
	if _, err := q.Claim(ctx, "op_b002", "coordinator-1", 30*time.Second); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	ok, err = q.RejectPending(ctx, "op_b002")
	if err != nil {
		t.Fatalf("RejectPending b002: %v", err)
	}
	if ok {
		t.Fatal("持有中的操作不应被撤回")
	}
}

func TestDBQueuePendingCounts(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	seedDispatchOperation(t, store, "run_a001", "op_a001", "ti_abcdef", 1)
	q := NewDBQueue(store)
	ctx := context.Background()
	n := int64(0)
	for {
		ops, err := q.Pending(ctx)
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if len(ops) == 0 {
			break
		}
		op := ops[0]
		if _, err := q.Claim(ctx, op.OperationID, "coordinator-1", time.Second); err != nil {
			t.Fatalf("Claim %s: %v", op.OperationID, err)
		}
		if err := q.Complete(ctx, op.OperationID, "coordinator-1", model.ResultApplied); err != nil {
			t.Fatalf("Complete %s: %v", op.OperationID, err)
		}
		n++
		if n > 1000 {
			t.Fatal("可能出现死循环")
		}
	}
	if n != 1 {
		t.Fatalf("应消费 1 个操作，实际 %d", n)
	}
}
