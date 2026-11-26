package codebuddy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// interpretAdapter 造一个时钟固定的适配器（Interpret 不依赖探测结果）。
func interpretAdapter(t *testing.T) *Adapter {
	t.Helper()
	cfg, _, _ := testNode(t)
	return newAdapter(t, cfg, newFakeRunner(), WithClock(func() time.Time { return evidenceClock }))
}

func exitCode(n int) *int { return &n }

// resultRecord 取出样本文件里第一条 result 记录。
func resultRecord(t *testing.T, rel string) []byte {
	t.Helper()
	return recordOf(t, evidenceLines(t, rel), "result", "")
}

// TestInterpretCompleted 覆盖正常结果（S03）：四个条件都满足才判完成。
func TestInterpretCompleted(t *testing.T) {
	a := interpretAdapter(t)
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: resultRecord(t, "samples/VN301-S03-print-stream-json/events.jsonl"),
		ExitCode:    exitCode(0),
	})
	if err != nil {
		t.Fatalf("正常结果不应报错：%v", err)
	}
	if res.Outcome != model.OutcomeCompleted {
		t.Fatalf("应判 completed，实际 %s", res.Outcome)
	}
	if res.ResultText != "MAW-OK" {
		t.Fatalf("结果正文应为 MAW-OK，实际 %q", res.ResultText)
	}
	if res.Quality != model.QualityObserved {
		t.Fatalf("质量应为 observed，实际 %s", res.Quality)
	}
	assertInterpretValid(t, res)
}

// TestInterpretExitZeroAuthFailure 是本卡最重要的一条：退出码 0 + 认证失败。
//
// S08：真实未登录时 CLI 退出 0、stdout 有完整 result 记录，但 subtype=error_during_execution、
// is_error=true、errors 里是 "Authentication required…"。按退出码判成功就是假 done（P141）。
func TestInterpretExitZeroAuthFailure(t *testing.T) {
	a := interpretAdapter(t)
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: resultRecord(t, "samples/VN301-S08-print-stream-json-no-credentials/events.jsonl"),
		ExitCode:    exitCode(0),
	})
	e := requirePortErr(t, err, model.PortErrAuthRequired)
	if e.Class != model.ClassTerminal {
		t.Fatalf("未登录应是 terminal（要人去登录，不是重试），实际 %s", e.Class)
	}
	if res.Outcome != model.OutcomeFailed {
		t.Fatalf("未登录不能判完成，应为 failed，实际 %s", res.Outcome)
	}
	if res.Quality != model.QualityObserved {
		t.Fatalf("这条结论有实测依据，质量应为 observed，实际 %s", res.Quality)
	}
	assertInterpretValid(t, res)
}

// TestInterpretMissingResult 覆盖 X02：缺最终结果记录。
//
// 退出码 0 → 什么都不能判（unknown）；退出码非 0 → 进程确实失败了（failed）。
func TestInterpretMissingResult(t *testing.T) {
	a := interpretAdapter(t)

	res, err := a.Interpret(context.Background(), ports.InterpretRequest{ExitCode: exitCode(0)})
	requirePortErr(t, err, model.PortErrMissingResult)
	if res.Outcome != model.OutcomeUnknown {
		t.Fatalf("缺结果记录 + 退出码 0 应保持 unknown，实际 %s", res.Outcome)
	}
	assertInterpretValid(t, res)

	// 只给退出码、不给结果记录，且退出码非 0：进程层面失败是确定的事实。
	failed, err := a.Interpret(context.Background(), ports.InterpretRequest{ExitCode: exitCode(1)})
	if err != nil {
		t.Fatalf("退出码非 0 是确定结论，不应报错：%v", err)
	}
	if failed.Outcome != model.OutcomeFailed {
		t.Fatalf("退出码非 0 应判 failed，实际 %s", failed.Outcome)
	}
	assertInterpretValid(t, failed)

	// 连退出码都没有：请求本身没有可判定依据，属于非法规格而不是「待核实」。
	if _, err := a.Interpret(context.Background(), ports.InterpretRequest{}); err == nil {
		t.Fatal("没有任何依据的请求必须被拒绝")
	}
}

