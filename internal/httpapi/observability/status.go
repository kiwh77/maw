package observability

import (
	"time"

	"github.com/kiwh77/maw/internal/projection"
)

// deriveStatus 把能力、完整性与水位折成一个类型化状态。
//
// 优先级：不支持 > 未知 > 落后 > 部分 > 就绪。
//
//   - 不支持排第一：能力没接线时，任何完整性字段都是空的，说「部分」会误导；
//   - 未知排在落后前：连水位都没推进过时，「落后多少条」没有意义；
//   - 落后排在部分前：投影有积压是可执行的运维动作，比「部分」更该先被看到。
func deriveStatus(supported bool, completeness projection.Completeness,
	cursor, completeBefore int64) Status {
	if !supported {
		return StatusUnsupported
	}
	switch completeness {
	case projection.CompletenessUnknown:
		return StatusUnknown
	case projection.CompletenessPartial:
		// 部分完整里若还带投影积压，仍然报落后：补算完才会回到 partial/ready。
		if cursor > completeBefore {
			return StatusStale
		}
		return StatusPartial
	case projection.CompletenessComplete:
		if cursor > completeBefore {
			return StatusStale
		}
		return StatusReady
	}
	// 完整性字段缺失：按未知处理，不默认成 complete。
	return StatusUnknown
}

// lagOf 在投影落后时给出具体差额；未落后返回 nil。
func lagOf(cursor, completeBefore int64) *ProjectionLag {
	if cursor <= completeBefore {
		return nil
	}
	return &ProjectionLag{
		Cursor:         cursor,
		CompleteBefore: completeBefore,
		Pending:        cursor - completeBefore,
	}
}

// newResult 组装公共响应部分。
//
// 所有读接口都经它出口，保证五个接口的水位、完整性、缺口、缺测字段形状一致：
// 口径统一先体现在结构上，才谈得上统一语义。
func newResult(cap Capability, scope Scope, window *Window, wm *Watermark,
	completeness projection.Completeness, gaps []Gap, missing []Missing,
	unsupported []Unsupported) Result {
	if gaps == nil {
		gaps = []Gap{}
	}
	if missing == nil {
		missing = []Missing{}
	}
	if unsupported == nil {
		unsupported = []Unsupported{}
	}
	if completeness == "" {
		completeness = projection.CompletenessUnknown
	}
	res := Result{
		Capability:   cap,
		Scope:        scope,
		Window:       window,
		Completeness: completeness,
		Watermark:    wm,
		Gaps:         gaps,
		Missing:      missing,
		Unsupported:  unsupported,
	}
	var cursor, completeBefore int64
	if wm != nil {
		cursor, completeBefore = wm.Cursor, wm.CompleteBefore
		res.ProjectionLag = lagOf(cursor, completeBefore)
	}
	res.Status = deriveStatus(cap.Supported && len(unsupported) == 0, completeness, cursor, completeBefore)
	return res
}

// unsupportedResult 是「能力不可用」的统一出口：数据一律为空，状态一律 unsupported。
//
// 不返回未经过滤的数据：过滤条件无法满足时给全量数据，等于让调用方把子集当全集。
func unsupportedResult(cap Capability, scope Scope, window *Window, unsupported []Unsupported) Result {
	if cap.Reason == "" && len(unsupported) > 0 {
		cap.Reason = unsupported[0].Reason
	}
	return newResult(cap, scope, window, nil, projection.CompletenessUnknown,
		nil, nil, unsupported)
}

// windowOf 把解析后的窗口转成回显结构。
func windowOf(w windowSpec) *Window {
	return &Window{
		Start:     w.Window.Start.UTC(),
		End:       w.Window.End.UTC(),
		Timezone:  w.TZ,
		Hours:     w.Hours,
		Defaulted: w.Defaulted,
	}
}

// completenessOf 收口完整性：有缺口时不得写 complete（契约 §2）。
func completenessOf(c projection.Completeness, gaps []Gap) projection.Completeness {
	if len(gaps) > 0 && c == projection.CompletenessComplete {
		return projection.CompletenessPartial
	}
	return c
}

// missingForGaps 把缺口翻译成缺测说明：缺口是「这段时间没有完整事实」，
// 不是「这段时间没有活动」。
func missingForGaps(gaps []Gap) []Missing {
	out := make([]Missing, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, Missing{
			Section: "intervals",
			Reason:  "collector_gap",
			Detail:  g.Reason,
		})
	}
	return out
}

// watermarkOf 组装水位；cursor／completeBefore 来自投影，时间戳取查询时刻。
func watermarkOf(cursor, completeBefore int64, at time.Time, gaps []Gap) *Watermark {
	if gaps == nil {
		gaps = []Gap{}
	}
	return &Watermark{
		Cursor:         cursor,
		CompleteBefore: completeBefore,
		ReceivedAt:     at.UTC(),
		Gaps:           gaps,
	}
}
