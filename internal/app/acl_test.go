package app_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/db"
)

// addLeads 在工程配置里登记本机 lead 名单（等价于运维手改 [identity] leads）。
func addLeads(t *testing.T, repo string, leads ...string) {
	t.Helper()
	path := filepath.Join(repo, ".multiagent", "config.toml")
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
	next := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestACLReassignRequiresLead 覆盖越权改派：普通成员被 not_lead 拒绝，owner 不变。
func TestACLReassignRequiresLead(t *testing.T) {
	repo, gdb := newStatusRepo(t) // MAW_AGENT=cursor-demo-1，未登记 leads
	seedWaitTask(t, gdb, "A-1", "ready", nil)
	seedAgent(t, gdb, "peer-1")

	_, err := app.Reassign(repo, app.ReassignInput{
		TaskID: "A-1", To: "peer-1", ExpectedRevision: 0,
	})
	if err == nil {
		t.Fatal("非 lead 改派必须失败")
	}
	if !strings.Contains(err.Error(), "not_lead") {
		t.Fatalf("错误码应稳定为 not_lead: %v", err)
	}
	if task := currentTask(t, gdb, "A-1"); task.Owner != "" {
		t.Fatalf("越权拒绝后 owner 不应变化：%q", task.Owner)
	}
}

// TestACLReassignLeadSucceeds 覆盖 Lead 路径：登记后同一动作成功。
func TestACLReassignLeadSucceeds(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	seedWaitTask(t, gdb, "A-2", "ready", nil)
	seedAgent(t, gdb, "peer-1")
	addLeads(t, repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")

	res, err := app.Reassign(repo, app.ReassignInput{
		TaskID: "A-2", To: "peer-1", ExpectedRevision: 0,
	})
	if err != nil {
		t.Fatalf("lead 改派应成功: %v", err)
	}
	if got, _ := res["owner"].(string); got != "peer-1" {
		t.Fatalf("owner=%q", got)
	}
}

// TestACLInterruptRequiresLead 覆盖越权中断：非 lead 被拒，任务仍在执行态。
func TestACLInterruptRequiresLead(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	seedWaitTask(t, gdb, "A-3", "in_progress", nil)

	_, err := app.Interrupt(repo, app.InterruptInput{
		TaskID: "A-3", ExpectedRevision: 0, Reason: "旁路喊停",
	})
	if err == nil {
		t.Fatal("非 lead 中断必须失败")
	}
	if !strings.Contains(err.Error(), "not_lead") {
		t.Fatalf("错误码应稳定为 not_lead: %v", err)
	}
	if task := currentTask(t, gdb, "A-3"); task.Status != "in_progress" {
		t.Fatalf("越权拒绝后状态不应变化：%s", task.Status)
	}
}

// TestACLInterruptLeadBlocksTask 覆盖 Lead 路径：中断后转 blocked（可恢复）并写事件。
func TestACLInterruptLeadBlocksTask(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	seedWaitTask(t, gdb, "A-4", "in_progress", nil)
	addLeads(t, repo, "lead-1")
	t.Setenv("MAW_AGENT", "lead-1")

	res, err := app.Interrupt(repo, app.InterruptInput{
		TaskID: "A-4", ExpectedRevision: 0, Reason: "跑到别人的写盘范围了",
	})
	if err != nil {
		t.Fatalf("lead 中断应成功: %v", err)
	}
	if res["status"] != "blocked" {
		t.Fatalf("中断后应为 blocked: %v", res["status"])
	}

	var (
		status, prev, reason string
		eventFound           bool
	)
	projects := func(tx *sql.Tx) (string, error) {
		ps, e := db.ListProjects(tx)
		if e != nil || len(ps) == 0 {
			t.Fatalf("no project")
		}
		return ps[0].ID, e
	}
	if err := db.WithTx(gdb, func(tx *sql.Tx) error {
		pid, e := projects(tx)
		if e != nil {
			return e
		}
		task, e := db.GetTask(tx, pid, "A-4")
		if e != nil {
			return e
		}
		status, prev, reason = task.Status, task.PrevStatus, task.BlockedReason
		events, e := db.ListEventsByTask(tx, pid, "A-4", 50)
		if e != nil {
			return e
		}
		for _, ev := range events {
			if ev.Type == "task_interrupted" {
				eventFound = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" || prev != "in_progress" {
		t.Fatalf("status=%q prev=%q", status, prev)
	}
	if reason != "跑到别人的写盘范围了" {
		t.Fatalf("原因未落库: %q", reason)
	}
	if !eventFound {
		t.Fatal("缺少 task_interrupted 事件")
	}
}
