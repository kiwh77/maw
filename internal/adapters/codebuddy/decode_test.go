package codebuddy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// evidenceClock 是测试用固定时钟：晚于 VN-301 全部样本的 __timestamp（2026-10-23T03:05Z），
// 这样「接收晚于发生」成立，观测质量不会被时钟偏差降级。
var evidenceClock = time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC)

// evidenceFile 读取证据目录里的样本文件（测试只读，不改证据）。
func evidenceFile(t *testing.T, rel string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "evidence", "codebuddy", rel))
	if err != nil {
		t.Fatalf("读证据样本 %s 失败（本卡依赖 VN-301 产物）：%v", rel, err)
	}
	return raw
}

// evidenceLines 把 JSONL 样本拆成逐条记录（保留原始字节，损坏行也在里面）。
func evidenceLines(t *testing.T, rel string) [][]byte {
	t.Helper()
	var out [][]byte
	for _, line := range strings.Split(string(evidenceFile(t, rel)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, []byte(line))
	}
	return out
}

// recordOf 在样本里找出第一条 type／subtype 匹配的记录。
func recordOf(t *testing.T, lines [][]byte, typ, subtype string) []byte {
	t.Helper()
	for _, line := range lines {
		var probe struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Type == typ && (subtype == "" || probe.Subtype == subtype) {
			return line
		}
	}
	t.Fatalf("样本里找不到 type=%s subtype=%s 的记录", typ, subtype)
	return nil
}

func decodeReq(payload []byte, cursor int) ports.DecodeRequest {
	return ports.DecodeRequest{
		RecordRef:  "run_abc/stdout.jsonl#" + strconv.Itoa(cursor),
		RecordType: "stream.record",
		Payload:    payload,
		Cursor:     model.Cursor(cursor),
		Protocol:   model.Protocol{Format: "stream-json", Version: ProtocolVersion},
	}
}

// decodeAdapter 造一个时钟固定的适配器（探测未被调用，Decode 不需要它）。
func decodeAdapter(t *testing.T) *Adapter {
	t.Helper()
	cfg, _, _ := testNode(t)
	return newAdapter(t, cfg, newFakeRunner(), WithClock(func() time.Time { return evidenceClock }))
}

// TestDecodeRealStream 用 S03 的真实 23 条记录跑一遍：只该产出 4 条事件。
//
// 23 = init 1 + status 1 + stream_event 18 + assistant 2 + result 1。
// 其中 status 与 18 条 SSE 透传都不承载运行事实，不产出事件。
func TestDecodeRealStream(t *testing.T) {
	a := decodeAdapter(t)
	lines := evidenceLines(t, "samples/VN301-S03-print-stream-json/events.jsonl")
	if len(lines) != 23 {
		t.Fatalf("S03 应有 23 条记录，实际 %d", len(lines))
	}

	kinds := map[string]int{}
	ids := map[model.EventID]bool{}
	cursor := 0
	for _, line := range lines {
		cursor++
		res, err := a.Decode(context.Background(), decodeReq(line, cursor))
		if err != nil {
			t.Fatalf("第 %d 条记录解码失败：%v", cursor, err)
		}
		if res.RawRef == "" {
			t.Fatalf("第 %d 条记录必须保留原始引用", cursor)
		}
		for _, ev := range res.Events {
			kinds[ev.Kind]++
			if ids[ev.EventID] {
				t.Fatalf("事件身份重复：%s", ev.EventID)
			}
			ids[ev.EventID] = true
			if !ev.EventID.Valid() {
				t.Errorf("事件身份不合法：%q", ev.EventID)
			}
			if ev.Cursor != model.Cursor(cursor) {
				t.Errorf("游标应等于入库序号 %d，实际 %d", cursor, ev.Cursor)
			}
			if ev.SchemaVersion != EventSchemaVersion {
				t.Errorf("事件必须带 schema 版本 %d，实际 %d", EventSchemaVersion, ev.SchemaVersion)
			}
			if ev.Quality != model.QualityObserved {
				t.Errorf("样本带 __timestamp 且接收晚于发生，质量应为 observed，实际 %s", ev.Quality)
			}
			if ev.OccurredAt.IsZero() || ev.ReceivedAt.IsZero() {
				t.Errorf("发生与接收时间都必须记录")
			}
			if ev.OccurredAt.After(ev.ReceivedAt) {
				t.Errorf("发生时间不得晚于接收时间")
			}
		}
	}

	want := map[string]int{KindTurnStarted: 1, KindTurnMessage: 2, KindTurnFinished: 1}
	if len(kinds) != len(want) {
		t.Fatalf("事件种类应为 %v，实际 %v", want, kinds)
	}
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("%s 应有 %d 条，实际 %d（全部：%v）", k, n, kinds[k], kinds)
		}
	}
}

