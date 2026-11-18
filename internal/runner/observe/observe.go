// Package observe 定义运行观测的事实与落库端口。
//
// 观测与运行事实分开：运行事实回答「这次运行发生了什么」，观测回答
// 「这次运行在来源侧对应哪一轮对话、哪一次调用、用了多少 Token」。
//
// 三条口径在这里冻结（契约 maw.pi.execution/v1 §8–§11、
// maw.observability.timeline/v1 §4）：
//
//  1. 逐次调用（ScopeCall）与运行级（ScopeRun）是两类事实，分开存放、不得相加；
//  2. Token 指针为 nil 表示来源没给，不得用 0 冒充；0 是「实测为零」；
//  3. 缓存字段独立记账，是否已含在 input 里必须有证据，没证据就是未归一。
//
// 归属（项目、节点、Run、代际、用途）由 maw 绑定，来源只描述事实：
// 模型输出里的任何「我是谁／我这轮属于哪题」都不作为绑定依据。
package observe

import (
	"context"
	"strconv"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// 用量口径（契约 §8／§9）。两类事实分开存放，有 call 级时 run 级不参与汇总。
const (
	ScopeCall = "call"
	ScopeRun  = "run"
)

// 用量用途（契约 §3 的 usage_kind）。来源只描述用途，归属与预算口径由 maw 决定。
const (
	PurposeWorker   = "worker"
	PurposeLead     = "lead"
	PurposeDiagnose = "diagnose"
)

// 缓存归一状态（契约 §10.1）。
const (
	// CacheAbsent：来源没给缓存字段。
	CacheAbsent = "absent"
	// CacheVerified：有证据说明缓存分解已含在 input 里（不得再相加）。
	CacheVerified = "verified"
	// CacheUnverified：给了缓存字段但没有归一证据——不得自行拆分，也不得并入 input。
	CacheUnverified = "unverified"
)

// PurposeValid 报告用途是否属于封闭集合；空值表示未声明。
func PurposeValid(p string) bool {
	switch p {
	case "", PurposeWorker, PurposeLead, PurposeDiagnose:
		return true
	}
	return false
}

// Facts 是一次运行观测到的事实。
//
// 身份分两层：归属字段（ProjectID／NodeID／RunID／Generation／Purpose）由 maw
// 绑定；来源字段（SourceNS／SessionID／RecordUUID／CallID）来自来源侧。
type Facts struct {
	// 归属：maw 侧绑定，来源不得自述。
	ProjectID  string
	NodeID     string
	RunID      string
	InstanceID string
	// Generation 是这次运行的代际；跨代际不得合并。
	Generation int
	// Purpose 是用量用途（worker／lead／diagnose）；空表示未声明，按未知保留。
	Purpose string

	// 来源身份。
	SourceNS string
	// SessionID 是来源侧的会话身份，不是本库的 session_id。
	SessionID  string
	RecordUUID string
	RecordType string
	// RecordRef 指向原始记录位置（journal 序号与行号），用于稳定兜底身份。
	RecordRef string
	// CallID 是来源侧为一次模型请求生成的稳定身份；空表示运行级事实。
	CallID string

	// Provider／Model 是实际供应商与模型名；来源没报就留空（未知），不猜。
	Provider string
	Model    string

	OccurredAt time.Time

	// Semantic 声明用量口径（delta／cumulative）；空表示这次没有用量观测。
	Semantic string
	Scope    string
	// Baseline 标记「运行基线」：没有任何调用事实时的兜底痕迹，不是调用事实。
	Baseline bool

	InputTokens      *int64
	OutputTokens     *int64
	CacheReadTokens  *int64
	CacheWriteTokens *int64
	TotalTokens      *int64
	Quality          model.Quality

	// CacheIncludedInInput 是「缓存分解已含在 input 里」的三态证据：
	// nil=没有证据（不得归一）、true=有证据已含、false=有证据不含。
	CacheIncludedInInput *bool
	// CacheEvidence 指向归一证据（样本／能力矩阵条目）；没有证据时必须写明缺什么。
	CacheEvidence string

	Evidence string
	// Raw 保留来源原始字段：归一可以修数字，不能把原始记录抹掉。
	Raw map[string]any
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
	return f.RecordType == "result" || f.HasUsage() || f.IsCall()
}

// IsCall 报告这是否逐次调用事实（有调用身份，而不是运行级汇总）。
func (f Facts) IsCall() bool {
	return f.Scope == ScopeCall || f.CallID != ""
}

// CacheState 按证据判定缓存归一状态（契约 §10.1）。
func (f Facts) CacheState() string {
	if f.CacheReadTokens == nil && f.CacheWriteTokens == nil {
		return CacheAbsent
	}
	if f.CacheIncludedInInput != nil {
		return CacheVerified
	}
	return CacheUnverified
}

// HasCache 报告来源是否给了缓存分量。
func (f Facts) HasCache() bool {
	return f.CacheReadTokens != nil || f.CacheWriteTokens != nil
}

// Writer 是观测落库端口。
//
// 没有实现就不落观测：这是有意的——落不了的事实宁可缺席，也不能编。
type Writer interface {
	WriteFacts(ctx context.Context, f Facts) error
}

// Record 是一条来源原始记录：journal 里的 payload 与位置。
type Record struct {
	Payload    []byte
	Seq        int64
	Line       int
	ReceivedAt time.Time
}

// Ref 是这条记录在本库里的稳定位置引用（重放算出同一个身份）。
func (r Record) Ref() string {
	return strconv.FormatInt(r.Seq, 10) + ":" + strconv.Itoa(r.Line)
}

// Extractor 把一个来源的原始记录翻译成观测事实。
//
// 每个来源一套解释：CodeBuddy 给运行级用量，Pi 给逐次调用用量。
// 不认识就返回 ok=false——不假装理解，也不补造字段。
type Extractor interface {
	SourceNS() string
	Extract(rec Record) (Facts, bool)
}

// ExtractorFor 按协议选来源解释器。
//
// 判定口径是协议而不是厂商名：「大概是兼容的」不算兼容，版本对不上就不解释。
func ExtractorFor(p model.Protocol) (Extractor, bool) {
	switch {
	case IsPiProtocol(p):
		return piExtractor{}, true
	case IsCodeBuddyProtocol(p):
		return codeBuddyExtractor{}, true
	default:
		return nil, false
	}
}
