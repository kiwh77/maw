package observe

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/adapters/codebuddy"
	"github.com/kiwh77/maw/internal/adapters/pi"
	"github.com/kiwh77/maw/internal/engine/model"
)

// 来源命名空间：计量与去重身份的第一段。
const (
	CodeBuddySourceNS = "codebuddy"
	PiSourceNS        = "pi"
)

// codeBuddyCacheEvidence 是「input 已含缓存分解」的实测锚点（能力矩阵 C17／C18）。
const codeBuddyCacheEvidence = "docs/evidence/codebuddy/samples/VN301-S03-print-stream-json (C17/C18)"

// IsPiProtocol 报告协议是否是 Pi 运行时冻结的那一套（pi-stream-json/1）。
func IsPiProtocol(p model.Protocol) bool {
	return strings.TrimSpace(p.Format) == pi.ProtocolFormat &&
		strings.TrimSpace(p.Version) == pi.ProtocolVersion
}

// IsCodeBuddyProtocol 报告协议是否是 CodeBuddy 实测过的那一套（stream-json/1）。
func IsCodeBuddyProtocol(p model.Protocol) bool {
	return strings.TrimSpace(p.Format) == "stream-json" &&
		strings.TrimSpace(p.Version) == codebuddy.ProtocolVersion
}

// codeBuddyExtractor 从 CodeBuddy 的 stream-json 记录里解析运行级观测。
type codeBuddyExtractor struct{}

// SourceNS 实现 Extractor。
func (codeBuddyExtractor) SourceNS() string { return CodeBuddySourceNS }

// Extract 解析一条 CodeBuddy 记录。
//
// 只有实测过的记录形状才产事实：带 session_id 的记录给出会话身份；
// usage 只从 result 记录解析（C19：assistant 记录上的 usage 聚合口径未验证，一律不解析）。
// 没有来源会话身份就没有事实——调用方保持未归属，而不是被塞进一个假会话。
func (codeBuddyExtractor) Extract(rec Record) (Facts, bool) {
	ref, err := codebuddy.ParseSessionRef(rec.Payload)
	if err != nil || ref == nil || strings.TrimSpace(ref.SessionID) == "" {
		return Facts{}, false
	}
	facts := Facts{
		SourceNS:   CodeBuddySourceNS,
		SessionID:  ref.SessionID,
		RecordUUID: ref.RecordUUID,
		RecordType: ref.RecordType,
		RecordRef:  rec.Ref(),
		OccurredAt: rec.ReceivedAt,
		// 旧 CLI 只给整次运行的汇总：这是运行级事实，不是逐次调用。
		Scope:   ScopeRun,
		Quality: model.QualityUnknown,
		Raw:     rawMap(rec.Payload),
	}
	usage, err := codebuddy.ParseUsage(rec.Payload)
	if err != nil || usage == nil {
		// 没有用量也要给出会话身份：run 级调用事实可以由会话认出来。
		return facts, true
	}
	facts.Semantic = string(usage.Semantic)
	facts.InputTokens = usage.InputTokens
	facts.OutputTokens = usage.OutputTokens
	facts.CacheReadTokens = usage.CacheReadTokens
	facts.CacheWriteTokens = usage.CacheWriteTokens
	facts.TotalTokens = usage.TotalTokens
	facts.Quality = usage.Quality
	facts.Evidence = usage.Evidence
	if facts.HasCache() {
		// C18 实测：CodeBuddy 的 input_tokens 已含缓存分解，不得再相加。
		included := true
		facts.CacheIncludedInInput = &included
		facts.CacheEvidence = codeBuddyCacheEvidence
	}
	return facts, true
}

// rawMap 保留来源原始字段；解析不了就留空，不编。
func rawMap(payload []byte) map[string]any {
	out := map[string]any{}
	if len(payload) == 0 {
		return out
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// timeOr 返回解析出的时间；解析不了就用兜底时间（不猜时刻，但也不丢事实）。
func timeOr(raw string, fallback time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw)); err == nil {
		return t.UTC()
	}
	return fallback.UTC()
}
