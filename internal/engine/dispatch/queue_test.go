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
// 可选传入工程 ID，供多工程隔离用例各自播种；不传时沿用默认的单工程夹具。
func seedDispatchOperation(t *testing.T, store *db.Store, runID model.RunID, opID model.OperationID, instance model.InstanceID, generation int, projectID ...model.ProjectID) {
	t.Helper()
	project := model.ProjectID("0123456789abcdef")
	if len(projectID) > 0 {
		project = projectID[0]
	}
	now := db.UTCNow()
	err := store.Write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO runs(run_id, project_id, instance_id, version_no, generation,
    phase, reconciliation_state, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, 'gate', 'idle', ?, ?)`,
			string(runID), string(project), string(instance), 1, generation, now, now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO operations(operation_id, project_id, kind, run_id, instance_id,
    idempotency_key, request_digest, result, created_at, updated_at)
VALUES(?, ?, 'dispatch', ?, ?, ?, ?, 'pending', ?, ?)`,
			string(opID), string(project), string(runID), string(instance), "idem-"+string(opID),
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

const (
	testProjectA = model.ProjectID("0123456789abcdef")
	testProjectB = model.ProjectID("fedcba9876543210")
)

// seedTwoProjects 在全局库里各放一条属于不同工程的 pending dispatch 操作。
func seedTwoProjects(t *testing.T, store *db.Store) {
	t.Helper()
	seedDispatchOperation(t, store, "run_a001", "op_a001", "ti_aaaaaa", 1, testProjectA)
	seedDispatchOperation(t, store, "run_b002", "op_b002", "ti_bbbbbb", 2, testProjectB)
}

func TestDBQueuePendingScopedByProject(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	seedTwoProjects(t, store)
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		queue   *DBQueue
		wantIDs []model.OperationID
	}{
		{"工程 A", NewDBQueue(store, testProjectA), []model.OperationID{"op_a001"}},
		{"工程 B", NewDBQueue(store, testProjectB), []model.OperationID{"op_b002"}},
		// 不指定工程：与引入隔离前一致，仍是全库视图。
		{"未指定工程", NewDBQueue(store), []model.OperationID{"op_a001", "op_b002"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops, err := tc.queue.Pending(ctx)
			if err != nil {
				t.Fatalf("Pending: %v", err)
			}
			if len(ops) != len(tc.wantIDs) {
				t.Fatalf("待办数期望 %d，实际 %d: %+v", len(tc.wantIDs), len(ops), ops)
			}
			for i, want := range tc.wantIDs {
				if ops[i].OperationID != want {
					t.Fatalf("第 %d 条期望 %s，实际 %+v", i, want, ops[i])
				}
			}
		})
	}
}

func TestDBQueueClaimScopedByProject(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	seedTwoProjects(t, store)
	ctx := context.Background()
	qa := NewDBQueue(store, testProjectA)
	qb := NewDBQueue(store, testProjectB)

	// A 的协调者不能领走 B 排队的操作。
	ok, err := qa.Claim(ctx, "op_b002", "coord-a", 30*time.Second)
	if err != nil || ok {
		t.Fatalf("跨工程领取应失败: ok=%v err=%v", ok, err)
	}
	// A 的操作仍可被 A 领取。
	ok, err = qa.Claim(ctx, "op_a001", "coord-a", 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("本工程领取应成功: ok=%v err=%v", ok, err)
	}
	// B 的操作没有被 A 误占，B 的协调者仍能领走。
	ok, err = qb.Claim(ctx, "op_b002", "coord-b", 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("B 领取自己的操作应成功: ok=%v err=%v", ok, err)
	}
}

func TestDBQueueCompleteAndRejectScopedByProject(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	seedTwoProjects(t, store)
	ctx := context.Background()
	qa := NewDBQueue(store, testProjectA)
	qb := NewDBQueue(store, testProjectB)

	// A 不能替 B 写终态。
	if err := qa.Complete(ctx, "op_b002", "coord-a", model.ResultApplied); err == nil {
		t.Fatal("跨工程完成应被拒绝")
	}
	// A 不能撤回 B 排队的操作。
	ok, err := qa.RejectPending(ctx, "op_b002")
	if err != nil || ok {
		t.Fatalf("跨工程撤回应返回 false: ok=%v err=%v", ok, err)
	}
	// B 的操作仍完好，且可由 B 自己撤回。
	ops, err := qb.Pending(ctx)
	if err != nil || len(ops) != 1 || ops[0].OperationID != "op_b002" {
		t.Fatalf("B 的待办被误改: %+v err=%v", ops, err)
	}
	ok, err = qb.RejectPending(ctx, "op_b002")
	if err != nil || !ok {
		t.Fatalf("B 撤回自己的操作应成功: ok=%v err=%v", ok, err)
	}
}

func TestDBQueueRejectsMalformedProjectScope(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	seedTwoProjects(t, store)
	q := NewDBQueue(store, model.ProjectID("not-a-project-id"))
	if _, err := q.Pending(context.Background()); err == nil {
		t.Fatal("非法工程 ID 应显式报错，不能静默产出空队列")
	}
	if _, err := q.Claim(context.Background(), "op_a001", "coord-a", time.Second); err == nil {
		t.Fatal("非法工程 ID 下 Claim 应报错")
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