// TestInterpretEmptySuccess 覆盖 X03：subtype=success 但正文为空。
func TestInterpretEmptySuccess(t *testing.T) {
	a := interpretAdapter(t)

	var records []json.RawMessage
	raw := evidenceFile(t, "samples/synthetic/VN301-X03-success-without-content/records.json")
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatalf("解析 X03 失败：%v", err)
	}
	last := records[len(records)-1]

	res, err := a.Interpret(context.Background(), ports.InterpretRequest{FinalResult: last, ExitCode: exitCode(0)})
	requirePortErr(t, err, model.PortErrMissingResult)
	if res.Outcome != model.OutcomeUnknown {
		t.Fatalf("空正文的成功标记必须降级为待核实，实际 %s", res.Outcome)
	}
	if res.ResultText != "" {
		t.Fatalf("不得凭空补正文，实际 %q", res.ResultText)
	}
	assertInterpretValid(t, res)
}

// TestInterpretUnknownSubtype 覆盖未识别 subtype：既不判完成也不判失败（P142）。
func TestInterpretUnknownSubtype(t *testing.T) {
	a := interpretAdapter(t)
	payload := []byte(`{"type":"result","subtype":"paused","is_error":false,"result":"half way"}`)

	res, err := a.Interpret(context.Background(), ports.InterpretRequest{FinalResult: payload, ExitCode: exitCode(0)})
	e := requirePortErr(t, err, model.PortErrProtocolUnknown)
	if e.Class != model.ClassUnknown {
		t.Fatalf("未识别结果属 unknown 类（保持现场），实际 %s", e.Class)
	}
	if res.Outcome != model.OutcomeUnknown {
		t.Fatalf("应保持 unknown，实际 %s", res.Outcome)
	}
	if res.Quality != model.QualityUnknown {
		t.Fatalf("质量应为 unknown，实际 %s", res.Quality)
	}
}

// TestInterpretUnknownFieldsIgnored 覆盖 X04：未知字段不影响判定。
//
// X04 的 result 记录带 cli_version 与 future_usage_field 两个未实测字段；
// 判定必须与 S03 完全一致——未识别字段既不升级也不降级结论。
func TestInterpretUnknownFieldsIgnored(t *testing.T) {
	a := interpretAdapter(t)
	payload := recordOf(t, evidenceLines(t, "samples/synthetic/VN301-X04-unknown-event-and-version/events.jsonl"), "result", "")

	res, err := a.Interpret(context.Background(), ports.InterpretRequest{FinalResult: payload, ExitCode: exitCode(0)})
	if err != nil {
		t.Fatalf("未知字段不应影响判定：%v", err)
	}
	if res.Outcome != model.OutcomeCompleted || res.ResultText != "MAW-OK" {
		t.Fatalf("应与 S03 同判，实际 outcome=%s text=%q", res.Outcome, res.ResultText)
	}
}

// TestInterpretExitCodeConflict 覆盖「结果记录说成功、退出码非 0」：证据冲突，不取单方。
func TestInterpretExitCodeConflict(t *testing.T) {
	a := interpretAdapter(t)
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: resultRecord(t, "samples/VN301-S03-print-stream-json/events.jsonl"),
		ExitCode:    exitCode(1),
	})
	requirePortErr(t, err, model.PortErrProtocolUnknown)
	if res.Outcome != model.OutcomeUnknown {
		t.Fatalf("证据冲突应保持待核实，实际 %s", res.Outcome)
	}
}

// TestInterpretTimedOut 覆盖超时：确定结论，不被结果记录覆盖。
func TestInterpretTimedOut(t *testing.T) {
	a := interpretAdapter(t)
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{TimedOut: true, ExitCode: exitCode(0)})
	if err != nil {
		t.Fatalf("超时是确定结论，不应报错：%v", err)
	}
	if res.Outcome != model.OutcomeTimedOut {
		t.Fatalf("应判 timed_out，实际 %s", res.Outcome)
	}
	assertInterpretValid(t, res)
}

