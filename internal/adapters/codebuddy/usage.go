package codebuddy

import (
	"encoding/json"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// UsageSemantic 是用量口径声明；取值与计量契约 §4 对齐
// （docs/contracts/vnext/observability/timeline-metering-contract-v1.md）。
//
// 本适配器只产出 delta：一次 headless 运行恰好一条 result 记录（C08），
// result.usage 描述的就是这一次运行的用量（C03/C17）。来源没有给出跨运行的
// 累计计数，也没有观测到累计值重置，因此不声明 cumulative——口径必须有证据。
type UsageSemantic string

const (
	// UsageSemanticDelta 表示这条记账本身就是增量，直接落库（VN-804 ObserveUsage delta 路径）。
	UsageSemanticDelta UsageSemantic = "delta"
	// UsageSemanticCumulative 保留给未来实测到累计口径的版本；当前版本不得使用（C19）。
	UsageSemanticCumulative UsageSemantic = "cumulative"
)

// 用量能力的证据锚点：字段存在（C17）与「input 已含缓存分解」（C18）是 observed；
// 多记录聚合口径（C19）是 unknown——assistant 记录上的 usage 一律不解析、不求和。
const (
	usageEvidence    = "docs/evidence/codebuddy/samples/VN301-S03-print-stream-json (C17/C18)"
	aggregationLimit = "docs/evidence/codebuddy/capability-matrix.json (C19)"
)

// UsageObservation 是从 result 记录解析出的一轮用量观测。
//
// 只有 C17 实测到的四个 token 字段会出现在这里；num_turns（C20）、
// total_cost_usd（C21）、modelUsage（C22）与 assistant 记录上的 usage（C19）
// 语义未验证，一律不解析——不解析就不会被下游误用。
type UsageObservation struct {
	// SourceNS 是来源命名空间；计量侧用它和 TurnID 一起做去重身份。
	SourceNS string `json:"source_ns"`
	// SessionID 与 RecordUUID 是来源侧身份（都是实测字段）。
	// 计量主键 turn_id 由 app 层用它们组合，本包不发明全局身份格式。
	SessionID  string `json:"session_id"`
	RecordUUID string `json:"record_uuid"`
	// Semantic 声明口径：当前版本只有 delta（见类型注释）。
	Semantic UsageSemantic `json:"semantic"`
	// Token 指针为 nil 表示来源没给；不得用 0 冒充（契约 T142）。
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	CacheReadTokens  *int64 `json:"cache_read_tokens"`
	CacheWriteTokens *int64 `json:"cache_write_tokens"`
	// TotalTokens = input + output；缓存字段不并入（C18 实测 input 已含缓存分解，
	// 相加会重复计算；契约 T140 要求 total 不含缓存字段）。
	TotalTokens *int64        `json:"total_tokens"`
	Quality     model.Quality `json:"quality"`
	// Evidence 指向实测样本与能力条目。
	Evidence string `json:"evidence"`
}

// usageSource 是 result 记录里本包解释的字段集合；其余字段（service_tier、
// server_tool_use、_meta 等）不解释，原始字节留在记录引用处。
type usageSource struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
	Usage     *struct {
		InputTokens   *int64   `json:"input_tokens"`
		OutputTokens  *int64   `json:"output_tokens"`
		CacheCreation *int64   `json:"cache_creation_input_tokens"`
		CacheRead     *int64   `json:"cache_read_input_tokens"`
		TotalCostUSD  *float64 `json:"total_cost_usd"`
		NumTurns      *int64   `json:"num_turns"`
	} `json:"usage"`
}

// ParseUsage 把一条 stream-json 记录解析成用量观测。
//
// 规则（全部有证据背书）：
//
//   - 只解析 result 记录（C11）；assistant 记录上的 usage 不解析——thinking 那条
//     为 0、text 那条为全量，聚合口径未验证（C19），求和就是伪造总量；
//   - usage 字段缺失 → (nil, nil)：该轮用量未观测，保留 unknown，不填 0；
//   - token 为非负整数，负值拒绝（契约 T140）；
//   - cache_creation_input_tokens → CacheWriteTokens，cache_read_input_tokens →
//     CacheReadTokens，都不并入 input（C18）；
//   - num_turns（C20）与 total_cost_usd（C21）不解析。
func ParseUsage(payload []byte) (*UsageObservation, error) {
	var rec usageSource
	if err := json.Unmarshal(payload, &rec); err != nil {
		return nil, model.PortFailuref(model.PortErrUnparseableRecord,
			"记录无法解析成 JSON 对象：%v；不推断内容", err)
	}
	if rec.Type != "result" {
		// C19：assistant 记录也带 usage，但聚合口径未验证——明确拒绝而不是顺手解析。
		return nil, model.PortFailuref(model.PortErrProtocolUnknown,
			"只从 result 记录解析用量，收到 %q：assistant 记录的 usage 聚合口径未验证（C19），不得解析",
			rec.Type)
	}
	if rec.Usage == nil {
		// usage 缺失 = 该轮用量未观测。返回 nil 让调用方保持 unknown，
		// 绝不返回全 0 的观测——0 是「实测为零」，不是「没看到」。
		return nil, nil
	}
	u := rec.Usage
	if err := checkNonNegative("input_tokens", u.InputTokens); err != nil {
		return nil, err
	}
	if err := checkNonNegative("output_tokens", u.OutputTokens); err != nil {
		return nil, err
	}
	if err := checkNonNegative("cache_creation_input_tokens", u.CacheCreation); err != nil {
		return nil, err
	}
	if err := checkNonNegative("cache_read_input_tokens", u.CacheRead); err != nil {
		return nil, err
	}
	obs := &UsageObservation{
		SourceNS:         "codebuddy",
		SessionID:        strings.TrimSpace(rec.SessionID),
		RecordUUID:       strings.TrimSpace(rec.UUID),
		Semantic:         UsageSemanticDelta,
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheRead,
		CacheWriteTokens: u.CacheCreation,
		Quality:          model.QualityObserved,
		Evidence:         usageEvidence + "; " + aggregationLimit,
	}
	if u.InputTokens != nil && u.OutputTokens != nil {
		total := *u.InputTokens + *u.OutputTokens
		obs.TotalTokens = &total
	}
	return obs, nil
}

