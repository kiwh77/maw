package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/adapters/codebuddy"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/dbstore"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/observe"
)

// codeBuddyProtocol 是 CodeBuddy 实测过的协议。
var codeBuddyProtocol = model.Protocol{Format: "stream-json", Version: "1"}

// piProtocol 是 Pi 运行时冻结的协议。
var piProtocol = model.Protocol{Format: "pi-stream-json", Version: "1"}

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
	cfg := Config{RunID: "run_1", Instance: "ti_1", Protocol: codeBuddyProtocol}

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
	if resultFacts.Scope != observe.ScopeRun {
		t.Fatalf("旧 CLI 只给运行级用量，得到 %q", resultFacts.Scope)
	}
	if !resultFacts.HasTurn() {
		t.Fatal("result 记录是一轮对话")
	}
}

// TestFactsOfBindsAttributionFromConfig 说明归属由 maw 绑定：
// 项目、节点、Run、代际与用途来自 Runner 的配置，不是来源或模型自述。
func TestFactsOfBindsAttributionFromConfig(t *testing.T) {
	r := &Runner{source: dbstore.PassthroughSource{}}
	cfg := Config{
		RunID:      "run_bind",
		Instance:   "ti_bind",
		Generation: 3,
		Protocol:   codeBuddyProtocol,
		ProjectID:  "p_bind",
		NodeID:     "node_bind",
		Purpose:    observe.PurposeLead,
	}
	facts, ok := r.factsOf(cfg, journal.Record{
		Seq:        9,
		Line:       2,
		ReceivedAt: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC),
		Payload:    []byte(`{"type":"result","subtype":"success","session_id":"sess-b","usage":{"input_tokens":3,"output_tokens":4}}`),
	})
	if !ok {
		t.Fatal("应产出事实")
	}
	if facts.ProjectID != "p_bind" || facts.NodeID != "node_bind" {
		t.Fatalf("项目／节点归属=%q/%q", facts.ProjectID, facts.NodeID)
	}
	if facts.RunID != "run_bind" || facts.InstanceID != "ti_bind" || facts.Generation != 3 {
		t.Fatalf("Run 归属=%q/%q g=%d", facts.RunID, facts.InstanceID, facts.Generation)
	}
	if facts.Purpose != observe.PurposeLead {
		t.Fatalf("用途=%q", facts.Purpose)
	}
	if facts.RecordRef != "9:2" {
		t.Fatalf("记录位置=%q", facts.RecordRef)
	}
}

// TestFactsOfWithoutSessionIdentityHasNoFacts 说明没有来源会话身份就没有事实：
// 调用方保持未归属。
func TestFactsOfWithoutSessionIdentityHasNoFacts(t *testing.T) {
	r := codeBuddyRunner(t)
	cfg := Config{RunID: "run_2", Instance: "ti_2", Protocol: codeBuddyProtocol}
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

// TestFactsOfPiCallUsage 说明 Pi 的 usage 事件按逐次调用观测，并带上 maw 侧归属。
func TestFactsOfPiCallUsage(t *testing.T) {
	r := &Runner{}
	cfg := Config{
		RunID:      "run_pi",
		Instance:   "ti_pi",
		Generation: 2,
		Protocol:   piProtocol,
		ProjectID:  "p_pi",
		Purpose:    observe.PurposeWorker,
	}
	facts, ok := r.factsOf(cfg, journal.Record{
		Seq:        4,
		Line:       1,
		ReceivedAt: time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC),
		Payload: []byte(`{"contract":"maw.pi.execution/v1","event_id":"pe_9","type":"usage",
		  "run_id":"run_pi","generation":2,"call_id":"pc_9",
		  "payload":{"scope":"call","semantic":"delta","input_tokens":7,"output_tokens":3,
		    "quality":"observed"}}`),
	})
	if !ok {
		t.Fatal("Pi usage 事件应产出事实")
	}
	if facts.Scope != observe.ScopeCall || facts.CallID != "pc_9" {
		t.Fatalf("调用归属=%q/%q", facts.Scope, facts.CallID)
	}
	if facts.Generation != 2 || facts.ProjectID != "p_pi" {
		t.Fatalf("代际／项目=%d/%q", facts.Generation, facts.ProjectID)
	}
}

