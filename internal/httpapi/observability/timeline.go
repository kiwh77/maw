package observability

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/projection"
)

// timelineResponse 是时间轴响应。
type timelineResponse struct {
	meta
	Result
	Items []TimelineItem `json:"items"`
	// NextCursor 为 null 表示最后一页（契约 §2）。
	NextCursor any `json:"next_cursor"`
	// Totals 与统计页同源；缺统计能力时为空并计入 missing。
	Totals *analytics.TimelineSummary `json:"totals,omitempty"`
	// OpenIntervals 是窗口内未闭合的区间数：它们不计入已完成时长。
	OpenIntervals int `json:"open_intervals"`
	Returned      int `json:"returned"`
}

// handleTimeline 返回分层执行时间轴（F35／F36／F38）。
//
// 窗口、来源过滤、权限检查与其他读接口共用同一套实现：窗口走 parseWindow，
// 来源走 scope.source_ns（由数据源决定能否下推），权限由 Handler 统一拦截。
func (a *API) handleTimeline(w http.ResponseWriter, r *http.Request) {
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
	spec, err := parseWindow(q, a.now(), 24*time.Hour)
	if err != nil {
		mapErr(w, err)
		return
	}
	kinds, err := parseKinds(q)
	if err != nil {
		mapErr(w, err)
		return
	}
	sourceNS, err := parseSourceNS(q)
	if err != nil {
		mapErr(w, err)
		return
	}
	scope := Scope{
		ProjectID:  projectID,
		SourceNS:   sourceNS,
		InstanceID: strings.TrimSpace(q.Get("instance_id")),
		RunID:      strings.TrimSpace(q.Get("run_id")),
		Kinds:      kinds,
	}
	window := windowOf(spec)
	capability := capabilityOf("timeline", a.Timeline != nil,
		reasonIf(a.Timeline == nil, "source_not_configured"))

	if a.Timeline == nil {
		res := unsupportedResult(capability, scope, window, []Unsupported{{
			Capability: "timeline", Reason: "source_not_configured",
			Detail: "未注入时间轴数据源",
		}})
		writeJSON(w, http.StatusOK, timelineResponse{meta: meta{OK: true}, Result: res,
			Items: []TimelineItem{}, NextCursor: nil})
		return
	}

	data, err := a.Timeline.Timeline(r.Context(), TimelineQuery{
		ProjectID:  projectID,
		Window:     spec.Window,
		Loc:        spec.Loc,
		SourceNS:   sourceNS,
		InstanceID: scope.InstanceID,
		RunID:      scope.RunID,
		Kinds:      kinds,
	})
	if err != nil {
		mapErr(w, err)
		return
	}
	// 数据源无法按请求的过滤条件提供数据时，宁可给空结果＋类型化原因，
	// 也不返回未过滤的全量区间：那等于让调用方把全集当子集。
	if len(data.Unsupported) > 0 {
		res := unsupportedResult(capability, scope, window, data.Unsupported)
		writeJSON(w, http.StatusOK, timelineResponse{meta: meta{OK: true}, Result: res,
			Items: []TimelineItem{}, NextCursor: nil})
		return
	}

	all := filterIntervals(data.Intervals, kinds, scope.InstanceID, scope.RunID)
	page, next, err := pageIntervals(all, limit, strings.TrimSpace(q.Get("cursor")))
	if err != nil {
		mapErr(w, err)
		return
	}

	open := 0
	for _, iv := range all {
		if iv.Open || iv.EndAt == nil {
			open++
		}
	}

	gaps := data.Gaps
	if gaps == nil {
		gaps = []Gap{}
	}
	completeness := completenessOf(data.Cursors.Completeness, gaps)
	missing := missingForGaps(gaps)
	missing = append(missing, data.Missing...)
	wm := watermarkOf(data.Cursors.StateCursor, data.Cursors.ProjectionCursor, a.now(), gaps)

	var totals *analytics.TimelineSummary
	if a.Stats == nil {
		missing = append(missing, Missing{
			Section: "totals", Reason: "source_not_configured",
			Detail: "未注入统计数据源，合计不可用",
		})
	} else {
		report, err := a.Stats.Report(r.Context(), StatsQuery{
			ProjectID: projectID, SourceNS: sourceNS,
			Window: spec.Window, Loc: spec.Loc,
		}, TimelineData{Intervals: all, Cursors: data.Cursors})
		if err != nil {
			mapErr(w, err)
			return
		}
		if report != nil {
			totals = &report.Timeline
			if report.Usage.UsageUnknown {
				missing = append(missing, Missing{
					Section: "usage", Reason: "usage_unknown",
					Detail: "窗口内存在未知用量的轮次，合计不含这部分",
				})
			}
		}
	}

	res := newResult(capability, scope, window, wm, completeness, gaps, missing, data.Unsupported)
	writeJSON(w, http.StatusOK, timelineResponse{
		meta:          meta{OK: true},
		Result:        res,
		Items:         page,
		NextCursor:    cursorOrNil(next),
		Totals:        totals,
		OpenIntervals: open,
		Returned:      len(page),
	})
}

// filterIntervals 按类别、实例与运行过滤区间。
//
// 过滤是纯谓词：不重排、不合并、不补边界——那属于投影层（契约 §5）。
func filterIntervals(in []projection.Interval, kinds []projection.Kind,
	instanceID, runID string) []projection.Interval {
	want := make(map[projection.Kind]struct{}, len(kinds))
	for _, k := range kinds {
		want[k] = struct{}{}
	}
	out := make([]projection.Interval, 0, len(in))
	for _, iv := range in {
		if len(want) > 0 {
			if _, ok := want[iv.Kind]; !ok {
				continue
			}
		}
		if instanceID != "" && iv.InstanceID != instanceID {
			continue
		}
		if runID != "" && iv.RunID != runID {
			continue
		}
		out = append(out, iv)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartAt.Equal(out[j].StartAt) {
			return out[i].IntervalID < out[j].IntervalID
		}
		return out[i].StartAt.Before(out[j].StartAt)
	})
	return out
}

// pageIntervals 按 (start_at, interval_id) 做游标分页。
//
// 排序键包含身份，保证同一时刻的多个区间不会因为翻页而重复或漏掉。
func pageIntervals(items []projection.Interval, limit int, cursor string) ([]projection.Interval, string, error) {
	start := 0
	if cursor != "" {
		keys, err := decodeCursor(cursor, kindTimeline, 2)
		if err != nil {
			return nil, "", err
		}
		at, err := parseTimeParam("cursor", keys[0])
		if err != nil {
			return nil, "", err
		}
		id := keys[1]
		start = len(items)
		for i, iv := range items {
			if iv.StartAt.After(at) || (iv.StartAt.Equal(at) && iv.IntervalID > id) {
				start = i
				break
			}
		}
	}
	rest := items[start:]
	if len(rest) <= limit {
		return rest, "", nil
	}
	page := rest[:limit]
	last := page[len(page)-1]
	return page, encodeCursor(kindTimeline,
		last.StartAt.UTC().Format(time.RFC3339Nano), last.IntervalID), nil
}
