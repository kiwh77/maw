package model

import (
	"errors"
	"fmt"
)

// Code 是冻结契约「不变量登记表」中的错误码。
// 分段：E1xx 任务实例与版本、E2xx 运行与占用、E3xx 交付与验收、P1xx 端口与装配。
type Code string

// 任务实例与版本（maw.task.instance/v1）。
const (
	CodeTaskFieldInvalid        Code = "E100"
	CodeTaskContractUnsupported Code = "E101"
	CodeInstanceDuplicate       Code = "E110"
	CodeInstanceIDShape         Code = "E111"
	CodeDisplayIDShape          Code = "E112"
	CodeActiveAliasConflict     Code = "E120"
	CodeActiveVersionDangling   Code = "E130"
	CodeVersionSequence         Code = "E131"
	CodeActiveVersionRollback   Code = "E132"
	CodeVersionFrozenOrder      Code = "E133"
	CodeRetiredIncomplete       Code = "E140"
	CodeFrozenAfterRetire       Code = "E142"
	CodeDepMissing              Code = "E150"
	CodeDepRetired              Code = "E151"
	CodeDepSelf                 Code = "E152"
	CodeDepAmbiguous            Code = "E153"
	CodeLegacyAnchorMissing     Code = "E160"
	CodeLegacyAmbiguousActive   Code = "E161"
)

// 运行与占用（maw.runtime.state/v1）。
const (
	CodeRuntimeFieldInvalid     Code = "E200"
	CodeRuntimeContractUnknown  Code = "E201"
	CodeRunDuplicate            Code = "E210"
	CodeOperationDuplicate      Code = "E211"
	CodeEventDuplicate          Code = "E212"
	CodeCursorNotIncreasing     Code = "E213"
	CodeSchemaVersionMissing    Code = "E214"
	CodePhaseUnknown            Code = "E220"
	CodePhaseTransitionIllegal  Code = "E221"
	CodePhaseHistoryMismatch    Code = "E222"
	CodeTimeOrder               Code = "E223"
	CodeTerminalWithoutProof    Code = "E230"
	CodeCancelledUnconfirmed    Code = "E231"
	CodeUnknownTerminal         Code = "E232"
	CodeDoubleOccupancy         Code = "E240"
	CodeSlotReleasedBusy        Code = "E241"
	CodeWorkspaceNotFrozen      Code = "E242"
	CodeReleaseGeneration       Code = "E243"
	CodeTerminalRewritten       Code = "E250"
	CodeCancelNotReported       Code = "E251"
	CodeIdempotencyConflict     Code = "E252"
	CodePhaseEventMissing       Code = "E260"
	CodeReleaseEventMissing     Code = "E261"
	CodeClockSkewMarkedObserved Code = "E262"
	CodeEventUnknownRun         Code = "E263"
	CodeAutoExecutionLimit      Code = "E270"
)

// 交付与验收（maw.delivery.manifest/v1）。
const (
	CodeDeliveryFieldInvalid    Code = "E300"
	CodeDeliveryContractUnknown Code = "E301"
	CodeDeliveryTaskMissing     Code = "E310"
	CodeDeliveryVersionMissing  Code = "E311"
	CodeDeliveryRunMissing      Code = "E312"
	CodeCommitRefInvalid        Code = "E313"
	CodeArtifactMissing         Code = "E320"
	CodeArtifactOutOfScope      Code = "E321"
	CodeVerificationVersion     Code = "E330"
	CodeVerificationStaleCommit Code = "E331"
	CodeVerificationNoEvidence  Code = "E332"
	CodeVerificationExitOnly    Code = "E333"
	CodeVerificationArtifact    Code = "E334"
	CodeDoneWithoutPass         Code = "E340"
	CodeDoneWithoutArtifact     Code = "E341"
	CodeDoneWithoutMerge        Code = "E342"
	CodeDoneWithoutApproval     Code = "E343"
	CodeNonCodeWithoutAccept    Code = "E350"
	CodeCompletionModeMismatch  Code = "E351"
	CodeRejectedWithoutReason   Code = "E360"
	CodeDoneWithFailedCheck     Code = "E361"
	CodeSupersededStillDone     Code = "E370"
	CodeSupersedeInvalid        Code = "E371"
)

