package events

import "github.com/kiwh77/maw/internal/engine/model"

// Reason 是「这条记录为什么没被接受／为什么只是重放」的局部原因码。
//
// 为什么不复用 model.Code：不变量登记表（E/P 段）是冻结契约，只登记契约正文里
// 出现过的规则，摄取层的原因码不属于其中任何一条；凭空加码等于私自扩契约。
// 但上层又要判断「能不能重试、是不是冲突」，所以每个原因码经 PortError 映射到
// 已登记的端口错误码，分类（retryable/conflict/terminal/unknown）由登记表给出，
// 不在这里另立一套。
type Reason string

// 摄取原因码。前十三条是拒收，key_conflict 记为冲突而非拒收。
const (
	// ReasonSourceUnbound 来源未绑定或接入器缺名，无法分域去重。
	ReasonSourceUnbound Reason = "source_unbound"
	// ReasonRecordRefMissing 缺少稳定日志位置，事后无法回溯原始字节。
	ReasonRecordRefMissing Reason = "record_ref_missing"
	// ReasonRecordTypeMissing 缺少来源记录类型，无法选择解释器。
	ReasonRecordTypeMissing Reason = "record_type_missing"
	// ReasonSchemaUnversioned 来源 schema 版本缺失或非正，解释规则不确定。
	ReasonSchemaUnversioned Reason = "schema_unversioned"
	// ReasonKindMissing 缺标准事件 kind。
	ReasonKindMissing Reason = "kind_missing"
	// ReasonKindMalformed kind 不是 命名空间.动作 的形态。
	ReasonKindMalformed Reason = "kind_malformed"
	// ReasonOccurredAtMissing 缺发生时间。不以接收时间冒充发生时间。
	ReasonOccurredAtMissing Reason = "occurred_at_missing"
	// ReasonRunGenerationUnpaired 给了 run_id 却没给 generation（或反之）。
	ReasonRunGenerationUnpaired Reason = "run_generation_unpaired"
	// ReasonQualityInvalid 声称的证据质量不在 observed/estimated/unknown 内。
	ReasonQualityInvalid Reason = "quality_invalid"
	// ReasonPhaseUnknown 运行阶段不在十个封闭值内。
	ReasonPhaseUnknown Reason = "phase_unknown"
	// ReasonTaskStatusUnknown 任务状态不在十一个封闭值内。
	ReasonTaskStatusUnknown Reason = "task_status_unknown"
	// ReasonAuthorityEscalation 该接入域无权写这类事实（旁路写控制事实、夺取所有权）。
	ReasonAuthorityEscalation Reason = "authority_escalation"
	// ReasonClaimAgentMismatch 声明的工作者与上报者不一致，等于替他人领取。
	ReasonClaimAgentMismatch Reason = "claim_agent_mismatch"
	// ReasonClaimUnbound 领取声明缺任务实例或实例身份非法。
	ReasonClaimUnbound Reason = "claim_unbound"
	// ReasonPayloadKindMismatch 载荷与 kind 错位（用任意 kind 夹带阶段或任务状态）。
	ReasonPayloadKindMismatch Reason = "payload_kind_mismatch"
	// ReasonCursorRegression 分配的游标不大于上一条，会破坏「游标只前进」。
	ReasonCursorRegression Reason = "cursor_regression"
	// ReasonKeyConflict 同来源同键但内容不同：不覆盖、不新建，交人判断。
	ReasonKeyConflict Reason = "key_conflict"
	// ReasonSeedIncomplete 重建索引时记录缺键或缺指纹，无法恢复幂等。
	ReasonSeedIncomplete Reason = "seed_incomplete"
)

