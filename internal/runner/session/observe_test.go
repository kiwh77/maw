package session

import (
	"testing"

	"github.com/kiwh77/maw/internal/adapters/codebuddy"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/dbstore"
	"github.com/kiwh77/maw/internal/runner/journal"
)

// codeBuddyRunner 造一个用 CodeBuddy adapter 做来源的 Runner。
func codeBuddyRunner(t *testing.T) *Runner {
	t.Helper()
	ad, err := codebuddy.New(model.NodeConfig{
		NodeID:    "node_codebuddy",
		Name:      "codebuddy",
		Adapter:   model.AdapterCodeBuddy,
		EntryPath: "/usr/bin/env",
		Platform:  "darwin",
		Mode:      model.NodeManagedCLI,
		SlotLimit: 1,
		AcceptNew: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &Runner{source: ad}
}

// TestFactsOfReadsUsageOnlyFromResultRecords 说明用量只从 result 记录解析：
// init 记录给的是会话身份，不是用量。
func TestFactsOfReadsUsageOnlyFromResultRecords(t *testing.T) {
	r := codeBuddyRunner(t)
	cfg := Config{RunID: "run_1", Instance: "ti_1"}

	initFacts, ok := r.factsOf(cfg, journal.Record{
		Payload: []byte(`{"type":"system","subtype":"init","session_id":"sess-1"}`),
	})
	if !ok {
		t.Fatal("init 记录应给出会话身份")
	}
	if initFacts.SessionID != "sess-1" {
		t.Fatalf("session_id=%q", initFacts.SessionID)
	}
	if initFacts.HasUsage() {
		t.Fatal("init 记录没有用量，不该凭空造出 token")
	}
	if initFacts.HasTurn() {
		t.Fatal("init 记录不是一轮对话，不该造出轮次")
	}

	resultFacts, ok := r.factsOf(cfg, journal.Record{
		Payload: []byte(`{"type":"result","subtype":"success","session_id":"sess-1","usage":{"input_tokens":10,"output_tokens":5}}`),
	})
	if !ok {
		t.Fatal("result 记录应给出事实")
	}
	if !resultFacts.HasUsage() {
		t.Fatal("result 记录带 usage，应解析出用量")
	}
	if resultFacts.InputTokens == nil || *resultFacts.InputTokens != 10 {
		t.Fatalf("input_tokens=%v", resultFacts.InputTokens)
	}
	if resultFacts.OutputTokens == nil || *resultFacts.OutputTokens != 5 {
		t.Fatalf("output_tokens=%v", resultFacts.OutputTokens)
	}
	if resultFacts.Semantic != "delta" {
		t.Fatalf("semantic=%q 期望 delta", resultFacts.Semantic)
	}
	if !resultFacts.HasTurn() {
		t.Fatal("result 记录是一轮对话")
	}
}

// TestFactsOfWithoutSessionIdentityHasNoFacts 说明没有来源会话身份就没有事实：
// 调用方保持未归属。
func TestFactsOfWithoutSessionIdentityHasNoFacts(t *testing.T) {
	r := codeBuddyRunner(t)
	cfg := Config{RunID: "run_2", Instance: "ti_2"}
	if _, ok := r.factsOf(cfg, journal.Record{
		Payload: []byte(`{"type":"result","subtype":"success","usage":{"input_tokens":1,"output_tokens":1}}`),
	}); ok {
		t.Fatal("没有 session_id 就不该有事实")
	}
}

// TestFactsOfPassthroughSourceHasNoUsageFacts 说明回退来源没有可解释的计量字段：
// passthrough 只按退出码判定，不假装理解来源语义。
func TestFactsOfPassthroughSourceHasNoUsageFacts(t *testing.T) {
	r := &Runner{source: dbstore.PassthroughSource{}}
	cfg := Config{RunID: "run_3", Instance: "ti_3"}
	if _, ok := r.factsOf(cfg, journal.Record{
		Payload: []byte(`{"type":"result","subtype":"success","session_id":"sess-3","usage":{"input_tokens":1,"output_tokens":1}}`),
	}); ok {
		t.Fatal("回退来源不该产出观测事实")
	}
}
