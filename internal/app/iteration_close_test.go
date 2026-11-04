package app

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

// setupIterationCloseRepo 造一个真实初始化过的工程：真实 git 仓库 + 独立全局库。
// 收口命令要读工程配置与全局库，用替身测不出装配问题。
func setupIterationCloseRepo(t *testing.T) (repo, projectID, globalDB string) {
	t.Helper()
	t.Setenv("MAW_GLOBAL", t.TempDir())
	t.Setenv("MAW_LANG", "zh")
	t.Setenv("MAW_AGENT", "")
	repo = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	res, err := Init(repo, false, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	projectID, _ = res["project_id"].(string)
	globalDB, _ = res["global_db"].(string)
	if projectID == "" || globalDB == "" {
		t.Fatalf("init 结果缺 project_id/global_db：%+v", res)
	}
	return repo, projectID, globalDB
}

// seedIteration 直接写库造一条迭代与若干任务，避免依赖别的命令的副作用。
func seedIteration(t *testing.T, globalDB, projectID, iterID, status string, taskStatus string, taskIDs ...string) {
	t.Helper()
	err := db.WithTx(globalDB, func(tx *sql.Tx) error {
		if err := db.UpsertIteration(tx, &db.Iteration{
			ProjectID: projectID, ID: iterID, Name: iterID,
			Status: status, TargetBranch: "main",
		}); err != nil {
			return err
		}
		for _, id := range taskIDs {
			if err := db.UpsertTask(tx, &db.Task{
				ProjectID: projectID, ID: id, Title: id, Status: taskStatus,
				Priority: 10, IterationID: iterID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readIteration(t *testing.T, globalDB, projectID, iterID string) *db.Iteration {
	t.Helper()
	var out *db.Iteration
	err := db.WithTx(globalDB, func(tx *sql.Tx) error {
		it, err := db.GetIteration(tx, projectID, iterID)
		out = it
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatalf("迭代 %s 不存在", iterID)
	}
	return out
}

// countIterationEvents 数 iteration_status_changed 事件，顺带把最后一次的 from/to 取出来。
func countIterationEvents(t *testing.T, globalDB, projectID string) (int, map[string]any) {
	t.Helper()
	n := 0
	var last map[string]any
	err := db.WithTx(globalDB, func(tx *sql.Tx) error {
		events, err := db.ListEventsSince(tx, 0, projectID, "", 500)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e == nil || e.Type != "iteration_status_changed" {
				continue
			}
			n++
			if e.Data != nil {
				last = e.Data
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n, last
}

func TestIterationCloseMergesAndIsIdempotent(t *testing.T) {
	repo, pid, gdb := setupIterationCloseRepo(t)
	seedIteration(t, gdb, pid, "IT-1", db.IterationRunning, "done", "T-1", "T-2")

	res, err := IterationClose(repo, IterationCloseInput{ID: "IT-1", MergeCommit: "abc1234"})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if res["changed"] != true || res["from"] != db.IterationRunning || res["to"] != db.IterationMerged {
		t.Fatalf("收口结果不符：%+v", res)
	}
	// 语言包在包级初始化，测试进程里未必是中文；只断言标签已解析（不是裸键）。
	if label, _ := res["status_label"].(string); label == "" || label == "iteration.status.merged" {
		t.Fatalf("状态标签未解析：%q", label)
	}
	it := readIteration(t, gdb, pid, "IT-1")
	if it.Status != db.IterationMerged || it.MergeCommit != "abc1234" {
		t.Fatalf("收口未落库：%+v", it)
	}
	n, data := countIterationEvents(t, gdb, pid)
	if n != 1 {
		t.Fatalf("应写一条审计事件，得到 %d", n)
	}
	if data["from"] != db.IterationRunning || data["to"] != db.IterationMerged {
		t.Fatalf("审计事件缺少迁移前后状态：%+v", data)
	}

	// 重复收口：状态不变、不追加第二条事件。
	again, err := IterationClose(repo, IterationCloseInput{ID: "IT-1"})
	if err != nil {
		t.Fatalf("repeat close: %v", err)
	}
	if again["changed"] != false {
		t.Fatalf("重复收口应报告 changed=false：%+v", again)
	}
	if n2, _ := countIterationEvents(t, gdb, pid); n2 != 1 {
		t.Fatalf("重复收口不该追加事件，得到 %d 条", n2)
	}
}

func TestIterationCloseRefusesOpenTasksUnlessForced(t *testing.T) {
	repo, pid, gdb := setupIterationCloseRepo(t)
	seedIteration(t, gdb, pid, "IT-2", db.IterationRunning, "ready", "T-9")

	_, err := IterationClose(repo, IterationCloseInput{ID: "IT-2"})
	if err == nil {
		t.Fatal("迭代内仍有未完成任务时应拒绝收口")
	}
	msg := err.Error()
	if !strings.Contains(msg, "T-9") || !strings.Contains(msg, "--force") {
		t.Fatalf("拒绝信息要能定位任务并给出口：%v", err)
	}
	if it := readIteration(t, gdb, pid, "IT-2"); it.Status != db.IterationRunning {
		t.Fatalf("被拒绝的收口不得改库：%+v", it)
	}

	res, err := IterationClose(repo, IterationCloseInput{ID: "IT-2", Force: true})
	if err != nil {
		t.Fatalf("force close: %v", err)
	}
	skipped, _ := res["forced_open_tasks"].([]string)
	if len(skipped) != 1 || skipped[0] != "T-9" {
		t.Fatalf("强制收口要回报被跳过的任务：%+v", res)
	}
	if it := readIteration(t, gdb, pid, "IT-2"); it.Status != db.IterationMerged {
		t.Fatalf("强制收口后状态应为 merged：%+v", it)
	}
}

func TestIterationCloseRejectsIllegalTransitionAndBadInput(t *testing.T) {
	repo, pid, gdb := setupIterationCloseRepo(t)
	seedIteration(t, gdb, pid, "IT-3", db.IterationMerged, "done", "T-1")

	// 已合入不可改判为放弃。
	if _, err := IterationClose(repo, IterationCloseInput{ID: "IT-3", Status: db.IterationAbandoned}); err == nil {
		t.Fatal("merged → abandoned 应被拒绝")
	}
	if it := readIteration(t, gdb, pid, "IT-3"); it.Status != db.IterationMerged {
		t.Fatalf("被拒绝的迁移不得改库：%+v", it)
	}
	// 从 planning 直接收口也不行（不跳过执行）。
	seedIteration(t, gdb, pid, "IT-4", db.IterationPlanning, "done", "T-2")
	if _, err := IterationClose(repo, IterationCloseInput{ID: "IT-4"}); err == nil {
		t.Fatal("planning → merged 应被拒绝")
	}
	// 入参校验。
	if _, err := IterationClose(repo, IterationCloseInput{}); err == nil {
		t.Fatal("缺迭代 id 应报错")
	}
	if _, err := IterationClose(repo, IterationCloseInput{ID: "IT-3", Status: "finishing"}); err == nil {
		t.Fatal("收口只接受 merged/abandoned")
	}
	if _, err := IterationClose(repo, IterationCloseInput{ID: "IT-404"}); err == nil {
		t.Fatal("未知迭代应报错")
	}
}

func TestIterationCloseDryRunDoesNotWrite(t *testing.T) {
	repo, pid, gdb := setupIterationCloseRepo(t)
	seedIteration(t, gdb, pid, "IT-5", db.IterationRunning, "done", "T-1")

	res, err := IterationClose(repo, IterationCloseInput{ID: "IT-5", DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res["dry_run"] != true || res["to"] != db.IterationMerged {
		t.Fatalf("dry run 应只报告计划：%+v", res)
	}
	if it := readIteration(t, gdb, pid, "IT-5"); it.Status != db.IterationRunning {
		t.Fatalf("dry run 不得改库：%+v", it)
	}
	if n, _ := countIterationEvents(t, gdb, pid); n != 0 {
		t.Fatalf("dry run 不得写事件，得到 %d", n)
	}
}
