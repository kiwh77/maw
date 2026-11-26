package app_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

type bpTask struct {
	id, iteration, dir string
	prio               int
}

func writeIterationBP(t *testing.T, repo, name string, iterations map[string]string, tasks []bpTask) string {
	t.Helper()
	body := fmt.Sprintf("[meta]\nname = %q\nmain_branch = \"main\"\n\n", name)
	for _, id := range sortedKeys(iterations) {
		body += fmt.Sprintf("[[iteration]]\nid = %q\nname = %q\ntarget_branch = \"\"\n\n", id, iterations[id])
	}
	for _, task := range tasks {
		body += fmt.Sprintf("[[task]]\nid = %q\ntitle = %q\npriority = %d\niteration = %q\n"+
			"write_scope = [%q]\nacceptance = [\"ok\"]\nverify = [\"manual: ok\"]\nmerge_order = %d\n\n",
			task.id, task.id, task.prio, task.iteration, task.dir+"/**", task.prio)
	}
	p := filepath.Join(repo, name+".toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func TestPlanIterationApplyWaves(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "maw")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/maw")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	repo := t.TempDir()
	globalDir := t.TempDir()
	pathEnv := "PATH=" + filepath.Dir(bin) + string(os.PathListSeparator) + os.Getenv("PATH")
	baseEnv := append(os.Environ(), pathEnv, "MAW_GLOBAL="+globalDir)
	run := func(args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir = repo
		c.Env = baseEnv
		out, err := c.CombinedOutput()
		return string(out), err
	}
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = baseEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git("init", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# t\n"), 0o644)
	for _, d := range []string{"mod_w1", "mod_w2"} {
		_ = os.MkdirAll(filepath.Join(repo, d), 0o755)
		_ = os.WriteFile(filepath.Join(repo, d, ".gitkeep"), nil, 0o644)
	}
	git("add", ".")
	git("commit", "-m", "init")
	if out, err := run("init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	wave1Tasks := []bpTask{
		{id: "T-W1-A", iteration: "IT-W1", dir: "mod_w1", prio: 10},
		{id: "T-W1-B", iteration: "IT-W1", dir: "mod_w1", prio: 20},
	}
	bp1 := writeIterationBP(t, repo, "wave1",
		map[string]string{"IT-W1": "第一波"}, wave1Tasks)
	if out, err := run("plan", "apply", bp1, "--yes"); err != nil {
		t.Fatalf("apply wave1: %v\n%s", err, out)
	}

	// 第二波是累加清单：保留第一波任务，同时加入第二波迭代与任务。
	wave2Tasks := append(append([]bpTask{}, wave1Tasks...), []bpTask{
		{id: "T-W2-A", iteration: "IT-W2", dir: "mod_w2", prio: 30},
		{id: "T-W2-B", iteration: "IT-W2", dir: "mod_w2", prio: 40},
	}...)
	bp2 := writeIterationBP(t, repo, "wave2",
		map[string]string{"IT-W1": "第一波", "IT-W2": "第二波"}, wave2Tasks)
	if out, err := run("plan", "apply", bp2, "--yes"); err != nil {
		t.Fatalf("apply wave2: %v\n%s", err, out)
	}

	gdb := filepath.Join(globalDir, "maw.db")
	err := db.WithTx(gdb, func(tx *sql.Tx) error {
		var pid string
		if err := tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&pid); err != nil {
			return err
		}
		iterations, err := db.ListIterations(tx, pid)
		if err != nil {
			return err
		}
		if len(iterations) != 2 {
			t.Fatalf("两波 apply 应得到 2 个迭代，实际 %d：%+v", len(iterations), iterations)
		}
		for _, id := range []string{"T-W1-A", "T-W1-B"} {
			task, err := db.GetTask(tx, pid, id)
			if err != nil {
				return err
			}
			if task == nil || task.IterationID != "IT-W1" {
				t.Fatalf("%s 归属错误：%+v", id, task)
			}
		}
		for _, id := range []string{"T-W2-A", "T-W2-B"} {
			task, err := db.GetTask(tx, pid, id)
			if err != nil {
				return err
			}
			if task == nil || task.IterationID != "IT-W2" {
				t.Fatalf("%s 归属错误：%+v", id, task)
			}
		}

		var raw string
		if err := tx.QueryRow(`SELECT data_json FROM events WHERE project_id = ? AND type = 'plan_applied' ORDER BY id DESC LIMIT 1`, pid).Scan(&raw); err != nil {
			return err
		}
		var payload struct {
			Iterations map[string]struct {
				Created int `json:"created"`
				Updated int `json:"updated"`
			} `json:"iterations"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return err
		}
		if payload.Iterations["IT-W2"].Created != 2 {
			t.Fatalf("第二波事件应按迭代统计 created=2：%s", raw)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 迭代生命周期状态不因重复 apply 回退。
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		var pid string
		if err := tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&pid); err != nil {
			return err
		}
		it, err := db.GetIteration(tx, pid, "IT-W1")
		if err != nil {
			return err
		}
		it.Status = db.IterationRunning
		it.UpdatedAt = ""
		return db.UpsertIteration(tx, it)
	})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := run("plan", "apply", bp2, "--yes"); err != nil {
		t.Fatalf("reapply wave2: %v\n%s", err, out)
	}
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		var pid string
		if err := tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&pid); err != nil {
			return err
		}
		counts, err := db.IterationTaskCounts(tx, pid)
		if err != nil {
			return err
		}
		if counts["IT-W1"] != 2 || counts["IT-W2"] != 2 {
			t.Fatalf("迭代任务数不符：%+v", counts)
		}
		it, err := db.GetIteration(tx, pid, "IT-W1")
		if err != nil {
			return err
		}
		if it.Status != db.IterationRunning {
			t.Fatalf("重复 apply 不应回退 iteration 状态：%+v", it)
		}
		var raw string
		if err := tx.QueryRow(`SELECT data_json FROM events WHERE project_id = ? AND type = 'plan_applied' ORDER BY id DESC LIMIT 1`, pid).Scan(&raw); err != nil {
			return err
		}
		var payload struct {
			Iterations map[string]struct {
				Created int `json:"created"`
				Updated int `json:"updated"`
			} `json:"iterations"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return err
		}
		if payload.Iterations["IT-W2"].Updated != 2 {
			t.Fatalf("重跑事件应按迭代统计 updated=2：%s", raw)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestPlanApplyIterationRetireKeepsReservedTasks 覆盖 retire 策略的确定语义：
// 蓝图外的 idle 题退役并留事件，保留态（blocked）题不动，迭代归属不被改写。
//
// 失败信息一律带 iteration_id：红的时候一眼能看出是哪一迭代出的问题。
func TestPlanIterationApplyRetireKeepsReservedTasks(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "maw")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/maw")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	repo := t.TempDir()
	globalDir := t.TempDir()
	pathEnv := "PATH=" + filepath.Dir(bin) + string(os.PathListSeparator) + os.Getenv("PATH")
	baseEnv := append(os.Environ(), pathEnv, "MAW_GLOBAL="+globalDir)
	run := func(args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir = repo
		c.Env = baseEnv
		out, err := c.CombinedOutput()
		return string(out), err
	}
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = baseEnv
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git("init", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# t\n"), 0o644)
	for _, d := range []string{"mod_w1", "mod_w2"} {
		_ = os.MkdirAll(filepath.Join(repo, d), 0o755)
		_ = os.WriteFile(filepath.Join(repo, d, ".gitkeep"), nil, 0o644)
	}
	git("add", ".")
	git("commit", "-m", "init")
	if out, err := run("init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	// 第一波：一道 idle 题与一道保留态（blocked）题，都属 IT-W1。
	wave1 := writeIterationBP(t, repo, "wave1", map[string]string{"IT-W1": "第一波"}, []bpTask{
		{id: "T-W1-IDLE", iteration: "IT-W1", dir: "mod_w1", prio: 10},
		{id: "T-W1-KEPT", iteration: "IT-W1", dir: "mod_w1", prio: 20},
	})
	if out, err := run("plan", "apply", wave1, "--yes"); err != nil {
		t.Fatalf("apply wave1: %v\n%s", err, out)
	}

	gdb := filepath.Join(globalDir, "maw.db")
	if err := db.WithTx(gdb, func(tx *sql.Tx) error {
		var pid string
		if err := tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&pid); err != nil {
			return err
		}
		task, err := db.GetTask(tx, pid, "T-W1-KEPT")
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("迭代 IT-W1 的题 T-W1-KEPT 不存在：wave1 apply 后应有该题")
		}
		task.Status = "blocked"
		if err := db.UpsertTask(tx, task); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 第二波：只带第二波题与第一波的保留态题；第一波的 idle 题不在蓝图里。
	wave2 := writeIterationBP(t, repo, "wave2", map[string]string{"IT-W1": "第一波", "IT-W2": "第二波"}, []bpTask{
		{id: "T-W1-KEPT", iteration: "IT-W1", dir: "mod_w1", prio: 20},
		{id: "T-W2-A", iteration: "IT-W2", dir: "mod_w2", prio: 30},
	})
	if out, err := run("plan", "apply", wave2, "--yes"); err != nil {
		t.Fatalf("apply wave2: %v\n%s", err, out)
	}

	if err := db.WithTx(gdb, func(tx *sql.Tx) error {
		var pid string
		if err := tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&pid); err != nil {
			return err
		}
		// 蓝图外的 idle 题退役（IT-W1）。
		idle, err := db.GetTask(tx, pid, "T-W1-IDLE")
		if err != nil {
			return err
		}
		if idle != nil {
			return fmt.Errorf("迭代 IT-W1 的题 T-W1-IDLE 仍在库：蓝图外的 idle 题应退役（%+v）", idle)
		}
		var retired int
		if err := tx.QueryRow(
			`SELECT COUNT(*) FROM events WHERE project_id = ? AND type = 'task_retired'`,
			pid,
		).Scan(&retired); err != nil {
			return err
		}
		if retired != 1 {
			return fmt.Errorf("迭代 IT-W1 的退役事件数=%d 期望 1", retired)
		}
		// 保留态题不动，归属仍是 IT-W1。
		kept, err := db.GetTask(tx, pid, "T-W1-KEPT")
		if err != nil {
			return err
		}
		if kept == nil {
			return fmt.Errorf("迭代 IT-W1 的题 T-W1-KEPT 被删了：保留态题不该因 apply 退役")
		}
		if kept.IterationID != "IT-W1" {
			return fmt.Errorf("迭代 IT-W1 的题 T-W1-KEPT 归属变成了 %q", kept.IterationID)
		}
		if kept.Status != "blocked" {
			return fmt.Errorf("迭代 IT-W1 的题 T-W1-KEPT 状态变成了 %q 期望 blocked", kept.Status)
		}
		// 第二波题归属 IT-W2。
		second, err := db.GetTask(tx, pid, "T-W2-A")
		if err != nil {
			return err
		}
		if second == nil {
			return fmt.Errorf("迭代 IT-W2 的题 T-W2-A 不存在")
		}
		if second.IterationID != "IT-W2" {
			return fmt.Errorf("迭代 IT-W2 的题 T-W2-A 归属是 %q 期望 IT-W2", second.IterationID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
