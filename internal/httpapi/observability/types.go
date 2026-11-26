package observability

import (
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/projection"
)

// Status 是观测读接口的类型化结果状态（封闭集合）。
//
// 它回答「这份数据能不能直接用」，而不是「请求成功没有」：HTTP 200 只说明服务端
// 处理完了，status 才说明数据可不可信。前端据此决定是否显示「部分未知」横幅，
// 不靠猜字段缺失。
type Status string

const (
	// StatusReady：水位已追上事实，完整性为 complete。
	StatusReady Status = "ready"
	// StatusPartial：有脏范围、未闭合区间或采集缺口；已观测部分仍然可信。
	StatusPartial Status = "partial"
	// StatusStale：投影落后于事实水位（complete_before < cursor），需要补算。
	StatusStale Status = "stale"
	// StatusUnknown：投影从未推进或完整性未知；不能当作「零活动」。
	StatusUnknown Status = "unknown"
	// StatusUnsupported：本次请求依赖的能力没有配置或无法按请求的过滤条件提供。
	StatusUnsupported Status = "unsupported"
)

// Valid 报告状态是否属于封闭集合。
func (s Status) Valid() bool {
	switch s {
	case StatusReady, StatusPartial, StatusStale, StatusUnknown, StatusUnsupported:
		return true
	}
	return false
}

// Capability 描述本次查询依赖的能力是否可用。
//
// 不可用时给出 reason（稳定的机器码）而不是让人读文案猜：调用方要能区分
// 「没接线」和「接线了但这次过滤不支持」。
type Capability struct {
	// Name 是能力名，例如 timeline／activity／sessions／stats／reminders。
	Name string `json:"name"`
	// Supported 为 false 时 items 必然为空，status 必为 unsupported。
	Supported bool `json:"supported"`
	// Reason 是稳定机器码（source_not_configured／source_filter_not_supported…）。
	Reason string `json:"reason,omitempty"`
}

// Gap 是采集缺口（契约 §2）：[from, to] 闭区间内的游标没有完整事实。
//
// 落在缺口里的时长与轮次必须标 unknown，不得按均值补齐，也不得从分母里悄悄剔除。
type Gap struct {
	From   int64  `json:"from"`
	To     int64  `json:"to"`
	Reason string `json:"reason"`
}

// Watermark 是投影水位（契约 §2）。
//
// CompleteBefore 之后的区间与轮次可能仍会补；Cursor 是已消费的最大入库游标。
// 两个值分开给，读者才能判断「这份报表的截止点在哪」。
type Watermark struct {
	Cursor         int64     `json:"cursor"`
	CompleteBefore int64     `json:"complete_before"`
	ReceivedAt     time.Time `json:"received_at"`
	Gaps           []Gap     `json:"gaps"`
}

// ProjectionLag 表示投影落后于事实水位（complete_before < cursor）。
//
// 它单独成字段而不是只体现在 status 里：落后多少条是运维要看的具体数字。
type ProjectionLag struct {
	Cursor         int64 `json:"cursor"`
	CompleteBefore int64 `json:"complete_before"`
	Pending        int64 `json:"pending"`
}

// Missing 是「缺测」的类型化说明。
//
// Section 是被判定为未知的报表分区（tokens／hours／totals／source_filter…），
// Reason 是稳定机器码。缺测不等于 0：调用方必须按「没有数据」展示。
type Missing struct {
	Section string `json:"section"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail,omitempty"`
}

