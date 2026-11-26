package model

// TaskStatus 是业务任务状态，封闭集合，沿用现有词汇（ARCHITECTURE_VNEXT §7.1）。
//
// 本包只负责「是不是这十一个值」，**合法转移矩阵的权威仍是 internal/machine**；
// VN-202 会把转移规则迁到 internal/engine/state 并在此登记。这里不复制一份转移表，
// 避免出现两套互相矛盾的规则。
type TaskStatus string

// 十一个业务任务状态。
const (
	TaskBacklog    TaskStatus = "backlog"
	TaskReady      TaskStatus = "ready"
	TaskClaimed    TaskStatus = "claimed"
	TaskInProgress TaskStatus = "in_progress"
	TaskSelfCheck  TaskStatus = "self_check"
	TaskInReview   TaskStatus = "in_review"
	TaskMerging    TaskStatus = "merging"
	TaskConflict   TaskStatus = "conflict"
	TaskBlocked    TaskStatus = "blocked"
	TaskDone       TaskStatus = "done"
	TaskCancelled  TaskStatus = "cancelled"
)

var taskStatuses = map[TaskStatus]bool{
	TaskBacklog: true, TaskReady: true, TaskClaimed: true, TaskInProgress: true,
	TaskSelfCheck: true, TaskInReview: true, TaskMerging: true, TaskConflict: true,
	TaskBlocked: true, TaskDone: true, TaskCancelled: true,
}

// Valid 报告该状态是否属于封闭集合。
func (s TaskStatus) Valid() bool { return taskStatuses[s] }

// Terminal 报告是否为业务终态：done/cancelled 不再参与依赖满足。
func (s TaskStatus) Terminal() bool { return s == TaskDone || s == TaskCancelled }

// IsActiveOwner 报告该状态是否意味着仍有工作者持有任务（写盘序位仍被占用）。
func (s TaskStatus) IsActiveOwner() bool {
	switch s {
	case TaskClaimed, TaskInProgress, TaskSelfCheck, TaskInReview, TaskMerging, TaskConflict:
		return true
	}
	return false
}

// AllTaskStatuses 返回封闭集合，供对齐测试与看板枚举使用。
func AllTaskStatuses() []TaskStatus {
	return []TaskStatus{
		TaskBacklog, TaskReady, TaskClaimed, TaskInProgress, TaskSelfCheck,
		TaskInReview, TaskMerging, TaskConflict, TaskBlocked, TaskDone, TaskCancelled,
	}
}

// RunPhase 是运行阶段，十个值封闭（runtime-state-contract-v1 §2）。
type RunPhase string

// 十个运行阶段与四个终态。
const (
	PhaseReserved  RunPhase = "reserved"
	PhasePreparing RunPhase = "preparing"
	PhaseLaunching RunPhase = "launching"
	PhaseRunning   RunPhase = "running"
	PhaseWaiting   RunPhase = "waiting"
	PhaseStopping  RunPhase = "stopping"
	PhaseCompleted RunPhase = "completed"
	PhaseFailed    RunPhase = "failed"
	PhaseCancelled RunPhase = "cancelled"
	PhaseTimedOut  RunPhase = "timed_out"
)

// phaseTransitions 是契约 §2 的转移表；终态不出现在键里，因此不可能再出发。
var phaseTransitions = map[RunPhase]map[RunPhase]bool{
	PhaseReserved:  {PhasePreparing: true, PhaseStopping: true},
	PhasePreparing: {PhaseLaunching: true, PhaseStopping: true, PhaseFailed: true},
	PhaseLaunching: {PhaseRunning: true, PhaseStopping: true, PhaseFailed: true},
	PhaseRunning:   {PhaseWaiting: true, PhaseStopping: true, PhaseCompleted: true, PhaseFailed: true},
	PhaseWaiting:   {PhaseRunning: true, PhaseStopping: true, PhaseCompleted: true, PhaseFailed: true},
	PhaseStopping:  {PhaseCancelled: true, PhaseTimedOut: true},
}

// Valid 报告阶段是否属于十个封闭值。
func (p RunPhase) Valid() bool {
	if _, ok := phaseTransitions[p]; ok {
		return true
	}
	switch p {
	case PhaseCompleted, PhaseFailed, PhaseCancelled, PhaseTimedOut:
		return true
	}
	return false
}

// Terminal 报告是否为终态（不再出发任何转移）。
func (p RunPhase) Terminal() bool {
	switch p {
	case PhaseCompleted, PhaseFailed, PhaseCancelled, PhaseTimedOut:
		return true
	}
	return false
}

// Finished 报告是否「有结果但不算取消」：completed/failed/timed_out。
// 取消竞争里这三个阶段先到，取消必须认输（E250、E251）。
func (p RunPhase) Finished() bool {
	switch p {
	case PhaseCompleted, PhaseFailed, PhaseTimedOut:
		return true
	}
	return false
}

// CanTransition 报告 src → dst 是否合法；从终态出发一律非法。
func CanTransition(src, dst RunPhase) bool {
	if !src.Valid() || !dst.Valid() {
		return false
	}
	return phaseTransitions[src][dst]
}

