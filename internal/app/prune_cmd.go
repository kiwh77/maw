package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/machine"
)

// Prune lists or removes leftover worktrees and agent/* branches.
// Keeps: ActiveOwner task worktrees/branches, and per-agent trees for non-offline agents.
// Default is dry-run (yes=false); pass yes=true to delete.
func Prune(cwd string, yes, dryRun bool) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	// dryRun global flag OR !yes → preview only
	preview := dryRun || !yes

	var tasks []*db.Task
	var agents []*db.Agent
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		tasks, e = db.ListTasks(tx, pc.ID())
		if e != nil {
			return e
		}
		agents, e = db.ListAgents(tx, pc.ID())
		return e
	})
	if err != nil {
		return nil, err
	}

	protectedWT := map[string]bool{}
	protectedBranch := map[string]bool{}
	for _, t := range tasks {
		if t == nil || !machine.ActiveOwner[t.Status] {
			continue
		}
		if t.Worktree != "" {
			protectedWT[absWorktree(pc.Repo, t.Worktree)] = true
		}
		if t.Branch != "" {
			protectedBranch[t.Branch] = true
		}
		if t.Owner != "" {
			protectedWT[filepath.Join(pc.Repo, ".worktrees", t.Owner)] = true
		}
	}
	for _, a := range agents {
		if a == nil || a.ID == "" {
			continue
		}
		if a.Status == "offline" {
			continue
		}
		protectedWT[filepath.Join(pc.Repo, ".worktrees", a.ID)] = true
	}

	var removeWTs []string
	wtRoot := filepath.Join(pc.Repo, ".worktrees")
	if entries, err := os.ReadDir(wtRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			abs := filepath.Join(wtRoot, e.Name())
			if protectedWT[abs] {
				continue
			}
			removeWTs = append(removeWTs, abs)
		}
	}

	var removeBranches []string
	out, _ := home.Git(pc.Repo, "branch", "--list", "agent/*")
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(strings.TrimPrefix(ln, "*"))
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if protectedBranch[ln] {
			continue
		}
		removeBranches = append(removeBranches, ln)
	}

	removedWT := []string{}
	removedBranches := []string{}
	if !preview {
		for _, wt := range removeWTs {
			gitops.RemoveWorktree(pc.Repo, wt)
			removedWT = append(removedWT, wt)
		}
		for _, br := range removeBranches {
			// If a kept worktree is still on this branch, detach first.
			for _, a := range agents {
				if a == nil || a.Status == "offline" {
					continue
				}
				agentWT := filepath.Join(pc.Repo, ".worktrees", a.ID)
				if cur, err := gitops.CurrentBranch(agentWT); err == nil && cur == br {
					_ = gitops.DetachWorktree(agentWT)
				}
			}
			gitops.DeleteBranch(pc.Repo, br)
			removedBranches = append(removedBranches, br)
		}
		_, _ = home.Git(pc.Repo, "worktree", "prune")
	} else {
		removedWT = removeWTs
		removedBranches = removeBranches
	}

	next := "maw prune --yes  # 确认后删除上述残留"
	if !preview {
		next = "残留已清理；继续 maw next / maw board --serve"
	}
	if len(removeWTs) == 0 && len(removeBranches) == 0 {
		next = "无残留可清"
	}

	return Result{
		"ok":               true,
		"dry_run":          preview,
		"project_id":       pc.ID(),
		"worktrees":        removeWTs,
		"branches":         removeBranches,
		"removed_worktrees": removedWT,
		"removed_branches": removedBranches,
		"kept_worktrees":   len(protectedWT),
		"next":             next,
	}, nil
}

func absWorktree(repo, relOrAbs string) string {
	if relOrAbs == "" {
		return ""
	}
	if filepath.IsAbs(relOrAbs) {
		return relOrAbs
	}
	return filepath.Join(repo, relOrAbs)
}
