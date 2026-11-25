package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
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
		return nil, i18n.E("err.merge_need_yes", "next.merge_yes")
	}
	// 主检出允许脏：squash 在临时 worktree 完成，不触碰主工作区未提交改动。
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
		return nil, i18n.E("err.merge_empty", "next.wait_submit")
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
			// 与 submit/review 同一验收用例：证据不齐、unknown／残留进程时拒绝合入。
			if err := assertDeliveryMergeReady(pc, task); err != nil {
				return err
			}
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
			_, err := gitops.SquashMergeIsolated(pc.Repo, task.Branch, pc.Config.Project.MainBranch, "maw: merge "+tid+" "+task.Title)
			return err
		}()
		if mergeErr != nil {
			msg := mergeErr.Error()
			if e, ok := mergeErr.(*errx.Error); ok {
				msg = e.Msg
			}
			envFail := gitops.IsEnvFailure(msg)
			dirtyFail := gitops.IsDirtyTree(msg)
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
				gateUnrelated := gitops.IsUnrelatedRepoGate(msg, t.WriteScope)
				if envFail || dirtyFail || gateUnrelated {
					// A8：环境失败 / 脏树 / 无关闸门 → blocked（可恢复），不累加 attempts，不进 conflict
					t.PrevStatus = from
					t.Status = "blocked"
					reasonMsg := msg
					cat := BlockCatEnv
					if dirtyFail && !envFail {
						cat = BlockCatConflict
						if paths, _ := gitops.DirtyTrackedPaths(pc.Repo); len(paths) > 0 {
							list := strings.Join(paths, "; ")
							if len(list) > 240 {
								list = list[:240] + "…"
							}
							reasonMsg = "[conflict] 主工作区脏文件阻塞 merge：" + list +
								"。建议：维护者提交/stash 这些文件；Agent 勿擅清他人改动。"
						} else {
							reasonMsg = "[conflict] " + msg
						}
					} else if gateUnrelated && !envFail {
						cat = BlockCatGate
						snip := msg
						if len(snip) > 280 {
							snip = snip[:280] + "…"
						}
						reasonMsg = "[gate] 合入闸门失败且失败面不在本卡 write_scope（疑似主干预置红灯）：" + snip
					}
					t.BlockedReason = reasonMsg
					if len(t.BlockedReason) > 500 {
						t.BlockedReason = t.BlockedReason[:500]
					}
					if t.Extra == nil {
						t.Extra = map[string]any{}
					}
					t.Extra["block_category"] = cat
					t.Revision++
					_ = db.UpsertTask(tx, t)
					evType := "gate_env_failed"
					reasonCode := "env_failure"
					if dirtyFail && !envFail {
						evType = "gate_failed"
						reasonCode = "dirty_tree"
					} else if cat == BlockCatGate {
						evType = "gate_failed"
						reasonCode = "unrelated_gate"
					}
					_ = db.AppendEvent(tx, pc.ID(), who, evType, tid, map[string]any{
						"error": reasonMsg, "phase": "merge", "recoverable": true,
						"category": cat, "reason_code": reasonCode,
					})
					_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", tid, map[string]any{
						"from": from, "to": "blocked", "reason": reasonMsg,
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
			if envFail || dirtyFail || gitops.IsUnrelatedRepoGate(msg, task.WriteScope) {
				st = "blocked"
			}
			if skipped {
				st = "skipped"
			}
			results = append(results, map[string]any{"id": tid, "status": st, "error": msg, "env_failure": envFail})
			// A6：冲突题隔离，继续后续队列
			continue
		}
		// 合入成功：删除 per-agent worktree 与题分支（下一题 next 会重建同路径树）
		gitops.CleanupMergedTask(pc.Repo, task.Worktree, task.Branch, false)
		// 主检出可能不在主干或仍脏；以主干 ref 为准，勿用 HEAD。
		mergedCommit, _ := home.Git(pc.Repo, "rev-parse", pc.Config.Project.MainBranch)
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
			finalizeTerminalFields(t) // 清空 blocked_reason/branch/worktree，归档历史
			t.Revision++
			_ = db.UpsertTask(tx, t)
			_ = db.AppendEvent(tx, pc.ID(), who, "merged", tid, map[string]any{
				"branch": task.Branch, "worktree_kept": false, "worktree_removed": true, "branch_deleted": true,
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
			_, _ = applyDeliveryCase(pc, deliveryCaseInput{
				Action:       deliveryActionMerge,
				TaskID:       tid,
				Actor:        who,
				MergedCommit: strings.TrimSpace(mergedCommit),
			})
			results = append(results, map[string]any{
				"id": tid, "status": "done",
				"worktree_kept":     false,
				"worktree_removed":  task.Worktree != "",
				"branch_deleted":    task.Branch,
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
