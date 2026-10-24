package observability

import (
	"net/http"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/projection"
)

// sessionsResponse 是会话与轮次下钻（F37）。
type sessionsResponse struct {
	meta
	Result
	Items []SessionItem `json:"items"`
	// NextCursor 为 null 表示最后一页。
	NextCursor any `json:"next_cursor"`
	Count      int `json:"count"`
}

// handleSessions 返回窗口内已关联的会话与轮次。
//
// 与时间轴的分工：时间轴按任务实例与运行索引，会话按来源会话索引；
// 一个任务可以跨会话，一个会话也可以关联多个任务，两者靠 run_source_links 对上，
// 所以这里不做「按任务拼历史」的推断。
func (a *API) handleSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := rejectLegacyPagination(q); err != nil {
		mapErr(w, err)
		return
	}
	projectID := strings.TrimSpace(r.PathValue("project_id"))
	if projectID == "" {
		mapErr(w, &ErrNotFound{Resource: "project", Message: "缺少工程身份"})
		return
	}
	limit, err := parseLimit(q)
	if err != nil {
		mapErr(w, err)
		return
	}
	spec, err := parseWindow(q, a.now(), 7*24*time.Hour)
	if err != nil {
		mapErr(w, err)
		return
	}
	sourceNS, err := parseSourceNS(q)
	if err != nil {
		mapErr(w, err)
		return
	}
	scope := Scope{ProjectID: projectID, SourceNS: sourceNS}
	window := windowOf(spec)

	if a.Sessions == nil {
		res := unsupportedResult(capabilityOf("sessions", false, "source_not_configured"),
			scope, window, []Unsupported{{
				Capability: "sessions", Reason: "source_not_configured",
				Detail: "未注入会话数据源",
			}})
		writeJSON(w, http.StatusOK, sessionsResponse{meta: meta{OK: true}, Result: res,
			Items: []SessionItem{}})
		return
	}

	page, err := a.Sessions.Sessions(r.Context(), SessionQuery{
		ProjectID: projectID,
		SourceNS:  sourceNS,
		Window:    spec.Window,
		Limit:     limit,
		Cursor:    strings.TrimSpace(q.Get("cursor")),
	})
	if err != nil {
		mapErr(w, err)
		return
	}

	completeness := page.Completeness
	if completeness == "" {
		completeness = projection.CompletenessUnknown
	}
	missing := page.Missing
	if missing == nil {
		missing = []Missing{}
	}
	var wm *Watermark
	if page.Watermark != nil {
		wm = page.Watermark
	}
	items := page.Items
	if items == nil {
		items = []SessionItem{}
	}
	res := newResult(capabilityOf("sessions", true, ""), scope, window, wm,
		completeness, nil, missing, nil)
	writeJSON(w, http.StatusOK, sessionsResponse{
		meta:       meta{OK: true},
		Result:     res,
		Items:      items,
		NextCursor: cursorOrNil(page.Next),
		Count:      len(items),
	})
}
