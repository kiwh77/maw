package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/dispatch"
	"github.com/kiwh77/maw/internal/engine/matching"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/systemagent"
)

// serve leader 的事件与节流参数。
//
// 一次 board serve 会为全局库里每个已登记且路径仍存在的工程各尝试起一个 leader：
// 每个工程用自己的状态目录锁、自己的两个开关，互不推导。循环里的瞬时错误如果每轮
// 都写事件会把时间轴刷爆，因此同类错误在一个窗口内只落一条。
const (
	leadLoopErrorWindow      = 60 * time.Second
	leadLoopErrorEvent       = "lead_loop_error"
	leadLoopAttemptEvent     = "lead_loop_attempt"
	leadLoopLockSkippedEvent = "lead_loop_lock_skipped"
)

// serveLeaderProject 是启动器需要的工程登记视图（不直接暴露 db.Project，便于替换来源）。
type serveLeaderProject struct {
	ID   string
	Name string
	Path string
}

// serveLeaderRunner 是常驻循环的最小端口；*dispatch.Leader 直接满足。
type serveLeaderRunner interface {
	Run(ctx context.Context) error
}

// serveLeaderBundle 是单个工程装配出的常驻循环，含本工程的两个开关与常驻 Watcher。
type serveLeaderBundle struct {
	runner   serveLeaderRunner
	watcher  *systemagent.Watcher
	dispatch bool
	system   bool
	// producer 为 true 表示 auto_select 已开启：即使两个既有开关全关，
	// leader 仍要起循环来跑生产者（三开关互不推导）。
	producer bool
}

// serveLeaderOutcome 记录每个工程的处置结果：尝试了什么、有没有起 goroutine、为什么跳过。
type serveLeaderOutcome struct {
	ProjectID string `json:"project_id"`
	Project   string `json:"project"`
	Attempted bool   `json:"attempted"`
	Started   bool   `json:"started"`
	Reason    string `json:"reason"`
}

// serveLeaderDeps 把「列举工程／读配置／装配循环／起 goroutine／写事件」全部变成端口，
// 让多工程覆盖、锁竞争与错误节流都能在不真起进程的测试里断言。
type serveLeaderDeps struct {
	now      func() time.Time
	projects func() ([]serveLeaderProject, error)
	pathOK   func(string) bool
	load     func(string) (*projCtx, config.SystemAgent, error)
	build    func(*projCtx, config.SystemAgent, func(error)) (*serveLeaderBundle, error)
	launch   func(*projCtx, *serveLeaderBundle, func(error))
	write    func(projectID, actor, typ, taskID string, data any) error
	logf     func(string, ...any)
}

// startServeLoops 在 board serve 内为全部已登记工程启动 leader。
//
// 签名与调用点保持不变（board_cmd.go 仍传 cwd）。节点探测是全局一次，
// 之后每个工程各自 load 配置、各自持锁，所以某个工程目录失效或配置损坏
// 不会阻止其它工程起循环。
func startServeLoops(cwd string) {
	pc, _, err := loadSystemAgentConfig(cwd)
	if err != nil {
		return
	}
	startNodeProbeLoop(pc.GlobalDB)
	startServeLeaders(context.Background(), defaultServeLeaderDeps(pc.GlobalDB))
}

