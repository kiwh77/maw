package app

import (
	"database/sql"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
)

func TaskList(cwd string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	var tasks []*db.Task
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		tasks, e = db.ListTasks(tx, pc.ID())
		return e
	})
	return Result{"ok": true, "tasks": tasks, "project_id": pc.ID()}, err
}

func TaskShow(cwd, id string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	var t *db.Task
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		t, e = db.GetTask(tx, pc.ID(), id)
		return e
	})
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, i18n.E("err.unknown_task", "next.task_list", id)
	}
	return Result{"ok": true, "task": t, "project_id": pc.ID()}, nil
}

func Block(cwd, message, taskID string) (Result, error) {
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
	out, err := applyDeliveryCase(pc, deliveryCaseInput{
		Action:  deliveryActionBlock,
		TaskID:  taskID,
		Actor:   who,
		Message: message,
	})
	if err != nil {
		return nil, err
	}
	category := ClassifyBlockCategory(message)
	followups := suggestedFollowupsForBlock(message)
	_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, e := db.GetTask(tx, pc.ID(), taskID)
		if e != nil || t == nil {
			return e
		}
		if t.Extra == nil {
			t.Extra = map[string]any{}
		}
		t.Extra["block_category"] = category
		if len(followups) > 0 {
			t.Extra["suggested_followups"] = followups
		}
		t.Revision++
		return db.UpsertTask(tx, t)
	})
	_, _ = refreshBoard(pc)
	next := i18n.T("next.task_unblock", taskID)
	if len(followups) > 0 {
		next = followups[0]
	}
	return Result{
		"ok": true, "task_id": taskID, "delivery_id": out.DeliveryID,
		"project_id": pc.ID(), "block_category": category,
		"suggested_followups": followups, "next": next,
	}, nil
}

func Unblock(cwd, taskID string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	var prev string
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return i18n.E("err.unknown_task", "", taskID)
		}
		prev = t.PrevStatus
		if prev == "" {
			prev = "ready"
		}
		from := t.Status
		t.Status = prev
		t.BlockedReason = ""
		t.PrevStatus = ""
		t.Revision++
		_ = db.AppendEvent(tx, pc.ID(), "orchestrator", "status_changed", taskID, map[string]any{"from": from, "to": prev})
		return db.UpsertTask(tx, t)
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	return Result{"ok": true, "task_id": taskID, "status": prev, "project_id": pc.ID()}, nil
}