// AllRunPhases 返回十个阶段。
func AllRunPhases() []RunPhase {
	return []RunPhase{
		PhaseReserved, PhasePreparing, PhaseLaunching, PhaseRunning, PhaseWaiting,
		PhaseStopping, PhaseCompleted, PhaseFailed, PhaseCancelled, PhaseTimedOut,
	}
}

// ReconciliationState 是核实状态：我们有多确定「它现在到底是什么样」。
type ReconciliationState string

// 两种核实状态。unknown 不得直接落终态（E232）。
const (
	ReconciliationConfirmed ReconciliationState = "confirmed"
	ReconciliationUnknown   ReconciliationState = "unknown"
)

// Valid 报告取值是否合法。
func (r ReconciliationState) Valid() bool {
	return r == ReconciliationConfirmed || r == ReconciliationUnknown
}

// DesiredAction 是期望动作：表达意图，不代表已发生。
type DesiredAction string

// 无期望动作／请求停止。
const (
	ActionNone DesiredAction = "none"
	ActionStop DesiredAction = "stop"
)

// Valid 报告取值是否合法。
func (a DesiredAction) Valid() bool { return a == ActionNone || a == ActionStop }

// Quality 是证据质量。发生晚于接收说明时钟不可信，必须降级（E262）。
type Quality string

// 三种证据质量。
const (
	QualityObserved  Quality = "observed"
	QualityEstimated Quality = "estimated"
	QualityUnknown   Quality = "unknown"
)

// Valid 报告取值是否合法。
func (q Quality) Valid() bool {
	return q == QualityObserved || q == QualityEstimated || q == QualityUnknown
}

// LeaseKind 是三类彼此独立的资源（runtime-state-contract-v1 §4）。
type LeaseKind string

// 节点槽／工作目录／合入位。
const (
	LeaseNodeSlot  LeaseKind = "node_slot"
	LeaseWorkspace LeaseKind = "workspace"
	LeaseMerge     LeaseKind = "merge"
)

// Valid 报告取值是否合法。
func (l LeaseKind) Valid() bool {
	return l == LeaseNodeSlot || l == LeaseWorkspace || l == LeaseMerge
}

// DeliveryMode 决定完成条件走哪条路径（E350、E351）。
type DeliveryMode string

// 代码任务／非代码任务。
const (
	DeliveryCode    DeliveryMode = "code"
	DeliveryNonCode DeliveryMode = "non_code"
)

// Valid 报告取值是否合法。
func (m DeliveryMode) Valid() bool { return m == DeliveryCode || m == DeliveryNonCode }

// ArtifactKind 是产物类型。
type ArtifactKind string

// 提交／文件／内容哈希。
const (
	ArtifactCommit      ArtifactKind = "commit"
	ArtifactFile        ArtifactKind = "file"
	ArtifactContentHash ArtifactKind = "content_hash"
)

// Valid 报告取值是否合法。
func (k ArtifactKind) Valid() bool {
	return k == ArtifactCommit || k == ArtifactFile || k == ArtifactContentHash
}

// Declarer 说明产物是谁声明的：cli 只是候选，manual 才是人确认过的。
type Declarer string

// CLI 声明／人工确认。
const (
	DeclaredByCLI    Declarer = "cli"
	DeclaredByManual Declarer = "manual"
)

// Valid 报告取值是否合法。
func (d Declarer) Valid() bool { return d == DeclaredByCLI || d == DeclaredByManual }

// VerificationResult 是验证结论等级。
type VerificationResult string

// 通过／人工验收／失败／待定／失效。
const (
	VerifyPass    VerificationResult = "pass"
	VerifyManual  VerificationResult = "manual"
	VerifyFail    VerificationResult = "fail"
	VerifyPending VerificationResult = "pending"
	VerifyStale   VerificationResult = "stale"
)

// Valid 报告取值是否合法。
func (r VerificationResult) Valid() bool {
	switch r {
	case VerifyPass, VerifyManual, VerifyFail, VerifyPending, VerifyStale:
		return true
	}
	return false
}

// Verdict 是评审结论。
type Verdict string

// 通过／要求修改／不需要评审。
const (
	VerdictApproved        Verdict = "approved"
	VerdictChangesRequired Verdict = "changes_requested"
	VerdictNotRequired     Verdict = "not_required"
)

// Valid 报告取值是否合法。
func (v Verdict) Valid() bool {
	return v == VerdictApproved || v == VerdictChangesRequired || v == VerdictNotRequired
}

// Decision 是完成判定结论。
type Decision string

// 完成／待定／拒绝。
const (
	DecisionDone     Decision = "done"
	DecisionPending  Decision = "pending"
	DecisionRejected Decision = "rejected"
)

// Valid 报告取值是否合法。
func (d Decision) Valid() bool {
	return d == DecisionDone || d == DecisionPending || d == DecisionRejected
}

// EntryState 是节点入口与运行时的观测状态（§5.1 第一维）。
type EntryState string

