package app

import (
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/machine"
	"github.com/kiwh77/maw/internal/scope"
)

// RequestChangeOptions 扩写当前题 write_scope，供 Agent 合法越界后继续干。
type RequestChangeOptions struct {
	TaskID  string
	Add     []string // 显式追加的 glob / 路径
	Message string
	DryRun  bool
}

// RequestChange 把路径并入当前（或指定）任务的 write_scope，并写审计事件。
//
// --add 为空时：扫描 worktree 相对主干的改动，自动把越界文件收成前缀 glob 建议。
// 不改蓝图文件；下次 plan apply 仍可能用蓝图覆盖——调用方应同步改 blueprint。
func RequestChange(cwd string, opts RequestChangeOptions) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	taskID := strings.TrimSpace(opts.TaskID)
	if taskID == "" {
		taskID, err = inferTaskID(cwd)
		if err != nil {
			return nil, err
		}
	}
	wt, err := home.WorktreeRoot(cwd)
	if err != nil {
		wt = filepath.Join(pc.Repo, ".worktrees", who)
	}

	adds := normalizeScopeAdds(opts.Add)
	autoDetected := false
	if len(adds) == 0 {
		files, ferr := gitops.ChangedFiles(wt, pc.Config.Project.MainBranch)
		if ferr != nil {
			return nil, ferr
		}
		files = filterScopeFiles(files)
		var task *db.Task
		_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			task, _ = db.GetTask(tx, pc.ID(), taskID)
			return nil
		})
		if task == nil {
			return nil, i18n.E("err.unknown_task", "next.task_list", taskID)
		}
		bad := scope.OutOfScopeFiles(files, task.WriteScope)
		adds = scopeSuggestionsFromFiles(bad)
		autoDetected = true
		if len(adds) == 0 {
			return nil, i18n.E("err.no_scope_expand", "next.request_change_add")
		}
	}

	if opts.DryRun {
		return Result{
			"ok": true, "dry_run": true, "task_id": taskID,
			"add": adds, "auto_detected": autoDetected, "project_id": pc.ID(),
		}, nil
	}

	var before, after []string
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return i18n.E("err.unknown_task", "next.task_list", taskID)
		}
		if !machine.ActiveOwner[t.Status] && t.Status != "blocked" && t.Status != "ready" {
			return i18n.E("err.cannot_request_change", "next.inspect_board", taskID, t.Status)
		}
		if t.Owner != "" && t.Owner != who {
			if err := identity.RequireOwner(who, t); err != nil {
				return err
			}
		}
		before = append([]string{}, t.WriteScope...)
		t.WriteScope = mergeScopes(t.WriteScope, adds)
		after = append([]string{}, t.WriteScope...)
		t.Revision++
		if t.Extra == nil {
			t.Extra = map[string]any{}
		}
		t.Extra["scope_expanded_at"] = db.UTCNow()
		if opts.Message != "" {
			t.Extra["scope_expand_reason"] = opts.Message
		}
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		if err := freezeIfDefinitionChanged(tx, t); err != nil {
			return err
		}
		_ = db.AppendEvent(tx, pc.ID(), who, "scope_expanded", taskID, map[string]any{
			"added": adds, "before": before, "after": after,
			"message": opts.Message, "auto_detected": autoDetected,
		})
		// 若正 blocked 且 PrevStatus 可恢复，不自动 unblock——调用方自行 unblock。
		return nil
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	return Result{
		"ok": true, "task_id": taskID, "added": adds,
		"write_scope": after, "auto_detected": autoDetected,
		"project_id": pc.ID(),
		"next":       i18n.T("next.after_request_change", taskID),
		"note":       i18n.T("note.request_change_blueprint"),
	}, nil
}

func normalizeScopeAdds(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		p := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		p = strings.TrimPrefix(p, "./")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func mergeScopes(existing, add []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range existing {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range add {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// scopeSuggestionsFromFiles 把越界文件收成尽量短的 /** 建议（按第二段目录）。
func scopeSuggestionsFromFiles(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		f = strings.TrimSpace(strings.ReplaceAll(f, "\\", "/"))
		if f == "" {
			continue
		}
		sug := f
		parts := strings.Split(f, "/")
		switch {
		case len(parts) >= 3 && parts[0] == "backend" && parts[1] == "internal":
			sug = strings.Join(parts[:3], "/") + "/**"
		case len(parts) >= 2:
			sug = parts[0] + "/**"
		}
		if seen[sug] {
			continue
		}
		seen[sug] = true
		out = append(out, sug)
	}
	return out
}
