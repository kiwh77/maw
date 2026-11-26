package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	enginereconcile "github.com/kiwh77/maw/internal/engine/reconcile"
	"github.com/kiwh77/maw/internal/gitops"
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

// TaskCancel 把题标为 cancelled，级联收尾 Run／租约，并清理磁盘 worktree／分支。
//
// 用于「蓝图已去掉 / 并入别题 / 明确不做」；blocked 题也可取消。
// 已是 cancelled/done 时若仍有残留占用则幂等清理（不再空手拒绝）。
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

	var (
		from           string
		wt, branch     string
		cleanedLeftover bool
		alreadyTerminal bool
	)
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return i18n.E("err.unknown_task", "next.task_list", taskID)
		}
		wt, branch = t.Worktree, t.Branch
		from = t.Status

		if t.Status == "cancelled" || t.Status == "done" {
			alreadyTerminal = true
			n, err := cascadeCancelOccupancy(tx, pc.ID(), t)
			if err != nil {
				return err
			}
			if n == 0 {
				return i18n.E("err.task_already_terminal", "next.board_deps", taskID, t.Status)
			}
			cleanedLeftover = true
			_ = db.AppendEvent(tx, pc.ID(), who, "task_cancel_reconciled", taskID, map[string]any{
				"status": t.Status, "released": n, "instance_id": t.InstanceID,
			})
			return nil
		}
		if err := machine.AssertTransition(t.Status, "cancelled"); err != nil {
			return err
		}
		if _, err := cascadeCancelOccupancy(tx, pc.ID(), t); err != nil {
			return err
		}

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

	if wt != "" || branch != "" {
		gitops.CleanupMergedTask(pc.Repo, wt, branch, false)
	}
	_, _ = refreshBoard(pc)

	next := i18n.T("next.task_cancelled", taskID)
	if supersededBy != "" {
		next = i18n.T("next.task_superseded", taskID, supersededBy)
	}
	if cleanedLeftover {
		next = i18n.T("next.task_cancel_reconciled", taskID)
	}
	status := "cancelled"
	if alreadyTerminal && cleanedLeftover {
		status = from
	}
	return Result{
		"ok": true, "task_id": taskID, "status": status,
		"from": from, "reason": reason, "superseded_by": supersededBy,
		"project_id": pc.ID(), "cancelled_at": time.Now().UTC().Format(time.RFC3339),
		"leftover_cleaned": cleanedLeftover,
		"next":             next,
	}, nil
}

// cascadeCancelOccupancy 收尾该题实例上仍占用的 Run 与租约。
// 返回释放／收尾的动作数；managed 且有进程证据的占用中 Run 会拒绝取消。
func cascadeCancelOccupancy(tx *sql.Tx, projectID string, t *db.Task) (int, error) {
	if t == nil || t.InstanceID == "" {
		return 0, nil
	}
	actions := 0
	runs, err := db.ListRuns(tx, projectID)
	if err != nil {
		return 0, err
	}
	for _, run := range runs {
		if run == nil || run.InstanceID != t.InstanceID {
			continue
		}
		if !run.Occupies() {
			// 终态但仍可能挂着未释放租约（cancel 旧路径留下的）。
			n, err := releaseInstanceLeasesCounted(tx, projectID, run.InstanceID, "task_cancelled")
			if err != nil {
				return actions, err
			}
			actions += n
			continue
		}
		// 有进程证据的受管 Run：必须先停稳（接 PlanTaskCancel 门禁）。
		if !runHasNoProcessEvidence(tx, run) {
			plan := enginereconcile.PlanTaskCancel(ptrRunModel(run))
			if !plan.Allowed {
				return actions, i18n.E("err.task_cancel_occupied", "next.task_cancel_occupied", t.ID)
			}
		}
		if err := settleRunForCancel(tx, projectID, run); err != nil {
			return actions, err
		}
		actions++
		n, err := releaseInstanceLeasesCounted(tx, projectID, run.InstanceID, "task_cancelled")
		if err != nil {
			return actions, err
		}
		actions += n
	}
	// 没有 Run 行但残留租约（孤儿 lease）。
	n, err := releaseInstanceLeasesCounted(tx, projectID, t.InstanceID, "task_cancelled")
	if err != nil {
		return actions, err
	}
	actions += n
	return actions, nil
}

func ptrRunModel(run *db.Run) *model.Run {
	if run == nil {
		return nil
	}
	m := run.Model()
	return &m
}

// settleRunForCancel 把占用中的 Run 收到 cancelled 终态并确认进程停止。
// 阶段走契约最短合法路径；manual-compat / 无进程证据可强制收尾。
func settleRunForCancel(tx *sql.Tx, projectID string, run *db.Run) error {
	if run == nil {
		return nil
	}
	if run.Phase.Terminal() {
		if !run.ProcessStopConfirmed {
			if err := db.ConfirmProcessStop(tx, run.ID, "task_cancelled"); err != nil {
				return err
			}
		}
		return nil
	}
	path, ok := runPhasePath(run.Phase, model.PhaseCancelled)
	if !ok {
		// 无法直达 cancelled 时尝试经 stopping。
		path, ok = runPhasePath(run.Phase, model.PhaseStopping)
		if !ok {
			return i18n.E("err.task_cancel_occupied", "next.task_cancel_occupied", run.InstanceID)
		}
		path = append(path, model.PhaseCancelled)
	}
	evidence := []string{"reason:task_cancelled", "strategy:" + manualCompatStrategy}
	for _, next := range path {
		if run.Phase == next {
			continue
		}
		if !model.CanTransition(run.Phase, next) {
			return i18n.E("err.task_cancel_occupied", "next.task_cancel_occupied", run.InstanceID)
		}
		if _, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
			ID:            cancelRunEventID(run.ID, string(next)),
			ProjectID:     projectID,
			RunID:         run.ID,
			InstanceID:    run.InstanceID,
			Kind:          db.EventRunPhase,
			Phase:         next,
			SchemaVersion: db.RunEventSchemaVersion,
			Payload:       map[string]any{"reason": "task_cancelled"},
		}); err != nil {
			return err
		}
		stop := next == model.PhaseCancelled || next.Terminal()
		// 取消即放弃产物：DeliveryFrozen=true 以便释放 workspace（E242 保护对象为空）。
		updated, err := db.UpdateRunProgress(tx, run.ID, next, stop, stop, evidence)
		if err != nil {
			return err
		}
		run = updated
	}
	if run != nil && !run.ProcessStopConfirmed {
		return db.ConfirmProcessStop(tx, run.ID, "task_cancelled")
	}
	return nil
}

func releaseInstanceLeasesCounted(tx *sql.Tx, projectID, instanceID, reason string) (int, error) {
	leases, err := db.ListActiveLeases(tx, projectID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range leases {
		if l == nil || l.InstanceID != instanceID || l.Released() {
			continue
		}
		cond := db.ReleaseCondition{ProcessStopConfirmed: true, DeliveryFrozen: true}
		if err := db.ReleaseLease(tx, l.ID, l.Generation, reason, cond); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func cancelRunEventID(runID, phase string) model.EventID {
	sum := sha256.Sum256([]byte(runID + "|" + phase + "|task-cancelled"))
	return model.EventID("evt_" + hex.EncodeToString(sum[:8]))
}
