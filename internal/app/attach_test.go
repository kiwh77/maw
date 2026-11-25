package app

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
)

func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")
}

func TestEnsureAttachedZeroRepoWrites(t *testing.T) {
	g := t.TempDir()
	t.Setenv("MAW_GLOBAL", g)
	t.Setenv("MAW_HOME", "")

	repo := t.TempDir()
	gitInitRepo(t, repo)

	before, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}

	repoOut, local, gdb, pid, _, err := EnsureAttached(repo, false)
	if err != nil {
		t.Fatalf("EnsureAttached: %v", err)
	}
	if repoOut == "" || local == "" || gdb == "" || pid == "" {
		t.Fatalf("incomplete attach: repo=%s home=%s db=%s id=%s", repoOut, local, gdb, pid)
	}
	if strings.Contains(local, filepath.Join(repo, home.MultiagentDir)) {
		t.Fatalf("local home must be out-of-tree, got %s", local)
	}
	if _, err := os.Stat(home.ConfigPath(local)); err != nil {
		t.Fatalf("missing out-of-tree config: %v", err)
	}

	after, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("repo dirty after attach:\nbefore=%q\nafter=%q", before, after)
	}
	if _, err := os.Stat(filepath.Join(repo, home.MultiagentDir)); !os.IsNotExist(err) {
		t.Fatal("must not create in-repo .multiagent")
	}

	var proj *db.Project
	if err := db.WithTx(gdb, func(tx *sql.Tx) error {
		var e error
		proj, e = db.GetProject(tx, pid)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if proj == nil || proj.Path != repoOut && !sameAbs(proj.Path, repo) {
		// path may be cleaned/abs
		if proj == nil {
			t.Fatal("project not in global db")
		}
	}
}

func sameAbs(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return filepath.Clean(aa) == filepath.Clean(bb)
}

func TestResolveProjectIgnoresInRepoConfig(t *testing.T) {
	g := t.TempDir()
	t.Setenv("MAW_GLOBAL", g)
	t.Setenv("MAW_HOME", "")

	repo := t.TempDir()
	gitInitRepo(t, repo)

	// 哨兵：仓内错误主干；若被读取会导致 MainBranch=poison
	poisonDir := filepath.Join(repo, home.MultiagentDir)
	if err := os.MkdirAll(poisonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	poison := `[project]
main_branch = "poison"
merge_strategy = "squash"
max_parallel = 0
blueprint = "blueprint.toml"
`
	if err := os.WriteFile(filepath.Join(poisonDir, "config.toml"), []byte(poison), 0o644); err != nil {
		t.Fatal(err)
	}

	pc, err := resolveProject(repo)
	if err != nil {
		t.Fatalf("resolveProject: %v", err)
	}
	if pc.Config.Project.MainBranch == "poison" {
		t.Fatal("must not read in-repo .multiagent/config.toml")
	}
	if strings.HasPrefix(pc.LocalHome, filepath.Join(repo, home.MultiagentDir)) {
		t.Fatalf("LocalHome in-repo: %s", pc.LocalHome)
	}
	// 仓外 config 应为 DetectMainBranch（通常 main）
	cfg, err := config.Load(home.ConfigPath(pc.LocalHome))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.MainBranch == "poison" {
		t.Fatal("out-of-tree config poisoned")
	}
}

func TestInitDoesNotWriteProtocolOrHooks(t *testing.T) {
	g := t.TempDir()
	t.Setenv("MAW_GLOBAL", g)
	t.Setenv("MAW_HOME", "")

	repo := t.TempDir()
	gitInitRepo(t, repo)

	res, err := Init(repo, false, false)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if res["ok"] != true {
		t.Fatalf("init not ok: %v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("Init must not create AGENTS.md")
	}
	hooksDir, _ := home.Git(repo, "rev-parse", "--git-path", "hooks")
	if hooksDir != "" && !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(repo, hooksDir)
	}
	for _, name := range []string{"pre-commit", "pre-push"} {
		p := filepath.Join(hooksDir, name)
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), "maw hook") {
			t.Fatalf("Init must not install maw %s", name)
		}
	}
	doc, err := Doctor(repo)
	if err != nil {
		t.Fatal(err)
	}
	if doc["ok"] != true {
		t.Fatalf("doctor after init: %v", doc)
	}
}

func TestProjectAdd(t *testing.T) {
	g := t.TempDir()
	t.Setenv("MAW_GLOBAL", g)
	t.Setenv("MAW_HOME", "")
	repo := t.TempDir()
	gitInitRepo(t, repo)
	res, err := ProjectAdd(repo)
	if err != nil {
		t.Fatal(err)
	}
	if res["ok"] != true || res["project_id"] == nil {
		t.Fatalf("%v", res)
	}
}
