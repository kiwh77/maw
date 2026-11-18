package app

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/goal"
)

// 本文件是目标的收尾链路（契约 maw.lead.goal/v1 §12–§13）：
// 逐条验收 → 聚合 → 决定 done／返工／暂停／升级，以及重启后的只读恢复。
//
// 边界：
//   - 验收只读持久化证据（交付、任务状态、映射），不凭内存里的进度；
//   - 返工只产出「带证据的提案 + 新计划修订」，任务仍由批准后入队创建；
//   - 重复验收／重复恢复不产生新任务，也不重复推进状态。

// LeadGoalAcceptInput 是一次整体验收请求。
type LeadGoalAcceptInput struct {
	GoalID string
	// Manual 是人工验收结论：criterion_id → pass|fail。
	Manual map[string]string
	DryRun bool
	// TimeoutSec 是单条 command 验收的上限，默认 300。
	TimeoutSec int
}

// LeadGoalAccept 按目标验收条件给出整体结论，并在需要时推进目标状态。
//
// 默认只判不写：`--dry-run` 与「验收后还有返工空间」都不改目标状态。
func LeadGoalAccept(cwd string, in LeadGoalAcceptInput) (Result, error) {
	actor, err := leadActor(cwd)
	if err != nil {
		return nil, err
	}
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	goalID := strings.TrimSpace(in.GoalID)
	if goalID == "" {
		return nil, fmt.Errorf("需要 --goal")
	}
	var snap *db.GoalSnapshot
	if err := withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		snap, e = db.LoadGoalSnapshot(tx, pc.ID(), goalID)
		return e
	}); err != nil {
		return nil, err
	}

	results := evaluateGoalCriteria(pc, snap, in.Manual, in.TimeoutSec)
	overall := goal.OverallVerdict(results)
	budget := goalReworkBudget(snap)

	decision := goal.DecideCompletion(snap.Goal.Status, overall, budget)
	// 预算耗尽优先判暂停——但只在「还没通过」时：全通过就结束，不再需要预算。
	if overall != goal.VerdictPass && budgetExhausted(snap) {
		decision = goal.BudgetExhaustedDecision()
	}

	proposal := goal.NewReworkProposal(goalID, goal.FailingResults(results))
	out := Result{
		"ok": true, "goal_id": goalID, "goal_status": snap.Goal.Status,
		"overall": overall, "criteria": results, "decision": decision,
		"rework": proposal, "dry_run": in.DryRun,
	}

	if in.DryRun {
		out["next"] = "maw lead goal accept（去掉 --dry-run 才推进状态）"
		return out, nil
	}

	if decision.NeedsRework {
		// 返工提案阶段不动状态：等新修订批准入队后才按 verifying→active 迁移。
		out["next"] = fmt.Sprintf("maw lead goal plan --goal %s --kind rework（带证据 %s）", goalID, strings.Join(proposal.Evidence, ","))
		return out, nil
	}

	if err := applyGoalDecision(pc, snap.Goal, decision, actor); err != nil {
		return nil, err
	}
	out["goal_status"] = decision.Status
	out["next"] = "maw lead goal show --goal " + goalID
	return out, nil
}

// applyGoalDecision 把结论落成状态迁移（带 revision；同状态幂等）。
func applyGoalDecision(pc *projCtx, current *goal.Goal, decision goal.Decision, actor string) error {
	expected := current.Revision
	switch decision.Status {
	case goal.StatusDone:
		// active → verifying → done：契约 §3 没有 active→done 的直连边。
		if current.Status == goal.StatusActive {
			g, changed, err := setGoalStatus(pc, current.GoalID, goal.StatusVerifying, "", actor, expected)
			if err != nil {
				return err
			}
			if changed {
				expected = g.Revision
			}
		}
		_, _, err := setGoalStatus(pc, current.GoalID, goal.StatusDone, "", actor, expected)
		return err
	case goal.StatusEscalated, goal.StatusPaused:
		reason := goal.ReasonInvariant
		if decision.Reason == goal.ReasonBudgetExhaust {
			reason = goal.ReasonBudgetExhaust
		}
		_, _, err := setGoalStatus(pc, current.GoalID, decision.Status, reason, actor, expected)
		return err
	case goal.StatusAbandoned:
		_, _, err := setGoalStatus(pc, current.GoalID, goal.StatusAbandoned, decision.Reason, actor, expected)
		return err
	default:
		// 需要返工或状态未变：不写库。
		return nil
	}
}

func setGoalStatus(pc *projCtx, goalID, to, reason, actor string, expected int) (*goal.Goal, bool, error) {
	var (
		g       *goal.Goal
		changed bool
	)
	if err := withGlobalWrite(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		g, changed, e = db.SetGoalStatusCAS(tx, pc.ID(), goalID, to, reason, actor, expected)
		return e
	}); err != nil {
		return nil, false, err
	}
	return g, changed, nil
}