// 端口与装配（maw.app.ports/v1）。
const (
	CodePortFieldInvalid      Code = "P100"
	CodePortContractUnknown   Code = "P101"
	CodeLayerUnknown          Code = "P110"
	CodePortUnknown           Code = "P111"
	CodePortDuplicateBinding  Code = "P112"
	CodePortUnbound           Code = "P113"
	CodeAdapterForbiddenDep   Code = "P120"
	CodePortDependsOnImpl     Code = "P121"
	CodeImportUndeclared      Code = "P122"
	CodeDependencyCycle       Code = "P123"
	CodeAdapterWritesStatus   Code = "P130"
	CodeCallWithoutErrors     Code = "P131"
	CodeEffectInTx            Code = "P132"
	CodeStatusOutsideTx       Code = "P133"
	CodeClaimWithoutGuards    Code = "P134"
	CodeCapabilityUnversioned Code = "P140"
	CodeExitCodeAsSuccess     Code = "P141"
	CodeUnknownAsSuccess      Code = "P142"
)

var codeTitles = map[Code]string{
	CodeTaskFieldInvalid:        "必填字段缺失或类型不符",
	CodeTaskContractUnsupported: "契约标识不受支持",
	CodeInstanceDuplicate:       "instance_id 重复",
	CodeInstanceIDShape:         "instance_id 命名空间非法",
	CodeDisplayIDShape:          "display_id 格式非法",
	CodeActiveAliasConflict:     "同一 display_id 存在多个活跃实例",
	CodeActiveVersionDangling:   "active_version 未指向已冻结版本",
	CodeVersionSequence:         "版本号未从 1 连续递增",
	CodeActiveVersionRollback:   "active_version 与最大版本不一致",
	CodeVersionFrozenOrder:      "版本冻结时间未严格递增",
	CodeRetiredIncomplete:       "退役实例缺退役时间/原因或仍持活跃指针",
	CodeFrozenAfterRetire:       "退役之后又冻结了新版本",
	CodeDepMissing:              "依赖目标不存在",
	CodeDepRetired:              "依赖已退役实例",
	CodeDepSelf:                 "依赖自身",
	CodeDepAmbiguous:            "legacy 依赖命中多个实例",
	CodeLegacyAnchorMissing:     "legacy 锚点缺失或主键错位",
	CodeLegacyAmbiguousActive:   "歧义历史充当活跃实例",

	CodeRuntimeFieldInvalid:     "必填字段缺失或类型不符",
	CodeRuntimeContractUnknown:  "契约标识不受支持",
	CodeRunDuplicate:            "run_id 重复",
	CodeOperationDuplicate:      "operation_id 重复",
	CodeEventDuplicate:          "event_id 重复",
	CodeCursorNotIncreasing:     "事件游标非严格递增",
	CodeSchemaVersionMissing:    "事件缺少 schema_version",
	CodePhaseUnknown:            "未知运行阶段",
	CodePhaseTransitionIllegal:  "非法阶段转移",
	CodePhaseHistoryMismatch:    "阶段历史末项与当前阶段不一致",
	CodeTimeOrder:               "阶段时间倒序或结束早于开始",
	CodeTerminalWithoutProof:    "终态缺少证据",
	CodeCancelledUnconfirmed:    "cancelled 却未确认进程已停止",
	CodeUnknownTerminal:         "核实未知时落了终态",
	CodeDoubleOccupancy:         "同一任务实例存在多个有效占用",
	CodeSlotReleasedBusy:        "运行仍占用时释放节点槽",
	CodeWorkspaceNotFrozen:      "产物未冻结就释放工作目录",
	CodeReleaseGeneration:       "释放代际与持有代际不一致",
	CodeTerminalRewritten:       "终态之后被追改为 cancelled",
	CodeCancelNotReported:       "取消已结束运行未返回 already_finished",
	CodeIdempotencyConflict:     "同幂等键不同请求摘要",
	CodePhaseEventMissing:       "当前阶段缺少同批 run.phase 事件",
	CodeReleaseEventMissing:     "释放缺少 lease.released 事件",
	CodeClockSkewMarkedObserved: "发生晚于接收却标 observed",
	CodeEventUnknownRun:         "事件引用不存在的运行",
	CodeAutoExecutionLimit:      "自动执行开启但未配置上限",

	CodeDeliveryFieldInvalid:    "必填字段缺失或类型不符",
	CodeDeliveryContractUnknown: "契约标识不受支持",
	CodeDeliveryTaskMissing:     "交付未绑定任务实例",
	CodeDeliveryVersionMissing:  "交付未绑定任务版本",
	CodeDeliveryRunMissing:      "交付未绑定运行",
	CodeCommitRefInvalid:        "提交引用格式非法",
	CodeArtifactMissing:         "产物声明了但查不到实体",
	CodeArtifactOutOfScope:      "产物越出写盘范围",
	CodeVerificationVersion:     "验证目标版本与交付版本不一致",
	CodeVerificationStaleCommit: "验证针对旧提交",
	CodeVerificationNoEvidence:  "判定通过却没有证据",
	CodeVerificationExitOnly:    "只有退出码不算通过",
	CodeVerificationArtifact:    "产物已变，旧验证失效",
	CodeDoneWithoutPass:         "没有有效通过验证就 done",
	CodeDoneWithoutArtifact:     "没有产物就 done",
	CodeDoneWithoutMerge:        "代码任务未合入就 done",
	CodeDoneWithoutApproval:     "代码任务评审未通过就 done",
	CodeNonCodeWithoutAccept:    "非代码任务缺验收结论",
	CodeCompletionModeMismatch:  "完成方式与任务模式不匹配",
	CodeRejectedWithoutReason:   "rejected 未写明原因",
	CodeDoneWithFailedCheck:     "done 仍存在失败验证",
	CodeSupersededStillDone:     "被取代的交付仍保留 done",
	CodeSupersedeInvalid:        "supersedes 指向不存在或自身",

	CodePortFieldInvalid:      "必填字段缺失或类型不符",
	CodePortContractUnknown:   "契约标识不受支持",
	CodeLayerUnknown:          "模块层不在 app/port/adapter/infra 内",
	CodePortUnknown:           "绑定了未登记的端口",
	CodePortDuplicateBinding:  "同一端口重复绑定",
	CodePortUnbound:           "端口声明了却没有绑定实现",
	CodeAdapterForbiddenDep:   "适配器反向依赖业务或基础设施",
	CodePortDependsOnImpl:     "端口层依赖了实现",
	CodeImportUndeclared:      "import 了未声明的模块",
	CodeDependencyCycle:       "模块依赖成环",
	CodeAdapterWritesStatus:   "接入器直接写业务任务状态",
	CodeCallWithoutErrors:     "调用未声明可能错误",
	CodeEffectInTx:            "事务内出现外部副作用",
	CodeStatusOutsideTx:       "事务外写业务状态",
	CodeClaimWithoutGuards:    "领取缺幂等键或冲突检查",
	CodeCapabilityUnversioned: "能力未绑定版本或证据",
	CodeExitCodeAsSuccess:     "把退出码 0 当作成功",
	CodeUnknownAsSuccess:      "未知协议结果被标成功",
}

