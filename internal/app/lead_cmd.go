package app

import (
	"strings"

	"github.com/kiwh77/maw/internal/systemagent"
)

// LeadStatus 是 lead 视角的只读视图：复用系统 Agent 状态，补上可执行动作提示。
// 不写状态、不入队、不启动循环——看一眼不会改变任何东西。
func LeadStatus(cwd string) (Result, error) {
	st, err := SystemStatus(cwd)
	if err != nil {
		return nil, err
	}
	st["view"] = "lead"
	st["next"] = "maw lead split --from <需求文件> --dry-run / maw lead dispatch"
	return st, nil
}

// LeadDispatch 由 lead 显式发起一次调度：
// 入队 dispatch_ready（显式入队，不受 auto_kinds 限制）→ 立刻消费该 job。
//
// 设计边界（见 docs/maw_lead_and_node_model_可执行任务清单.md §2.4）：
//   - 不打开常驻循环、不改 auto_enabled；循环仍归 maw system watch。
//   - 不选题、不造 operation：Dispatcher 只消费队列里已有的意图。
//   - 默认关闭的 [system_agent] enabled 不挡这次显式动作，但动作全程留
//     system_jobs 与审计 sidecar，可回溯。
func LeadDispatch(cwd, message string) (Result, error) {
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
		return Result{"ok": true, "deduped": true, "trigger": trig}, nil
	}
	ran, err := SystemRun(cwd, jobID)
	if err != nil {
		return nil, err
	}
	return Result{
		"ok":      true,
		"job_id":  jobID,
		"kind":    systemagent.SystemKindDispatchReady,
		"run":     ran,
		"next":    "maw system status --json",
		"message": msg,
	}, nil
}
