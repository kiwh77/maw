package board

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

func task(id, projectID, status string) *db.Task {
	return &db.Task{ProjectID: projectID, ProjectName: "工程" + projectID, ID: id, Status: status}
}

func TestBuildProblemsCoversFiveKinds(t *testing.T) {
	stale := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	tasks := []*db.Task{
		{ProjectID: "p1", ID: "T-1", Status: "blocked", BlockedReason: "缺上游契约", Heartbeat: "2026-11-04 08:00:00"},
		{ProjectID: "p1", ID: "T-2", Status: "conflict"},
		{ProjectID: "p2", ID: "T-3", Status: "in_progress", Attempts: 2},
		{ProjectID: "p2", ID: "T-4", Status: "backlog", Deps: []string{"T-9"}}, // 引用查不到 → 未完成
		{ProjectID: "p2", ID: "T-5", Status: "ready", Deps: []string{"T-6"}},   // T-6 非 done → 等待
		{ProjectID: "p2", ID: "T-6", Status: "in_progress"},
		{ProjectID: "p3", ID: "T-7", Status: "ready", Deps: []string{"T-8"}}, // T-8 done → 无问题
		{ProjectID: "p3", ID: "T-8", Status: "done"},
	}
	agents := []*db.Agent{
		{ProjectID: "p2", ID: "app-p2-w1", Status: "working", TaskID: "T-3", LastSeen: stale},
		{ProjectID: "p2", ID: "app-p2-w2", Status: "working", TaskID: "T-6", LastSeen: time.Now().UTC().Format(time.RFC3339)},
		{ProjectID: "p2", ID: "app-p2-w3", Status: "idle", LastSeen: stale},
	}
	problems := BuildProblems(tasks, agents)
	got := map[string][]Problem{}
	for _, p := range problems {
		got[p.Kind] = append(got[p.Kind], p)
	}
	for _, kind := range []string{ProblemKindBlocked, ProblemKindConflict, ProblemKindRetry, ProblemKindStalled, ProblemKindDepWait} {
		ps, ok := got[kind]
		if !ok || len(ps) == 0 {
			t.Fatalf("缺少 kind=%s，实际：%+v", kind, problems)
		}
		for _, p := range ps {
			if p.ProjectID == "" || p.TaskID == "" || p.Message == "" {
				t.Fatalf("kind=%s 字段不完整：%+v", kind, p)
			}
		}
	}
	// dep-wait 两条：T-4 引用查不到（视为未完成）、T-5 引用 T-6 非 done；T-7 的 dep T-8 已 done 不该有问题。
	if len(got[ProblemKindDepWait]) != 2 {
		t.Fatalf("dep-wait 应恰为 2 条：%+v", problems)
	}
	if got[ProblemKindBlocked][0].Message != "缺上游契约" {
		t.Fatalf("blocked 应透传 blocked_reason：%+v", got[ProblemKindBlocked])
	}
	if got[ProblemKindConflict][0].Severity != ProblemSeverityHigh || got[ProblemKindBlocked][0].Severity != ProblemSeverityHigh {
		t.Fatalf("blocked/conflict 应为 high：%+v", problems)
	}
	if got[ProblemKindRetry][0].Severity != ProblemSeverityMedium || got[ProblemKindStalled][0].Severity != ProblemSeverityMedium {
		t.Fatalf("retry/stalled 应为 medium：%+v", problems)
	}
	if got[ProblemKindDepWait][0].Severity != ProblemSeverityLow {
		t.Fatalf("dep-wait 应为 low：%+v", got[ProblemKindDepWait])
	}
	if got[ProblemKindStalled][0].TaskID != "T-3" {
		t.Fatalf("stalled 应挂在 working 中的任务上：%+v", got[ProblemKindStalled])
	}
}

func TestBuildProblemsBlockedReasonEmptyFallsBack(t *testing.T) {
	problems := BuildProblems([]*db.Task{task("T-1", "p1", "blocked")}, nil)
	if len(problems) != 1 || problems[0].Message != "未填写原因" {
		t.Fatalf("空 blocked_reason 应回退默认文案：%+v", problems)
	}
}

func TestBuildProblemsSeverityOrderStable(t *testing.T) {
	tasks := []*db.Task{
		{ProjectID: "p1", ID: "T-3", Status: "conflict"},
		{ProjectID: "p1", ID: "T-1", Status: "blocked"},
		{ProjectID: "p1", ID: "T-2", Status: "blocked"},
		{ProjectID: "p2", ID: "T-5", Status: "in_progress", Attempts: 3},
		{ProjectID: "p2", ID: "T-4", Status: "ready", Deps: []string{"T-6"}},
	}
	problems := BuildProblems(tasks, nil)
	want := []string{
		"p1/T-1", "p1/T-2", "p1/T-3", // high 档按 (project_id, task_id) 排序
		"p2/T-5", // medium 档
		"p2/T-4", // low 档
	}
	if len(problems) != len(want) {
		t.Fatalf("问题数不符：%+v", problems)
	}
	for i, key := range want {
		p := problems[i]
		if p.ProjectID+"/"+p.TaskID != key {
			t.Fatalf("第 %d 条应为 %s，实际 %s/%s（%+v）", i, key, p.ProjectID, p.TaskID, problems)
		}
	}
	for i := 1; i < len(problems); i++ {
		if severityRank(problems[i].Severity) > severityRank(problems[i-1].Severity) {
			t.Fatalf("severity 必须降序排列：%+v", problems)
		}
	}
}

func TestBuildProblemsEmptyInputReturnsEmptySlice(t *testing.T) {
	problems := BuildProblems(nil, nil)
	if problems == nil || len(problems) != 0 {
		t.Fatalf("空输入应返回非 nil 空切片：%#v", problems)
	}
}

func TestSnapshotProblemsJSONAndNormalize(t *testing.T) {
	snap := BuildSnapshot([]*db.Task{task("T-1", "p1", "blocked")}, nil, 4)
	if snap.Problems == nil || len(snap.Problems) != 1 {
		t.Fatalf("快照应含聚合问题：%+v", snap.Problems)
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"problems":[`) {
		t.Fatalf("JSON 序列化应含 problems 数组：%s", b)
	}
	var empty Snapshot
	empty.Normalize()
	if empty.Problems == nil {
		t.Fatal("Normalize 后 problems 不得为 nil")
	}
	b, err = json.Marshal(Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"problems":[]`) {
		t.Fatalf("空库 JSON 的 problems 应为 []：%s", b)
	}
}

func TestSnapshotWithIterationRecomputesProblems(t *testing.T) {
	tasks := []*db.Task{
		{ProjectID: "p1", ProjectName: "P1", ID: "T-1", Status: "blocked", IterationID: "IT-A"},
		{ProjectID: "p1", ProjectName: "P1", ID: "T-2", Status: "conflict", IterationID: "IT-B"},
	}
	snap := BuildSnapshot(tasks, nil, 4)
	if len(snap.Problems) != 2 {
		t.Fatalf("全量应含两条问题：%+v", snap.Problems)
	}
	narrow := snap.WithIteration("IT-A")
	if len(narrow.Problems) != 1 {
		t.Fatalf("收窄后应只含该迭代的问题：%+v", narrow.Problems)
	}
	if narrow.Problems[0].TaskID != "T-1" || narrow.Problems[0].Kind != ProblemKindBlocked {
		t.Fatalf("收窄后问题不正确：%+v", narrow.Problems)
	}
}
