package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wuhao/maw/internal/errx"
	"github.com/wuhao/maw/internal/home"
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
	return "", errx.New("trunk branch "+mainBranch+" not found", "create the branch or set meta.main_branch / project.main_branch")
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

// EnsureAgentWorktree creates or reuses a per-agent persistent worktree.
// When the directory already exists it resets tracked files to base and
// recreates branch with checkout -B. Ignored files (e.g. node_modules) are kept.
func EnsureAgentWorktree(repo, worktree, branch, base string) error {
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		return err
	}
	st, err := os.Stat(worktree)
	if err != nil || !st.IsDir() {
		return CreateWorktree(repo, worktree, branch, base)
	}
	// Best-effort refresh of remote tips; ignore failure (offline / no remote).
	_, _ = home.Git(repo, "fetch", "--all", "--prune")
	if out, err := home.Git(worktree, "reset", "--hard", base); err != nil {
		return errx.New("failed to reset agent worktree to "+base+": "+out, "inspect dirty state in "+worktree)
	}
	if out, err := home.Git(worktree, "checkout", "-B", branch, base); err != nil {
		return errx.New("failed to recreate branch "+branch+": "+out, "run `git worktree list` and clean stale branches")
	}
	return nil
}

// RemoveWorktree best-effort cleans a worktree directory and git worktree registration.
func RemoveWorktree(repo, worktree string) {
	if worktree == "" {
		return
	}
	_, _ = home.Git(repo, "worktree", "remove", "--force", worktree)
	_ = os.RemoveAll(worktree)
	_, _ = home.Git(repo, "worktree", "prune")
}

// DeleteBranch best-effort deletes a local branch (force). Skips empty or current branch names.
func DeleteBranch(repo, branch string) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return
	}
	if cur, err := CurrentBranch(repo); err == nil && cur == branch {
		return
	}
	_, _ = home.Git(repo, "branch", "-D", branch)
}

// DetachWorktree moves a worktree off its current branch so the branch can be deleted.
func DetachWorktree(worktree string) error {
	if worktree == "" {
		return nil
	}
	if st, err := os.Stat(worktree); err != nil || !st.IsDir() {
		return nil
	}
	if out, err := home.Git(worktree, "checkout", "--detach"); err != nil {
		return errx.New("failed to detach worktree: "+out, "cd "+worktree+" and checkout another ref before deleting the branch")
	}
	return nil
}

// CleanupMergedTask cleans up after a successful merge.
// When keepWorktree is true (per-agent mode), the worktree directory is retained
// (detached) so ignored deps survive; only the agent task branch is deleted.
// When keepWorktree is false, the worktree is removed entirely.
func CleanupMergedTask(repo, worktreeRel, branch string, keepWorktree bool) {
	wt := worktreeRel
	if wt != "" && !filepath.IsAbs(wt) {
		wt = filepath.Join(repo, wt)
	}
	if keepWorktree {
		_ = DetachWorktree(wt)
		DeleteBranch(repo, branch)
		return
	}
	RemoveWorktree(repo, wt)
	DeleteBranch(repo, branch)
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
		// squash 不留 MERGE_HEAD；清掉可能的空暂存即可
		_, _ = home.Git(repo, "reset", "--mixed", "HEAD")
		return errx.New("squash merge produced no changes for "+branch, "ensure the task branch has commits vs main; empty merge is rejected")
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

// AssertWorkingTreeClean fails if cwd has staged/unstaged changes to tracked files.
// Untracked files are ignored. Maw meta (.gitignore 仅因 init 追加 .multiagent/.worktrees、
// 以及 .multiagent/**) 不视为阻塞合入的脏状态。
func AssertWorkingTreeClean(cwd string) error {
	// 不用 home.Git：它对 CombinedOutput 做 TrimSpace，会吃掉 porcelain 首行 X 位空格。
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
	cmd.Dir = cwd
	rawOut, err := cmd.Output()
	if err != nil {
		return errx.Wrap("git status failed", "inspect "+cwd, err)
	}
	var dirty []string
	for _, raw := range strings.Split(string(rawOut), "\n") {
		// porcelain: XY␠path —— 勿 TrimSpace 整行，否则会吃掉 X 位的空格
		ln := strings.TrimRight(raw, "\r\n")
		if ln == "" || len(ln) < 4 {
			continue
		}
		p := strings.TrimSpace(ln[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		p = strings.Trim(p, "\"")
		if p == ".gitignore" || strings.HasPrefix(p, ".multiagent/") {
			continue
		}
		dirty = append(dirty, strings.TrimSpace(ln))
	}
	if len(dirty) == 0 {
		return nil
	}
	snippet := strings.Join(dirty, "; ")
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	return errx.New(
		"working tree dirty in "+cwd+": "+snippet,
		"cd "+cwd+" && git add <write_scope files> && git commit -m \"…\" then retry (maw submit / maw merge); do not leave staged-only changes",
	)
}

func RebaseOntoMain(worktree, mainBranch string) error {
	if err := AssertWorkingTreeClean(worktree); err != nil {
		return err
	}
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

// GateResult is one gate command outcome for review evidence.
type GateResult struct {
	Command    string `json:"command"`
	OK         bool   `json:"ok"`
	Output     string `json:"output,omitempty"`
	EnvFailure bool   `json:"env_failure,omitempty"`
}

// IsEnvFailure reports whether a gate/merge error looks like local environment,
// not task code (shim, missing tools, unpack failures, timeouts).
func IsEnvFailure(msg string) bool {
	low := strings.ToLower(msg)
	needles := []string{
		"command not found", "not found in path", "enoent", "eacces", "enotfound",
		"safe-delete", "cannot find module", "no such file or directory",
		"gate timed out", "npm err!", "npx:", "permission denied",
		"module not found", "unable to resolve", "tarball",
	}
	for _, n := range needles {
		if strings.Contains(low, n) {
			return true
		}
	}
	return false
}

// RunGatesDetailed runs gate commands and returns per-command evidence.
// On failure it returns the partial results plus an error; EnvFailure is set when classified.
func RunGatesDetailed(commands []string, cwd string, timeoutSec int) ([]GateResult, error) {
	if timeoutSec <= 0 {
		timeoutSec = 900
	}
	var results []GateResult
	for _, cmdStr := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
		c := exec.CommandContext(ctx, "sh", "-c", cmdStr)
		c.Dir = cwd
		out, err := c.CombinedOutput()
		cancel()
		outStr := string(out)
		if len(outStr) > 4000 {
			outStr = outStr[:4000] + "…"
		}
		gr := GateResult{Command: cmdStr, Output: outStr}
		if ctx.Err() == context.DeadlineExceeded {
			gr.EnvFailure = true
			results = append(results, gr)
			errMsg := "gate timed out: " + cmdStr
			return results, errx.New(errMsg, "raise gates.timeout_sec or fix tests")
		}
		if err != nil {
			msg := "gate failed: " + cmdStr + "\n" + string(out)
			gr.EnvFailure = IsEnvFailure(msg)
			results = append(results, gr)
			return results, errx.New(msg, "fix failures then `maw submit` again")
		}
		gr.OK = true
		results = append(results, gr)
	}
	return results, nil
}

func RunGates(commands []string, cwd string, timeoutSec int) error {
	_, err := RunGatesDetailed(commands, cwd, timeoutSec)
	return err
}
