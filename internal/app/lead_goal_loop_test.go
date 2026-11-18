package app

import (
	"database/sql"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/goal"
)

func seedLeadGoal(t *testing.T, f *fixture, acceptance []goal.Acceptance, activate bool) string {
	t.Helper()
	goalID := "gl_pi001ax9"
	if err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		if _, _, err := db.CreateGoal(tx, &goal.Goal{
			ProjectID: f.pc.ID(), GoalID: goalID,
			Status: goal.StatusDraft, ActiveVersion: 1,
		}, &goal.Version{
			ProjectID: f.pc.ID(), GoalID: goalID, GoalVersion: 1,
			Title: "目标", Statement: "现状：无。目标：有。",
			Acceptance: acceptance, Budget: goal.DefaultBudget(),
		}, "lead-1"); err != nil {
			return err
		}
		if !activate {
			return nil
		}
		_, _, err := db.SetGoalStatusCAS(tx, f.pc.ID(), goalID, goal.StatusActive, "", "lead-1", 0)
		return err
	}); err != nil {
		t.Fatalf("seed goal: %v", err)
	}
	return goalID
}

func commandAcceptance(check string) []goal.Acceptance {
	return []goal.Acceptance{{
		ID: "AC-1", Kind: goal.AcceptanceCommand, Check: check,
		Statement: "命令通过", EvidenceRequired: true,
	}}
}

