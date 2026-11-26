package commands

import (
	"context"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// OperationStatus 是异步操作的封闭状态集合（http-api-contract-v1 §3）。
type OperationStatus string

const (
	StatusQueued    OperationStatus = "queued"
	StatusRunning   OperationStatus = "running"
	StatusSucceeded OperationStatus = "succeeded"
	StatusFailed    OperationStatus = "failed"
)

// Valid 报告状态是否属于四值封闭集合。
func (s OperationStatus) Valid() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusSucceeded, StatusFailed:
		return true
	}
	return false
}

// Terminal 报告是否已到终止态。
func (s OperationStatus) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed
}

// Failure 是终止态操作携带的错误。
type Failure struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

// Receipt 是命令受理结果。
//
// OperationID 必填；状态为空时按 queued 处理。非终止态由传输层补 Location、
// retry_after_ms 并返回 202；Replay=true 且已终止时返回 200 与原结果。
type Receipt struct {
	OperationID model.OperationID
	Status      OperationStatus
	Action      string
	Resource    string
	CreatedAt   time.Time
	Revision    *model.Revision
	RetryAfter  time.Duration
	Result      any
	Failure     *Failure
	Replay      bool
}

// DispatchCommand 是「按指定任务／节点派发」的输入。
type DispatchCommand struct {
	ProjectID        model.ProjectID
	Actor            string
	RequestedTask    *model.InstanceID
	NodeID           model.NodeID
	PredecessorRunID *model.RunID
	Capacity         ports.Capacity
	ExpectedRevision model.Revision
	IdempotencyKey   string
	Fingerprint      string
}

// ClaimCommand 是「主动领取」的输入；与调度共用同一领取用例，不绕过排他协调。
type ClaimCommand struct {
	ProjectID        model.ProjectID
	Actor            string
	RequestedTask    *model.InstanceID
	NodeID           model.NodeID
	Workspace        string
	Capacity         ports.Capacity
	ExpectedRevision model.Revision
	IdempotencyKey   string
	Fingerprint      string
}

// CancelCommand 是业务任务取消的输入。
type CancelCommand struct {
	Instance         model.InstanceID
	Actor            string
	Reason           string
	ExpectedRevision model.Revision
	IdempotencyKey   string
	Fingerprint      string
}

// RetryCommand 是业务任务重新执行的输入；重试仍走资源领取，不在请求内起进程。
type RetryCommand struct {
	Instance         model.InstanceID
	Actor            string
	Reason           string
	ExpectedRevision model.Revision
	IdempotencyKey   string
	Fingerprint      string
}

// CancelRunCommand 是单次运行申请停止的输入。
//
// revision 是任务行版本，generation 是运行代际；两者都带时必须同时满足。
// 停止受理不等于停止成功，结果由操作查询确认。
type CancelRunCommand struct {
	RunID            model.RunID
	Actor            string
	Reason           string
	Generation       model.Generation
	ExpectedRevision *model.Revision
	IdempotencyKey   string
	Fingerprint      string
}

// DeliveryCommand 是申报交付的输入；产物收集与冻结在 202 之后完成。
type DeliveryCommand struct {
	RunID            model.RunID
	Actor            string
	Generation       model.Generation
	Commit           model.Commit
	Note             string
	ExpectedRevision model.Revision
	IdempotencyKey   string
	Fingerprint      string
}

// VerificationCommand 是申请验证的输入。
type VerificationCommand struct {
	DeliveryID       model.DeliveryID
	Actor            string
	Mode             string
	ExpectedRevision model.Revision
	IdempotencyKey   string
	Fingerprint      string
}

// ReviewCommand 是人工评审结论的输入。
type ReviewCommand struct {
	Instance         model.InstanceID
	DeliveryID       model.DeliveryID
	Actor            string
	Decision         string
	Reason           string
	ExpectedRevision model.Revision
	IdempotencyKey   string
	Fingerprint      string
}

// MergeCommand 是合入的输入；合入结果单独持久化，不把受理当成功。
type MergeCommand struct {
	Instance         model.InstanceID
	Actor            string
	DeliveryID       model.DeliveryID
	Strategy         string
	ExpectedRevision model.Revision
	IdempotencyKey   string
	Fingerprint      string
}

// Dispatcher 受理一次派发。
type Dispatcher interface {
	Dispatch(ctx context.Context, cmd DispatchCommand) (Receipt, error)
}

// Claimer 受理一次主动领取。
type Claimer interface {
	Claim(ctx context.Context, cmd ClaimCommand) (Receipt, error)
}

// TaskCommands 受理业务任务取消与重试。
type TaskCommands interface {
	Cancel(ctx context.Context, cmd CancelCommand) (Receipt, error)
	Retry(ctx context.Context, cmd RetryCommand) (Receipt, error)
}

// RunCommands 受理单次运行停止。
type RunCommands interface {
	CancelRun(ctx context.Context, cmd CancelRunCommand) (Receipt, error)
}

// Deliveries 受理交付申报。
type Deliveries interface {
	Deliver(ctx context.Context, cmd DeliveryCommand) (Receipt, error)
}

// Verifications 受理验证申请。
type Verifications interface {
	Verify(ctx context.Context, cmd VerificationCommand) (Receipt, error)
}

// Reviews 受理人工评审结论。
type Reviews interface {
	Review(ctx context.Context, cmd ReviewCommand) (Receipt, error)
}

// Merges 受理合入。
type Merges interface {
	Merge(ctx context.Context, cmd MergeCommand) (Receipt, error)
}

// 用例错误：HTTP 层按契约统一映射。
type (
	// ErrConflict 是 409：版本、归属、幂等键、写盘冲突等事实冲突。
	ErrConflict struct {
		Code    string
		Message string
		Details map[string]any
	}
	// ErrNotFound 是 404：资源或操作不存在。
	ErrNotFound struct{ Message string }
	// ErrInvalid 是 422：语义不合法，不是事实冲突。
	ErrInvalid struct {
		Code    string
		Message string
		Details map[string]any
	}
	// ErrRevisionMismatch 是 409 revision_mismatch，并给出当前 revision。
	ErrRevisionMismatch struct{ Actual int64 }
	// ErrUnauthorized 是 403：凭据有效但无权。
	ErrUnauthorized struct {
		Code    string
		Message string
	}
	// ErrBusy 是 503 + Retry-After：数据库忙或依赖暂不可用。
	ErrBusy struct{ RetryAfter time.Duration }
)

func (e ErrConflict) Error() string         { return e.Code + ": " + e.Message }
func (e ErrNotFound) Error() string         { return e.Message }
func (e ErrInvalid) Error() string          { return e.Code + ": " + e.Message }
func (e ErrRevisionMismatch) Error() string { return "revision_mismatch" }
func (e ErrUnauthorized) Error() string     { return e.Code + ": " + e.Message }
func (e ErrBusy) Error() string             { return "busy" }
