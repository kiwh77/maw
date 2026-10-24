package projection

import (
	"time"
)

// SplitByDay 按 timezone 本地零点切开已闭合区间；未结束不生成 splits。
//
// 各段时长之和等于原区间时长，段不重叠且落在原区间内（契约 T130）。
func SplitByDay(iv Interval, loc *time.Location) []DaySplit {
	if loc == nil {
		loc = time.UTC
	}
	if iv.Open || iv.EndAt == nil || !iv.EndAt.After(iv.StartAt) {
		return nil
	}
	start := iv.StartAt.In(loc)
	end := iv.EndAt.In(loc)
	var out []DaySplit
	cur := start
	for cur.Before(end) {
		nextMidnight := time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, loc)
		segEnd := end
		if nextMidnight.Before(end) {
			segEnd = nextMidnight
		}
		out = append(out, DaySplit{
			Day:     cur.Format("2006-01-02"),
			StartAt: cur.UTC(),
			EndAt:   segEnd.UTC(),
		})
		cur = nextMidnight
	}
	return out
}
