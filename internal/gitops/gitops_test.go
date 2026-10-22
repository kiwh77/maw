package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssertWorkingTreeCleanIgnoresGitignore(t *testing.T) {
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
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")

	// simulate maw init appending entries
	_ = os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n.multiagent/\n.worktrees/\n"), 0o644)
	if err := AssertWorkingTreeClean(repo); err != nil {
		t.Fatalf("gitignore-only dirty should be ignored: %v", err)
	}

	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("dirty\n"), 0o644)
	err := AssertWorkingTreeClean(repo)
	if err == nil {
		t.Fatal("README dirty should fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "working tree dirty") || !strings.Contains(msg, "git commit") {
		t.Fatalf("dirty error should hint commit, got: %s", msg)
	}
}