func checkNonNegative(field string, v *int64) error {
	if v != nil && *v < 0 {
		return model.PortFailuref(model.PortErrInvalidSpec,
			"usage.%s 为负数（%d）：计量契约要求非负整数，拒绝而不是取绝对值", field, *v)
	}
	return nil
}

// SessionRef 是来源会话／轮次身份的最小视图，供 app 层做归属与关联
// （VN-804 的 EnsureSession／LinkRun 需要这些身份）。
type SessionRef struct {
	SessionID  string `json:"session_id"`
	RecordUUID string `json:"record_uuid"`
	RecordType string `json:"record_type"`
}

// ParseSessionRef 提取记录上的来源身份；init 与 result 都实测携带 session_id。
//
// 提取身份不等于允许恢复：恢复能力见 Recovery。
func ParseSessionRef(payload []byte) (*SessionRef, error) {
	var rec struct {
		Type      string `json:"type"`
		UUID      string `json:"uuid"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(payload, &rec); err != nil {
		return nil, model.PortFailuref(model.PortErrUnparseableRecord,
			"记录无法解析成 JSON 对象：%v", err)
	}
	if rec.SessionID == "" {
		// 没有来源会话身份就不发明：调用方保持未归属（unallocated）。
		return nil, nil
	}
	return &SessionRef{
		SessionID:  strings.TrimSpace(rec.SessionID),
		RecordUUID: strings.TrimSpace(rec.UUID),
		RecordType: rec.Type,
	}, nil
}

// RecoveryCapability 是会话恢复的能力声明（VN-308）。
//
// C24 是 unknown：`-r/--resume`、`-c/--continue` 未实测，恢复语义、会话是否存在、
// 跨进程可用性都没有证据。unknown 不是「不支持」的反义词——它是「不许用」：
// Launch 因此拒绝 ResumeSessionID（见 launch.go），这里把同一结论暴露给调用方，
// 让上层不需要重新推断。
type RecoveryCapability struct {
	// State 恒为 unknown（C24）；版本矩阵降级规则与 capabilities.go 一致。
	State model.CapabilityState `json:"state"`
	// Supported 恒为 false：unknown 的能力不得用于启动。
	Supported bool `json:"supported"`
	// ResumeFlags 是帮助清单里出现、但未实测的恢复参数（C24/C25）。
	ResumeFlags []string `json:"resume_flags"`
	Evidence    string   `json:"evidence"`
	Reason      string   `json:"reason"`
}

// Recovery 返回当前版本的恢复能力声明。
func Recovery() RecoveryCapability {
	return RecoveryCapability{
		State:       model.CapabilityUnknown,
		Supported:   false,
		ResumeFlags: []string{"-r", "--resume", "-c", "--continue", "--session-id"},
		Evidence:    EvidenceRoot + "/capability-matrix.json (C24/C25)",
		Reason: "恢复语义与会话存在性未验证（C24 unknown），--session-id 复用未观测（C25 unobserved）：" +
			"按未验证语义启动会把「新会话」当成「续跑」",
	}
}

// ResumeTarget 返回可用于启动的恢复目标。
//
// 它永远失败：本版本没有任何已验证的恢复目标。特别地，**最近会话不作为
// 恢复目标**——「最近用过」不等于「可以续跑」，传什么进来都拒绝，
// 不存在静默兜底路径。
func ResumeTarget(requestedSessionID string) (string, error) {
	_ = requestedSessionID
	cap := Recovery()
	return "", model.PortFailuref(model.PortErrCapabilityMissing,
		"没有已验证的恢复目标：%s；最近会话不得作为恢复目标，要恢复请先用真实样本验证 session.resume（C24）", cap.Reason)
}