// startServeLeaders 遍历全局库里所有已登记工程，逐个装配并启动 leader。
//
// 「抢不到锁」由 dispatch.Leader.Run 返回 ErrCoordinatorActive：那不是失败，
// 说明该工程已有 serve 在跑，本次跳过并留一条可查询的事件，不消费任何操作。
func startServeLeaders(ctx context.Context, deps serveLeaderDeps) []serveLeaderOutcome {
	projects, err := deps.projects()
	if err != nil {
		deps.logf("serve leader: 列举已登记工程失败: %v\n", err)
		return nil
	}
	rec := &leadLoopErrorRecorder{
		now: deps.now, window: leadLoopErrorWindow, write: deps.write,
		last: map[string]time.Time{},
	}
	out := make([]serveLeaderOutcome, 0, len(projects))
	for _, p := range projects {
		o := serveLeaderOutcome{ProjectID: p.ID, Project: p.Name, Attempted: true}
		switch {
		case p.ID == "" || p.Path == "" || !deps.pathOK(p.Path):
			o.Reason = "path_missing"
		default:
			pc, cfg, loadErr := deps.load(p.Path)
			if loadErr != nil {
				o.Reason = "config_error"
				deps.logf("serve leader: project=%s 配置不可读: %v\n", p.ID, loadErr)
			} else {
				b, buildErr := deps.build(pc, cfg, func(err error) { rec.report(p.ID, err) })
				switch {
				case buildErr != nil:
					o.Reason = "build_error"
					deps.logf("serve leader: project=%s 装配失败: %v\n", p.ID, buildErr)
				case b == nil || b.runner == nil || (!b.dispatch && !b.system && !b.producer):
					// 三个开关全关：一条循环都不该跑，也不该留下空转的 leader 占着锁。
					o.Reason = "no_switches"
				default:
					o.Started = true
					o.Reason = "started"
					projectID := pc.ID()
					deps.launch(pc, b, func(runErr error) {
						recordServeLeaderExit(deps, rec, projectID, runErr)
					})
				}
			}
		}
		// 每个工程都留一条尝试记录：跳过了也能在事件时间轴里查到原因。
		_ = deps.write(p.ID, "system", leadLoopAttemptEvent, "", map[string]any{
			"project_id": p.ID, "project": p.Name,
			"dispatch": o.Reason == "started", "started": o.Started, "reason": o.Reason,
		})
		out = append(out, o)
	}
	return out
}

// recordServeLeaderExit 把 Run 的返回值变成可查询事实，而不是只打 stderr。
func recordServeLeaderExit(deps serveLeaderDeps, rec *leadLoopErrorRecorder, projectID string, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, dispatch.ErrCoordinatorActive) {
		_ = deps.write(projectID, "system", leadLoopLockSkippedEvent, "", map[string]any{
			"reason": "lock_held", "error": err.Error(),
		})
		deps.logf("serve leader: project=%s 已有 leader，本次跳过\n", projectID)
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	rec.report(projectID, err)
}

// leadErrorKind 是节流的「同类」口径：按错误性质归类，而不是按完整消息。
// 同类错误在同一窗口内只落一条事件，避免每轮轮询都往时间轴里塞一行。
func leadErrorKind(err error) string {
	switch {
	case errors.Is(err, dispatch.ErrCoordinatorActive):
		return "lock_active"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "context"
	default:
		return "loop_step"
	}
}

// leadLoopErrorRecorder 对 (工程, 错误类别) 做时间窗口节流。
type leadLoopErrorRecorder struct {
	mu     sync.Mutex
	now    func() time.Time
	window time.Duration
	write  func(projectID, actor, typ, taskID string, data any) error
	last   map[string]time.Time
}

func (r *leadLoopErrorRecorder) report(projectID string, err error) {
	if err == nil {
		return
	}
	kind := leadErrorKind(err)
	key := projectID + "\x00" + kind
	now := r.now()
	r.mu.Lock()
	if t, ok := r.last[key]; ok && now.Sub(t) < r.window {
		r.mu.Unlock()
		return
	}
	r.last[key] = now
	r.mu.Unlock()
	_ = r.write(projectID, "system", leadLoopErrorEvent, "", map[string]any{
		"kind": kind, "error": err.Error(),
	})
}

