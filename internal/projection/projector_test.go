package projection_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/projection"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func fact(cursor int64, kind projection.Kind, action projection.Action, id, at string, opts ...func(*projection.Fact)) projection.Fact {
	f := projection.Fact{
		Cursor: cursor, Kind: kind, Action: action, IntervalID: id,
		OccurredAt: ts(at), ReceivedAt: ts(at),
		InstanceID: "ti_a", RunID: "run_1", Generation: 1,
		Quality: model.QualityObserved, Evidence: "event:1",
	}
	for _, o := range opts {
		o(&f)
	}
	return f
}

func withPhase(p string) func(*projection.Fact) {
	return func(f *projection.Fact) { f.Phase = p }
}
func withReason(r string) func(*projection.Fact) {
	return func(f *projection.Fact) { f.Reason = r }
}
func withLate() func(*projection.Fact) {
	return func(f *projection.Fact) { f.LateArrival = true }
}

func TestKindsSeparatedAndNoInventedDuration(t *testing.T) {
	p := projection.New("proj1")
	facts := []projection.Fact{
		fact(1, projection.KindTaskPhase, projection.ActionOpen, "iv_p", "2026-10-23T02:00:00Z", withPhase("in_progress")),
		fact(2, projection.KindCLIProcess, projection.ActionOpen, "iv_c", "2026-10-23T02:05:00Z"),
		fact(3, projection.KindCLIProcess, projection.ActionClose, "iv_c", "2026-10-23T02:15:00Z"),
		fact(4, projection.KindExplicitWait, projection.ActionOpen, "iv_w", "2026-10-23T02:10:00Z", withReason("external_dependency")),
		fact(5, projection.KindExplicitWait, projection.ActionClose, "iv_w", "2026-10-23T02:40:00Z"),
		fact(6, projection.KindTaskPhase, projection.ActionClose, "iv_p", "2026-10-23T02:20:00Z"),
		// 只有 open、无 close 的 turn：不制造时长
		fact(7, projection.KindTurn, projection.ActionOpen, "iv_t", "2026-10-23T02:12:00Z"),
	}
	if err := p.Apply(facts...); err != nil {
		t.Fatal(err)
	}
	all := p.Intervals()
	if projection.CompletedSeconds(all, projection.KindTaskPhase) != 20*60 {
		t.Fatalf("task_phase=%d", projection.CompletedSeconds(all, projection.KindTaskPhase))
	}
	if projection.CompletedSeconds(all, projection.KindCLIProcess) != 10*60 {
		t.Fatalf("cli=%d", projection.CompletedSeconds(all, projection.KindCLIProcess))
	}
	if projection.CompletedSeconds(all, projection.KindExplicitWait) != 30*60 {
		t.Fatalf("wait=%d", projection.CompletedSeconds(all, projection.KindExplicitWait))
	}
	if projection.CompletedSeconds(all, projection.KindTurn) != 0 {
		t.Fatal("open turn must not invent duration")
	}
	c := p.Cursors()
	if c.StateCursor != 7 || c.ProjectionCursor != 7 {
		t.Fatalf("cursors=%+v", c)
	}
	if c.Completeness != projection.CompletenessPartial {
		// 仍有 open turn
		t.Fatalf("completeness=%s", c.Completeness)
	}
}

func TestHalfOpenWindowCarryInAndClip(t *testing.T) {
	p := projection.New("proj1")
	_ = p.Apply(
		fact(1, projection.KindTaskPhase, projection.ActionOpen, "iv_p", "2026-10-23T01:00:00Z", withPhase("in_progress")),
		fact(2, projection.KindTaskPhase, projection.ActionClose, "iv_p", "2026-10-23T03:00:00Z"),
	)
	w := projection.Window{Start: ts("2026-10-23T01:30:00Z"), End: ts("2026-10-23T02:30:00Z")}
	got := p.Query(w, time.UTC)
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if !got[0].StartAt.Equal(w.Start) || !got[0].EndAt.Equal(w.End) {
		t.Fatalf("clip=%v..%v", got[0].StartAt, got[0].EndAt)
	}
	d, ok := got[0].Duration()
	if !ok || d != time.Hour {
		t.Fatalf("duration=%v ok=%v", d, ok)
	}
	// 窗口终点半开：恰好从 End 开始的区间不入窗
	_ = p.Apply(
		fact(3, projection.KindCLIProcess, projection.ActionOpen, "iv_x", "2026-10-23T02:30:00Z"),
		fact(4, projection.KindCLIProcess, projection.ActionClose, "iv_x", "2026-10-23T03:00:00Z"),
	)
	got = p.Query(w, time.UTC)
	for _, iv := range got {
		if iv.IntervalID == "iv_x" {
			t.Fatal("half-open end must exclude interval starting at End")
		}
	}
}

