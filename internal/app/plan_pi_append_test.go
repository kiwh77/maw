package app

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/blueprint"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/goal"
	"github.com/kiwh77/maw/internal/home"
)

func setPiBlueprint(t *testing.T, f *fixture) string {
	t.Helper()
	bp := home.BlueprintPath(f.pc.LocalHome)
	content := `[meta]
name = "pi"
version = 1
main_branch = "main"

[[task]]
id = "KEEP-1"
title = "历史题：必须原样保留"
goal = "现状：已有。目标：不被这次追加影响。"
priority = 10
merge_order = 10
deps = []
write_scope = ["README.md"]
read_scope = ["README.md"]
acceptance = ["仍可查询"]
verify = ["manual: 人工核对"]
not_doing = ["不新增文件"]
`
	if err := os.MkdirAll(filepath.Dir(bp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanApply(bp, f.repo, true, false, false); err != nil {
		t.Fatalf("初始 apply: %v", err)
	}
	return bp
}

func blueprintTaskIDs(t *testing.T, path string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := blueprint.Parse(b)
	if err != nil {
		t.Fatalf("解析蓝图: %v", err)
	}
	ids := map[string]bool{}
	for _, task := range f.Tasks {
		ids[task.ID] = true
	}
	return ids
}

func keepTaskFingerprint(t *testing.T, f *fixture) string {
	t.Helper()
	var out string
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		task, err := db.GetTask(tx, f.pc.ID(), "KEEP-1")
		if err != nil {
			return err
		}
		if task == nil {
			return nil
		}
		inst, err := db.GetInstance(tx, task.InstanceID)
		if err != nil {
			return err
		}
		version := 0
		if inst != nil && inst.ActiveVersion != nil {
			version = int(*inst.ActiveVersion)
		}
		out = fmt.Sprintf("%s|%s|%s|%d", task.Status, task.InstanceID, task.ID, version)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func newPiPlan(t *testing.T, f *fixture, rev, goalVersion int, cards []goal.PlanCard) goal.PlanRevision {
	t.Helper()
	plan := goal.PlanRevision{
		ProjectID: f.pc.ID(), GoalID: "gl_pi001ax9", GoalVersion: goalVersion,
		PlanRev: rev, Kind: goal.PlanKindInitial, Status: goal.PlanProposed,
		Baseline: goal.Baseline{BlueprintSHA256: strings.Repeat("a", 64), TasksRevision: 1},
		Cards:    cards,
	}
	if err := plan.EnsureDigest(); err != nil {
		t.Fatal(err)
	}
	var approved goal.PlanRevision
	if err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		if _, err := db.SavePlanRevision(tx, &plan, "lead"); err != nil {
			return err
		}
		r, _, err := db.ApprovePlanRevision(tx, f.pc.ID(), plan.GoalID, rev, "alice", goal.PolicyManual, plan.PlanDigest, "lead")
		if err != nil {
			return err
		}
		approved = *r
		return nil
	}); err != nil {
		t.Fatalf("保存／批准修订: %v", err)
	}
	return approved
}

func piCard(cardID, id, title string, deps, scope []string) goal.PlanCard {
	return goal.PlanCard{
		CardID: cardID, ID: id, Title: title,
		Goal: "现状：缺。目标：补齐。", Priority: 20, MergeOrder: 20,
		Deps: deps, WriteScope: scope, ReadScope: []string{"README.md"},
		Acceptance: []string{"可核对"}, Verify: []string{"manual: 人工核对"},
		NotDoing: []string{"不改主干"},
		Origin:   goal.Origin{GoalID: "gl_pi001ax9", SourceRefs: []string{"docs/plan.md#1"}},
	}
}

// TestPiAppendPlanOnceAndReplay 覆盖验收 2 的主路径：
// 同一批准操作只入队一次，重放不再追加；既有任务与实例版本不变。
func TestPiAppendPlanOnceAndReplay(t *testing.T) {
	f := newFixture(t)
	bp := setPiBlueprint(t, f)
	keepBefore := keepTaskFingerprint(t, f)

	if err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, _, err := db.CreateGoal(tx, &goal.Goal{
			ProjectID: f.pc.ID(), GoalID: "gl_pi001ax9",
			Status: goal.StatusDraft, ActiveVersion: 1,
		}, &goal.Version{
			ProjectID: f.pc.ID(), GoalID: "gl_pi001ax9", GoalVersion: 1,
			Title: "目标", Statement: "现状：无。目标：有。",
			Acceptance: []goal.Acceptance{{
				ID: "AC-1", Kind: goal.AcceptanceCommand,
				Check: "go test ./internal/goal -count=1", Statement: "通过", EvidenceRequired: true,
			}},
			Budget: goal.DefaultBudget(),
		}, "lead")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	plan := newPiPlan(t, f, 1, 1, []goal.PlanCard{
		piCard("c01", "GL-001", "文档卡", nil, []string{"docs/new.md"}),
		piCard("c02", "GL-002", "代码卡", []string{"KEEP-1"}, []string{"internal/goal/**"}),
	})

	beforeBytes, err := os.ReadFile(bp)
	if err != nil {
		t.Fatal(err)
	}

	// 预览：不写文件、不建任务。
	res, err := PiAppendPlan(PiAppendInput{Cwd: f.repo, Plan: plan, DryRun: true})
	if err != nil {
		t.Fatalf("预览失败：%v", err)
	}
	if num(t, res, "append") != 2 {
		t.Fatalf("预览应报告 2 张待追加，得到 %v", res["append"])
	}
	afterDry, _ := os.ReadFile(bp)
	if string(afterDry) != string(beforeBytes) {
		t.Fatal("dry-run 不得改动蓝图")
	}
	if ids := blueprintTaskIDs(t, bp); ids["GL-001"] || ids["GL-002"] {
		t.Fatal("dry-run 不得创建任务")
	}

	// 真入队：追加 2 张、建题、写映射与台账。
	res, err = PiAppendPlan(PiAppendInput{Cwd: f.repo, Plan: plan})
	if err != nil {
		t.Fatalf("入队失败：%v", err)
	}
	if num(t, res, "appended") != 2 || res["replayed"] == true {
		t.Fatalf("首次入队应追加 2 张：%v", res)
	}
	ids := blueprintTaskIDs(t, bp)
	for _, id := range []string{"KEEP-1", "GL-001", "GL-002"} {
		if !ids[id] {
			t.Fatalf("蓝图缺少 %s：%v", id, ids)
		}
	}
	if len(ids) != 3 {
		t.Fatalf("蓝图任务数应 +2，得到 %d", len(ids))
	}

	// 台账与映射：两条 applied，且指向具体实例。
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		links, err := db.ListTaskLinks(tx, f.pc.ID(), "gl_pi001ax9")
		if err != nil {
			return err
		}
		if len(links) != 2 {
			t.Fatalf("应有 2 条映射，得到 %d", len(links))
		}
		for _, l := range links {
			if l.State != goal.LinkApplied || l.InstanceID == "" || l.VersionNo < 1 {
				t.Fatalf("映射不完整：%+v", l)
			}
			rec, err := db.GetEnqueueRecord(tx, f.pc.ID(), l.EnqueueKey)
			if err != nil {
				return err
			}
			if rec == nil || rec.State != goal.EnqueueApplied {
				t.Fatalf("台账未置 applied：%+v", rec)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 重放：不再追加、不重复建题。
	res, err = PiAppendPlan(PiAppendInput{Cwd: f.repo, Plan: plan})
	if err != nil {
		t.Fatalf("重放失败：%v", err)
	}
	if res["replayed"] != true || num(t, res, "appended") != 0 {
		t.Fatalf("重放应返回原映射：%v", res)
	}
	if ids := blueprintTaskIDs(t, bp); len(ids) != 3 {
		t.Fatalf("重放不得新增任务，得到 %d", len(ids))
	}

	if keepAfter := keepTaskFingerprint(t, f); keepAfter != keepBefore {
		t.Fatalf("既有任务被改动：%s → %s", keepBefore, keepAfter)
	}
}

// TestPiAppendPlanRecoversAfterAppendFailure 覆盖验收 2 的可恢复分支：
// 蓝图已追加、apply 没跑成（台账停在 appended）时，重跑只做 apply，不重复追加。
func TestPiAppendPlanRecoversAfterAppendFailure(t *testing.T) {
	f := newFixture(t)
	bp := setPiBlueprint(t, f)
	if err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, _, err := db.CreateGoal(tx, &goal.Goal{
			ProjectID: f.pc.ID(), GoalID: "gl_pi001ax9",
			Status: goal.StatusDraft, ActiveVersion: 1,
		}, &goal.Version{
			ProjectID: f.pc.ID(), GoalID: "gl_pi001ax9", GoalVersion: 1,
			Title: "目标", Statement: "现状：无。目标：有。",
			Acceptance: []goal.Acceptance{{
				ID: "AC-1", Kind: goal.AcceptanceCommand,
				Check: "true", Statement: "通过", EvidenceRequired: true,
			}},
			Budget: goal.DefaultBudget(),
		}, "lead")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	plan := newPiPlan(t, f, 1, 1, []goal.PlanCard{
		piCard("c01", "GL-001", "文档卡", nil, []string{"docs/new.md"}),
	})

	// 模拟「追加成功、apply 失败」的现场：只写文件与 appended 台账。
	fragment, err := renderPiCards(plan, plan.Cards)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AppendTasks(bp, fragment, false); err != nil {
		t.Fatalf("模拟追加：%v", err)
	}
	key := goal.EnqueueKey(f.pc.ID(), plan.GoalID, plan.PlanRev, plan.PlanDigest, "c01")
	if err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, _, err := db.RecordEnqueue(tx, &goal.EnqueueRecord{
			ProjectID: f.pc.ID(), EnqueueKey: key, GoalID: plan.GoalID,
			PlanRev: plan.PlanRev, CardID: "c01", PlanDigest: plan.PlanDigest,
			State: goal.EnqueueAppended,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	countBefore := len(blueprintTaskIDs(t, bp))

	res, err := PiAppendPlan(PiAppendInput{Cwd: f.repo, Plan: plan})
	if err != nil {
		t.Fatalf("恢复入队失败：%v", err)
	}
	if num(t, res, "appended") != 0 {
		t.Fatalf("恢复时不得重复追加：%v", res)
	}
	if ids := blueprintTaskIDs(t, bp); len(ids) != countBefore {
		t.Fatalf("恢复后蓝图任务数不变，得到 %d（原 %d）", len(ids), countBefore)
	}
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		links, err := db.ListTaskLinks(tx, f.pc.ID(), plan.GoalID)
		if err != nil {
			return err
		}
		if len(links) != 1 || links[0].DisplayID != "GL-001" {
			t.Fatalf("恢复后应补上映射：%+v", links)
		}
		rec, err := db.GetEnqueueRecord(tx, f.pc.ID(), key)
		if err != nil {
			return err
		}
		if rec == nil || rec.State != goal.EnqueueApplied {
			t.Fatalf("恢复后台账应 applied：%+v", rec)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestPiAppendPlanRejectsUnvalidatedInput 覆盖验收 1 的拒绝路径：
// 未批准、编号撞车、依赖不存在、占位验收、无序写盘重叠都不得落盘。
func TestPiAppendPlanRejectsUnvalidatedInput(t *testing.T) {
	f := newFixture(t)
	bp := setPiBlueprint(t, f)
	if err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, _, err := db.CreateGoal(tx, &goal.Goal{
			ProjectID: f.pc.ID(), GoalID: "gl_pi001ax9",
			Status: goal.StatusDraft, ActiveVersion: 1,
		}, &goal.Version{
			ProjectID: f.pc.ID(), GoalID: "gl_pi001ax9", GoalVersion: 1,
			Title: "目标", Statement: "现状：无。目标：有。",
			Acceptance: []goal.Acceptance{{
				ID: "AC-1", Kind: goal.AcceptanceCommand,
				Check: "true", Statement: "通过", EvidenceRequired: true,
			}},
			Budget: goal.DefaultBudget(),
		}, "lead")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	before, _ := os.ReadFile(bp)
	expectReject := func(name string, plan goal.PlanRevision) {
		t.Helper()
		if _, err := PiAppendPlan(PiAppendInput{Cwd: f.repo, Plan: plan}); err == nil {
			t.Fatalf("%s 应被拒绝", name)
		}
		after, _ := os.ReadFile(bp)
		if string(after) != string(before) {
			t.Fatalf("%s 被拒绝时不得改动蓝图", name)
		}
		if ids := blueprintTaskIDs(t, bp); len(ids) != 1 {
			t.Fatalf("%s 被拒绝时不得新增任务：%v", name, ids)
		}
	}

	// 未批准。
	pending := newPiPlan(t, f, 1, 1, []goal.PlanCard{piCard("c01", "GL-001", "文档卡", nil, []string{"docs/a.md"})})
	pending.Status = goal.PlanProposed
	expectReject("未批准修订", pending)

	// 编号撞既有蓝图。
	dup := newPiPlan(t, f, 2, 1, []goal.PlanCard{piCard("c01", "KEEP-1", "撞号卡", nil, []string{"docs/b.md"})})
	expectReject("编号撞车", dup)

	// 依赖不存在的题。
	missingDep := newPiPlan(t, f, 3, 1, []goal.PlanCard{piCard("c01", "GL-003", "依赖卡", []string{"NOPE-9"}, []string{"docs/c.md"})})
	expectReject("依赖不存在", missingDep)

	// 占位验收与无序写盘重叠更早：落库校验就拒绝，连修订都存不下来。
	placeholder := piCard("c01", "GL-004", "占位卡", nil, []string{"docs/d.md"})
	placeholder.Acceptance = []string{"TODO: 补验收"}
	expectPlanSaveRejected(t, f, 4, []goal.PlanCard{placeholder})

	overlapA := piCard("c01", "GL-005", "重叠一", nil, []string{"internal/x/**"})
	overlapB := piCard("c02", "GL-006", "重叠二", nil, []string{"internal/x/y.go"})
	expectPlanSaveRejected(t, f, 5, []goal.PlanCard{overlapA, overlapB})

	if ids := blueprintTaskIDs(t, bp); len(ids) != 1 {
		t.Fatalf("拒绝路径不得留下任务：%v", ids)
	}
}

// expectPlanSaveRejected 断言坏计划在 SavePlanRevision 就被拒绝（不落库）。
func expectPlanSaveRejected(t *testing.T, f *fixture, rev int, cards []goal.PlanCard) {
	t.Helper()
	plan := goal.PlanRevision{
		ProjectID: f.pc.ID(), GoalID: "gl_pi001ax9", GoalVersion: 1,
		PlanRev: rev, Kind: goal.PlanKindInitial, Status: goal.PlanProposed, Cards: cards,
	}
	if err := plan.EnsureDigest(); err != nil {
		t.Fatal(err)
	}
	if err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, err := db.SavePlanRevision(tx, &plan, "lead")
		return err
	}); err == nil {
		t.Fatalf("修订 %d 应被拒绝落库", rev)
	}
}

func num(t *testing.T, res Result, key string) int {
	t.Helper()
	switch v := res[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	default:
		t.Fatalf("%s 不是整数：%T %v", key, res[key], res[key])
		return 0
	}
}
