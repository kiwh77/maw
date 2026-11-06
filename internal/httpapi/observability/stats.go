package observability

import (
	"net/http"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/projection"
)

// statsResponse 是工程与来源统计报表（F39–F43、F46）。
//
// report 内部已带 scope／watermark／completeness／quality 与证据计数；
// 外层再给一份类型化 status，是为了让前端不必解析报表就能决定展示方式。
type statsResponse struct {
	meta
	Result
	Report *analytics.Report `json:"report"`
}

// handleStats 返回窗口内的统计报表。
//
// 聚合全部由 internal/analytics 完成：时长按同身份并集、token 按 (turn_id, epoch)
// 去重、零分母比率记 0。HTTP 层不重算，只负责窗口、来源过滤与状态收口。
func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := rejectLegacyPagination(q); err != nil {
		mapErr(w, err)
		return
	}
	// 空工程表示「全部工程」：全局路由不带 project_id，由统计服务聚合。
	projectID := strings.TrimSpace(r.PathValue("project_id"))
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

	if a.Stats == nil {
		capability := capabilityOf("stats", false, "source_not_configured")
		res := unsupportedResult(capability, scope, window, []Unsupported{{
			Capability: "stats", Reason: "source_not_configured",
			Detail: "未注入统计数据源",
		}})
		writeJSON(w, http.StatusOK, statsResponse{meta: meta{OK: true}, Result: res})
		return
	}

	// 区间可选：没有时间轴数据源时仍能给出用量与交付统计，
	// 但必须显式说明时长分区缺测，不能把 0 秒当成「没有执行」。
	var data TimelineData
	missing := []Missing{}
	if a.Timeline == nil {
		missing = append(missing, Missing{
			Section: "timeline", Reason: "source_not_configured",
			Detail: "未注入时间轴数据源，时长与并发分区不可用",
		})
	} else {
		data, err = a.Timeline.Timeline(r.Context(), TimelineQuery{
			ProjectID: projectID, Window: spec.Window, Loc: spec.Loc, SourceNS: sourceNS,
		})
		if err != nil {
			mapErr(w, err)
			return
		}
		if len(data.Unsupported) > 0 {
			res := unsupportedResult(capabilityOf("stats", true, ""), scope, window, data.Unsupported)
			writeJSON(w, http.StatusOK, statsResponse{meta: meta{OK: true}, Result: res})
			return
		}
	}

	report, err := a.Stats.Report(r.Context(), StatsQuery{
		ProjectID: projectID, SourceNS: sourceNS, Window: spec.Window, Loc: spec.Loc,
	}, data)
	if err != nil {
		mapErr(w, err)
		return
	}
	if report == nil {
		mapErr(w, &ErrInvalid{Code: "internal_error", Field: "report", Message: "统计数据源返回空报表"})
		return
	}

	gaps := data.Gaps
	if gaps == nil {
		gaps = []Gap{}
	}
	completeness := completenessOf(report.Completeness, gaps)
	if completeness == "" {
		completeness = projection.CompletenessUnknown
	}
	missing = append(missing, missingForGaps(gaps)...)
	missing = append(missing, data.Missing...)
	if report.Usage.UsageUnknown {
		missing = append(missing, Missing{
			Section: "usage", Reason: "usage_unknown",
			Detail: "窗口内存在未知用量的轮次，token 合计不含这部分",
		})
	}
	if report.Usage.Coverage.ObservedDenom > 0 && report.Usage.Coverage.ObservedTurns < report.Usage.Coverage.ObservedDenom {
		missing = append(missing, Missing{
			Section: "usage.coverage", Reason: "coverage_incomplete",
			Detail: "部分轮次没有任何用量记账",
		})
	}

	var wm *Watermark
	if a.Timeline != nil {
		wm = watermarkOf(data.Cursors.StateCursor, data.Cursors.ProjectionCursor, a.now(), gaps)
	}
	res := newResult(capabilityOf("stats", true, ""), scope, window, wm,
		completeness, gaps, missing, nil)
	writeJSON(w, http.StatusOK, statsResponse{
		meta: meta{OK: true}, Result: res, Report: report,
	})
}
