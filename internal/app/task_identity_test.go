package app

import (
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// TestPlanApplyRetiresIdleAndRebuildGetsNewInstance：
// 蓝图外 idle 退役；同显示号重建不得串到旧 instance_id。
func TestPlanApplyRetiresIdleAndRebuildGetsNewInstance(t *testing.T) {
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

	if out, err := run("init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	bp1 := filepath.Join(repo, "bp1.toml")
	_ = os.WriteFile(bp1, []byte(`
[meta]
name = "wave1"
version = 1

[[task]]
id = "VN-100"
title = "first life"
priority = 10
write_scope = ["mod_a/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 10
`), 0o644)
	if out, err := run("plan", "apply", bp1, "--yes"); err != nil {
		t.Fatalf("apply1: %v\n%s", err, out)
	}

	gdb := filepath.Join(globalDir, "maw.db")
	var firstInstance, projectID string
	if err := db.WithReadTx(gdb, func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&projectID); err != nil {
			return err
		}
		task, err := db.GetTask(tx, projectID, "VN-100")
		if err != nil || task == nil {
			t.Fatalf("missing VN-100: %v %#v", err, task)
		}
		firstInstance = task.InstanceID
		if firstInstance == "" {
			t.Fatal("首次 apply 应绑定 instance_id")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	bp2 := filepath.Join(repo, "bp2.toml")
	_ = os.WriteFile(bp2, []byte(`
[meta]
name = "wave2"
version = 2

[[task]]
id = "VN-200"
title = "other"
priority = 10
write_scope = ["README.md"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 10
`), 0o644)
	if out, err := run("plan", "apply", bp2, "--yes"); err != nil {
		t.Fatalf("apply2: %v\n%s", err, out)
	}

	if err := db.WithReadTx(gdb, func(tx *sql.Tx) error {
		gone, err := db.GetTask(tx, projectID, "VN-100")
		if err != nil {
			return err
		}
		if gone != nil {
			t.Fatalf("VN-100 应已退役释放别名，仍有 %#v", gone)
		}
		inst, err := db.GetInstance(tx, firstInstance)
		if err != nil {
			return err
		}
		if inst == nil || inst.State != model.InstanceStateRetired {
			t.Fatalf("旧实例应退役: %+v", inst)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	bp3 := filepath.Join(repo, "bp3.toml")
	_ = os.WriteFile(bp3, []byte(`
[meta]
name = "wave3"
version = 3

[[task]]
id = "VN-100"
title = "second life"
priority = 10
write_scope = ["mod_a/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 10

[[task]]
id = "VN-200"
title = "other"
priority = 10
write_scope = ["README.md"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 20
`), 0o644)
	if out, err := run("plan", "apply", bp3, "--yes"); err != nil {
		t.Fatalf("apply3: %v\n%s", err, out)
	}

	if err := db.WithReadTx(gdb, func(tx *sql.Tx) error {
		task, err := db.GetTask(tx, projectID, "VN-100")
		if err != nil || task == nil {
			t.Fatalf("重建后应有 VN-100: %v %#v", err, task)
		}
		if task.InstanceID == "" || task.InstanceID == firstInstance {
			t.Fatalf("重建同显示号必须换 instance_id，got %q want != %q", task.InstanceID, firstInstance)
		}
		hist, err := db.DisplayHistory(tx, projectID, "VN-100")
		if err != nil {
			return err
		}
		if len(hist) != 2 {
			t.Fatalf("编号历史应有 2 条，得到 %d", len(hist))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestPlanApplyFreezesDefinitionChange：保留态题改标题应追加版本，不换 instance_id。
func TestPlanApplyFreezesDefinitionChange(t *testing.T) {
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
	if out, err := run("init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	bp1 := filepath.Join(repo, "bp1.toml")
	_ = os.WriteFile(bp1, []byte(`
[meta]
name = "v1"
version = 1

[[task]]
id = "VN-010"
title = "original"
priority = 10
write_scope = ["mod_a/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 10
`), 0o644)
	if out, err := run("plan", "apply", bp1, "--yes"); err != nil {
		t.Fatalf("apply1: %v\n%s", err, out)
	}

	gdb := filepath.Join(globalDir, "maw.db")
	var instanceID, projectID string
	if err := db.WithTx(gdb, func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT id FROM projects LIMIT 1`).Scan(&projectID); err != nil {
			return err
		}
		task, err := db.GetTask(tx, projectID, "VN-010")
		if err != nil || task == nil {
			return err
		}
		instanceID = task.InstanceID
		task.Status = "done"
		return db.UpsertTask(tx, task)
	}); err != nil {
		t.Fatal(err)
	}

	bp2 := filepath.Join(repo, "bp2.toml")
	_ = os.WriteFile(bp2, []byte(`
[meta]
name = "v2"
version = 2

[[task]]
id = "VN-010"
title = "retitled"
priority = 10
write_scope = ["mod_a/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 10
`), 0o644)
	if out, err := run("plan", "apply", bp2, "--yes"); err != nil {
		t.Fatalf("apply2: %v\n%s", err, out)
	}

	if err := db.WithReadTx(gdb, func(tx *sql.Tx) error {
		task, err := db.GetTask(tx, projectID, "VN-010")
		if err != nil || task == nil {
			t.Fatalf("missing: %v", err)
		}
		if task.InstanceID != instanceID {
			t.Fatalf("保留态不得换身份: %q → %q", instanceID, task.InstanceID)
		}
		if task.Status != "done" || task.Title != "retitled" {
			t.Fatalf("应保留 done 并刷新标题: %#v", task)
		}
		inst, err := db.GetInstance(tx, instanceID)
		if err != nil {
			return err
		}
		if len(inst.Versions) != 2 {
			t.Fatalf("改标题应冻结 v2，得到 %d 版", len(inst.Versions))
		}
		if inst.Versions[1].Title != "retitled" {
			t.Fatalf("v2 title=%q", inst.Versions[1].Title)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestLoadBoardSnapshotDoesNotMigrate：只读查询不得推进 schema。
func TestLoadBoardSnapshotDoesNotMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maw.db")
	s, err := db.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	_, err = loadBoardSnapshot(path, "", "", "", 4)
	if err == nil {
		t.Fatal("未迁移库的只读查询应失败")
	}

	st, serr := schemaStatusNoMigrate(path)
	if serr != nil {
		t.Fatalf("查询失败原因=%v；检查 schema 失败: %v", err, serr)
	}
	if st.Current == db.LatestVersion() && len(st.Pending) == 0 {
		t.Fatalf("loadBoardSnapshot 不得隐式 Migrate；原错误=%v", err)
	}
	if !errors.Is(err, db.ErrNotMigrated) {
		// 允许包装错误，但 schema 必须仍落后。
		t.Logf("只读失败（可包装）: %v", err)
	}
}

func schemaStatusNoMigrate(path string) (db.SchemaStatus, error) {
	s, err := db.OpenStore(path)
	if err != nil {
		return db.SchemaStatus{}, err
	}
	defer s.Close()
	return s.SchemaStatus()
}
