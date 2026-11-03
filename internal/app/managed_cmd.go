package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/claim"
	"github.com/kiwh77/maw/internal/engine/dispatch"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/runner/dbstore"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/process"
	"github.com/kiwh77/maw/internal/runner/session"
)

// ManagedStart 显式按任务／节点进入受管派发：ClaimExecution → Operation → 独立 Runner。
//
// 自动选题关闭时仍允许本路径；窗口／中枢退出后 Runner 继续存活（独立进程组）。
func ManagedStart(cwd, taskID, nodeID, agentFlag string, dryRun bool) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	cfgPath := home.ConfigPath(pc.LocalHome)
	if _, err := config.EnsureManagedSection(cfgPath); err != nil {
		return nil, err
	}
	// 重新加载，确保迁移后的视图一致。
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	pc.Config = cfg

	rt, err := NewRuntime(pc, "")
	if err != nil {
		return nil, err
	}
	taskID = strings.TrimSpace(taskID)
	nodeID = strings.TrimSpace(nodeID)
	explicit := taskID != "" && nodeID != ""
	if !explicit {
		return nil, i18n.E("err.managed_need_explicit", "next.managed_explicit")
	}
	if err := rt.EnsureManagedAllowed(true); err != nil {
		return nil, err
	}
	if !model.NodeID(nodeID).Valid() {
		return nil, i18n.E("err.managed_bad_node", "next.managed_node", nodeID)
	}

	who, _, err := resolveManagedAgent(cwd, agentFlag, pc)
	if err != nil {
		return nil, err
	}
	if dryRun {
		view := rt.ManagedView()
		view["ok"] = true
		view["dry_run"] = true
		view["task_id"] = taskID
		view["node_id"] = nodeID
		view["agent"] = who
		view["next"] = i18n.T("next.managed_start_real")
		return view, nil
	}

	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	var task *db.Task
	err = store.Read(func(tx *sql.Tx) error {
		t, e := db.GetTask(tx, pc.ID(), taskID)
		if e != nil {
			return e
		}
		if t == nil {
			return i18n.E("err.unknown_task", "next.task_list", taskID)
		}
		task = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(task.InstanceID) == "" {
		return nil, i18n.E("err.managed_no_instance", "next.plan_apply", taskID)
	}
	instID := model.InstanceID(task.InstanceID)
	if !instID.Valid() {
		return nil, i18n.E("err.managed_no_instance", "next.plan_apply", taskID)
	}

	workspace := filepath.Join(rt.StateDir(), "workspaces", taskID)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, err
	}
	absWS, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}

	// 确保节点已登记（首闭环显式节点；未登记时给出可操作下一步）。
	err = store.Write(func(tx *sql.Tx) error {
		n, e := db.GetNode(tx, nodeID)
		if e != nil {
			return e
		}
		if n != nil {
			return nil
		}
		return db.UpsertNode(tx, &db.Node{
			ID: nodeID, ProjectID: pc.ID(), Label: nodeID, SlotLimit: 1,
		})
	})
	if err != nil {
		return nil, err
	}

	enabled, limit := pc.Config.AutoExecution()
	_ = enabled
	svc := claim.New(store, nil)
	outcome, err := svc.Claim(context.Background(), claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID:      model.ProjectID(pc.ID()),
			AgentID:        model.AgentID(who),
			RequestedTask:  &instID,
			IdempotencyKey: "managed-start:" + taskID + ":" + nodeID + ":" + who,
			Capacity: ports.Capacity{
				NodeSlotLimit: 1,
				ManagedLimit:  limit,
			},
		},
		NodeID:    model.NodeID(nodeID),
		Workspace: absWS,
		Branch:    task.Branch,
		Worktree:  task.Worktree,
	})
	if err != nil {
		return nil, mapManagedClaimErr(err)
	}

	queue := dispatch.NewDBQueue(store)
	coord, err := rt.BuildCoordinator(queue)
	if err != nil {
		return nil, err
	}
	if err := coord.Acquire(); err != nil {
		return nil, i18n.EW("err.managed_coord_lock", "next.managed_status", err)
	}
	defer func() { _ = coord.Release() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	step, err := coord.Step(ctx)
	if err != nil {
		return nil, i18n.EW("err.managed_dispatch", "next.managed_status", err)
	}

	out := rt.ManagedView()
	out["ok"] = true
	out["task_id"] = outcome.TaskID
	out["task_instance"] = string(outcome.Instance)
	out["run_id"] = string(outcome.RunID)
	out["operation_id"] = string(outcome.OperationID)
	out["generation"] = int(outcome.Generation)
	out["node_id"] = nodeID
	out["workspace"] = absWS
	out["agent"] = who
	out["replay"] = outcome.Replay
	out["runner_detached"] = true
	out["dispatch_step"] = map[string]any{
		"operation_id": string(step.OperationID),
		"action":       string(step.Action),
		"progressed":   step.Progressed,
		"note":         step.Note,
		"launched":     step.Action == dispatch.ActionLaunched || step.Action == dispatch.ActionResumed,
	}
	out["next"] = i18n.T("next.managed_running")
	return out, nil
}

