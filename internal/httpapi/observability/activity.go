package observability

import (
	"net/http"
	"strings"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/projection"
)

// activityResponse 是全天活动热图（F34：按日期看工程活动分布）。
type activityResponse struct {
	meta
	Result
	// Day 是热图对应的本地日期（YYYY-MM-DD）。
	Day string `json:"day"`
	// Hours 只包含**有观测**的小时桶；没有桶的小时由前端按 completeness 判缺测。
	Hours []analytics.HourBucket `json:"hours"`
	// PresentHours 是有桶的小时数：0 且 completeness≠complete 时是缺测，不是零活动。
	PresentHours int                        `json:"present_hours"`
	Totals       *analytics.TimelineSummary `json:"totals,omitempty"`
}

// handleActivity 返回某个本地日的 24 小时活动桶。
//
// 热图与统计同源：小时桶由 analytics 按本地小时切分，秒数是「同身份并集后相加」，
// 所以各桶之和等于当天合计，不会因为跨桶重复计时而放大。
func (a *API) handleActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := rejectLegacyPagination(q); err != nil {
		mapErr(w, err)
		return
	}
	// 空工程表示「全部工程」：全局路由不带 project_id，由数据源聚合。
	projectID := strings.TrimSpace(r.PathValue("project_id"))
	spec, err := parseDayWindow(q, a.now())
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
	day := dayOf(spec)

	// 热图同时需要区间（时长）与统计（分桶）：缺任一项都只能报不支持，
	// 不能拿一半数据画出一张看起来完整的热图。
	if a.Stats == nil || a.Timeline == nil {
		reason := "source_not_configured"
		detail := "未注入统计数据源"
		if a.Stats != nil {
			detail = "未注入时间轴数据源"
		}
		capability := capabilityOf("activity", false, reason)
		res := unsupportedResult(capability, scope, window, []Unsupported{{
			Capability: "activity", Reason: reason, Detail: detail,
		}})
		writeJSON(w, http.StatusOK, activityResponse{meta: meta{OK: true}, Result: res,
			Day: day, Hours: []analytics.HourBucket{}})
		return
	}

	data, err := a.Timeline.Timeline(r.Context(), TimelineQuery{
		ProjectID: projectID, Window: spec.Window, Loc: spec.Loc, SourceNS: sourceNS,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	if len(data.Unsupported) > 0 {
		res := unsupportedResult(capabilityOf("activity", true, ""), scope, window, data.Unsupported)
		writeJSON(w, http.StatusOK, activityResponse{meta: meta{OK: true}, Result: res,
			Day: day, Hours: []analytics.HourBucket{}})
		return
	}
	report, err := a.Stats.Report(r.Context(), StatsQuery{
		ProjectID: projectID, SourceNS: sourceNS, Window: spec.Window, Loc: spec.Loc,
	}, data)
	if err != nil {
		mapErr(w, err)
		return
	}

	gaps := data.Gaps
	if gaps == nil {
		gaps = []Gap{}
	}
	completeness := completenessOf(data.Cursors.Completeness, gaps)
	if report != nil && report.Completeness != "" && report.Completeness != projection.CompletenessComplete {
		completeness = report.Completeness
	}
	missing := missingForGaps(gaps)
	missing = append(missing, data.Missing...)

	hours := []analytics.HourBucket{}
	var totals *analytics.TimelineSummary
	if report != nil {
		hours = append(hours, report.Timeline.Hours...)
		t := report.Timeline
		totals = &t
		if report.Usage.UsageUnknown {
			missing = append(missing, Missing{
				Section: "usage", Reason: "usage_unknown",
				Detail: "当天存在未知用量的轮次，用量合计不含这部分",
			})
		}
	}
	if len(hours) == 0 && completeness != projection.CompletenessComplete {
		// 一个桶都没有且完整性不是 complete：这是缺测，不能画成「全天零活动」。
		missing = append(missing, Missing{
			Section: "hours", Reason: "no_observed_buckets",
			Detail: "当天没有已观测的小时桶，且投影未标记为完整",
		})
	}

	wm := watermarkOf(data.Cursors.StateCursor, data.Cursors.ProjectionCursor, a.now(), gaps)
	res := newResult(capabilityOf("activity", true, ""), scope, window, wm,
		completeness, gaps, missing, data.Unsupported)
	writeJSON(w, http.StatusOK, activityResponse{
		meta:         meta{OK: true},
		Result:       res,
		Day:          day,
		Hours:        hours,
		PresentHours: len(hours),
		Totals:       totals,
	})
}
