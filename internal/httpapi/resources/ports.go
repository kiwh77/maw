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
	Remove(ctx context.Context, nodeID model.NodeID) error
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

// NodeCheckItem 是一条同步自检项（二进制、版本、参数等）。
type NodeCheckItem struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// NodeCheckResult 是一次同步 CLI 环境自检的结果。
//
// 与 Probes 的分工：Probes 只受理异步 Operation，不跑厂商 CLI；
// NodeChecks 是看板「测试环境」按钮直连的同步检查，会实际启动 CLI 并回写节点 meta。
type NodeCheckResult struct {
	NodeID    model.NodeID    `json:"node_id"`
	OK        bool            `json:"ok"`
	Version   string          `json:"version,omitempty"`
	LatencyMS int64           `json:"latency_ms"`
	CheckedAt string          `json:"checked_at"`
	Checks    []NodeCheckItem `json:"checks"`
	Error     string          `json:"error,omitempty"`
	Meta      map[string]any  `json:"meta,omitempty"`
}

// NodeChecks 是同步 CLI 环境自检用例。
type NodeChecks interface {
	Check(ctx context.Context, nodeID model.NodeID) (NodeCheckResult, error)
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

// AutoExecutionNode 是一个候选执行节点及其就绪结论。
type AutoExecutionNode struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
	Slots  int    `json:"slots"`
}

// AutoExecutionState 是工作台「自动执行」开关的完整状态。
//
// 表达的是产品语义——「让中枢用这个节点自动领题并执行」——而不是内部
// 四个配置字段。界面不该知道为了实现一句话要写几处配置。
type AutoExecutionState struct {
	ProjectID string              `json:"project_id"`
	Enabled   bool                `json:"enabled"`
	NodeID    string              `json:"node_id"`
	Node      AutoExecutionNode   `json:"node"`
	Nodes     []AutoExecutionNode `json:"nodes"`
	Limit     int                 `json:"limit"`
	Ready     bool                `json:"ready"`
	Reasons   []string            `json:"reasons,omitempty"`
	Detail    string              `json:"detail,omitempty"`
	// ServeRunning 报告常驻中枢是否在跑；不在跑时写盘成功也不会立刻执行。
	ServeRunning bool `json:"serve_running"`
	// Applied 为真表示本次写入会立即生效。
	Applied bool   `json:"applied"`
	Reason  string `json:"reason,omitempty"`
	// LastAttempt 是最近一次自动派发尝试的事实。
	LastAttempt map[string]any `json:"last_attempt,omitempty"`
}

// AutoExecutionSetRequest 是开关写入请求。
type AutoExecutionSetRequest struct {
	ProjectID string
	Enabled   bool
	NodeID    string
	Actor     string
}

// AutoExecution 是「自动执行」开关用例。
//
// Get 用于页面首次渲染与刷新；Set 是唯一的写入口，一次性写齐
// auto_enabled／auto_select／allowed_nodes／worker_node 四项。
type AutoExecution interface {
	Get(ctx context.Context, projectID string) (AutoExecutionState, error)
	Set(ctx context.Context, req AutoExecutionSetRequest) (AutoExecutionState, error)
}

// SystemAgentConfig 是系统 Agent（Router）的可写配置视图。
type SystemAgentConfig struct {
	ProjectID        string   `json:"project_id"`
	Enabled          bool     `json:"enabled"`
	LLMNode          string   `json:"llm_node"`
	WorkerNode       string   `json:"worker_node"`
	LLMCommand       []string `json:"llm_command"`
	AutoKinds        []string `json:"auto_kinds"`
	OpsAllowlist     []string `json:"ops_allowlist"`
	EscalatePatterns []string `json:"escalate_patterns"`
	PollIntervalSec  int      `json:"poll_interval_sec"`
	MaxConcurrent    int      `json:"max_concurrent"`
	CooldownSec      int      `json:"cooldown_sec"`
	MaxAttempts      int      `json:"max_attempts"`
	// LLMReady 为真表示 llm_command 已配置：否则 Router 只能升级人工。
	LLMReady bool `json:"llm_ready"`
}

// SystemAgentSetRequest 是 Router 配置的增量更新（只改非 nil 字段）。
type SystemAgentSetRequest struct {
	ProjectID    string
	Enabled      *bool
	LLMNode      *string
	WorkerNode   *string
	LLMCommand   *[]string
	AutoKinds    *[]string
	OpsAllowlist *[]string
	Actor        string
}

// SystemAgentSettings 是 Router 配置用例。
type SystemAgentSettings interface {
	Get(ctx context.Context, projectID string) (SystemAgentConfig, error)
	Update(ctx context.Context, req SystemAgentSetRequest) (SystemAgentConfig, error)
}
