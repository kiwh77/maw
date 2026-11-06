package systemagent

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/gitops"
)

// 系统级信号的判定窗口。
//
// config.SystemAgent 目前没有独立的心跳/停滞字段，这里统一用包级常量：
//   - agentHeartbeatWindow：与「工程内 15 分钟内有心跳的非 offline Agent 视为忙」
//     的 project_busy 守卫保持一致；
//   - mergingStallThreshold / staleLockAge：按任务约定的 30 分钟。
//
// 这些是纯判定参数，装配卡若需要别的窗口，改这里的常量即可，判定逻辑不动。
const (
	agentHeartbeatWindow  = 15 * time.Minute
	mergingStallThreshold = 30 * time.Minute
	staleLockAge          = 30 * time.Minute
	// runUnverifiedWindow 是「非终态 Run 心跳缺失」的判定阈值，与 Agent 心跳窗口同值。
	runUnverifiedWindow = agentHeartbeatWindow
)

// SignalKind 是系统级信号的类别，与 system_jobs.kind 是两套概念：
// SignalKind 描述「观察到了什么」，system_jobs.kind 才是「要入什么作业」。
type SignalKind string

const (
	SignalGateEnvFailure   SignalKind = "gate_env_failure"
	SignalAllAgentsOffline SignalKind = "all_agents_offline"
	SignalMergingStalled   SignalKind = "merging_stalled"
	SignalStaleLock        SignalKind = "stale_lock"
	SignalRunUnverified    SignalKind = "run_unverified"
)

// Signal 是一次系统级观察的结果。
//
// DetectSystemSignals 只产出它、不执行任何动作；是否入队由 EnqueueSignals 按
// JobKind / KindAllowed / 冷却决定。JobKind 为空表示「只上报，不产出 job」。
type Signal struct {
	Kind        SignalKind
	ProjectID   string
	TaskID      string
	ResourceKey string
	Reason      string
	// JobKind 是建议入队的 system_jobs.kind；空串表示只上报、不产出 job。
	JobKind string
	// Decision 是入队时随作业携带的决策，越具体越好（如 ops_command）。
	Decision map[string]any
	// OpsCommand 是 ops_run 建议执行的命令，已通过 OpsAllowlist 校验。
	OpsCommand string
	// Escalate 表示命中 needs_human / auth_required，须走升级人工、不自动处置。
	Escalate bool
}

// Key 返回信号的稳定去重键，冷却与去重都按它，而不是按 job 的 task+kind。
func (s Signal) Key() string {
	id := s.TaskID
	if s.Kind == SignalStaleLock || s.Kind == SignalRunUnverified {
		id = s.ResourceKey
	}
	return string(s.Kind) + ":" + s.ProjectID + ":" + id
}

// ---- 证据接口与值对象 ----
//
// 进程 / 门禁 / 心跳 / 锁这些「环境事实从哪来」全部抽成接口注入，
// 判定逻辑（DetectSystemSignals）只吃这些值对象，因此可以用假证据完整覆盖，
// 不需要真实 DB、真实进程，也不需要在测试里整包 skip。
// 装配卡负责提供读库 / 读事件的真实实现。

// GateFailureFact 是最近一次 submit 门禁失败的证据。
type GateFailureFact struct {
	ProjectID string
	TaskID    string
	Output    string
	// EnvFailure 为真是事实源已判定「环境失败」，直接采信，不再跑词表。
	EnvFailure bool
}

// AgentHeartbeatFact 是一个 Agent 的最后心跳与状态。
type AgentHeartbeatFact struct {
	ProjectID string
	AgentID   string
	Status    string
	LastSeen  time.Time
}

// MergingTaskFact 是一个停在 merging 的任务及其最近推进时间。
type MergingTaskFact struct {
	ProjectID string
	TaskID    string
	// Since 是进入 / 最近推进 merging 的时间，零值表示证据不足、不判定停滞。
	Since time.Time
	// OpsCommand 是建议的运维命令，必须在 OpsAllowlist 内才会产出信号。
	OpsCommand string
}

// HeldLockFact 是一个仍被占用的租约 / 锁。
type HeldLockFact struct {
	ProjectID   string
	ResourceKey string
	Holder      string
	AcquiredAt  time.Time
	// UnknownHolder 表示占用者身份不明 / 证据不足，受 unknown 占用保护，
	// 这种残留只上报，不允许自动释放或自动处置。
	UnknownHolder bool
}

