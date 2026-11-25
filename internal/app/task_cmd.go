package app

import (
	"database/sql"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/machine"
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
	var (
		restored string
		note     string
	)
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return i18n.E("err.unknown_task", "", taskID)
		}
		if t.Status != "blocked" {
			return i18n.E("err.cannot_unblock", "next.task_list", taskID, t.Status)
		}
		prev := t.PrevStatus
		if prev == "" {
			prev = "ready"
		}
		cat := ""
		if t.Extra != nil {
			if v, ok := t.Extra["block_category"].(string); ok {
				cat = v
			}
		}
		if cat == "" {
			cat = ClassifyBlockCategory(t.BlockedReason)
		}
		target := prev
		// conflict／脏树类：若要回到 merging，须交付仍 merge-ready；否则落到 in_review 重交。
		if (cat == BlockCatConflict || strings.Contains(strings.ToLower(t.BlockedReason), "dirty")) &&
			prev == "merging" {
			ready := false
			if rec, e := latestDelivery(tx, pc.ID(), t); e == nil && rec != nil {
				if r, e2 := db.DeliveryReadiness(tx, string(rec.Delivery.DeliveryID)); e2 == nil {
					ready = r.PassingVerification && r.ApprovedReview &&
						r.StaleVerifications == 0 && !r.ReviewStale
				}
			}
			if !ready {
				target = "in_review"
				note = "delivery_not_merge_ready"
			}
		}
		if err := machine.AssertTransition(t.Status, target); err != nil {
			return err
		}
		from := t.Status
		t.Status = target
		t.BlockedReason = ""
		t.PrevStatus = ""
		if t.Extra != nil {
			delete(t.Extra, "block_category")
			delete(t.Extra, "suggested_followups")
		}
		t.Revision++
		payload := map[string]any{"from": from, "to": target}
		if note != "" {
			payload["note"] = note
		}
		_ = db.AppendEvent(tx, pc.ID(), "orchestrator", "status_changed", taskID, payload)
		restored = target
		return db.UpsertTask(tx, t)
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	res := Result{"ok": true, "task_id": taskID, "status": restored, "project_id": pc.ID()}
	if note == "delivery_not_merge_ready" {
		res["next"] = "交付证据已失效；请重新 maw submit 后再 maw merge --yes"
		res["note"] = note
	} else if restored == "merging" {
		res["next"] = "maw merge --yes"
	}
	return res, nil
}
