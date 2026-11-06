package systemagent

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

// fakeFacts 是 SignalFacts 的假证据实现：所有判定逻辑都只吃这些切片，
// 不碰真实 DB、真实进程，也不需要任何平台跳过守卫。
type fakeFacts struct {
	projects []string
	gates    []GateFailureFact
	agents   []AgentHeartbeatFact
	merging  []MergingTaskFact
	locks    []HeldLockFact
	runs     []UnverifiedRunFact
}

func (f fakeFacts) Projects() []string                    { return f.projects }
func (f fakeFacts) RecentGateFailures() []GateFailureFact { return f.gates }
func (f fakeFacts) AgentHeartbeats() []AgentHeartbeatFact { return f.agents }
func (f fakeFacts) MergingTasks() []MergingTaskFact       { return f.merging }
func (f fakeFacts) HeldLocks() []HeldLockFact             { return f.locks }
func (f fakeFacts) UnverifiedRuns() []UnverifiedRunFact   { return f.runs }

var signalTestNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func findSignal(signals []Signal, kind SignalKind) *Signal {
	for i := range signals {
		if signals[i].Kind == kind {
			return &signals[i]
		}
	}
	return nil
}

func countSignal(signals []Signal, kind SignalKind) int {
	n := 0
	for _, s := range signals {
		if s.Kind == kind {
			n++
		}
	}
	return n
}

// TestDetectGateEnvFailure 覆盖验收：门禁环境失败识别 → unblock_assist + needs_human 升级。
func TestDetectGateEnvFailure(t *testing.T) {
	cases := []struct {
		name    string
		fact    GateFailureFact
		trigger bool
	}{
		{"沙箱 ps 被拦", GateFailureFact{Output: "fork/exec /bin/ps: operation not permitted"}, true},
		{"EPERM 大写", GateFailureFact{Output: "EPERM: operation not permitted"}, true},
		{"gitops 既有词表", GateFailureFact{Output: "command not found: foo"}, true},
		{"permission denied", GateFailureFact{Output: "open config: permission denied"}, true},
		{"事实源已判定环境失败", GateFailureFact{Output: "assertion failed", EnvFailure: true}, true},
		{"纯代码失败", GateFailureFact{Output: "assertion failed: expected 1 got 2"}, false},
		{"空输出", GateFailureFact{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fact.ProjectID = "p1"
			tc.fact.TaskID = "T-1"
			signals := DetectSystemSignals(fakeFacts{
				projects: []string{"p1"},
				gates:    []GateFailureFact{tc.fact},
			}, config.SystemAgent{}, signalTestNow)
			got := findSignal(signals, SignalGateEnvFailure)
			if tc.trigger && got == nil {
				t.Fatalf("应触发 gate_env_failure，实际 %+v", signals)
			}
			if !tc.trigger {
				if got != nil {
					t.Fatalf("不应触发 gate_env_failure: %+v", got)
				}
				return
			}
			if got.JobKind != SystemKindUnblock {
				t.Fatalf("JobKind=%q want %q", got.JobKind, SystemKindUnblock)
			}
			if !strings.Contains(got.Reason, "needs_human") {
				t.Fatalf("reason 缺 needs_human: %q", got.Reason)
			}
			if !got.Escalate || got.Decision["action"] != string(ActionEscalateHuman) {
				t.Fatalf("应走升级人工: Escalate=%v Decision=%v", got.Escalate, got.Decision)
			}
		})
	}
}

// TestDetectGateEnvFailureDeduplicates 同一 project+task 的多条失败只产出一条信号。
func TestDetectGateEnvFailureDeduplicates(t *testing.T) {
	signals := DetectSystemSignals(fakeFacts{
		projects: []string{"p1"},
		gates: []GateFailureFact{
			{ProjectID: "p1", TaskID: "T-1", Output: "/bin/ps: operation not permitted"},
			{ProjectID: "p1", TaskID: "T-1", Output: "permission denied"},
			{ProjectID: "p1", TaskID: "T-2", Output: "EPERM"},
		},
	}, config.SystemAgent{}, signalTestNow)
	if n := countSignal(signals, SignalGateEnvFailure); n != 2 {
		t.Fatalf("应按 project+task 去重为 2 条，实际 %d: %+v", n, signals)
	}
}