// UnverifiedRunFact 是一条可能仍占资源、但尚未核实的受管 Run 证据。
type UnverifiedRunFact struct {
	ProjectID  string
	TaskID     string
	RunID      string
	InstanceID string
	// State 取 runs.reconciliation_state：running / finished / not_started / unknown。
	State string
	// LastSeen 是 Run 的最后更新时间（runs.updated_at）；零值表示证据不足。
	LastSeen time.Time
	// CheckedAt 是最近一次核实的时刻；零值表示从未核实。
	CheckedAt time.Time
}

// UnverifiedRunSource 是 SignalFacts 的可选扩展：提供 Run 核实证据。
//
// 写成可选接口而不是给 SignalFacts 加必选方法，是为了不打断既有装配实现
// （hubSignalFacts 等）；未实现时只是不产出 run_unverified，不影响其它信号。
type UnverifiedRunSource interface {
	UnverifiedRuns() []UnverifiedRunFact
}

// SignalFacts 是 DetectSystemSignals 唯一的证据来源。
type SignalFacts interface {
	// Projects 返回需要判定「是否全员离线」的工程 ID 列表。
	Projects() []string
	// RecentGateFailures 返回最近 submit 的门禁失败证据。
	RecentGateFailures() []GateFailureFact
	// AgentHeartbeats 返回工程内 Agent 的最后心跳与状态。
	AgentHeartbeats() []AgentHeartbeatFact
	// MergingTasks 返回当前停在 merging 的任务证据。
	MergingTasks() []MergingTaskFact
	// HeldLocks 返回仍被占用的租约 / 锁证据。
	HeldLocks() []HeldLockFact
}

