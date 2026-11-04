package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/dispatch"
	"github.com/kiwh77/maw/internal/engine/matching"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/runner/process"
	"github.com/kiwh77/maw/internal/systemagent"
)

// SystemStatus 查看系统 Agent 配置与队列摘要。
func SystemStatus(cwd string) (Result, error) {
	pc, cfg, err := loadSystemAgentConfig(cwd)
	if err != nil {
		return nil, err
	}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil, err
	}
	var queued, running, done, failed, escalated int
	var jobs []*db.SystemJob
	_ = store.Read(func(tx *sql.Tx) error {
		jobs, _ = db.ListSystemJobs(tx, pc.ID(), "", 20)
		for _, j := range jobs {
			switch j.Status {
			case db.SystemJobQueued:
				queued++
			case db.SystemJobRunning:
				running++
			case db.SystemJobDone:
				done++
			case db.SystemJobFailed:
				failed++
			case db.SystemJobEscalated:
				escalated++
			}
		}
		return nil
	})
	return Result{
		"ok":                true,
		"enabled":           cfg.Enabled,
		"config_version":    cfg.ConfigVersion,
		"poll_interval_sec": cfg.PollIntervalSec,
		"auto_kinds":        cfg.AutoKinds,
		"llm_node":          cfg.LLMNode,
		"worker_node":       cfg.WorkerNode,
		"queue": map[string]any{
			"queued": queued, "running": running, "done": done,
			"failed": failed, "escalated": escalated, "recent": len(jobs),
		},
		"next": i18n.T("next.system_status"),
	}, nil
}

// SystemTrigger 显式入队。
func SystemTrigger(cwd, kind, taskID, message string) (Result, error) {
	pc, cfg, err := loadSystemAgentConfig(cwd)
	if err != nil {
		return nil, err
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = systemagent.SystemKindUnblock
	}
	decision := map[string]any{"message": message}
	if kind == systemagent.SystemKindOpsRun {
		decision["ops_command"] = message
	}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil, err
	}
	var job *db.SystemJob
	err = store.Write(func(tx *sql.Tx) error {
		instanceID := ""
		if taskID != "" {
			if t, e := db.GetTask(tx, pc.ID(), taskID); e == nil && t != nil {
				instanceID = t.InstanceID
				if message == "" {
					message = t.BlockedReason
				}
			}
		}
		var e error
		job, e = systemagent.Enqueue(tx, cfg, pc.ID(), kind, taskID, instanceID, message, decision, true)
		return e
	})
	if err != nil {
		return nil, err
	}
	if job == nil {
		return Result{"ok": true, "deduped": true, "next": i18n.T("next.system_trigger")}, nil
	}
	return Result{
		"ok": true, "job_id": job.JobID, "kind": job.Kind, "status": job.Status,
		"next": i18n.T("next.system_trigger"),
	}, nil
}