// evaluateGoalCriteria 逐条判定目标验收条件。
func evaluateGoalCriteria(pc *projCtx, snap *db.GoalSnapshot, manual map[string]string, timeoutSec int) []goal.CriterionResult {
	if snap == nil || snap.Active == nil {
		return nil
	}
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	results := make([]goal.CriterionResult, 0, len(snap.Active.Acceptance))
	for _, a := range snap.Active.Acceptance {
		res := goal.CriterionResult{ID: a.ID, Kind: a.Kind}
		switch a.Kind {
		case goal.AcceptanceCommand:
			res.Verdict, res.Evidence, res.Detail = runCriterionCommand(pc, a, timeoutSec)
		case goal.AcceptanceManual:
			verdict := strings.ToLower(strings.TrimSpace(manual[a.ID]))
			switch verdict {
			case goal.VerdictPass, goal.VerdictFail:
				res.Verdict = verdict
				res.Evidence = []string{"manual:" + verdict}
				res.Detail = "人工验收"
			default:
				res.Verdict = goal.VerdictUnknown
				res.Detail = "缺少人工验收结论"
			}
		case goal.AcceptanceEvidence:
			res.Verdict, res.Evidence, res.Detail = deliveryEvidenceVerdict(snap)
		default:
			res.Verdict = goal.VerdictUnknown
			res.Detail = "未知验收形态"
		}
		results = append(results, res)
	}
	return results
}

// runCriterionCommand 跑一条目标级命令验收，证据里保留命令与输出摘要。
func runCriterionCommand(pc *projCtx, a goal.Acceptance, timeoutSec int) (string, []string, string) {
	results, err := gitops.RunGatesDetailed([]string{a.Check}, pc.Repo, timeoutSec)
	if err != nil && len(results) == 0 {
		return goal.VerdictUnknown, []string{"error:" + truncate(err.Error(), 200)}, "命令无法执行"
	}
	if len(results) == 0 {
		return goal.VerdictUnknown, nil, "命令没有结果"
	}
	r := results[0]
	evidence := []string{"command:" + a.Check}
	if r.Output != "" {
		evidence = append(evidence, "output:"+truncate(r.Output, 300))
	}
	if r.OK {
		return goal.VerdictPass, evidence, "命令通过"
	}
	if r.EnvFailure || gitops.IsEnvFailure(r.Output) {
		// 环境失败不是目标失败：判不了就 unknown，交人处置（契约 §4）。
		return goal.VerdictUnknown, append(evidence, "env_failure:true"), "验证环境不可用"
	}
	return goal.VerdictFail, append(evidence, "exit_code_nonzero"), "命令失败"
}

// deliveryEvidenceVerdict 由持久化交付证据判：所有映射任务都 done 才算通过。
//
// 有链接缺失／实例漂移 → unknown；有任务明确未完成 → fail。
func deliveryEvidenceVerdict(snap *db.GoalSnapshot) (string, []string, string) {
	if len(snap.TaskLinks) == 0 {
		return goal.VerdictUnknown, []string{"links:0"}, "还没有任何任务映射"
	}
	byInstance := map[string]*db.Task{}
	for _, t := range snap.Tasks {
		byInstance[t.InstanceID] = t
	}
	evidence := []string{"links:" + fmt.Sprint(len(snap.TaskLinks))}
	doneCnt := 0
	for _, l := range snap.TaskLinks {
		if l.State == goal.LinkDrifted {
			return goal.VerdictUnknown, append(evidence, "drifted:"+l.DisplayID), "映射已漂移"
		}
		t := byInstance[l.InstanceID]
		if t == nil {
			return goal.VerdictUnknown, append(evidence, "missing_instance:"+l.InstanceID), "映射指向的任务不存在"
		}
		if t.Status == string(model.TaskDone) {
			doneCnt++
		}
	}
	evidence = append(evidence, fmt.Sprintf("done:%d/%d", doneCnt, len(snap.TaskLinks)))
	if doneCnt == len(snap.TaskLinks) {
		return goal.VerdictPass, evidence, "全部映射任务已交付"
	}
	return goal.VerdictFail, evidence, "仍有任务未交付"
}

func goalReworkBudget(snap *db.GoalSnapshot) goal.ReworkBudget {
	b := goal.ReworkBudget{}
	if snap != nil && snap.Active != nil {
		b.MaxReworkRounds = snap.Active.Budget.MaxReworkRounds
		b.MaxPlanRevisions = snap.Active.Budget.MaxPlanRevisions
	}
	if snap != nil {
		b.UsedPlanRevisions = len(snap.Plans)
		for _, p := range snap.Plans {
			if p.Kind == goal.PlanKindRework {
				b.UsedReworkRounds++
			}
		}
	}
	return b
}

func budgetExhausted(snap *db.GoalSnapshot) bool {
	if snap == nil || snap.Active == nil {
		return false
	}
	maxRevisions := snap.Active.Budget.MaxPlanRevisions
	if maxRevisions > 0 && len(snap.Plans) >= maxRevisions {
		// 修订用尽：预算维度耗尽，暂停等人工加预算（契约 §10）。
		return true
	}
	return false
}