// fakeObserver 记录写库请求，并可指定前几次失败（模拟数据库短暂不可用）。
type fakeObserver struct {
	writes  []observe.Facts
	failFor int
	calls   int
}

func (f *fakeObserver) WriteFacts(_ context.Context, facts observe.Facts) error {
	f.calls++
	if f.calls <= f.failFor {
		return errors.New("db: database is locked")
	}
	f.writes = append(f.writes, facts)
	return nil
}

// TestObserveFactsCountsFailureAndKeepsBacklog 说明写库失败不丢事实：
// 失败要计数，位置要记下来，供事后补录。
func TestObserveFactsCountsFailureAndKeepsBacklog(t *testing.T) {
	obs := &fakeObserver{failFor: 1}
	r := &Runner{observer: obs}
	cfg := Config{RunID: "run_f", Instance: "ti_f", Protocol: codeBuddyProtocol}
	rec := journal.Record{
		Seq:        11,
		Line:       1,
		ReceivedAt: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC),
		Payload:    []byte(`{"type":"result","subtype":"success","session_id":"s1","usage":{"input_tokens":2,"output_tokens":1}}`),
	}
	r.observeFacts(context.Background(), cfg, rec)
	if got := r.observationFailures.Load(); got != 1 {
		t.Fatalf("失败计数=%d 期望 1", got)
	}
	if backlog := r.observationBacklogSeqs(); len(backlog) != 1 || backlog[0] != 11 {
		t.Fatalf("待补录位置=%v 期望 [11]", backlog)
	}
	if len(obs.writes) != 0 {
		t.Fatal("失败时不该当成写成功")
	}
}

