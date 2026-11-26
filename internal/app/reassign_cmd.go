package app

import (
	"database/sql"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/machine"
)

// ReassignInput 是 `maw reassign` 的入参。
type ReassignInput struct {
	TaskID string
	// To 是目标 Agent（必须已在本工程注册）。
	To string
	// ExpectedRevision 是调用方读到的任务版本；缺省（<0）直接拒绝。
	// 契约要求归属变更一律带期望版本，否则两个人同时改派会互相静默覆盖。
	ExpectedRevision int
}

// Reassign 把任务归属改到目标 Agent。
//
// 边界（契约 maw.collaboration.primitives/v1 §1/§5）：
//   - 只改 owner 与 revision，不搬 worktree、不改任务状态：把别人的半成品目录
//     悄悄交给另一个人，比不搬更危险。新 owner 自行 next/换树。
//   - 必须带 expected_revision；冲突返回 revision_mismatch（含 actual_revision）。
//   - 不改 claim 事务、不改写盘强制检查。
func Reassign(cwd string, in ReassignInput) (Result, error) {
	taskID := strings.TrimSpace(in.TaskID)
	to := strings.TrimSpace(in.To)
	if taskID == "" {
		return nil, &errx.Error{Msg: i18n.T("err.reassign_task"), Next: i18n.T("next.reassign_task"), Code: 1}
	}
	if to == "" {
		return nil, &errx.Error{Msg: i18n.T("err.reassign_to"), Next: i18n.T("next.reassign_to"), Code: 1}
	}
	if in.ExpectedRevision < 0 {
		return nil, &errx.Error{
			Msg:  i18n.T("err.revision_required"),
			Next: i18n.T("next.include_revision"),
			Code: 1,
		}
	}

	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	// 权限分层（契约 §5）：只有 Lead/中枢能改派。普通成员越权必须失败，
	// 且失败要发生在读任务之前——不允许「先看一眼再拒绝」泄漏归属信息。
	if err := identity.RequireLeadAction(who, pc.Config, "reassign"); err != nil {
		return nil, leadErr(err)
	}

	var (
		prevOwner string
		revision  int
		status    string
	)
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, e := db.GetTask(tx, pc.ID(), taskID)
		if e != nil {
			return e
		}
		if t == nil {
			return &errx.Error{
				Msg:  i18n.T("err.unknown_task") + ": " + taskID,
				Next: i18n.T("next.task_list"),
				Code: 1,
			}
		}
		target, e := db.GetAgent(tx, pc.ID(), to)
		if e != nil {
			return e
		}
		if target == nil {
			return &errx.Error{
				Msg:  i18n.T("err.reassign_agent_unknown") + ": " + to,
				Next: i18n.T("next.reassign_agent_unknown"),
				Code: 1,
			}
		}
		prevOwner = t.Owner
		updated, e := db.UpdateTaskCAS(tx, pc.ID(), taskID, in.ExpectedRevision, func(x *db.Task) error {
			x.Owner = to
			return nil
		})
		if e != nil {
			if mm := asRevisionMismatch(e); mm != nil {
				return &errx.Error{
					Msg: i18n.T("err.reassign_revision_mismatch") + ": " +
						i18n.T("next.actual_revision") + "=" + strconv.Itoa(mm.Actual),
					Next: i18n.T("next.reassign_reread"),
					Code: 1,
				}
			}
			return e
		}
		revision = updated.Revision
		status = updated.Status
		return db.AppendEvent(tx, pc.ID(), who, "task_reassigned", taskID, map[string]any{
			"from": prevOwner, "to": to,
			"expected_revision": in.ExpectedRevision, "revision": revision,
		})
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	return Result{
		"ok": true, "project_id": pc.ID(), "task_id": taskID,
		"owner": to, "previous_owner": prevOwner, "status": status,
		"revision": revision, "actor": who,
		"next": i18n.T("next.reassigned"),
	}, nil
}