// buildServeWatcher 装配常驻 Watcher。
//
// 与 CLI 的 SystemWatch 一样必须注入真实证据源：SignalFacts 为 nil 时 PollOnce
// 会完全跳过系统级信号检测，常驻循环就永远发现不了卡住的任务/离线节点。
// leader 已持有本工程的 coordinator.lock，这里不再让 Watcher 自己抢锁。
//
// Housekeeping 让过期心跳巡检不依赖任何 worker 调 maw next：中枢常驻就能发现
// 掉线的 Agent。时钟统一走 w.Clock（测试可注入确定性时刻）。
func buildServeWatcher(pc *projCtx, cfg config.SystemAgent, orch *systemagent.Orchestrator) *systemagent.Watcher {
	w := &systemagent.Watcher{
		Store:        orch.Store,
		Config:       cfg,
		Orchestrator: orch,
		ProjectID:    pc.ID(),
		LeaderLock:   nil,
		SignalFacts:  newHubSignalFacts(pc.GlobalDB, cfg),
	}
	// 延迟读取 w.Clock：调用方在构造后再注入 Clock 也能生效；未注入时用真实时间。
	w.Housekeeping = leadHousekeeping(func() time.Time {
		if w.Clock != nil {
			return w.Clock().UTC()
		}
		return time.Now().UTC()
	}, defaultLeadHousekeepingDeps(pc.GlobalDB))
	return w
}

// buildServeLeaderBundle 为单个工程装配常驻循环：本工程的 Runtime／库／队列／开关。
func buildServeLeaderBundle(pc *projCtx, cfg config.SystemAgent, onError func(error)) (*serveLeaderBundle, error) {
	rt, err := NewRuntime(pc, "")
	if err != nil {
		return nil, err
	}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil, err
	}
	// 队列按本工程过滤：全局库由多工程共用，不隔离会领走别人的 operation。
	coord, err := rt.BuildCoordinator(dispatch.NewDBQueue(store, model.ProjectID(pc.ID())))
	if err != nil {
		return nil, err
	}

	view := rt.ManagedView()
	auto, _ := view["auto_enabled"].(bool)
	limit, _ := view["managed_execution_limit"].(int)
	supported, _ := view["platform_supported"].(bool)
	if auto && (limit < 1 || !supported) {
		// 受管执行在当前平台/上限下不可用：不推进排队操作，等人或 CLI 处理。
		auto = false
	}
	// 系统 Agent 开关与派发开关互不推导；平台限制不影响阻塞自愈。
	systemOn := cfg.Enabled
	if pc.Config != nil {
		systemOn = pc.Config.SystemAgentEnabled()
	}
	autoSelect := pc.Config != nil && pc.Config.Managed.AutoSelect

	leader := &dispatch.Leader{Coord: coord, Dispatch: auto, OnError: onError}
	b := &serveLeaderBundle{dispatch: auto, producer: autoSelect}
	// 自愈轮询与自动选题都需要编排器；后者即便 system_agent.enabled=false
	// 也必须能 RunJobID，所以装配条件是两个开关的并集。
	if systemOn || autoSelect {
		orch, err := buildOrchestratorWithCoord(pc, cfg, coord)
		if err != nil {
			// 单个工程编排器装配失败不该拖垮其它工程：保留派发循环，只丢自愈/生产。
			fmt.Fprintf(os.Stderr, "serve leader: project=%s build orchestrator: %v\n", pc.ID(), err)
		} else {
			if systemOn {
				w := buildServeWatcher(pc, cfg, orch)
				b.watcher = w
				leader.System = func(ctx context.Context) error {
					_, _, err := w.PollOnce(ctx)
					return err
				}
				leader.SystemInterval = time.Duration(cfg.PollIntervalSec) * time.Second
			}
			if autoSelect {
				leader.Producer = buildAutoSelectProducer(pc, cfg, store, rt, orch, limit)
			}
		}
	}
	b.system = leader.System != nil
	b.runner = leader
	// 中枢重启后先按持久化证据重建目标视图：只读、幂等，不推进状态。
	// 失败只记录到 stderr，不影响派发循环启动（恢复不是启动的前提）。
	if _, err := recoverGoalsForProject(pc); err != nil && onError != nil {
		onError(fmt.Errorf("recover goals: %w", err))
	}
	return b, nil
}

