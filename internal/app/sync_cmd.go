package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
)

// Sync 将当前（或指定）任务 worktree rebase 到主干 tip。
func Sync(cwd, taskID string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	if taskID == "" {
		taskID, err = inferTaskID(cwd)
		if err != nil {
			return nil, err
		}
	}
	var wtRel string
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, e := db.GetTask(tx, pc.ID(), taskID)
		if e != nil || t == nil {
			return i18n.E("err.unknown_task", "next.task_list", taskID)
		}
		if t.Owner != "" && t.Owner != who {
			if err := identity.RequireOwner(who, t); err != nil {
				return err
			}
		}
		wtRel = t.Worktree
		return nil
	})
	if err != nil {
		return nil, err
	}
	wt := absWorktree(pc.Repo, wtRel)
	if wt == "" {
		wt, err = home.WorktreeRoot(cwd)
		if err != nil {
			return nil, i18n.E("err.sync_no_worktree", "next.claim_first", taskID)
		}
	}
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		return nil, i18n.E("err.sync_no_worktree", "next.claim_first", taskID)
	}
	main := pc.Config.Project.MainBranch
	before, _ := home.Git(wt, "rev-parse", "HEAD")
	if err := gitops.RebaseOntoMain(wt, main); err != nil {
		return nil, err
	}
	after, _ := home.Git(wt, "rev-parse", "HEAD")
	_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		return db.AppendEvent(tx, pc.ID(), who, "worktree_synced", taskID, map[string]any{
			"main_branch": main, "before": strings.TrimSpace(before), "after": strings.TrimSpace(after),
		})
	})
	return Result{
		"ok": true, "task_id": taskID, "worktree": wt, "project_id": pc.ID(),
		"before": strings.TrimSpace(before), "after": strings.TrimSpace(after),
		"next": "继续实现或 maw submit",
	}, nil
}

// requireSubmitWorktree 强制 submit 在任务 worktree 内执行，禁止扫主仓脏文件。
func requireSubmitWorktree(cwd string, pc *projCtx, taskWorktree string) (string, error) {
	expected := absWorktree(pc.Repo, taskWorktree)
	if expected == "" {
		return "", i18n.E("err.submit_no_worktree", "next.claim_first")
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		cwdAbs = cwd
	}
	root, err := home.WorktreeRoot(cwdAbs)
	if err != nil {
		root = cwdAbs
	}
	root, _ = filepath.EvalSymlinks(root)
	expected, _ = filepath.EvalSymlinks(expected)
	if !sameOrInside(root, expected) {
		return "", i18n.E("err.submit_wrong_worktree", "next.submit_in_worktree", expected, root)
	}
	if st, err := os.Stat(expected); err != nil || !st.IsDir() {
		return "", i18n.E("err.submit_no_worktree", "next.claim_first")
	}
	return expected, nil
}

func sameOrInside(got, want string) bool {
	got = filepath.Clean(got)
	want = filepath.Clean(want)
	if got == want {
		return true
	}
	rel, err := filepath.Rel(want, got)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
