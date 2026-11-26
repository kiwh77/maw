package projection

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// Projector 把边界事实投影为分层区间；内存权威，可全量重建。
type Projector struct {
	projectID string
	version   int

	stateCursor int64 // 已见最大事实游标
	projCursor  int64 // complete_before
	dirtyFrom   *int64
	dirtyTo     *int64

	intervals map[string]*Interval // interval_id → 区间
	openByKey map[string]string    // identity key → open interval_id
}

// New 创建空投影；projectID 用于检查点归属。
func New(projectID string) *Projector {
	return &Projector{
		projectID: projectID,
		version:   Version,
		intervals: map[string]*Interval{},
		openByKey: map[string]string{},
	}
}

// Apply 按游标增量消费事实；迟到事实标脏并触发受影响范围补算标记。
//
// 游标只前进：重复或更旧的 cursor 被忽略（幂等）。同一批可乱序 occurred_at。
func (p *Projector) Apply(facts ...Fact) error {
	if p.intervals == nil {
		p.intervals = map[string]*Interval{}
		p.openByKey = map[string]string{}
	}
	sorted := append([]Fact(nil), facts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Cursor != sorted[j].Cursor {
			return sorted[i].Cursor < sorted[j].Cursor
		}
		return sorted[i].OccurredAt.Before(sorted[j].OccurredAt)
	})
	for _, f := range sorted {
		if err := p.applyOne(f); err != nil {
			return err
		}
	}
	p.advanceProjectionCursor()
	return nil
}

// Rebuild 清空后按事实全量重建；同一输入应得到相同区间集合（幂等）。
func (p *Projector) Rebuild(facts ...Fact) error {
	p.intervals = map[string]*Interval{}
	p.openByKey = map[string]string{}
	p.stateCursor = 0
	p.projCursor = 0
	p.dirtyFrom, p.dirtyTo = nil, nil
	return p.Apply(facts...)
}

func (p *Projector) applyOne(f Fact) error {
	if f.Cursor <= 0 {
		return model.Violation(model.CodePortFieldInvalid, "fact.cursor", "游标必须为正")
	}
	if !f.Kind.Valid() {
		return model.Violationf(model.CodePortFieldInvalid, "fact.kind", "非法区间类别 %q", f.Kind)
	}
	if f.Action != ActionOpen && f.Action != ActionClose {
		return model.Violationf(model.CodePortFieldInvalid, "fact.action", "非法动作 %q", f.Action)
	}
	if strings.TrimSpace(f.IntervalID) == "" {
		return model.Violation(model.CodePortFieldInvalid, "fact.interval_id", "区间身份必填")
	}
	if f.OccurredAt.IsZero() {
		return model.Violation(model.CodePortFieldInvalid, "fact.occurred_at", "发生时间必填")
	}
	if f.Kind == KindTaskPhase && f.Action == ActionOpen {
		if !model.TaskStatus(f.Phase).Valid() {
			return model.Violationf(model.CodePortFieldInvalid, "fact.phase", "非法阶段 %q", f.Phase)
		}
	}
	if f.Kind == KindExplicitWait && f.Action == ActionOpen {
		switch f.Reason {
		case "blocked", "manual_pause", "external_dependency":
		default:
			return model.Violationf(model.CodePortFieldInvalid, "fact.reason",
				"explicit_wait 必须写原因，收到 %q", f.Reason)
		}
	}
	if f.Quality == "" {
		f.Quality = model.QualityObserved
	}
	if !f.Quality.Valid() {
		return model.Violationf(model.CodePortFieldInvalid, "fact.quality", "非法质量 %q", f.Quality)
	}

	// 状态水位只前进
	if f.Cursor > p.stateCursor {
		p.stateCursor = f.Cursor
	}

	key := identityKey(f)
	switch f.Action {
	case ActionOpen:
		if existing, ok := p.intervals[f.IntervalID]; ok {
			// 同 id 重放 open：幂等保持
			if existing.StartAt.Equal(f.OccurredAt.UTC()) {
				return nil
			}
			return model.Violationf(model.CodePortFieldInvalid, "fact.interval_id",
				"区间 %q 已存在且起点不同", f.IntervalID)
		}
		// 同身份同类别禁止重叠：若仍有 open，拒绝（契约 T113）
		if openID, ok := p.openByKey[key]; ok {
			return model.Violationf(model.CodePortFieldInvalid, "fact",
				"同身份同类别已有未关闭区间 %s", openID)
		}
		if f.LateArrival {
			p.markDirty(f.Cursor)
		}
		iv := &Interval{
			IntervalID:  f.IntervalID,
			Kind:        f.Kind,
			InstanceID:  f.InstanceID,
			RunID:       f.RunID,
			Generation:  f.Generation,
			Phase:       f.Phase,
			Reason:      f.Reason,
			StartAt:     f.OccurredAt.UTC(),
			EndAt:       nil,
			Quality:     f.Quality,
			EvidenceRef: f.Evidence,
			LateArrival: f.LateArrival,
			Open:        true,
		}
		p.intervals[f.IntervalID] = iv
		p.openByKey[key] = f.IntervalID
	case ActionClose:
		iv, ok := p.intervals[f.IntervalID]
		if !ok {
			// 缺失打开边界：不制造完整时长
			p.markDirty(f.Cursor)
			return nil
		}
		if !iv.Open || iv.EndAt != nil {
			// 幂等重放 close
			if iv.EndAt != nil && iv.EndAt.Equal(f.OccurredAt.UTC()) {
				return nil
			}
			return model.Violationf(model.CodePortFieldInvalid, "fact",
				"区间 %q 已关闭", f.IntervalID)
		}
		if f.OccurredAt.Before(iv.StartAt) {
			// 时钟倒置：不产生负时长，标 unknown 并保持 open
			iv.Quality = model.QualityUnknown
			p.markDirty(f.Cursor)
			return nil
		}
		if f.LateArrival {
			p.markDirty(f.Cursor)
			iv.LateArrival = true
		}
		end := f.OccurredAt.UTC()
		iv.EndAt = &end
		iv.Open = false
		delete(p.openByKey, key)
	}
	return nil
}

