package codebuddy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// 本包产出的三个观测类 kind。
//
// 都不是控制类事实（run.phase／task.status／lease.*／claim.*）：接入器只能说
// 「来源报告了一次回合开始／一条消息／一次回合结束」，不能说「任务已完成」——
// 业务状态只能由 app 层在事务里写（P130、P133）。
const (
	// KindTurnStarted 对应 `system/init`：会话开始，附带 session_id 等来源自述字段。
	KindTurnStarted = "turn.started"
	// KindTurnMessage 对应 `assistant`：模型回合里的一条消息。
	// 同一回合会有 thinking 与 text 两条（C10），所以它是两条事件而不是一条。
	KindTurnMessage = "turn.message"
	// KindTurnFinished 对应 `result`：最终结果记录出现。
	// 记录出现不等于成功——成不成功由 Interpret 按 result.subtype 判（C12）。
	KindTurnFinished = "turn.finished"
)

// EventSchemaVersion 是标准事件的 schema 版本（正整数，E214）。
const EventSchemaVersion = 1

// authRequiredPrefix 是未登录的唯一稳定信号（C14）：
// 没有机器可判的错误码，只有这句英文文本，且必须按前缀匹配——
// 全等匹配会在文案微调后静默失效，把认证失败当成应用错误。
const authRequiredPrefix = "Authentication required"

// sourceRecord 是 stream-json 一条记录里**本适配器解释**的字段集合。
//
// 不在这里的字段一律不解释：`cli_version`、`future_usage_field`、`modelUsage`
// 这类字段（含未来新增字段）不参与判定，原始字节留在 DecodeRequest.RecordRef 指向的位置。
// 「未知字段只保留证据」的落点就是这里——不解析就不会误用。
type sourceRecord struct {
	Type      string   `json:"type"`
	Subtype   string   `json:"subtype"`
	UUID      string   `json:"uuid"`
	MessageID string   `json:"_messageId"`
	Timestamp string   `json:"__timestamp"`
	SessionID string   `json:"session_id"`
	IsError   *bool    `json:"is_error"`
	Result    string   `json:"result"`
	Errors    []string `json:"errors"`
}

// Decode 实现 ports.Decode：把一条来源记录翻译成零到一条标准事件。
//
// 三种结果，都有确定语义：
//   - 可映射（system/init、assistant、result）→ 一条事件；
//   - 已知但不承载运行事实（stream_event 的 SSE 透传、system/status 快照）→ 零事件、无错误；
//   - 无法解析或未识别类型 → 零事件 + 错误，原始引用保留在 RawRef（X01、X04）。
//
// 未识别记录**不产生事件**：E263 只允许引用已存在的身份，凭空造一条事件等于伪造时间轴。
func (a *Adapter) Decode(ctx context.Context, req ports.DecodeRequest) (ports.DecodeResult, error) {
	if vs := req.Validate(); len(vs) > 0 {
		return ports.DecodeResult{}, model.PortFailuref(model.PortErrInvalidSpec,
			"解码请求不合法：%v", model.Err(vs))
	}
	if err := checkProtocol(req.Protocol); err != nil {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown}, err
	}

	var rec sourceRecord
	if err := json.Unmarshal(req.Payload, &rec); err != nil {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown},
			model.PortFailuref(model.PortErrUnparseableRecord,
				"记录 %s 无法解析成 JSON 对象：%v；原始字节保留在该位置，不推断内容也不据此判完成",
				req.RecordRef, err)
	}

	kind, known := recordKind(rec)
	if !known {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown},
			model.PortFailuref(model.PortErrUnknownRecordType,
				"未识别的记录类型 %q（subtype %q，位置 %s）：只保留证据，不产生事件、不映射成状态迁移",
				rec.Type, rec.Subtype, req.RecordRef)
	}
	if kind == "" {
		// 已知但不承载运行事实：SSE 透传与状态快照不是运行阶段，映射它们就是造事实。
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityObserved}, nil
	}

	occurred, hasOccurred := parseTimestamp(rec.Timestamp)
	received := a.now().UTC()
	quality := model.QualityObserved
	switch {
	case !hasOccurred:
		// 来源没给发生时间：只能取接收时刻，并明确降级，不冒充「发生时间」。
		occurred = received
		quality = model.QualityEstimated
	case occurred.After(received):
		// 发生晚于接收说明来源时钟不可信（E262），降级为 unknown 而不是「校正」。
		quality = model.QualityUnknown
	}

	ev := model.Event{
		EventID:       recordEventID(rec, req.Payload),
		Cursor:        req.Cursor,
		SchemaVersion: EventSchemaVersion,
		Kind:          kind,
		OccurredAt:    occurred,
		ReceivedAt:    received,
		Quality:       quality,
	}
	return ports.DecodeResult{
		Events:  []model.Event{ev},
		RawRef:  req.RecordRef,
		Quality: quality,
	}, nil
}