// TestDetectAllAgentsOffline 覆盖验收：工程内无在线 Agent 才产出 dispatch_ready 观察信号。
func TestDetectAllAgentsOffline(t *testing.T) {
	fresh := signalTestNow.Add(-1 * time.Minute)
	stale := signalTestNow.Add(-agentHeartbeatWindow - time.Minute)
	cases := []struct {
		name       string
		projects   []string
		agents     []AgentHeartbeatFact
		offlinePID string
	}{
		{
			name:     "全员心跳过期 + offline",
			projects: []string{"p1"},
			agents: []AgentHeartbeatFact{
				{ProjectID: "p1", AgentID: "a1", Status: "idle", LastSeen: stale},
				{ProjectID: "p1", AgentID: "a2", Status: "offline", LastSeen: fresh},
			},
			offlinePID: "p1",
		},
		{
			name:       "工程无任何 Agent 登记",
			projects:   []string{"p1"},
			offlinePID: "p1",
		},
		{
			name:     "有新鲜心跳则不触发",
			projects: []string{"p1"},
			agents: []AgentHeartbeatFact{
				{ProjectID: "p1", AgentID: "a1", Status: "idle", LastSeen: fresh},
			},
		},
		{
			name:     "其它工程有心跳不影响本工程判定",
			projects: []string{"p1", "p2"},
			agents: []AgentHeartbeatFact{
				{ProjectID: "p2", AgentID: "a1", Status: "idle", LastSeen: fresh},
			},
			offlinePID: "p1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signals := DetectSystemSignals(fakeFacts{
				projects: tc.projects,
				agents:   tc.agents,
			}, config.SystemAgent{}, signalTestNow)
			got := findSignal(signals, SignalAllAgentsOffline)
			if tc.offlinePID == "" {
				if got != nil {
					t.Fatalf("不应触发 all_agents_offline: %+v", got)
				}
				return
			}
			if got == nil || got.ProjectID != tc.offlinePID {
				t.Fatalf("应触发 %s 的 all_agents_offline，实际 %+v", tc.offlinePID, signals)
			}
			if got.JobKind != SystemKindDispatchReady {
				t.Fatalf("JobKind=%q want dispatch_ready", got.JobKind)
			}
			if got.Decision["observation"] != true {
				t.Fatalf("应标注仅观察: %v", got.Decision)
			}
			if got.Decision["action"] != string(ActionEscalateHuman) || !got.Escalate {
				t.Fatalf("应预置升级人工以代码层强制只观察: Decision=%v Escalate=%v", got.Decision, got.Escalate)
			}
		})
	}
}

// TestDetectAllAgentsOfflinePerProject 两个工程同时离线各产出一条，Key 不互相冷却。
func TestDetectAllAgentsOfflinePerProject(t *testing.T) {
	signals := DetectSystemSignals(fakeFacts{
		projects: []string{"p1", "p2"},
	}, config.SystemAgent{}, signalTestNow)
	if n := countSignal(signals, SignalAllAgentsOffline); n != 2 {
		t.Fatalf("应产出 2 条，实际 %d", n)
	}
	seen := map[string]bool{}
	for _, s := range signals {
		if seen[s.Key()] {
			t.Fatalf("Key 重复: %s", s.Key())
		}
		seen[s.Key()] = true
	}
}

// signalDispatchSpy 记录 Dispatcher 是否被调用，用于证明「只观察」没走到派发。
type signalDispatchSpy struct{ seen int }

func (s *signalDispatchSpy) DispatchReady(_ context.Context, _ DispatchRequest) (DispatchOutcome, error) {
	s.seen++
	return DispatchOutcome{}, nil
}

