package app

import (
	"database/sql"
	"strings"

	"github.com/kiwh77/maw/internal/board"
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
	LogPath   string // optional absolute session log path (Cursor/Codex/WorkBuddy, …)
	// Kind 是协作消息分型 progress|question|blocker|final；省略时按契约默认 progress。
	Kind string
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

	kind, kindErr := board.NormalizeKind(in.Kind)
	if kindErr != nil {
		return nil, i18n.EW("err.invalid_kind", "next.use_kind_enum", kindErr)
	}

	logPath := strings.TrimSpace(in.LogPath)
	if logPath != "" {
		abs, verr := board.ValidateLogPath(logPath)
		if verr != nil {
			return nil, i18n.EW("err.log_path", "next.fix_log_path", verr)
		}
		logPath = abs
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
	if logPath != "" {
		data["log_path"] = logPath
	}
	// 只有真的产生了协作消息（带了说明）才写 kind：纯心跳不发明分型（契约 §2.2）。
	if in.Message != "" {
		data["kind"] = kind
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
		// 先把目标任务读出来：占不占得上，决定这次上报能写什么。
		var target *db.Task
		if in.TaskID != "" {
			var e error
			target, e = db.GetTask(tx, projectID, in.TaskID)
			if e != nil {
				return e
			}
		}
		// 心跳只能续自己持有的任务：旁路／进度上报不取得所有权。
		// 以前这里在 owner 为空时顺手把任务记给上报者，相当于一条 progress 就能领走
		// 任务——它既没过依赖与写盘检查，也没人知道它领了。
		owns := target != nil && target.Owner == actor
		if target != nil && !owns {
			data["ownership"] = "not_granted"
			data["task_owner"] = target.Owner
			data["task_status"] = target.Status
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
		// 没拿到所有权就不把自己写成该任务的执行者：看板上不该出现两个人同时在做一件事。
		if target != nil && !owns {
			a.TaskID = ""
			if existing != nil {
				a.TaskID = existing.TaskID
			}
		}
		a.Meta = mergeAgentMeta(a.Meta, logPath)
		if err := db.UpsertAgent(tx, a); err != nil {
			return err
		}
		if owns {
			target.Heartbeat = now
			if err := db.UpsertTask(tx, target); err != nil {
				return err
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
	res := Result{
		"ok": true, "project_id": projectID, "agent": actor,
		"task_id": in.TaskID, "event_id": eventID, "event_type": eventType,
		"next": i18n.T("next.status_visible"),
	}
	if in.Message != "" {
		res["kind"] = kind
	}
	if logPath != "" {
		res["log_path"] = logPath
	}
	return res, nil
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
