package model

import (
	"fmt"
	"time"
)

// RuntimeContract 是运行状态与资源契约的标识（E201）。
const RuntimeContract = "maw.runtime.state/v1"

// OperationKind 是持久副作用操作的种类。
type OperationKind string

// 派发／取消／验证。
const (
	OpDispatch OperationKind = "dispatch"
	OpCancel   OperationKind = "cancel"
	OpVerify   OperationKind = "verify"
)

// Valid 报告取值是否合法。
func (k OperationKind) Valid() bool { return k == OpDispatch || k == OpCancel || k == OpVerify }

// OperationResult 是操作的结果结论；already_finished 是取消竞争中「结束事实先到」的合法结果。
type OperationResult string

// 四种操作结果。
const (
	ResultPending         OperationResult = "pending"
	ResultApplied         OperationResult = "applied"
	ResultAlreadyFinished OperationResult = "already_finished"
	ResultRejected        OperationResult = "rejected"
)

// Valid 报告取值是否合法。
func (r OperationResult) Valid() bool {
	switch r {
	case ResultPending, ResultApplied, ResultAlreadyFinished, ResultRejected:
		return true
	}
	return false
}

// PhaseEntry 是阶段历史的一条记录；时间是发生时间，必须严格递增（E223）。
type PhaseEntry struct {
	Phase  RunPhase  `json:"phase"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
}

// Lease 是一次资源占用。释放是条件写入：release_generation 必须等于 generation（E243）。
type Lease struct {
	LeaseID           LeaseID     `json:"lease_id"`
	Kind              LeaseKind   `json:"kind"`
	Generation        Generation  `json:"generation"`
	Released          bool        `json:"released"`
	ReleaseGeneration *Generation `json:"release_generation,omitempty"`
	ReleaseEventID    *EventID    `json:"release_event_id,omitempty"`
}

// Event 是一条标准事实：身份唯一、游标按接收排序、必须带 schema_version。
type Event struct {
	EventID       EventID   `json:"event_id"`
	Cursor        Cursor    `json:"cursor"`
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"` // 例如 run.phase、lease.released
	RunID         RunID     `json:"run_id,omitempty"`
	Phase         *RunPhase `json:"phase,omitempty"`
	LeaseID       *LeaseID  `json:"lease_id,omitempty"`
	OccurredAt    time.Time `json:"occurred_at"`
	ReceivedAt    time.Time `json:"received_at"`
	Quality       Quality   `json:"quality"`
}

