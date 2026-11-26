package codebuddy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
)

// S03 的真实 result 记录（实测）：input 已含缓存分解（C18）。
func realResultRecord(t *testing.T) []byte {
	t.Helper()
	return recordOf(t, evidenceLines(t, "samples/VN301-S03-print-stream-json/events.jsonl"), "result", "")
}

// 只有真实样本支持时才映射：S03 实测字段的解析结果必须一字不差。
func TestParseUsageFromRealSample(t *testing.T) {
	obs, err := ParseUsage(realResultRecord(t))
	if err != nil {
		t.Fatalf("解析真实样本失败：%v", err)
	}
	if obs == nil {
		t.Fatal("实测样本带 usage，解析不得为 nil")
	}
	if got := *obs.InputTokens; got != 23945 {
		t.Fatalf("input_tokens = %d，要 23945（S03 实测）", got)
	}
	if got := *obs.OutputTokens; got != 23 {
		t.Fatalf("output_tokens = %d，要 23", got)
	}
	if got := *obs.CacheWriteTokens; got != 23433 {
		t.Fatalf("cache_write = %d，要 23433（cache_creation_input_tokens）", got)
	}
	if got := *obs.CacheReadTokens; got != 512 {
		t.Fatalf("cache_read = %d，要 512", got)
	}
	// C18：input 已含缓存，total 只能是 input+output；再加缓存就是重复计量。
	if got := *obs.TotalTokens; got != 23968 {
		t.Fatalf("total_tokens = %d，要 23968（input+output，不含缓存）", got)
	}
	if obs.Semantic != UsageSemanticDelta {
		t.Fatalf("口径 = %q，要 delta：一次运行恰好一条 result（C08），来源没有累计计数", obs.Semantic)
	}
	if obs.Quality != model.QualityObserved {
		t.Fatalf("质量 = %v，要 observed（C17 实测字段）", obs.Quality)
	}
	if !strings.Contains(obs.Evidence, "C17") || !strings.Contains(obs.Evidence, "C18") {
		t.Fatalf("口径证据缺失：%s", obs.Evidence)
	}
	if obs.SessionID == "" {
		t.Fatal("result 记录实测携带 session_id，解析不得丢")
	}
}

// 重放同一条记录得到同一观测：计量去重身份稳定。
func TestParseUsageReplayStable(t *testing.T) {
	payload := realResultRecord(t)
	a, err := ParseUsage(payload)
	if err != nil {
		t.Fatalf("第一次解析失败：%v", err)
	}
	b, err := ParseUsage(payload)
	if err != nil {
		t.Fatalf("重放解析失败：%v", err)
	}
	if a.SessionID != b.SessionID || a.RecordUUID != b.RecordUUID {
		t.Fatal("重放同一条记录必须得到同一来源身份")
	}
	if *a.InputTokens != *b.InputTokens || *a.TotalTokens != *b.TotalTokens {
		t.Fatal("重放同一条记录的用量必须一致")
	}
	// delta 口径：两次重放也不相加、不做差——重复观测由计量侧按身份去重。
	if *a.InputTokens != 23945 {
		t.Fatalf("重放不得改变数值，得到 %d", *a.InputTokens)
	}
}

// C19 反例：assistant 记录也带 usage，但聚合口径未验证——必须拒绝解析。
func TestParseUsageRefusesAssistantRecord(t *testing.T) {
	line := recordOf(t, evidenceLines(t, "samples/VN301-S03-print-stream-json/events.jsonl"), "assistant", "")
	obs, err := ParseUsage(line)
	if err == nil {
		t.Fatalf("assistant 记录的 usage 不得解析（C19 unknown），得到 %+v", obs)
	}
	if obs != nil {
		t.Fatal("拒绝时不得返回观测")
	}
	// 思考那条 usage=0、正文那条为全量：求和就是伪造总量——错误信息要说明这一点。
	e := requirePortErr(t, err, model.PortErrProtocolUnknown)
	if !strings.Contains(e.Error(), "C19") {
		t.Fatalf("错误应引用 C19：%v", e)
	}
}

