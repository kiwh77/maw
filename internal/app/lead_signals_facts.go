package app

import (
	"database/sql"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/matching"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/systemagent"
)

// hubSignalFacts 把本机状态库里的真实事实喂给 systemagent.DetectSystemSignals。
//
// 「事实从哪来」与「怎么判定」分离：本文件只读库、不做判断；四类信号的判定
// 全在 systemagent 的纯函数里，可以用假证据完整覆盖。读库失败时返回空快照，
// 绝不猜测、绝不伪造证据。
type hubSignalFacts struct {
	projects   []string
	gateFails  []systemagent.GateFailureFact
	heartbeats []systemagent.AgentHeartbeatFact
	merging    []systemagent.MergingTaskFact
	locks      []systemagent.HeldLockFact
	runs       []systemagent.UnverifiedRunFact
	stalledOps []systemagent.StalledOperationFact
	claimed    []systemagent.ClaimedTaskFact
	deadlocks  []systemagent.DependencyDeadlockFact
	noNodes    []systemagent.NoCapableNodeFact
	failures   []systemagent.FailureFact
}

func newHubSignalFacts(gdb string, cfg config.SystemAgent) systemagent.SignalFacts {
	f := &hubSignalFacts{}
	if strings.TrimSpace(gdb) == "" {
		return f
	}
	_ = db.WithTx(gdb, func(tx *sql.Tx) error {
		f.load(tx, cfg)
		return nil
	})
	return f
}

