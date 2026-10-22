package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
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
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")
	return repo
}

func TestCreateAndEnsureAgentWorktree(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(repo, ".worktrees", "cursor-demo-1")
	if err := CreateWorktree(repo, wt, "agent/cursor-demo-1/T-1", "main"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		t.Fatal("worktree missing")
	}
	// second create is no-op
	if err := CreateWorktree(repo, wt, "agent/cursor-demo-1/T-1", "main"); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(wt, "extra.txt"), []byte("x\n"), 0o644)
	if err := EnsureAgentWorktree(repo, wt, "agent/cursor-demo-1/T-2", "main"); err != nil {
		t.Fatal(err)
	}
	br, err := CurrentBranch(wt)
	if err != nil || br != "agent/cursor-demo-1/T-2" {
		t.Fatalf("branch=%s %v", br, err)
	}
}

func TestChangedFilesAndSquashMerge(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(repo, ".worktrees", "w1")
	if err := CreateWorktree(repo, wt, "agent/w1/T-1", "main"); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(wt, "feat.txt"), []byte("feat\n"), 0o644)
	c := exec.Command("git", "add", "feat.txt")
	c.Dir = wt
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	c = exec.Command("git", "commit", "-m", "feat")
	c.Dir = wt
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	files, err := ChangedFiles(wt, "main")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f == "feat.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("changed=%v", files)
	}
	if err := SquashMerge(repo, "agent/w1/T-1", "main", "merge T-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "feat.txt")); err != nil {
		t.Fatal("feat.txt not on main")
	}
	// empty squash should fail
	if err := SquashMerge(repo, "agent/w1/T-1", "main", "again"); err == nil {
		t.Fatal("expected empty squash error")
	}
}

func TestCleanupMergedTaskKeepWorktree(t *testing.T) {
	repo := initRepo(t)
	wtRel := filepath.Join(".worktrees", "keep")
	wt := filepath.Join(repo, wtRel)
	branch := "agent/keep/T-1"
	if err := CreateWorktree(repo, wt, branch, "main"); err != nil {
		t.Fatal(err)
	}
	CleanupMergedTask(repo, wtRel, branch, true)
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		t.Fatal("worktree should remain")
	}
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", branch).CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("branch should be deleted: %s", out)
	}
}

func TestIsEnvFailure(t *testing.T) {
	if !IsEnvFailure("command not found: foo") {
		t.Fatal("expected env failure")
	}
	if IsEnvFailure("assertion failed") {
		t.Fatal("not env")
	}
}

func TestRunGates(t *testing.T) {
	dir := t.TempDir()
	if err := RunGates([]string{"true"}, dir, 5); err != nil {
		t.Fatal(err)
	}
	if err := RunGates([]string{"false"}, dir, 5); err == nil {
		t.Fatal("expected gate fail")
	}
}
