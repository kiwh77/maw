package ports

import (
	"context"
	"strconv"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// ClaimExecution 统一领取用例（契约 §2.1）。指定题、人工 next 与后续自动匹配共用它。
//
// 事务：必须在短事务内完成验版本、占槽、预定目录、建 Run 与 Operation、追加事件。
// 禁止：事务内建 worktree、跑 git、起进程、调验证命令。指定任务时不得内部再选题。
type ClaimExecution interface {
	Claim(ctx context.Context, req ClaimRequest) (ClaimResult, error)
}

// Store 是事务端口（契约 §2.2）：一次调用＝一个事务，调用方不持有 *sql.Tx。
type Store interface {
	Apply(ctx context.Context, req StoreRequest) (StoreResult, error)
}

// Probe 探测节点（契约 §2.3）：只读，可执行 --version／--help，不登录、不发模型任务。
type Probe interface {
	Probe(ctx context.Context, cfg model.NodeConfig) (model.ProbeResult, error)
}

// Launch 把冻结的 RunSpec 翻成启动规格（契约 §2.4）：argv 数组，不拼 shell。
type Launch interface {
	BuildLaunchSpec(ctx context.Context, spec model.RunSpec) (model.LaunchSpec, error)
}

// Decode 把一条来源记录翻译成标准事件（契约 §2.5）：未知记录只留引用，不产生事件。
type Decode interface {
	Decode(ctx context.Context, req DecodeRequest) (DecodeResult, error)
}

// Interpret 判定运行结果（契约 §2.6）：退出码 0 不是成功依据，未知结果保持 unknown。
type Interpret interface {
	Interpret(ctx context.Context, req InterpretRequest) (model.InterpretResult, error)
}

// ProcessController 创建与停止进程（契约 §2.7）：禁止在事务内调用。
type ProcessController interface {
	Start(ctx context.Context, spec model.LaunchSpec, runID model.RunID, gen model.Generation) (model.ProcessHandle, error)
	Stop(ctx context.Context, req model.StopRequest) (model.StopEvidence, error)
}

// Capacity 是领取时的容量上限：节点槽与受管执行上限分开，不互相顶替。
type Capacity struct {
	NodeSlotLimit int `json:"node_slot_limit"`
	ManagedLimit  int `json:"managed_limit"`
}

// ClaimRequest 是领取输入（契约 §2.1 输入行）。
type ClaimRequest struct {
	ProjectID        model.ProjectID   `json:"project_id"`
	AgentID          model.AgentID     `json:"agent_id"`
	RequestedTask    *model.InstanceID `json:"requested_task,omitempty"`
	ExpectedRevision *model.Revision   `json:"expected_revision,omitempty"`
	IdempotencyKey   string            `json:"idempotency_key"`
	Capacity         Capacity          `json:"capacity"`
}

// Validate 拒绝不完整领取：缺幂等键或冲突检查位即 P134。
func (r ClaimRequest) Validate() []*model.Error {
	var out []*model.Error
	if !r.ProjectID.Valid() {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "claim.project_id", "必须是 16 位小写十六进制"))
	}
	if !r.AgentID.Valid() {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "claim.agent_id", "必须是规范的 {应用}-{工程slug}-{序号}"))
	}
	if r.RequestedTask != nil && !r.RequestedTask.Valid() {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "claim.requested_task", "指定任务必须是合法实例身份"))
	}
	if r.ExpectedRevision != nil && *r.ExpectedRevision < 0 {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "claim.expected_revision", "不得为负"))
	}
	if r.IdempotencyKey == "" {
		out = append(out, model.Violation(model.CodeClaimWithoutGuards, "claim.idempotency_key",
			"领取必须带幂等键，否则重放会双开"))
	}
	if r.Capacity.NodeSlotLimit < 0 || r.Capacity.ManagedLimit < 0 {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "claim.capacity", "容量不得为负"))
	}
	return out
}

// ClaimResult 是领取结果（契约 §2.1 输出行）。
type ClaimResult struct {
	Instance    model.InstanceID  `json:"task_instance"`
	Version     model.VersionNo   `json:"task_version"`
	RunID       model.RunID       `json:"run_id"`
	Generation  model.Generation  `json:"generation"`
	Leases      []model.Lease     `json:"leases"`
	OperationID model.OperationID `json:"operation_id"`
}