// SystemRun 执行指定 job 或立刻 Step 一次。
func SystemRun(cwd, jobID string) (Result, error) {
	pc, cfg, err := loadSystemAgentConfig(cwd)
	if err != nil {
		return nil, err
	}
	orch, err := buildOrchestrator(pc, cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if jobID != "" {
		if err := orch.RunJobID(ctx, jobID); err != nil {
			return nil, err
		}
		return Result{"ok": true, "job_id": jobID, "next": i18n.T("next.system_run")}, nil
	}
	n, err := orch.Step(ctx)
	if err != nil {
		return nil, err
	}
	return Result{"ok": true, "ran": n, "next": i18n.T("next.system_run")}, nil
}

// SystemWatch 常驻轮询（与 dispatch 共用 coordinator.lock）。
func SystemWatch(cwd string, once bool) (Result, error) {
	pc, cfg, err := loadSystemAgentConfig(cwd)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled && !once {
		return nil, i18n.E("err.system_disabled", "next.system_enable")
	}
	orch, err := buildOrchestrator(pc, cfg)
	if err != nil {
		return nil, err
	}
	rt, err := NewRuntime(pc, "")
	if err != nil {
		return nil, err
	}
	w := &systemagent.Watcher{
		Store:        orch.Store,
		Config:       cfg,
		Orchestrator: orch,
		ProjectID:    pc.ID(),
		LeaderLock: func() (func(), error) {
			lockPath := filepath.Join(rt.StateDir(), "dispatch", "coordinator.lock")
			_ = os.MkdirAll(filepath.Dir(lockPath), 0o755)
			lock, err := process.AcquireLock(lockPath)
			if err != nil {
				return nil, err
			}
			return func() { _ = lock.Release() }, nil
		},
	}
	// once 模式强制 Enabled 以便 PollOnce 工作
	if once {
		cfg.Enabled = true
		w.Config = cfg
	}
	ctx := context.Background()
	if once {
		enqueued, ran, err := w.PollOnce(ctx)
		if err != nil {
			return nil, err
		}
		return Result{"ok": true, "enqueued": enqueued, "ran": ran, "once": true}, nil
	}
	fmt.Fprintf(os.Stderr, "maw system watch: project=%s poll=%ds\n", pc.ID(), cfg.PollIntervalSec)
	return nil, w.Run(ctx)
}

func loadSystemAgentConfig(cwd string) (*projCtx, config.SystemAgent, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, config.SystemAgent{}, err
	}
	cfgPath := home.ConfigPath(pc.LocalHome)
	if _, err := config.EnsureSystemAgentSection(cfgPath); err != nil {
		return nil, config.SystemAgent{}, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, config.SystemAgent{}, err
	}
	pc.Config = cfg
	return pc, cfg.SystemAgent, nil
}

func buildOrchestrator(pc *projCtx, cfg config.SystemAgent) (*systemagent.Orchestrator, error) {
	return buildOrchestratorWithCoord(pc, cfg, nil)
}

// buildOrchestratorWithCoord 组装编排器；coord 非空时复用 serve 内已经持有
// coordinator.lock 的协调者，避免自动选题的 Step 被自己的锁挡在门外。
func buildOrchestratorWithCoord(pc *projCtx, cfg config.SystemAgent, coord *dispatch.Coordinator) (*systemagent.Orchestrator, error) {
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil, err
	}
	rt, err := NewRuntime(pc, "")
	if err != nil {
		return nil, err
	}
	disp, err := newSystemDispatcher(pc, cfg, store, rt, coord)
	if err != nil {
		return nil, err
	}
	var inv systemagent.Invoker
	if len(cfg.LLMCommand) > 0 {
		inv = systemagent.ExecInvoker{Argv: cfg.LLMCommand, Timeout: 3 * time.Minute}
	} else {
		// 无 LLM 时规则层：无法判断则 escalate
		inv = systemagent.FuncInvoker(func(ctx context.Context, req systemagent.DiagnoseRequest) (string, error) {
			_ = ctx
			return `{"action":"escalate_human","reason":"llm_command 未配置，默认升级人工"}`, nil
		})
	}
	return &systemagent.Orchestrator{
		Store:    store,
		Config:   cfg,
		Invoker:  inv,
		Effects:  appSystemEffects{cwd: pc.Repo},
		Dispatch: disp,
		WorkerID: "system-agent",
		ResolvePath: func(projectID string) (string, error) {
			if projectID == pc.ID() {
				return pc.Repo, nil
			}
			var path string
			err := store.Read(func(tx *sql.Tx) error {
				p, e := db.GetProject(tx, projectID)
				if e != nil {
					return e
				}
				if p == nil {
					return fmt.Errorf("unknown project %s", projectID)
				}
				path = p.Path
				return nil
			})
			return path, err
		},
	}, nil
}

