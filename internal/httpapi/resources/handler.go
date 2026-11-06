package resources

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/nodes"
)

// API 是任务／节点配置写资源。依赖必须注入；本包不打开数据库。
type API struct {
	Tasks      Tasks
	Nodes      Nodes
	Probes     Probes
	NodeChecks NodeChecks
	Projects   Projects
	Auth       *localauth.Authenticator
}

// Handler 返回可挂载的 /api/v1 写树（不含根路由装配）。
//
// 路径与方法分开注册：同一个路径换错方法也要回 **JSON 错误码**（method_not_allowed），
// 而不是 net/http ServeMux 的纯文本 405。未接线的写资源统一回 501 unsupported，
// 让「中枢声明有写树」与「这台机器的写入口到底通不通」对得上。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/projects/{project_id}/tasks", a.handleCreateTask)
	mux.HandleFunc("/api/v1/tasks/{instance_id}", a.handleUpdateTask)
	mux.HandleFunc("/api/v1/projects/{project_id}/nodes", a.handleRegisterNode)
	mux.HandleFunc("/api/v1/nodes/{node_id}", a.handleUpdateNode)
	mux.HandleFunc("/api/v1/nodes/{node_id}/probes", a.handleProbeNode)
	mux.HandleFunc("/api/v1/nodes/{node_id}/checks", a.handleCheckNode)
	mux.HandleFunc("/api/v1/projects/{project_id}", a.handleProject)
	// 写树兜底：未知写路径回 JSON not_found，不再让 404 落到纯文本页。
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "未知的 /api/v1 写路径", nil)
	})

	var h http.Handler = mux
	if a.Auth != nil {
		h = a.Auth.Middleware(a.Auth.RequireScope(localauth.ScopeWrite, mux))
	}
	return h
}

// requireWriteMethod 把方法不匹配写成 JSON 405，并显式给出 Allow。
func requireWriteMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed",
		"该写入口只接受 "+method, map[string]any{"allow": method})
	return false
}

// writeNotWired 表示写树已挂载、但这台中枢没有注入对应用例。
// 这是 unsupported（501），不是 internal_error（500）：请求本身没毛病，
// 只是本中枢只提供定义/登记类写资源的一部分，受管执行仍走 CLI。
func writeNotWired(w http.ResponseWriter, name string) {
	writeErr(w, http.StatusNotImplemented, "unsupported",
		name+" 写入口未在本中枢接线；受管执行请改用 CLI", map[string]any{"use": "cli"})
}

// handleProject 工程写入口：DELETE 移除，PATCH 更新属性／策略。
func (a *API) handleProject(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodDelete:
		a.handleRemoveProject(w, r)
	case http.MethodPatch:
		a.handleUpdateProject(w, r)
	default:
		w.Header().Set("Allow", "DELETE, PATCH")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed",
			"该写入口只接受 DELETE 或 PATCH", map[string]any{"allow": "DELETE, PATCH"})
	}
}