func TestCrossDaySplits(t *testing.T) {
	p := projection.New("proj1")
	_ = p.Apply(
		fact(1, projection.KindTaskPhase, projection.ActionOpen, "iv_p", "2026-10-22T15:30:00Z", withPhase("in_progress")),
		fact(2, projection.KindTaskPhase, projection.ActionClose, "iv_p", "2026-10-22T16:30:00Z"),
	)
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	w := projection.Window{Start: ts("2026-10-22T15:00:00Z"), End: ts("2026-10-22T17:00:00Z")}
	got := p.Query(w, loc)
	if len(got) != 1 || len(got[0].DaySplits) != 2 {
		t.Fatalf("splits=%+v", got)
	}
	if got[0].DaySplits[0].Day != "2026-10-22" || got[0].DaySplits[1].Day != "2026-10-23" {
		t.Fatalf("days=%+v", got[0].DaySplits)
	}
	var sum time.Duration
	for _, s := range got[0].DaySplits {
		sum += s.EndAt.Sub(s.StartAt)
	}
	if sum != time.Hour {
		t.Fatalf("split sum=%v", sum)
	}
}

func TestRebuildIdempotent(t *testing.T) {
	facts := []projection.Fact{
		fact(1, projection.KindTaskPhase, projection.ActionOpen, "iv_p", "2026-10-23T02:00:00Z", withPhase("in_progress")),
		fact(2, projection.KindTaskPhase, projection.ActionClose, "iv_p", "2026-10-23T02:20:00Z"),
		fact(3, projection.KindCLIProcess, projection.ActionOpen, "iv_c", "2026-10-23T02:05:00Z"),
		fact(4, projection.KindCLIProcess, projection.ActionClose, "iv_c", "2026-10-23T02:15:00Z"),
	}
	a := projection.New("p")
	b := projection.New("p")
	if err := a.Apply(facts...); err != nil {
		t.Fatal(err)
	}
	if err := b.Rebuild(facts...); err != nil {
		t.Fatal(err)
	}
	if err := b.Rebuild(facts...); err != nil {
		t.Fatal(err)
	}
	ai, bi := a.Intervals(), b.Intervals()
	if len(ai) != len(bi) {
		t.Fatalf("len %d vs %d", len(ai), len(bi))
	}
	for i := range ai {
		if ai[i].IntervalID != bi[i].IntervalID || !ai[i].StartAt.Equal(bi[i].StartAt) {
			t.Fatalf("mismatch %+v vs %+v", ai[i], bi[i])
		}
		if (ai[i].EndAt == nil) != (bi[i].EndAt == nil) {
			t.Fatal("end mismatch")
		}
		if ai[i].EndAt != nil && !ai[i].EndAt.Equal(*bi[i].EndAt) {
			t.Fatal("end time mismatch")
		}
	}
	// 重放 Apply 幂等且不回退水位
	c1 := a.Cursors()
	_ = a.Apply(facts...)
	c2 := a.Cursors()
	if c2.StateCursor < c1.StateCursor || c2.ProjectionCursor < c1.ProjectionCursor {
		t.Fatalf("watermark moved backward: %+v → %+v", c1, c2)
	}
}

func TestLateArrivalMarksDirtyWithoutRewinding(t *testing.T) {
	p := projection.New("p")
	_ = p.Apply(
		fact(10, projection.KindTaskPhase, projection.ActionOpen, "iv_p", "2026-10-23T01:00:00Z", withPhase("in_progress")),
		fact(11, projection.KindTaskPhase, projection.ActionClose, "iv_p", "2026-10-23T01:30:00Z"),
	)
	before := p.Cursors()
	// 迟到 close 修正（高游标、旧发生时间）——这里用独立区间模拟补算
	_ = p.Apply(
		fact(20, projection.KindCLIProcess, projection.ActionOpen, "iv_late", "2026-10-23T00:50:00Z", withLate()),
		fact(21, projection.KindCLIProcess, projection.ActionClose, "iv_late", "2026-10-23T00:55:00Z", withLate()),
	)
	after := p.Cursors()
	if after.StateCursor < before.StateCursor {
		t.Fatal("state cursor must not rewind")
	}
	if after.ProjectionCursor < before.ProjectionCursor {
		t.Fatal("projection cursor must not rewind")
	}
	from, to, ok := p.DirtyRange()
	if !ok || from > to {
		t.Fatalf("dirty=%v %d-%d", ok, from, to)
	}
	if after.Completeness == projection.CompletenessComplete {
		t.Fatal("late arrival must leave completeness partial until cleared")
	}
	p.ClearDirty()
	cleared := p.Cursors()
	if cleared.ProjectionCursor != cleared.StateCursor {
		t.Fatalf("after clear: %+v", cleared)
	}
}