// Title 返回错误码的中文标题（与契约登记表同名行一致）；未知码返回空串。
func (c Code) Title() string { return codeTitles[c] }

// Known 报告该码是否在冻结登记表中。
func (c Code) Known() bool {
	_, ok := codeTitles[c]
	return ok
}

// AllCodes 返回冻结登记表中的全部错误码，供注册表对齐测试使用。
func AllCodes() []Code {
	out := make([]Code, 0, len(codeTitles))
	for c := range codeTitles {
		out = append(out, c)
	}
	return out
}

// Class 是契约 §4 的错误分类，决定调用方是否能重试。
type Class string

const (
	// ClassRetryable 可重试，但必须记录尝试次数。
	ClassRetryable Class = "retryable"
	// ClassConflict 版本／代际／资源冲突，不重试，重新读事实再决定。
	ClassConflict Class = "conflict"
	// ClassTerminal 明确失败，不重试，向用户说明原因。
	ClassTerminal Class = "terminal"
	// ClassUnknown 证据不足：保留占用并标注，不得降级为成功或失败。
	ClassUnknown Class = "unknown"
)

// Valid 报告分类是否在四类之内。
func (c Class) Valid() bool {
	switch c {
	case ClassRetryable, ClassConflict, ClassTerminal, ClassUnknown:
		return true
	}
	return false
}