func identityKey(f Fact) string {
	return fmt.Sprintf("%s|%s|%s|%d", f.Kind, f.InstanceID, f.RunID, f.Generation)
}

func (p *Projector) markDirty(cursor int64) {
	if p.dirtyFrom == nil || cursor < *p.dirtyFrom {
		v := cursor
		p.dirtyFrom = &v
	}
	if p.dirtyTo == nil || cursor > *p.dirtyTo {
		v := cursor
		p.dirtyTo = &v
	}
}

func (p *Projector) advanceProjectionCursor() {
	// 水位只前进：有脏范围时不推进 projection_cursor（完整度变 partial），
	// 无脏时追上 state_cursor。禁止把 complete_before 调小。
	if p.dirtyFrom != nil {
		return
	}
	if p.stateCursor > p.projCursor {
		p.projCursor = p.stateCursor
	}
}

// ClearDirty 在调用方确认已对脏范围完成补算后清除脏标记并推进投影水位。
func (p *Projector) ClearDirty() {
	p.dirtyFrom, p.dirtyTo = nil, nil
	if p.stateCursor > p.projCursor {
		p.projCursor = p.stateCursor
	}
}

// DirtyRange 返回当前脏游标范围（含端点）；无脏时 ok=false。
func (p *Projector) DirtyRange() (from, to int64, ok bool) {
	if p.dirtyFrom == nil || p.dirtyTo == nil {
		return 0, 0, false
	}
	return *p.dirtyFrom, *p.dirtyTo, true
}

// Cursors 返回状态／投影双水位。
func (p *Projector) Cursors() Cursors {
	c := Cursors{
		StateCursor:      p.stateCursor,
		ProjectionCursor: p.projCursor,
		Version:          p.version,
		Completeness:     CompletenessComplete,
	}
	if p.dirtyFrom != nil || len(p.openByKey) > 0 {
		c.Completeness = CompletenessPartial
	}
	if p.stateCursor == 0 {
		c.Completeness = CompletenessUnknown
	}
	return c
}

// Intervals 返回当前全部区间的拷贝（未裁剪）。
func (p *Projector) Intervals() []Interval {
	out := make([]Interval, 0, len(p.intervals))
	for _, iv := range p.intervals {
		out = append(out, *iv)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartAt.Equal(out[j].StartAt) {
			return out[i].IntervalID < out[j].IntervalID
		}
		return out[i].StartAt.Before(out[j].StartAt)
	})
	return out
}

// Query 按半开窗口裁剪，并附带时区日切；缺边界区间不贡献完整时长。
func (p *Projector) Query(w Window, loc *time.Location) []Interval {
	if loc == nil {
		loc = time.UTC
	}
	var out []Interval
	for _, iv := range p.Intervals() {
		clipped, ok := ClipToWindow(iv, w)
		if !ok {
			continue
		}
		if !clipped.Open {
			clipped.DaySplits = SplitByDay(clipped, loc)
		}
		out = append(out, clipped)
	}
	return out
}

// CompletedSeconds 按 kind 汇总已完成时长（秒）；Open／缺 end 不计入。
func CompletedSeconds(intervals []Interval, kind Kind) int64 {
	var total time.Duration
	for _, iv := range intervals {
		if iv.Kind != kind {
			continue
		}
		d, ok := iv.Duration()
		if !ok {
			continue
		}
		total += d
	}
	return int64(total / time.Second)
}
