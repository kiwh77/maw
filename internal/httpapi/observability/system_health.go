package observability

import (
	"net/http"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/projection"
)

// systemHealthResponse 是自愈／系统健康报表（`maw.lead.selfheal/v1` §6）。
//
// health 内部已带每项指标的分子分母与 quality；外层只补窗口、水位与完整性，
// 让前端能区分「确实为 0」与「无法计量」。
type systemHealthResponse struct {
	meta
	Result
	Health *analytics.SystemHealthReport `json:"health"`
}

// handleSystemHealth 返回窗口内的系统健康指标。
//
// 全局路由（/api/v1/system/health）与服务端工程作用域（/projects/{id}/system/health）
// 走同一个处理函数：空 project_id 表示全部工程，聚合在 analytics 层完成。
// 指标直接读事实表，没有投影补算，因此完整性默认 complete，只有在确实
// 缺少维度证据时才追加 Missing。
func (a *API) handleSystemHealth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := rejectLegacyPagination(q); err != nil {
		mapErr(w, err)
		return
	}
	projectID := strings.TrimSpace(r.PathValue("project_id"))
	spec, err := parseWindow(q, a.now(), 24*time.Hour)
	if err != nil {
		mapErr(w, err)
		return
	}
	scope := Scope{ProjectID: projectID}
	window := windowOf(spec)

	if a.SystemHealth == nil {
		capability := capabilityOf("system_health", false, "source_not_configured")
		res := unsupportedResult(capability, scope, window, []Unsupported{{
			Capability: "system_health", Reason: "source_not_configured",
			Detail: "未注入系统健康数据源",
		}})
		writeJSON(w, http.StatusOK, systemHealthResponse{meta: meta{OK: true}, Result: res})
		return
	}

	page, err := a.SystemHealth.SystemHealth(r.Context(), SystemHealthQuery{
		ProjectID: projectID,
		Window:    spec.Window,
		Loc:       spec.Loc,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	if page.Report == nil {
		mapErr(w, invalidField(codeInvalidWindow, "window", "系统健康数据源返回空报表"))
		return
	}

	missing := []Missing{}
	if page.Report.Agents.NodeSuccessRate.Quality == model.QualityUnknown {
		missing = append(missing, Missing{
			Section: "agents.node_success_rate", Reason: "source_dimension_absent",
			Detail: "runs 表没有节点归属字段，节点成功率在 v1 不计量",
		})
	}
	res := newResult(capabilityOf("system_health", true, ""), scope, window, page.Watermark,
		projection.CompletenessComplete, nil, missing, nil)
	writeJSON(w, http.StatusOK, systemHealthResponse{
		meta:   meta{OK: true},
		Result: res,
		Health: page.Report,
	})
}
