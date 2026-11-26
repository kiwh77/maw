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

// MergeOptions 控制合入范围与冲突中止。
type MergeOptions struct {
	Yes     bool
	DryRun  bool
	TaskIDs []string // 非空时只合这些 merging 题
	Abort   bool     // 将 conflict/merge-blocked 题退回可修复态
}

func Merge(cwd string, yes, dryRun bool) (Result, error) {
	return MergeWith(cwd, MergeOptions{Yes: yes, DryRun: dryRun})
}

func MergeWith(cwd string, opts MergeOptions) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	// 合入是工程级操作：无 MAW_AGENT 时用 orchestrator，不强迫调用者身份。
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		who = "orchestrator"
	}
	if opts.Abort {
		return mergeAbort(pc, who, opts.TaskIDs)
	}
	yes, dryRun := opts.Yes, opts.DryRun
	// 任意已识别 Agent 均可合入；仍须走 maw merge，不可直接改主干
	if pc.Config.Approvals.Merge == "manual" && !yes {
		return nil, i18n.E("err.merge_need_yes", "next.merge_yes")
	}
	want := map[string]bool{}
	for _, id := range opts.TaskIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			want[id] = true
		}
	}
	// 主检出允许脏：squash 在临时 worktree 完成，不触碰主工作区未提交改动。
	var queue []*db.Task
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		tasks, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.Status != "merging" {
				continue
			}
			if len(want) > 0 && !want[t.ID] {
				continue
			}
			queue = append(queue, t)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(queue) == 0 {
		if len(want) > 0 {
			return nil, i18n.E("err.merge_task_not_merging", "next.wait_submit")
		}
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
	var lastCheckoutSynced bool
	var lastCheckoutWarning string
	for _, task := range queue {
		tid := task.ID
		wt := filepath.Join(pc.Repo, task.Worktree)
		var newTip string
		preClean, preOnMain := primaryCheckoutCleanOnMain(pc)
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
			tip, err := gitops.SquashMergeIsolated(pc.Repo, task.Branch, pc.Config.Project.MainBranch, "maw: merge "+tid+" "+task.Title)
			newTip = tip
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
			conflictFiles := parseConflictPaths(msg)
			row := map[string]any{
				"id": tid, "status": st, "error": msg, "env_failure": envFail,
				"next": "在 worktree 修复后 maw submit（conflict 可再交）；或 maw merge --abort --task " + tid,
			}
			if len(conflictFiles) > 0 {
				row["conflict_files"] = conflictFiles
			}
			results = append(results, row)
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
			synced, warn := syncCheckoutAfterMerge(pc, preOnMain, preClean, strings.TrimSpace(newTip))
			lastCheckoutSynced = synced
			lastCheckoutWarning = warn
			row := map[string]any{
				"id": tid, "status": "done",
				"worktree_kept":    false,
				"worktree_removed": task.Worktree != "",
				"branch_deleted":   task.Branch,
				"checkout_synced":  synced,
			}
			if warn != "" {
				row["checkout_warning"] = warn
			}
			results = append(results, row)
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
	if lastCheckoutWarning != "" {
		next = i18n.T("next.merge_checkout_dirty") + "；" + next
	}
	out := Result{"ok": allOK, "results": results, "board": html, "project_id": pc.ID(), "next": next,
		"checkout_synced": lastCheckoutSynced}
	if lastCheckoutWarning != "" {
		out["checkout_warning"] = lastCheckoutWarning
	}
	return out, nil
}

// mergeAbort 将 conflict 或 merge 导致的 blocked 退回 in_progress，保留 branch/worktree。
func mergeAbort(pc *projCtx, who string, taskIDs []string) (Result, error) {
	want := map[string]bool{}
	for _, id := range taskIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			want[id] = true
		}
	}
	if len(want) == 0 {
		return nil, i18n.E("err.merge_abort_need_task", "next.merge_abort_task")
	}
	var aborted []map[string]any
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		for id := range want {
			t, err := db.GetTask(tx, pc.ID(), id)
			if err != nil || t == nil {
				return i18n.E("err.unknown_task", "next.task_list", id)
			}
			ok := t.Status == "conflict" ||
				(t.Status == "blocked" && (t.PrevStatus == "merging" ||
					strings.Contains(strings.ToLower(t.BlockedReason), "conflict") ||
					strings.Contains(strings.ToLower(t.BlockedReason), "rebase")))
			if !ok {
				return i18n.E("err.merge_abort_status", "next.inspect_board", id, t.Status)
			}
			from := t.Status
			if err := machine.AssertTransition(t.Status, "in_progress"); err != nil {
				// conflict→in_progress 合法；blocked 走解阻语义
				if t.Status != "blocked" {
					return err
				}
			}
			t.Status = "in_progress"
			t.BlockedReason = ""
			t.PrevStatus = ""
			if t.Extra != nil {
				delete(t.Extra, "block_category")
			}
			t.Revision++
			if err := db.UpsertTask(tx, t); err != nil {
				return err
			}
			_ = db.AppendEvent(tx, pc.ID(), who, "merge_aborted", id, map[string]any{
				"from": from, "to": "in_progress",
			})
			_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", id, map[string]any{
				"from": from, "to": "in_progress", "reason": "merge_abort",
			})
			aborted = append(aborted, map[string]any{
				"id": id, "status": "in_progress",
				"branch": t.Branch, "worktree": t.Worktree,
				"next": "修复后 maw submit",
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	return Result{
		"ok": true, "aborted": aborted, "project_id": pc.ID(),
		"next": "在对应 worktree 修复冲突后 maw submit",
	}, nil
}

// parseConflictPaths 从 git/rebase 错误文本里尽量抽出路径。
func parseConflictPaths(msg string) []string {
	var out []string
	seen := map[string]bool{}
	for _, ln := range strings.Split(msg, "\n") {
		ln = strings.TrimSpace(ln)
		low := strings.ToLower(ln)
		var path string
		switch {
		case strings.HasPrefix(low, "conflict"):
			// CONFLICT (content): Merge conflict in path
			if i := strings.LastIndex(ln, " "); i >= 0 {
				path = strings.TrimSpace(ln[i+1:])
			}
		case strings.Contains(low, "merge conflict in "):
			if i := strings.Index(low, "merge conflict in "); i >= 0 {
				path = strings.TrimSpace(ln[i+len("merge conflict in "):])
			}
		case strings.HasPrefix(low, "both modified:") || strings.HasPrefix(low, "both added:"):
			if i := strings.Index(ln, ":"); i >= 0 {
				path = strings.TrimSpace(ln[i+1:])
			}
		case strings.HasPrefix(low, "u\t") || strings.HasPrefix(low, "uu "):
			fields := strings.Fields(ln)
			if len(fields) >= 2 {
				path = fields[len(fields)-1]
			}
		}
		path = strings.Trim(path, "\"'")
		if path == "" || strings.Contains(path, " ") && !strings.Contains(path, "/") {
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
		if len(out) >= 40 {
			break
		}
	}
	return out
}

// primaryCheckoutCleanOnMain 报告主仓是否在主干且相对 HEAD 干净（无 tracked 改动、无未跟踪文件）。
func primaryCheckoutCleanOnMain(pc *projCtx) (clean, onMain bool) {
	main := strings.TrimSpace(pc.Config.Project.MainBranch)
	br, err := gitops.CurrentBranch(pc.Repo)
	if err != nil || strings.TrimSpace(br) != main {
		return false, false
	}
	onMain = true
	if err := gitops.AssertWorkingTreeClean(pc.Repo); err != nil {
		return false, true
	}
	untracked, _ := home.Git(pc.Repo, "ls-files", "--others", "--exclude-standard")
	if strings.TrimSpace(untracked) != "" {
		return false, true
	}
	return true, true
}

// syncCheckoutAfterMerge 在 update-ref 推进主干后对齐主检出 index/worktree。
// 仅当合入前主检出在主干且干净时自动 reset --hard；否则只警告，避免毁掉本地改动。
func syncCheckoutAfterMerge(pc *projCtx, onMain, wasClean bool, newTip string) (synced bool, warning string) {
	if !onMain {
		return false, ""
	}
	if !wasClean {
		return false, i18n.T("warn.merge_checkout_dirty")
	}
	if newTip == "" {
		tip, err := home.Git(pc.Repo, "rev-parse", pc.Config.Project.MainBranch)
		if err != nil {
			return false, err.Error()
		}
		newTip = strings.TrimSpace(tip)
	}
	if out, err := home.Git(pc.Repo, "reset", "--hard", newTip); err != nil {
		return false, out
	}
	return true, ""
}
