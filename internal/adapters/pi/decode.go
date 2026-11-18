package pi

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

// 本包产出的观测类 kind。都不是控制类事实：接入器只说「来源报告了什么」，
// 不能说「任务已完成」——业务状态只能由 app 层在事务里写。
const (
	// KindTurnStarted 对应 session_started：会话开始。
	KindTurnStarted = "turn.started"
	// KindTurnFinished 对应 result：最终结果记录出现（不等于成功，由 Interpret 判）。
	KindTurnFinished = "turn.finished"
)

// EventSchemaVersion 是标准事件的 schema 版本（正整数）。
const EventSchemaVersion = 1

// contractID 是本适配器认识的记录契约（与 docs 的契约标识一致）。
const contractID = "maw.pi.execution/v1"

// envelope 是运行时输出的一行事件（契约 §5.2）。
type envelope struct {
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

// recordKind 把运行时事件类型映射成标准事件 kind。
//
// 返回 (kind, known, carriesFact)：
//   - carriesFact=false 表示已知但不承载运行事实（usage／工具／版本自述），
//     它们进 journal 或观测端口，不能造时间轴；
//   - known=false 表示未识别类型，只保留证据。
func recordKind(typ string) (kind string, known, carriesFact bool) {
	switch strings.TrimSpace(typ) {
	case "session_started":
		return KindTurnStarted, true, true
	case "result":
		return KindTurnFinished, true, true
	case "version_report", "usage", "tool_call", "tool_result", "tool_denied",
		"stage_hint", "session_ended", "error":
		// error 不单独成事件：失败由 result 承载，错误详情留在 journal 与 Interpret 证据里。
		return "", true, false
	default:
		return "", false, false
	}
}

// Decode 实现 ports.Decode：一行 NDJSON → 一条标准事件。
func (a *Adapter) Decode(ctx context.Context, req ports.DecodeRequest) (ports.DecodeResult, error) {
	if vs := req.Validate(); len(vs) > 0 {
		return ports.DecodeResult{}, model.PortFailuref(model.PortErrInvalidSpec,
			"解码请求不合法：%v", model.Err(vs))
	}
	if err := checkProtocol(req.Protocol); err != nil {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown}, err
	}
	if err := ctx.Err(); err != nil {
		return ports.DecodeResult{}, err
	}

	var env envelope
	if err := json.Unmarshal(req.Payload, &env); err != nil {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown},
			model.PortFailuref(model.PortErrUnparseableRecord,
				"记录 %s 不是单行 JSON：%v；原始字节保留在该位置，不推断内容", req.RecordRef, err)
	}
	if strings.TrimSpace(env.Contract) != contractID {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown},
			model.PortFailuref(model.PortErrVersionUnsupported,
				"事件契约 %q 不是 %s", env.Contract, contractID)
	}
	kind, known, carriesFact := recordKind(env.Type)
	if !known {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown},
			model.PortFailuref(model.PortErrUnknownRecordType,
				"未识别的事件类型 %q（位置 %s）：只保留证据，不产生事件、不映射成状态迁移",
				env.Type, req.RecordRef)
	}
	if !carriesFact {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityObserved}, nil
	}

	received := a.now()
	occurred, hasOccurred := parseTime(env.OccurredAt)
	quality := model.QualityObserved
	switch {
	case !hasOccurred:
		occurred = received
		quality = model.QualityEstimated
	case occurred.After(received):
		// 发生晚于接收说明来源时钟不可信；降级为 unknown，而不是「校正」。
		quality = model.QualityUnknown
	}

	ev := model.Event{
		EventID:       eventID(env, req.Payload),
		Cursor:        req.Cursor,
		SchemaVersion: EventSchemaVersion,
		Kind:          kind,
		RunID:         model.RunID(strings.TrimSpace(env.RunID)),
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

func parseTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// eventID 优先用来源给的稳定身份，缺失时按记录内容派生。
//
// 稳定身份是去重的前提：同一记录重复到达必须得到同一 event_id。
func eventID(env envelope, payload []byte) model.EventID {
	if id := strings.TrimSpace(env.EventID); id != "" {
		return model.EventID(id)
	}
	sum := sha256.Sum256(append([]byte(env.Type+"|"+env.RunID+"|"), payload...))
	return model.EventID("evt_" + hex.EncodeToString(sum[:])[:20])
}
