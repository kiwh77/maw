package events

import (
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// TestLateArrivalMarkedAndFrontierNotRewound 覆盖乱序到达：迟到记录允许入库并
// 回溯修正统计，但发生时间前沿只能前进，游标也只能前进（契约 §3）。
func TestLateArrivalMarkedAndFrontierNotRewound(t *testing.T) {
	g := newIng()

	newer := baseObs()
	newer.SourceKey = "msg-0002"
	newer.OccurredAt = at(30)
	newer.ReceivedAt = *at(31)
	newer.Phase = phaseOf("completed")

	older := baseObs() // 同一来源，但发生在更早的时刻，却后到
	older.SourceKey = "msg-0001"
	older.OccurredAt = at(10)
	older.ReceivedAt = *at(40)

	first := mustAccept(t, g.Ingest(managedSrc(), []Observation{newer}))
	late := mustAccept(t, g.Ingest(managedSrc(), []Observation{older}))

	if late.LateArrival != true {
		t.Fatal("晚到的旧事件必须标记 late_arrival")
	}
	if late.Event.OccurredAt.After(late.Event.ReceivedAt) {
		t.Fatal("发生时间被改写成了接收时间")
	}
	if !late.Event.OccurredAt.Equal(*at(10)) {
		t.Fatalf("迟到记录的原始发生时间必须保留，实际 %s", late.Event.OccurredAt)
	}
	if late.Event.Cursor <= first.Event.Cursor {
		t.Fatal("迟到记录仍按接收顺序取更大的游标，游标不得回退")
	}

	frontier, ok := g.Frontier(managedSrc())
	if !ok || !frontier.Equal(*at(30)) {
		t.Fatalf("前沿不得被迟到记录拉回：%s", frontier)
	}
}

// TestOutOfOrderWithinBatch 覆盖同一批内部的乱序：以已见前沿为准，
// 批内第二条（更早）为迟到，第三条（更晚）不算迟到。
func TestOutOfOrderWithinBatch(t *testing.T) {
	g := newIng()

	mk := func(key string, m int) Observation {
		o := baseObs()
		o.SourceKey = key
		o.OccurredAt = at(m)
		o.ReceivedAt = *at(60) // 同批提交，接收时间相同
		return o
	}

	res := g.Ingest(managedSrc(), []Observation{mk("k1", 5), mk("k2", 3), mk("k3", 7)})
	if len(res.Accepted) != 3 {
		t.Fatalf("三条都应接受（乱序不等于脏数据），实际 %d（%+v）", len(res.Accepted), res.Rejected)
	}
	if res.Accepted[0].LateArrival {
		t.Fatal("首条不可能是迟到")
	}
	if !res.Accepted[1].LateArrival {
		t.Fatal("第二批内更早的一条应判迟到")
	}
	if res.Accepted[2].LateArrival {
		t.Fatal("更晚的一条不是迟到")
	}
	if frontier, _ := g.Frontier(managedSrc()); !frontier.Equal(*at(7)) {
		t.Fatalf("前沿应为批内最大发生时间，实际 %s", frontier)
	}
}

// TestBackfillKeepsBothTimestamps 覆盖日志补交：发生时间来自日志、接收时间是今天，
// 两者都保留，并标记 backfill；质量不因此被抬成 observed。
func TestBackfillKeepsBothTimestamps(t *testing.T) {
	g := newIng()
	obs := baseObs()
	obs.RecordRef = "file:///var/log/cursor/session-2026-10-20.log#118"
	obs.SourceKey = "log-118"
	obs.OccurredAt = at(1)
	obs.ReceivedAt = *at(300)
	obs.Backfill = true
	obs.Quality = model.QualityEstimated

	rec := mustAccept(t, g.Ingest(managedSrc(), []Observation{obs}))
	if !rec.Backfill {
		t.Fatal("必须保留 backfill 标记")
	}
	if !rec.Event.OccurredAt.Equal(*at(1)) {
		t.Fatalf("补交记录的发生时间应来自日志，实际 %s", rec.Event.OccurredAt)
	}
	if !rec.Event.ReceivedAt.Equal(*at(300)) {
		t.Fatalf("接收时间应是补交时刻，实际 %s", rec.Event.ReceivedAt)
	}
	if rec.Event.Quality != model.QualityEstimated {
		t.Fatalf("来源声称 estimated 应被尊重，实际 %s", rec.Event.Quality)
	}
	// 补交之后，正常时延的两条不会被误判迟到。
	follow := baseObs()
	follow.SourceKey = "log-119"
	follow.OccurredAt = at(2)
	follow.ReceivedAt = *at(301)
	if got := mustAccept(t, g.Ingest(managedSrc(), []Observation{follow})); got.LateArrival {
		t.Fatal("补交不改变时序判定基准")
	}
}

// TestCursorRegressionRejected 覆盖外部分配器给出倒退游标：宁可拒收这条，
// 也不能让游标出现回退（否则按游标补拉的客户端会漏事件）。
func TestCursorRegressionRejected(t *testing.T) {
	cursor := model.Cursor(1)
	g := New(Options{
		Now:        func() time.Time { return t0.Add(60 * time.Minute) },
		NextCursor: func() model.Cursor { return cursor }, // 永远返回 1
	})

	obs := baseObs()
	first := mustAccept(t, g.Ingest(managedSrc(), []Observation{obs}))
	if first.Event.Cursor != 1 {
		t.Fatalf("首条游标应为 1，实际 %d", first.Event.Cursor)
	}

	second := baseObs()
	second.SourceKey = "msg-0002"
	second.OccurredAt = at(12)
	second.ReceivedAt = *at(13)
	rej := mustReject(t, g.Ingest(managedSrc(), []Observation{second}), ReasonCursorRegression)
	if rej.Field != "cursor" {
		t.Fatalf("应定位到 cursor，实际 %s", rej.Field)
	}
	if rej.Reason.Class() != model.ClassConflict {
		t.Fatalf("游标倒退应归 conflict 类，实际 %s", rej.Reason.Class())
	}
}
