package projection

import (
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// Kind 是四类区间之一（契约 §1）。
type Kind string

const (
	KindTaskPhase    Kind = "task_phase"
	KindCLIProcess   Kind = "cli_process"
	KindTurn         Kind = "turn"
	KindExplicitWait Kind = "explicit_wait"
)

// Valid 报告类别是否封闭。
func (k Kind) Valid() bool {
	switch k {
	case KindTaskPhase, KindCLIProcess, KindTurn, KindExplicitWait:
		return true
	}
	return false
}

// Action 打开或关闭一条区间。
type Action string

const (
	ActionOpen  Action = "open"
	ActionClose Action = "close"
)

// Fact 是投影输入的一条边界事实（按入库游标消费）。
type Fact struct {
	Cursor      int64
	OccurredAt  time.Time
	ReceivedAt  time.Time
	LateArrival bool

	Kind       Kind
	Action     Action
	IntervalID string // 稳定身份；同 id 的 open/close 成对
	InstanceID string
	RunID      string
	Generation int
	Phase      string // task_phase
	Reason     string // explicit_wait
	Quality    model.Quality
	Evidence   string
}

// DaySplit 是按查询时区切开的本地日片段。
type DaySplit struct {
	Day     string    `json:"day"` // YYYY-MM-DD（本地）
	StartAt time.Time `json:"start_at"`
	EndAt   time.Time `json:"end_at"`
}

// Interval 是一条投影区间。
type Interval struct {
	IntervalID  string        `json:"interval_id"`
	Kind        Kind          `json:"kind"`
	InstanceID  string        `json:"instance_id,omitempty"`
	RunID       string        `json:"run_id,omitempty"`
	Generation  int           `json:"generation,omitempty"`
	Phase       string        `json:"phase,omitempty"`
	Reason      string        `json:"reason,omitempty"`
	StartAt     time.Time     `json:"start_at"`
	EndAt       *time.Time    `json:"end_at"` // nil = 未结束
	Quality     model.Quality `json:"quality"`
	EvidenceRef string        `json:"evidence_ref,omitempty"`
	LateArrival bool          `json:"late_arrival,omitempty"`
	DaySplits   []DaySplit    `json:"day_splits,omitempty"`
	// Open 表示缺少关闭边界；不参与「已完成时长」。
	Open bool `json:"open"`
}

// Duration 返回已完成时长；未结束或缺失边界返回 0 且 ok=false。
func (iv Interval) Duration() (time.Duration, bool) {
	if iv.Open || iv.EndAt == nil {
		return 0, false
	}
	if iv.EndAt.Before(iv.StartAt) {
		return 0, false
	}
	return iv.EndAt.Sub(iv.StartAt), true
}

// Window 是半开查询窗口 [Start, End)。
type Window struct {
	Start time.Time
	End   time.Time
}

// Valid 要求 End 严格晚于 Start。
func (w Window) Valid() bool {
	return !w.Start.IsZero() && !w.End.IsZero() && w.End.After(w.Start)
}

// Completeness 描述投影相对事实的完整度。
type Completeness string

const (
	CompletenessComplete Completeness = "complete"
	CompletenessPartial  Completeness = "partial"
	CompletenessUnknown  Completeness = "unknown"
)

// Cursors 分开返回业务状态水位与投影水位（架构 §10.3）。
type Cursors struct {
	// StateCursor 是已见到的最大事实入库游标（可领先于投影）。
	StateCursor int64 `json:"state_cursor"`
	// ProjectionCursor 是已完整投影的最大游标（complete_before）。
	ProjectionCursor int64 `json:"projection_cursor"`
	// Version 是投影算法／物化版本。
	Version int `json:"projection_version"`
	// Completeness 有脏范围或未关闭区间时不为 complete。
	Completeness Completeness `json:"completeness"`
}

// Name 是写入 projection_state 的投影名。
const Name = "timeline"

// Version 是当前投影实现版本。
const Version = 1
