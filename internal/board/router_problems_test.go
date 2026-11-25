package board

import (
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

// TestBuildRouterProblemsClassifiesIntervention 覆盖 Router 介入面：
// 有作业的任务显示介入结论，没作业的 blocked 任务显示「无人接管」。
func TestBuildRouterProblemsClassifiesIntervention(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	recent := now.Add(-10 * time.Minute).Format(time.RFC3339)
	tasks := []*db.Task{
		{ProjectID: "p1", ID: "T-1", Status: "blocked", BlockedReason: "环境失败", Heartbeat: recent},
		{ProjectID: "p1", ID: "T-2", Status: "blocked", BlockedReason: "依赖缺失", Heartbeat: recent},
		{ProjectID: "p1", ID: "T-3", Status: "ready"},
	}
	jobs := []*db.SystemJob{
		{
			JobID: "j1", ProjectID: "p1", TaskID: "T-1", Kind: "unblock_assist",
			Status: "done", CreatedAt: recent,
			Decision: map[string]any{"action": "unblock_and_retry"},
			Result:   map[string]any{"note": "retried"},
		},
	}
	got := BuildRouterProblems(tasks, jobs, now)
	if len(got) != 2 {
		t.Fatalf("只有 blocked 任务参与对账，应产出 2 条：%+v", got)
	}
	byTask := map[string]Problem{}
	for _, p := range got {
		byTask[p.TaskID] = p
	}
	if byTask["T-1"].Kind != ProblemKindRouter {
		t.Fatalf("有作业的任务应标 router: %+v", byTask["T-1"])
	}
	if byTask["T-1"].Severity != ProblemSeverityLow {
		t.Fatalf("已介入应为低优先：%+v", byTask["T-1"])
	}
	if !strings.Contains(byTask["T-1"].Message, "unblock_and_retry") {
		t.Fatalf("应带决策动作：%q", byTask["T-1"].Message)
	}
	if byTask["T-2"].Kind != ProblemKindRouterIdle {
		t.Fatalf("没有作业的阻塞应标 router-idle: %+v", byTask["T-2"])
	}
	if byTask["T-2"].Severity != ProblemSeverityMedium {
		t.Fatalf("无人接管应为中优先：%+v", byTask["T-2"])
	}
	if _, ok := byTask["T-3"]; ok {
		t.Fatalf("非 blocked 任务不该出现：%+v", byTask["T-3"])
	}
}

// TestBuildRouterProblemsEscalatedAndStale：升级要给原因，超窗口的旧作业不再算介入。
func TestBuildRouterProblemsEscalatedAndStale(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour).Format(time.RFC3339)
	old := now.Add(-48 * time.Hour).Format(time.RFC3339)
	tasks := []*db.Task{
		{ProjectID: "p1", ID: "T-1", Status: "blocked"},
		{ProjectID: "p1", ID: "T-2", Status: "blocked"},
	}
	jobs := []*db.SystemJob{
		{
			JobID: "j1", ProjectID: "p1", TaskID: "T-1", Kind: "unblock_assist",
			Status: "escalated", CreatedAt: recent, Reason: "submit 环境失联",
			Decision: map[string]any{"action": "escalate_human", "reason": "needs_human"},
		},
		{
			JobID: "j2", ProjectID: "p1", TaskID: "T-2", Kind: "unblock_assist",
			Status: "done", CreatedAt: old,
		},
	}
	got := BuildRouterProblems(tasks, jobs, now)
	byTask := map[string]Problem{}
	for _, p := range got {
		byTask[p.TaskID] = p
	}
	if byTask["T-1"].Kind != ProblemKindRouter || !strings.Contains(byTask["T-1"].Message, "needs_human") {
		t.Fatalf("升级项应带原因：%+v", byTask["T-1"])
	}
	if byTask["T-2"].Kind != ProblemKindRouterIdle {
		t.Fatalf("超过窗口的旧作业不算介入：%+v", byTask["T-2"])
	}
}
