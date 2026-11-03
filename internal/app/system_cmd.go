package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/dispatch"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
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
	store, err := db.OpenStore(pc.GlobalDB)
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
		orch, err := buildOrchestrator(pc, cfg)
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
