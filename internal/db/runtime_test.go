package db

import (
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// seedClaim 准备一个可领取的实例与节点，返回实例 id。
func seedClaim(t *testing.T, path string) string {
	t.Helper()
	var instanceID string
	mustWithTx(t, path, func(tx *sql.Tx) error {
		if err := UpsertNode(tx, &Node{ID: "n1", SlotLimit: 4}); err != nil {
			return err
		}
		inst, err := CreateInstance(tx, testProject, "VN-600", InstanceDraft{Title: "领取"})
		if err != nil {
			return err
		}
		instanceID = string(inst.InstanceID)
		return nil
	})
	return instanceID
}

func claimSpec(instanceID, key, workspace string, generation model.Generation) ClaimSpec {
	return ClaimSpec{
		ProjectID:      testProject,
		InstanceID:     instanceID,
		Generation:     generation,
		NodeID:         "n1",
		Workspace:      workspace,
		Coordinator:    "coordinator-1",
		IdempotencyKey: key,
	}
}

func counts(t *testing.T, path string) (runs, ops, leases, events int) {
	t.Helper()
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		for _, q := range []struct {
			sql  string
			dest *int
		}{
			{`SELECT COUNT(*) FROM runs`, &runs},
			{`SELECT COUNT(*) FROM operations`, &ops},
			{`SELECT COUNT(*) FROM resource_leases`, &leases},
			{`SELECT COUNT(*) FROM run_events`, &events},
		} {
			if err := tx.QueryRow(q.sql).Scan(q.dest); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return
}

func TestClaimRunIsAtomicAndIdempotent(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	instanceID := seedClaim(t, path)

	var first *ClaimOutcome
	mustWithTx(t, path, func(tx *sql.Tx) error {
		out, err := ClaimRun(tx, claimSpec(instanceID, "key-1", "/tmp/ws/claim", 1))
		if err != nil {
			return err
		}
		first = out
		return nil
	})
	if first.Cursor != 1 {
		t.Fatalf("首条事件游标应为 1，得到 %d", first.Cursor)
	}
	if first.Run.Phase != model.PhaseReserved || first.Run.Generation != 1 || len(first.Leases) != 2 {
		t.Fatalf("领取结果不对: run=%+v leases=%+v", first.Run, first.Leases)
	}
	if first.Operation.Kind != model.OpDispatch || first.Operation.Result != model.ResultPending {
		t.Fatalf("派发操作不对: %+v", first.Operation)
	}
	runs, ops, leases, events := counts(t, path)
	if runs != 1 || ops != 1 || leases != 2 || events != 1 {
		t.Fatalf("runs=%d ops=%d leases=%d events=%d", runs, ops, leases, events)
	}

	// 同键同摘要：返回原结果，不新写任何行。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		out, err := ClaimRun(tx, claimSpec(instanceID, "key-1", "/tmp/ws/claim", 1))
		if err != nil {
			return err
		}
		if !out.Replay || out.Run.ID != first.Run.ID || out.Cursor != first.Cursor {
			t.Fatalf("重放应返回原结果: %+v", out)
		}
		return nil
	})
	if r2, o2, l2, e2 := counts(t, path); r2 != 1 || o2 != 1 || l2 != 2 || e2 != 1 {
		t.Fatalf("重放写入了新行: runs=%d ops=%d leases=%d events=%d", r2, o2, l2, e2)
	}

	// 同键不同内容：E252，不可重试。
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, err := ClaimRun(tx, claimSpec(instanceID, "key-1", "/tmp/ws/other", 1))
		return err
	}); !hasCode(err, model.CodeIdempotencyConflict) {
		t.Fatalf("同键不同摘要应报 E252，得到 %v", err)
	}
}