// Unsupported 是「不支持能力」的类型化说明。
type Unsupported struct {
	// Capability 是能力名，例如 timeline.source_filter。
	Capability string `json:"capability"`
	// Reason 是稳定机器码。
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// Window 是回显给客户端的半开查询窗口 [Start, End)。
//
// 无论客户端是否显式给了窗口，响应都回显实际使用的窗口：默认窗口必须可见，
// 否则「没数据」和「查错了时间段」分不开。
type Window struct {
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Timezone  string    `json:"timezone"`
	Hours     int       `json:"hours"`
	Defaulted bool      `json:"defaulted,omitempty"`
}

// Scope 回显本次查询的过滤条件（契约 §5：报表必须带范围）。
//
// 前端搜索只影响局部展示时，服务端仍以这里的范围为准，不把子集当全集统计。
type Scope struct {
	ProjectID  string            `json:"project_id,omitempty"`
	SourceNS   string            `json:"source_ns,omitempty"`
	InstanceID string            `json:"instance_id,omitempty"`
	RunID      string            `json:"run_id,omitempty"`
	Kinds      []projection.Kind `json:"kinds,omitempty"`
}

// Result 是所有观测读响应的公共部分。
//
// 五个读接口都必须带全这些字段：缺 watermark／completeness 的报表视为不可比较
// （契约 §5），所以它们不是可选项。
type Result struct {
	Status        Status                  `json:"status"`
	Capability    Capability              `json:"capability"`
	Scope         Scope                   `json:"scope"`
	Window        *Window                 `json:"window,omitempty"`
	Completeness  projection.Completeness `json:"completeness"`
	Watermark     *Watermark              `json:"watermark,omitempty"`
	Gaps          []Gap                   `json:"gaps"`
	ProjectionLag *ProjectionLag          `json:"projection_lag,omitempty"`
	Missing       []Missing               `json:"missing"`
	Unsupported   []Unsupported           `json:"unsupported"`
}

// meta 是所有成功响应的外层：ok 恒为 true，失败走 localauth.WriteJSONError。
type meta struct {
	OK bool `json:"ok"`
}

// TimelineItem 是时间轴的一条区间。
//
// 直接复用 projection.Interval 的 JSON 形状（interval_id／kind／open／quality…），
// 前端 normalizeIntervals 按这些字段判定「未知或未闭合不补满」。
type TimelineItem = projection.Interval

// SessionItem 是一个来源会话及其窗口内轮次（F37：下钻到会话与轮次）。
type SessionItem struct {
	SessionID string `json:"session_id"`
	ProjectID string `json:"project_id,omitempty"`
	SourceNS  string `json:"source_ns"`
	Adapter   string `json:"adapter,omitempty"`
	// ExternalID 是来源侧身份；只做展示与追溯，不作为本地主键。
	ExternalID string `json:"external_id"`
	OpenedAt   string `json:"opened_at"`
	ClosedAt   string `json:"closed_at,omitempty"`
	// SpanSeconds 是会话整体跨度；未关闭时算到窗口终点，并置 Open。
	SpanSeconds int64 `json:"span_seconds"`
	// Open 为真表示会话没有关闭边界：跨度是下界，不是完整时长。
	Open bool `json:"open"`
	// Unattributed 为真表示会话自身没有工程归属，靠 Run 关联命中本次过滤。
	Unattributed bool       `json:"unattributed"`
	TurnCount    int        `json:"turn_count"`
	Turns        []TurnItem `json:"turns"`
	// TurnsTruncated 为真表示该会话的轮次超过单会话上限，明细不完整。
	TurnsTruncated bool `json:"turns_truncated,omitempty"`
	// Usage 是该会话窗口内轮次的去重累计；未知分量保持 null。
	Usage TurnUsage `json:"usage"`
}

// TurnItem 是一轮对话的时间与用量。
type TurnItem struct {
	TurnID string `json:"turn_id"`
	// SessionID 便于前端在扁平列表里重建归属。
	SessionID       string   `json:"session_id"`
	SourceNS        string   `json:"source_ns"`
	StartedAt       string   `json:"started_at"`
	EndedAt         string   `json:"ended_at,omitempty"`
	Open            bool     `json:"open"`
	DurationSeconds int64    `json:"duration_seconds"`
	InstanceIDs     []string `json:"instance_ids,omitempty"`
	// SharedWith 非空表示一轮同时服务多道题：用量只计一次（契约 §4.2）。
	SharedWith []string  `json:"shared_with,omitempty"`
	Usage      TurnUsage `json:"usage"`
}

// TurnUsage 是按轮去重后的用量；nil 表示未知，绝不用 0 冒充（契约 §4.3）。
type TurnUsage struct {
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	CacheReadTokens  *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
	TotalTokens      *int64 `json:"total_tokens,omitempty"`
	// Quality 取 observed／estimated／unknown；多轮汇总时 unknown 优先。
	Quality model.Quality `json:"quality"`
	// UnknownTurns 是参与汇总但用量未知的轮次数；非 0 时报表必须显示「部分未知」。
	UnknownTurns int `json:"unknown_turns"`
	// Unallocated 为真表示有关联但没有合法分摊依据（契约 §4.2）。
	Unallocated bool `json:"unallocated"`
}

// ReminderItem 是一条提醒记录（F33：站内／桌面托盘，可确认）。
//
// 字段是 notification_deliveries 的读投影：写入语义（尝试计数、去重、恢复）
// 仍归 internal/notifications，本包只读与确认。
type ReminderItem struct {
	NotificationID string `json:"notification_id"`
	ProjectID      string `json:"project_id,omitempty"`
	EventID        string `json:"event_id"`
	Kind           string `json:"kind"`
	Channel        string `json:"channel"`
	Subject        string `json:"subject,omitempty"`
	State          string `json:"state,omitempty"`
	Title          string `json:"title,omitempty"`
	Body           string `json:"body,omitempty"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
	DeliveredAt    string `json:"delivered_at,omitempty"`
	AckedAt        string `json:"acked_at,omitempty"`
	// Attempts／AmbiguousAttempts 是投递痕迹；Ambiguous 只增不减。
	Attempts          int  `json:"attempts"`
	AmbiguousAttempts int  `json:"ambiguous_attempts"`
	OpenAttempt       bool `json:"open_attempt"`
	// DuplicateRisk 为真表示再投一次可能让用户看到第二遍。
	DuplicateRisk bool `json:"duplicate_risk"`
}

// AckResult 是确认提醒的结果。
//
// ChangedTaskStatus 恒为 false：确认提醒不改业务任务状态（HTTP 契约 §7.4）。
type AckResult struct {
	Notification      *ReminderItem
	Changed           bool
	IdempotentReplay  bool
	ChangedTaskStatus bool
}

// capabilities 是本次请求依赖的能力清单，用于响应头部的 capability 字段与
// 能力发现接口。
func capabilityOf(name string, supported bool, reason string) Capability {
	return Capability{Name: name, Supported: supported, Reason: reason}
}