// DetectSystemSignals 识别四类系统级信号，是纯函数：只读注入的证据、配置与时间，
// 不改任何状态、不入队、不执行命令。返回的信号是否入队交给 EnqueueSignals 决定。
func DetectSystemSignals(facts SignalFacts, cfg config.SystemAgent, now time.Time) []Signal {
	if facts == nil {
		return nil
	}
	now = now.UTC()
	var out []Signal
	seen := make(map[string]bool)
	emit := func(s Signal) {
		key := s.Key()
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, s)
	}

	// 1) 门禁环境失败：命中环境特征才入队，且 reason 带 needs_human 走升级人工。
	for _, f := range facts.RecentGateFailures() {
		if !gateIsEnvFailure(f) {
			continue
		}
		emit(Signal{
			Kind:      SignalGateEnvFailure,
			ProjectID: f.ProjectID,
			TaskID:    f.TaskID,
			Reason:    "门禁失败疑似环境问题，需人工处理：needs_human",
			JobKind:   SystemKindUnblock,
			Decision:  map[string]any{"action": string(ActionEscalateHuman)},
			Escalate:  true,
		})
	}

	// 2) 全员离线：工程内没有任何状态非 offline 且心跳在窗口内的 Agent。
	// JobKind 为 dispatch_ready 但仅作观察，是否入队由 auto_kinds 放行决定。
	heartbeats := facts.AgentHeartbeats()
	for _, pid := range facts.Projects() {
		alive := false
		for _, hb := range heartbeats {
			if hb.ProjectID == pid && agentAlive(hb, now) {
				alive = true
				break
			}
		}
		if alive {
			continue
		}
		// 预置 action=escalate_human：orchestrator.go:189-192 在 switch job.Kind 之前
		// 对预分类 escalate 直接 finish 为 Escalated，代码层强制跳过 runDispatchJob。
		// 这样「只观察」不依赖「auto_kinds 永不放行 dispatch_ready」这条配置约定：
		// 即便误放行或被显式触发，也不会真派发 / 改任务状态，且与 needs_human 升级口径同源。
		emit(Signal{
			Kind:      SignalAllAgentsOffline,
			ProjectID: pid,
			Reason:    "工程内无在线 Agent（心跳超时或 offline），仅入队观察，不自动改状态",
			JobKind:   SystemKindDispatchReady,
			Decision: map[string]any{
				"action":      string(ActionEscalateHuman),
				"observation": true,
			},
			Escalate: true,
		})
	}

	// 3) merging 停滞：超过阈值且命令在 OpsAllowlist 内才产出；否则不产出。
	for _, m := range facts.MergingTasks() {
		if m.Since.IsZero() || now.Sub(m.Since) <= mergingStallThreshold {
			continue
		}
		if !OpsAllowed(m.OpsCommand, cfg.OpsAllowlist) {
			continue
		}
		emit(Signal{
			Kind:       SignalMergingStalled,
			ProjectID:  m.ProjectID,
			TaskID:     m.TaskID,
			Reason:     fmt.Sprintf("任务处于 merging 超过 %s 未推进，需白名单内运维命令处理", mergingStallThreshold),
			JobKind:    SystemKindOpsRun,
			OpsCommand: m.OpsCommand,
			Decision: map[string]any{
				"action":      string(ActionOpsRun),
				"ops_command": m.OpsCommand,
			},
		})
	}

	// 4) 残留锁：过期且受 unknown 占用保护，只上报（JobKind 为空），不产出 job。
	for _, l := range facts.HeldLocks() {
		if l.AcquiredAt.IsZero() || now.Sub(l.AcquiredAt) <= staleLockAge {
			continue
		}
		if !lockUnknownProtected(l) {
			continue
		}
		emit(Signal{
			Kind:        SignalStaleLock,
			ProjectID:   l.ProjectID,
			ResourceKey: l.ResourceKey,
			Reason:      "残留租约/锁且占用者不明（unknown 保护），仅上报，不自动处置",
			JobKind:     "",
			Decision: map[string]any{
				"report_only":  true,
				"resource_key": l.ResourceKey,
			},
		})
	}
	// 5) 未核实 Run：unknown 一律产出；非终态且心跳超阈值也产出。只入队观察，
	//    真正的释放/保留结论由 ReconcilePort 给出，编排层不按 State 自行分支。
	if src, ok := facts.(UnverifiedRunSource); ok {
		for _, r := range src.UnverifiedRuns() {
			if !runUnverified(r, now) {
				continue
			}
			state := strings.ToLower(strings.TrimSpace(r.State))
			decision := map[string]any{
				"run_id":          r.RunID,
				"reconcile_state": state,
				"observation":     true,
			}
			escalate := state == "unknown"
			if escalate {
				// 契约：unknown 行「mode=keep（信号预分类为 escalate）」，
				// 编排层先路由 run_reconcile 再处理预分类，故不会绕过核实。
				decision["action"] = string(ActionEscalateHuman)
			}
			emit(Signal{
				Kind:        SignalRunUnverified,
				ProjectID:   r.ProjectID,
				TaskID:      r.TaskID,
				ResourceKey: r.RunID,
				Reason: fmt.Sprintf("Run %s 处于 %s 且超过 %s 未核实，入队核实并只按端口结论处置",
					r.RunID, state, runUnverifiedWindow),
				JobKind:  SystemKindRunReconcile,
				Decision: decision,
				Escalate: escalate,
			})
		}
	}
	return out
}

// runUnverified 判定一条 Run 证据是否属于「未核实的占用」。
//
// 本轮已核实过的一律跳过；finished 是确定结束，不产出；unknown 无现场可依，
// 直接产出；其余非终态按最后更新是否超过阈值判定（零值按证据不足处理）。
func runUnverified(f UnverifiedRunFact, now time.Time) bool {
	if !f.CheckedAt.IsZero() && now.Sub(f.CheckedAt) <= runUnverifiedWindow {
		return false
	}
	state := strings.ToLower(strings.TrimSpace(f.State))
	if state == "finished" {
		return false
	}
	if state == "unknown" {
		return true
	}
	if f.LastSeen.IsZero() {
		return true
	}
	return now.Sub(f.LastSeen) > runUnverifiedWindow
}

// agentAlive 报告 Agent 是否算「在线」：状态非 offline 且心跳在窗口内。
func agentAlive(hb AgentHeartbeatFact, now time.Time) bool {
	if strings.EqualFold(strings.TrimSpace(hb.Status), "offline") {
		return false
	}
	if hb.LastSeen.IsZero() {
		return false
	}
	return now.Sub(hb.LastSeen) <= agentHeartbeatWindow
}