// TestDecodeNeverEmitsControlKinds 钉住「接入器不写控制类事实」。
func TestDecodeNeverEmitsControlKinds(t *testing.T) {
	control := map[string]bool{
		"run.phase": true, "task.status": true, "lease.acquired": true,
		"lease.released": true, "operation.updated": true, "claim.requested": true,
	}
	a := decodeAdapter(t)
	for _, rel := range []string{
		"samples/VN301-S03-print-stream-json/events.jsonl",
		"samples/VN301-S08-print-stream-json-no-credentials/events.jsonl",
		"samples/synthetic/VN301-X04-unknown-event-and-version/events.jsonl",
	} {
		cursor := 0
		for _, line := range evidenceLines(t, rel) {
			cursor++
			res, err := a.Decode(context.Background(), decodeReq(line, cursor))
			if err != nil {
				continue // 未识别／损坏记录本来就不产事件
			}
			for _, ev := range res.Events {
				if control[ev.Kind] {
					t.Fatalf("%s 产出了控制类事件 %s：业务状态只能由 app 层写", rel, ev.Kind)
				}
				if ev.Phase != nil {
					t.Fatalf("%s 的事件带了运行阶段：接入器不能声称阶段迁移", rel)
				}
			}
		}
	}
}

// TestDecodeEventIDIsStable 钉住重放去重：同一条记录重放必须得到同一个事件身份。
func TestDecodeEventIDIsStable(t *testing.T) {
	a := decodeAdapter(t)
	line := recordOf(t, evidenceLines(t, "samples/VN301-S03-print-stream-json/events.jsonl"), "assistant", "")

	first, err := a.Decode(context.Background(), decodeReq(line, 1))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	replay, err := a.Decode(context.Background(), decodeReq(line, 9))
	if err != nil {
		t.Fatalf("重放解码失败：%v", err)
	}
	if first.Events[0].EventID != replay.Events[0].EventID {
		t.Fatalf("重放必须得到同一事件身份：%s vs %s", first.Events[0].EventID, replay.Events[0].EventID)
	}

	// 内容不同的两条记录不能撞同一个身份。
	other := recordOf(t, evidenceLines(t, "samples/VN301-S03-print-stream-json/events.jsonl"), "result", "")
	otherRes, err := a.Decode(context.Background(), decodeReq(other, 2))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if otherRes.Events[0].EventID == first.Events[0].EventID {
		t.Fatal("不同记录不应得到同一事件身份")
	}
}

// TestDecodeWithoutUUID 覆盖来源没给 uuid 时的兜底：靠内容哈希，仍可重放去重。
func TestDecodeWithoutUUID(t *testing.T) {
	a := decodeAdapter(t)
	raw := map[string]any{"type": "assistant", "message": map[string]any{"content": "hello"}}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("构造记录失败：%v", err)
	}

	first, err := a.Decode(context.Background(), decodeReq(payload, 1))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	replay, err := a.Decode(context.Background(), decodeReq(payload, 2))
	if err != nil {
		t.Fatalf("重放解码失败：%v", err)
	}
	if first.Events[0].EventID != replay.Events[0].EventID {
		t.Fatal("无 uuid 时同一内容重放也必须同身份")
	}

	changed := map[string]any{"type": "assistant", "message": map[string]any{"content": "hello!"}}
	changedPayload, _ := json.Marshal(changed)
	other, err := a.Decode(context.Background(), decodeReq(changedPayload, 3))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if other.Events[0].EventID == first.Events[0].EventID {
		t.Fatal("内容变化必须得到不同事件身份")
	}
}

// TestDecodeCorruptedLine 覆盖 X01：坏行只留证据，不推断内容、不产事件。
func TestDecodeCorruptedLine(t *testing.T) {
	a := decodeAdapter(t)
	lines := evidenceLines(t, "samples/synthetic/VN301-X01-corrupted-line/events.jsonl")
	broken := lines[len(lines)-1]

	res, err := a.Decode(context.Background(), decodeReq(broken, len(lines)))
	requirePortErr(t, err, model.PortErrUnparseableRecord)
	if len(res.Events) != 0 {
		t.Fatalf("坏行不得产出事件，实际 %d 条", len(res.Events))
	}
	if res.RawRef != decodeReq(broken, len(lines)).RecordRef {
		t.Fatalf("坏行必须保留原始引用，实际 %q", res.RawRef)
	}
	if res.Quality != model.QualityUnknown {
		t.Fatalf("坏行的证据质量应为 unknown，实际 %s", res.Quality)
	}
}

