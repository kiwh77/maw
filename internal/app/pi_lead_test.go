package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/goal"
	"github.com/kiwh77/maw/internal/home"
)

// setTestLeads 在工程配置里登记 lead 名单（等价于运维手改 [identity] leads）。
func setTestLeads(t *testing.T, repo string, leads ...string) {
	t.Helper()
	local, err := home.MawHome(repo)
	if err != nil {
		t.Fatalf("maw home: %v", err)
	}
	path := home.ConfigPath(local)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	quoted := `"` + strings.Join(leads, `", "`) + `"`
	lines := strings.Split(string(raw), "\n")
	replaced := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "leads") {
			lines[i] = "leads = [" + quoted + "]"
			replaced = true
		}
	}
	if !replaced {
		t.Fatalf("config 里没有 leads 行：\n%s", raw)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// fakeLeadPlanner 是测试注入的规划器：不跑模型、不联网。
type fakeLeadPlanner struct {
	cards []goal.PlanCard
	err   error
	calls int
	last  goal.PlanRequest
}

func (f *fakeLeadPlanner) Plan(_ context.Context, req goal.PlanRequest) ([]goal.PlanCard, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return nil, f.err
	}
	return f.cards, nil
}

func usePlanner(t *testing.T, p LeadPlanner) {
	t.Helper()
	prev := leadPlannerFactory
	leadPlannerFactory = func(*projCtx) (LeadPlanner, error) { return p, nil }
	t.Cleanup(func() { leadPlannerFactory = prev })
}

func leadPlanCards() []goal.PlanCard {
	return []goal.PlanCard{
		{
			CardID: "c01", ID: "GL-901", Title: "文档卡", Goal: "现状：缺文档。目标：补齐。",
			Priority: 10, MergeOrder: 10, WriteScope: []string{"docs/plan-lead.md"},
			ReadScope: []string{"README.md"}, Acceptance: []string{"段落齐全"},
			Verify: []string{"manual: 人工核对"}, NotDoing: []string{"不改代码"},
		},
		{
			CardID: "c02", ID: "GL-902", Title: "实现卡", Goal: "现状：缺实现。目标：补齐。",
			Priority: 20, MergeOrder: 20, Deps: []string{"GL-901"}, WriteScope: []string{"internal/goal/**"},
			ReadScope: []string{"README.md"}, Acceptance: []string{"测试通过"},
			Verify: []string{"go test ./internal/goal -count=1"}, NotDoing: []string{"不改主干"},
		},
	}
}

func leadGoalInput() LeadGoalPlanInput {
	return LeadGoalPlanInput{
		Title:      "让 Lead 产出可审查计划",
		Statement:  "现状：Lead 只出模板卡。目标：产出结构化计划并批准入队。",
		Acceptance: []string{"command:go test ./internal/goal -count=1|领域逻辑通过"},
		Evidence:   []string{"docs/contracts/vnext/lead/pi-goal-workflow-v1.md#6"},
	}
}

