package workbuddy

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

// 本包产出的三个观测类 kind（与家族协议同名，语义按本包证据解释）。
//
// 都不是控制类事实（run.phase／task.status／lease.*／claim.*）：接入器只能说
// 「来源报告了一次回合开始／一条消息／一次回合结束」，不能说「任务已完成」。
const (
	// KindTurnStarted 对应 `system/init`：会话开始。
	KindTurnStarted = "turn.started"
	// KindTurnMessage 对应 `assistant`：模型回合里的一条消息。
	KindTurnMessage = "turn.message"
	// KindTurnFinished 对应 `result`：最终结果记录出现（≠ 成功）。
	KindTurnFinished = "turn.finished"
)

// EventSchemaVersion 是标准事件的 schema 版本（正整数，E214）。
const EventSchemaVersion = 1

// authRequiredPrefix 是未登录的唯一稳定信号（W11）。
//
// 注意它与 CodeBuddy 的路径不同：本包两款的认证缺失出现在 **stderr**，
// 不是 result.errors。所以这条前缀既用于 Decode 的记录错误文本，也用于
// Interpret 的 ApplicationError——两条路都要认，缺一条就会把「需登录」当应用错误。
const authRequiredPrefix = "Authentication required"

// sourceRecord 是一条 json 结果记录里**本适配器解释**的字段集合。
//
// 不在这里的字段一律不解释（cli_version、usage、modelUsage 之类）：
// 不解析就不会误用，原始字节留在 DecodeRequest.RecordRef 指向的位置。
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
// 这里是本卡最关键的一道闸：**记录形状未观测就不解释**。
// 两款 WorkBuddy 从未产出过一条成功结果（认证先行失败），所以单条 json 结果的
// 记录类型与字段语义都没有样本；此时把 `{"type":"result"}` 映射成 turn.finished
// 属于「按未验证语义造事实」。于是：
//   - 记录无法解析成 JSON 对象 → unparseable_record（留 RawRef）；
//   - 形状未观测（W09 unobserved）→ 零事件 + unknown_record_type，只留证据；
//   - 形状已观测（证据补上之后）→ 按下面 recordKind 的封闭映射产出事件。
//
// 三条纪律与家族协议一致：不产出控制类事件、不伪造执行区间、事件身份由内容确定性推导。
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

	m, err := a.measurement()
	if err != nil {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown}, err
	}
	if state := m.caps[capResultRecord]; state != model.CapabilityObserved {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown},
			model.PortFailuref(model.PortErrUnknownRecordType,
				"记录 %s 的类型 %q 在 %s %s（随包 CLI %s）上未观测：%s 的能力状态是 %s，单条 json 结果的形状没有样本，"+
					"只保留原始引用、不产生事件；补到样本后把矩阵里该行改成 observed 才会开始解释",
				req.RecordRef, rec.Type, m.dist.AppName, m.dist.AppVersion, m.version, capResultRecord, state)
	}

	kind, known := recordKind(rec)
	if !known {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown},
			model.PortFailuref(model.PortErrUnknownRecordType,
				"未识别的记录类型 %q（subtype %q，位置 %s）：只保留证据，不产生事件、不映射成状态迁移",
				rec.Type, rec.Subtype, req.RecordRef)
	}
	if kind == "" {
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
		// 发生晚于接收说明来源时钟不可信，降级为 unknown 而不是「校正」。
		quality = model.QualityUnknown
	}

	ev := model.Event{
		EventID:       recordEventID(m.dist, rec, req.Payload),
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

// checkProtocol 只接受本包声明的输出形态：单条 json 结果的版本 1。
//
// 不接受 stream-json：那需要记录序列与增量语义，而本包一条 stream-json 记录都没采到
// （W06／W07 均为 unobserved）。
func checkProtocol(p model.Protocol) error {
	if strings.TrimSpace(p.Format) != OutputFormat {
		return model.PortFailuref(model.PortErrVersionUnsupported,
			"本包只接受 %q 单条结果，收到格式 %q；%q 的记录序列语义在两款随包 CLI 上都未观测",
			OutputFormat, p.Format, "stream-json")
	}
	if strings.TrimSpace(p.Version) != ProtocolVersion {
		return model.PortFailuref(model.PortErrVersionUnsupported,
			"记录形状版本 %q 未观测：只认版本 %s；需要采到样本后在本文件里加版本号，不能沿用旧号「大概兼容」",
			p.Version, ProtocolVersion)
	}
	return nil
}

// recordKind 把来源记录类型映射成标准事件 kind。
//
// 返回值：kind 为空且 known 为真 = 已知但不产生事件；known 为假 = 未识别类型。
//
// 映射是封闭的：json 模式（`--output-format json`，帮助清单写的是 "single result"）
// 只有一条最终结果记录，所以只认 `result`。system／assistant／stream_event 属于
// stream-json 的形态，出现在本形态里说明来源换了一套输出，按未识别处理而不是硬解释。
func recordKind(rec sourceRecord) (kind string, known bool) {
	switch rec.Type {
	case "result":
		return KindTurnFinished, true
	default:
		return "", false
	}
}

// parseTimestamp 解析来源时间戳（家族形态形如 2026-10-23T03:05:05.523Z）。
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
// 重放同一条记录必须得到同一个 event_id，store 才能按 E212 判重放；
// 两条不同记录绝不能撞同一个身份，否则第二条会被当成重放静默丢掉。
// 命名空间里带发行包种类：两包的记录即便内容相同，也是两个来源的事实。
func recordEventID(dist Distribution, rec sourceRecord, payload []byte) model.EventID {
	return model.EventID("evt_" + hashKey("workbuddy/" + string(dist.Kind) + "/" + OutputFormat + "/" + ProtocolVersion + "|" + stableRecordKey(rec, payload))[:20])
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
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
