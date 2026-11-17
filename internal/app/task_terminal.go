package app

import (
	"database/sql"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/machine"
)

const (
	extraBlockHistoryKey = "block_history"
	extraCancelReasonKey = "cancel_reason"
	extraSupersededByKey = "superseded_by"
)

// finalizeTerminalFields 进入 done/cancelled 时收尾展示字段：
// 把 blocked_reason 归档进 extra.block_history，再清空 blocked_reason / branch / worktree，
// 避免终态卡仍挂着「脏」阻塞文案（niuma T-1214 类问题）。
func finalizeTerminalFields(t *db.Task) {
	if t == nil {
		return
	}
	if reason := strings.TrimSpace(t.BlockedReason); reason != "" {
		if t.Extra == nil {
			t.Extra = map[string]any{}
		}
		hist, _ := t.Extra[extraBlockHistoryKey].([]any)
		hist = append(hist, map[string]any{
			"at":     db.UTCNow(),
			"reason": reason,
		})
		t.Extra[extraBlockHistoryKey] = hist
		t.BlockedReason = ""
	}
	t.Branch = ""
	t.Worktree = ""
	t.PrevStatus = ""
}

// CancelOptions 取消任务时的审计字段。
type CancelOptions struct {
	Reason       string
	SupersededBy string
}

// TaskCancel 把题标为 cancelled，写审计事件，并做终态字段收尾。
//
// 用于「蓝图已去掉 / 并入别题 / 明确不做」；blocked 题也可取消（转移矩阵对 blocked 不限制目标）。
func TaskCancel(cwd, taskID string, opts CancelOptions) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		who = "orchestrator"
	}
	reason := strings.TrimSpace(opts.Reason)
	if reason == "" {
		reason = "cancelled"
	}
	supersededBy := strings.TrimSpace(opts.SupersededBy)

	var from string
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return i18n.E("err.unknown_task", "next.task_list", taskID)
		}
		if t.Status == "cancelled" || t.Status == "done" {
			return i18n.E("err.task_already_terminal", "next.board_deps", taskID, t.Status)
		}
		if err := machine.AssertTransition(t.Status, "cancelled"); err != nil {
			return err
		}
		from = t.Status
		owner := t.Owner
		t.Status = "cancelled"
		finalizeTerminalFields(t)
		if t.Extra == nil {
			t.Extra = map[string]any{}
		}
		t.Extra[extraCancelReasonKey] = reason
		if supersededBy != "" {
			t.Extra[extraSupersededByKey] = supersededBy
		}
		t.Owner = ""
		t.Revision++
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		payload := map[string]any{
			"from": from, "to": "cancelled", "reason": reason,
			"instance_id": t.InstanceID, "iteration_id": t.IterationID,
		}
		if supersededBy != "" {
			payload["superseded_by"] = supersededBy
		}
		_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", taskID, payload)
		_ = db.AppendEvent(tx, pc.ID(), who, "task_cancelled", taskID, payload)
		if owner != "" {
			_ = db.ClearAgentTask(tx, pc.ID(), owner)
		}
		return promoteReady(tx, pc.ID())
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	next := i18n.T("next.task_cancelled", taskID)
	if supersededBy != "" {
		next = i18n.T("next.task_superseded", taskID, supersededBy)
	}
	return Result{
		"ok": true, "task_id": taskID, "status": "cancelled",
		"from": from, "reason": reason, "superseded_by": supersededBy,
		"project_id": pc.ID(), "cancelled_at": time.Now().UTC().Format(time.RFC3339),
		"next": next,
	}, nil
}
