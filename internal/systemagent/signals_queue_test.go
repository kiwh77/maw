package systemagent

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

// queueFacts 在 fakeFacts 之上补齐五类「队列卡死」信号的证据源。
//
// 单独定义而不改 fakeFacts，是为了让既有信号测试保持最小证据面：
// 未填的证据就是「没有证据」，对应信号自然不产出。
type queueFacts struct {
	fakeFacts
	stalled   []StalledOperationFact
	claimed   []ClaimedTaskFact
	deadlocks []DependencyDeadlockFact
	noNodes   []NoCapableNodeFact
	failures  []FailureFact
}

func (f queueFacts) StalledOperations() []StalledOperationFact     { return f.stalled }
func (f queueFacts) ClaimedNotStarted() []ClaimedTaskFact          { return f.claimed }
func (f queueFacts) DependencyDeadlocks() []DependencyDeadlockFact { return f.deadlocks }
func (f queueFacts) NoCapableNodes() []NoCapableNodeFact           { return f.noNodes }
func (f queueFacts) RecentFailures() []FailureFact                 { return f.failures }

// assertReportOnly 断言信号只上报：不产出作业、决策带 report_only。
func assertReportOnly(t *testing.T, sig *Signal) {
	t.Helper()
	if sig.JobKind != "" {
		t.Fatalf("应只上报，实际 JobKind=%q", sig.JobKind)
	}
	if sig.Decision["report_only"] != true {
		t.Fatalf("应标注 report_only: %v", sig.Decision)
	}
}

// TestDetectDispatchOpStalled 覆盖验收：pending 且租约过期超阈值才产出，且只上报。
func TestDetectDispatchOpStalled(t *testing.T) {
	stale := signalTestNow.Add(-dispatchOpStallThreshold - time.Minute)
	recent := signalTestNow.Add(-dispatchOpStallThreshold + time.Minute)
	cases := []struct {
		name    string
		fact    StalledOperationFact
		trigger bool
	}{
		{"租约过期超阈值", StalledOperationFact{Result: "pending", LeaseExpiresAt: stale, CreatedAt: stale}, true},
		{"从未领取且创建超阈值", StalledOperationFact{Result: "pending", CreatedAt: stale}, true},
		{"租约刚过期", StalledOperationFact{Result: "pending", LeaseExpiresAt: recent, CreatedAt: recent}, false},
		{"已完成的操作", StalledOperationFact{Result: "applied", LeaseExpiresAt: stale}, false},
		{"时间证据缺失", StalledOperationFact{Result: "pending"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fact.ProjectID = "p1"
			tc.fact.OperationID = "op_1"
			tc.fact.Kind = "dispatch"
			signals := DetectSystemSignals(queueFacts{
				fakeFacts: fakeFacts{projects: []string{"p1"}},
				stalled:   []StalledOperationFact{tc.fact},
			}, config.SystemAgent{}, signalTestNow)
			got := findSignal(signals, SignalDispatchOpStalled)
			if tc.trigger != (got != nil) {
				t.Fatalf("trigger=%v got=%+v signals=%+v", tc.trigger, got, signals)
			}
			if !tc.trigger {
				return
			}
			if got.ResourceKey != "op_1" || got.Decision["operation_id"] != "op_1" {
				t.Fatalf("应按操作去重并携带 operation_id: %+v", got)
			}
			assertReportOnly(t, got)
		})
	}
}

// TestDetectClaimedNotStarted 覆盖验收：已领取但心跳缺失/过期且 Run 非 running 才产出。
func TestDetectClaimedNotStarted(t *testing.T) {
	stale := signalTestNow.Add(-agentHeartbeatWindow - time.Minute)
	fresh := signalTestNow.Add(-time.Minute)
	cases := []struct {
		name    string
		fact    ClaimedTaskFact
		trigger bool
	}{
		{"从未心跳且无 Run", ClaimedTaskFact{}, true},
		{"心跳过期且 Run 已失败", ClaimedTaskFact{Heartbeat: stale, RunPhase: "failed"}, true},
		{"心跳新鲜", ClaimedTaskFact{Heartbeat: fresh}, false},
		{"心跳过期但 Run 仍在跑", ClaimedTaskFact{Heartbeat: stale, RunPhase: "running"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fact.ProjectID = "p1"
			tc.fact.TaskID = "T-1"
			signals := DetectSystemSignals(queueFacts{
				fakeFacts: fakeFacts{projects: []string{"p1"}},
				claimed:   []ClaimedTaskFact{tc.fact},
			}, config.SystemAgent{}, signalTestNow)
			got := findSignal(signals, SignalClaimedNotStarted)
			if tc.trigger != (got != nil) {
				t.Fatalf("trigger=%v got=%+v signals=%+v", tc.trigger, got, signals)
			}
			if !tc.trigger {
				return
			}
			assertReportOnly(t, got)
			if !strings.Contains(got.Reason, "in_progress") {
				t.Fatalf("reason 应说明任务仍在 in_progress: %q", got.Reason)
			}
		})
	}
}

