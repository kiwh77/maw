package systemagent

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/claim"
	"github.com/kiwh77/maw/internal/engine/matching"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// DispatchPort 是 Orchestrator 消费 dispatch_ready 的端口；Dispatcher 是默认实现。
type DispatchPort interface {
	DispatchReady(ctx context.Context, req DispatchRequest) (DispatchOutcome, error)
}

// DispatchProjection 是自动选题所需的只读投影。
//
// matching 只做纯函数匹配，投影由 app 层从库、配置与节点观测填入；四个方法都不写库。
// 节点没有本版本、未过期的 observed 证据时，投影必须如实给出 unknown／过期，
// 不能为了让匹配成功而猜 ready。
type DispatchProjection interface {
	Policy(ctx context.Context, projectID string) (matching.Policy, error)
	Tasks(ctx context.Context, projectID string) ([]matching.Task, error)
	Nodes(ctx context.Context, projectID string) ([]matching.Node, error)
	ActiveManaged(ctx context.Context, projectID string) (int, error)
}

// DispatchAdvance 是协调者推进端口：Claim 落库后由它写 launch_spec 并启动 Runner。
//
// serve 内 leader 已持 coordinator.lock，实现方必须复用同一个协调者实例，
// 否则第二次抢锁会被拒（ErrCoordinatorActive），排队操作永远推不动。
type DispatchAdvance interface {
	Advance(ctx context.Context) (DispatchStep, error)
}

// DispatchStep 是一次推进的事实回报。
type DispatchStep struct {
	OperationID string
	Action      string
	Progressed  bool
	LaunchSpec  bool
	// JournalDir 是本次操作对应的日志目录；写方是 app 层的协调者装配。
	JournalDir string
	Note       string
}

// DispatchRequest 是系统 Agent 处理 dispatch_ready 时带上的上下文。
type DispatchRequest struct {
	ProjectID   string
	ProjectPath string
	JobID       string
}

// DispatchOutcome 是一次派发的事实回报：匹配结论、领取结果与推进结果分开记。
type DispatchOutcome struct {
	Matched bool
	TaskID  string
	NodeID  model.NodeID

	RunID       string
	OperationID string
	Replay      bool

	// SteppedOperationID 是 Coordinator.Step 实际推进的操作；可能不是本次领取的那条。
	SteppedOperationID string

	// Reasons 是未匹配时 matching 给出的可解释原因（含每个候选节点为何不可用）。
	Reasons []matching.QueueReason

	Progressed bool
	Action     string
	LaunchSpec bool
	JournalDir string
	Note       string
}

// Dispatcher 把「Match → Claim → launch_spec → Step」串成一次可重放的派发。
//
// 三次失败语义分开：匹配失败是正常结论（带 Reasons 结束 job），
// 领取失败原样上抛让 job 重排，推进失败同样上抛但保留已领取的 Run／Operation
// 以便协调者下次接着推进——不把「已领取」重做成第二次领取。
type Dispatcher struct {
	Store      claim.Store
	Nodes      claim.NodeGuard
	Projection DispatchProjection
	Advance    DispatchAdvance
	// Workspace 给出本次执行独占的绝对目录；为空表示不能派发受管执行。
	Workspace func(projectID, taskID string) (string, error)
	// AgentID 是自动派发使用的规范身份；为空时用 system-agent-1。
	AgentID string
	Clock   func() time.Time
}

func (d *Dispatcher) now() time.Time {
	if d != nil && d.Clock != nil {
		return d.Clock().UTC()
	}
	return time.Now().UTC()
}

func (d *Dispatcher) agentID() string {
	if d != nil && strings.TrimSpace(d.AgentID) != "" {
		return strings.TrimSpace(d.AgentID)
	}
	return "system-agent-1"
}

