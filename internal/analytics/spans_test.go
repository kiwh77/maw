package analytics

import (
	"math"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/projection"
)

var (
	identA = "inst-a"
	identB = "inst-b"
	runA   = "run-a"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func ptrT(s string) *time.Time {
	t := ts(s)
	return &t
}

func iv(id, instance string, kind projection.Kind, start, end string) projection.Interval {
	return projection.Interval{
		IntervalID: id, Kind: kind, InstanceID: instance, RunID: runA, Generation: 1,
		StartAt: ts(start), EndAt: ptrT(end),
	}
}

func window(start, end string) projection.Window {
	return projection.Window{Start: ts(start), End: ts(end)}
}

func engMatch(k projection.Kind) bool {
	return k == projection.KindTaskPhase || k == projection.KindCLIProcess
}

// 时长按身份并集，不是各区间相加：同身份重叠段只算一次（契约 §5）。
func TestUnionVsCumulativeSameIdentity(t *testing.T) {
	intervals := []projection.Interval{
		iv("i1", identA, projection.KindTaskPhase, "2026-10-23T10:00:00Z", "2026-10-23T11:00:00Z"),
		iv("i2", identA, projection.KindTaskPhase, "2026-10-23T10:30:00Z", "2026-10-23T12:00:00Z"),
	}
	w := window("2026-10-23T00:00:00Z", "2026-10-24T00:00:00Z")
	if got := unionSeconds(intervals, w, engMatch); got != 7200 {
		t.Fatalf("并集时长 = %d，要 7200（10:00–12:00）", got)
	}
	// 同样两条的朴素相加是 9000；并集必须小于相加。
	var naive int64
	for _, i := range intervals {
		d, _ := i.Duration()
		naive += int64(d / time.Second)
	}
	if naive != 9000 || 7200 >= naive {
		t.Fatalf("并集 %d 应小于朴素相加 %d", 7200, naive)
	}
}

// 跨身份允许重叠，按身份分别求并集后再相加；平均并发 = Σ 并集 / 窗口。
func TestCrossIdentityOverlapAndConcurrency(t *testing.T) {
	intervals := []projection.Interval{
		iv("i1", identA, projection.KindTaskPhase, "2026-10-23T10:00:00Z", "2026-10-23T11:00:00Z"),
		iv("i2", identB, projection.KindTaskPhase, "2026-10-23T10:00:00Z", "2026-10-23T11:00:00Z"),
	}
	w := window("2026-10-23T09:00:00Z", "2026-10-23T11:00:00Z")
	if got := unionSeconds(intervals, w, engMatch); got != 7200 {
		t.Fatalf("跨身份并集之和 = %d，要 7200", got)
	}
	if got := concurrency(intervals, w, engMatch); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("平均并发 = %v，要 1.0（2 小时窗口忙碌 2 小时·身份）", got)
	}
}

// engineering = task_phase ∪ cli_process（跨类别、同身份并集），与等待分开。
func TestEngineeringUnionAcrossKinds(t *testing.T) {
	intervals := []projection.Interval{
		iv("i1", identA, projection.KindTaskPhase, "2026-10-23T10:00:00Z", "2026-10-23T11:00:00Z"),
		iv("i2", identA, projection.KindCLIProcess, "2026-10-23T10:30:00Z", "2026-10-23T11:30:00Z"),
		iv("i3", identA, projection.KindExplicitWait, "2026-10-23T11:00:00Z", "2026-10-23T12:00:00Z"),
	}
	w := window("2026-10-23T00:00:00Z", "2026-10-24T00:00:00Z")
	if got := unionSeconds(intervals, w, engMatch); got != 5400 {
		t.Fatalf("工程时长 = %d，要 5400（10:00–11:30 并集）", got)
	}
	if got := unionSeconds(intervals, w, func(k projection.Kind) bool {
		return k == projection.KindExplicitWait
	}); got != 3600 {
		t.Fatalf("等待时长 = %d，要 3600，等待不得并入工程时长", got)
	}
}

// 未结束区间不参与已完成时长；窗口裁剪按半开区间。
func TestOpenAndClipping(t *testing.T) {
	open := projection.Interval{
		IntervalID: "i1", Kind: projection.KindTaskPhase, InstanceID: identA, RunID: runA, Generation: 1,
		StartAt: ts("2026-10-23T09:00:00Z"), EndAt: nil, Open: true,
	}
	w := window("2026-10-23T10:00:00Z", "2026-10-23T11:00:00Z")
	if got := unionSeconds([]projection.Interval{open}, w, engMatch); got != 0 {
		t.Fatalf("未结束区间不得贡献已完成时长，得到 %d", got)
	}
	spanning := iv("i2", identA, projection.KindTaskPhase, "2026-10-23T09:00:00Z", "2026-10-23T12:00:00Z")
	if got := unionSeconds([]projection.Interval{spanning}, w, engMatch); got != 3600 {
		t.Fatalf("跨窗口区间应被裁到 3600，得到 %d", got)
	}
}

// 小时桶按查询时区切分；各桶之和等于并集总量，边界桶不被整小时稀释。
func TestHourBucketsSplitByLocalHour(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	// 16:30Z–18:00Z = 本地次日 00:30–02:00，跨本地零点。
	intervals := []projection.Interval{
		iv("i1", identA, projection.KindTaskPhase, "2026-10-23T16:30:00Z", "2026-10-23T18:00:00Z"),
	}
	w := window("2026-10-23T00:00:00Z", "2026-10-24T00:00:00Z")
	buckets := hourBuckets(intervals, w, loc)
	if len(buckets) != 2 {
		t.Fatalf("小时桶数 = %d，要 2", len(buckets))
	}
	var sum int64
	for i, b := range buckets {
		sum += b.TaskPhaseSeconds
		if b.EngineeringSeconds != b.TaskPhaseSeconds {
			t.Fatalf("桶 %d 工程时长应等于 task_phase（无其它类别）", i)
		}
		local := b.Hour.In(loc)
		if local.Hour() != i { // 第一个桶本地 0 点，第二个 1 点
			t.Fatalf("桶 %d 本地小时 = %d，要 %d", i, local.Hour(), i)
		}
	}
	if sum != 5400 {
		t.Fatalf("小时桶之和 = %d，要 5400（与并集一致）", sum)
	}
	// 边界桶：窗口在本地 01:30 截断，最后一个桶只有 1800 秒；
	// 桶长按窗口裁剪后为 1800 秒，并发仍为 1，不被整小时稀释。
	w2 := window("2026-10-23T00:00:00Z", "2026-10-23T17:30:00Z")
	b2 := hourBuckets(intervals, w2, loc)
	if len(b2) != 2 {
		t.Fatalf("边界窗口桶数 = %d，要 2", len(b2))
	}
	last := b2[len(b2)-1]
	if last.TaskPhaseSeconds != 1800 {
		t.Fatalf("边界桶秒数 = %d，要 1800", last.TaskPhaseSeconds)
	}
	if math.Abs(last.AvgConcurrency-1.0) > 1e-9 {
		t.Fatalf("边界桶平均并发 = %v，要 1.0（桶长按窗口裁剪）", last.AvgConcurrency)
	}
}

func TestEmptyWindowZeroDenominator(t *testing.T) {
	w := window("2026-10-23T10:00:00Z", "2026-10-23T11:00:00Z")
	if got := unionSeconds(nil, w, engMatch); got != 0 {
		t.Fatalf("空输入并集 = %d，要 0", got)
	}
	if got := concurrency(nil, w, engMatch); got != 0 {
		t.Fatalf("空输入平均并发 = %v，要 0，不假装有并发", got)
	}
}
