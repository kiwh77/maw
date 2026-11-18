package observe

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// piContract 是 Pi 运行时事件信封的契约标识（契约 §5.2）。
const piContract = "maw.pi.execution/v1"

// piEnvelope 是运行时输出的一行事件信封。
type piEnvelope struct {
	Contract   string          `json:"contract"`
	EventID    string          `json:"event_id"`
	Type       string          `json:"type"`
	RunID      string          `json:"run_id"`
	Generation int             `json:"generation"`
	CallID     string          `json:"call_id"`
	OccurredAt string          `json:"occurred_at"`
	ReceivedAt string          `json:"received_at"`
	Payload    json.RawMessage `json:"payload"`
}

// piUsagePayload 是 usage 事件载荷（契约 §8／§9／§10）。
type piUsagePayload struct {
	Scope      string `json:"scope"`
	Semantic   string `json:"semantic"`
	Quality    string `json:"quality"`
	Basis      string `json:"basis"`
	UsageKind  string `json:"usage_kind"`
	Purpose    string `json:"purpose"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	NodeID     string `json:"node_id"`
	CallID     string `json:"call_id"`
	OccurredAt string `json:"occurred_at"`

	InputTokens      json.RawMessage `json:"input_tokens"`
	OutputTokens     json.RawMessage `json:"output_tokens"`
	CacheReadTokens  json.RawMessage `json:"cache_read_tokens"`
	CacheWriteTokens json.RawMessage `json:"cache_write_tokens"`
	TotalTokens      json.RawMessage `json:"total_tokens"`
	SourceTotal      json.RawMessage `json:"source_total_tokens"`

	// InputIncludesCache 是缓存归一证据（契约 §10.1）：只有来源明确给出才认。
	InputIncludesCache *bool  `json:"input_includes_cache"`
	CacheEvidence      string `json:"cache_evidence"`
}

// piSessionStarted 是会话开始载荷：只有模式与工作目录，没有用量。
type piSessionStarted struct {
	Mode string `json:"mode"`
}

// piExtractor 从 Pi 运行时的事件流里解析逐次调用观测。
type piExtractor struct{}

// SourceNS 实现 Extractor。
func (piExtractor) SourceNS() string { return PiSourceNS }

// Extract 解析一条 Pi 事件。
//
// Pi 是我们自己的运行时：usage 事件就是一次调用的事实（scope=call）。
// 只有 session_started 与 usage 承载观测事实；result 只描述终态，用量由 usage 事件承载，
// 因此不从 result 里再解析一份（避免同一轮记两次）。
func (piExtractor) Extract(rec Record) (Facts, bool) {
	var env piEnvelope
	if err := json.Unmarshal(rec.Payload, &env); err != nil {
		return Facts{}, false
	}
	if strings.TrimSpace(env.Contract) != piContract {
		// 契约不符说明这条流不归本解释器管：不猜、不降级。
		return Facts{}, false
	}
	if strings.TrimSpace(env.Type) != "usage" && strings.TrimSpace(env.Type) != "session_started" {
		return Facts{}, false
	}
	occurred := timeOr(env.OccurredAt, rec.ReceivedAt)
	callID := strings.TrimSpace(env.CallID)
	recordUUID := ""
	switch {
	case callID != "":
		// call_id 是一次调用的稳定身份：重放算出同一个 turn，不会记第二次。
		recordUUID = "call:" + callID
	case strings.TrimSpace(env.EventID) != "":
		recordUUID = "event:" + strings.TrimSpace(env.EventID)
	}
	facts := Facts{
		SourceNS:   PiSourceNS,
		SessionID:  piSessionID(env.RunID),
		RecordUUID: recordUUID,
		RecordType: strings.TrimSpace(env.Type),
		RecordRef:  rec.Ref(),
		CallID:     callID,
		OccurredAt: occurred,
		Quality:    model.QualityUnknown,
		Raw:        rawMap(rec.Payload),
	}
	if strings.TrimSpace(env.Type) == "session_started" {
		// 会话开始只给出用途（模式）：没有用量就不造用量。
		var started piSessionStarted
		_ = json.Unmarshal(env.Payload, &started)
		if purpose := strings.TrimSpace(started.Mode); PurposeValid(purpose) {
			facts.Purpose = purpose
		}
		return facts, true
	}
	return piUsageFacts(facts, env), true
}

// piSessionID 由 Run 派生会话身份。
//
// Pi 运行时不发 session_id（它一次进程只服务一次 Run）：用 Run 派生既保证可归属，
// 也可以重放，还不引入模型自述。来源自报的 session_id 若出现则不覆盖。
func piSessionID(runID string) string {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ""
	}
	return "run:" + runID
}

// piUsageFacts 把 usage 载荷翻成观测事实。
//
// 归一规则（契约 §8–§10）：
//   - scope 默认 call（逐次调用），来源显式说 run 才按运行级；
//   - semantic 默认 delta；只有来源实测到累计口径才认 cumulative；
//   - 缺 token 就是 nil，绝不填 0；格式错误（字符串／浮点／负数）记 invalid_usage；
//   - 缓存字段独立，归一证据只在来源明确给出时才认。
func piUsageFacts(base Facts, env piEnvelope) Facts {
	var p piUsagePayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return base
	}
	facts := base
	var invalid []string
	facts.InputTokens = piToken(p.InputTokens, "input_tokens", &invalid)
	facts.OutputTokens = piToken(p.OutputTokens, "output_tokens", &invalid)
	facts.CacheReadTokens = piToken(p.CacheReadTokens, "cache_read_tokens", &invalid)
	facts.CacheWriteTokens = piToken(p.CacheWriteTokens, "cache_write_tokens", &invalid)
	facts.TotalTokens = piToken(p.TotalTokens, "total_tokens", &invalid)

	scope := strings.TrimSpace(p.Scope)
	switch scope {
	case ScopeRun:
		facts.Scope = ScopeRun
	case ScopeCall, "":
		facts.Scope = ScopeCall
	default:
		// 不认识的口径按未声明处理：按调用记，并在证据里写明。
		facts.Scope = ScopeCall
		invalid = append(invalid, "scope="+scope+" 未识别，按 call 记")
	}
	semantic := strings.TrimSpace(p.Semantic)
	switch semantic {
	case "cumulative", "delta":
		facts.Semantic = semantic
	case "":
		facts.Semantic = "delta"
	default:
		facts.Semantic = "delta"
		invalid = append(invalid, "semantic="+semantic+" 未识别，按 delta 记")
	}
	if facts.Scope == ScopeRun {
		// 运行级事实：没有调用身份，避免被当成某一次调用。
		facts.CallID = ""
	}

	quality := model.Quality(strings.TrimSpace(p.Quality))
	if !quality.Valid() {
		quality = model.QualityUnknown
		invalid = append(invalid, "quality 缺省或非法，按 unknown 记")
	}
	facts.Quality = quality

	if purpose := firstNonEmpty(p.UsageKind, p.Purpose); purpose != "" {
		if PurposeValid(purpose) {
			facts.Purpose = purpose
		} else {
			invalid = append(invalid, "usage_kind="+purpose+" 不在封闭集合内")
		}
	}
	facts.Provider = strings.TrimSpace(p.Provider)
	facts.Model = strings.TrimSpace(p.Model)
	if node := strings.TrimSpace(p.NodeID); node != "" {
		facts.NodeID = node
	}
	if p.CallID != "" && facts.CallID == "" {
		facts.CallID = strings.TrimSpace(p.CallID)
	}

	evidence := []string{}
	if basis := strings.TrimSpace(p.Basis); basis != "" {
		evidence = append(evidence, basis)
	}
	if facts.TotalTokens != nil && facts.InputTokens != nil && facts.OutputTokens != nil &&
		*facts.TotalTokens != *facts.InputTokens+*facts.OutputTokens {
		// 契约 P171：total 必须等于 input+output。来源对不上就不采信这个数（不猜）。
		evidence = append(evidence, "来源 total_tokens 与 input+output 不一致，已丢弃 total")
		facts.TotalTokens = nil
	}
	if sourceTotal := piToken(p.SourceTotal, "source_total_tokens", &invalid); sourceTotal != nil {
		evidence = append(evidence, "来源自报 total="+itoa(*sourceTotal))
	}
	if len(invalid) > 0 {
		evidence = append(evidence, "invalid_usage: "+strings.Join(invalid, "；"))
		if facts.Quality == model.QualityObserved {
			// 有格式错误的字段就不能再声称 observed（契约 §10.2）。
			facts.Quality = model.QualityUnknown
		}
	}
	if facts.HasCache() {
		if p.InputIncludesCache != nil {
			included := *p.InputIncludesCache
			facts.CacheIncludedInInput = &included
			facts.CacheEvidence = firstNonEmpty(p.CacheEvidence,
				"运行时自报 input_includes_cache="+boolText(included))
		} else {
			facts.CacheEvidence = firstNonEmpty(p.CacheEvidence,
				"缺归一证据：来源给了缓存分量，但没有说明 input 是否已含缓存分解")
		}
	}
	facts.Evidence = strings.Join(evidence, "；")
	return facts
}

// piToken 解析一个 token 字段。
//
// 非数字、浮点、负数都算格式错误：记进 invalid 后返回 nil——不猜数值，也不填 0。
func piToken(raw json.RawMessage, name string, invalid *[]string) *int64 {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		// 字符串形式的数字是格式错误：encoding/json 会宽容地接受 "1200"，
		// 但契约 §10.2 要求按 invalid_usage 处理，不猜数值。
		*invalid = append(*invalid, name+" 是字符串，不是数字")
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err != nil {
		*invalid = append(*invalid, name+" 不是数字")
		return nil
	}
	v, err := num.Int64()
	if err != nil {
		*invalid = append(*invalid, name+" 不是整数")
		return nil
	}
	if v < 0 {
		*invalid = append(*invalid, name+" 为负")
		return nil
	}
	return &v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
