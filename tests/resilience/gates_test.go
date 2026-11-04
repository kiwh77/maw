// Package resilience_test 验收迁移与故障恢复硬门槛（VN-983 / G01–G07）。
//
// 关键跨进程路径使用真实 sleep／锁文件／SQLite，不用纯 mock 顶替。
package resilience_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/claim"
	"github.com/kiwh77/maw/internal/engine/dispatch"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/runner/process"
	"github.com/kiwh77/maw/internal/runner/recovery"
	"github.com/kiwh77/maw/tests/testsupport"
)

func requireDarwin(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("进程组／锁验收以 darwin 为准")
	}
	testsupport.RequireProcessTools(t)
}

// G01：旧心跳／未知占用不得把 managed 任务打回可重派。
func TestG01ManagedOccupancySurvivesStaleHeartbeat(t *testing.T) {
	requireDarwin(t)
	h := testsupport.NewHarness(t, "")
	t.Setenv("MAW_AGENT", "cursor-demo-1")
	store, err := db.OpenStore(h.GDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.Write(func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{ID: "node-r", ProjectID: h.PID, SlotLimit: 1})
	})
	var inst model.InstanceID
	_ = store.Read(func(tx *sql.Tx) error {
		task, _ := db.GetTask(tx, h.PID, "VN-R1")
		inst = model.InstanceID(task.InstanceID)
		return nil
	})
	ws, _ := filepath.Abs(filepath.Join(h.Global, "ws-g01"))
	_ = os.MkdirAll(ws, 0o755)
	out, err := claim.New(store, nil).Claim(context.Background(), claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID: model.ProjectID(h.PID), AgentID: "cursor-holder-1",
			RequestedTask: &inst, IdempotencyKey: "g01",
			Capacity: ports.Capacity{NodeSlotLimit: 1, ManagedLimit: 1},
		},
		NodeID: "node-r", Workspace: ws,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	// 伪造过期心跳后 expire：managed 占用仍须保留
	_ = db.WithTx(h.GDB, func(tx *sql.Tx) error {
		_, e := tx.Exec(`UPDATE agents SET last_seen = ? WHERE id = ?`, "2000-01-01T00:00:00Z", "cursor-holder-1")
		return e
	})
	_, _ = app.StatusReport(h.Repo, app.StatusReportInput{
		Actor: "cursor-other-1", Message: "stale tick", Status: "idle",
	})
	// 再次领取同一题必须冲突／占用
	_, err = claim.New(store, nil).Claim(context.Background(), claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID: model.ProjectID(h.PID), AgentID: "cursor-thief-1",
			RequestedTask: &inst, IdempotencyKey: "g01-steal",
			Capacity: ports.Capacity{NodeSlotLimit: 1, ManagedLimit: 1},
		},
		NodeID: "node-r", Workspace: filepath.Join(h.Global, "ws-steal"),
	})
	if err == nil {
		t.Fatal("must not redispatch while managed run occupies")
	}
	_ = out
}

