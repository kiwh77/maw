package home

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T, dir string) {
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

func samePath(a, b string) bool {
	return CanonicalRepoPath(a) == CanonicalRepoPath(b)
}

func TestRepoRootAndMawHome(t *testing.T) {
	g := t.TempDir()
	t.Setenv("MAW_GLOBAL", g)
	t.Setenv("MAW_HOME", "")

	repo := t.TempDir()
	gitInit(t, repo)
	root, err := RepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(root, repo) {
		t.Fatalf("root=%s want %s", root, repo)
	}
	h, err := MawHome(repo)
	if err != nil {
		t.Fatal(err)
	}
	// ProjectHome 必须以 git 解析后的 root 为准（macOS /var vs /private/var）
	want, err := ProjectHome(root)
	if err != nil {
		t.Fatal(err)
	}
	if h != want {
		t.Fatalf("home=%s want %s", h, want)
	}
	expect := filepath.Join(g, ProjectsDir, ProjectIDFromPath(root))
	if h != expect && !samePath(h, expect) {
		t.Fatalf("expected under %s/projects, got %s", g, h)
	}
	if _, err := RequireHome(repo); err == nil {
		t.Fatal("RequireHome should fail before attach dir exists")
	}
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := RequireHome(repo)
	if err != nil {
		t.Fatalf("RequireHome: %v", err)
	}
	if got != want {
		t.Fatalf("RequireHome=%s want %s", got, want)
	}
}

func TestMawHomeIgnoresInRepoMultiagent(t *testing.T) {
	g := t.TempDir()
	t.Setenv("MAW_GLOBAL", g)
	t.Setenv("MAW_HOME", "")

	repo := t.TempDir()
	gitInit(t, repo)
	inRepo := filepath.Join(repo, MultiagentDir)
	if err := os.MkdirAll(inRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	h, err := MawHome(repo)
	if err != nil {
		t.Fatal(err)
	}
	if samePath(h, inRepo) {
		t.Fatal("MawHome must not use in-repo .multiagent")
	}
	want, _ := ProjectHome(repo)
	if !samePath(h, want) {
		t.Fatalf("home=%s want %s", h, want)
	}
}

func TestMAW_HOMEOverride(t *testing.T) {
	g := t.TempDir()
	t.Setenv("MAW_GLOBAL", g)
	repo := t.TempDir()
	gitInit(t, repo)
	custom := filepath.Join(t.TempDir(), "custom-home")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAW_HOME", custom)
	root, err := RepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(root, repo) {
		t.Fatalf("RepoRoot must stay git root, got %s want %s", root, repo)
	}
	h, err := MawHome(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(h, custom) {
		t.Fatalf("MawHome=%s want %s", h, custom)
	}
}

func TestGlobalDirAndDBPath(t *testing.T) {
	g := t.TempDir()
	t.Setenv("MAW_GLOBAL", g)
	dir, err := GlobalDir()
	if err != nil || !samePath(dir, g) {
		t.Fatalf("GlobalDir=%s %v", dir, err)
	}
	dbp, err := GlobalDBPath()
	if err != nil || !samePath(dbp, filepath.Join(g, DBName)) {
		t.Fatalf("db=%s %v", dbp, err)
	}
}

func TestProjectIDFromPathStable(t *testing.T) {
	a := ProjectIDFromPath("/tmp/foo")
	b := ProjectIDFromPath("/tmp/foo")
	c := ProjectIDFromPath("/tmp/bar")
	if a != b || len(a) != 16 {
		t.Fatalf("a=%s b=%s", a, b)
	}
	if a == c {
		t.Fatal("different paths must differ")
	}
}

func TestPathHelpers(t *testing.T) {
	h := "/tmp/maw-projects/abc"
	if ConfigPath(h) != filepath.Join(h, ConfigName) {
		t.Fatal("config")
	}
	if BlueprintPath(h) != filepath.Join(h, BlueprintName) {
		t.Fatal("blueprint")
	}
	if ProjectMetaPath(h) != filepath.Join(h, ProjectFile) {
		t.Fatal("project meta")
	}
	if LegacyLocalDBPath(h) != filepath.Join(h, DBName) {
		t.Fatal("legacy db")
	}
}

func TestWorktreeRoot(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	top, err := WorktreeRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(top, repo) {
		t.Fatalf("top=%s", top)
	}
}

func TestRepoRootNotGit(t *testing.T) {
	t.Setenv("MAW_HOME", "")
	dir := t.TempDir()
	if _, err := RepoRoot(dir); err == nil {
		t.Fatal("expected error outside git")
	}
}
