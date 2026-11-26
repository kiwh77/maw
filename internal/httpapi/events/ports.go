package events

import (
	"context"
	"time"

	"github.com/kiwh77/maw/internal/events"
	"github.com/kiwh77/maw/internal/query"
)

// Store 是只读友好的查询入口（与 httpapi/read 相同约定）。
type Store = query.Store

// ChangeSignal 是一次失效通知的载荷：不携带事实正文。
type ChangeSignal struct {
	// Watermark 是服务端此刻的事件／修订水位提示；客户端据此决定是否补拉。
	Watermark query.Watermark `json:"watermark"`
	// Revision 是可选的工程级聚合版本。
	Revision int64 `json:"revision,omitempty"`
	// At 是发出通知的服务端时间（诊断用，不参与游标）。
	At time.Time `json:"at"`
}

// ChangeSource 向 SSE 订阅者投递失效信号。测试可注入内存实现。
type ChangeSource interface {
	// Subscribe 返回信号通道与取消函数；通道关闭表示源结束。
	Subscribe(ctx context.Context) (<-chan ChangeSignal, func())
}

// ReportRequest 是旁路上报的受理输入（HTTP 已鉴权后的形态）。
type ReportRequest struct {
	Actor          string
	ProjectID      string
	IdempotencyKey string
	Source         events.Source
	Observations   []events.Observation
	// Legacy 字段：旧 /api/status 风格的单条进度；转成一条观测事实。
	LegacyMessage string
	LegacyPhase   string
	LegacyPct     int
	LegacyTaskRef string // 显示号或 instance_id；只进载荷，不改任务状态
	LegacyStatus  string // agent 侧状态词，不是任务状态机
}

// ReportResult 是上报受理结果。
type ReportResult struct {
	Accepted           int
	Duplicates         int
	Conflicts          int
	Rejected           int
	ChangedTaskStatus  bool // 契约要求恒为 false
	IdempotentReplay   bool
	RejectionSummaries []string
}

// Reporter 把旁路上报摄取并（可选）落库；不得改业务任务状态。
type Reporter interface {
	Accept(ctx context.Context, req ReportRequest) (ReportResult, error)
}