// PortErrorCode 是端口调用的结果错误（app-ports-contract-v1 §2 各端口「错误」行）。
// 它和不变量码是两个命名空间：前者描述一次调用为什么没成，后者描述一份数据违反了哪条规则。
type PortErrorCode string

const (
	PortErrNoReady             PortErrorCode = "no_ready"
	PortErrConflict            PortErrorCode = "conflict"
	PortErrRevisionMismatch    PortErrorCode = "revision_mismatch"
	PortErrCapacityExhausted   PortErrorCode = "capacity_exhausted"
	PortErrIdempotentReplay    PortErrorCode = "idempotent_replay"
	PortErrBusy                PortErrorCode = "busy"
	PortErrInvalidTransition   PortErrorCode = "invalid_transition"
	PortErrUnknownIdentity     PortErrorCode = "unknown_identity"
	PortErrEntryMissing        PortErrorCode = "entry_missing"
	PortErrVersionUnparsed     PortErrorCode = "version_unparsed"
	PortErrProbeTimeout        PortErrorCode = "probe_timeout"
	PortErrPlatformUnsupported PortErrorCode = "platform_unsupported"
	PortErrInvalidSpec         PortErrorCode = "invalid_spec"
	PortErrCapabilityMissing   PortErrorCode = "capability_unsupported"
	PortErrEntryUnavailable    PortErrorCode = "entry_unavailable"
	PortErrUnparseableRecord   PortErrorCode = "unparseable_record"
	PortErrUnknownRecordType   PortErrorCode = "unknown_record_type"
	PortErrVersionUnsupported  PortErrorCode = "version_unsupported"
	PortErrMissingResult       PortErrorCode = "missing_result_record"
	PortErrAuthRequired        PortErrorCode = "auth_required"
	PortErrApplication         PortErrorCode = "application_error"
	PortErrProtocolUnknown     PortErrorCode = "protocol_unknown"
	PortErrNotStarted          PortErrorCode = "not_started"
	PortErrAlreadyStopped      PortErrorCode = "already_stopped"
	PortErrStopUnconfirmed     PortErrorCode = "stop_unconfirmed"
	// PortErrGenerationMismatch 对应契约 §4 conflict 行的 generation_mismatch。
	PortErrGenerationMismatch PortErrorCode = "generation_mismatch"
)

// portErrorClass 把每个端口错误归入契约 §4 的四类之一。
//
// 分类依据：§4 举例的（busy、probe_timeout → retryable；conflict、revision_mismatch、
// generation_mismatch → conflict；no_ready、invalid_spec、capability_unsupported、
// platform_unsupported → terminal；protocol_unknown、stop_unconfirmed、version_unsupported
// → unknown）逐一照抄；其余按同一判据推导——「重做一次可能变好」retryable，
// 「事实变了要先重读」conflict，「确定不可做」terminal，「证据不足」unknown。
var portErrorClass = map[PortErrorCode]Class{
	PortErrBusy:                ClassRetryable,
	PortErrProbeTimeout:        ClassRetryable,
	PortErrConflict:            ClassConflict,
	PortErrRevisionMismatch:    ClassConflict,
	PortErrGenerationMismatch:  ClassConflict,
	PortErrIdempotentReplay:    ClassConflict,
	PortErrNoReady:             ClassTerminal,
	PortErrInvalidSpec:         ClassTerminal,
	PortErrCapabilityMissing:   ClassTerminal,
	PortErrPlatformUnsupported: ClassTerminal,
	PortErrEntryMissing:        ClassTerminal,
	PortErrEntryUnavailable:    ClassTerminal,
	PortErrInvalidTransition:   ClassTerminal,
	PortErrUnknownIdentity:     ClassTerminal,
	PortErrAuthRequired:        ClassTerminal,
	PortErrNotStarted:          ClassTerminal,
	PortErrAlreadyStopped:      ClassTerminal,
	PortErrCapacityExhausted:   ClassConflict,
	PortErrProtocolUnknown:     ClassUnknown,
	PortErrStopUnconfirmed:     ClassUnknown,
	PortErrVersionUnsupported:  ClassUnknown,
	PortErrVersionUnparsed:     ClassUnknown,
	PortErrUnknownRecordType:   ClassUnknown,
	PortErrUnparseableRecord:   ClassUnknown,
	PortErrMissingResult:       ClassUnknown,
	PortErrApplication:         ClassUnknown,
}

