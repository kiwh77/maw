package app_test

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

// TestPlanApplyPreservesHistory：增量 apply 不得抹掉 done / in_progress。
func TestPlanApplyPreservesHistory(t *testing.T) {
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
	_ = os.MkdirAll(filepath.Join(repo, "mod_a"), 0o755)
	_ = os.MkdirAll(filepath.Join(repo, "mod_b"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "mod_a", ".gitkeep"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(repo, "mod_b", ".gitkeep"), nil, 0o644)
	git("add", ".")
	git("commit", "-m", "init")

	if out, err := run("init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	bp1 := filepath.Join(repo, "bp1.toml")
	_ = os.WriteFile(bp1, []byte(`
[meta]
name = "wave1"
version = 1

[[task]]
id = "T-DONE"
title = "already done later"
priority = 10
write_scope = ["mod_a/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 10

[[task]]
id = "T-WIP"
title = "will be in progress"
priority = 10
write_scope = ["mod_b/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 20

[[task]]
id = "T-IDLE"
title = "will be dropped"
priority = 10
write_scope = ["README.md"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 30
`), 0o644)
	if out, err := run("plan", "apply", bp1, "--yes"); err != nil {
		t.Fatalf("apply1: %v\n%s", err, out)
	}

	gdb := filepath.Join(globalDir, "maw.db")
	mustStatus := func(id, want string) {
		t.Helper()
		err := db.WithTx(gdb, func(tx *sql.Tx) error {
			var pid string
			_ = tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&pid)
			task, err := db.GetTask(tx, pid, id)
			if err != nil {
				return err
			}
			if task == nil {
				t.Fatalf("missing %s", id)
			}
			if task.Status != want {
				t.Fatalf("%s status=%s want=%s", id, task.Status, want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	setStatus := func(id, status string) {
		t.Helper()
		err := db.WithTx(gdb, func(tx *sql.Tx) error {
			var pid string
			if err := tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&pid); err != nil {
				return err
			}
			task, err := db.GetTask(tx, pid, id)
			if err != nil || task == nil {
				return err
			}
			task.Status = status
			if status == "in_progress" {
				task.Owner = "w1"
				task.Branch = "agent/w1/" + id
				task.Worktree = ".worktrees/w1-" + id
			}
			return db.UpsertTask(tx, task)
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	setStatus("T-DONE", "done")
	setStatus("T-WIP", "in_progress")
	mustStatus("T-DONE", "done")
	mustStatus("T-WIP", "in_progress")
	mustStatus("T-IDLE", "ready") // promoteReady 后无依赖应 ready

	// 第二波：只压入新题；旧 idle 应消失，done/wip 应保留
	bp2 := filepath.Join(repo, "bp2.toml")
	_ = os.WriteFile(bp2, []byte(`
[meta]
name = "wave2"
version = 2

[[task]]
id = "T-NEW"
title = "new gap"
priority = 10
deps = ["T-DONE"]
write_scope = ["mod_a/**"]
acceptance = ["wired"]
verify = ["manual: wired"]
merge_order = 10

[[task]]
id = "T-WIP"
title = "wip retitled"
priority = 20
write_scope = ["mod_b/**"]
acceptance = ["still going"]
verify = ["manual: still"]
merge_order = 20
`), 0o644)
	if out, err := run("plan", "apply", bp2, "--yes"); err != nil {
		t.Fatalf("apply2: %v\n%s", err, out)
	}

	err := db.WithTx(gdb, func(tx *sql.Tx) error {
		var pid string
		if err := tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&pid); err != nil {
			return err
		}
		all, err := db.ListTasks(tx, pid)
		if err != nil {
			return err
		}
		by := map[string]*db.Task{}
		for _, tsk := range all {
			by[tsk.ID] = tsk
		}
		idle := by["T-IDLE"]
		if idle == nil || idle.Status != "cancelled" {
			t.Fatalf("T-IDLE 应软删除为 cancelled，实际 %#v", idle)
		}
		if by["T-DONE"] == nil || by["T-DONE"].Status != "done" {
			t.Fatalf("T-DONE 应保留 done，实际 %#v", by["T-DONE"])
		}
		wip := by["T-WIP"]
		if wip == nil || wip.Status != "in_progress" {
			t.Fatalf("T-WIP 应保留 in_progress，实际 %#v", wip)
		}
		if wip.Owner != "w1" || wip.Title != "wip retitled" {
			t.Fatalf("T-WIP 应刷新标题并保留 owner，实际 title=%q owner=%q", wip.Title, wip.Owner)
		}
		neu := by["T-NEW"]
		if neu == nil {
			t.Fatal("T-NEW 应入库")
		}
		// deps T-DONE 已 done → 应 promote 为 ready（写盘与 WIP 不重叠）
		if neu.Status != "ready" {
			t.Fatalf("T-NEW status=%s want ready", neu.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// --reset 才允许清空历史
	if out, err := run("plan", "apply", bp2, "--yes", "--reset"); err != nil {
		t.Fatalf("apply --reset: %v\n%s", err, out)
	}
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		var pid string
		_ = tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&pid)
		all, err := db.ListTasks(tx, pid)
		if err != nil {
			return err
		}
		by := map[string]*db.Task{}
		for _, tsk := range all {
			by[tsk.ID] = tsk
		}
		if _, ok := by["T-DONE"]; ok {
			t.Fatal("--reset 后不应再有 T-DONE（未在蓝图）")
		}
		if by["T-WIP"] == nil || by["T-WIP"].Status != "ready" && by["T-WIP"].Status != "backlog" {
			// reset 后按蓝图重插为 backlog，再 promote → ready
			if by["T-WIP"] == nil {
				t.Fatal("T-WIP 在蓝图中应存在")
			}
			if by["T-WIP"].Status == "in_progress" {
				t.Fatal("--reset 应抹掉 in_progress")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestPlanApplySyncsMainBranch：蓝图 meta.main_branch 应写入 config 与 projects 表。
func TestPlanApplySyncsMainBranch(t *testing.T) {
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
	_ = os.MkdirAll(filepath.Join(repo, "mod_a"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "mod_a", ".gitkeep"), nil, 0o644)
	git("add", ".")
	git("commit", "-m", "init")
	git("branch", "dev")

	if out, err := run("init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	cfgPath := filepath.Join(repo, ".multiagent", "config.toml")
	cfg0, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg0.Project.MainBranch != "main" {
		t.Fatalf("init MainBranch=%q want main", cfg0.Project.MainBranch)
	}

	// 无 meta.main_branch：不改写 config
	bpLegacy := filepath.Join(repo, "bp-legacy.toml")
	_ = os.WriteFile(bpLegacy, []byte(`
[meta]
name = "legacy"
version = 1

[[task]]
id = "T-LEG"
title = "legacy"
priority = 10
write_scope = ["mod_a/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 10
`), 0o644)
	if out, err := run("plan", "apply", bpLegacy, "--yes"); err != nil {
		t.Fatalf("apply legacy: %v\n%s", err, out)
	}
	cfg1, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg1.Project.MainBranch != "main" {
		t.Fatalf("legacy apply should keep main, got %q", cfg1.Project.MainBranch)
	}

	bpDev := filepath.Join(repo, "bp-dev.toml")
	_ = os.WriteFile(bpDev, []byte(`
[meta]
name = "on-dev"
version = 2
main_branch = "dev"

[[task]]
id = "T-DEV"
title = "on dev trunk"
priority = 10
write_scope = ["mod_a/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 10
`), 0o644)
	out, err := run("plan", "apply", bpDev, "--yes")
	if err != nil {
		t.Fatalf("apply dev: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"main_branch"`) && !strings.Contains(out, "main_branch") {
		// JSON 输出应含 main_branch；纯文本路径也至少 config 已更新
		t.Logf("apply output:\n%s", out)
	}
	cfg2, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Project.MainBranch != "dev" {
		t.Fatalf("config MainBranch=%q want dev", cfg2.Project.MainBranch)
	}

	gdb := filepath.Join(globalDir, "maw.db")
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		var mb string
		if err := tx.QueryRow(`SELECT main_branch FROM projects LIMIT 1`).Scan(&mb); err != nil {
			return err
		}
		if mb != "dev" {
			t.Fatalf("db main_branch=%q want dev", mb)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