// StoreOpKind 是事务内可提交的操作类型（契约 §2.2 输入行）。
type StoreOpKind string

// 六种操作。
const (
	StoreTaskStatus      StoreOpKind = "task_status"
	StoreRunPhase        StoreOpKind = "run_phase"
	StoreLeaseAcquire    StoreOpKind = "lease_acquire"
	StoreLeaseRelease    StoreOpKind = "lease_release"
	StoreEventAppend     StoreOpKind = "event_append"
	StoreOperationUpdate StoreOpKind = "operation_update"
)

// Valid 报告取值是否合法。
func (k StoreOpKind) Valid() bool {
	switch k {
	case StoreTaskStatus, StoreRunPhase, StoreLeaseAcquire, StoreLeaseRelease, StoreEventAppend, StoreOperationUpdate:
		return true
	}
	return false
}

// TaskStatusWrite 写业务任务状态。
type TaskStatusWrite struct {
	Instance model.InstanceID `json:"task_instance"`
	Status   model.TaskStatus `json:"status"`
}

// RunPhaseWrite 写运行阶段；阶段变更必须与 run.phase 事件同批提交（E260）。
type RunPhaseWrite struct {
	RunID model.RunID    `json:"run_id"`
	Phase model.RunPhase `json:"phase"`
	At    time.Time      `json:"at"`
	Event model.EventID  `json:"event_id"`
}

// LeaseAcquire 占用资源。
type LeaseAcquire struct {
	Instance model.InstanceID `json:"task_instance"`
	Lease    model.Lease      `json:"lease"`
}

// LeaseRelease 释放资源；释放必须留下 lease.released 事件（E261）。
type LeaseRelease struct {
	LeaseID           model.LeaseID    `json:"lease_id"`
	Kind              model.LeaseKind  `json:"kind"`
	ReleaseGeneration model.Generation `json:"release_generation"`
	Event             model.EventID    `json:"event_id"`
}

// OperationUpdate 推进持久操作。
type OperationUpdate struct {
	OperationID model.OperationID     `json:"operation_id"`
	Result      model.OperationResult `json:"result"`
}

// StoreOp 是一个带条件的事务操作。每个 op 必须带 expected_revision 或 generation。
type StoreOp struct {
	Kind             StoreOpKind       `json:"kind"`
	ExpectedRevision *model.Revision   `json:"expected_revision,omitempty"`
	Generation       *model.Generation `json:"generation,omitempty"`

	TaskStatus      *TaskStatusWrite `json:"task_status,omitempty"`
	RunPhase        *RunPhaseWrite   `json:"run_phase,omitempty"`
	LeaseAcquire    *LeaseAcquire    `json:"lease_acquire,omitempty"`
	LeaseRelease    *LeaseRelease    `json:"lease_release,omitempty"`
	Event           *model.Event     `json:"event_append,omitempty"`
	OperationUpdate *OperationUpdate `json:"operation_update,omitempty"`
}

