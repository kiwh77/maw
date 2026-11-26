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
	// 隔离合入后主检出工作区可能尚未刷新出文件；以主干树为准。
	out, err := exec.Command("git", "-C", repo, "ls-tree", "-r", "--name-only", "main").CombinedOutput()
	if err != nil {
		t.Fatalf("ls-tree: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "feat.txt") {
		t.Fatalf("feat.txt not on main ref: %s", out)
	}
	// empty squash should fail
	if err := SquashMerge(repo, "agent/w1/T-1", "main", "again"); err == nil {
		t.Fatal("expected empty squash error")
	}
}

func TestSquashMergeIsolatedIgnoresDirtyMainCheckout(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(repo, ".worktrees", "w2")
	if err := CreateWorktree(repo, wt, "agent/w2/T-2", "main"); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(wt, "ok.txt"), []byte("ok\n"), 0o644)
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(wt, "add", "ok.txt")
	run(wt, "commit", "-m", "ok")

	// 主检出留无关脏文件
	_ = os.WriteFile(filepath.Join(repo, "WIP.md"), []byte("human wip\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\ndirty\n"), 0o644)
	if err := AssertWorkingTreeClean(repo); err == nil {
		t.Fatal("expected dirty main checkout")
	}
	tip, err := SquashMergeIsolated(repo, "agent/w2/T-2", "main", "merge T-2")
	if err != nil {
		t.Fatalf("isolated merge: %v", err)
	}
	if tip == "" {
		t.Fatal("empty tip")
	}
	// 脏文件仍在
	if _, err := os.Stat(filepath.Join(repo, "WIP.md")); err != nil {
		t.Fatal("WIP should remain")
	}
	out, _ := exec.Command("git", "-C", repo, "ls-tree", "-r", "--name-only", "main").CombinedOutput()
	if !strings.Contains(string(out), "ok.txt") {
		t.Fatalf("ok.txt missing on main: %s", out)
	}
}

func TestBranchHasUniqueChanges(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(repo, ".worktrees", "w3")
	if err := CreateWorktree(repo, wt, "agent/w3/T-3", "main"); err != nil {
		t.Fatal(err)
	}
	ok, err := BranchHasUniqueChanges(wt, "main")
	if err != nil || ok {
		t.Fatalf("expected no unique changes, ok=%v err=%v", ok, err)
	}
	_ = os.WriteFile(filepath.Join(wt, "x.txt"), []byte("x\n"), 0o644)
	c := exec.Command("git", "add", "x.txt")
	c.Dir = wt
	_ = c.Run()
	c = exec.Command("git", "commit", "-m", "x")
	c.Dir = wt
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	ok, err = BranchHasUniqueChanges(wt, "main")
	if err != nil || !ok {
		t.Fatalf("expected unique changes, ok=%v err=%v", ok, err)
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

func TestCleanupMergedTaskRemoveWorktree(t *testing.T) {
	repo := initRepo(t)
	wtRel := filepath.Join(".worktrees", "drop")
	wt := filepath.Join(repo, wtRel)
	branch := "agent/drop/T-1"
	if err := CreateWorktree(repo, wt, branch, "main"); err != nil {
		t.Fatal(err)
	}
	CleanupMergedTask(repo, wtRel, branch, false)
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed: %v", err)
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
