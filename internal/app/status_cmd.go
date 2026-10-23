package app

import (
	"database/sql"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
)

// StatusReportInput is a heartbeat / progress injection from any local agent.
type StatusReportInput struct {
	ProjectID string
	Actor     string
	TaskID    string
	Message   string
	Phase     string
	Pct       *int
	Client    string
	Status    string // agent status: idle|working (default working when message set)
}

// StatusReport upserts agent presence and appends an event into the global DB.
func StatusReport(cwd string, in StatusReportInput) (Result, error) {
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	if err := db.Init(gdb); err != nil {
		return nil, err
	}

	projectID := strings.TrimSpace(in.ProjectID)
	if projectID == "" {
		pc, err := resolveProject(cwd)
		if err != nil {
			return nil, i18n.E("err.resolve_project", "next.init_or_project")
		}
		projectID = pc.ID()
	}

	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		var e error
		actor, e = identity.Resolve(cwd, "")
		if e != nil {
			return nil, e
		}
	}

	agentStatus := strings.TrimSpace(in.Status)
	if agentStatus == "" {
		if in.Message != "" || in.TaskID != "" {
			agentStatus = "working"
		} else {
			agentStatus = "idle"
		}
	}

	eventType := "agent_status"
	if in.Message != "" {
		eventType = "external_progress"
	}

	data := map[string]any{}
	if in.Message != "" {
		data["message"] = in.Message
	}
	if in.Phase != "" {
		data["phase"] = in.Phase
	}
	if in.Pct != nil {
		data["pct"] = *in.Pct
	}
	if in.Client != "" {
		data["client"] = in.Client
	}

	now := db.UTCNow()
	var eventID int64
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		p, e := db.GetProject(tx, projectID)
		if e != nil {
			return e
		}
		if p == nil {
			return i18n.E("err.project_missing", "next.run_init", projectID)
		}
		existing, _ := db.GetAgent(tx, projectID, actor)
		a := &db.Agent{
			ProjectID:    projectID,
			ID:           actor,
			Role:         "worker",
			Status:       agentStatus,
			TaskID:       in.TaskID,
			RegisteredAt: now,
			LastSeen:     now,
			Meta:         map[string]any{},
		}
		if existing != nil {
			a.RegisteredAt = existing.RegisteredAt
			a.Role = existing.Role
			a.Worktree = existing.Worktree
			a.Branch = existing.Branch
			a.Meta = existing.Meta
			if in.TaskID == "" {
				a.TaskID = existing.TaskID
			}
		}
		if err := db.UpsertAgent(tx, a); err != nil {
			return err
		}
		if in.TaskID != "" {
			t, e := db.GetTask(tx, projectID, in.TaskID)
			if e != nil {
				return e
			}
			if t != nil {
				t.Heartbeat = now
				if t.Owner == "" {
					t.Owner = actor
				}
				if err := db.UpsertTask(tx, t); err != nil {
					return err
				}
			}
		}
		if err := db.AppendEvent(tx, projectID, actor, eventType, in.TaskID, data); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM events`).Scan(&eventID)
	})
	if err != nil {
		return nil, err
	}
	return Result{
		"ok": true, "project_id": projectID, "agent": actor,
		"task_id": in.TaskID, "event_id": eventID, "event_type": eventType,
		"next": i18n.T("next.status_visible"),
	}, nil
}

// LoadEventFeed returns events with id > since for the status hub.
func LoadEventFeed(gdb string, since int64, projectID, agent string) ([]*db.Event, error) {
	var out []*db.Event
	err := db.WithTx(gdb, func(tx *sql.Tx) error {
		var e error
		out, e = db.ListEventsSince(tx, since, projectID, agent, 200)
		return e
	})
	return out, err
}
