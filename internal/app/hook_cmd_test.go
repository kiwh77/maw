package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/gitops"
)

func setupHookRepo(t *testing.T) string {
	t.Helper()
	global := t.TempDir()
	t.Setenv("MAW_GLOBAL", global)
	t.Setenv("MAW_LANG", "en")
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	if _, err := Init(repo, false, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	return repo
}

func TestHookPreCommitAllowsTrunkWithoutAgent(t *testing.T) {
	repo := setupHookRepo(t)
	t.Setenv("MAW_AGENT", "")
	os.Unsetenv(gitops.MergeEnv)

	if _, err := HookPreCommit(repo); err != nil {
		t.Fatalf("human trunk commit should be allowed after init, got %v", err)
	}
}

func TestHookPreCommitBlocksTrunkWhenAgentSet(t *testing.T) {
	repo := setupHookRepo(t)
	t.Setenv("MAW_AGENT", "cursor-goalagents-1")
	os.Unsetenv(gitops.MergeEnv)

	if _, err := HookPreCommit(repo); err == nil {
		t.Fatal("expected trunk commit blocked when MAW_AGENT is set")
	}
}

func TestHookPreCommitChecksWriteScopeInTaskWorktree(t *testing.T) {
	repo := setupHookRepo(t)
	t.Setenv("MAW_AGENT", "")
	os.Unsetenv(gitops.MergeEnv)

	markerDir := filepath.Join(repo, ".maw")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskJSON := `{"id":"T-1","write_scope":["internal/app/"]}`
	if err := os.WriteFile(filepath.Join(markerDir, "task.json"), []byte(taskJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("out of scope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("git", "add", "README.md")
	c.Dir = repo
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	if _, err := HookPreCommit(repo); err == nil {
		t.Fatal("expected out-of-scope staged file to fail pre-commit")
	}
}