// ClassOf 返回端口错误的分类；未登记的码按 unknown 处理（保持现场，不降级为成功）。
func (p PortErrorCode) ClassOf() Class {
	if c, ok := portErrorClass[p]; ok {
		return c
	}
	return ClassUnknown
}

// Known 报告该端口错误码是否已登记分类。
func (p PortErrorCode) Known() bool {
	_, ok := portErrorClass[p]
	return ok
}

// AllPortErrorCodes 返回已登记的端口错误码。
func AllPortErrorCodes() []PortErrorCode {
	out := make([]PortErrorCode, 0, len(portErrorClass))
	for c := range portErrorClass {
		out = append(out, c)
	}
	return out
}

// Error 是类型化错误。不变量码与端口错误码二选一，二者都不设时视为未知错误。
type Error struct {
	Code  Code
	Port  PortErrorCode
	Class Class
	Path  string
	Msg   string
	Err   error
}

func (e *Error) Error() string {
	id := string(e.Code)
	if id == "" {
		id = string(e.Port)
	}
	msg := e.Msg
	if msg == "" {
		msg = e.Code.Title()
	}
	switch {
	case e.Path != "" && id != "":
		return fmt.Sprintf("%s %s: %s", id, e.Path, msg)
	case id != "":
		return fmt.Sprintf("%s: %s", id, msg)
	case e.Path != "":
		return fmt.Sprintf("%s: %s", e.Path, msg)
	default:
		return msg
	}
}

func (e *Error) Unwrap() error { return e.Err }

// Is 让 errors.Is(err, &Error{Code: E111}) 按码匹配，忽略路径与文案。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || t == nil {
		return false
	}
	if e.Code != "" || t.Code != "" {
		return e.Code == t.Code
	}
	return e.Port == t.Port
}

// Violation 构造一条不变量违反。
func Violation(code Code, path, msg string) *Error {
	class := ClassTerminal
	switch code {
	case CodeCursorNotIncreasing, CodeIdempotencyConflict:
		class = ClassConflict
	}
	return &Error{Code: code, Class: class, Path: path, Msg: msg}
}

// Violationf 同 Violation，带格式化文案。
func Violationf(code Code, path, format string, args ...any) *Error {
	return Violation(code, path, fmt.Sprintf(format, args...))
}

// PortFailure 构造一条端口调用失败，分类取自登记表。
func PortFailure(code PortErrorCode, msg string) *Error {
	if msg == "" {
		msg = string(code)
	}
	return &Error{Port: code, Class: code.ClassOf(), Msg: msg}
}

// PortFailuref 同 PortFailure，带格式化文案。
func PortFailuref(code PortErrorCode, format string, args ...any) *Error {
	return PortFailure(code, fmt.Sprintf(format, args...))
}

// Err 把一组违反压成一个 error；为空返回 nil。
// 保留第一条的码，便于上层 errors.Is 快速判定，同时文案列出全部条目。
func Err(vs []*Error) error {
	if len(vs) == 0 {
		return nil
	}
	if len(vs) == 1 {
		return vs[0]
	}
	rest := make([]string, 0, len(vs)-1)
	for _, v := range vs[1:] {
		rest = append(rest, v.Error())
	}
	return fmt.Errorf("%w（另 %d 条：%v）", vs[0], len(vs)-1, rest)
}

// HasCode 报告违反集合中是否包含指定不变量码。
func HasCode(vs []*Error, code Code) bool {
	for _, v := range vs {
		if v != nil && v.Code == code {
			return true
		}
	}
	return false
}

// HasPortError 报告违反集合中是否包含指定端口错误码。
func HasPortError(vs []*Error, code PortErrorCode) bool {
	for _, v := range vs {
		if v != nil && v.Port == code {
			return true
		}
	}
	return false
}

// AsError 取回类型化错误（包装层也能穿透）。
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
