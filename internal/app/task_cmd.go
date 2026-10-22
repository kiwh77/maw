package app

import (
	"database/sql"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
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
		return nil, errx.New("unknown task "+id, "maw task list")
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
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return errx.New("unknown task "+taskID, "")
		}
		if err := identity.RequireOwner(who, t); err != nil {
			return err
		}
		from := t.Status
		t.PrevStatus = t.Status
		t.Status = "blocked"
		t.BlockedReason = message
		t.Revision++
		_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", taskID, map[string]any{"from": from, "to": "blocked", "reason": message})
		return db.UpsertTask(tx, t)
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	return Result{"ok": true, "task_id": taskID, "project_id": pc.ID(), "next": "maw task unblock " + taskID}, nil
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
			return errx.New("unknown task "+taskID, "")
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