// 就绪／缺失／不兼容／未知。
const (
	EntryReady        EntryState = "ready"
	EntryMissing      EntryState = "missing"
	EntryIncompatible EntryState = "incompatible"
	EntryUnknown      EntryState = "unknown"
)

// Valid 报告取值是否合法。
func (e EntryState) Valid() bool {
	switch e {
	case EntryReady, EntryMissing, EntryIncompatible, EntryUnknown:
		return true
	}
	return false
}

// ReadinessState 是认证／连接／容量维度上的就绪取值。
type ReadinessState string

// 通用就绪取值集合，各维度取其中子集。
const (
	ReadyOK           ReadinessState = "ready"
	ReadyNeedsLogin   ReadinessState = "needs-login"
	ReadyNeedsModel   ReadinessState = "needs-model"
	ReadyFresh        ReadinessState = "fresh"
	ReadyStale        ReadinessState = "stale"
	ReadyDisconnected ReadinessState = "disconnected"
	ReadyAvailable    ReadinessState = "available"
	ReadyOccupied     ReadinessState = "occupied"
	ReadyDraining     ReadinessState = "draining"
	ReadyDisabled     ReadinessState = "disabled"
	ReadyUnknown      ReadinessState = "unknown"
)

// Valid 报告取值是否属于已知就绪词汇。
func (r ReadinessState) Valid() bool {
	switch r {
	case ReadyOK, ReadyNeedsLogin, ReadyNeedsModel, ReadyFresh, ReadyStale,
		ReadyDisconnected, ReadyAvailable, ReadyOccupied, ReadyDraining,
		ReadyDisabled, ReadyUnknown:
		return true
	}
	return false
}

// AcceptsNewWork 报告该容量状态是否允许接收新任务；draining 不接新但打断不了已有执行。
func (r ReadinessState) AcceptsNewWork() bool { return r == ReadyAvailable }

// CapabilityState 是能力状态，与 docs/evidence/codebuddy/capability-matrix.json 同构。
type CapabilityState string

// 已观测／未观测／未知／不支持。
const (
	CapabilityObserved    CapabilityState = "observed"
	CapabilityUnobserved  CapabilityState = "unobserved"
	CapabilityUnknown     CapabilityState = "unknown"
	CapabilityUnsupported CapabilityState = "unsupported"
)

// Valid 报告取值是否合法。
func (c CapabilityState) Valid() bool {
	switch c {
	case CapabilityObserved, CapabilityUnobserved, CapabilityUnknown, CapabilityUnsupported:
		return true
	}
	return false
}

// AutoMatchable 报告该能力能否参与自动匹配：只有 observed 可以（§5）。
func (c CapabilityState) AutoMatchable() bool { return c == CapabilityObserved }

// NodeMode 是接入模式。
type NodeMode string

// 受管 CLI／人工客户端／仅观测。
const (
	NodeManagedCLI   NodeMode = "managed_cli"
	NodeManualClient NodeMode = "manual_client"
	NodeObservedOnly NodeMode = "observed_only"
)

// Valid 报告取值是否合法。
func (m NodeMode) Valid() bool {
	return m == NodeManagedCLI || m == NodeManualClient || m == NodeObservedOnly
}

// Layer 是四层之一（P110）。
type Layer string

// 用例／端口／适配器／基础设施。
const (
	LayerApp     Layer = "app"
	LayerPort    Layer = "port"
	LayerAdapter Layer = "adapter"
	LayerInfra   Layer = "infra"
)

// Valid 报告取值是否合法。
func (l Layer) Valid() bool {
	return l == LayerApp || l == LayerPort || l == LayerAdapter || l == LayerInfra
}

// SideEffect 是调用可能产生的作用；事务边界按它判定（P132、P133）。
type SideEffect string

// 已知作用集合，与 ports_contract.KNOWN_EFFECTS 逐字一致。
const (
	EffectDBWrite         SideEffect = "db_write"
	EffectEventAppend     SideEffect = "event_append"
	EffectTaskStatusWrite SideEffect = "task_status_write"
	EffectGitWrite        SideEffect = "git_write"
	EffectProcessSpawn    SideEffect = "process_spawn"
	EffectProcessStop     SideEffect = "process_stop"
	EffectNetwork         SideEffect = "network"
	EffectFSWrite         SideEffect = "fs_write"
)

// Valid 报告取值是否合法。
func (e SideEffect) Valid() bool {
	switch e {
	case EffectDBWrite, EffectEventAppend, EffectTaskStatusWrite, EffectGitWrite,
		EffectProcessSpawn, EffectProcessStop, EffectNetwork, EffectFSWrite:
		return true
	}
	return false
}

// External 报告该作用是否触碰外部世界（事务内禁止，P132）。
func (e SideEffect) External() bool {
	switch e {
	case EffectProcessSpawn, EffectProcessStop, EffectGitWrite, EffectNetwork, EffectFSWrite:
		return true
	}
	return false
}

// WritesBusinessStatus 报告该作用是否写业务任务状态（事务外禁止，P133／P130）。
func (e SideEffect) WritesBusinessStatus() bool { return e == EffectTaskStatusWrite }