// TestClaimRollbackLeavesNoTrace 验证「任一步失败整笔回滚」：
// 资源已占用之后 createRun 撞唯一约束，前面占的槽与目录必须一起消失。
func TestClaimRollbackLeavesNoTrace(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	instanceID := seedClaim(t, path)

	// 先占一个不构成风险占用的 Run（终态且已确认停止），并把代际 2 预置成冲突行。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		return createRun(tx, &Run{
			ID: "run_pre", ProjectID: testProject, InstanceID: instanceID,
			VersionNo: 1, Generation: 2, Phase: model.PhaseCompleted,
			Reconciliation: model.ReconciliationConfirmed, DesiredAction: model.ActionNone,
			ProcessStopConfirmed: true, DeliveryFrozen: true,
			PhaseHistory: []model.PhaseEntry{{Phase: model.PhaseReserved, At: time.Now().UTC()}},
			Evidence:     []string{"done"}, CreatedAt: UTCNow(), UpdatedAt: UTCNow(),
		})
	})
	runsBefore, opsBefore, leasesBefore, eventsBefore := counts(t, path)

	if err := WithTx(path, func(tx *sql.Tx) error {
		_, err := ClaimRun(tx, claimSpec(instanceID, "key-gen2", "/tmp/ws/gen2", 2))
		return err
	}); err == nil {
		t.Fatal("代际冲突的领取应失败")
	}

	runs, ops, leases, events := counts(t, path)
	if runs != runsBefore || ops != opsBefore || leases != leasesBefore || events != eventsBefore {
		t.Fatalf("失败的领取留下了痕迹: runs %d→%d ops %d→%d leases %d→%d events %d→%d",
			runsBefore, runs, opsBefore, ops, leasesBefore, leases, eventsBefore, events)
	}
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		active, err := ActiveLease(tx, "ws:/tmp/ws/gen2")
		if err != nil {
			return err
		}
		if active != nil {
			t.Fatalf("回滚后目录仍被占用: %+v", active)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRiskyOccupancyBlocksNextGeneration(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	instanceID := seedClaim(t, path)

	var runID string
	mustWithTx(t, path, func(tx *sql.Tx) error {
		out, err := ClaimRun(tx, claimSpec(instanceID, "key-1", "/tmp/ws/risk", 1))
		if err != nil {
			return err
		}
		runID = out.Run.ID
		return nil
	})

	// 阶段先写事件，再改状态：failed 但进程停止未确认。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		if _, _, err := AppendRunEvent(tx, RunEventSpec{
			ID: "evt_failed_1", ProjectID: testProject, RunID: runID, InstanceID: instanceID,
			Kind: EventRunPhase, Phase: model.PhaseFailed, SchemaVersion: RunEventSchemaVersion,
		}); err != nil {
			return err
		}
		_, err := UpdateRunProgress(tx, runID, model.PhaseFailed, false, false, []string{"exit 1"})
		return err
	})

	if err := WithTx(path, func(tx *sql.Tx) error {
		_, err := ClaimRun(tx, claimSpec(instanceID, "key-gen2", "/tmp/ws/risk2", 2))
		return err
	}); !hasCode(err, model.CodeDoubleOccupancy) {
		t.Fatalf("失败未确认停止仍算占用，应报 E240，得到 %v", err)
	}

	// 确认进程已停后，新一代可以领取。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		if err := ConfirmProcessStop(tx, runID, "ps -p 1234 无进程"); err != nil {
			return err
		}
		return nil
	})
	var second *ClaimOutcome
	mustWithTx(t, path, func(tx *sql.Tx) error {
		out, err := ClaimRun(tx, claimSpec(instanceID, "key-gen2", "/tmp/ws/risk2", 2))
		if err != nil {
			return err
		}
		second = out
		return nil
	})
	if second.Run.Generation != 2 {
		t.Fatalf("第二代 Run 代际不对: %+v", second.Run)
	}

	// unknown 核实状态即使到了终态也继续占用（E232 的另一面）。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE runs SET phase = ?, reconciliation_state = ? WHERE run_id = ?`,
			string(model.PhaseCompleted), string(model.ReconciliationUnknown), second.Run.ID)
		return err
	})
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, err := ClaimRun(tx, claimSpec(instanceID, "key-gen3", "/tmp/ws/risk3", 3))
		return err
	}); !hasCode(err, model.CodeDoubleOccupancy) {
		t.Fatalf("unknown 状态仍应占用，得到 %v", err)
	}
}