// asRevisionMismatch 取乐观锁冲突（保持 app 包不直接依赖错误链细节）。
func asRevisionMismatch(err error) *db.RevisionMismatch {
	if err == nil {
		return nil
	}
	if mm, ok := err.(*db.RevisionMismatch); ok {
		return mm
	}
	return nil
}

// InterruptInput 是 `maw interrupt` 的入参。
type InterruptInput struct {
	TaskID string
	// ExpectedRevision 必填：中断是破坏性动作，猜版本号就是猜现场。
	ExpectedRevision int
	Reason           string
}

// Interrupt 由 Lead/中枢请求中止某个任务的执行：任务转 blocked（可恢复），
// 并留下 task_interrupted 事件；不改 write_scope、不删 worktree、不杀进程。
//
// 为什么是 blocked 而不是 cancelled：中断是「先停手，等人处置」，
// cancelled 表示这道题不做了，两者混用会让审计看不出谁在等谁。
func Interrupt(cwd string, in InterruptInput) (Result, error) {
	taskID := strings.TrimSpace(in.TaskID)
	if taskID == "" {
		return nil, &errx.Error{Msg: i18n.T("err.reassign_task"), Next: i18n.T("next.reassign_task"), Code: 1}
	}
	if in.ExpectedRevision < 0 {
		return nil, &errx.Error{Msg: i18n.T("err.revision_required"), Next: i18n.T("next.include_revision"), Code: 1}
	}
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	if err := identity.RequireLeadAction(who, pc.Config, "interrupt"); err != nil {
		return nil, leadErr(err)
	}

	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "interrupted_by:" + who
	}
	var (
		fromStatus string
		revision   int
	)
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, e := db.GetTask(tx, pc.ID(), taskID)
		if e != nil {
			return e
		}
		if t == nil {
			return &errx.Error{
				Msg:  i18n.T("err.unknown_task") + ": " + taskID,
				Next: i18n.T("next.task_list"),
				Code: 1,
			}
		}
		fromStatus = t.Status
		if !machine.ActiveOwner[t.Status] {
			return &errx.Error{
				Msg:  i18n.T("err.interrupt_inactive") + ": " + t.Status,
				Next: i18n.T("next.interrupt_inactive"),
				Code: 1,
			}
		}
		updated, e := db.UpdateTaskCAS(tx, pc.ID(), taskID, in.ExpectedRevision, func(x *db.Task) error {
			if err := machine.AssertTransition(x.Status, "blocked"); err != nil {
				return err
			}
			x.PrevStatus = x.Status
			x.Status = "blocked"
			x.BlockedReason = reason
			return nil
		})
		if e != nil {
			if mm := asRevisionMismatch(e); mm != nil {
				return &errx.Error{
					Msg: i18n.T("err.reassign_revision_mismatch") + ": " +
						i18n.T("next.actual_revision") + "=" + strconv.Itoa(mm.Actual),
					Next: i18n.T("next.reassign_reread"),
					Code: 1,
				}
			}
			return e
		}
		revision = updated.Revision
		return db.AppendEvent(tx, pc.ID(), who, "task_interrupted", taskID, map[string]any{
			"from": fromStatus, "to": "blocked", "reason": reason,
			"expected_revision": in.ExpectedRevision, "revision": revision,
		})
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	return Result{
		"ok": true, "project_id": pc.ID(), "task_id": taskID,
		"from": fromStatus, "status": "blocked", "reason": reason,
		"revision": revision, "actor": who,
		"next": i18n.T("next.interrupted"),
	}, nil
}

// leadErr 把 identity 的鉴权失败翻成带稳定码的错误。
func leadErr(err error) error {
	if v, ok := err.(*identity.LeadViolation); ok {
		return &errx.Error{
			Msg:  i18n.T("err.not_lead") + ": " + v.Error(),
			Next: i18n.T("next.lead_required"),
			Code: 1,
		}
	}
	return err
}