func TestLeadGoalAcceptPassCompletesGoal(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")
	goalID := seedLeadGoal(t, f, append(commandAcceptance("true"), goal.Acceptance{
		ID: "AC-2", Kind: goal.AcceptanceManual, Check: "人工核对",
		Statement: "人确认", EvidenceRequired: false,
	}), true)

	res, err := LeadGoalAccept(f.repo, LeadGoalAcceptInput{
		GoalID: goalID, Manual: map[string]string{"AC-2": goal.VerdictPass},
	})
	if err != nil {
		t.Fatalf("验收失败: %v", err)
	}
	if res["overall"] != goal.VerdictPass {
		t.Fatalf("整体结论应为 pass: %+v", res)
	}
	decision, _ := res["decision"].(goal.Decision)
	if decision.Status != goal.StatusDone || decision.ExitCode != goal.ExitGoalDone {
		t.Fatalf("决定应 done/0: %+v", decision)
	}
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		g, err := db.GetGoal(tx, f.pc.ID(), goalID)
		if err != nil {
			return err
		}
		if g.Status != goal.StatusDone {
			t.Fatalf("目标未完成: %+v", g)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLeadGoalAcceptFailProposesReworkWithoutStatusChange(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")
	goalID := seedLeadGoal(t, f, commandAcceptance("false"), true)

	res, err := LeadGoalAccept(f.repo, LeadGoalAcceptInput{GoalID: goalID})
	if err != nil {
		t.Fatal(err)
	}
	if res["overall"] != goal.VerdictFail {
		t.Fatalf("结论应为 fail: %+v", res)
	}
	decision, _ := res["decision"].(goal.Decision)
	if !decision.NeedsRework {
		t.Fatalf("应提出返工: %+v", decision)
	}
	proposal, _ := res["rework"].(goal.ReworkProposal)
	if len(proposal.Targets) != 1 || len(proposal.Evidence) == 0 || proposal.Key == "" {
		t.Fatalf("返工提案缺证据: %+v", proposal)
	}
	// 提案阶段不改状态、不建任务。
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		g, err := db.GetGoal(tx, f.pc.ID(), goalID)
		if err != nil {
			return err
		}
		if g.Status != goal.StatusActive {
			t.Fatalf("返工提案阶段不得推进状态: %+v", g)
		}
		plans, err := db.ListPlanRevisions(tx, f.pc.ID(), goalID)
		if err != nil {
			return err
		}
		if len(plans) != 0 {
			t.Fatalf("返工提案阶段不得创建修订: %+v", plans)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLeadGoalAcceptUnknownEscalates(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")
	goalID := seedLeadGoal(t, f, []goal.Acceptance{{
		ID: "AC-1", Kind: goal.AcceptanceManual, Check: "人工核对",
		Statement: "人确认", EvidenceRequired: true,
	}}, true)

	res, err := LeadGoalAccept(f.repo, LeadGoalAcceptInput{GoalID: goalID})
	if err != nil {
		t.Fatal(err)
	}
	if res["overall"] != goal.VerdictUnknown {
		t.Fatalf("缺人工结论应为 unknown: %+v", res)
	}
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		g, err := db.GetGoal(tx, f.pc.ID(), goalID)
		if err != nil {
			return err
		}
		if g.Status != goal.StatusEscalated {
			t.Fatalf("unknown 应升级人工: %+v", g)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLeadGoalAcceptBudgetExhaustedPauses(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")
	goalID := seedLeadGoal(t, f, commandAcceptance("false"), true)

	// 预算：只允许 1 个修订；先造 1 个修订，验收时即判预算耗尽。
	if err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		v, err := db.GetGoalVersion(tx, f.pc.ID(), goalID, 1)
		if err != nil {
			return err
		}
		v.Budget.MaxPlanRevisions = 1
		_, err = db.AppendGoalVersion(tx, &goal.Version{
			ProjectID: f.pc.ID(), GoalID: goalID, GoalVersion: 2,
			Title: v.Title, Statement: v.Statement, Acceptance: v.Acceptance, Budget: v.Budget,
		}, 1, "lead-1")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		rev := testPlanForGoal(f.pc.ID(), goalID, 2, 1)
		_, err := db.SavePlanRevision(tx, rev, "lead-1")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	res, err := LeadGoalAccept(f.repo, LeadGoalAcceptInput{GoalID: goalID, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	// 预算用尽且未通过 → 暂停（不是继续返工、也不是 done）。
	decision, _ := res["decision"].(goal.Decision)
	if decision.ExitCode != goal.ExitGoalBudgetExhausted || decision.Status != goal.StatusPaused {
		t.Fatalf("预算耗尽应暂停: %+v", decision)
	}
}

func TestLeadGoalReworkCreatesPlanWithEvidenceThenReplays(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")
	goalID := seedLeadGoal(t, f, commandAcceptance("false"), true)
	usePlanner(t, &fakeLeadPlanner{cards: []goal.PlanCard{{
		CardID: "r01", ID: "GL-950", Title: "修复失败断言", Goal: "现状：失败。目标：通过。",
		Priority: 10, MergeOrder: 10, WriteScope: []string{"internal/goal/fix.go"},
		ReadScope: []string{"README.md"}, Acceptance: []string{"修复后通过"},
		Verify: []string{"go test ./internal/goal -count=1"}, NotDoing: []string{"不改主干"},
	}}})

	res, err := LeadGoalRework(f.repo, LeadGoalReworkInput{GoalID: goalID})
	if err != nil {
		t.Fatalf("返工规划失败: %v", err)
	}
	if res["status"] != goal.PlanProposed {
		t.Fatalf("返工修订应 proposed: %+v", res)
	}
	proposal, _ := res["rework"].(goal.ReworkProposal)

	var rework *goal.PlanRevision
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		plans, err := db.ListPlanRevisions(tx, f.pc.ID(), goalID)
		if err != nil {
			return err
		}
		for _, p := range plans {
			if p.Kind == goal.PlanKindRework {
				rework = p
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if rework == nil || len(rework.Cards) != 1 {
		t.Fatalf("返工修订没落库: %+v", rework)
	}
	if len(rework.Cards[0].Origin.Evidence) == 0 {
		t.Fatalf("返工卡必须带证据（G216）: %+v", rework.Cards[0].Origin)
	}

	// 同一失败证据重复请求：返回已有修订，不再新增。
	again, err := LeadGoalRework(f.repo, LeadGoalReworkInput{GoalID: goalID})
	if err != nil {
		t.Fatalf("重放失败: %v", err)
	}
	if again["replayed"] != true {
		t.Fatalf("重复返工应重放: %+v", again)
	}
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		plans, err := db.ListPlanRevisions(tx, f.pc.ID(), goalID)
		if err != nil {
			return err
		}
		reworkCount := 0
		for _, p := range plans {
			if p.Kind == goal.PlanKindRework {
				reworkCount++
			}
		}
		if reworkCount != 1 {
			t.Fatalf("重复事件不得造重复修订: %d", reworkCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = proposal
}

func TestLeadGoalRecoverIsReadOnlyAndIdempotent(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")
	goalID := seedLeadGoal(t, f, commandAcceptance("true"), true)

	first, err := LeadGoalRecover(f.repo)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	second, err := LeadGoalRecover(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	if first["count"] != second["count"] {
		t.Fatalf("恢复应幂等: %v vs %v", first["count"], second["count"])
	}
	recovered, _ := first["recovered"].([]map[string]any)
	if len(recovered) == 0 {
		t.Fatalf("应恢复出进行中目标: %+v", first)
	}
	// 只读：状态与修订数不变。
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		g, err := db.GetGoal(tx, f.pc.ID(), goalID)
		if err != nil {
			return err
		}
		if g.Status != goal.StatusActive || g.Revision != 1 {
			t.Fatalf("恢复不得改状态: %+v", g)
		}
		plans, err := db.ListPlanRevisions(tx, f.pc.ID(), goalID)
		if err != nil {
			return err
		}
		if len(plans) != 0 {
			t.Fatalf("恢复不得造修订: %d", len(plans))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// testPlanForGoal 是预算用例需要的「已存在修订」。
func testPlanForGoal(projectID, goalID string, goalVersion, rev int) *goal.PlanRevision {
	plan := &goal.PlanRevision{
		ProjectID: projectID, GoalID: goalID, GoalVersion: goalVersion, PlanRev: rev,
		Kind: goal.PlanKindInitial, Status: goal.PlanProposed,
		Cards: []goal.PlanCard{{
			CardID: "c01", ID: "GL-960", Title: "占位修订", Goal: "现状：无。目标：有。",
			Priority: 10, MergeOrder: 10, WriteScope: []string{"docs/seeded.md"},
			ReadScope: []string{"README.md"}, Acceptance: []string{"可核对"},
			Verify: []string{"manual: 人工核对"}, NotDoing: []string{"无"},
			Origin: goal.Origin{GoalID: goalID, PlanRev: rev, CardID: "c01"},
		}},
	}
	_ = plan.EnsureDigest()
	return plan
}