// TestBackfillObservationsFillsGapWithoutDoubleCounting 说明补录独立可用：
// 重放 journal 原始记录即可补上缺口，且同一条记录不会记两次。
func TestBackfillObservationsFillsGapWithoutDoubleCounting(t *testing.T) {
	jr := newJournal(t, "run_bf")
	payloads := []string{
		`{"type":"system","subtype":"init","session_id":"sess-bf"}`,
		`{"type":"result","subtype":"success","session_id":"sess-bf","uuid":"rec-1","usage":{"input_tokens":10,"output_tokens":5}}`,
	}
	for _, p := range payloads {
		if _, err := jr.Append(journal.Record{
			Kind:    kindStreamRecord,
			Source:  journal.Source{Namespace: "codebuddy", ParseVersion: "1", Path: "/x"},
			Payload: []byte(p),
		}); err != nil {
			t.Fatal(err)
		}
	}
	obs := &fakeObserver{failFor: 2}
	r := &Runner{journal: jr, observer: obs}
	cfg := Config{RunID: "run_bf", Instance: "ti_bf", Protocol: codeBuddyProtocol}

	// 首轮摄取：两次写库都失败（数据库短暂不可用）。
	for _, rec := range jr.Records() {
		r.observeFacts(context.Background(), cfg, rec)
	}
	if got := r.observationFailures.Load(); got == 0 {
		t.Fatal("应记下写库失败")
	}
	if len(r.observationBacklogSeqs()) == 0 {
		t.Fatal("应留下待补录位置")
	}

	// 补录：重放同一批记录，写入仍未重复时的身份由来源身份派生。
	rep, err := r.BackfillObservations(context.Background(), cfg)
	if err != nil {
		t.Fatalf("补录失败: %v", err)
	}
	if rep.Matched == 0 || rep.Written == 0 || rep.Failed != 0 {
		t.Fatalf("补录结果=%+v", rep)
	}
	if got := len(r.observationBacklogSeqs()); got != 0 {
		t.Fatalf("补录成功后应清空待补录位置，剩 %d", got)
	}
	// 再补一次：同一条记录重放只重复投递同一身份，去重由落库端按身份保证
	// （dbstore 的 usage_id 与 turn_id 都由来源身份派生，ON CONFLICT 不会多一行）。
	identities := func() map[string]bool {
		out := map[string]bool{}
		for _, f := range obs.writes {
			out[f.SourceNS+"|"+f.SessionID+"|"+f.RecordUUID+"|"+f.Scope+"|"+f.CallID] = true
		}
		return out
	}
	first := identities()
	if _, err := r.BackfillObservations(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if second := identities(); len(second) != len(first) {
		t.Fatalf("重放产生了新的记录身份：%d → %d", len(first), len(second))
	}
}

// TestSourceNamespaceFollowsProtocol 说明 journal 命名空间按协议定，不写死厂商名。
func TestSourceNamespaceFollowsProtocol(t *testing.T) {
	if got := sourceNamespace(piProtocol); got != observe.PiSourceNS {
		t.Fatalf("Pi 命名空间=%q", got)
	}
	if got := sourceNamespace(codeBuddyProtocol); got != observe.CodeBuddySourceNS {
		t.Fatalf("CodeBuddy 命名空间=%q", got)
	}
	if got := sourceNamespace(model.Protocol{Format: "mystery", Version: "9"}); got != "mystery" {
		t.Fatalf("未知协议命名空间=%q", got)
	}
	if got := sourceNamespace(model.Protocol{}); got != "unspecified" {
		t.Fatalf("空协议命名空间=%q", got)
	}
}

// TestBackfillObservationsRecordsPiCallFacts 说明 Pi 逐次调用经统一端口入账：
// 重放 journal 里的 usage 事件即可补出带完整归属的调用事实。
func TestBackfillObservationsRecordsPiCallFacts(t *testing.T) {
	jr := newJournal(t, "run_pi_bf")
	payloads := []string{
		`{"contract":"maw.pi.execution/v1","event_id":"pe_1","type":"session_started",
		  "run_id":"run_pi_bf","generation":2,"payload":{"mode":"worker","cwd":"/tmp","source_ns":"pi"}}`,
		`{"contract":"maw.pi.execution/v1","event_id":"pe_2","type":"usage",
		  "run_id":"run_pi_bf","generation":2,"call_id":"pc_1",
		  "payload":{"scope":"call","semantic":"delta","input_tokens":120,"output_tokens":30,
		    "cache_read_tokens":80,"quality":"observed","input_includes_cache":true,
		    "model":"claude-x","provider":"anthropic"}}`,
		`{"contract":"maw.pi.execution/v1","event_id":"pe_3","type":"usage",
		  "run_id":"run_pi_bf","generation":2,"call_id":"pc_2",
		  "payload":{"scope":"call","semantic":"delta","quality":"unknown",
		    "basis":"来源未给出任何 token 字段"}}`,
	}
	for _, p := range payloads {
		if _, err := jr.Append(journal.Record{
			Kind:    kindStreamRecord,
			Source:  journal.Source{Namespace: observe.PiSourceNS, ParseVersion: "1", Path: "/usr/bin/node"},
			Payload: []byte(p),
		}); err != nil {
			t.Fatal(err)
		}
	}
	obs := &fakeObserver{}
	r := &Runner{journal: jr, observer: obs}
	cfg := Config{
		RunID:      "run_pi_bf",
		Instance:   "ti_pi_bf",
		Generation: 2,
		Protocol:   piProtocol,
		ProjectID:  "p_pi",
		NodeID:     "node_pi",
		Purpose:    observe.PurposeWorker,
	}
	rep, err := r.BackfillObservations(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Written != len(payloads) {
		t.Fatalf("三条记录都应入账，实际写入 %d", rep.Written)
	}
	var calls []observe.Facts
	for _, f := range obs.writes {
		if f.IsCall() {
			calls = append(calls, f)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("应有两条调用事实，得到 %d", len(calls))
	}
	first := calls[0]
	if first.CallID != "pc_1" || first.Scope != observe.ScopeCall {
		t.Fatalf("调用身份／口径=%q/%q", first.CallID, first.Scope)
	}
	if first.ProjectID != "p_pi" || first.NodeID != "node_pi" || first.Generation != 2 {
		t.Fatalf("归属=%q/%q/g%d", first.ProjectID, first.NodeID, first.Generation)
	}
	if first.CacheState() != observe.CacheVerified {
		t.Fatalf("缓存归一=%q", first.CacheState())
	}
	if first.Model != "claude-x" || first.Provider != "anthropic" {
		t.Fatalf("供应商模型=%q/%q", first.Provider, first.Model)
	}
	if calls[1].HasUsage() {
		t.Fatal("未知用量的调用不得造出 token")
	}
}