// DispatchReady 执行一次自动选题派发。
//
// 容量遵守受管执行上限：matching 用 policy.auto_execution.managed_execution_limit
// 与 ActiveManaged 先挡一次，Claim 在事务内再按同一上限复核一次；两处口径一致，
// 不会因为「匹配到」就绕过全局配额。
func (d *Dispatcher) DispatchReady(ctx context.Context, req DispatchRequest) (DispatchOutcome, error) {
	if d == nil || d.Store == nil {
		return DispatchOutcome{}, fmt.Errorf("systemagent: 自动派发缺少 Store")
	}
	if d.Projection == nil {
		return DispatchOutcome{}, fmt.Errorf("systemagent: 自动派发缺少投影端口")
	}
	policy, err := d.Projection.Policy(ctx, req.ProjectID)
	if err != nil {
		return DispatchOutcome{}, err
	}
	tasks, err := d.Projection.Tasks(ctx, req.ProjectID)
	if err != nil {
		return DispatchOutcome{}, err
	}
	nodes, err := d.Projection.Nodes(ctx, req.ProjectID)
	if err != nil {
		return DispatchOutcome{}, err
	}
	active, err := d.Projection.ActiveManaged(ctx, req.ProjectID)
	if err != nil {
		return DispatchOutcome{}, err
	}

	dec, err := matching.Match(matching.Request{
		Policy:        policy,
		Tasks:         tasks,
		Nodes:         nodes,
		ActiveManaged: active,
		Now:           d.now(),
	})
	if err != nil {
		return DispatchOutcome{}, err
	}
	out := DispatchOutcome{
		Matched: dec.Matched,
		TaskID:  dec.TaskID,
		NodeID:  dec.NodeID,
		Reasons: dec.Reasons,
	}
	if !dec.Matched {
		// 没有可跑题也是正常结论：把原因带回 job 结果，不制造空转。
		return out, nil
	}

	if d.Workspace == nil {
		return DispatchOutcome{}, fmt.Errorf("systemagent: 自动派发缺少工作目录端口")
	}
	var task *db.Task
	err = d.Store.Read(func(tx *sql.Tx) error {
		t, e := db.GetTask(tx, req.ProjectID, dec.TaskID)
		if e != nil {
			return e
		}
		if t == nil {
			return fmt.Errorf("systemagent: 匹配到的任务 %s 已不存在", dec.TaskID)
		}
		task = t
		return nil
	})
	if err != nil {
		return DispatchOutcome{}, err
	}
	inst := model.InstanceID(task.InstanceID)
	if !inst.Valid() {
		return DispatchOutcome{}, fmt.Errorf("systemagent: 任务 %s 没有可用实例身份", task.ID)
	}
	ws, err := d.Workspace(req.ProjectID, task.ID)
	if err != nil {
		return DispatchOutcome{}, err
	}

	claimed, err := claim.New(d.Store, d.Nodes).Claim(ctx, claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID:      model.ProjectID(req.ProjectID),
			AgentID:        model.AgentID(d.agentID()),
			RequestedTask:  &inst,
			IdempotencyKey: dispatchKey(req, dec),
			Capacity: ports.Capacity{
				NodeSlotLimit: dispatchSlotLimit(nodes, dec.NodeID),
				ManagedLimit:  policy.AutoExecution.ManagedExecutionLimit,
			},
		},
		NodeID:    dec.NodeID,
		Workspace: ws,
		Branch:    task.Branch,
		Worktree:  task.Worktree,
	})
	if err != nil {
		return DispatchOutcome{}, err
	}
	out.RunID = string(claimed.RunID)
	out.OperationID = string(claimed.OperationID)
	out.Replay = claimed.Replay

	if d.Advance == nil {
		// 没有推进端口时不假装跑起来了：Run／Operation 已领到，等协调者接手。
		return out, nil
	}
	step, err := d.Advance.Advance(ctx)
	if err != nil {
		return out, err
	}
	out.SteppedOperationID = step.OperationID
	out.Progressed = step.Progressed
	out.Action = step.Action
	out.LaunchSpec = step.LaunchSpec
	out.JournalDir = step.JournalDir
	out.Note = step.Note
	return out, nil
}

// dispatchKey 用 job 身份派生稳定幂等键：同一 job 重排后重放命中同一 Run／Operation。
func dispatchKey(req DispatchRequest, dec matching.Decision) string {
	job := strings.TrimSpace(req.JobID)
	if job == "" {
		job = "adhoc"
	}
	return "dispatch-ready:" + job + ":" + dec.TaskID + ":" + string(dec.NodeID)
}

// dispatchSlotLimit 取匹配节点的槽上限作为本次领取的节点上限；找不到时为 0（交给库复核）。
func dispatchSlotLimit(nodes []matching.Node, id model.NodeID) int {
	for _, n := range nodes {
		if n.ID == id {
			return n.Config.SlotLimit
		}
	}
	return 0
}

// RenderQueueReasons 把匹配原因渲染成便于看板与日志阅读的行。
func RenderQueueReasons(reasons []matching.QueueReason) []string {
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		out = append(out, r.String())
	}
	return out
}