func (f *hubSignalFacts) load(tx *sql.Tx, cfg config.SystemAgent) {
	projects, err := db.ListProjects(tx)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	mergeCmd := allowedMergeCommand(cfg.OpsAllowlist)
	for _, p := range projects {
		if p == nil || p.ID == "" {
			continue
		}
		f.projects = append(f.projects, p.ID)

		if agents, e := db.ListAgents(tx, p.ID); e == nil {
			for _, a := range agents {
				if a == nil {
					continue
				}
				f.heartbeats = append(f.heartbeats, systemagent.AgentHeartbeatFact{
					ProjectID: p.ID, AgentID: a.ID, Status: a.Status,
					LastSeen: parseRFC3339(a.LastSeen),
				})
			}
		}

		// 任务只读一次，后续 merging / 依赖 / 领取未开跑都基于同一份快照。
		tasks, taskErr := db.ListTasks(tx, p.ID)
		if taskErr != nil {
			// 读不到任务就整段留空：宁可漏报，也不伪造证据。
			continue
		}
		taskByInstance := map[string]string{}
		for _, t := range tasks {
			if t != nil && t.InstanceID != "" {
				taskByInstance[t.InstanceID] = t.ID
			}
		}

		for _, t := range tasks {
			if t == nil || t.Status != string(model.TaskMerging) {
				continue
			}
			f.merging = append(f.merging, systemagent.MergingTaskFact{
				ProjectID: p.ID, TaskID: t.ID,
				Since: parseRFC3339(t.Heartbeat), OpsCommand: mergeCmd,
			})
		}

		if leases, e := db.ListActiveLeases(tx, p.ID); e == nil {
			for _, l := range leases {
				if l == nil {
					continue
				}
				f.locks = append(f.locks, systemagent.HeldLockFact{
					ProjectID: p.ID, ResourceKey: l.ResourceKey, Holder: l.Holder,
					AcquiredAt: parseRFC3339(l.AcquiredAt),
					// 活跃租约的占用者身份一律按「证据不足」处理：只上报，
					// 不允许自动释放（与 maw prune 的 unknown 占用保护一致）。
					UnknownHolder: true,
				})
			}
		}

		// 派发操作停滞证据：只取 pending 行，判定交给 systemagent。
		f.stalledOps = append(f.stalledOps, stalledOperationFacts(tx, p.ID)...)

		// Run 证据：同一次扫描同时产出「未核实占用」与「窗口内失败计数」。
		// 状态取 reconciliation_state，时间取 updated_at；keep 会刷新 updated_at，
		// 因此「最近一次核实」天然成为 15 分钟窗口的一部分。
		instPhase := map[string]string{}
		instGen := map[string]model.Generation{}
		failCount := map[string]int{}
		lastFail := map[string]time.Time{}
		if runs, e := db.ListRuns(tx, p.ID); e == nil {
			for _, r := range runs {
				if r == nil || r.ID == "" {
					continue
				}
				if prev, ok := instGen[r.InstanceID]; !ok || r.Generation > prev {
					instGen[r.InstanceID] = r.Generation
					instPhase[r.InstanceID] = string(r.Phase)
				}
				if r.Phase == model.PhaseFailed {
					at := parseRFC3339(r.FinishedAt)
					if at.IsZero() {
						at = parseRFC3339(r.UpdatedAt)
					}
					if at.IsZero() || now.Sub(at) <= systemagent.RepeatedFailureWindow {
						failCount[r.InstanceID]++
						if at.After(lastFail[r.InstanceID]) {
							lastFail[r.InstanceID] = at
						}
					}
				}
				// 已确认的终态 Run 不再产出核实证据；unknown 或非终态才需要核实。
				if r.Reconciliation != model.ReconciliationUnknown && r.Phase.Terminal() {
					continue
				}
				seen := parseRFC3339(r.UpdatedAt)
				f.runs = append(f.runs, systemagent.UnverifiedRunFact{
					ProjectID:  p.ID,
					TaskID:     taskByInstance[r.InstanceID],
					RunID:      r.ID,
					InstanceID: r.InstanceID,
					State:      string(r.Reconciliation),
					LastSeen:   seen,
					CheckedAt:  seen,
				})
			}
		}
		for inst, n := range failCount {
			f.failures = append(f.failures, systemagent.FailureFact{
				ProjectID: p.ID, TaskID: taskByInstance[inst], InstanceID: inst,
				Failures: n, LastFailure: lastFail[inst],
			})
		}

		// 已领取没开跑证据：任务心跳 + 同实例最新 Run 的阶段。
		for _, t := range tasks {
			if t == nil || t.Status != string(model.TaskInProgress) {
				continue
			}
			f.claimed = append(f.claimed, systemagent.ClaimedTaskFact{
				ProjectID: p.ID, TaskID: t.ID,
				Heartbeat: parseRFC3339(t.Heartbeat),
				RunPhase:  instPhase[t.InstanceID],
			})
		}

		// 依赖死锁证据：优先报环，其次报「全部 ready 题都被未完成依赖堵住」。
		if cycle := dependencyCycle(tasks); len(cycle) > 0 {
			f.deadlocks = append(f.deadlocks, systemagent.DependencyDeadlockFact{
				ProjectID: p.ID, Cycle: cycle,
			})
		} else if blocked, since := blockedReadyTasks(tasks); len(blocked) > 0 {
			f.deadlocks = append(f.deadlocks, systemagent.DependencyDeadlockFact{
				ProjectID: p.ID, BlockedSince: since, BlockedTasks: blocked,
			})
		}

		// 无可用节点证据：复用 matching.Match，不另立一套「可派发」规则。
		f.noNodes = append(f.noNodes, noCapableNodeFacts(tx, p.ID, tasks)...)
	}

	// 门禁失败证据：gate_env_failed 直接采信；gate_failed 需再用词表复核，
	// 只有「环境失败」才算系统级信号，代码失败不在这里升级。
	events, err := db.ListEventsSince(tx, 0, "", "", 5000)
	if err != nil {
		return
	}
	cutoff := now.Add(-30 * time.Minute)
	for _, e := range events {
		if e == nil || e.Type != "gate_env_failed" && e.Type != "gate_failed" {
			continue
		}
		if ts := parseRFC3339(e.TS); !ts.IsZero() && ts.Before(cutoff) {
			continue
		}
		out, _ := e.Data["error"].(string)
		env := e.Type == "gate_env_failed" || gitops.IsEnvFailure(out)
		if !env {
			continue
		}
		f.gateFails = append(f.gateFails, systemagent.GateFailureFact{
			ProjectID: e.ProjectID, TaskID: e.TaskID, Output: out, EnvFailure: env,
		})
	}
}