// TestDecodeUnknownRecordType 覆盖 X04 的 system/hook：类型未识别，不产事件。
func TestDecodeUnknownRecordType(t *testing.T) {
	a := decodeAdapter(t)
	lines := evidenceLines(t, "samples/synthetic/VN301-X04-unknown-event-and-version/events.jsonl")
	last := lines[len(lines)-1]

	res, err := a.Decode(context.Background(), decodeReq(last, len(lines)))
	requirePortErr(t, err, model.PortErrUnknownRecordType)
	if len(res.Events) != 0 {
		t.Fatalf("未识别记录不得产出事件，实际 %d 条", len(res.Events))
	}
	if res.RawRef == "" || res.Quality != model.QualityUnknown {
		t.Fatalf("未识别记录应保留引用并标 unknown，实际 ref=%q quality=%s", res.RawRef, res.Quality)
	}
}

// TestDecodeKnownButFactless 覆盖 system/status 与 stream_event：零事件，但不是错误。
func TestDecodeKnownButFactless(t *testing.T) {
	a := decodeAdapter(t)
	lines := evidenceLines(t, "samples/VN301-S03-print-stream-json/events.jsonl")

	for _, line := range [][]byte{
		recordOf(t, lines, "system", "status"),
		recordOf(t, lines, "stream_event", ""),
	} {
		res, err := a.Decode(context.Background(), decodeReq(line, 1))
		if err != nil {
			t.Fatalf("已知但不承载事实的记录不应报错：%v", err)
		}
		if len(res.Events) != 0 {
			t.Fatalf("这类记录不应产出事件，实际 %d 条", len(res.Events))
		}
		if res.Quality != model.QualityObserved {
			t.Fatalf("能读懂且无异常，质量应为 observed，实际 %s", res.Quality)
		}
	}
}

// TestDecodeProtocolVersion 覆盖未知版本与错误格式：不猜兼容。
func TestDecodeProtocolVersion(t *testing.T) {
	a := decodeAdapter(t)
	line := recordOf(t, evidenceLines(t, "samples/VN301-S03-print-stream-json/events.jsonl"), "result", "")

	for _, proto := range []model.Protocol{
		{Format: "stream-json", Version: "2"},
		{Format: "stream-json", Version: "v1"},
		{Format: "json", Version: ProtocolVersion},
	} {
		req := decodeReq(line, 1)
		req.Protocol = proto
		res, err := a.Decode(context.Background(), req)
		requirePortErr(t, err, model.PortErrVersionUnsupported)
		if len(res.Events) != 0 {
			t.Fatalf("%+v 不得产出事件", proto)
		}
	}
}

// TestDecodeRejectsBadRequest 覆盖请求本身不合法。
func TestDecodeRejectsBadRequest(t *testing.T) {
	a := decodeAdapter(t)
	line := recordOf(t, evidenceLines(t, "samples/VN301-S03-print-stream-json/events.jsonl"), "result", "")

	noRef := decodeReq(line, 1)
	noRef.RecordRef = ""
	if _, err := a.Decode(context.Background(), noRef); err == nil {
		t.Fatal("缺原始位置必须被拒绝")
	}

	badCursor := decodeReq(line, 1)
	badCursor.Cursor = 0
	if _, err := a.Decode(context.Background(), badCursor); err == nil {
		t.Fatal("游标从 1 开始，0 必须被拒绝")
	}
}

// TestDecodeClockSkew 覆盖「发生晚于接收」：降级为 unknown，不校正成看起来完整。
func TestDecodeClockSkew(t *testing.T) {
	cfg, _, _ := testNode(t)
	// 时钟拨到样本时间之前 → 所有记录都「发生在未来」。
	early := time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)
	a := newAdapter(t, cfg, newFakeRunner(), WithClock(func() time.Time { return early }))

	line := recordOf(t, evidenceLines(t, "samples/VN301-S03-print-stream-json/events.jsonl"), "result", "")
	res, err := a.Decode(context.Background(), decodeReq(line, 1))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if res.Events[0].Quality != model.QualityUnknown {
		t.Fatalf("发生晚于接收必须降级为 unknown，实际 %s", res.Events[0].Quality)
	}
}
