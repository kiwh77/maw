package resources

import (
	"net/http"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/localauth"
)

// 工作台「自动执行」开关与 Router（系统 Agent）配置的 HTTP 入口。
//
// 两条路径都是「读写同源」：GET 返回服务端判定的就绪结论，PUT 一次性写齐
// 所有相关配置。界面只表达意图（开／关、用哪个节点），不自己拼内部开关。

func (a *API) handleAutoExecution(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("project_id")
	if !model.ProjectID(projectID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_project_id", "project_id 必须是 16 位十六进制", nil)
		return
	}
	if a.AutoExecution == nil {
		writeNotWired(w, "自动执行开关")
		return
	}
	switch r.Method {
	case http.MethodGet:
		out, err := a.AutoExecution.Get(r.Context(), projectID)
		if err != nil {
			mapUseCaseErr(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"auto_execution": out})
	case http.MethodPut:
		var body struct {
			Enabled *bool   `json:"enabled"`
			NodeID  *string `json:"node_id"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		if body.Enabled == nil {
			writeErr(w, http.StatusUnprocessableEntity, "invalid_request",
				"enabled 必填：开关必须显式给出开或关", nil)
			return
		}
		nodeID := ""
		if body.NodeID != nil {
			nodeID = strings.TrimSpace(*body.NodeID)
		}
		out, err := a.AutoExecution.Set(r.Context(), AutoExecutionSetRequest{
			ProjectID: projectID,
			Enabled:   *body.Enabled,
			NodeID:    nodeID,
			Actor:     actorOf(r),
		})
		if err != nil {
			mapUseCaseErr(w, err)
			return
		}
		// 打开被拒（节点接不了活）不是 4xx：请求本身合法，是现场不满足。
		// 用 200 + applied=false + reasons 表达，界面据此解释原因并保持开关关闭。
		writeOK(w, http.StatusOK, map[string]any{"auto_execution": out})
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed",
			"该入口只接受 GET 或 PUT", map[string]any{"allow": "GET, PUT"})
	}
}

func (a *API) handleSystemAgent(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("project_id")
	if !model.ProjectID(projectID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_project_id", "project_id 必须是 16 位十六进制", nil)
		return
	}
	if a.SystemAgent == nil {
		writeNotWired(w, "Router 配置")
		return
	}
	switch r.Method {
	case http.MethodGet:
		out, err := a.SystemAgent.Get(r.Context(), projectID)
		if err != nil {
			mapUseCaseErr(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"system_agent": out})
	case http.MethodPut:
		var body struct {
			Enabled      *bool     `json:"enabled"`
			LLMNode      *string   `json:"llm_node"`
			WorkerNode   *string   `json:"worker_node"`
			LLMCommand   *[]string `json:"llm_command"`
			AutoKinds    *[]string `json:"auto_kinds"`
			OpsAllowlist *[]string `json:"ops_allowlist"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		out, err := a.SystemAgent.Update(r.Context(), SystemAgentSetRequest{
			ProjectID:    projectID,
			Enabled:      body.Enabled,
			LLMNode:      body.LLMNode,
			WorkerNode:   body.WorkerNode,
			LLMCommand:   body.LLMCommand,
			AutoKinds:    body.AutoKinds,
			OpsAllowlist: body.OpsAllowlist,
			Actor:        actorOf(r),
		})
		if err != nil {
			mapUseCaseErr(w, err)
			return
		}
		writeOK(w, http.StatusOK, map[string]any{"system_agent": out})
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed",
			"该入口只接受 GET 或 PUT", map[string]any{"allow": "GET, PUT"})
	}
}

// actorOf 取本机身份用于事件审计。
func actorOf(r *http.Request) string {
	if id, ok := localauth.IdentityFrom(r.Context()); ok {
		return id.Actor
	}
	return ""
}