// LeadGoalPause / LeadGoalResume / LeadGoalAbandon 是三个显式生命周期动作。
func LeadGoalPause(cwd, goalID, reason string) (Result, error) {
	return leadGoalStatusChange(cwd, goalID, goal.StatusPaused, reason)
}

func LeadGoalResume(cwd, goalID string) (Result, error) {
	actor, err := leadActor(cwd)
	if err != nil {
		return nil, err
	}
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	var current *goal.Goal
	if err := withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		current, e = db.GetGoal(tx, pc.ID(), strings.TrimSpace(goalID))
		return e
	}); err != nil {
		return nil, err
	}
	if current == nil {
		return nil, fmt.Errorf("%w: %s", goal.ErrGoalNotFound, goalID)
	}
	// escalated 不能直接回 active：先降为 paused 再由人显式 resume（契约 §3）。
	if current.Status == goal.StatusEscalated {
		if _, _, err := setGoalStatus(pc, current.GoalID, goal.StatusPaused, goal.ReasonInvariant, actor, current.Revision); err != nil {
			return nil, err
		}
		current.Status = goal.StatusPaused
		current.Revision++
	}
	g, changed, err := setGoalStatus(pc, current.GoalID, goal.StatusActive, "", actor, current.Revision)
	if err != nil {
		return nil, err
	}
	return Result{"ok": true, "goal_id": goalID, "status": g.Status, "changed": changed,
		"next": "maw lead goal accept --goal " + goalID}, nil
}

func LeadGoalAbandon(cwd, goalID, reason string) (Result, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("放弃目标需要理由（--reason）")
	}
	return leadGoalStatusChange(cwd, goalID, goal.StatusAbandoned, reason)
}

func leadGoalStatusChange(cwd, goalID, to, reason string) (Result, error) {
	actor, err := leadActor(cwd)
	if err != nil {
		return nil, err
	}
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	goalID = strings.TrimSpace(goalID)
	if goalID == "" {
		return nil, fmt.Errorf("需要 --goal")
	}
	var current *goal.Goal
	if err := withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		current, e = db.GetGoal(tx, pc.ID(), goalID)
		return e
	}); err != nil {
		return nil, err
	}
	if current == nil {
		return nil, fmt.Errorf("%w: %s", goal.ErrGoalNotFound, goalID)
	}
	if to == goal.StatusPaused && strings.TrimSpace(reason) == "" {
		reason = goal.ReasonHumanPause
	}
	g, changed, err := setGoalStatus(pc, goalID, to, reason, actor, current.Revision)
	if err != nil {
		return nil, err
	}
	return Result{"ok": true, "goal_id": goalID, "status": g.Status, "changed": changed}, nil
}

// LeadGoalRecover 是重启后的只读恢复：按持久化证据重建每个进行中目标的状态视图。
//
// 不推进状态、不建任务、不写库；重复调用结果相同。真正需要推进的（例如
// 证据仍 unknown）留给 accept 或人工处置。
func LeadGoalRecover(cwd string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	return recoverGoalsForProject(pc)
}

// recoverGoalsForProject 是恢复的工程级实现：中枢重启时也走这一条。
func recoverGoalsForProject(pc *projCtx) (Result, error) {
	var goals []*goal.Goal
	if err := withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		goals, e = db.ListGoals(tx, pc.ID())
		return e
	}); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(goals))
	for _, g := range goals {
		if goal.IsTerminal(g.Status) {
			continue
		}
		var snap *db.GoalSnapshot
		if err := withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
			var e error
			snap, e = db.LoadGoalSnapshot(tx, pc.ID(), g.GoalID)
			return e
		}); err != nil {
			return nil, err
		}
		item := map[string]any{"goal_id": g.GoalID, "status": g.Status, "revision": g.Revision}
		if snap != nil {
			item["plans"] = len(snap.Plans)
			item["links"] = len(snap.TaskLinks)
			byInstance := map[string]*db.Task{}
			for _, t := range snap.Tasks {
				byInstance[t.InstanceID] = t
			}
			drifted, done := 0, 0
			for _, l := range snap.TaskLinks {
				if l.State == goal.LinkDrifted {
					drifted++
					continue
				}
				if t := byInstance[l.InstanceID]; t != nil && t.Status == string(model.TaskDone) {
					done++
				}
			}
			item["drifted"] = drifted
			item["done"] = done
		}
		switch g.Status {
		case goal.StatusEscalated:
			item["note"] = "等待人工处置：escalated 不能自动回 active"
		case goal.StatusPaused:
			item["note"] = "已暂停：resume 后才继续派发"
		}
		out = append(out, item)
	}
	return Result{"ok": true, "recovered": out, "count": len(out),
		"next": "maw lead goal accept --goal <id>（按需推进）"}, nil
}

// 编译期确认：验收命令经 gitops 执行，不在这里另造 shell。
var _ = exec.CommandContext
var _ = context.Background
var _ = time.Second