// TestInterpretApplicationError 覆盖应用错误：进程层面异常优先于结果记录。
func TestInterpretApplicationError(t *testing.T) {
	a := interpretAdapter(t)
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{
		ApplicationError: "listen EADDRINUSE: address already in use",
		FinalResult:      resultRecord(t, "samples/VN301-S03-print-stream-json/events.jsonl"),
		ExitCode:         exitCode(0),
	})
	if err != nil {
		t.Fatalf("应用错误是确定结论，不应报错：%v", err)
	}
	if res.Outcome != model.OutcomeFailed {
		t.Fatalf("有应用错误时不得判完成，实际 %s", res.Outcome)
	}
	if res.ResultText == "" {
		t.Fatal("应用错误必须保留原文")
	}
}

// TestInterpretProtocolViolations 覆盖「最终结果不是单条 result 记录」。
func TestInterpretProtocolViolations(t *testing.T) {
	a := interpretAdapter(t)

	for _, payload := range [][]byte{
		[]byte(`not json`),
		[]byte(`[{"type":"result","subtype":"success","result":"x"}]`), // json 模式的数组（C05）
		[]byte(`{"type":"assistant","message":{}}`),                    // 类型不对
	} {
		_, err := a.Interpret(context.Background(), ports.InterpretRequest{FinalResult: payload, ExitCode: exitCode(0)})
		requirePortErr(t, err, model.PortErrProtocolUnknown)
	}
}

// TestInterpretContradictoryRecord 覆盖 subtype=success 但 is_error=true：记录自相矛盾。
func TestInterpretContradictoryRecord(t *testing.T) {
	a := interpretAdapter(t)
	payload := []byte(`{"type":"result","subtype":"success","is_error":true,"result":"MAW-OK"}`)

	res, err := a.Interpret(context.Background(), ports.InterpretRequest{FinalResult: payload, ExitCode: exitCode(0)})
	requirePortErr(t, err, model.PortErrApplication)
	if res.Outcome != model.OutcomeFailed {
		t.Fatalf("矛盾记录不能判完成，实际 %s", res.Outcome)
	}
	if res.Quality != model.QualityUnknown {
		t.Fatalf("记录自相矛盾时必须降级质量，实际 %s", res.Quality)
	}
}

// TestInterpretArtifactsStayCandidates 覆盖产物：原样透传，不冒充「已核实」。
func TestInterpretArtifactsStayCandidates(t *testing.T) {
	a := interpretAdapter(t)
	artifacts := []model.ArtifactCandidate{
		{Kind: model.ArtifactCommit, Ref: "8c13a5a", DeclaredBy: model.DeclaredByCLI},
	}
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: resultRecord(t, "samples/VN301-S03-print-stream-json/events.jsonl"),
		ExitCode:    exitCode(0),
		Artifacts:   artifacts,
	})
	if err != nil {
		t.Fatalf("判定失败：%v", err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].Ref != "8c13a5a" {
		t.Fatalf("产物必须原样透传，实际 %+v", res.Artifacts)
	}
	if res.Artifacts[0].DeclaredBy != model.DeclaredByCLI {
		t.Fatal("CLI 声明的产物不能被升级为人工确认")
	}
	if res.Artifacts[0].SHA256 != nil {
		t.Fatal("未核实的产物不得带内容哈希")
	}
}

// TestInterpretRejectsEmptyRequest 覆盖空请求。
func TestInterpretRejectsEmptyRequest(t *testing.T) {
	a := interpretAdapter(t)
	if _, err := a.Interpret(context.Background(), ports.InterpretRequest{}); err == nil {
		t.Fatal("没有任何可判定依据的请求必须被拒绝")
	}
}

// assertInterpretValid 确认判定结果本身合契约。
func assertInterpretValid(t *testing.T, res model.InterpretResult) {
	t.Helper()
	if vs := res.Validate(); len(vs) != 0 {
		t.Fatalf("判定结果不合契约：%v", model.Err(vs))
	}
}