func parseRFC3339(s string) time.Time {
	v := strings.TrimSpace(s)
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// allowedMergeCommand 只从 ops_allowlist 里挑一条 merge 命令，绝不拼新命令。
func allowedMergeCommand(allowlist []string) string {
	for _, c := range allowlist {
		if strings.TrimSpace(c) == "maw merge --yes" {
			return c
		}
	}
	return ""
}

// stalledOperationFacts 读出 pending 派发 / 取消操作；是否算停滞交给判定层。
func stalledOperationFacts(tx *sql.Tx, projectID string) []systemagent.StalledOperationFact {
	rows, err := tx.Query(`SELECT operation_id, kind, result, lease_expires_at, created_at
FROM operations WHERE project_id = ? AND result = 'pending'`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []systemagent.StalledOperationFact
	for rows.Next() {
		var id, kind, result, created string
		var lease sql.NullString
		if err := rows.Scan(&id, &kind, &result, &lease, &created); err != nil {
			continue
		}
		out = append(out, systemagent.StalledOperationFact{
			ProjectID: projectID, OperationID: id, Kind: kind, Result: result,
			LeaseExpiresAt: parseRFC3339(lease.String),
			CreatedAt:      parseRFC3339(created),
		})
	}
	return out
}

// dependencyCycle 在非终态任务间找一条依赖环；没有环返回 nil。
//
// 只把未完成的任务当作图节点：指向已完成任务的边不再阻塞任何东西，不参与成环。
func dependencyCycle(tasks []*db.Task) []string {
	index := make(map[string]*db.Task, len(tasks))
	for _, t := range tasks {
		if t == nil || model.TaskStatus(t.Status).Terminal() {
			continue
		}
		index[t.ID] = t
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(index))
	var stack []string
	var cycle []string
	var dfs func(id string) bool
	dfs = func(id string) bool {
		color[id] = gray
		stack = append(stack, id)
		t := index[id]
		for _, dep := range t.Deps {
			if _, ok := index[dep]; !ok {
				continue
			}
			switch color[dep] {
			case gray:
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i] == dep {
						cycle = append(append([]string{}, stack[i:]...), dep)
						return true
					}
				}
			case white:
				if dfs(dep) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}
	for _, t := range tasks {
		if t == nil {
			continue
		}
		if _, ok := index[t.ID]; !ok || color[t.ID] != white {
			continue
		}
		if dfs(t.ID) {
			return cycle
		}
	}
	return nil
}

// blockedReadyTasks 判定「全部 ready 题都被未完成依赖堵住」。
//
// 只要还有一道 ready 题依赖已满足，就说明依赖不是瓶颈，返回空；否则返回受堵题号，
// 并把最早的（非零）任务心跳当作「从何时开始堵」的证据；没有心跳证据时 since 为零。
func blockedReadyTasks(tasks []*db.Task) ([]string, time.Time) {
	index := make(map[string]*db.Task, len(tasks))
	for _, t := range tasks {
		if t != nil {
			index[t.ID] = t
		}
	}
	var blocked []string
	var since time.Time
	hasReady := false
	for _, t := range tasks {
		if t == nil || t.Status != string(model.TaskReady) {
			continue
		}
		hasReady = true
		blockedByDep := false
		for _, dep := range t.Deps {
			d, ok := index[dep]
			if !ok || !model.TaskStatus(d.Status).Terminal() {
				blockedByDep = true
				break
			}
		}
		if !blockedByDep {
			return nil, time.Time{}
		}
		blocked = append(blocked, t.ID)
		if hb := parseRFC3339(t.Heartbeat); !hb.IsZero() && (since.IsZero() || hb.Before(since)) {
			since = hb
		}
	}
	if !hasReady {
		return nil, time.Time{}
	}
	return blocked, since
}

// nodeLevelReasons 是「题本身可执行、但节点侧不允许」的原因码；
// 只命中这些码才算 no_capable_node，依赖/写盘冲突属于别的信号。
var nodeLevelReasons = map[matching.ReasonCode]bool{
	matching.ReasonNoCandidateNode:   true,
	matching.ReasonProjectPolicy:     true,
	matching.ReasonNodeDisabled:      true,
	matching.ReasonNodeDraining:      true,
	matching.ReasonNodeStale:         true,
	matching.ReasonNodeNotReady:      true,
	matching.ReasonNodeOccupied:      true,
	matching.ReasonCapabilityUnknown: true,
}

// noCapableNodeFacts 逐条 ready 题跑一次匹配，收集「有题但没节点」的证据。
//
// 复用 matching.Match，不另立一套可派发规则；策略里把自动执行打开并给最小上限，
// 避免「自动执行关闭 / 受管上限」这类全局原因掩盖节点侧的真实缺口。
func noCapableNodeFacts(tx *sql.Tx, projectID string, tasks []*db.Task) []systemagent.NoCapableNodeFact {
	nodes := matchingNodes(tx, projectID)
	var out []systemagent.NoCapableNodeFact
	for _, t := range tasks {
		if t == nil || t.Status != string(model.TaskReady) {
			continue
		}
		dec, err := matching.Match(matching.Request{
			Policy: matching.Policy{AutoExecution: model.AutoExecution{
				Enabled: true, ManagedExecutionLimit: 1,
			}},
			Tasks: []matching.Task{{
				ID: t.ID, ProjectID: model.ProjectID(projectID), Status: model.TaskReady,
				Priority: t.Priority, MergeOrder: t.MergeOrder,
				Deps: t.Deps, WriteScope: t.WriteScope,
			}},
			Nodes: nodes,
		})
		if err != nil || dec.Matched {
			continue
		}
		reason, ok := nodeLevelReason(dec.Reasons)
		if !ok {
			continue
		}
		out = append(out, systemagent.NoCapableNodeFact{
			ProjectID: projectID, TaskID: t.ID, Reason: reason,
		})
	}
	return out
}

// matchingNodes 把节点行与槽位账目投影成匹配输入。
func matchingNodes(tx *sql.Tx, projectID string) []matching.Node {
	rows, err := db.ListNode(tx, projectID)
	if err != nil {
		return nil
	}
	var out []matching.Node
	for _, n := range rows {
		if n == nil || n.ID == "" {
			continue
		}
		capacity, e := db.Capacity(tx, n.ID)
		if e != nil {
			continue
		}
		out = append(out, matchingNode(n, capacity.ActiveSlots))
	}
	return out
}

// nodeLevelReason 从匹配原因里挑一条节点级原因码。
func nodeLevelReason(reasons []matching.QueueReason) (string, bool) {
	for _, r := range reasons {
		if nodeLevelReasons[r.Code] {
			return string(r.Code), true
		}
	}
	return "", false
}

func (f *hubSignalFacts) Projects() []string { return f.projects }
func (f *hubSignalFacts) RecentGateFailures() []systemagent.GateFailureFact {
	return f.gateFails
}
func (f *hubSignalFacts) AgentHeartbeats() []systemagent.AgentHeartbeatFact {
	return f.heartbeats
}
func (f *hubSignalFacts) MergingTasks() []systemagent.MergingTaskFact { return f.merging }
func (f *hubSignalFacts) HeldLocks() []systemagent.HeldLockFact       { return f.locks }
func (f *hubSignalFacts) UnverifiedRuns() []systemagent.UnverifiedRunFact {
	return f.runs
}
func (f *hubSignalFacts) StalledOperations() []systemagent.StalledOperationFact {
	return f.stalledOps
}
func (f *hubSignalFacts) ClaimedNotStarted() []systemagent.ClaimedTaskFact {
	return f.claimed
}
func (f *hubSignalFacts) DependencyDeadlocks() []systemagent.DependencyDeadlockFact {
	return f.deadlocks
}
func (f *hubSignalFacts) NoCapableNodes() []systemagent.NoCapableNodeFact {
	return f.noNodes
}
func (f *hubSignalFacts) RecentFailures() []systemagent.FailureFact {
	return f.failures
}
