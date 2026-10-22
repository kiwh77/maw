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
	resolve := func(p string) string {
		if p == "" {
			return p
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Clean(r)
		}
		// Path may not exist yet — resolve existing parents.
		dir, base := filepath.Split(filepath.Clean(p))
		for dir != "" && dir != string(filepath.Separator) {
			if r, err := filepath.EvalSymlinks(dir); err == nil {
				return filepath.Clean(filepath.Join(r, base))
			}
			parent := filepath.Dir(dir)
			base = filepath.Join(filepath.Base(dir), base)
			if parent == dir {
				break
			}
			dir = parent + string(filepath.Separator)
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

func TestRepoRootAndMawHome(t *testing.T) {
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
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
	want := filepath.Join(repo, MultiagentDir)
	if !samePath(h, want) {
		t.Fatalf("home=%s want %s", h, want)
	}
	if _, err := RequireHome(repo); err == nil {
		t.Fatal("RequireHome should fail before init dir exists")
	}
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := RequireHome(repo); err != nil || !samePath(got, want) {
		t.Fatalf("RequireHome=%s %v", got, err)
	}
}

func TestMAW_HOMEOverride(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	custom := filepath.Join(t.TempDir(), "custom-home")
	if err := os.MkdirAll(filepath.Join(custom, MultiagentDir), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAW_HOME", custom)
	root, err := RepoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(root, custom) {
		t.Fatalf("RepoRoot with MAW_HOME=%s want %s", root, custom)
	}
	h, err := MawHome(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(h, filepath.Join(custom, MultiagentDir)) {
		t.Fatalf("MawHome=%s", h)
	}
}

func TestMAW_HOMEPointsAtMultiagent(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	ma := filepath.Join(repo, MultiagentDir)
	if err := os.MkdirAll(ma, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAW_HOME", ma)
	root, err := RepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(root, repo) {
		t.Fatalf("root=%s want %s", root, repo)
	}
	h, err := MawHome(".")
	if err != nil || !samePath(h, ma) {
		t.Fatalf("home=%s %v", h, err)
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
	h := "/repo/.multiagent"
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
