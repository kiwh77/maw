package observability

import (
	"context"
	"time"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/projection"
)

// TimelineQuery 是一次时间轴查询（窗口与过滤已由传输层解析并校验）。
type TimelineQuery struct {
	ProjectID string
	Window    projection.Window
	Loc       *time.Location
	// SourceNS 为空表示不按来源过滤。
	SourceNS   string
	InstanceID string
	RunID      string
	Kinds      []projection.Kind
}

// TimelineData 是投影给出的一次快照。
//
// Intervals 已按窗口裁剪；Cursors 是**双水位**（事实水位与投影水位分开），
// 缺了它就无法判断「这份区间集合是否完整」。
type TimelineData struct {
	Intervals []projection.Interval
	Cursors   projection.Cursors
	// Gaps 是采集缺口，必须升序且互不重叠（契约 §2）。
	Gaps []Gap
	// Unsupported 由数据源填写：它无法按本次过滤条件提供数据时使用，
	// 例如来源过滤需要会话关联但投影没有来源维度。
	Unsupported []Unsupported
	// Missing 由数据源填写：某些分区确实缺测（例如未接入的用量采集）。
	Missing []Missing
}

// TimelineSource 提供分层区间。内存投影（internal/projection）与测试替身都实现它。
type TimelineSource interface {
	Timeline(ctx context.Context, q TimelineQuery) (TimelineData, error)
}

// StatsQuery 是一次统计查询；范围字段会在报表里回显（契约 §5）。
type StatsQuery struct {
	ProjectID string
	SourceNS  string
	Window    projection.Window
	Loc       *time.Location
}

// StatsSource 产出带水位与完整性的统计报表。
//
// 它同时是时间轴 totals 的来源：时间轴的合计与统计页必须同口径，
// 所以两边都从同一个报表拿数，不在时间轴接口里另算一遍并集。
type StatsSource interface {
	Report(ctx context.Context, q StatsQuery, data TimelineData) (*analytics.Report, error)
}

// SessionQuery 是一次会话列表查询。
type SessionQuery struct {
	ProjectID string
	SourceNS  string
	Window    projection.Window
	Limit     int
	Cursor    string
}

// SessionPage 是一页会话。
type SessionPage struct {
	Items []SessionItem
	Next  string
	// Watermark 是读取时的水位；会话与轮次是事实直读，没有投影落后，
	// 因此实现通常令 complete_before == cursor。
	Watermark *Watermark
	// Completeness 由实现判定：有未关闭会话／轮次时为 partial。
	Completeness projection.Completeness
	// Missing 是本次读取的缺测说明（例如轮次明细被单会话上限截断）。
	Missing []Missing
}

// SessionSource 提供会话与轮次明细（F37）。
type SessionSource interface {
	Sessions(ctx context.Context, q SessionQuery) (SessionPage, error)
}

// ReminderQuery 是一次提醒列表查询。
type ReminderQuery struct {
	ProjectID string
	Window    projection.Window
	// Status／Kind／Channel 为空表示不过滤；取值由传输层按封闭集合校验。
	Status  string
	Kind    string
	Channel string
	Limit   int
	Cursor  string
}

// ReminderPage 是一页提醒。
type ReminderPage struct {
	Items []ReminderItem
	Next  string
	// Unhandled 是范围内未确认的条数（不受分页影响）：首页要能显示角标。
	Unhandled int
	// Counts 是按状态分组的条数，用于「待处理／已确认」标签页。
	Counts map[string]int
	// Watermark 是读取时的水位（提醒是事实直读，complete_before 通常等于 cursor）。
	Watermark *Watermark
	// Completeness 由实现判定。
	Completeness projection.Completeness
}

// ReminderSource 提供提醒查询与确认。
//
// Ack 必须是幂等的：重复确认返回 Changed=false，不报错、不产生第二条记录。
type ReminderSource interface {
	Reminders(ctx context.Context, q ReminderQuery) (ReminderPage, error)
	Ack(ctx context.Context, projectID, notificationID string, at time.Time) (AckResult, error)
}

// ChangeSignal 是 SSE 失效通知的载荷。
//
// 只允许出现「哪类数据变了 + 水位」，不得携带事实正文：SSE 掉帧是正常的，
// 事实必须能通过查询接口恢复（契约 §12.2）。
type ChangeSignal struct {
	// Watermark 是服务端此刻的水位提示；客户端据此决定是否补拉。
	Watermark *Watermark `json:"watermark,omitempty"`
	// Revision 是可选的工程级聚合版本。
	Revision int64 `json:"revision,omitempty"`
	// At 是发出通知的服务端时间（诊断用，不参与游标）。
	At time.Time `json:"at"`
	// Capabilities 是本次可能失效的数据类别（timeline／activity／sessions／stats／reminders）。
	Capabilities []string `json:"capabilities,omitempty"`
}

// ChangeSource 向 SSE 订阅者投递失效信号；测试可注入内存实现。
type ChangeSource interface {
	Subscribe(ctx context.Context) (<-chan ChangeSignal, func())
}
