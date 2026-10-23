package resources

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/nodes"
)

// API 是任务／节点配置写资源。依赖必须注入；本包不打开数据库。
type API struct {
	Tasks  Tasks
	Nodes  Nodes
	Probes Probes
	Auth   *localauth.Authenticator
}

// Handler 返回可挂载的 /api/v1 写树（不含根路由装配）。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{project_id}/tasks", a.handleCreateTask)
	mux.HandleFunc("PATCH /api/v1/tasks/{instance_id}", a.handleUpdateTask)
	mux.HandleFunc("POST /api/v1/projects/{project_id}/nodes", a.handleRegisterNode)
	mux.HandleFunc("PATCH /api/v1/nodes/{node_id}", a.handleUpdateNode)
	mux.HandleFunc("POST /api/v1/nodes/{node_id}/probes", a.handleProbeNode)

	var h http.Handler = mux
	if a.Auth != nil {
		h = a.Auth.Middleware(a.Auth.RequireScope(localauth.ScopeWrite, mux))
	}
	return h
}

func (a *API) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	projectID := r.PathValue("project_id")
	if !model.ProjectID(projectID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_project_id", "project_id 非法", nil)
		return
	}
	var body struct {
		DisplayID  string   `json:"display_id"`
		Title      string   `json:"title"`
		Deps       []string `json:"deps"`
		WriteScope []string `json:"write_scope"`
		ReadScope  []string `json:"read_scope"`
		Acceptance []string `json:"acceptance"`
		Priority   int      `json:"priority"`
		MergeOrder int      `json:"merge_order"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if a.Tasks == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "Tasks 用例未注入", nil)
		return
	}
	out, err := a.Tasks.Create(r.Context(), TaskCreateRequest{
		ProjectID: model.ProjectID(projectID), DisplayID: model.DisplayID(body.DisplayID),
		Title: body.Title, Deps: body.Deps, WriteScope: body.WriteScope, ReadScope: body.ReadScope,
		Acceptance: body.Acceptance, Priority: body.Priority, MergeOrder: body.MergeOrder,
		IdempotencyKey: key,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	status := http.StatusCreated
	if out.Replay {
		status = http.StatusOK
	}
	w.Header().Set("ETag", etag(int64(out.Revision)))
	writeOK(w, status, map[string]any{
		"task_instance": out.InstanceID, "display_id": out.DisplayID,
		"task_version": out.Version, "revision": out.Revision,
		"idempotent_replay": out.Replay,
	})
}

func (a *API) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	rev, ok := parseIfMatch(w, r)
	if !ok {
		return
	}
	instanceID := r.PathValue("instance_id")
	if !model.InstanceID(instanceID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_instance_id", "必须是 ti_ 形态", nil)
		return
	}
	var body struct {
		Title      string   `json:"title"`
		Deps       []string `json:"deps"`
		WriteScope []string `json:"write_scope"`
		ReadScope  []string `json:"read_scope"`
		Acceptance []string `json:"acceptance"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if a.Tasks == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "Tasks 用例未注入", nil)
		return
	}
	out, err := a.Tasks.UpdateDefinition(r.Context(), TaskUpdateRequest{
		InstanceID: model.InstanceID(instanceID), ExpectedRevision: model.Revision(rev),
		Title: body.Title, Deps: body.Deps, WriteScope: body.WriteScope, ReadScope: body.ReadScope,
		Acceptance: body.Acceptance, IdempotencyKey: key,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	w.Header().Set("ETag", etag(int64(out.Revision)))
	writeOK(w, http.StatusOK, map[string]any{
		"task_instance": out.InstanceID, "task_version": out.Version,
		"revision": out.Revision, "idempotent_replay": out.Replay,
	})
}

func (a *API) handleRegisterNode(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	projectID := r.PathValue("project_id")
	raw, reg, ok := decodeNodeRegistration(w, r, projectID)
	if !ok {
		return
	}
	_ = raw
	if a.Nodes == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "Nodes 用例未注入", nil)
		return
	}
	out, err := a.Nodes.Register(r.Context(), NodeWriteRequest{
		Registration: reg, IdempotencyKey: key,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	status := http.StatusCreated
	if out.Replay {
		status = http.StatusOK
	}
	w.Header().Set("ETag", etag(int64(out.Snapshot.Revision)))
	writeOK(w, status, map[string]any{
		"node": out.Snapshot, "idempotent_replay": out.Replay,
	})
}

func (a *API) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	rev, ok := parseIfMatch(w, r)
	if !ok {
		return
	}
	nodeID := r.PathValue("node_id")
	if !model.NodeID(nodeID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_node_id", "node_id 非法", nil)
		return
	}
	_, reg, ok := decodeNodeRegistration(w, r, "")
	if !ok {
		return
	}
	reg.Config.NodeID = model.NodeID(nodeID)
	if a.Nodes == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "Nodes 用例未注入", nil)
		return
	}
	out, err := a.Nodes.UpdateConfig(r.Context(), NodeWriteRequest{
		Registration: reg, ExpectedRevision: model.Revision(rev), IdempotencyKey: key,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	w.Header().Set("ETag", etag(int64(out.Snapshot.Revision)))
	writeOK(w, http.StatusOK, map[string]any{
		"node": out.Snapshot, "idempotent_replay": out.Replay,
	})
}

func (a *API) handleProbeNode(w http.ResponseWriter, r *http.Request) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	rev, ok := parseIfMatch(w, r)
	if !ok {
		return
	}
	nodeID := r.PathValue("node_id")
	if !model.NodeID(nodeID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_node_id", "node_id 非法", nil)
		return
	}
	if a.Probes == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "Probes 用例未注入", nil)
		return
	}
	actor := ""
	if id, ok := localauth.IdentityFrom(r.Context()); ok {
		actor = id.Actor
	}
	out, err := a.Probes.Accept(r.Context(), ProbeRequest{
		NodeID: model.NodeID(nodeID), ExpectedRevision: model.Revision(rev),
		IdempotencyKey: key, Actor: actor,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/operations/"+string(out.OperationID))
	writeOK(w, http.StatusAccepted, map[string]any{
		"operation": map[string]any{
			"operation_id": out.OperationID,
			"status":       "queued",
			"action":       "probe",
			"resource":     "/api/v1/nodes/" + nodeID + "/probes",
		},
		"idempotent_replay": out.Replay,
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_json", "请求体不是合法 JSON", nil)
		return false
	}
	return true
}

func decodeNodeRegistration(w http.ResponseWriter, r *http.Request, projectID string) (map[string]any, nodes.Registration, bool) {
	rawBytes, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_json", "无法读取请求体", nil)
		return nil, nodes.Registration{}, false
	}
	var raw map[string]any
	if err := json.Unmarshal(rawBytes, &raw); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_json", "请求体不是合法 JSON", nil)
		return nil, nodes.Registration{}, false
	}
	if err := rejectCredentials(raw); err != nil {
		mapUseCaseErr(w, err)
		return nil, nodes.Registration{}, false
	}
	var reg nodes.Registration
	if err := json.Unmarshal(rawBytes, &reg); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_json", "节点配置字段不合法", nil)
		return nil, nodes.Registration{}, false
	}
	if projectID != "" {
		reg.ProjectID = projectID
	}
	if strings.TrimSpace(string(reg.Config.NodeID)) == "" {
		if id, _ := raw["node_id"].(string); id != "" {
			reg.Config.NodeID = model.NodeID(id)
		}
	}
	return raw, reg, true
}
