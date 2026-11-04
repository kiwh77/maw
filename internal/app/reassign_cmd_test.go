package app_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/db"
)

// seedAgent 注册一个目标 Agent（改派要求目标已注册）。
func seedAgent(t *testing.T, gdb, id string) {
	t.Helper()
	err := db.WithTx(gdb, func(tx *sql.Tx) error {
		projects, e := db.ListProjects(tx)
		if e != nil {
			return e
		}
		if len(projects) == 0 {
			t.Fatal("no project")
		}
		if err := db.UpsertAgent(tx, &db.Agent{
			ProjectID: projects[0].ID, ID: id, Role: "worker", Status: "idle",
		}); err != nil {
			return err
		}
		return db.SetAgentLabels(tx, projects[0].ID, id,
			[]string{"go", "db"}, []string{"reviewer-capable"})
	})
	if err != nil {
		t.Fatalf("seed agent %s: %v", id, err)
	}
}

// currentTask 读回任务行（owner/revision 断言用）。
func currentTask(t *testing.T, gdb, id string) *db.Task {
	t.Helper()
	var out *db.Task
	if err := db.WithTx(gdb, func(tx *sql.Tx) error {
		projects, e := db.ListProjects(tx)
		if e != nil {
			return e
		}
		task, e := db.GetTask(tx, projects[0].ID, id)
		out = task
		return e
	}); err != nil {
		t.Fatalf("read task %s: %v", id, err)
	}
	if out == nil {
		t.Fatalf("任务 %s 不存在", id)
	}
	return out
}

// TestReassignUpdatesOwnerAndRevision 覆盖成功路径：owner 改掉、revision +1、事件可查。
func TestReassignUpdatesOwnerAndRevision(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	seedWaitTask(t, gdb, "R-1", "ready", nil)
	seedAgent(t, gdb, "peer-1")

	res, err := app.Reassign(repo, app.ReassignInput{
		TaskID: "R-1", To: "peer-1", ExpectedRevision: 0,
	})
	if err != nil {
		t.Fatalf("改派失败: %v", err)
	}
	if got, _ := res["owner"].(string); got != "peer-1" {
		t.Fatalf("结果 owner=%q", got)
	}
	task := currentTask(t, gdb, "R-1")
	if task.Owner != "peer-1" || task.Revision != 1 {
		t.Fatalf("落库 owner=%q revision=%d", task.Owner, task.Revision)
	}

	// 事件可查
	var found bool
	var payload map[string]any
	if err := db.WithTx(gdb, func(tx *sql.Tx) error {
		events, e := db.ListEventsByTask(tx, task.ProjectID, "R-1", 50)
		if e != nil {
			return e
		}
		for _, ev := range events {
			if ev.Type == "task_reassigned" {
				found = true
				payload = ev.Data
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("缺少 task_reassigned 事件")
	}
	if payload["to"] != "peer-1" || payload["from"] != "" {
		t.Fatalf("事件载荷不对: %#v", payload)
	}
}

// TestReassignRevisionMismatchKeepsOwner 覆盖冲突：旧 revision 改派失败且 owner 不变。
func TestReassignRevisionMismatchKeepsOwner(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	seedWaitTask(t, gdb, "R-2", "ready", nil)
	seedAgent(t, gdb, "peer-1")
	seedAgent(t, gdb, "peer-2")

	if _, err := app.Reassign(repo, app.ReassignInput{
		TaskID: "R-2", To: "peer-1", ExpectedRevision: 0,
	}); err != nil {
		t.Fatalf("第一次改派: %v", err)
	}
	_, err := app.Reassign(repo, app.ReassignInput{
		TaskID: "R-2", To: "peer-2", ExpectedRevision: 0,
	})
	if err == nil {
		t.Fatal("过期 revision 必须被拒绝")
	}
	if !strings.Contains(err.Error(), "revision_mismatch") {
		t.Fatalf("错误应含 revision_mismatch: %v", err)
	}
	task := currentTask(t, gdb, "R-2")
	if task.Owner != "peer-1" || task.Revision != 1 {
		t.Fatalf("冲突后 owner/revision 应保持 peer-1/1，得到 %q/%d", task.Owner, task.Revision)
	}
}

// TestReassignRequiresRevision 覆盖契约硬要求：不带期望版本不许改派。
func TestReassignRequiresRevision(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	seedWaitTask(t, gdb, "R-3", "ready", nil)
	seedAgent(t, gdb, "peer-1")

	if _, err := app.Reassign(repo, app.ReassignInput{
		TaskID: "R-3", To: "peer-1", ExpectedRevision: -1,
	}); err == nil {
		t.Fatal("缺 --revision 必须被拒绝")
	}
	if task := currentTask(t, gdb, "R-3"); task.Owner != "" || task.Revision != 0 {
		t.Fatalf("拒绝后不应改任何字段：owner=%q revision=%d", task.Owner, task.Revision)
	}
}

// TestAgentRolesAndTagsRoundTrip 覆盖标签存储：tags/roles 存 meta、可读回、可判命中。
func TestAgentRolesAndTagsRoundTrip(t *testing.T) {
	_, gdb := newStatusRepo(t)
	seedAgent(t, gdb, "peer-9")

	err := db.WithTx(gdb, func(tx *sql.Tx) error {
		projects, e := db.ListProjects(tx)
		if e != nil {
			return e
		}
		pid := projects[0].ID
		tags, roles, e := db.AgentLabels(tx, pid, "peer-9")
		if e != nil {
			return e
		}
		if len(tags) != 2 || tags[0] != "db" || tags[1] != "go" {
			t.Fatalf("tags 未归一化排序: %v", tags)
		}
		if len(roles) != 1 || roles[0] != "reviewer-capable" {
			t.Fatalf("roles=%v", roles)
		}
		hit, e := db.AgentHasLabel(tx, pid, "peer-9", "reviewer-capable")
		if e != nil || !hit {
			t.Fatalf("roles 也应命中 label: hit=%v err=%v", hit, e)
		}
		// 只改 tags 不得抹掉 roles
		if e := db.SetAgentLabels(tx, pid, "peer-9", []string{"frontend"}, nil); e != nil {
			return e
		}
		tags, roles, e = db.AgentLabels(tx, pid, "peer-9")
		if e != nil {
			return e
		}
		if len(tags) != 1 || tags[0] != "frontend" || len(roles) != 1 {
			t.Fatalf("覆盖写破坏 roles: tags=%v roles=%v", tags, roles)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("labels: %v", err)
	}
}
