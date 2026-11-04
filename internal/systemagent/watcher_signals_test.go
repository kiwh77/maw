package systemagent

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

func newSignalStore(t *testing.T) *db.Store {
	t.Helper()
	st, err := db.OpenStore(filepath.Join(t.TempDir(), "maw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// signalFacts 组装一批同时命中四类信号的假证据。
func signalFacts(now time.Time) fakeFacts {
	return fakeFacts{
		projects: []string{"p1", "p2"},
		gates: []GateFailureFact{
			{ProjectID: "p1", TaskID: "T-1", Output: "fork/exec /bin/ps: operation not permitted"},
		},
		merging: []MergingTaskFact{{
			ProjectID: "p1", TaskID: "T-2",
			Since: now.Add(-2 * mergingStallThreshold), OpsCommand: "maw merge --yes",
		}},
		locks: []HeldLockFact{{
			ProjectID: "p1", ResourceKey: "locks/merge/p1",
			AcquiredAt: now.Add(-2 * staleLockAge), UnknownHolder: true,
		}},
	}
}

func listJobs(t *testing.T, st *db.Store) []*db.SystemJob {
	t.Helper()
	var out []*db.SystemJob
	if err := st.Read(func(tx *sql.Tx) error {
		var e error
		out, e = db.ListSystemJobs(tx, "", "", 100)
		return e
	}); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	return out
}

// TestEnqueueSignalsRespectsAutoKinds 覆盖硬要求：默认只放行 auto_kinds 内的 kind，
// 未放行不入队；stale_lock 只上报不产出 job。
func TestEnqueueSignalsRespectsAutoKinds(t *testing.T) {
	st := newSignalStore(t)
	now := time.Now().UTC()
	cfg := config.SystemAgent{
		AutoKinds:      []string{SystemKindUnblock},
		OpsAllowlist:   []string{"maw merge --yes"},
		CooldownoutSec: 300,
		MaxAttempts:    3,
	}
	signals := DetectSystemSignals(signalFacts(now), cfg, now)
	if len(signals) == 0 {
		t.Fatal("假证据应产出信号")
	}
	err := st.Write(func(tx *sql.Tx) error {
		_, e := EnqueueSignals(tx, cfg, signals, now)
		return e
	})
	if err != nil {
		t.Fatalf("enqueue signals: %v", err)
	}
	jobs := listJobs(t, st)
	if len(jobs) != 1 {
		t.Fatalf("默认只应入队 1 条 unblock_assist，实际 %d: %+v", len(jobs), jobs)
	}
	if jobs[0].Kind != SystemKindUnblock {
		t.Fatalf("kind=%q want %q", jobs[0].Kind, SystemKindUnblock)
	}
	if jobs[0].Decision["signal_kind"] != string(SignalGateEnvFailure) {
		t.Fatalf("缺 signal_kind: %v", jobs[0].Decision)
	}
	if jobs[0].Decision["action"] != string(ActionEscalateHuman) {
		t.Fatalf("needs_human 应走升级人工: %v", jobs[0].Decision)
	}
}

// TestEnqueueSignalsAllKindsAllowed 放行全部 kind 时：三类动作信号入队，
// stale_lock 仍不产出 job（且 offline 按工程各一条）。
func TestEnqueueSignalsAllKindsAllowed(t *testing.T) {
	st := newSignalStore(t)
	now := time.Now().UTC()
	cfg := config.SystemAgent{
		AutoKinds: []string{
			SystemKindUnblock, SystemKindDispatchReady, SystemKindOpsRun,
		},
		OpsAllowlist:   []string{"maw merge --yes"},
		CooldownoutSec: 300,
		MaxAttempts:    3,
	}
	signals := DetectSystemSignals(signalFacts(now), cfg, now)
	err := st.Write(func(tx *sql.Tx) error {
		_, e := EnqueueSignals(tx, cfg, signals, now)
		return e
	})
	if err != nil {
		t.Fatalf("enqueue signals: %v", err)
	}
	jobs := listJobs(t, st)
	// gate(1) + offline(p1,p2 各 1) + merging(1) = 4；stale_lock 不产出。
	if len(jobs) != 4 {
		t.Fatalf("应入队 4 条，实际 %d: %+v", len(jobs), jobs)
	}
	for _, j := range jobs {
		if j.Kind == "" || j.Decision["signal_kind"] == nil {
			t.Fatalf("作业缺信号元数据: %+v", j)
		}
		if j.Decision["signal_kind"] == string(SignalStaleLock) {
			t.Fatalf("stale_lock 不应产出 job: %+v", j)
		}
	}
}

// TestEnqueueSignalsCooldown 同一 signal key 在 cooldown 内不重复入队：
// 预置一条带相同 signal_key 的作业，用受控 created_at 分别覆盖冷却内 / 冷却外。
func TestEnqueueSignalsCooldown(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	signal := Signal{
		Kind:      SignalGateEnvFailure,
		ProjectID: "p1",
		TaskID:    "T-NEW",
		Reason:    "needs_human",
		JobKind:   SystemKindUnblock,
		Escalate:  true,
	}
	key := signal.Key()
	cfg := config.SystemAgent{
		AutoKinds:      []string{SystemKindUnblock},
		CooldownoutSec: 300,
		MaxAttempts:    3,
	}
	cases := []struct {
		name       string
		createdAt  time.Time
		wantQueued int
	}{
		{"冷却内不重复入队", now.Add(-time.Minute), 0},
		{"超出冷却窗口允许入队", now.Add(-10 * time.Minute), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newSignalStore(t)
			// 预置作业用不同 taskID，避免撞上 InsertSystemJob 的 UNIQUE(project,kind,task,trigger)，
			// 让「能否再入队」只由 signal key 冷却决定。
			if err := st.Write(func(tx *sql.Tx) error {
				_, e := db.InsertSystemJob(tx, &db.SystemJob{
					JobID:     "sjob_pre",
					ProjectID: "p1",
					Kind:      SystemKindUnblock,
					Status:    db.SystemJobDone,
					TaskID:    "T-OLD",
					Decision:  map[string]any{"signal_key": key},
					CreatedAt: tc.createdAt.Format(time.RFC3339),
				})
				return e
			}); err != nil {
				t.Fatalf("seed job: %v", err)
			}
			var queued []*db.SystemJob
			if err := st.Write(func(tx *sql.Tx) error {
				var e error
				queued, e = EnqueueSignals(tx, cfg, []Signal{signal}, now)
				return e
			}); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
			if len(queued) != tc.wantQueued {
				t.Fatalf("queued=%d want %d", len(queued), tc.wantQueued)
			}
		})
	}
}

// TestWatcherPollOnceIngestSignals 验证 watcher.go 的最小接入：注入 SignalFacts 后，
// PollOnce 会把信号经 Enqueue 入队，且再次轮询受冷却保护不重复入队。
func TestWatcherPollOnceIngestSignals(t *testing.T) {
	st := newSignalStore(t)
	now := time.Now().UTC()
	cfg := config.SystemAgent{
		Enabled:        true,
		AutoKinds:      []string{SystemKindUnblock},
		OpsAllowlist:   []string{"maw merge --yes"},
		CooldownoutSec: 300,
		MaxAttempts:    3,
	}
	w := &Watcher{
		Store:       st,
		Config:      cfg,
		SignalFacts: signalFacts(now),
	}
	ctx := context.Background()
	enqueued, ran, err := w.PollOnce(ctx)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if enqueued != 1 || ran != 0 {
		t.Fatalf("enqueued=%d ran=%d，期望只入队 1 条观察作业", enqueued, ran)
	}
	jobs := listJobs(t, st)
	if len(jobs) != 1 || jobs[0].Kind != SystemKindUnblock {
		t.Fatalf("落库作业不对: %+v", jobs)
	}

	// 再轮询一次：同 key 在冷却窗口内，不应重复入队。
	enqueued2, _, err := w.PollOnce(ctx)
	if err != nil {
		t.Fatalf("poll again: %v", err)
	}
	if enqueued2 != 0 {
		t.Fatalf("冷却期内不应重复入队，实际 %d", enqueued2)
	}
}

// TestWatcherWithoutSignalFactsUnchanged 未注入证据源时 PollOnce 不触碰信号路径。
func TestWatcherWithoutSignalFactsUnchanged(t *testing.T) {
	st := newSignalStore(t)
	w := &Watcher{
		Store:  st,
		Config: config.SystemAgent{Enabled: true, MaxAttempts: 3},
	}
	enqueued, ran, err := w.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if enqueued != 0 || ran != 0 {
		t.Fatalf("未注入 SignalFacts 应完全走既有路径: enqueued=%d ran=%d", enqueued, ran)
	}
	if jobs := listJobs(t, st); len(jobs) != 0 {
		t.Fatalf("不应产生任何作业: %+v", jobs)
	}
}