// Operation 是一次持久副作用请求。幂等键相同但请求摘要不同即冲突（E252）。
type Operation struct {
	OperationID    OperationID     `json:"operation_id"`
	Kind           OperationKind   `json:"kind"`
	RunID          RunID           `json:"run_id,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	RequestDigest  string          `json:"request_digest,omitempty"`
	TargetPhase    RunPhase        `json:"target_phase,omitempty"`
	Result         OperationResult `json:"result"`
}

// AutoExecution 默认关闭；开启时必须给出正整数上限（E270）。
type AutoExecution struct {
	Enabled               bool `json:"enabled"`
	ManagedExecutionLimit int  `json:"managed_execution_limit"`
}

// Run 是一次执行尝试。业务任务状态不在这里——Run completed 不等于任务 done。
type Run struct {
	RunID    RunID      `json:"run_id"`
	Instance InstanceID `json:"task_instance"`
	Version  VersionNo  `json:"task_version"`
	// Generation 从 1 开始单调递增；旧代际的写入一律被拒。
	Generation Generation `json:"generation"`

	Phase        RunPhase     `json:"phase"`
	PhaseHistory []PhaseEntry `json:"phase_history"`

	Reconciliation       ReconciliationState `json:"reconciliation_state"`
	DesiredAction        DesiredAction       `json:"desired_action"`
	ProcessStopConfirmed bool                `json:"process_stop_confirmed"`
	// DeliveryFrozen 为真才允许释放工作目录（E242）。
	DeliveryFrozen bool `json:"delivery_frozen"`

	Leases     []Lease    `json:"leases"`
	Evidence   []string   `json:"evidence,omitempty"` // 终态证据引用（E230）
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Occupies 报告该 Run 是否仍计入资源占用（runtime-state-contract-v1 §4.1）。
func (r *Run) Occupies() bool {
	if !r.Phase.Terminal() {
		return true
	}
	if r.Reconciliation == ReconciliationUnknown {
		return true
	}
	if (r.Phase == PhaseFailed || r.Phase == PhaseCancelled) && !r.ProcessStopConfirmed {
		return true
	}
	return false
}

// RuntimeSnapshot 是某工程某一时刻的运行状态快照。
type RuntimeSnapshot struct {
	Contract      string        `json:"contract"`
	ProjectID     ProjectID     `json:"project_id"`
	GeneratedAt   time.Time     `json:"generated_at"`
	AutoExecution AutoExecution `json:"auto_execution"`
	Runs          []Run         `json:"runs"`
	Operations    []Operation   `json:"operations"`
	Events        []Event       `json:"events"`
}

// Validate 按 runtime-state-contract-v1 §9 的不变量逐条检查。
func (s RuntimeSnapshot) Validate() []*Error {
	var out []*Error
	if s.Contract != RuntimeContract {
		out = append(out, Violationf(CodeRuntimeContractUnknown, "contract",
			"必须是 %s，收到 %q", RuntimeContract, s.Contract))
	}
	if !s.ProjectID.Valid() {
		out = append(out, Violation(CodeRuntimeFieldInvalid, "project_id", "必须是 16 位小写十六进制"))
	}
	if s.GeneratedAt.IsZero() {
		out = append(out, Violation(CodeRuntimeFieldInvalid, "generated_at", "必须是可解析的 ISO8601 时间"))
	}
	if s.AutoExecution.Enabled && s.AutoExecution.ManagedExecutionLimit < 1 {
		out = append(out, Violation(CodeAutoExecutionLimit, "auto_execution.managed_execution_limit",
			"开启自动执行时必须配置正整数上限"))
	}

	byRun := make(map[RunID]*Run, len(s.Runs))
	occupancy := make(map[InstanceID]int, len(s.Runs))
	for i := range s.Runs {
		run := &s.Runs[i]
		if _, dup := byRun[run.RunID]; dup {
			out = append(out, Violationf(CodeRunDuplicate, fmt.Sprintf("runs[%d].run_id", i), "%q 重复", run.RunID))
		} else {
			byRun[run.RunID] = run
		}
		if run.Occupies() {
			occupancy[run.Instance]++
		}
		out = append(out, run.validate(fmt.Sprintf("runs[%d]", i))...)
	}

	for inst, n := range occupancy {
		if n > 1 {
			out = append(out, Violationf(CodeDoubleOccupancy, "runs",
				"任务实例 %s 有 %d 个有效占用（含 unknown 与进程未确认停止）", inst, n))
		}
	}

	out = append(out, s.validateOperations(byRun)...)
	out = append(out, s.validateEvents(byRun)...)
	return out
}

func (r *Run) validate(p string) []*Error {
	var out []*Error
	if !r.RunID.Valid() {
		out = append(out, Violation(CodeRuntimeFieldInvalid, p+".run_id", "必须形如 run_<小写字母数字，至少 3 位>"))
	}
	if !r.Instance.Valid() {
		out = append(out, Violation(CodeRuntimeFieldInvalid, p+".task_instance", "任务实例身份非法"))
	}
	if !r.Version.Valid() {
		out = append(out, Violation(CodeRuntimeFieldInvalid, p+".task_version", "版本号必须从 1 开始"))
	}
	if !r.Generation.Valid() {
		out = append(out, Violation(CodeRuntimeFieldInvalid, p+".generation", "代际必须从 1 开始"))
	}
	if !r.Reconciliation.Valid() {
		out = append(out, Violationf(CodeRuntimeFieldInvalid, p+".reconciliation_state", "只能是 confirmed 或 unknown，收到 %q", r.Reconciliation))
	}
	if r.Phase != "" && !r.Phase.Valid() {
		out = append(out, Violationf(CodePhaseUnknown, p+".phase", "不属于十个阶段：%q", r.Phase))
	}
	if r.DesiredAction != "" && !r.DesiredAction.Valid() {
		out = append(out, Violationf(CodeRuntimeFieldInvalid, p+".desired_action", "只能是 none 或 stop，收到 %q", r.DesiredAction))
	}

	out = append(out, r.validatePhaseHistory(p)...)

	// E230 终态证据；E231 取消必须已确认停止；E232 unknown 不得落终态。
	switch r.Phase {
	case PhaseFailed, PhaseCancelled, PhaseTimedOut:
		if len(r.Evidence) == 0 {
			out = append(out, Violationf(CodeTerminalWithoutProof, p+".evidence", "%s 必须带证据", r.Phase))
		}
	}
	if r.Phase == PhaseCancelled && !r.ProcessStopConfirmed {
		out = append(out, Violation(CodeCancelledUnconfirmed, p+".process_stop_confirmed",
			"cancel 是意图、cancelled 是事实，中间必须隔着停止确认"))
	}
	if r.Reconciliation == ReconciliationUnknown && r.Phase.Terminal() {
		out = append(out, Violation(CodeUnknownTerminal, p+".phase",
			"观测缺失时不得落终态，应保留占用并标注待核实"))
	}
	if r.StartedAt != nil && r.FinishedAt != nil && r.FinishedAt.Before(*r.StartedAt) {
		out = append(out, Violation(CodeTimeOrder, p+".finished_at", "结束不得早于开始"))
	}

	// 占用释放条件（E241、E242、E243）。
	for i := range r.Leases {
		l := &r.Leases[i]
		lp := fmt.Sprintf("%s.leases[%d]", p, i)
		if !l.LeaseID.Valid() {
			out = append(out, Violation(CodeRuntimeFieldInvalid, lp+".lease_id", "必须形如 ls_<小写字母数字，至少 3 位>"))
		}
		if !l.Kind.Valid() {
			out = append(out, Violationf(CodeRuntimeFieldInvalid, lp+".kind", "只能是 node_slot/workspace/merge，收到 %q", l.Kind))
		}
		if !l.Released {
			continue
		}
		if l.Kind == LeaseNodeSlot && r.Occupies() {
			out = append(out, Violation(CodeSlotReleasedBusy, lp, "运行仍计入占用时不得释放节点槽"))
		}
		if l.Kind == LeaseWorkspace && !r.DeliveryFrozen {
			out = append(out, Violation(CodeWorkspaceNotFrozen, lp, "产物未冻结就释放工作目录"))
		}
		if l.ReleaseGeneration == nil || *l.ReleaseGeneration != l.Generation {
			out = append(out, Violationf(CodeReleaseGeneration, lp+".release_generation",
				"必须等于持有代际 %d", l.Generation))
		}
	}
	return out
}

func (r *Run) validatePhaseHistory(p string) []*Error {
	var out []*Error
	if len(r.PhaseHistory) == 0 {
		return append(out, Violation(CodeRuntimeFieldInvalid, p+".phase_history", "阶段历史至少一条"))
	}
	if first := r.PhaseHistory[0].Phase; first != PhaseReserved {
		out = append(out, Violationf(CodePhaseTransitionIllegal, p+".phase_history[0].phase",
			"起点必须是 reserved，收到 %q", first))
	}
	terminalAt := -1
	for i, entry := range r.PhaseHistory {
		ep := fmt.Sprintf("%s.phase_history[%d]", p, i)
		if !entry.Phase.Valid() {
			out = append(out, Violationf(CodePhaseUnknown, ep+".phase", "不属于十个阶段：%q", entry.Phase))
			continue
		}
		if entry.At.IsZero() {
			out = append(out, Violation(CodeRuntimeFieldInvalid, ep+".at", "必须是可解析的 ISO8601 时间"))
		}
		if i > 0 {
			prev := r.PhaseHistory[i-1]
			if prev.Phase.Valid() && !CanTransition(prev.Phase, entry.Phase) {
				out = append(out, Violationf(CodePhaseTransitionIllegal, ep,
					"%s → %s 非法（终态不再出发任何转移）", prev.Phase, entry.Phase))
			}
			if !prev.At.IsZero() && !entry.At.IsZero() && !entry.At.After(prev.At) {
				out = append(out, Violation(CodeTimeOrder, ep+".at", "阶段时间必须严格递增"))
			}
		}
		if entry.Phase.Terminal() && terminalAt < 0 {
			terminalAt = i
		}
	}
	if r.Phase.Valid() {
		if last := r.PhaseHistory[len(r.PhaseHistory)-1].Phase; last != r.Phase {
			out = append(out, Violationf(CodePhaseHistoryMismatch, p+".phase",
				"历史末项 %q 与当前阶段 %q 不一致", last, r.Phase))
		}
	}
	if terminalAt >= 0 && terminalAt < len(r.PhaseHistory)-1 {
		if r.Phase == PhaseCancelled {
			out = append(out, Violation(CodeTerminalRewritten, p+".phase",
				"已确认终态之后不得追改成 cancelled"))
		}
	}
	return out
}

func (s RuntimeSnapshot) validateOperations(byRun map[RunID]*Run) []*Error {
	var out []*Error
	seenID := make(map[OperationID]bool, len(s.Operations))
	seenKey := make(map[string]string, len(s.Operations)) // key → digest
	for i, op := range s.Operations {
		p := fmt.Sprintf("operations[%d]", i)
		if seenID[op.OperationID] {
			out = append(out, Violationf(CodeOperationDuplicate, p+".operation_id", "%q 重复", op.OperationID))
		}
		seenID[op.OperationID] = true
		if !op.OperationID.Valid() {
			out = append(out, Violation(CodeRuntimeFieldInvalid, p+".operation_id", "必须形如 op_<小写字母数字，至少 3 位>"))
		}
		if !op.Kind.Valid() {
			out = append(out, Violationf(CodeRuntimeFieldInvalid, p+".kind", "只能是 dispatch/cancel/verify，收到 %q", op.Kind))
		}
		if op.Result != "" && !op.Result.Valid() {
			out = append(out, Violationf(CodeRuntimeFieldInvalid, p+".result", "未知操作结果 %q", op.Result))
		}
		if op.IdempotencyKey != "" {
			if prev, ok := seenKey[op.IdempotencyKey]; ok && prev != op.RequestDigest {
				out = append(out, Violationf(CodeIdempotencyConflict, p+".idempotency_key",
					"同一幂等键出现不同请求摘要，不得复用旧结果"))
			}
			seenKey[op.IdempotencyKey] = op.RequestDigest
		}
		// E251：对已结束运行的取消必须如实返回 already_finished。
		if op.Kind == OpCancel {
			if run, ok := byRun[op.RunID]; ok && run.Phase.Finished() && op.Result != ResultAlreadyFinished {
				out = append(out, Violationf(CodeCancelNotReported, p+".result",
					"运行已是 %s，取消必须返回 already_finished 并保留原结果", run.Phase))
			}
		}
	}
	return out
}

func (s RuntimeSnapshot) validateEvents(byRun map[RunID]*Run) []*Error {
	var out []*Error
	seenID := make(map[EventID]bool, len(s.Events))
	var prev Cursor
	for i, ev := range s.Events {
		p := fmt.Sprintf("events[%d]", i)
		if seenID[ev.EventID] {
			out = append(out, Violationf(CodeEventDuplicate, p+".event_id", "%q 重复", ev.EventID))
		}
		seenID[ev.EventID] = true
		if !ev.EventID.Valid() {
			out = append(out, Violation(CodeRuntimeFieldInvalid, p+".event_id", "必须形如 evt_<小写字母数字，至少 3 位>"))
		}
		// 游标表达入库顺序，必须严格递增（E213）。
		if prev != 0 && ev.Cursor <= prev {
			out = append(out, Violationf(CodeCursorNotIncreasing, p+".cursor",
				"必须严格大于前一条的 %d，收到 %d", prev, ev.Cursor))
		}
		prev = ev.Cursor
		if ev.SchemaVersion < 1 {
			out = append(out, Violation(CodeSchemaVersionMissing, p+".schema_version", "必须携带正整数契约版本"))
		}
		if !ev.Quality.Valid() {
			out = append(out, Violationf(CodeRuntimeFieldInvalid, p+".quality", "只能是 observed/estimated/unknown，收到 %q", ev.Quality))
		}
		if !ev.OccurredAt.IsZero() && !ev.ReceivedAt.IsZero() &&
			ev.OccurredAt.After(ev.ReceivedAt) && ev.Quality == QualityObserved {
			out = append(out, Violation(CodeClockSkewMarkedObserved, p+".quality",
				"发生晚于接收说明来源时钟不可信，必须降级为 estimated/unknown"))
		}
		if ev.RunID != "" {
			if _, ok := byRun[ev.RunID]; !ok {
				out = append(out, Violationf(CodeEventUnknownRun, p+".run_id", "引用了不存在的运行 %q", ev.RunID))
			}
		}
	}

	// E260：当前阶段必须有同批 run.phase 事件；E261：释放必须有 lease.released 事件。
	for i := range s.Runs {
		run := &s.Runs[i]
		if run.Phase == "" {
			continue
		}
		if !hasPhaseEvent(s.Events, run) {
			out = append(out, Violationf(CodePhaseEventMissing, fmt.Sprintf("runs[%d].phase", i),
				"当前阶段 %s 缺少同事务提交的 run.phase 事件", run.Phase))
		}
		for j := range run.Leases {
			l := &run.Leases[j]
			if !l.Released {
				continue
			}
			if !hasReleaseEvent(s.Events, l) {
				out = append(out, Violationf(CodeReleaseEventMissing,
					fmt.Sprintf("runs[%d].leases[%d]", i, j), "释放必须留下 lease.released 事件"))
			}
		}
	}
	return out
}

func hasPhaseEvent(events []Event, run *Run) bool {
	for _, ev := range events {
		if ev.RunID == run.RunID && ev.Kind == "run.phase" && ev.Phase != nil && *ev.Phase == run.Phase {
			return true
		}
	}
	return false
}

func hasReleaseEvent(events []Event, l *Lease) bool {
	for _, ev := range events {
		if ev.Kind != "lease.released" || ev.LeaseID == nil || *ev.LeaseID != l.LeaseID {
			continue
		}
		if l.ReleaseEventID == nil || *l.ReleaseEventID == ev.EventID {
			return true
		}
	}
	return false
}
