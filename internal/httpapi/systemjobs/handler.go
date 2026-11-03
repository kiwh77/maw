package systemjobs

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/systemagent"
)

// API 挂载系统 Agent 作业读写。
type API struct {
	Store  *db.Store
	Auth   *localauth.Authenticator
	Config func(projectID string) (config.SystemAgent, error)
}

// Handler 返回 /api/v1 下的 system-jobs 路由。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/system-jobs", a.listJobs)
	mux.HandleFunc("/api/v1/projects/", a.projectJobs)
	var h http.Handler = mux
	if a.Auth != nil {
		h = a.Auth.Middleware(a.Auth.RequireScope(localauth.ScopeWrite, mux))
		// list 允许 read；简化：统一要求 write（创建也是 write）
		_ = localauth.ScopeRead
	}
	return h
}

func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		localauth.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only", nil)
		return
	}
	status := r.URL.Query().Get("status")
	projectID := r.URL.Query().Get("project_id")
	var jobs []*db.SystemJob
	err := a.Store.Read(func(tx *sql.Tx) error {
		var e error
		jobs, e = db.ListSystemJobs(tx, projectID, status, 100)
		return e
	})
	if err != nil {
		localauth.WriteJSONError(w, http.StatusInternalServerError, "db_error", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jobs": jobsView(jobs)})
}

func (a *API) projectJobs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/projects/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[1] != "system-jobs" {
		http.NotFound(w, r)
		return
	}
	projectID := parts[0]
	switch r.Method {
	case http.MethodGet:
		var jobs []*db.SystemJob
		err := a.Store.Read(func(tx *sql.Tx) error {
			var e error
			jobs, e = db.ListSystemJobs(tx, projectID, r.URL.Query().Get("status"), 100)
			return e
		})
		if err != nil {
			localauth.WriteJSONError(w, http.StatusInternalServerError, "db_error", err.Error(), nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jobs": jobsView(jobs)})
	case http.MethodPost:
		var body struct {
			Kind    string `json:"kind"`
			TaskID  string `json:"task_id"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			localauth.WriteJSONError(w, http.StatusBadRequest, "bad_json", err.Error(), nil)
			return
		}
		cfg := config.SystemAgent{AutoKinds: []string{"unblock_assist", "task_split", "ops_run"}, MaxAttempts: 3}
		if a.Config != nil {
			if c, err := a.Config(projectID); err == nil {
				cfg = c
			}
		}
		kind := body.Kind
		if kind == "" {
			kind = systemagent.SystemKindUnblock
		}
		reason := body.Reason
		if reason == "" {
			reason = body.Message
		}
		decision := map[string]any{"message": body.Message}
		if kind == systemagent.SystemKindOpsRun {
			decision["ops_command"] = reason
		}
		var job *db.SystemJob
		err := a.Store.Write(func(tx *sql.Tx) error {
			instanceID := ""
			if body.TaskID != "" {
				if t, e := db.GetTask(tx, projectID, body.TaskID); e == nil && t != nil {
					instanceID = t.InstanceID
				}
			}
			var e error
			job, e = systemagent.Enqueue(tx, cfg, projectID, kind, body.TaskID, instanceID, reason, decision, true)
			return e
		})
		if err != nil {
			localauth.WriteJSONError(w, http.StatusBadRequest, "enqueue_failed", err.Error(), nil)
			return
		}
		if job == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deduped": true})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "job_id": job.JobID, "status": job.Status})
	default:
		localauth.WriteJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST", nil)
	}
}

func jobsView(jobs []*db.SystemJob) []map[string]any {
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		out = append(out, map[string]any{
			"job_id": j.JobID, "project_id": j.ProjectID, "kind": j.Kind, "status": j.Status,
			"task_id": j.TaskID, "reason": j.Reason, "attempts": j.Attempts,
			"created_at": j.CreatedAt, "updated_at": j.UpdatedAt,
			"decision": j.Decision, "result": j.Result,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