// ManagedStatus 展示受管配置视图（版本、上限、自动开关、平台）。
func ManagedStatus(cwd string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	cfgPath := home.ConfigPath(pc.LocalHome)
	if _, err := config.EnsureManagedSection(cfgPath); err != nil {
		return nil, err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	pc.Config = cfg
	rt, err := NewRuntime(pc, "")
	if err != nil {
		return nil, err
	}
	out := rt.ManagedView()
	out["ok"] = true
	out["state_dir"] = rt.StateDir()
	out["next"] = i18n.T("next.managed_explicit")
	return out, nil
}

// RunnerOpts 是内部 `__runner` 入口的白名单参数。
type RunnerOpts struct {
	OperationID string
	Nonce       string
	StateDir    string
	Token       string
	// Once 为真时校验完立即退出（测试用）；生产路径阻塞直到信号。
	Once bool
}

// InternalRunner 是同一二进制的受限内部入口：校验引导令牌、参数与排他锁后驻留。
//
// 不接受白名单外参数；无 GUI；重复启动同一 Operation 会被锁拒绝。
func InternalRunner(opts RunnerOpts) (Result, error) {
	opID := strings.TrimSpace(opts.OperationID)
	nonce := strings.TrimSpace(opts.Nonce)
	stateDir := strings.TrimSpace(opts.StateDir)
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv(runnerTokenEnv))
	}
	if opID == "" || nonce == "" || stateDir == "" || token == "" {
		return nil, i18n.E("err.managed_runner_args", "next.managed_internal")
	}
	if !model.OperationID(opID).Valid() {
		return nil, i18n.E("err.managed_runner_args", "next.managed_internal")
	}
	if !filepath.IsAbs(stateDir) {
		abs, err := filepath.Abs(stateDir)
		if err != nil {
			return nil, err
		}
		stateDir = abs
	}
	opDir := filepath.Join(stateDir, "ops", opID)
	wantPath := filepath.Join(opDir, runnerTokenFile)
	want, err := os.ReadFile(wantPath)
	if err != nil {
		return nil, i18n.E("err.managed_runner_token", "next.managed_internal")
	}
	if strings.TrimSpace(string(want)) != token {
		return nil, i18n.E("err.managed_runner_token", "next.managed_internal")
	}
	if err := os.MkdirAll(opDir, 0o755); err != nil {
		return nil, err
	}
	lock, err := process.AcquireLock(filepath.Join(opDir, runnerLockFile))
	if err != nil {
		if errors.Is(err, process.ErrLocked) {
			return nil, i18n.E("err.managed_runner_dup", "next.managed_status")
		}
		if errors.Is(err, process.ErrUnsupported) {
			return nil, i18n.E("err.managed_platform", "next.manual_or_observe", "unsupported")
		}
		return nil, err
	}
	defer func() { _ = lock.Release() }()

	id, err := process.Capture(opID, os.Getpid(), nonce)
	if err != nil {
		// Capture 在部分环境下读不到启动时间时仍允许驻留，但必须留下 pid/nonce 证据。
		id = process.Identity{
			RunID:  opID,
			PID:    os.Getpid(),
			Nonce:  nonce,
			Start:  time.Now().UTC(),
			Booted: time.Now().UTC(),
		}
	}
	if err := process.WriteIdentity(filepath.Join(opDir, "process.json"), id); err != nil {
		return nil, err
	}

	out := Result{
		"ok":           true,
		"operation_id": opID,
		"nonce":        nonce,
		"pid":          id.PID,
		"state_dir":    stateDir,
		"headless":     true,
	}
	if opts.Once || os.Getenv("MAW_RUNNER_ONCE") == "1" {
		out["once"] = true
		// 测试 once 路径仍尝试接线 session（有 launch_spec 时）。
		if rep, wired, err := maybeRunSession(opDir, opID); wired {
			out["session_wired"] = true
			if err != nil {
				out["session_error"] = err.Error()
			} else {
				out["session_outcome"] = string(rep.Outcome)
			}
		}
		return out, nil
	}
	// RUNNER-WIRE：若 ops 目录有 launch_spec.json，接入 session.Runner 起真子进程。
	if rep, wired, err := maybeRunSession(opDir, opID); wired {
		out["session_wired"] = true
		if err != nil {
			out["session_error"] = err.Error()
			out["ok"] = false
			return out, err
		}
		out["session_outcome"] = string(rep.Outcome)
		out["stopped"] = true
		return out, nil
	}
	// 无 GUI 驻留：只响应终止信号；中枢退出不会到这里（我们是独立进程组）。
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	<-ch
	out["stopped"] = true
	return out, nil
}

