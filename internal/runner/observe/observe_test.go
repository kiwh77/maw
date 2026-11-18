package observe

import (
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// piRecord 造一条 Pi 事件原始记录。
func piRecord(t *testing.T, payload string) Record {
	t.Helper()
	return Record{Payload: []byte(payload), Seq: 7, Line: 3, ReceivedAt: time.Date(2026, 11, 18, 2, 41, 0, 0, time.UTC)}
}

// TestPiExtractorReadsCallUsage 说明 Pi 的 usage 事件就是一次调用的事实：
// scope=call、semantic=delta、身份来自 call_id，缓存字段独立且带归一证据。
func TestPiExtractorReadsCallUsage(t *testing.T) {
	ext, ok := ExtractorFor(model.Protocol{Format: "pi-stream-json", Version: "1"})
	if !ok {
		t.Fatal("Pi 协议应能选到解释器")
	}
	facts, ok := ext.Extract(piRecord(t, `{
	  "contract":"maw.pi.execution/v1","event_id":"pe_01","type":"usage",
	  "run_id":"run_a001","generation":2,"call_id":"pc_01",
	  "occurred_at":"2026-11-18T02:41:09.120Z","received_at":"2026-11-18T02:41:09.180Z",
	  "payload":{"scope":"call","semantic":"delta","input_tokens":1200,"output_tokens":340,
	    "cache_read_tokens":800,"cache_write_tokens":0,"total_tokens":1540,
	    "quality":"observed","usage_kind":"worker","provider":"anthropic","model":"claude-x",
	    "input_includes_cache":true,"cache_evidence":"runtime:C30"}}`))
	if !ok {
		t.Fatal("usage 事件应产出观测事实")
	}
	if facts.SourceNS != PiSourceNS || facts.CallID != "pc_01" {
		t.Fatalf("来源身份=%q call_id=%q", facts.SourceNS, facts.CallID)
	}
	if facts.Scope != ScopeCall || facts.Semantic != "delta" {
		t.Fatalf("scope=%q semantic=%q 期望 call/delta", facts.Scope, facts.Semantic)
	}
	if !facts.HasUsage() || *facts.InputTokens != 1200 || *facts.OutputTokens != 340 {
		t.Fatalf("token 解析错误: %+v", facts)
	}
	if facts.TotalTokens == nil || *facts.TotalTokens != 1540 {
		t.Fatalf("total=%v 期望 1540", facts.TotalTokens)
	}
	if facts.Purpose != PurposeWorker {
		t.Fatalf("用途=%q 期望 worker", facts.Purpose)
	}
	if facts.Model != "claude-x" || facts.Provider != "anthropic" {
		t.Fatalf("模型归属=%q/%q", facts.Provider, facts.Model)
	}
	if facts.CacheState() != CacheVerified {
		t.Fatalf("缓存归一=%q 期望 verified", facts.CacheState())
	}
	if facts.Raw == nil || facts.Raw["type"] != "usage" {
		t.Fatalf("原始字段应保留: %+v", facts.Raw)
	}
	// 记录身份取自 call_id：重放同一条记录算出同一个轮次。
	if facts.RecordUUID != "call:pc_01" {
		t.Fatalf("record uuid=%q", facts.RecordUUID)
	}
}

// TestPiExtractorKeepsUnknownWithoutFillingZero 说明来源没给 token 时不填 0：
// quality=unknown 且 Token 全 nil，事实仍然成立（调用发生过）。
func TestPiExtractorKeepsUnknownWithoutFillingZero(t *testing.T) {
	ext, _ := ExtractorFor(model.Protocol{Format: "pi-stream-json", Version: "1"})
	facts, ok := ext.Extract(piRecord(t, `{
	  "contract":"maw.pi.execution/v1","event_id":"pe_02","type":"usage",
	  "run_id":"run_a002","generation":1,"call_id":"pc_02",
	  "payload":{"scope":"call","quality":"unknown",
	    "input_tokens":null,"output_tokens":null,
	    "basis":"来源在错误结果上给全 0 占位；按契约不当作真实用量"}}`))
	if !ok {
		t.Fatal("未知用量也要留下调用事实")
	}
	if facts.HasUsage() {
		t.Fatal("未知用量不得造出 token")
	}
	if facts.Quality != model.QualityUnknown {
		t.Fatalf("quality=%q", facts.Quality)
	}
	if !strings.Contains(facts.Evidence, "全 0 占位") {
		t.Fatalf("证据应保留来源说明: %q", facts.Evidence)
	}
}

// TestPiExtractorRejectsMalformedTokens 说明格式错误按 invalid_usage 处理：
// 字符串／负数都不猜数值，并且不再声称 observed。
func TestPiExtractorRejectsMalformedTokens(t *testing.T) {
	ext, _ := ExtractorFor(model.Protocol{Format: "pi-stream-json", Version: "1"})
	facts, ok := ext.Extract(piRecord(t, `{
	  "contract":"maw.pi.execution/v1","event_id":"pe_03","type":"usage",
	  "run_id":"run_a003","generation":1,"call_id":"pc_03",
	  "payload":{"scope":"call","quality":"observed",
	    "input_tokens":"1200","output_tokens":-5,"total_tokens":1195}}`))
	if !ok {
		t.Fatal("记录本身合法，应产出事实")
	}
	if facts.InputTokens != nil || facts.OutputTokens != nil {
		t.Fatalf("格式错误的分量必须留 nil: %+v", facts)
	}
	if facts.Quality != model.QualityUnknown {
		t.Fatalf("有格式错误时不能再声称 observed，得到 %q", facts.Quality)
	}
	if !strings.Contains(facts.Evidence, "invalid_usage") {
		t.Fatalf("应记 invalid_usage 证据: %q", facts.Evidence)
	}
}

// TestPiExtractorDropsInconsistentTotal 说明 total 与 input+output 不一致时不采信这个数（P171）。
func TestPiExtractorDropsInconsistentTotal(t *testing.T) {
	ext, _ := ExtractorFor(model.Protocol{Format: "pi-stream-json", Version: "1"})
	facts, _ := ext.Extract(piRecord(t, `{
	  "contract":"maw.pi.execution/v1","event_id":"pe_04","type":"usage",
	  "run_id":"run_a004","generation":1,"call_id":"pc_04",
	  "payload":{"scope":"call","quality":"observed",
	    "input_tokens":100,"output_tokens":50,"total_tokens":999}}`))
	if facts.TotalTokens != nil {
		t.Fatalf("不一致的 total 应被丢弃，得到 %v", *facts.TotalTokens)
	}
	if !strings.Contains(facts.Evidence, "不一致") {
		t.Fatalf("应说明丢弃原因: %q", facts.Evidence)
	}
}

// TestPiExtractorCacheWithoutEvidenceIsUnverified 说明缓存字段没有归一证据时标 unverified：
// 不自行拆分，也不并入 input。
func TestPiExtractorCacheWithoutEvidenceIsUnverified(t *testing.T) {
	ext, _ := ExtractorFor(model.Protocol{Format: "pi-stream-json", Version: "1"})
	facts, _ := ext.Extract(piRecord(t, `{
	  "contract":"maw.pi.execution/v1","event_id":"pe_05","type":"usage",
	  "run_id":"run_a005","generation":1,"call_id":"pc_05",
	  "payload":{"scope":"call","quality":"observed",
	    "input_tokens":100,"output_tokens":10,"cache_read_tokens":60}}`))
	if facts.CacheState() != CacheUnverified {
		t.Fatalf("缓存归一=%q 期望 unverified", facts.CacheState())
	}
	if facts.InputTokens == nil || *facts.InputTokens != 100 {
		t.Fatalf("缓存不得并入 input，得到 %v", facts.InputTokens)
	}
	if !strings.Contains(facts.CacheEvidence, "缺归一证据") {
		t.Fatalf("应写明缺什么: %q", facts.CacheEvidence)
	}
}

// TestPiExtractorSessionStartedCarriesPurposeOnly 说明会话开始只给用途，不造用量。
func TestPiExtractorSessionStartedCarriesPurposeOnly(t *testing.T) {
	ext, _ := ExtractorFor(model.Protocol{Format: "pi-stream-json", Version: "1"})
	facts, ok := ext.Extract(piRecord(t, `{
	  "contract":"maw.pi.execution/v1","event_id":"pe_06","type":"session_started",
	  "run_id":"run_a006","generation":1,
	  "payload":{"mode":"lead","cwd":"/tmp/x","source_ns":"pi"}}`))
	if !ok {
		t.Fatal("session_started 应产出会话事实")
	}
	if facts.Purpose != PurposeLead {
		t.Fatalf("用途=%q 期望 lead", facts.Purpose)
	}
	if facts.HasUsage() {
		t.Fatal("会话开始没有用量")
	}
	if facts.HasTurn() {
		t.Fatal("会话开始不是一轮调用")
	}
}

// TestPiExtractorIgnoresForeignEvents 说明契约不符或不承载观测的事件不产事实：
// 不认识就不假装理解（result 的用量由 usage 事件承载，不能记两次）。
func TestPiExtractorIgnoresForeignEvents(t *testing.T) {
	ext, _ := ExtractorFor(model.Protocol{Format: "pi-stream-json", Version: "1"})
	for _, payload := range []string{
		`{"contract":"maw.pi.execution/v1","type":"result","run_id":"r","generation":1,"payload":{"ok":true}}`,
		`{"contract":"other/v1","type":"usage","run_id":"r","generation":1,"payload":{}}`,
		`not json`,
	} {
		if _, ok := ext.Extract(piRecord(t, payload)); ok {
			t.Fatalf("不该产出事实: %s", payload)
		}
	}
}

// TestExtractorForOnlyKnownProtocols 说明按协议选解释器：不认识就不解释。
func TestExtractorForOnlyKnownProtocols(t *testing.T) {
	if _, ok := ExtractorFor(model.Protocol{Format: "pi-stream-json", Version: "2"}); ok {
		t.Fatal("版本对不上不该解释")
	}
	cb, ok := ExtractorFor(model.Protocol{Format: "stream-json", Version: "1"})
	if !ok {
		t.Fatal("CodeBuddy 实测协议应能选到解释器")
	}
	if cb.SourceNS() != CodeBuddySourceNS {
		t.Fatalf("source ns=%q", cb.SourceNS())
	}
	if _, ok := ExtractorFor(model.Protocol{Format: "whatever", Version: "9"}); ok {
		t.Fatal("未知协议不该解释")
	}
}

// TestCodeBuddyExtractorGivesRunLevelUsage 说明旧 CLI 只给运行级汇总：
// usage 只从 result 记录解析，缓存归一有实测证据。
func TestCodeBuddyExtractorGivesRunLevelUsage(t *testing.T) {
	ext, _ := ExtractorFor(model.Protocol{Format: "stream-json", Version: "1"})
	rec := Record{
		Payload:    []byte(`{"type":"result","subtype":"success","session_id":"sess-1","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3}}`),
		Seq:        1,
		Line:       1,
		ReceivedAt: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC),
	}
	facts, ok := ext.Extract(rec)
	if !ok {
		t.Fatal("result 记录应给出事实")
	}
	if facts.Scope != ScopeRun {
		t.Fatalf("旧 CLI 是运行级事实，得到 %q", facts.Scope)
	}
	if facts.CallID != "" {
		t.Fatal("运行级事实不该有调用身份")
	}
	if !facts.HasUsage() || *facts.InputTokens != 10 {
		t.Fatalf("用量解析错误: %+v", facts)
	}
	if facts.CacheState() != CacheVerified {
		t.Fatalf("C18 实测过缓存分解，应为 verified，得到 %q", facts.CacheState())
	}
	if facts.Raw == nil {
		t.Fatal("原始字段应保留")
	}
}
