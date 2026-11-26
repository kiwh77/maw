package projection

import "time"

// ClipToWindow 按半开窗口 [w.Start, w.End) 裁剪区间。
//
// 窗口前已开始且仍覆盖窗口起点的区间会被带入（carry-in），起点裁到 w.Start。
// 终点若落在窗口外则裁到 w.End（不含），未结束区间在窗口内仍标 Open。
// 与窗口无交集则 ok=false。
func ClipToWindow(iv Interval, w Window) (Interval, bool) {
	if !w.Valid() {
		return Interval{}, false
	}
	start := iv.StartAt
	var end *time.Time
	if iv.EndAt != nil {
		e := *iv.EndAt
		end = &e
	}

	// 已在窗口前结束 → 无交集
	if end != nil && !end.After(w.Start) {
		return Interval{}, false
	}
	// 在窗口终点及之后才开始 → 无交集（半开）
	if !start.Before(w.End) {
		return Interval{}, false
	}

	out := iv
	if start.Before(w.Start) {
		out.StartAt = w.Start
	} else {
		out.StartAt = start
	}
	if end == nil {
		// 未结束：展示上裁到窗口终点，但仍标 Open，不制造完整时长
		e := w.End
		out.EndAt = &e
		out.Open = true
	} else if end.After(w.End) {
		e := w.End
		out.EndAt = &e
		out.Open = false // 原区间有闭合，只是被窗口裁切
	} else {
		e := *end
		out.EndAt = &e
		out.Open = iv.Open
	}
	// 裁切后空集（理论上不应，半开保护）
	if out.EndAt != nil && !out.EndAt.After(out.StartAt) {
		return Interval{}, false
	}
	return out, true
}

// OverlapsWindow 报告原区间是否与半开窗口相交（含窗口前 carry-in）。
func OverlapsWindow(iv Interval, w Window) bool {
	_, ok := ClipToWindow(iv, w)
	return ok
}