// launchSpecFile 是 Coordinator/系统 Agent 在 Launch 前写入的启动规格文件名。
// 写入方见 Runtime.PrepareRunnerArtifacts：显式来源缺失时不写，Runner 保持驻留。
const launchSpecFile = "launch_spec.json"

// maybeRunSession 若存在 launch_spec.json 则用 session.Runner + passthrough Source 执行。
func maybeRunSession(opDir, opID string) (session.Report, bool, error) {
	specPath := filepath.Join(opDir, launchSpecFile)
	b, err := os.ReadFile(specPath)
	if err != nil {
		return session.Report{}, false, nil
	}
	var spec model.LaunchSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		return session.Report{}, true, err
	}
	// 与 Launch 侧写出的形状共用同一个结构：读的一侧跟着写的一侧变，不会再漂移。
	metaPath := filepath.Join(opDir, runMetaFile)
	meta := RunMeta{}
	if mb, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(mb, &meta)
	}
	if meta.RunID == "" {
		meta.RunID = runIDForOperation(opID)
	}
	if meta.InstanceID == "" {
		meta.InstanceID = "ti_systemagent"
	}
	if meta.Generation < 1 {
		meta.Generation = 1
	}
	jr, err := journal.Open(filepath.Join(opDir, "journal"), journal.Options{RunID: meta.RunID, Sync: true})
	if err != nil {
		return session.Report{}, true, err
	}
	defer jr.Close()

	var store session.Store = recordingNopStore{}
	if meta.GlobalDB != "" {
		dbStore, err := db.OpenStore(meta.GlobalDB)
		if err == nil {
			store = &dbstore.Store{DB: dbStore, ProjectID: meta.ProjectID}
		}
	}
	r, err := session.New(store, dbstore.PassthroughSource{}, jr)
	if err != nil {
		return session.Report{}, true, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rep, err := r.Run(ctx, session.Config{
		Instance:   model.InstanceID(meta.InstanceID),
		RunID:      model.RunID(meta.RunID),
		Generation: model.Generation(meta.Generation),
		Spec:       spec,
		Protocol:   spec.ExpectedProtocol,
		Heartbeat:  2 * time.Second,
	})
	return rep, true, err
}

// recordingNopStore 在无全局库时吞掉 Apply（仍允许进程跑完）。
type recordingNopStore struct{}

func (recordingNopStore) Apply(ctx context.Context, req ports.StoreRequest) (ports.StoreResult, error) {
	_ = ctx
	out := ports.StoreResult{}
	for _, op := range req.Ops {
		out.Applied = append(out.Applied, op.Kind)
	}
	return out, nil
}

func resolveManagedAgent(cwd, agentFlag string, pc *projCtx) (string, bool, error) {
	var agents []*db.Agent
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		list, e := db.ListAgents(tx, pc.ID())
		if e != nil {
			return e
		}
		agents = list
		return nil
	})
	if err != nil {
		return "", false, err
	}
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	who, allocated, err := identity.ResolveOrAllocate(cwd, agentFlag, pc.Project.Name, ids)
	if err != nil {
		return "", false, err
	}
	if !model.AgentID(who).Valid() {
		return "", false, i18n.E("err.managed_bad_agent", "next.managed_agent", who)
	}
	return who, allocated, nil
}

func mapManagedClaimErr(err error) error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*errx.Error); ok {
		return e
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "capacity") || strings.Contains(msg, "上限"):
		return i18n.EW("err.managed_capacity", "next.managed_status", err)
	case strings.Contains(msg, "no_ready") || strings.Contains(msg, "没有处于 ready"):
		return i18n.EW("err.managed_not_ready", "next.task_list", err)
	default:
		return i18n.EW("err.managed_claim", "next.managed_status", err)
	}
}