// handleUpdateProject 更新工程名称／路径／执行策略。
func (a *API) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("project_id")
	if !model.ProjectID(projectID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_project_id", "project_id 必须是 16 位十六进制", nil)
		return
	}
	if a.Projects == nil {
		writeNotWired(w, "工程更新")
		return
	}
	var body struct {
		Name         *string   `json:"name"`
		Path         *string   `json:"path"`
		MainBranch   *string   `json:"main_branch"`
		MaxParallel  *int      `json:"max_parallel"`
		AutoEnabled  *bool     `json:"auto_enabled"`
		Limit        *int      `json:"limit"`
		AllowedNodes *[]string `json:"allowed_nodes"`
		WorkerNode   *string   `json:"worker_node"`
		LLMNode      *string   `json:"llm_node"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	actor := ""
	if id, ok := localauth.IdentityFrom(r.Context()); ok {
		actor = id.Actor
	}
	out, err := a.Projects.Update(r.Context(), ProjectUpdateRequest{
		ProjectID: projectID, Name: body.Name, Path: body.Path,
		MainBranch: body.MainBranch, MaxParallel: body.MaxParallel,
		AutoEnabled: body.AutoEnabled, Limit: body.Limit,
		AllowedNodes: body.AllowedNodes, WorkerNode: body.WorkerNode,
		LLMNode: body.LLMNode, Actor: actor,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{
		"project_id": out.ProjectID, "project_name": out.ProjectName,
		"project_path": out.ProjectPath, "main_branch": out.MainBranch,
		"max_parallel": out.MaxParallel, "config_path": out.ConfigPath,
		"config_written": out.ConfigWriten,
	})
}

// handleRemoveProject 移除工程登记。
//
// DELETE 本身幂等，所以不要求 Idempotency-Key；但删除范围必须由用例回报，
// 界面不能自行推断「删成功了」——只有 200 且带 removed 计数才算。
func (a *API) handleRemoveProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("project_id")
	if !model.ProjectID(projectID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_project_id", "project_id 必须是 16 位十六进制", nil)
		return
	}
	if a.Projects == nil {
		writeNotWired(w, "工程移除")
		return
	}
	force := false
	switch r.URL.Query().Get("force") {
	case "1", "true":
		force = true
	}
	actor := ""
	if id, ok := localauth.IdentityFrom(r.Context()); ok {
		actor = id.Actor
	}
	out, err := a.Projects.Remove(r.Context(), ProjectRemovalRequest{
		ProjectID: projectID, Force: force, Actor: actor,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{
		"project_id":   out.ProjectID,
		"project_name": out.ProjectName,
		"project_path": out.ProjectPath,
		"summary":      out.Summary,
		"removed":      out.Removed,
		"retained":     out.Retained,
	})
}

func (a *API) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if !requireWriteMethod(w, r, http.MethodPost) {
		return
	}
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
		writeNotWired(w, "任务定义创建")
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
	if !requireWriteMethod(w, r, http.MethodPatch) {
		return
	}
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
		writeNotWired(w, "任务定义更新")
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
	if !requireWriteMethod(w, r, http.MethodPost) {
		return
	}
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
		writeNotWired(w, "节点登记")
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
	switch r.Method {
	case http.MethodPatch:
		a.handlePatchNode(w, r)
	case http.MethodDelete:
		a.handleRemoveNode(w, r)
	default:
		w.Header().Set("Allow", "PATCH, DELETE")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed",
			"该写入口只接受 PATCH 或 DELETE", map[string]any{"allow": "PATCH, DELETE"})
	}
}

func (a *API) handlePatchNode(w http.ResponseWriter, r *http.Request) {
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
		writeNotWired(w, "节点配置更新")
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

func (a *API) handleRemoveNode(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("node_id")
	if !model.NodeID(nodeID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_node_id", "node_id 非法", nil)
		return
	}
	if a.Nodes == nil {
		writeNotWired(w, "节点删除")
		return
	}
	if err := a.Nodes.Remove(r.Context(), model.NodeID(nodeID)); err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{
		"node_id": nodeID, "removed": true,
	})
}

func (a *API) handleProbeNode(w http.ResponseWriter, r *http.Request) {
	if !requireWriteMethod(w, r, http.MethodPost) {
		return
	}
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
		writeNotWired(w, "节点探测")
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

// handleCheckNode 同步执行一次 CLI 环境自检（看板「测试环境」按钮）。
//
// 与 /probes 的异步受理不同，这里真的会启动 CLI 做 --version 探测，
// 并把结果写回节点 meta；界面拿到 200 即可直接渲染，不需要轮询 Operation。
func (a *API) handleCheckNode(w http.ResponseWriter, r *http.Request) {
	if !requireWriteMethod(w, r, http.MethodPost) {
		return
	}
	nodeID := r.PathValue("node_id")
	if !model.NodeID(nodeID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_node_id", "node_id 非法", nil)
		return
	}
	if a.NodeChecks == nil {
		writeNotWired(w, "节点环境自检")
		return
	}
	// 单次自检最长 20s：最坏情况是二进制能起但挂住，超时按失败回写。
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, err := a.NodeChecks.Check(ctx, model.NodeID(nodeID))
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{
		"node_id":    out.NodeID,
		"ok":         out.OK,
		"version":    out.Version,
		"latency_ms": out.LatencyMS,
		"checked_at": out.CheckedAt,
		"checks":     out.Checks,
		"error":      out.Error,
		"meta":       out.Meta,
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