// checkProtocol 只接受本包实测过的协议：stream-json 的单条记录，版本 1。
//
// 「大概是兼容的」不算兼容：未知版本一律 version_unsupported（契约 §4 的 unknown 类），
// 保持现场等人工确认，不按最新版猜字段。
func checkProtocol(p model.Protocol) error {
	if strings.TrimSpace(p.Format) != "stream-json" {
		return model.PortFailuref(model.PortErrVersionUnsupported,
			"只支持 stream-json 单条记录，收到格式 %q；json 模式的数组结构是另一种协议，不按同一套规则解释", p.Format)
	}
	if strings.TrimSpace(p.Version) != ProtocolVersion {
		return model.PortFailuref(model.PortErrVersionUnsupported,
			"记录形状版本 %q 未观测：只认版本 %s（CLI %s 实测），需要重新采集证据再加版本号",
			p.Version, ProtocolVersion, MeasuredCLIVersion)
	}
	return nil
}

// recordKind 把来源记录类型映射成标准事件 kind。
//
// 返回值：kind 为空且 known 为真 = 已知但不产生事件；known 为假 = 未识别类型。
func recordKind(rec sourceRecord) (kind string, known bool) {
	switch rec.Type {
	case "system":
		switch rec.Subtype {
		case "init":
			return KindTurnStarted, true
		case "status":
			// 状态快照的 status 字段实测为 null，语义未验证；不映射。
			return "", true
		}
		// 例如 X04 的 system/hook：类型已知但子类型未观测，按未识别处理。
		return "", false
	case "stream_event":
		// SSE 原样透传（C09）：是 delta，不是归一化事件。
		return "", true
	case "assistant":
		return KindTurnMessage, true
	case "result":
		return KindTurnFinished, true
	default:
		return "", false
	}
}

// parseTimestamp 解析来源的 `__timestamp`（实测形如 2026-10-23T03:05:05.523Z）。
func parseTimestamp(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// recordEventID 由稳定记录键确定性地推导事件身份。
//
// 重放同一条记录必须得到同一个 event_id，store 才能按 E212 判重放而不是记两条；
// 反过来，两条不同记录绝不能撞同一个身份，否则第二条会被当成重放**静默丢掉**。
//
// 键的组成是「来源身份 + 内容指纹」，两条都要：
//   - 只用 uuid 会被证据里的脱敏样本直接打穿（VN-301 全部 uuid 都替换成 `<UUID>`），
//     而且真实来源也可能给同一条逻辑消息的多个记录复用同一个 id；
//   - 只用内容哈希则无法区分「同一内容的两条合法记录」（例如两次相同的 assistant 增量）。
//
// 字段顺序固定、不随 map 迭代变化，因此同一条字节重放必然同值。
func recordEventID(rec sourceRecord, payload []byte) model.EventID {
	return model.EventID("evt_" + hashKey(stableRecordKey(rec, payload))[:20])
}

func stableRecordKey(rec sourceRecord, payload []byte) string {
	identity := strings.TrimSpace(rec.UUID)
	if identity == "" {
		identity = strings.TrimSpace(rec.MessageID)
	}
	if identity == "" {
		identity = "-"
	}
	return identity + "|" + rec.Type + "|" + rec.Subtype + "|" + hashKey(string(payload))
}

func hashKey(s string) string {
	sum := sha256.Sum256([]byte("codebuddy/stream-json/" + ProtocolVersion + "|" + s))
	return hex.EncodeToString(sum[:])
}