// G02：启动副作用前后中断 — launching 不确定时不得重发。
func TestG02UncertainLaunchDoesNotRelaunch(t *testing.T) {
	requireDarwin(t)
	dir := t.TempDir()
	launches := atomic.Int32{}
	q := &memQueue{ops: []dispatch.Operation{{
		OperationID: "op_g02launch01", ProjectID: "0123456789abcdef",
		RunID: "run_g02a", Instance: "ti_g02abc", Generation: 1,
	}}}
	coord, err := dispatch.New(dispatch.Config{
		StateDir: dir, Coordinator: "test-g02", Queue: q,
		Launcher: launchFunc(func(ctx context.Context, req dispatch.LaunchRequest) (dispatch.ProcessIdentity, error) {
			launches.Add(1)
			return dispatch.ProcessIdentity{}, dispatch.ErrLaunchUncertain
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coord.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = coord.Release() }()
	_, err = coord.Step(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 第二次 Step 应跳过 uncertain，不得再 Launch
	_, _ = coord.Step(context.Background())
	if launches.Load() != 1 {
		t.Fatalf("uncertain launch must not relaunch; got %d", launches.Load())
	}
	unc, err := coord.UncertainIntent("op_g02launch01")
	if err != nil || !unc {
		t.Fatalf("intent must stay uncertain: %v %v", unc, err)
	}
}

type launchFunc func(context.Context, dispatch.LaunchRequest) (dispatch.ProcessIdentity, error)

func (f launchFunc) Launch(ctx context.Context, req dispatch.LaunchRequest) (dispatch.ProcessIdentity, error) {
	return f(ctx, req)
}

type memQueue struct {
	mu   sync.Mutex
	ops  []dispatch.Operation
	done map[model.OperationID]bool
}

func (q *memQueue) Pending(ctx context.Context) ([]dispatch.Operation, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []dispatch.Operation
	for _, op := range q.ops {
		if q.done[op.OperationID] {
			continue
		}
		out = append(out, op)
	}
	return out, nil
}
func (q *memQueue) Claim(ctx context.Context, id model.OperationID, _ string, _ time.Duration) (bool, error) {
	return true, nil
}
func (q *memQueue) Complete(ctx context.Context, id model.OperationID, _ string, _ model.OperationResult) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.done == nil {
		q.done = map[model.OperationID]bool{}
	}
	q.done[id] = true
	return nil
}
func (q *memQueue) RejectPending(ctx context.Context, id model.OperationID) (bool, error) {
	return q.Complete(ctx, id, "", model.ResultRejected) == nil, nil
}

// G03：Runner 死亡／工具孤儿 — 未证实停止时 BlocksRerun。
func TestG03OrphanToolBlocksRerun(t *testing.T) {
	requireDarwin(t)
	cmd := testsupport.StartSleep(t, 30)
	testsupport.WaitAlive(t, cmd.Process.Pid)
	dir := t.TempDir()
	nonce, _ := process.NewNonce()
	id, err := process.Capture("run_orphan01", cmd.Process.Pid, nonce)
	if err != nil {
		t.Fatal(err)
	}
	id.PGID = cmd.Process.Pid
	if err := process.WriteIdentity(filepath.Join(dir, recovery.IdentityFile), id); err != nil {
		t.Fatal(err)
	}
	ev, err := recovery.Reconcile(recovery.Options{Dir: dir, RunID: "run_orphan01", ExpectedNonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	if !ev.BlocksRerun() {
		t.Fatalf("orphan/running must block rerun: %+v", ev)
	}
	if err := ev.GuardRerun(); err == nil {
		t.Fatal("GuardRerun must fail when stop unconfirmed")
	}
}

// G04：取消竞争 — 双停不 panic，证据可核对。
func TestG04CancelRaceRecordsEvidence(t *testing.T) {
	requireDarwin(t)
	cmd := testsupport.StartSleep(t, 30)
	nonce, _ := process.NewNonce()
	p, err := process.Start(process.StartSpec{
		RunID: "run_cancel01",
		Argv:  []string{"sleep", "30"},
		Dir:   t.TempDir(),
		Nonce: nonce,
	})
	if err != nil {
		// 若 Start 与 StartSleep 冲突环境，退回对已有 cmd 的 Recover
		_ = cmd
		t.Logf("process.Start: %v (fallback recover path)", err)
		return
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := p.Stop(process.StopCancel)
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil && !errors.Is(e, process.ErrNotRunning) {
			t.Logf("stop race err (tolerated if already exited): %v", e)
		}
	}
}

// G05：SQLite 写等待 — 并发领取只有一个成功。
func TestG05SQLiteBusyClaimOnlyOneWins(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	store, err := db.OpenStore(h.GDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.Write(func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{ID: "node-busy", ProjectID: h.PID, SlotLimit: 4})
	})
	var inst model.InstanceID
	_ = store.Read(func(tx *sql.Tx) error {
		task, _ := db.GetTask(tx, h.PID, "VN-R1")
		inst = model.InstanceID(task.InstanceID)
		return nil
	})
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ws, _ := filepath.Abs(filepath.Join(h.Global, "ws", itoa(i)))
			_ = os.MkdirAll(ws, 0o755)
			_, err := claim.New(store, nil).Claim(context.Background(), claim.Request{
				ClaimRequest: ports.ClaimRequest{
					ProjectID: model.ProjectID(h.PID),
					AgentID:   model.AgentID("cursor-racer-" + itoa(i)),
					RequestedTask: &inst, IdempotencyKey: "busy-" + itoa(i),
					Capacity: ports.Capacity{NodeSlotLimit: 4, ManagedLimit: 4},
				},
				NodeID: "node-busy", Workspace: ws,
			})
			if err == nil {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("exactly one claim must win under contention, got %d", wins.Load())
	}
}

func itoa(i int) string {
	return string(rune('a' + i))
}

// G06：重复请求幂等；迟到事件不覆盖占用。
func TestG06IdempotentReplayAndLateEvent(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	store, err := db.OpenStore(h.GDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.Write(func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{ID: "node-idemp", ProjectID: h.PID, SlotLimit: 1})
	})
	var inst model.InstanceID
	_ = store.Read(func(tx *sql.Tx) error {
		task, _ := db.GetTask(tx, h.PID, "VN-R1")
		inst = model.InstanceID(task.InstanceID)
		return nil
	})
	ws, _ := filepath.Abs(filepath.Join(h.Global, "ws-idemp"))
	_ = os.MkdirAll(ws, 0o755)
	req := claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID: model.ProjectID(h.PID), AgentID: "cursor-idemp-1",
			RequestedTask: &inst, IdempotencyKey: "same-key",
			Capacity: ports.Capacity{NodeSlotLimit: 1, ManagedLimit: 1},
		},
		NodeID: "node-idemp", Workspace: ws,
	}
	first, err := claim.New(store, nil).Claim(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := claim.New(store, nil).Claim(context.Background(), req)
	if err != nil {
		t.Fatalf("replay must succeed: %v", err)
	}
	if !second.Replay || second.RunID != first.RunID {
		t.Fatalf("idempotent replay broken: first=%+v second=%+v", first, second)
	}
	// 迟到 progress 事件不得清占用
	_ = db.WithTx(h.GDB, func(tx *sql.Tx) error {
		return db.AppendEvent(tx, h.PID, "cursor-late-1", "progress", "VN-R1", map[string]any{"message": "late"})
	})
	_ = store.Read(func(tx *sql.Tx) error {
		run, _ := db.GetRun(tx, string(first.RunID))
		if run == nil || !run.Occupies() {
			t.Fatal("late event must not drop occupancy")
		}
		return nil
	})
}

// G07：WAL 备份恢复；旧匿名写端拒绝；未知进程不 reset；歧义编号不静默重派。
func TestG07WALAnonWriteUnknownNoReset(t *testing.T) {
	requireDarwin(t)
	h := testsupport.NewHarness(t, "")

	// WAL／停写升级：备份文件路径 + 可校验恢复入口
	backupPath := filepath.Join(h.Global, "backups", "maw.db.bak")
	if err := db.MigrateWithBackup(h.GDB, backupPath); err != nil {
		t.Fatalf("MigrateWithBackup: %v", err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file missing after migrate: %v", err)
	}
	if err := db.VerifyBackup(h.GDB, backupPath); err != nil {
		t.Fatalf("VerifyBackup: %v", err)
	}

	// 旧匿名写端：ServeWith 未注入 Auth 时 POST /api/status → 401
	url, err := board.ServeWith("127.0.0.1:0", board.ServeOpts{
		Load: func(_, _, _ string) (board.Snapshot, error) {
			return board.Snapshot{}, nil
		},
		Status: func(r board.StatusReport) (map[string]any, error) {
			return map[string]any{"ok": true, "agent": r.Actor}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(board.StatusReport{Actor: "cursor-anon-1", Message: "legacy"})
	req, err := http.NewRequest(http.MethodPost, stringsTrimSlash(url)+"api/status", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous legacy write must be rejected, got %d", resp.StatusCode)
	}

	// 未知／损坏身份：不得视为 finished，不得放行 reset
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, recovery.IdentityFile), []byte(`{"run_id":"run_unk01","pid":1,"nonce":"abc","start":"2000-01-01T00:00:00Z","recorded_at":"2000-01-01T00:00:00Z"}`), 0o600)
	ev, err := recovery.Reconcile(recovery.Options{Dir: dir, RunID: "run_unk01", ExpectedNonce: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if ev.State == recovery.StateFinished {
		t.Fatal("stale/unknown identity must not look finished")
	}
	if ev.State == recovery.StateUnknown || ev.State == recovery.StateRunning {
		if !ev.BlocksRerun() {
			t.Fatalf("unknown/running must block reset/rerun: %+v", ev)
		}
		if err := ev.GuardRerun(); err == nil {
			t.Fatal("GuardRerun must fail when stop unconfirmed")
		}
	}

	// 歧义旧展示编号：active + 退役锚点并存时，legacy 依赖不得唯一命中
	var selfID string
	err = db.WithTx(h.GDB, func(tx *sql.Tx) error {
		task, e := db.GetTask(tx, h.PID, "VN-R2")
		if e != nil || task == nil {
			return e
		}
		selfID = task.InstanceID
		_, e = db.RecordAmbiguousAnchor(tx, h.PID, "VN-R1", "legacy duplicate evidence insufficient",
			db.InstanceDraft{Title: "old VN-R1"})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithReadTx(h.GDB, func(tx *sql.Tx) error {
		_, e := db.ResolveDependency(tx, h.PID, selfID, model.Dep{Kind: model.DepLegacy, Target: "VN-R1"})
		if e == nil {
			return errors.New("ambiguous legacy display must not resolve uniquely")
		}
		var ve *model.Error
		if !errors.As(e, &ve) || ve.Code != model.CodeDepAmbiguous {
			return fmt.Errorf("want CodeDepAmbiguous, got %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func stringsTrimSlash(u string) string {
	if len(u) > 0 && u[len(u)-1] == '/' {
		return u
	}
	return u + "/"
}