// buildAutoSelectProducer 装配 auto_select 生产者回调。
//
// 选题判定复用 matching.Match 与 systemDispatchProjection，不另立一套「可派发」规则；
// 上限沿用本工程受管硬上限，因此「无空槽」由匹配层原样给出。执行直接调
// Orchestrator.RunJobID（不检查 Config.Enabled），保证 system_agent.enabled=false
// 时生产的作业仍能在同周期被执行并产生 Run。
func buildAutoSelectProducer(pc *projCtx, cfg config.SystemAgent, store *db.Store,
	rt *Runtime, orch *systemagent.Orchestrator, limit int) func(context.Context) error {
	proj := systemDispatchProjection{store: store, rt: rt, workerNode: cfg.WorkerNode}
	deps := defaultServeLeaderDeps(pc.GlobalDB)
	ports := systemagent.NewStoreProducerPorts(store, cfg,
		func() ([]systemagent.ProducerProject, error) {
			rows, err := deps.projects()
			if err != nil {
				return nil, err
			}
			out := make([]systemagent.ProducerProject, 0, len(rows))
			for _, r := range rows {
				out = append(out, systemagent.ProducerProject{ID: r.ID, Path: r.Path})
			}
			return out, nil
		},
		func(projectID string) (string, bool, error) {
			ctx := context.Background()
			policy, err := proj.Policy(ctx, projectID)
			if err != nil {
				return "", false, err
			}
			tasks, err := proj.Tasks(ctx, projectID)
			if err != nil {
				return "", false, err
			}
			nodes, err := proj.Nodes(ctx, projectID)
			if err != nil {
				return "", false, err
			}
			active, err := proj.ActiveManaged(ctx, projectID)
			if err != nil {
				return "", false, err
			}
			if limit > 0 {
				policy.AutoExecution.ManagedExecutionLimit = limit
			}
			// auto_select 是独立的选题授权：它只决定要不要生产 dispatch_ready，
			// 不改变 auto_enabled 对派发消费循环的语义。这里把匹配策略的开关
			// 换成 auto_select 已开启这一事实，其余门禁（上限、节点就绪、证据
			// 时效、依赖、写盘冲突）仍全部由 matching.Match 判定，不绕过匹配。
			policy.AutoExecution.Enabled = true
			if policy.AutoExecution.ManagedExecutionLimit < 1 {
				policy.AutoExecution.ManagedExecutionLimit = 1
			}
			dec, err := matching.Match(matching.Request{
				Policy: policy, Tasks: tasks, Nodes: nodes, ActiveManaged: active,
			})
			if err != nil {
				return "", false, err
			}
			return dec.TaskID, dec.Matched, nil
		},
		func(ctx context.Context, jobID string) error {
			return orch.RunJobID(ctx, jobID)
		},
	)
	producer := &systemagent.Producer{
		Config: systemagent.ProducerConfig{
			AutoSelect:  true,
			CooldownSec: cfg.CooldownoutSec,
			MaxAttempts: cfg.MaxAttempts,
		},
		Ports: ports,
	}
	return func(ctx context.Context) error {
		_, err := producer.Produce(ctx)
		return err
	}
}

// defaultServeLeaderDeps 是生产装配：工程清单来自全局库，事件写回同一库。
func defaultServeLeaderDeps(gdb string) serveLeaderDeps {
	return serveLeaderDeps{
		now: time.Now,
		projects: func() ([]serveLeaderProject, error) {
			var out []serveLeaderProject
			err := db.WithTx(gdb, func(tx *sql.Tx) error {
				rows, e := db.ListProjects(tx)
				if e != nil {
					return e
				}
				for _, p := range rows {
					if p == nil {
						continue
					}
					out = append(out, serveLeaderProject{ID: p.ID, Name: p.Name, Path: p.Path})
				}
				return nil
			})
			return out, err
		},
		pathOK: func(p string) bool {
			st, err := os.Stat(p)
			return err == nil && st.IsDir()
		},
		load:  loadSystemAgentConfig,
		build: buildServeLeaderBundle,
		launch: func(_ *projCtx, b *serveLeaderBundle, onExit func(error)) {
			go func() { onExit(b.runner.Run(context.Background())) }()
		},
		write: func(projectID, actor, typ, taskID string, data any) error {
			return db.WithTx(gdb, func(tx *sql.Tx) error {
				return db.AppendEvent(tx, projectID, actor, typ, taskID, data)
			})
		},
		logf: func(format string, args ...any) { fmt.Fprintf(os.Stderr, format, args...) },
	}
}