// newSystemDispatcher 接线「Match → Claim → launch_spec → Coordinator.Step」。
//
// 投影只读库与配置；推进复用协调者（serve 内传 leader 的实例，CLI 路径自建）。
func newSystemDispatcher(pc *projCtx, cfg config.SystemAgent, store *db.Store, rt *Runtime, coord *dispatch.Coordinator) (*systemagent.Dispatcher, error) {
	if coord == nil {
		c, err := rt.BuildCoordinator(dispatch.NewDBQueue(store))
		if err != nil {
			return nil, err
		}
		coord = c
	}
	return &systemagent.Dispatcher{
		Store:      store,
		Projection: systemDispatchProjection{store: store, rt: rt, workerNode: cfg.WorkerNode},
		Advance:    systemDispatchAdvance{rt: rt, coord: coord},
		Workspace: func(projectID, taskID string) (string, error) {
			_ = projectID
			dir := filepath.Join(rt.StateDir(), "workspaces", taskID)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
			return filepath.Abs(dir)
		},
		AgentID: identity.Format("system", pc.Project.Name, 1),
	}, nil
}

// systemDispatchProjection 把库里的任务／节点与受管策略投影成 matching 的只读输入。
//
// 节点四维只认节点行里已经落盘的观测证据（meta）；缺证据时保持 unknown，
// matching 会给出 node_stale／node_not_ready 的可解释原因，而不是猜 ready。
type systemDispatchProjection struct {
	store      *db.Store
	rt         *Runtime
	workerNode string
}

func (p systemDispatchProjection) Policy(_ context.Context, projectID string) (matching.Policy, error) {
	view := p.rt.ManagedView()
	auto, _ := view["auto_enabled"].(bool)
	limit, _ := view["managed_execution_limit"].(int)
	supported, _ := view["platform_supported"].(bool)
	if limit < 0 {
		limit = 0
	}
	policy := matching.Policy{
		AutoExecution: model.AutoExecution{
			// 平台不支持受管启动时降级为关闭：匹配到也起不来不算可执行。
			Enabled:               auto && supported && limit >= 1,
			ManagedExecutionLimit: limit,
		},
	}
	if node := strings.TrimSpace(p.workerNode); node != "" {
		policy.Projects = []matching.ProjectRule{{
			ProjectID:    model.ProjectID(projectID),
			AllowedNodes: []model.NodeID{model.NodeID(node)},
		}}
	}
	return policy, nil
}

func (p systemDispatchProjection) Tasks(_ context.Context, projectID string) ([]matching.Task, error) {
	var out []matching.Task
	err := p.store.Read(func(tx *sql.Tx) error {
		rows, e := db.ListTasks(tx, projectID)
		if e != nil {
			return e
		}
		for _, t := range rows {
			out = append(out, matching.Task{
				ID:         t.ID,
				ProjectID:  model.ProjectID(t.ProjectID),
				Status:     model.TaskStatus(t.Status),
				Priority:   t.Priority,
				MergeOrder: t.MergeOrder,
				Deps:       t.Deps,
				WriteScope: t.WriteScope,
			})
		}
		return nil
	})
	return out, err
}

func (p systemDispatchProjection) Nodes(_ context.Context, projectID string) ([]matching.Node, error) {
	var out []matching.Node
	err := p.store.Read(func(tx *sql.Tx) error {
		rows, e := db.ListNode(tx, projectID)
		if e != nil {
			return e
		}
		for _, n := range rows {
			c, e := db.Capacity(tx, n.ID)
			if e != nil {
				return e
			}
			out = append(out, matchingNode(n, c.ActiveSlots))
		}
		return nil
	})
	return out, err
}

func (p systemDispatchProjection) ActiveManaged(_ context.Context, projectID string) (int, error) {
	var n int
	err := p.store.Read(func(tx *sql.Tx) error {
		runs, e := db.ListRuns(tx, projectID)
		if e != nil {
			return e
		}
		for _, r := range runs {
			if r.Occupies() {
				n++
			}
		}
		return nil
	})
	return n, err
}

// 节点行 meta 里的观测字段名；由写观测的一方与这里约定，缺字段即视为没有证据。
const (
	metaNodeAcceptNew   = "accept_new"
	metaNodeDraining    = "draining"
	metaNodeEntry       = "entry_state"
	metaNodeAuth        = "auth_state"
	metaNodeConnection  = "connection_state"
	metaNodeCapacity    = "capacity_state"
	metaNodeObservedAt  = "observed_at"
	metaNodeSharedGroup = "shared_group"
	metaNodeGroupLimit  = "group_limit"
	metaNodeGroupActive = "group_active"
	metaNodeCapability  = "capabilities"
)

