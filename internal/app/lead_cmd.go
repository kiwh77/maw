package app

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/systemagent"
)

// leadActor 解析当前身份并校验 lead 能力标记。
//
// lead 是本机能力标记（[identity] leads），不是新的角色体系：Role() 仍只有
// worker/integrator/reviewer。写动作一律过这道门；只读诊断命令不过门，
// 让任何人都能看清「谁被授权、开关是什么」。
func leadActor(cwd string) (string, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return "", err
	}
	actor, err := identity.Resolve(cwd, "")
	if err != nil {
		return "", err
	}
	if err := identity.RequireLead(actor, pc.Config); err != nil {
		return actor, fmt.Errorf(
			"%w；请在仓外 config.toml 的 [identity] leads 中加入 %q，或用 MAW_AGENT 指定已授权身份",
			err, actor)
	}
	return actor, nil
}

// LeadStatus 是 lead 视角的只读视图：复用系统 Agent 状态，补上授权与动作提示。
// 不写状态、不入队、不启动循环——看一眼不会改变任何东西。
func LeadStatus(cwd string) (Result, error) {
	st, err := SystemStatus(cwd)
	if err != nil {
		return nil, err
	}
	actor, _ := identity.Resolve(cwd, "")
	st["actor"] = actor
	if pc, e := resolveProject(cwd); e == nil && pc.Config != nil {
		st["leads"] = pc.Config.Identity.Leads
		st["is_lead"] = identity.IsLead(actor, pc.Config)
	}
	st["view"] = "lead"
	st["next"] = "maw lead split --from <需求文件> / maw lead signals / maw lead dispatch"
	return st, nil
}

// LeadDispatch 由 lead 显式发起一次调度：
// 入队 dispatch_ready（显式入队，不受 auto_kinds 限制）→ 立刻消费该 job。
//
// 设计边界（见 docs/maw_lead_and_node_model_可执行任务清单.md §2.4）：
//   - 不打开常驻循环、不改 auto_enabled；循环仍归 maw system watch。
//   - 不选题、不造 operation：Dispatcher 只消费队列里已有的意图。
//   - 这是**有意的显式动作**：KindAllowed(explicit=true) 会放行，
//     Orchestrator.Step 也会强制消费 dispatch_ready，所以它会真派发。
//     想改这个语义要改 classifier/orchestrator，不是改配置。
func LeadDispatch(cwd, message string) (Result, error) {
	actor, err := leadActor(cwd)
	if err != nil {
		return nil, err
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "lead dispatch"
	}
	trig, err := SystemTrigger(cwd, systemagent.SystemKindDispatchReady, "", msg)
	if err != nil {
		return nil, err
	}
	if trig == nil {
		return Result{"ok": false, "error": "empty_trigger_result"}, nil
	}
	jobID, _ := trig["job_id"].(string)
	if jobID == "" {
		// 冷却期内被去重：不是失败，如实回报原状，不伪造一次调度。
		return Result{"ok": true, "actor": actor, "deduped": true, "trigger": trig}, nil
	}
	ran, err := SystemRun(cwd, jobID)
	if err != nil {
		return nil, err
	}
	return Result{
		"ok": true, "actor": actor, "job_id": jobID,
		"kind": systemagent.SystemKindDispatchReady, "run": ran,
		"next": "maw system status --json", "message": msg,
	}, nil
}

// LeadSplit 把一段需求拆成 [[task]] 草稿并**追加**到现有蓝图末尾。
//
// 历史事故防线：GenerateTaskBlocks 只产出追加片段（不含 [meta]/[iteration]），
// 绝不产出只含新题的独立计划文件；AppendTasks 保留既有题全集并拒绝重复 id。
// write=false 时只返回 diff，不碰磁盘；即便 write=true 也不自动 plan apply，
// 人工确认仍由 maw plan check / maw plan apply 完成。
func LeadSplit(cwd, brief, fromPath, lane string, ids []string, write bool) (Result, error) {
	actor, err := leadActor(cwd)
	if err != nil {
		return nil, err
	}
	blocks, generated, err := GenerateTaskBlocks(SplitRequest{
		Brief: brief, FromPath: fromPath, Lane: lane, IDs: ids,
	})
	if err != nil {
		return nil, err
	}
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	bp := home.BlueprintPath(pc.LocalHome)
	diff, err := AppendTasks(bp, blocks, !write)
	if err != nil {
		return nil, err
	}
	return Result{
		"ok": true, "actor": actor, "ids": generated, "count": len(generated),
		"written": write, "blueprint": bp, "diff": diff,
		"next": "maw plan check " + bp + " --json → 人工确认后 maw plan apply",
	}, nil
}

// LeadSignals 检测四类系统级信号。默认纯只读；enqueue=true 才入队（需 lead 身份）。
//
// 只观察的保证在代码层：EnqueueSignals 用 KindAllowed(explicit=false) 过滤，
// 且 all_agents_offline 预置 escalate_human，Orchestrator 会直接升级人工、
// 跳过 Dispatcher——即便运维把 dispatch_ready 放进 auto_kinds 也不会真派发。
func LeadSignals(cwd string, enqueue bool) (Result, error) {
	pc, cfg, err := loadSystemAgentConfig(cwd)
	if err != nil {
		return nil, err
	}
	actor := ""
	if enqueue {
		actor, err = leadActor(cwd)
		if err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	signals := systemagent.DetectSystemSignals(newHubSignalFacts(pc.GlobalDB, cfg), cfg, now)
	rows := make([]map[string]any, 0, len(signals))
	for _, s := range signals {
		rows = append(rows, map[string]any{
			"kind": string(s.Kind), "key": s.Key(), "project_id": s.ProjectID,
			"task_id": s.TaskID, "resource_key": s.ResourceKey,
			"reason": s.Reason, "job_kind": s.JobKind, "escalate": s.Escalate,
		})
	}
	out := Result{
		"ok": true, "actor": actor, "count": len(signals), "signals": rows,
		"auto_kinds": cfg.AutoKinds, "enqueued": 0,
		"note": "只读；--enqueue 才入队，且受 auto_kinds 与升级人工语义约束",
	}
	if !enqueue || len(signals) == 0 {
		return out, nil
	}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil, err
	}
	var jobs []*db.SystemJob
	if err := store.Write(func(tx *sql.Tx) error {
		var e error
		jobs, e = systemagent.EnqueueSignals(tx, cfg, signals, now)
		return e
	}); err != nil {
		return nil, err
	}
	out["enqueued"] = len(jobs)
	return out, nil
}
