// Package testsupport 为 resilience／managed 验收提供隔离工程与受控进程辅助。
package testsupport

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/db"
)

// Harness 是临时 Git 仓 + MAW_GLOBAL。
type Harness struct {
	T      *testing.T
	Repo   string
	Global string
	GDB    string
	PID    string
}

// DefaultTasks 最小双题蓝图。
const DefaultTasks = `
[meta]
name = "resilience"
version = 1
main_branch = "main"

[[task]]
id = "VN-R1"
title = "resilience primary"
priority = 1
deps = []
write_scope = ["mod_a/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 1

[[task]]
id = "VN-R2"
title = "resilience secondary"
priority = 2
deps = []
write_scope = ["mod_b/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 2
`

// NewHarness 初始化可领题的隔离工程。
func NewHarness(t *testing.T, tasksTOML string) *Harness {
	t.Helper()
	global := t.TempDir()
	t.Setenv("MAW_GLOBAL", global)
	t.Setenv("MAW_LANG", "en")
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# r\n"), 0o644)
	for _, mod := range []string{"mod_a", "mod_b"} {
		_ = os.MkdirAll(filepath.Join(repo, mod), 0o755)
		_ = os.WriteFile(filepath.Join(repo, mod, ".gitkeep"), nil, 0o644)
	}
	git("add", ".")
	git("commit", "-m", "init")
	if _, err := app.Init(repo, false, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	if tasksTOML == "" {
		tasksTOML = DefaultTasks
	}
	bp := filepath.Join(repo, "bp.toml")
	_ = os.WriteFile(bp, []byte(tasksTOML), 0o644)
	if _, err := app.PlanApply(bp, repo, true, false, false); err != nil {
		t.Fatalf("plan apply: %v", err)
	}
	res, err := app.Init(repo, false, false)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := res["project_id"].(string)
	gdb, _ := res["global_db"].(string)
	if gdb == "" {
		gdb = filepath.Join(global, "maw.db")
	}
	if pid == "" {
		_ = db.WithTx(gdb, func(tx *sql.Tx) error {
			ps, e := db.ListProjects(tx)
			if e != nil || len(ps) == 0 {
				return e
			}
			pid = ps[0].ID
			return nil
		})
	}
	return &Harness{T: t, Repo: repo, Global: global, GDB: gdb, PID: pid}
}
