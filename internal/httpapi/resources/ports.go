package resources

import (
	"context"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/nodes"
)

// TaskCreateRequest 是创建任务定义的输入。
type TaskCreateRequest struct {
	ProjectID      model.ProjectID
	DisplayID      model.DisplayID
	Title          string
	Deps           []string
	WriteScope     []string
	ReadScope      []string
	Acceptance     []string
	Priority       int
	MergeOrder     int
	IdempotencyKey string
}

// TaskCreateResult 是创建结果。
type TaskCreateResult struct {
	InstanceID model.InstanceID
	DisplayID  model.DisplayID
	Version    model.VersionNo
	Revision   model.Revision
	Replay     bool
}

// TaskUpdateRequest 是冻结新定义版本的输入。
type TaskUpdateRequest struct {
	InstanceID       model.InstanceID
	ExpectedRevision model.Revision
	Title            string
	Deps             []string
	WriteScope       []string
	ReadScope        []string
	Acceptance       []string
	IdempotencyKey   string
}

// TaskUpdateResult 是版本更新结果。
type TaskUpdateResult struct {
	InstanceID model.InstanceID
	Version    model.VersionNo
	Revision   model.Revision
	Replay     bool
}

// Tasks 是任务创建／版本更新用例（由接线层落到 engine/tasks + 仓储）。
type Tasks interface {
	Create(ctx context.Context, req TaskCreateRequest) (TaskCreateResult, error)
	UpdateDefinition(ctx context.Context, req TaskUpdateRequest) (TaskUpdateResult, error)
}

// NodeWriteRequest 是节点登记／配置更新。
type NodeWriteRequest struct {
	Registration     nodes.Registration
	ExpectedRevision model.Revision // 更新时必填；创建忽略
	IdempotencyKey   string
}

// NodeWriteResult 是节点写入结果。
type NodeWriteResult struct {
	Snapshot nodes.Snapshot
	Created  bool
	Replay   bool
}

// Nodes 是节点登记用例。
type Nodes interface {
	Register(ctx context.Context, req NodeWriteRequest) (NodeWriteResult, error)
	UpdateConfig(ctx context.Context, req NodeWriteRequest) (NodeWriteResult, error)
}

// ProbeRequest 受理一次节点探测（异步 Operation）。
type ProbeRequest struct {
	NodeID           model.NodeID
	ExpectedRevision model.Revision
	IdempotencyKey   string
	Actor            string
}

// ProbeResult 是探测受理结果（202）。
type ProbeResult struct {
	OperationID model.OperationID
	Replay      bool
}

// Probes 把探测请求落成可查询 Operation，不在请求内跑厂商 CLI。
type Probes interface {
	Accept(ctx context.Context, req ProbeRequest) (ProbeResult, error)
}

// ProjectRemovalRequest 是从本机中枢移除一个工程登记。
//
// Force 是显式的「连现场一起清掉」：默认实现必须在有未完成任务或在线 Agent
// 时返回 ErrConflict，而不是静默清空。
type ProjectRemovalRequest struct {
	ProjectID string
	Force     bool
	Actor     string
}

// ProjectRemovalResult 是移除结果：删了什么、留了什么都要能被界面读到。
type ProjectRemovalResult struct {
	ProjectID   string
	ProjectName string
	ProjectPath string
	Summary     string
	Removed     map[string]int64
	Retained    map[string]int64
}

// ProjectUpdateRequest 是工程属性与策略的增量更新。
//
// 指针字段区分「没传」与「传了空值」：PATCH 只写显式出现的字段。
type ProjectUpdateRequest struct {
	ProjectID    string
	Name         *string
	Path         *string
	MainBranch   *string
	MaxParallel  *int
	AutoEnabled  *bool
	Limit        *int
	AllowedNodes *[]string
	WorkerNode   *string
	LLMNode      *string
	Actor        string
}

// ProjectUpdateResult 是更新后的工程投影。
type ProjectUpdateResult struct {
	ProjectID    string
	ProjectName  string
	ProjectPath  string
	MainBranch   string
	MaxParallel  int
	ConfigPath   string
	ConfigWriten bool
}

// Projects 是工程登记管理用例。
type Projects interface {
	Remove(ctx context.Context, req ProjectRemovalRequest) (ProjectRemovalResult, error)
	Update(ctx context.Context, req ProjectUpdateRequest) (ProjectUpdateResult, error)
}

// 用例错误：HTTP 层按契约映射状态码。
type (
	ErrConflict struct {
		Code    string
		Message string
		Details map[string]any
	}
	ErrNotFound struct{ Message string }
	ErrInvalid  struct {
		Code    string
		Message string
		Details map[string]any
	}
)

func (e ErrConflict) Error() string { return e.Code + ": " + e.Message }
func (e ErrNotFound) Error() string { return e.Message }
func (e ErrInvalid) Error() string  { return e.Code + ": " + e.Message }
