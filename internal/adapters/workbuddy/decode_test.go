package workbuddy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

func jsonProtocol() model.Protocol {
	return model.Protocol{Format: OutputFormat, Version: ProtocolVersion}
}

func decodeReq(payload string) ports.DecodeRequest {
	return ports.DecodeRequest{
		RecordRef:  "run:1:stdout:1",
		RecordType: "result",
		Payload:    []byte(payload),
		Cursor:     1,
		Protocol:   jsonProtocol(),
	}
}

// TestDecodeRefusesUnobservedRecordShape 是本卡最重要的闸：
// 两包从未产出过 result 记录，此时把记录解释成事件就是「按未验证语义造事实」。
func TestDecodeRefusesUnobservedRecordShape(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	a.clock = func() time.Time { return time.Unix(1700000000, 0).UTC() }

	_, err := a.Decode(context.Background(), decodeReq(`{"type":"result","subtype":"success","result":"ok"}`))
	e := requirePortErr(t, err, model.PortErrUnknownRecordType)
	if !strings.Contains(e.Msg, capResultRecord) {
		t.Fatalf("错误文案应点名被挡住的能力，实际 %q", e.Msg)
	}
	if !strings.Contains(e.Msg, string(model.CapabilityUnobserved)) {
		t.Fatalf("错误文案应给出当前能力状态，实际 %q", e.Msg)
	}
}

// TestDecodeMapsResultWhenShapeObserved 覆盖「证据到位之后」的映射：
// 代码里确实有委托下来的语义，只是被证据闸门挡着。
func TestDecodeMapsResultWhenShapeObserved(t *testing.T) {
	dist, _ := Lookup(DistributionCN)
	cfg, _, _ := testNode(t, DistributionCN)
	a := newAdapter(t, cfg, nil)
	fixed := time.Unix(1700000000, 0).UTC()
	a.clock = func() time.Time { return fixed }
	forceMeasurement(a, dist, observedCaps(dist, capResultRecord), parseHelpFlags([]byte(helpFixture)))

	payload := `{"type":"result","subtype":"success","uuid":"u-1","result":"MAW-OK","__timestamp":"2023-11-14T22:13:20Z"}`
	res, err := a.Decode(context.Background(), decodeReq(payload))
	if err != nil {
		t.Fatalf("形状已观测时不应报错：%v", err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("应产出一条事件，实际 %d 条", len(res.Events))
	}
	ev := res.Events[0]
	if ev.Kind != KindTurnFinished {
		t.Errorf("result 记录应映射为 %s，实际 %s", KindTurnFinished, ev.Kind)
	}
	if ev.Quality != model.QualityObserved {
		t.Errorf("来源给了发生时间时应为 observed，实际 %s", ev.Quality)
	}
	if res.RawRef != "run:1:stdout:1" {
		t.Errorf("必须保留原始引用，实际 %q", res.RawRef)
	}

	// 重放同一条记录必须得到同一个事件身份，否则 store 会把重放记成两条。
	again, err := a.Decode(context.Background(), decodeReq(payload))
	if err != nil {
		t.Fatalf("重放解码失败：%v", err)
	}
	if again.Events[0].EventID != ev.EventID {
		t.Fatalf("同一条记录的事件身份应稳定：%s ≠ %s", again.Events[0].EventID, ev.EventID)
	}
	// 换发行包后身份不同：两包即便内容相同也是两个来源的事实。
	intl, _ := Lookup(DistributionIntl)
	forceMeasurement(a, intl, observedCaps(intl, capResultRecord), parseHelpFlags([]byte(helpFixture)))
	other, err := a.Decode(context.Background(), decodeReq(payload))
	if err != nil {
		t.Fatalf("intl 解码失败：%v", err)
	}
	if other.Events[0].EventID == ev.EventID {
		t.Fatal("两包的事件身份不应相同：命名空间必须带发行包")
	}
}

// TestDecodeDegradesQualityWithoutTimestamp 覆盖来源没给发生时间：
// 只能取接收时刻并降级为 estimated，不冒充「发生时间」。
func TestDecodeDegradesQualityWithoutTimestamp(t *testing.T) {
	dist, _ := Lookup(DistributionCN)
	cfg, _, _ := testNode(t, DistributionCN)
	a := newAdapter(t, cfg, nil)
	fixed := time.Unix(1700000000, 0).UTC()
	a.clock = func() time.Time { return fixed }
	forceMeasurement(a, dist, observedCaps(dist, capResultRecord), parseHelpFlags([]byte(helpFixture)))

	res, err := a.Decode(context.Background(), decodeReq(`{"type":"result","subtype":"success","result":"ok"}`))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	ev := res.Events[0]
	if ev.Quality != model.QualityEstimated {
		t.Fatalf("缺发生时间时应降级为 estimated，实际 %s", ev.Quality)
	}
	if !ev.OccurredAt.Equal(fixed) || !ev.ReceivedAt.Equal(fixed) {
		t.Fatalf("缺发生时间时 OccurredAt 应取接收时刻：%s／%s", ev.OccurredAt, ev.ReceivedAt)
	}
}

// TestDecodeUnparseable 覆盖坏记录：留引用、不推断。
func TestDecodeUnparseable(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	res, err := a.Decode(context.Background(), decodeReq("not-json"))
	requirePortErr(t, err, model.PortErrUnparseableRecord)
	if res.RawRef == "" {
		t.Fatal("坏记录也必须保留原始引用")
	}
}

// TestDecodeRejectsUnknownTypeWhenShapeObserved 覆盖「形状已知但类型不在这套形态里」。
func TestDecodeRejectsUnknownTypeWhenShapeObserved(t *testing.T) {
	dist, _ := Lookup(DistributionCN)
	cfg, _, _ := testNode(t, DistributionCN)
	a := newAdapter(t, cfg, nil)
	forceMeasurement(a, dist, observedCaps(dist, capResultRecord), parseHelpFlags([]byte(helpFixture)))

	_, err := a.Decode(context.Background(), decodeReq(`{"type":"assistant","uuid":"u-2"}`))
	requirePortErr(t, err, model.PortErrUnknownRecordType)
}

// TestDecodeRejectsWrongProtocol 覆盖协议不符：stream-json 与未知版本都不按「大概兼容」处理。
func TestDecodeRejectsWrongProtocol(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)

	stream := decodeReq(`{"type":"result"}`)
	stream.Protocol = model.Protocol{Format: "stream-json", Version: ProtocolVersion}
	_, err := a.Decode(context.Background(), stream)
	requirePortErr(t, err, model.PortErrVersionUnsupported)

	future := decodeReq(`{"type":"result"}`)
	future.Protocol = model.Protocol{Format: OutputFormat, Version: "2"}
	_, err = a.Decode(context.Background(), future)
	requirePortErr(t, err, model.PortErrVersionUnsupported)
}

// TestDecodeRequiresProbe 覆盖「没探测过就没有能力状态可判」。
func TestDecodeRequiresProbe(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	a := newAdapter(t, cfg, nil)

	_, err := a.Decode(context.Background(), decodeReq(`{"type":"result"}`))
	requirePortErr(t, err, model.PortErrEntryUnavailable)
}

// TestDecodeRejectsInvalidRequest 覆盖请求本身不合契约。
func TestDecodeRejectsInvalidRequest(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	req := decodeReq(`{"type":"result"}`)
	req.Cursor = 0

	_, err := a.Decode(context.Background(), req)
	requirePortErr(t, err, model.PortErrInvalidSpec)
}
