package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wuhao/goal-agents/internal/errx"
	"github.com/wuhao/goal-agents/internal/home"
)

const MergeEnv = "MAW_INTERNAL_MERGE"

func CurrentBranch(cwd string) (string, error) {
	return home.Git(cwd, "rev-parse", "--abbrev-ref", "HEAD")
}

func EnsureBranchBase(repo, mainBranch string) (string, error) {
	for _, ref := range []string{"origin/" + mainBranch, mainBranch} {
		if _, err := home.Git(repo, "rev-parse", "--verify", ref); err == nil {
			return ref, nil
		}
	}
	return "", errx.New("main branch "+mainBranch+" not found", "create the branch or set project.main_branch")
}

func CreateWorktree(repo, worktree, branch, base string) error {
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		return err
	}
	if st, err := os.Stat(worktree); err == nil && st.IsDir() {
		return nil
	}
	if _, err := home.Git(repo, "worktree", "add", "-b", branch, worktree, base); err == nil {
		return nil
	}
	if _, err := home.Git(repo, "worktree", "add", worktree, branch); err != nil {
		return errx.New("failed to create worktree: "+err.Error(), "run `git worktree list` and clean stale entries")
	}
	return nil
}

func ChangedFiles(cwd, mainBranch string) ([]string, error) {
	base, err := EnsureBranchBase(cwd, mainBranch)
	if err != nil {
		return nil, err
	}
	baseRef := base
	if mb, err := home.Git(cwd, "merge-base", base, "HEAD"); err == nil && mb != "" {
		baseRef = mb
	}
	out, _ := home.Git(cwd, "diff", "--name-only", baseRef+"...HEAD")
	seen := map[string]bool{}
	var files []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || seen[ln] {
			continue
		}
		seen[ln] = true
		files = append(files, ln)
	}
	st, _ := home.Git(cwd, "status", "--porcelain")
	for _, ln := range strings.Split(st, "\n") {
		if len(ln) < 4 {
			continue
		}
		p := strings.TrimSpace(ln[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		files = append(files, p)
	}
	return files, nil
}

func SquashMerge(repo, branch, mainBranch, message string) error {
	if _, err := home.Git(repo, "checkout", mainBranch); err != nil {
		return errx.Wrap("checkout main failed", "ensure main worktree is clean", err)
	}
	if out, err := home.Git(repo, "merge", "--squash", branch); err != nil {
		_, _ = home.Git(repo, "merge", "--abort")
		return errx.New("squash merge conflict for "+branch+": "+out, "inspect conflict files; maw will create a conflict task")
	}
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = repo
	if err := cmd.Run(); err == nil {
		return nil // nothing staged
	}
	c := exec.Command("git", "commit", "-m", message)
	c.Dir = repo
	c.Env = append(os.Environ(), MergeEnv+"=1")
	out, err := c.CombinedOutput()
	if err != nil {
		return errx.New("merge commit failed: "+string(out), "inspect git status on main")
	}
	return nil
}

func RebaseOntoMain(worktree, mainBranch string) error {
	base, err := EnsureBranchBase(worktree, mainBranch)
	if err != nil {
		return err
	}
	if out, err := home.Git(worktree, "rebase", base); err != nil {
		_, _ = home.Git(worktree, "rebase", "--abort")
		return errx.New("rebase failed: "+out, "resolve conflicts in a conflict task; do not merge manually")
	}
	return nil
}

func RunGates(commands []string, cwd string, timeoutSec int) error {
	if timeoutSec <= 0 {
		timeoutSec = 900
	}
	for _, cmdStr := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
		c := exec.CommandContext(ctx, "sh", "-c", cmdStr)
		c.Dir = cwd
		out, err := c.CombinedOutput()
		cancel()
		if ctx.Err() == context.DeadlineExceeded {
			return errx.New("gate timed out: "+cmdStr, "raise gates.timeout_sec or fix tests")
		}
		if err != nil {
			return errx.New("gate failed: "+cmdStr+"\n"+string(out), "fix failures then `maw submit` again")
		}
	}
	return nil
}
