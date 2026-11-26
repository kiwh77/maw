package analytics

import (
	"sort"
	"time"

	"github.com/kiwh77/maw/internal/projection"
)

// Span 是按身份合并后的一段忙碌时间；同身份内不重叠。
type Span struct {
	Start time.Time
	End   time.Time
}

func (s Span) seconds() int64 {
	if !s.End.After(s.Start) {
		return 0
	}
	return int64(s.End.Sub(s.Start) / time.Second)
}

// identity 是区间的去重身份：同一实例同一代际的区间才算同一段忙碌流。
func identity(iv projection.Interval) string {
	return iv.InstanceID + "|" + iv.RunID + "|" + itoa(iv.Generation)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// mergeByIdentity 把与选择器匹配的已闭合区间裁剪到窗口内，按身份合并重叠段。
//
// 未结束（Open）区间不贡献已完成时长，直接跳过（契约 §1）。
// 返回的段按 Start 升序；跨身份的段允许重叠。
func mergeByIdentity(intervals []projection.Interval, w projection.Window, match func(projection.Kind) bool) []Span {
	groups := map[string][]Span{}
	for _, iv := range intervals {
		if match != nil && !match(iv.Kind) {
			continue
		}
		clipped, ok := projection.ClipToWindow(iv, w)
		if !ok || clipped.Open || clipped.EndAt == nil || !clipped.EndAt.After(clipped.StartAt) {
			continue
		}
		id := identity(iv)
		groups[id] = append(groups[id], Span{Start: clipped.StartAt, End: *clipped.EndAt})
	}
	merged := make([]Span, 0, len(groups))
	for _, spans := range groups {
		sort.Slice(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
		cur := spans[0]
		for _, s := range spans[1:] {
			if !s.Start.After(cur.End) {
				if s.End.After(cur.End) {
					cur.End = s.End
				}
				continue
			}
			merged = append(merged, cur)
			cur = s
		}
		merged = append(merged, cur)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Start.Before(merged[j].Start) })
	return merged
}

// sumSpans 返回各段秒数之和（跨身份重叠会重复计入——这正是并发的含义）。
func sumSpans(spans []Span) int64 {
	var total int64
	for _, s := range spans {
		total += s.seconds()
	}
	return total
}

// unionSeconds 按类别选择器计算「各身份并集后再相加」的已完成秒数。
func unionSeconds(intervals []projection.Interval, w projection.Window, match func(projection.Kind) bool) int64 {
	return sumSpans(mergeByIdentity(intervals, w, match))
}

// concurrency 计算平均并发身份数：Σ 各身份并集秒数 / 窗口秒数。
//
// 窗口长度为 0 或无区间时返回 0，不假装有并发。
func concurrency(intervals []projection.Interval, w projection.Window, match func(projection.Kind) bool) float64 {
	length := w.End.Sub(w.Start).Seconds()
	if length <= 0 {
		return 0
	}
	return float64(unionSeconds(intervals, w, match)) / length
}

// HourBucket 是一个本地小时桶；秒数按身份并集后计入，AvgConcurrency 相对桶长。
type HourBucket struct {
	Hour                time.Time `json:"hour"` // 本地小时的起点（UTC 存储）
	TaskPhaseSeconds    int64     `json:"task_phase_seconds"`
	CLIProcessSeconds   int64     `json:"cli_process_seconds"`
	TurnSeconds         int64     `json:"turn_seconds"`
	ExplicitWaitSeconds int64     `json:"explicit_wait_seconds"`
	EngineeringSeconds  int64     `json:"engineering_seconds"`
	AvgConcurrency      float64   `json:"avg_concurrency"`
}

func kindMatch(kinds ...projection.Kind) func(projection.Kind) bool {
	set := map[projection.Kind]struct{}{}
	for _, k := range kinds {
		set[k] = struct{}{}
	}
	return func(k projection.Kind) bool {
		_, ok := set[k]
		return ok
	}
}

// hourBuckets 把合并后的各类区间按本地小时分桶；各桶秒数之和等于总并集秒数。
//
// 桶长按窗口与本地小时的交集计算，边界桶不会被整小时长度稀释。
func hourBuckets(intervals []projection.Interval, w projection.Window, loc *time.Location) []HourBucket {
	if loc == nil {
		loc = time.UTC
	}
	buckets := map[time.Time]*HourBucket{}
	distribute := func(spans []Span, fill func(*HourBucket, int64)) {
		for _, s := range spans {
			cur := s.Start.In(loc)
			for cur.Before(s.End) {
				next := time.Date(cur.Year(), cur.Month(), cur.Day(), cur.Hour()+1, 0, 0, 0, loc)
				segEnd := s.End
				if next.Before(segEnd) {
					segEnd = next
				}
				sec := int64(segEnd.Sub(cur) / time.Second)
				key := time.Date(cur.Year(), cur.Month(), cur.Day(), cur.Hour(), 0, 0, 0, loc)
				b := buckets[key]
				if b == nil {
					b = &HourBucket{Hour: key.UTC()}
					buckets[key] = b
				}
				fill(b, sec)
				cur = next
			}
		}
	}
	distribute(mergeByIdentity(intervals, w, kindMatch(projection.KindTaskPhase)),
		func(b *HourBucket, sec int64) { b.TaskPhaseSeconds += sec })
	distribute(mergeByIdentity(intervals, w, kindMatch(projection.KindCLIProcess)),
		func(b *HourBucket, sec int64) { b.CLIProcessSeconds += sec })
	distribute(mergeByIdentity(intervals, w, kindMatch(projection.KindTurn)),
		func(b *HourBucket, sec int64) { b.TurnSeconds += sec })
	distribute(mergeByIdentity(intervals, w, kindMatch(projection.KindExplicitWait)),
		func(b *HourBucket, sec int64) { b.ExplicitWaitSeconds += sec })
	engSpans := mergeByIdentity(intervals, w, kindMatch(projection.KindTaskPhase, projection.KindCLIProcess))
	distribute(engSpans, func(b *HourBucket, sec int64) { b.EngineeringSeconds += sec })

	keys := make([]time.Time, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	out := make([]HourBucket, 0, len(keys))
	for _, k := range keys {
		b := buckets[k]
		// 桶长 = min(桶终点, 窗口终点) - max(桶起点, 窗口起点)；至少 1 秒防零分母伪装。
		start := k
		end := k.Add(time.Hour)
		if w.Start.After(start) {
			start = w.Start
		}
		if w.End.Before(end) {
			end = w.End
		}
		length := end.Sub(start).Seconds()
		if length > 0 {
			b.AvgConcurrency = float64(b.EngineeringSeconds) / length
		}
		out = append(out, *b)
	}
	return out
}