func TestMissingCloseNoDurationAndCloseWithoutOpen(t *testing.T) {
	p := projection.New("p")
	_ = p.Apply(
		fact(1, projection.KindTaskPhase, projection.ActionOpen, "iv_open", "2026-10-23T01:00:00Z", withPhase("claimed")),
	)
	if projection.CompletedSeconds(p.Intervals(), projection.KindTaskPhase) != 0 {
		t.Fatal("open interval must not count as completed duration")
	}
	// 无 open 的 close：不发明区间
	_ = p.Apply(fact(2, projection.KindCLIProcess, projection.ActionClose, "iv_ghost", "2026-10-23T01:10:00Z"))
	for _, iv := range p.Intervals() {
		if iv.IntervalID == "iv_ghost" {
			t.Fatal("must not invent interval from close-only")
		}
	}
	c := p.Cursors()
	if c.StateCursor != 2 {
		t.Fatalf("state=%d", c.StateCursor)
	}
	if c.ProjectionCursor > c.StateCursor {
		t.Fatal("projection ahead of state")
	}
}

func TestClockInversionNoNegativeDuration(t *testing.T) {
	p := projection.New("p")
	_ = p.Apply(
		fact(1, projection.KindTaskPhase, projection.ActionOpen, "iv_p", "2026-10-23T02:00:00Z", withPhase("in_progress")),
		fact(2, projection.KindTaskPhase, projection.ActionClose, "iv_p", "2026-10-23T01:00:00Z"),
	)
	ivs := p.Intervals()
	if len(ivs) != 1 || !ivs[0].Open {
		t.Fatalf("must stay open on inversion: %+v", ivs)
	}
	if _, ok := ivs[0].Duration(); ok {
		t.Fatal("no duration on inversion")
	}
}

func TestStateAndProjectionCursorsSeparate(t *testing.T) {
	p := projection.New("p")
	_ = p.Apply(
		fact(5, projection.KindTaskPhase, projection.ActionOpen, "iv_p", "2026-10-23T01:00:00Z", withPhase("ready")),
		fact(6, projection.KindTaskPhase, projection.ActionClose, "iv_p", "2026-10-23T01:05:00Z"),
	)
	_ = p.Apply(fact(9, projection.KindTurn, projection.ActionOpen, "iv_t", "2026-10-22T12:00:00Z", withLate()))
	c := p.Cursors()
	if c.StateCursor != 9 {
		t.Fatalf("state=%d", c.StateCursor)
	}
	// 迟到使投影停在推进前水位，状态已到 9
	if c.ProjectionCursor >= c.StateCursor && c.Completeness == projection.CompletenessComplete {
		t.Fatalf("expected projection lag or partial, got %+v", c)
	}
	if c.StateCursor == c.ProjectionCursor && c.Completeness == projection.CompletenessPartial {
		// 允许数值相同但 completeness 标 partial（脏未清）
		ok := true
		_, _, dirty := p.DirtyRange()
		if !dirty {
			ok = false
		}
		if !ok {
			t.Fatalf("%+v", c)
		}
	}
}

func TestCheckpointRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maw.db")
	if err := db.Init(path); err != nil {
		t.Fatal(err)
	}
	p := projection.New("716c30878ba1b95f")
	_ = p.Apply(
		fact(3, projection.KindTaskPhase, projection.ActionOpen, "iv_p", "2026-10-23T01:00:00Z", withPhase("in_progress")),
		fact(4, projection.KindTaskPhase, projection.ActionClose, "iv_p", "2026-10-23T01:10:00Z"),
		fact(8, projection.KindCLIProcess, projection.ActionOpen, "iv_c", "2026-10-23T00:01:00Z", withLate()),
	)
	err := db.WithTx(path, func(tx *sql.Tx) error {
		return p.Checkpoint(tx)
	})
	if err != nil {
		t.Fatal(err)
	}
	p2 := projection.New("716c30878ba1b95f")
	err = db.WithTx(path, func(tx *sql.Tx) error {
		return p2.LoadCheckpoint(tx)
	})
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := p.Cursors(), p2.Cursors()
	if c1.StateCursor != c2.StateCursor || c1.ProjectionCursor != c2.ProjectionCursor {
		t.Fatalf("%+v vs %+v", c1, c2)
	}
	f1, t1, ok1 := p.DirtyRange()
	f2, t2, ok2 := p2.DirtyRange()
	if ok1 != ok2 || f1 != f2 || t1 != t2 {
		t.Fatalf("dirty %v %d-%d vs %v %d-%d", ok1, f1, t1, ok2, f2, t2)
	}
}