// TestDetectDepDeadlock 覆盖验收：依赖环立即报；全部 ready 被堵需超阈值才报。
func TestDetectDepDeadlock(t *testing.T) {
	stale := signalTestNow.Add(-depDeadlockThreshold - time.Minute)
	recent := signalTestNow.Add(-depDeadlockThreshold + time.Minute)
	cases := []struct {
		name    string
		fact    DependencyDeadlockFact
		trigger bool
	}{
		{"依赖环", DependencyDeadlockFact{Cycle: []string{"T-1", "T-2", "T-1"}}, true},
		{"全部 ready 被堵超阈值", DependencyDeadlockFact{BlockedSince: stale, BlockedTasks: []string{"T-3"}}, true},
		{"刚被堵", DependencyDeadlockFact{BlockedSince: recent, BlockedTasks: []string{"T-3"}}, false},
		{"证据不足", DependencyDeadlockFact{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fact.ProjectID = "p1"
			signals := DetectSystemSignals(queueFacts{
				fakeFacts: fakeFacts{projects: []string{"p1"}},
				deadlocks: []DependencyDeadlockFact{tc.fact},
			}, config.SystemAgent{}, signalTestNow)
			got := findSignal(signals, SignalDepDeadlock)
			if tc.trigger != (got != nil) {
				t.Fatalf("trigger=%v got=%+v signals=%+v", tc.trigger, got, signals)
			}
			if !tc.trigger {
				return
			}
			assertReportOnly(t, got)
			if len(tc.fact.Cycle) > 0 && !strings.Contains(got.Reason, "成环") {
				t.Fatalf("环应写进 reason: %q", got.Reason)
			}
		})
	}
}

// TestDetectNoCapableNode 覆盖验收：有 ready 题但无可用节点才产出，且不放宽匹配。
func TestDetectNoCapableNode(t *testing.T) {
	cases := []struct {
		name    string
		facts   []NoCapableNodeFact
		trigger bool
	}{
		{"无候选节点", []NoCapableNodeFact{{TaskID: "T-1", Reason: "no_candidate_node"}}, true},
		{"能力未知", []NoCapableNodeFact{{TaskID: "T-2", Reason: "capability_unknown"}}, true},
		{"没有证据", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.facts {
				tc.facts[i].ProjectID = "p1"
			}
			signals := DetectSystemSignals(queueFacts{
				fakeFacts: fakeFacts{projects: []string{"p1"}},
				noNodes:   tc.facts,
			}, config.SystemAgent{}, signalTestNow)
			got := findSignal(signals, SignalNoCapableNode)
			if tc.trigger != (got != nil) {
				t.Fatalf("trigger=%v got=%+v signals=%+v", tc.trigger, got, signals)
			}
			if !tc.trigger {
				return
			}
			assertReportOnly(t, got)
			if got.Decision["reason"] != tc.facts[0].Reason {
				t.Fatalf("应携带匹配原因: %v", got.Decision)
			}
		})
	}
}

// TestDetectRepeatedFailure 覆盖验收：窗口内失败达到 max_attempts 才产出，且不自动重派。
func TestDetectRepeatedFailure(t *testing.T) {
	cases := []struct {
		name    string
		cfg     config.SystemAgent
		fact    FailureFact
		trigger bool
	}{
		{"默认阈值 3 次", config.SystemAgent{}, FailureFact{Failures: 3}, true},
		{"可配置阈值 2 次", config.SystemAgent{MaxAttempts: 2}, FailureFact{Failures: 2}, true},
		{"未达阈值", config.SystemAgent{}, FailureFact{Failures: 2}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.fact.ProjectID = "p1"
			tc.fact.TaskID = "T-1"
			tc.fact.InstanceID = "ti_1"
			signals := DetectSystemSignals(queueFacts{
				fakeFacts: fakeFacts{projects: []string{"p1"}},
				failures:  []FailureFact{tc.fact},
			}, tc.cfg, signalTestNow)
			got := findSignal(signals, SignalRepeatedFailure)
			if tc.trigger != (got != nil) {
				t.Fatalf("trigger=%v got=%+v signals=%+v", tc.trigger, got, signals)
			}
			if !tc.trigger {
				return
			}
			assertReportOnly(t, got)
			if got.ResourceKey != "ti_1" {
				t.Fatalf("应按实例去重: %q", got.ResourceKey)
			}
		})
	}
}