// reasons 是登记表，Title 与 PortError 都从它取，避免两处各写一份。
var reasons = map[Reason]struct {
	title string
	port  model.PortErrorCode
}{
	ReasonSourceUnbound:         {"来源未绑定或接入器缺名", model.PortErrUnknownIdentity},
	ReasonRecordRefMissing:      {"缺少稳定日志位置", model.PortErrUnparseableRecord},
	ReasonRecordTypeMissing:     {"缺少来源记录类型", model.PortErrUnknownRecordType},
	ReasonSchemaUnversioned:     {"来源 schema 版本缺失", model.PortErrVersionUnsupported},
	ReasonKindMissing:           {"缺少标准事件 kind", model.PortErrInvalidSpec},
	ReasonKindMalformed:         {"kind 形态非法", model.PortErrInvalidSpec},
	ReasonOccurredAtMissing:     {"缺少发生时间", model.PortErrInvalidSpec},
	ReasonRunGenerationUnpaired: {"run_id 与 generation 未成对", model.PortErrInvalidSpec},
	ReasonQualityInvalid:        {"证据质量取值非法", model.PortErrInvalidSpec},
	ReasonPhaseUnknown:          {"运行阶段不在封闭集合内", model.PortErrInvalidSpec},
	ReasonTaskStatusUnknown:     {"任务状态不在封闭集合内", model.PortErrInvalidSpec},
	ReasonAuthorityEscalation:   {"该接入域无权写这类事实", model.PortErrInvalidSpec},
	ReasonClaimAgentMismatch:    {"声明者与上报者不一致", model.PortErrUnknownIdentity},
	ReasonClaimUnbound:          {"领取声明缺实例", model.PortErrInvalidSpec},
	ReasonPayloadKindMismatch:   {"载荷与 kind 错位", model.PortErrInvalidSpec},
	ReasonCursorRegression:      {"游标没有前进", model.PortErrConflict},
	ReasonKeyConflict:           {"同来源同键不同内容", model.PortErrConflict},
	ReasonSeedIncomplete:        {"索引重建记录缺键", model.PortErrUnparseableRecord},
}

// Title 返回中文说明；未登记返回空串。
func (r Reason) Title() string { return reasons[r].title }

// PortError 返回对应的已登记端口错误码；未登记按 unparseable_record 处理
// （保持现场，不把未知原因说成成功）。
func (r Reason) PortError() model.PortErrorCode {
	if e, ok := reasons[r]; ok {
		return e.port
	}
	return model.PortErrUnparseableRecord
}

// Class 返回登记表给出的错误分类，决定调用方能否重试。
func (r Reason) Class() model.Class { return r.PortError().ClassOf() }

// Error 把原因码做成可判定的错误，供上层用 errors.Is / model.AsError 分层处理。
func (r Reason) Error(msg string) *model.Error {
	if msg == "" {
		msg = r.Title()
	}
	return model.PortFailure(r.PortError(), msg)
}

// Known 报告该原因码是否已登记。
func (r Reason) Known() bool {
	_, ok := reasons[r]
	return ok
}

// AllReasons 返回全部已登记原因码，供注册表对齐测试使用。
func AllReasons() []Reason {
	out := make([]Reason, 0, len(reasons))
	for r := range reasons {
		out = append(out, r)
	}
	return out
}

// Attribution 是归属结论：事件本身到底挂在哪个任务／Run 上。
//
// 未归属不是错误：架构要求「关联不明确时保留未归属，不能强制猜测」（§4）。
type Attribution string

const (
	// Attributed 归属已验证，事件携带任务实例与运行。
	Attributed Attribution = "attributed"
	// Unattributed 归属不明或无权声明：来源的说法留在载荷，事件不带归属。
	Unattributed Attribution = "unattributed"
)

// Valid 报告归属取值是否合法。
func (a Attribution) Valid() bool { return a == Attributed || a == Unattributed }

// Disposition 是一条观测的处置结论。
type Disposition string

const (
	// Accepted 写成了一条新事件。
	Accepted Disposition = "accepted"
	// DispositionDuplicate 同键同内容的重放，未新建事件。
	DispositionDuplicate Disposition = "duplicate"
	// DispositionConflict 同键不同内容，两边都保留，等人工裁决。
	DispositionConflict Disposition = "conflict"
	// DispositionRejected 未通过校验，没有产生事件。
	DispositionRejected Disposition = "rejected"
)