// TestAllAgentsOfflineForcesObservation 覆盖加固：即便 auto_kinds 放行了 dispatch_ready，
// all_agents_offline 入队的作业也预置了 action=escalate_human，Orchestrator.Step 会在
// runJob 开头（orchestrator.go:189-192）直接 finish 为 Escalated，跳过 runDispatchJob，
// 不会真派发、不改任务状态。即「只观察」由代码层保证，而非配置约定。
func TestAllAgentsOfflineForcesObservation(t *testing.T) {
	st, err := db.OpenStore(filepath.Join(t.TempDir(), "maw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	// 故意放行 dispatch_ready，验证防护不依赖「绝不放行」这条配置约定。
	cfg := config.SystemAgent{
		AutoKinds:      []string{SystemKindUnblock, SystemKindDispatchReady},
		CooldownoutSec: 300,
		MaxAttempts:    3,
	}
	signals := DetectSystemSignals(fakeFacts{projects: []string{"p1"}}, cfg, now)
	offline := findSignal(signals, SignalAllAgentsOffline)
	if offline == nil {
		t.Fatal("应产出 all_agents_offline 信号")
	}
	if offline.Decision["action"] != string(ActionEscalateHuman) || !offline.Escalate {
		t.Fatalf("检测层应预置升级人工（与 auto_kinds 无关）: %+v", offline)
	}

	var queued []*db.SystemJob
	if err := st.Write(func(tx *sql.Tx) error {
		var e error
		queued, e = EnqueueSignals(tx, cfg, []Signal{*offline}, now)
		return e
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(queued) != 1 || queued[0].Kind != SystemKindDispatchReady {
		t.Fatalf("放行后应入队 1 条 dispatch_ready 观察作业: %+v", queued)
	}
	if queued[0].Decision["action"] != string(ActionEscalateHuman) {
		t.Fatalf("落库作业应带 escalate_human: %v", queued[0].Decision)
	}

	// 真跑编排器：预分类 escalate 必须在 switch job.Kind 之前截断派发。
	port := &signalDispatchSpy{}
	orch := &Orchestrator{
		Store:    st,
		Config:   cfg,
		Dispatch: port,
		WorkerID: "system-agent",
	}
	ran, err := orch.Step(context.Background())
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if ran != 1 {
		t.Fatalf("应消费 1 条作业，实际 %d", ran)
	}
	if port.seen != 0 {
		t.Fatalf("只观察不应调用 Dispatcher，实际 %d 次", port.seen)
	}
	if err := st.Read(func(tx *sql.Tx) error {
		got, e := db.GetSystemJob(tx, queued[0].JobID)
		if e != nil {
			return e
		}
		if got == nil {
			t.Fatal("作业不见了")
		}
		if got.Status != db.SystemJobEscalated {
			t.Fatalf("作业应升级人工而非派发，status=%s", got.Status)
		}
		if got.Result["mode"] != "preclassified" {
			t.Fatalf("应走预分类 escalate 分支而非 runDispatchJob: %v", got.Result)
		}
		return nil
	}); err != nil {
		t.Fatalf("read job: %v", err)
	}
}

// TestDetectMergingStalled 覆盖验收：超阈值 + 白名单命令才产出 ops_run。
func TestDetectMergingStalled(t *testing.T) {
	cfg := config.SystemAgent{OpsAllowlist: []string{"maw merge --yes", "maw prune"}}
	stalled := signalTestNow.Add(-mergingStallThreshold - time.Minute)
	recent := signalTestNow.Add(-mergingStallThreshold + time.Minute)
	cases := []struct {
		name    string
		fact    MergingTaskFact
		trigger bool
	}{
		{"超阈值且命令在白名单", MergingTaskFact{Since: stalled, OpsCommand: "maw merge --yes"}, true},
		{"带参数仍在白名单前缀内", MergingTaskFact{Since: stalled, OpsCommand: "maw prune --stale"}, true},
		{"未超阈值", MergingTaskFact{Since: recent, OpsCommand: "maw merge --yes"}, false},
		{"命令不在白名单", MergingTaskFact{Since: stalled, OpsCommand: "rm -rf /"}, false},
		{"命令为空", MergingTaskFact{Since: stalled}, false},
		{"Since 为零值", MergingTaskFact{OpsCommand: "maw merge --yes"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fact.ProjectID = "p1"
			tc.fact.TaskID = "T-1"
			signals := DetectSystemSignals(fakeFacts{
				projects: []string{"p1"},
				merging:  []MergingTaskFact{tc.fact},
			}, cfg, signalTestNow)
			got := findSignal(signals, SignalMergingStalled)
			if tc.trigger != (got != nil) {
				t.Fatalf("trigger=%v got=%+v signals=%+v", tc.trigger, got, signals)
			}
			if !tc.trigger {
				return
			}
			if got.JobKind != SystemKindOpsRun || got.OpsCommand != tc.fact.OpsCommand {
				t.Fatalf("ops_run 或命令不对: %+v", got)
			}
			if got.Decision["ops_command"] != tc.fact.OpsCommand {
				t.Fatalf("decision 缺 ops_command: %v", got.Decision)
			}
		})
	}
}

// TestDetectMergingStalledEmptyAllowlist 白名单为空时即使超阈值也不产出。
func TestDetectMergingStalledEmptyAllowlist(t *testing.T) {
	signals := DetectSystemSignals(fakeFacts{
		projects: []string{"p1"},
		merging: []MergingTaskFact{{
			ProjectID: "p1", TaskID: "T-1",
			Since: signalTestNow.Add(-2 * time.Hour), OpsCommand: "maw merge --yes",
		}},
	}, config.SystemAgent{}, signalTestNow)
	if findSignal(signals, SignalMergingStalled) != nil {
		t.Fatalf("白名单为空不应产出 ops_run: %+v", signals)
	}
}

// TestDetectStaleLock 覆盖验收：残留锁受 unknown 保护时只上报、不产出 job。
func TestDetectStaleLock(t *testing.T) {
	old := signalTestNow.Add(-staleLockAge - time.Minute)
	recent := signalTestNow.Add(-staleLockAge + time.Minute)
	cases := []struct {
		name    string
		fact    HeldLockFact
		trigger bool
	}{
		{"超龄且占用者不明", HeldLockFact{AcquiredAt: old, UnknownHolder: true}, true},
		{"超龄且 Holder 为空", HeldLockFact{AcquiredAt: old}, true},
		{"超龄但占用者明确", HeldLockFact{AcquiredAt: old, Holder: "agent-1"}, false},
		{"未超龄", HeldLockFact{AcquiredAt: recent, UnknownHolder: true}, false},
		{"AcquiredAt 零值", HeldLockFact{UnknownHolder: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fact.ProjectID = "p1"
			tc.fact.ResourceKey = "locks/merge/p1"
			signals := DetectSystemSignals(fakeFacts{
				projects: []string{"p1"},
				locks:    []HeldLockFact{tc.fact},
			}, config.SystemAgent{}, signalTestNow)
			got := findSignal(signals, SignalStaleLock)
			if tc.trigger != (got != nil) {
				t.Fatalf("trigger=%v got=%+v signals=%+v", tc.trigger, got, signals)
			}
			if !tc.trigger {
				return
			}
			if got.JobKind != "" {
				t.Fatalf("stale_lock 不应产出 job，JobKind=%q", got.JobKind)
			}
			if got.Decision["report_only"] != true {
				t.Fatalf("应标注仅上报: %v", got.Decision)
			}
			if !strings.Contains(got.Key(), "locks/merge/p1") {
				t.Fatalf("Key 应含 resource_key: %s", got.Key())
			}
		})
	}
}

// TestDetectSignalsIndependent 四类信号可同时产出，互不干扰，且 nil 证据源安全。
func TestDetectSignalsIndependent(t *testing.T) {
	if got := DetectSystemSignals(nil, config.SystemAgent{}, signalTestNow); got != nil {
		t.Fatalf("nil 证据源应返回 nil，实际 %+v", got)
	}
	cfg := config.SystemAgent{OpsAllowlist: []string{"maw merge --yes"}}
	signals := DetectSystemSignals(fakeFacts{
		projects: []string{"p1", "p2"},
		gates:    []GateFailureFact{{ProjectID: "p1", TaskID: "T-1", Output: "/bin/ps: operation not permitted"}},
		merging: []MergingTaskFact{{
			ProjectID: "p1", TaskID: "T-2",
			Since: signalTestNow.Add(-2 * time.Hour), OpsCommand: "maw merge --yes",
		}},
		locks: []HeldLockFact{{
			ProjectID: "p1", ResourceKey: "locks/merge/p1",
			AcquiredAt: signalTestNow.Add(-2 * time.Hour), UnknownHolder: true,
		}},
	}, cfg, signalTestNow)
	for _, k := range []SignalKind{SignalGateEnvFailure, SignalAllAgentsOffline, SignalMergingStalled, SignalStaleLock} {
		if findSignal(signals, k) == nil {
			t.Fatalf("缺信号 %s: %+v", k, signals)
		}
	}
}

// TestDetectRunUnverified 覆盖验收 2：超阈值未核实的占用 Run 产出 run_unverified，
// JobKind=run_reconcile；unknown 预分类升级，其余只入队观察。
func TestDetectRunUnverified(t *testing.T) {
	stale := signalTestNow.Add(-runUnverifiedWindow - time.Minute)
	fresh := signalTestNow.Add(-time.Minute)
	cases := []struct {
		name     string
		fact     UnverifiedRunFact
		trigger  bool
		escalate bool
	}{
		{"unknown 一律产出并预分类升级", UnverifiedRunFact{State: "unknown"}, true, true},
		{"running 心跳超阈值", UnverifiedRunFact{State: "running", LastSeen: stale}, true, false},
		{"running 心跳新鲜", UnverifiedRunFact{State: "running", LastSeen: fresh}, false, false},
		{"running 缺 LastSeen 按证据不足", UnverifiedRunFact{State: "running"}, true, false},
		{"not_started 超阈值", UnverifiedRunFact{State: "not_started", LastSeen: stale}, true, false},
		{"finished 不产出", UnverifiedRunFact{State: "finished", LastSeen: stale}, false, false},
		{"本轮已核实", UnverifiedRunFact{State: "unknown", CheckedAt: fresh}, false, false},
		{"核实时间也已超窗", UnverifiedRunFact{State: "unknown", CheckedAt: stale}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fact.ProjectID = "p1"
			tc.fact.TaskID = "T-1"
			tc.fact.RunID = "run_1"
			signals := DetectSystemSignals(fakeFacts{
				projects: []string{"p1"},
				runs:     []UnverifiedRunFact{tc.fact},
			}, config.SystemAgent{}, signalTestNow)
			got := findSignal(signals, SignalRunUnverified)
			if tc.trigger != (got != nil) {
				t.Fatalf("trigger=%v got=%+v signals=%+v", tc.trigger, got, signals)
			}
			if !tc.trigger {
				return
			}
			if got.JobKind != SystemKindRunReconcile {
				t.Fatalf("JobKind=%q want %q", got.JobKind, SystemKindRunReconcile)
			}
			if got.ResourceKey != "run_1" || got.Decision["run_id"] != "run_1" {
				t.Fatalf("应按 Run 维度去重并携带 run_id: %+v", got)
			}
			if got.Escalate != tc.escalate {
				t.Fatalf("Escalate=%v want %v", got.Escalate, tc.escalate)
			}
			if tc.escalate && got.Decision["action"] != string(ActionEscalateHuman) {
				t.Fatalf("unknown 应预分类升级: %v", got.Decision)
			}
		})
	}
}

// TestDetectRunUnverifiedDedupesByRunID 同一任务的不同 Run 各产出一条，Key 不互相冷却。
func TestDetectRunUnverifiedDedupesByRunID(t *testing.T) {
	signals := DetectSystemSignals(fakeFacts{
		projects: []string{"p1"},
		runs: []UnverifiedRunFact{
			{ProjectID: "p1", TaskID: "T-1", RunID: "run_1", State: "unknown"},
			{ProjectID: "p1", TaskID: "T-1", RunID: "run_2", State: "unknown"},
			{ProjectID: "p1", TaskID: "T-1", RunID: "run_1", State: "running"},
		},
	}, config.SystemAgent{}, signalTestNow)
	if n := countSignal(signals, SignalRunUnverified); n != 2 {
		t.Fatalf("应按 Run 去重为 2 条，实际 %d: %+v", n, signals)
	}
}

// TestEnqueueRunUnverifiedDefaultObservationOnly 默认不放行 auto_kinds 时只观察：
// run_reconcile 不落库；显式放行后才入队。
func TestEnqueueRunUnverifiedDefaultObservationOnly(t *testing.T) {
	st, err := db.OpenStore(filepath.Join(t.TempDir(), "maw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	signals := DetectSystemSignals(fakeFacts{
		projects: []string{"p1"},
		runs:     []UnverifiedRunFact{{ProjectID: "p1", TaskID: "T-1", RunID: "run_1", State: "unknown"}},
	}, config.SystemAgent{}, signalTestNow)
	sig := findSignal(signals, SignalRunUnverified)
	if sig == nil {
		t.Fatal("应产出 run_unverified")
	}

	// 默认：run_reconcile 不在 auto_kinds，只观察。
	if err := st.Write(func(tx *sql.Tx) error {
		_, e := EnqueueSignals(tx, config.SystemAgent{MaxAttempts: 3}, []Signal{*sig}, signalTestNow)
		return e
	}); err != nil {
		t.Fatalf("enqueue default: %v", err)
	}
	var n int
	if err := st.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM system_jobs WHERE kind = ?`, SystemKindRunReconcile).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("默认不应入队 run_reconcile，实际 %d 条", n)
	}

	// 放行后：入队且带信号键。
	cfg := config.SystemAgent{AutoKinds: []string{SystemKindRunReconcile}, CooldownoutSec: 300, MaxAttempts: 3}
	var queued []*db.SystemJob
	if err := st.Write(func(tx *sql.Tx) error {
		var e error
		queued, e = EnqueueSignals(tx, cfg, []Signal{*sig}, signalTestNow)
		return e
	}); err != nil {
		t.Fatalf("enqueue allowed: %v", err)
	}
	if len(queued) != 1 || queued[0].Kind != SystemKindRunReconcile {
		t.Fatalf("应入队 1 条 run_reconcile: %+v", queued)
	}
	if queued[0].Decision["signal_key"] != sig.Key() {
		t.Fatalf("决策应保留 signal_key: %v", queued[0].Decision)
	}
	// 预分类升级也随作业落库，编排层会先走核实再处理它。
	if queued[0].Decision["action"] != string(ActionEscalateHuman) {
		t.Fatalf("unknown 信号应带 escalate_human: %v", queued[0].Decision)
	}
}

// TestSignalCooldown 冷却时长取 cfg，缺省与非正值回落到 5 分钟。
func TestSignalCooldown(t *testing.T) {
	cases := []struct {
		cfg  config.SystemAgent
		want time.Duration
	}{
		{config.SystemAgent{}, 5 * time.Minute},
		{config.SystemAgent{CooldownoutSec: -1}, 5 * time.Minute},
		{config.SystemAgent{CooldownoutSec: 300}, 300 * time.Second},
		{config.SystemAgent{CooldownoutSec: 60}, time.Minute},
	}
	for _, tc := range cases {
		if got := SignalCooldown(tc.cfg); got != tc.want {
			t.Fatalf("cfg=%+v got=%v want=%v", tc.cfg, got, tc.want)
		}
	}
}

// TestWithinCooldown 纯函数冷却判定，覆盖零值、边界、关闭冷却与时钟回拨。
func TestWithinCooldown(t *testing.T) {
	now := signalTestNow
	cd := 5 * time.Minute
	cases := []struct {
		name     string
		last     time.Time
		cooldown time.Duration
		want     bool
	}{
		{"从未入队", time.Time{}, cd, false},
		{"关闭冷却", now.Add(-time.Minute), 0, false},
		{"负冷却", now.Add(-time.Minute), -time.Second, false},
		{"冷却中", now.Add(-time.Minute), cd, true},
		{"恰好等于冷却边界", now.Add(-cd), cd, false},
		{"已过冷却", now.Add(-cd - time.Second), cd, false},
		{"时钟回拨保守判冷却", now.Add(time.Hour), cd, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WithinCooldown(tc.last, now, tc.cooldown); got != tc.want {
				t.Fatalf("WithinCooldown=%v want %v", got, tc.want)
			}
		})
	}
}

// TestSignalKey 去重键稳定且不同信号不碰撞。
func TestSignalKey(t *testing.T) {
	base := Signal{Kind: SignalGateEnvFailure, ProjectID: "p1", TaskID: "T-1"}
	if base.Key() != "gate_env_failure:p1:T-1" {
		t.Fatalf("Key=%q", base.Key())
	}
	lock := Signal{Kind: SignalStaleLock, ProjectID: "p1", TaskID: "T-1", ResourceKey: "lock/a"}
	if lock.Key() != "stale_lock:p1:lock/a" || lock.Key() == base.Key() {
		t.Fatalf("stale_lock Key 应使用 resource_key 且不碰撞: %q", lock.Key())
	}
	offline := Signal{Kind: SignalAllAgentsOffline, ProjectID: "p1"}
	if offline.Key() != "all_agents_offline:p1:" {
		t.Fatalf("Key=%q", offline.Key())
	}
	otherProject := base
	otherProject.ProjectID = "p2"
	if otherProject.Key() == base.Key() {
		t.Fatalf("不同工程 Key 应不同")
	}
}