// TestConcurrentClaimHasSingleWinner 用真实并发连接验证「同一实例只有一个有效占用」。
func TestConcurrentClaimHasSingleWinner(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	instanceID := seedClaim(t, path)

	const workers = 6
	var wins atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			key := fmt.Sprintf("key-%d", id)
			ws := fmt.Sprintf("/tmp/ws/race-%d", id)
			err := WithTx(path, func(tx *sql.Tx) error {
				_, err := ClaimRun(tx, claimSpec(instanceID, key, ws, 1))
				return err
			})
			if err == nil {
				wins.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("并发领取应只有 1 个成功，得到 %d", got)
	}
	runs, ops, leases, events := counts(t, path)
	if runs != 1 || ops != 1 || leases != 2 || events != 1 {
		t.Fatalf("并发失败方留了痕迹: runs=%d ops=%d leases=%d events=%d", runs, ops, leases, events)
	}
}

func TestOperationCoordinatorLease(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	var opID string
	mustWithTx(t, path, func(tx *sql.Tx) error {
		op, replay, err := CreateOperation(tx, OperationSpec{
			ProjectID: testProject, Kind: model.OpVerify, TargetPhase: model.PhaseWaiting,
			IdempotencyKey: "verify-1",
		})
		if err != nil {
			return err
		}
		if replay {
			t.Fatal("首次创建不该是重放")
		}
		opID = op.ID
		// 同键同摘要重放
		again, replay2, err := CreateOperation(tx, OperationSpec{
			ProjectID: testProject, Kind: model.OpVerify, TargetPhase: model.PhaseWaiting,
			IdempotencyKey: "verify-1",
		})
		if err != nil || !replay2 || again.ID != op.ID {
			t.Fatalf("同键同摘要应重放: %+v %v", again, err)
		}
		// 同键不同内容
		_, _, err = CreateOperation(tx, OperationSpec{
			ProjectID: testProject, Kind: model.OpVerify, TargetPhase: model.PhaseCompleted,
			IdempotencyKey: "verify-1",
		})
		if !hasCode(err, model.CodeIdempotencyConflict) {
			t.Fatalf("同键不同内容应报 E252，得到 %v", err)
		}
		return nil
	})

	// 并发：只有一个协调者拿到租约。
	var claimed atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			coordinator := fmt.Sprintf("coord-%d", id)
			err := WithTx(path, func(tx *sql.Tx) error {
				ok, err := ClaimOperation(tx, opID, coordinator, 2*time.Second)
				if err != nil {
					return err
				}
				if ok {
					claimed.Add(1)
				}
				return nil
			})
			if err != nil {
				t.Errorf("claim: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if got := claimed.Load(); got != 1 {
		t.Fatalf("同一操作应只有一个协调者，得到 %d", got)
	}

	var winner string
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		op, err := GetOperation(tx, opID)
		if err != nil {
			return err
		}
		winner = op.Coordinator
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 非持有者完成 → 拒绝；持有者完成 → 落结果。
	if err := WithTx(path, func(tx *sql.Tx) error {
		return CompleteOperation(tx, opID, "someone-else", model.ResultApplied)
	}); err == nil {
		t.Fatal("非协调者不得写结果")
	}
	mustWithTx(t, path, func(tx *sql.Tx) error {
		return CompleteOperation(tx, opID, winner, model.ResultApplied)
	})

	// 租约过期后可被接手：新建一个待办操作，用极短租约模拟协调者失联。
	var expiredID string
	mustWithTx(t, path, func(tx *sql.Tx) error {
		op, _, err := CreateOperation(tx, OperationSpec{
			ProjectID: testProject, Kind: model.OpCancel, IdempotencyKey: "cancel-1",
		})
		if err != nil {
			return err
		}
		expiredID = op.ID
		ok, err := ClaimOperation(tx, op.ID, "coord-dead", time.Millisecond)
		if err != nil || !ok {
			t.Fatalf("首次占用应成功: %v", err)
		}
		return nil
	})
	time.Sleep(20 * time.Millisecond)
	mustWithTx(t, path, func(tx *sql.Tx) error {
		ok, err := ClaimOperation(tx, expiredID, "coord-alive", time.Second)
		if err != nil {
			return err
		}
		if !ok {
			t.Fatal("租约过期后应可被接手")
		}
		return nil
	})
}

// TestEventCursorPaginationAndBackfillDedupe 用真实并发连接验证游标分页与补交去重。
func TestEventCursorPaginationAndBackfillDedupe(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	const workers = 6
	const uniquePer = 5
	const shared = 5

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			err := WithTx(path, func(tx *sql.Tx) error {
				for i := 0; i < shared; i++ {
					// 同一身份被所有工作者补交：只应入库一次。
					if _, _, err := AppendRunEvent(tx, RunEventSpec{
						ID:            model.EventID(fmt.Sprintf("evt_shared_%02d", i)),
						ProjectID:     testProject,
						Kind:          "run.log",
						SchemaVersion: RunEventSchemaVersion,
						Payload:       map[string]any{"source": id},
					}); err != nil {
						return err
					}
				}
				for i := 0; i < uniquePer; i++ {
					if _, inserted, err := AppendRunEvent(tx, RunEventSpec{
						ID:            model.EventID(fmt.Sprintf("evt_w%d_%02d", id, i)),
						ProjectID:     testProject,
						Kind:          "run.log",
						SchemaVersion: RunEventSchemaVersion,
					}); err != nil {
						return err
					} else if !inserted {
						t.Errorf("唯一事件被判为重复: w%d i%d", id, i)
					}
				}
				return nil
			})
			if err != nil {
				errs <- err
			}
		}(w)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	want := shared + workers*uniquePer
	var total int
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM run_events`).Scan(&total)
	}); err != nil {
		t.Fatal(err)
	}
	if total != want {
		t.Fatalf("事件去重后应为 %d 条，实为 %d", want, total)
	}

	// 游标分页：严格递增、不重不漏。
	if err := WithReadTx(path, func(tx *sql.Tx) error {
		var after model.Cursor
		var seen int
		var last model.Cursor
		for {
			page, err := ReadRunEvents(tx, testProject, after, 4)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			for _, ev := range page {
				if ev.Cursor <= last {
					t.Fatalf("游标未严格递增: %d 之后 %d", last, ev.Cursor)
				}
				last = ev.Cursor
				seen++
			}
			after = page[len(page)-1].Cursor
			if len(page) < 4 {
				break
			}
		}
		if seen != want {
			t.Fatalf("分页读到 %d 条，期望 %d", seen, want)
		}
		max, err := MaxRunCursor(tx, testProject)
		if err != nil {
			return err
		}
		if int(max) < seen {
			t.Fatalf("最大游标 %d 小于条数 %d", max, seen)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEventValidationAndReferences(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		spec RunEventSpec
		code model.Code
	}{
		{"缺版本", RunEventSpec{ID: "evt_x1", Kind: "run.log"}, model.CodeRuntimeFieldInvalid},
		{"缺身份", RunEventSpec{Kind: "run.log", SchemaVersion: 1}, model.CodeRuntimeFieldInvalid},
		{"挂到不存在的 Run", RunEventSpec{ID: "evt_x2", RunID: "run_missing", SchemaVersion: 1, Kind: "run.log"}, model.CodeEventUnknownRun},
		{"时钟倒置未降级", RunEventSpec{
			ID: "evt_x3", SchemaVersion: 1, Kind: "run.log",
			OccurredAt: time.Now().UTC().Add(time.Hour), ReceivedAt: time.Now().UTC(),
			Quality: model.QualityObserved,
		}, model.CodeClockSkewMarkedObserved},
	}
	for _, c := range cases {
		err := WithTx(path, func(tx *sql.Tx) error {
			_, _, err := AppendRunEvent(tx, c.spec)
			return err
		})
		if c.code == "" {
			if err != nil {
				t.Fatalf("%s: 期望成功，得到 %v", c.name, err)
			}
			continue
		}
		if !hasCode(err, c.code) {
			t.Fatalf("%s: 期望 %s，得到 %v", c.name, c.code, err)
		}
	}
}
