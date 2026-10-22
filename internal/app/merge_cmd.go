package app

import (
	"database/sql"
	"os"
	"path/filepath"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/machine"
)

func Merge(cwd string, yes, dryRun bool) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	// 任意已识别 Agent 均可合入；仍须走 maw merge，不可直接改主干
	if pc.Config.Approvals.Merge == "manual" && !yes {
		return nil, errx.New("merge requires confirmation", "run `maw merge --yes`")
	}
	if err := gitops.AssertWorkingTreeClean(pc.Repo); err != nil {
		return nil, err
	}
	var queue []*db.Task
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		tasks, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.Status == "merging" {
				queue = append(queue, t)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(queue) == 0 {
		return nil, errx.New("merge queue empty", "wait for submit/review")
	}
	for i := 0; i < len(queue); i++ {
		for j := i + 1; j < len(queue); j++ {
			if queue[j].MergeOrder < queue[i].MergeOrder || (queue[j].MergeOrder == queue[i].MergeOrder && queue[j].ID < queue[i].ID) {
				queue[i], queue[j] = queue[j], queue[i]
			}
		}
	}
	if dryRun {
		ids := make([]string, len(queue))
		for i, t := range queue {
			ids[i] = t.ID
		}
		return Result{"ok": true, "dry_run": true, "queue": ids, "project_id": pc.ID()}, nil
	}
	var results []map[string]any
	for _, task := range queue {
		tid := task.ID
		wt := filepath.Join(pc.Repo, task.Worktree)
		mergeErr := func() error {
			if st, err := os.Stat(wt); err == nil && st.IsDir() {
				if err := gitops.RebaseOntoMain(wt, pc.Config.Project.MainBranch); err != nil {
					return err
				}
			}
			gateCwd := pc.Repo
			if st, err := os.Stat(wt); err == nil && st.IsDir() {
				gateCwd = wt
			}
			// merge 复核：rebase 后再跑 gate
			if err := gitops.RunGates(pc.Config.Gates.Commands, gateCwd, pc.Config.Gates.TimeoutSec); err != nil {
				return err
			}
			if cur, _ := gitops.CurrentBranch(pc.Repo); cur != pc.Config.Project.MainBranch {
				_, _ = home.Git(pc.Repo, "checkout", pc.Config.Project.MainBranch)
			}
			if err := gitops.AssertWorkingTreeClean(pc.Repo); err != nil {
				return err
			}
			return gitops.SquashMerge(pc.Repo, task.Branch, pc.Config.Project.MainBranch, "maw: merge "+tid+" "+task.Title)
		}()
		if mergeErr != nil {
			msg := mergeErr.Error()
			if e, ok := mergeErr.(*errx.Error); ok {
				msg = e.Msg
			}
			envFail := gitops.IsEnvFailure(msg)
			skipped := false
			_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
				t, _ := db.GetTask(tx, pc.ID(), tid)
				if t == nil {
					return nil
				}
				// A5：仅 merging 可写失败态；已 done 拒绝覆盖
				if t.Status == "done" {
					skipped = true
					_ = db.AppendEvent(tx, pc.ID(), who, "merge_skipped", tid, map[string]any{
						"reason": "already_done", "error": msg,
					})
					return nil
				}
				if t.Status != "merging" {
					skipped = true
					_ = db.AppendEvent(tx, pc.ID(), who, "merge_skipped", tid, map[string]any{
						"reason": "status_" + t.Status, "error": msg,
					})
					return nil
				}
				from := t.Status
				if envFail {
					// A8：环境失败 → blocked，不累加 attempts，不进 conflict
					t.PrevStatus = from
					t.Status = "blocked"
					t.BlockedReason = msg
					if len(t.BlockedReason) > 500 {
						t.BlockedReason = t.BlockedReason[:500]
					}
					t.Revision++
					_ = db.UpsertTask(tx, t)
					_ = db.AppendEvent(tx, pc.ID(), who, "gate_env_failed", tid, map[string]any{"error": msg, "phase": "merge"})
					_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", tid, map[string]any{
						"from": from, "to": "blocked", "reason": "env_failure",
					})
					return nil
				}
				t.Status = "conflict"
				t.BlockedReason = msg
				if len(t.BlockedReason) > 500 {
					t.BlockedReason = t.BlockedReason[:500]
				}
				t.Attempts++
				t.Revision++
				_ = db.UpsertTask(tx, t)
				_ = db.AppendEvent(tx, pc.ID(), who, "gate_failed", tid, map[string]any{"error": msg, "phase": "merge"})
				_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", tid, map[string]any{
					"from": from, "to": "conflict",
				})
				return nil
			})
			noteDir := filepath.Join(pc.LocalHome, "handoffs")
			_ = os.MkdirAll(noteDir, 0o755)
			_ = os.WriteFile(filepath.Join(noteDir, tid+"-conflict.md"), []byte("# Conflict: "+tid+"\n\n"+msg+"\n"), 0o644)
			// conflict/blocked 保留题分支供修复；残留由 maw prune 在 agent offline / 题终态后回收
			st := "conflict"
			if envFail {
				st = "blocked"
			}
			if skipped {
				st = "skipped"
			}
			results = append(results, map[string]any{"id": tid, "status": st, "error": msg, "env_failure": envFail})
			// A6：冲突题隔离，继续后续队列
			continue
		}
		// 合入成功：保留 per-agent worktree（依赖可复用），仅 detach 后删除题分支
		gitops.CleanupMergedTask(pc.Repo, task.Worktree, task.Branch, true)
		mergeOK := false
		_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			t, _ := db.GetTask(tx, pc.ID(), tid)
			if t == nil {
				return nil
			}
			// A5：仅 merging → done；已 done 则跳过（防重复合入）
			if t.Status == "done" {
				_ = db.AppendEvent(tx, pc.ID(), who, "merge_skipped", tid, map[string]any{
					"reason": "already_done",
				})
				return nil
			}
			if t.Status != "merging" {
				_ = db.AppendEvent(tx, pc.ID(), who, "merge_skipped", tid, map[string]any{
					"reason": "status_" + t.Status,
				})
				return nil
			}
			if err := machine.AssertTransition(t.Status, "done"); err != nil {
				return err
			}
			owner := t.Owner
			from := t.Status
			t.Status = "done"
			t.Worktree = ""
			t.BlockedReason = "" // conflict/blocked 残留不得带到已完成题
			t.Revision++
			_ = db.UpsertTask(tx, t)
			_ = db.AppendEvent(tx, pc.ID(), who, "merged", tid, map[string]any{
				"branch": task.Branch, "worktree_kept": true, "branch_deleted": true,
			})
			_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", tid, map[string]any{
				"from": from, "to": "done",
			})
			if owner != "" {
				_ = db.ClearAgentTask(tx, pc.ID(), owner)
			}
			mergeOK = true
			return promoteReady(tx, pc.ID())
		})
		if mergeOK {
			results = append(results, map[string]any{
				"id": tid, "status": "done",
				"worktree_kept":  task.Worktree != "",
				"branch_deleted": task.Branch,
			})
		} else {
			results = append(results, map[string]any{"id": tid, "status": "skipped"})
		}
	}
	html, _ := refreshBoard(pc)
	allOK := true
	for _, r := range results {
		if r["status"] != "done" {
			allOK = false
		}
	}
	next := "maw board --serve"
	if allOK {
		_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			tasks, err := db.ListTasks(tx, pc.ID())
			if err != nil {
				return err
			}
			for _, t := range tasks {
				if t.Status == "ready" {
					next = "maw next  # 仍有 ready，继续领取下一题"
					return nil
				}
			}
			next = "无更多 ready；可 maw unregister 或 maw board --serve"
			return nil
		})
	}
	return Result{"ok": allOK, "results": results, "board": html, "project_id": pc.ID(), "next": next}, nil
}