// TestQueueSignalKeysDoNotCollide 五个新信号互不碰撞，且同实例不同实例分开冷却。
func TestQueueSignalKeysDoNotCollide(t *testing.T) {
	signals := []Signal{
		{Kind: SignalDispatchOpStalled, ProjectID: "p1", ResourceKey: "op_1"},
		{Kind: SignalClaimedNotStarted, ProjectID: "p1", TaskID: "T-1"},
		{Kind: SignalDepDeadlock, ProjectID: "p1", ResourceKey: "p1"},
		{Kind: SignalNoCapableNode, ProjectID: "p1", TaskID: "T-1"},
		{Kind: SignalRepeatedFailure, ProjectID: "p1", TaskID: "T-1", ResourceKey: "ti_1"},
		{Kind: SignalRepeatedFailure, ProjectID: "p1", TaskID: "T-1", ResourceKey: "ti_2"},
	}
	seen := map[string]bool{}
	for _, s := range signals {
		if seen[s.Key()] {
			t.Fatalf("Key 碰撞: %s", s.Key())
		}
		seen[s.Key()] = true
	}
}

// TestEnqueueQueueSignalsReportOnly 覆盖验收 2：五类信号入队时全部被跳过，
// 作业表为零、任务表一行不动。
func TestEnqueueQueueSignalsReportOnly(t *testing.T) {
	st, err := db.OpenStore(filepath.Join(t.TempDir(), "maw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.SystemAgent{
		// 故意把能想到的 kind 全放行：只上报由「JobKind 为空」保证，不靠配置。
		AutoKinds: []string{
			SystemKindUnblock, SystemKindOpsRun, SystemKindDispatchReady,
			SystemKindTaskSplit, SystemKindRunReconcile,
		},
		CooldownoutSec: 300,
		MaxAttempts:    3,
	}
	signals := DetectSystemSignals(queueFacts{
		// 有新鲜心跳，避免 all_agents_offline 额外产出一条观察作业，
		// 让这里的断言只针对五类队列信号。
		fakeFacts: fakeFacts{
			projects: []string{"p1"},
			agents: []AgentHeartbeatFact{{
				ProjectID: "p1", AgentID: "a1", Status: "idle",
				LastSeen: signalTestNow.Add(-time.Minute),
			}},
		},
		stalled: []StalledOperationFact{{
			ProjectID: "p1", OperationID: "op_1", Kind: "dispatch", Result: "pending",
			LeaseExpiresAt: signalTestNow.Add(-time.Hour),
		}},
		claimed: []ClaimedTaskFact{{ProjectID: "p1", TaskID: "T-1"}},
		deadlocks: []DependencyDeadlockFact{{
			ProjectID: "p1", Cycle: []string{"T-1", "T-2", "T-1"},
		}},
		noNodes:  []NoCapableNodeFact{{ProjectID: "p1", TaskID: "T-1", Reason: "no_candidate_node"}},
		failures: []FailureFact{{ProjectID: "p1", TaskID: "T-1", InstanceID: "ti_1", Failures: 3}},
	}, cfg, signalTestNow)

	var countBefore int
	if err := st.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&countBefore)
	}); err != nil {
		t.Fatalf("count tasks: %v", err)
	}

	var queued []*db.SystemJob
	if err := st.Write(func(tx *sql.Tx) error {
		var e error
		queued, e = EnqueueSignals(tx, cfg, signals, signalTestNow)
		return e
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(queued) != 0 {
		t.Fatalf("五类信号不应入队，实际 %d 条: %+v", len(queued), queued)
	}
	var nJobs, countAfter int
	if err := st.Read(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT COUNT(*) FROM system_jobs`).Scan(&nJobs); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&countAfter)
	}); err != nil {
		t.Fatalf("read counts: %v", err)
	}
	if nJobs != 0 {
		t.Fatalf("system_jobs 应为空，实际 %d 条", nJobs)
	}
	if countAfter != countBefore {
		t.Fatalf("tasks 表应不变: before=%d after=%d", countBefore, countAfter)
	}
}