func TestLeadGoalPlanPreviewHasNoSideEffects(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")
	planner := &fakeLeadPlanner{cards: leadPlanCards()}
	usePlanner(t, planner)

	in := leadGoalInput()
	in.DryRun = true
	res, err := LeadGoalPlan(f.repo, in)
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if res["ok"] != true || res["dry_run"] != true || res["plan_digest"] == "" {
		t.Fatalf("预览结果不完整: %+v", res)
	}
	if planner.calls != 1 {
		t.Fatalf("规划应调用 1 次，得到 %d", planner.calls)
	}

	// 预览不落库：既没有目标，也没有修订。
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		goals, err := db.ListGoals(tx, f.pc.ID())
		if err != nil {
			return err
		}
		if len(goals) != 0 {
			t.Fatalf("预览不得创建目标: %+v", goals)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLeadGoalPlanPersistsProposedRevision(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")
	usePlanner(t, &fakeLeadPlanner{cards: leadPlanCards()})

	res, err := LeadGoalPlan(f.repo, leadGoalInput())
	if err != nil {
		t.Fatalf("规划失败: %v", err)
	}
	goalID, _ := res["goal_id"].(string)
	if goalID == "" || !strings.HasPrefix(goalID, "gl_") {
		t.Fatalf("goal_id 形态不对: %v", res["goal_id"])
	}
	if res["status"] != goal.PlanProposed {
		t.Fatalf("修订应停在 proposed: %v", res["status"])
	}
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		g, err := db.GetGoal(tx, f.pc.ID(), goalID)
		if err != nil {
			return err
		}
		if g == nil || g.Status != goal.StatusDraft || g.ActiveVersion != 1 {
			t.Fatalf("目标状态不对: %+v", g)
		}
		plans, err := db.ListPlanRevisions(tx, f.pc.ID(), goalID)
		if err != nil {
			return err
		}
		if len(plans) != 1 || plans[0].Status != goal.PlanProposed || plans[0].PlanDigest == "" {
			t.Fatalf("修订没落库: %+v", plans)
		}
		if len(plans[0].Cards) != 2 || len(plans[0].Cards[1].Deps) != 1 {
			t.Fatalf("卡字段不完整: %+v", plans[0].Cards)
		}
		if plans[0].Cards[0].Origin.GoalID != goalID {
			t.Fatalf("origin 未绑定目标: %+v", plans[0].Cards[0].Origin)
		}
		// 规划阶段不得建任务。
		tasks, err := db.ListTasks(tx, f.pc.ID())
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if strings.HasPrefix(task.ID, "GL-9") {
				t.Fatalf("规划不得创建任务: %+v", task)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLeadGoalPlanFailureLeavesNoTrace(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")

	// 规划器报错：不建目标、不存修订。
	usePlanner(t, &fakeLeadPlanner{err: errors.New("model timeout")})
	if _, err := LeadGoalPlan(f.repo, leadGoalInput()); err == nil {
		t.Fatal("规划失败应返回错误")
	}

	// 计划本身不合法（重复 card_id）：同样不留痕。
	dup := leadPlanCards()
	dup[1].CardID = dup[0].CardID
	usePlanner(t, &fakeLeadPlanner{cards: dup})
	if _, err := LeadGoalPlan(f.repo, leadGoalInput()); err == nil {
		t.Fatal("非法计划应被拒绝")
	}

	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		goals, err := db.ListGoals(tx, f.pc.ID())
		if err != nil {
			return err
		}
		if len(goals) != 0 {
			t.Fatalf("失败路径不得创建目标: %+v", goals)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLeadGoalApproveRequiresExplicitPolicyForAutoEnqueue(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")
	usePlanner(t, &fakeLeadPlanner{cards: leadPlanCards()})

	res, err := LeadGoalPlan(f.repo, leadGoalInput())
	if err != nil {
		t.Fatal(err)
	}
	goalID := res["goal_id"].(string)

	// 默认策略 + 自动入队标志：拒绝，且不写任何东西。
	if _, err := LeadGoalApprove(f.repo, LeadGoalApproveInput{
		GoalID: goalID, PlanRev: 1, Policy: goal.PolicyManual, AutoEnqueue: true,
	}); err == nil {
		t.Fatal("manual 策略下 auto-enqueue 必须被拒绝")
	}

	// 正常人工批准：停在 approved，等显式 enqueue。
	out, err := LeadGoalApprove(f.repo, LeadGoalApproveInput{
		GoalID: goalID, PlanRev: 1, Policy: goal.PolicyManual,
	})
	if err != nil {
		t.Fatalf("批准失败: %v", err)
	}
	if out["status"] != goal.PlanApproved || out["approved"] != true {
		t.Fatalf("批准结果不对: %+v", out)
	}
	if strings.Contains(strings.TrimSpace(out["next"].(string)), "enqueue") == false {
		t.Fatalf("下一步应提示入队: %v", out["next"])
	}

	// 重复批准幂等。
	out, err = LeadGoalApprove(f.repo, LeadGoalApproveInput{GoalID: goalID, PlanRev: 1, Policy: goal.PolicyManual})
	if err != nil || out["approved"] != false {
		t.Fatalf("重复批准应幂等: %v %+v", err, out)
	}
}

func TestLeadGoalEnqueueCreatesTasksOnceAndShowReadsBack(t *testing.T) {
	f := newFixture(t)
	setTestLeads(t, f.repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")
	setPiBlueprint(t, f)
	usePlanner(t, &fakeLeadPlanner{cards: leadPlanCards()})

	res, err := LeadGoalPlan(f.repo, leadGoalInput())
	if err != nil {
		t.Fatal(err)
	}
	goalID := res["goal_id"].(string)
	if _, err := LeadGoalApprove(f.repo, LeadGoalApproveInput{GoalID: goalID, PlanRev: 1, Policy: goal.PolicyManual}); err != nil {
		t.Fatal(err)
	}

	enq, err := LeadGoalEnqueue(f.repo, LeadGoalEnqueueInput{GoalID: goalID, PlanRev: 1})
	if err != nil {
		t.Fatalf("入队失败: %v", err)
	}
	if enq["replayed"] == true || num(t, enq, "appended") != 2 {
		t.Fatalf("首次入队应追加 2 张: %+v", enq)
	}

	// 重放：不再追加。
	again, err := LeadGoalEnqueue(f.repo, LeadGoalEnqueueInput{GoalID: goalID, PlanRev: 1})
	if err != nil {
		t.Fatalf("重放失败: %v", err)
	}
	if again["replayed"] != true {
		t.Fatalf("重放应标记 replayed: %+v", again)
	}

	show, err := LeadGoalShow(f.repo, goalID)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if show["ok"] != true {
		t.Fatalf("show 失败: %+v", show)
	}
	links, _ := show["links"].([]Result)
	if len(links) != 2 {
		t.Fatalf("映射应为 2 条: %+v", show["links"])
	}
	plans, _ := show["plans"].([]Result)
	if len(plans) != 1 || plans[0]["status"] != goal.PlanApplied {
		t.Fatalf("修订应已 applied: %+v", show["plans"])
	}
}

func TestLeadGoalPlanRequiresLead(t *testing.T) {
	f := newFixture(t)
	t.Setenv("MAW_AGENT", "not-a-lead")
	usePlanner(t, &fakeLeadPlanner{cards: leadPlanCards()})
	if _, err := LeadGoalPlan(f.repo, leadGoalInput()); err == nil {
		t.Fatal("非 lead 不得规划")
	}
}

func TestParsePiResultEvents(t *testing.T) {
	okStream := `{"type":"version_report","payload":{}}
{"type":"result","payload":{"ok":true,"outcome":"completed","reason":"result","text":"[{\"card_id\":\"c1\"}]"}}
{"type":"session_ended","payload":{}}`
	text, reason, ok, err := parsePiResultEvents(okStream)
	if err != nil || !ok || text == "" || reason != "result" {
		t.Fatalf("成功流解析不对: %v %q %v %q", err, text, ok, reason)
	}

	failStream := `{"type":"result","payload":{"ok":false,"outcome":"failed","reason":"auth","text":"401"}}`
	text, reason, ok, err = parsePiResultEvents(failStream)
	if err != nil || ok || reason != "auth" || text != "401" {
		t.Fatalf("失败流解析不对: %v %q %v %q", err, text, ok, reason)
	}

	if _, _, _, err := parsePiResultEvents("not json"); err == nil {
		t.Fatal("无 result 事件必须报错")
	}

	prompt := leadPlanPrompt(goal.PlanRequest{
		Title: "t", Statement: "s", PlanRev: 1, GoalVersion: 1, MaxCards: 3,
		Acceptance: []goal.Acceptance{{ID: "AC-1", Kind: goal.AcceptanceCommand, Check: "true", Statement: "通过"}},
	})
	if !strings.Contains(prompt, "只做规划") || !strings.Contains(prompt, "3") {
		t.Fatalf("规划提示缺关键约束: %s", prompt)
	}
}
