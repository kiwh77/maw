// Package observe 定义运行观测的事实与落库端口。
//
// 观测与运行事实分开：运行事实回答「这次运行发生了什么」，观测回答
// 「这次运行在来源侧对应哪一轮对话、用了多少 Token」。
//
// 缺失就是缺失：Token 指针为 nil 表示来源没给，不得用 0 冒充（计量契约 T142）。
// 0 是「实测为零」，不是「没看到」。
package observe

import (
	"context"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// Facts 是一次运行观测到的事实。
//
// 身份字段（SourceNS／SessionID／RecordUUID）来自来源侧，RunID／InstanceID
// 指向本库里的这次运行；两边的对应关系由 WriteFacts 落成显式关联。
type Facts struct {
	RunID      string
	InstanceID string
	ProjectID  string

	// SourceNS 是来源命名空间（如 codebuddy）。
	SourceNS string
	// SessionID 是来源侧的会话身份，不是本库的 session_id。
	SessionID  string
	RecordUUID string
	RecordType string
	// RecordRef 指向原始记录位置（journal 序号与行号），用于稳定兜底身份。
	RecordRef string

	OccurredAt time.Time

	// Semantic 声明用量口径（delta／cumulative）；空表示这次没有用量观测。
	Semantic         string
	Scope            string
	InputTokens      *int64
	OutputTokens     *int64
	CacheReadTokens  *int64
	CacheWriteTokens *int64
	TotalTokens      *int64
	Quality          model.Quality
	Evidence         string
}

// HasUsage 报告是否观测到用量。
//
// 只有真的拿到 token 才算观测到：全 nil 说明来源没给，这时不写用量记录。
func (f Facts) HasUsage() bool {
	return f.InputTokens != nil || f.OutputTokens != nil ||
		f.CacheReadTokens != nil || f.CacheWriteTokens != nil || f.TotalTokens != nil
}

// HasTurn 报告是否具备「一轮对话」的身份（轮次与关联都要有）。
func (f Facts) HasTurn() bool {
	return f.RecordType == "result" || f.HasUsage()
}

// Writer 是观测落库端口。
//
// 没有实现就不落观测：这是有意的——落不了的事实宁可缺席，也不能编。
type Writer interface {
	WriteFacts(ctx context.Context, f Facts) error
}