// Validate 拒绝无条件的写入与载荷错位的操作。
func (o StoreOp) Validate(path string) []*model.Error {
	var out []*model.Error
	if !o.Kind.Valid() {
		out = append(out, model.Violationf(model.CodePortFieldInvalid, path+".kind", "未知操作类型 %q", o.Kind))
		return out
	}
	if o.ExpectedRevision == nil && o.Generation == nil {
		out = append(out, model.Violation(model.CodePortFieldInvalid, path,
			"每个操作必须带 expected_revision 或 generation，否则「以最新为准」会掩盖冲突"))
	}
	payload := 0
	for _, present := range []bool{
		o.TaskStatus != nil, o.RunPhase != nil, o.LeaseAcquire != nil,
		o.LeaseRelease != nil, o.Event != nil, o.OperationUpdate != nil,
	} {
		if present {
			payload++
		}
	}
	if payload != 1 {
		out = append(out, model.Violationf(model.CodePortFieldInvalid, path, "必须且只能带一个载荷，收到 %d 个", payload))
	}
	switch o.Kind {
	case StoreTaskStatus:
		if o.TaskStatus == nil {
			out = append(out, model.Violation(model.CodePortFieldInvalid, path, "task_status 操作必须带 task_status 载荷"))
		} else if !o.TaskStatus.Status.Valid() {
			out = append(out, model.Violationf(model.CodePortFieldInvalid, path+".task_status.status",
				"未知任务状态 %q", o.TaskStatus.Status))
		}
	case StoreRunPhase:
		if o.RunPhase == nil {
			out = append(out, model.Violation(model.CodePortFieldInvalid, path, "run_phase 操作必须带 run_phase 载荷"))
		} else if !o.RunPhase.Phase.Valid() {
			out = append(out, model.Violationf(model.CodePortFieldInvalid, path+".run_phase.phase",
				"未知运行阶段 %q", o.RunPhase.Phase))
		}
	case StoreLeaseAcquire:
		if o.LeaseAcquire == nil {
			out = append(out, model.Violation(model.CodePortFieldInvalid, path, "lease_acquire 操作必须带占用载荷"))
		}
	case StoreLeaseRelease:
		if o.LeaseRelease == nil {
			out = append(out, model.Violation(model.CodePortFieldInvalid, path, "lease_release 操作必须带释放载荷"))
		}
	case StoreEventAppend:
		if o.Event == nil {
			out = append(out, model.Violation(model.CodePortFieldInvalid, path, "event_append 操作必须带事件"))
		}
	case StoreOperationUpdate:
		if o.OperationUpdate == nil {
			out = append(out, model.Violation(model.CodePortFieldInvalid, path, "operation_update 操作必须带操作载荷"))
		}
	}
	return out
}

// StoreRequest 是一次事务提交。
type StoreRequest struct {
	Ops []StoreOp `json:"ops"`
}

// Validate 要求至少一个操作，并逐条检查。
func (r StoreRequest) Validate() []*model.Error {
	var out []*model.Error
	if len(r.Ops) == 0 {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "store.ops", "事务至少包含一个操作"))
	}
	for i, op := range r.Ops {
		out = append(out, op.Validate(pathIndex("store.ops", i))...)
	}
	return out
}

// StoreResult 是一次提交的结果：游标是事件入库序号。
type StoreResult struct {
	Cursor   model.Cursor   `json:"cursor"`
	Revision model.Revision `json:"revision"`
	Applied  []StoreOpKind  `json:"applied"`
}

// DecodeRequest 是一条来源记录的解码输入。
type DecodeRequest struct {
	// RecordRef 是稳定日志位置：必须能回溯到原始字节。
	RecordRef  string         `json:"record_ref"`
	RecordType string         `json:"record_type"`
	Payload    []byte         `json:"payload"`
	Cursor     model.Cursor   `json:"cursor"`
	Protocol   model.Protocol `json:"protocol"`
}

// Validate 拒绝无法定位或协议含糊的记录。
func (r DecodeRequest) Validate() []*model.Error {
	var out []*model.Error
	if r.RecordRef == "" {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "decode.record_ref", "必须带稳定日志位置"))
	}
	if r.Cursor < 1 {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "decode.cursor", "游标从 1 开始"))
	}
	if r.Protocol.Format == "" || r.Protocol.Version == "" {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "decode.protocol", "必须写明格式与版本"))
	}
	return out
}

// DecodeResult 是零到多条标准事件与原始引用。
type DecodeResult struct {
	Events  []model.Event `json:"events"`
	RawRef  string        `json:"raw_ref"`
	Quality model.Quality `json:"quality"`
}

// InterpretRequest 是结果判定的全部输入；缺最终结果记录时不得判成功。
type InterpretRequest struct {
	FinalResult      []byte                    `json:"final_result,omitempty"`
	ApplicationError string                    `json:"application_error,omitempty"`
	ExitCode         *int                      `json:"exit_code,omitempty"`
	TimedOut         bool                      `json:"timed_out,omitempty"`
	Artifacts        []model.ArtifactCandidate `json:"artifacts,omitempty"`
}

// Validate 拒绝缺少任何可判定依据的请求。
func (r InterpretRequest) Validate() []*model.Error {
	var out []*model.Error
	if len(r.FinalResult) == 0 && r.ApplicationError == "" && r.ExitCode == nil && !r.TimedOut {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "interpret",
			"至少要有最终结果、应用错误、退出码或超时信息之一"))
	}
	return out
}

func pathIndex(prefix string, i int) string {
	return prefix + "[" + strconv.Itoa(i) + "]"
}