// usage 缺失 = 未观测，保留 unknown：不得返回全 0 的观测冒充实测。
func TestParseUsageMissingKeepsUnknown(t *testing.T) {
	payload := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"x","session_id":"s1","uuid":"u1"}`)
	obs, err := ParseUsage(payload)
	if err != nil {
		t.Fatalf("usage 缺失不是错误，是未观测：%v", err)
	}
	if obs != nil {
		t.Fatalf("usage 缺失必须返回 nil 让调用方保持 unknown，得到 %+v", obs)
	}
}

// 负数 token 拒绝：计量契约 T140。
func TestParseUsageRejectsNegative(t *testing.T) {
	payload := []byte(`{"type":"result","subtype":"success","is_error":false,"session_id":"s1","uuid":"u1",
		"usage":{"input_tokens":-5,"output_tokens":3}}`)
	obs, err := ParseUsage(payload)
	if err == nil {
		t.Fatalf("负数 token 必须拒绝，得到 %+v", obs)
	}
	requirePortErr(t, err, model.PortErrInvalidSpec)
}

// C20/C21 反例：num_turns 与 total_cost_usd 不出现在观测里。
func TestParseUsageIgnoresUnverifiedFields(t *testing.T) {
	payload := []byte(`{"type":"result","subtype":"success","is_error":false,"session_id":"s1","uuid":"u1",
		"usage":{"input_tokens":100,"output_tokens":20,"total_cost_usd":0.5,"num_turns":3},
		"num_turns":3,"total_cost_usd":0.5}`)
	obs, err := ParseUsage(payload)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	raw, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "num_turns") || strings.Contains(s, "cost") {
		t.Fatalf("未验证字段不得出现在观测里（C20/C21）：%s", s)
	}
	// total 只由 input+output 组成。
	if *obs.TotalTokens != 120 {
		t.Fatalf("total = %d，要 120（不含 cost/turns）", *obs.TotalTokens)
	}
}

// 共享轮次反例：CodeBuddy 样本没有 shared_with 字段，观测不得发明共享分摊。
func TestParseUsageNeverInventsSharedTurns(t *testing.T) {
	obs, err := ParseUsage(realResultRecord(t))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "shared") {
		t.Fatalf("样本没有共享轮次证据，观测不得发明 shared 字段：%s", raw)
	}
}

// wait 反例：四类区间里的 explicit_wait 没有任何实测样本——解码永远不产出等待事实。
func TestDecodeNeverEmitsWaitEvents(t *testing.T) {
	a, _, _ := probedAdapter(t)
	lines := evidenceLines(t, "samples/VN301-S03-print-stream-json/events.jsonl")
	for i, line := range lines {
		res, err := a.Decode(context.Background(), decodeReq(line, i+1))
		if err != nil {
			continue // 未识别记录允许零事件 + 错误
		}
		for _, ev := range res.Events {
			if strings.Contains(string(ev.Kind), "wait") {
				t.Fatalf("没有等待样本却产出等待事件 %q（记录 %d）", ev.Kind, i+1)
			}
		}
	}
}

// 恢复能力：C24 unknown → 不支持；请求什么恢复目标都拒绝，最近会话不兜底。
func TestRecoveryCapabilityUnknownAndResumeRefused(t *testing.T) {
	cap := Recovery()
	if cap.State != model.CapabilityUnknown {
		t.Fatalf("恢复能力状态 = %v，要 unknown（C24）", cap.State)
	}
	if cap.Supported {
		t.Fatal("unknown 的能力不得声明为支持")
	}

	// 空请求：没有已验证目标，拒绝。
	if target, err := ResumeTarget(""); err == nil || target != "" {
		t.Fatalf("空请求必须拒绝，得到 (%q, %v)", target, err)
	}
	// 明确请求某个会话：同样拒绝——恢复语义未验证。
	target, err := ResumeTarget("sess-abc")
	if err == nil || target != "" {
		t.Fatalf("显式会话请求必须拒绝，得到 (%q, %v)", target, err)
	}
	// 错误信息必须点名「最近会话不作为恢复目标」与 C24。
	e := requirePortErr(t, err, model.PortErrCapabilityMissing)
	if !strings.Contains(e.Error(), "最近会话") || !strings.Contains(e.Error(), "C24") {
		t.Fatalf("拒绝理由不完整：%v", e)
	}
}

// 恢复参数只进拒绝路径：即使规格带了 ResumeSessionID，argv 里也不得出现。
func TestLaunchNeverBuildsResumeArgv(t *testing.T) {
	a, _, fake := probedAdapter(t)
	spec := testRunSpec(t)
	spec.Session.ResumeSessionID = "sess-abc"
	_, err := a.BuildLaunchSpec(context.Background(), spec)
	if err == nil {
		t.Fatal("带恢复目标的规格必须被拒绝（C24）")
	}
	for _, call := range fake.calls {
		for _, arg := range call.Args {
			switch arg {
			case "-r", "--resume", "-c", "--continue", "--session-id":
				t.Fatalf("argv 不得包含恢复参数，得到 %v", call.Args)
			}
		}
	}
}

// 身份提取：init/result 都实测携带 session_id；没有就不发明。
func TestParseSessionRef(t *testing.T) {
	ref, err := ParseSessionRef(realResultRecord(t))
	if err != nil {
		t.Fatal(err)
	}
	if ref == nil || ref.SessionID == "" {
		t.Fatal("result 记录实测携带 session_id，提取不得为空")
	}
	if ref.RecordType != "result" {
		t.Fatalf("记录类型 = %q，要 result", ref.RecordType)
	}
	noSession := []byte(`{"type":"assistant","uuid":"u9"}`)
	ref, err = ParseSessionRef(noSession)
	if err != nil {
		t.Fatalf("无会话身份不是错误：%v", err)
	}
	if ref != nil {
		t.Fatalf("没有 session_id 就返回 nil，保持未归属，得到 %+v", ref)
	}
}