// matchingNode 把节点行投影成匹配输入：配置默认按受管 CLI，观测证据只从 meta 取。
func matchingNode(n *db.Node, active int) matching.Node {
	out := matching.Node{
		ID:        model.NodeID(n.ID),
		ProjectID: model.ProjectID(n.ProjectID),
		Config: model.NodeConfig{
			NodeID:    model.NodeID(n.ID),
			Name:      n.Label,
			Mode:      model.NodeManagedCLI,
			SlotLimit: n.SlotLimit,
			AcceptNew: metaBool(n.Meta, metaNodeAcceptNew, true),
		},
		Draining:     metaBool(n.Meta, metaNodeDraining, false),
		Entry:        model.EntryState(metaString(n.Meta, metaNodeEntry)),
		Auth:         model.ReadinessState(metaString(n.Meta, metaNodeAuth)),
		Connection:   model.ReadinessState(metaString(n.Meta, metaNodeConnection)),
		Capacity:     model.ReadinessState(metaString(n.Meta, metaNodeCapacity)),
		ObservedAt:   metaTime(n.Meta, metaNodeObservedAt),
		ActiveSlots:  active,
		SharedGroup:  metaString(n.Meta, metaNodeSharedGroup),
		GroupLimit:   metaInt(n.Meta, metaNodeGroupLimit),
		GroupActive:  metaInt(n.Meta, metaNodeGroupActive),
		Capabilities: metaCapabilities(n.Meta),
	}
	return out
}

func metaValue(meta map[string]any, key string) (any, bool) {
	if meta == nil {
		return nil, false
	}
	v, ok := meta[key]
	return v, ok
}

func metaString(meta map[string]any, key string) string {
	v, ok := metaValue(meta, key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func metaBool(meta map[string]any, key string, def bool) bool {
	v, ok := metaValue(meta, key)
	if !ok {
		return def
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true" || b == "yes" || b == "1"
	case float64:
		return b != 0
	}
	return def
}

func metaInt(meta map[string]any, key string) int {
	v, ok := metaValue(meta, key)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i)
		}
	}
	return 0
}