// lockUnknownProtected 报告残留锁是否受 unknown 占用保护：占用者为空或身份不明。
func lockUnknownProtected(l HeldLockFact) bool {
	return l.UnknownHolder || strings.TrimSpace(l.Holder) == ""
}

// gateIsEnvFailure 判定一次门禁失败是否为环境失败：事实源已判定则直接采信，
// 否则跑词表。
func gateIsEnvFailure(f GateFailureFact) bool {
	if f.EnvFailure {
		return true
	}
	return isEnvFailureText(f.Output)
}

// isEnvFailureText 复用 gitops.IsEnvFailure，并补齐它词表未覆盖的沙箱/权限特征
// （/bin/ps、operation not permitted、EPERM：见项目内 IsEnvFailure 已知缺口）。
func isEnvFailureText(s string) bool {
	if gitops.IsEnvFailure(s) {
		return true
	}
	low := strings.ToLower(s)
	for _, n := range []string{"/bin/ps", "operation not permitted", "eperm"} {
		if strings.Contains(low, n) {
			return true
		}
	}
	return false
}

// SignalCooldown 返回信号入队的冷却时长；与 ingestBlocked 一致，缺省 5 分钟。
func SignalCooldown(cfg config.SystemAgent) time.Duration {
	if cfg.CooldownoutSec <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(cfg.CooldownoutSec) * time.Second
}

// WithinCooldown 报告距同 key 上次入队是否仍在冷却窗口内（纯函数，便于假时间测）。
// last 为零值表示从未入队；cooldown<=0 表示不冷却；时钟回拨（now<last）保守判冷却。
func WithinCooldown(last, now time.Time, cooldown time.Duration) bool {
	if cooldown <= 0 || last.IsZero() {
		return false
	}
	return now.Sub(last) < cooldown
}

// EnqueueSignals 把信号经现有 Enqueue 入队，是给装配卡 / Watcher 用的帮助函数。
//
// 默认只入队观察：
//   - JobKind 为空的信号（如 stale_lock）只上报，不产出 job；
//   - auto_kinds 未放行（KindAllowed=false）的 kind 不入队；
//   - 同 signal key 在 cooldown 内不重复入队；
//   - needs_human / auth_required 类信号写 action=escalate_human，由编排层升级人工，
//     不自动处置；max_attempts 由 Enqueue 从 cfg 带入。
func EnqueueSignals(tx *sql.Tx, cfg config.SystemAgent, signals []Signal, now time.Time) ([]*db.SystemJob, error) {
	cooldown := SignalCooldown(cfg)
	last, err := recentSignalTimes(tx)
	if err != nil {
		return nil, err
	}
	var out []*db.SystemJob
	for _, s := range signals {
		if s.JobKind == "" {
			continue
		}
		if !KindAllowed(s.JobKind, cfg, false) {
			continue
		}
		if WithinCooldown(last[s.Key()], now, cooldown) {
			continue
		}
		decision := map[string]any{
			"signal_key":  s.Key(),
			"signal_kind": string(s.Kind),
		}
		for k, v := range s.Decision {
			decision[k] = v
		}
		if s.Escalate {
			decision["action"] = string(ActionEscalateHuman)
		}
		job, err := Enqueue(tx, cfg, s.ProjectID, s.JobKind, s.TaskID, "", s.Reason, decision, false)
		if err != nil {
			return out, err
		}
		if job != nil {
			out = append(out, job)
			// 本次已入队：即便同一批里出现同 key 也不再重复。
			last[s.Key()] = now
		}
	}
	return out, nil
}

// recentSignalTimes 从最近的系统作业里还原「signal key -> 上次入队时间」。
// signal key 是入队时写进 decision.signal_key 的，老路径（task.blocked）没有该字段，
// 会被自然忽略，不参与信号冷却。
func recentSignalTimes(tx *sql.Tx) (map[string]time.Time, error) {
	jobs, err := db.ListSystemJobs(tx, "", "", 200)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time)
	for _, j := range jobs {
		key, _ := j.Decision["signal_key"].(string)
		if key == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, j.CreatedAt)
		if err != nil {
			continue
		}
		if prev, ok := out[key]; !ok || ts.After(prev) {
			out[key] = ts
		}
	}
	return out, nil
}