func metaTime(meta map[string]any, key string) time.Time {
	s := metaString(meta, key)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// metaCapabilities 只接受能完整解析的能力证据；解析不了的条目丢弃，
// 让它们落回「缺少能力证据」，而不是变成一次伪造的 observed。
func metaCapabilities(meta map[string]any) []matching.Capability {
	v, ok := metaValue(meta, metaNodeCapability)
	if !ok {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var caps []matching.Capability
	if err := json.Unmarshal(raw, &caps); err != nil {
		return nil
	}
	out := make([]matching.Capability, 0, len(caps))
	for _, c := range caps {
		if strings.TrimSpace(c.Name) == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// systemDispatchAdvance 用协调者推进一步：launch_spec 由 PrepareRunnerArtifacts 在
// Launch 前写入，这里只回报「本次是否真的写了」。
type systemDispatchAdvance struct {
	rt    *Runtime
	coord *dispatch.Coordinator
}

func (a systemDispatchAdvance) Advance(ctx context.Context) (systemagent.DispatchStep, error) {
	if a.coord == nil {
		return systemagent.DispatchStep{}, fmt.Errorf("systemagent: 缺少协调者")
	}
	step, err := a.coord.Step(ctx)
	if err != nil {
		return systemagent.DispatchStep{}, err
	}
	out := systemagent.DispatchStep{
		OperationID: string(step.OperationID),
		Action:      string(step.Action),
		Progressed:  step.Progressed,
		Note:        step.Note,
	}
	if a.rt != nil && step.OperationID != "" {
		spec := filepath.Join(a.rt.OpDir(string(step.OperationID)), launchSpecFile)
		if _, err := os.Stat(spec); err == nil {
			out.LaunchSpec = true
		}
	}
	return out, nil
}

type appSystemEffects struct {
	cwd string
}

func (e appSystemEffects) UnblockTask(ctx context.Context, projectPath, taskID string) error {
	_ = ctx
	dir := projectPath
	if dir == "" {
		dir = e.cwd
	}
	_, err := Unblock(dir, taskID)
	return err
}

func (e appSystemEffects) RelaunchWorker(ctx context.Context, projectPath, taskID, nodeID string) error {
	_ = ctx
	dir := projectPath
	if dir == "" {
		dir = e.cwd
	}
	if strings.TrimSpace(nodeID) == "" {
		return fmt.Errorf("worker_node 未配置")
	}
	_, err := ManagedStart(dir, taskID, nodeID, "", false)
	return err
}

func (e appSystemEffects) WriteSplitProposal(ctx context.Context, projectPath, taskID, blueprint string) (string, error) {
	return systemagent.NopEffects{}.WriteSplitProposal(ctx, projectPath, taskID, blueprint)
}

func (e appSystemEffects) ApplyPlan(ctx context.Context, projectPath, blueprintPath string) error {
	_ = ctx
	dir := projectPath
	if dir == "" {
		dir = e.cwd
	}
	_, err := PlanApply(blueprintPath, dir, true, false, false)
	return err
}

func (e appSystemEffects) PlanApprovalAuto(projectPath string) bool {
	dir := projectPath
	if dir == "" {
		dir = e.cwd
	}
	pc, err := resolveProject(dir)
	if err != nil || pc.Config == nil {
		return false
	}
	return strings.EqualFold(pc.Config.Approvals.Plan, "auto")
}

// startServeLoops 在 board serve 内启动唯一 leader 循环。
//
// 一个状态目录只允许一个 leader：auto_enabled 时推进派发队列，
// system_agent.enabled 时按 poll_interval 轮询系统 Agent，两者共用同一把
// <state>/dispatch/coordinator.lock。抢不到锁的第二个 serve 会直接退出循环，
// 不把正在工作的 leader 挤掉，也不消费任何排队操作。
func startServeLoops(cwd string) {
	pc, cfg, err := loadSystemAgentConfig(cwd)
	if err != nil {
		return
	}
	rt, err := NewRuntime(pc, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve leader: runtime: %v\n", err)
		return
	}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve leader: open store: %v\n", err)
		return
	}
	coord, err := rt.BuildCoordinator(dispatch.NewDBQueue(store))
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve leader: coordinator: %v\n", err)
		return
	}

	view := rt.ManagedView()
	auto, _ := view["auto_enabled"].(bool)
	limit, _ := view["managed_execution_limit"].(int)
	supported, _ := view["platform_supported"].(bool)
	if auto && (limit < 1 || !supported) {
		// 受管执行在当前平台/上限下不可用：不推进排队操作，等人工或 CLI 处理。
		auto = false
	}

	leader := &dispatch.Leader{
		Coord:    coord,
		Dispatch: auto,
		OnError: func(err error) {
			fmt.Fprintf(os.Stderr, "serve leader: %v\n", err)
		},
	}
	if cfg.Enabled {
		// 复用 leader 已持锁的协调者，自动选题的 Step 不再自抢锁。
		orch, err := buildOrchestratorWithCoord(pc, cfg, coord)
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve leader: build orchestrator: %v\n", err)
		} else {
			w := &systemagent.Watcher{
				Store:        orch.Store,
				Config:       cfg,
				Orchestrator: orch,
				ProjectID:    pc.ID(),
				// leader 已持同一把锁，轮询不再自己抢锁，两套循环不会互相饿死。
				LeaderLock: nil,
			}
			leader.System = func(ctx context.Context) error {
				_, _, err := w.PollOnce(ctx)
				return err
			}
			leader.SystemInterval = time.Duration(cfg.PollIntervalSec) * time.Second
		}
	}
	if !auto && leader.System == nil {
		return
	}
	go func() {
		fmt.Fprintf(os.Stderr, "serve leader started (project=%s auto=%v system=%v)\n",
			pc.ID(), auto, leader.System != nil)
		if err := leader.Run(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "serve leader 退出（已有 leader 或锁不可用）: %v\n", err)
		}
	}()
}
