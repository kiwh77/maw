package dbstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/runner/observe"
)

// newObserveStore 起一个干净的小库（迁移 + 一个工程）。
func newObserveStore(t *testing.T) (*db.Store, *Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "maw.db")
	if err := db.Init(path); err != nil {
		t.Fatal(err)
	}
	st, err := db.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Write(func(tx *sql.Tx) error {
		return db.UpsertProject(tx, &db.Project{ID: "p1", Name: "proj", Path: t.TempDir(), MainBranch: "main"})
	}); err != nil {
		t.Fatal(err)
	}
	return st, &Store{DB: st, ProjectID: "p1"}
}

// countRows 数一数某张表里有多少行。
func countRows(t *testing.T, st *db.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := st.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(query, args...).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestWriteFactsRecordsSessionTurnLinkAndUsage 是 GAP-005 的正向验收：
// 一次观测要落成来源会话、轮次、run 关联与用量记账。
func TestWriteFactsRecordsSessionTurnLinkAndUsage(t *testing.T) {
	st, s := newObserveStore(t)
	in, out := int64(120), int64(34)
	total := in + out
	facts := observe.Facts{
		RunID:        "run_1",
		InstanceID:   "ti_1",
		SourceNS:     "codebuddy",
		SessionID:    "sess-1",
		RecordType:   "result",
		RecordRef:    "3:3",
		Semantic:     "delta",
		Scope:        "run",
		InputTokens:  &in,
		OutputTokens: &out,
		TotalTokens:  &total,
		Quality:      "observed",
		Evidence:     "test",
	}
	if err := s.WriteFacts(context.Background(), facts); err != nil {
		t.Fatalf("WriteFacts: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM source_sessions WHERE project_id = ?`, "p1"); n != 1 {
		t.Fatalf("source_sessions=%d 期望 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM source_turns`); n != 1 {
		t.Fatalf("source_turns=%d 期望 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM run_source_links WHERE run_id = ?`, "run_1"); n != 1 {
		t.Fatalf("run_source_links=%d 期望 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM usage_records WHERE source_ns = ?`, "codebuddy"); n != 1 {
		t.Fatalf("usage_records=%d 期望 1", n)
	}

	// 重放同一条观测：不重复计数，也不报错。
	if err := s.WriteFacts(context.Background(), facts); err != nil {
		t.Fatalf("重放 WriteFacts: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM usage_records WHERE source_ns = ?`, "codebuddy"); n != 1 {
		t.Fatalf("重放后 usage_records=%d 期望仍为 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM run_source_links WHERE run_id = ?`, "run_1"); n != 1 {
		t.Fatalf("重放后 run_source_links=%d 期望仍为 1", n)
	}
}

// TestWriteFactsWithoutUsageWritesNoUsageRecord 是 GAP-005 的反向验收：
// 来源没给用量就不写用量记录——0 Token 冒充观测比没有观测更糟。
func TestWriteFactsWithoutUsageWritesNoUsageRecord(t *testing.T) {
	st, s := newObserveStore(t)
	facts := observe.Facts{
		RunID:      "run_2",
		InstanceID: "ti_2",
		SourceNS:   "codebuddy",
		SessionID:  "sess-2",
		RecordType: "result",
		RecordRef:  "4:4",
		Scope:      "run",
		Quality:    "unknown",
	}
	if err := s.WriteFacts(context.Background(), facts); err != nil {
		t.Fatalf("WriteFacts: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM usage_records WHERE source_ns = ?`, "codebuddy"); n != 0 {
		t.Fatalf("来源没给用量却写了 %d 条用量记录", n)
	}
	// 会话、轮次与关联仍然要有：没用量不等于没观测。
	if n := countRows(t, st, `SELECT COUNT(*) FROM source_sessions WHERE project_id = ?`, "p1"); n != 1 {
		t.Fatalf("source_sessions=%d 期望 1", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM run_source_links WHERE run_id = ?`, "run_2"); n != 1 {
		t.Fatalf("run_source_links=%d 期望 1", n)
	}
}

// TestWriteFactsWithoutSessionIdentityWritesNothing 说明没有来源身份就不落库：
// 调用方保持未归属，而不是被塞进一个假会话。
func TestWriteFactsWithoutSessionIdentityWritesNothing(t *testing.T) {
	st, s := newObserveStore(t)
	facts := observe.Facts{
		RunID:      "run_3",
		InstanceID: "ti_3",
		SourceNS:   "codebuddy",
		SessionID:  "  ",
		RecordType: "result",
		RecordRef:  "5:5",
		Scope:      "run",
	}
	if err := s.WriteFacts(context.Background(), facts); err != nil {
		t.Fatalf("WriteFacts: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM source_sessions`); n != 0 {
		t.Fatalf("没有来源身份却写了 %d 条会话", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM run_source_links`); n != 0 {
		t.Fatalf("没有来源身份却写了 %d 条关联", n)
	}
}

// TestWriteFactsPiCallAttribution 说明 Pi 的逐次调用落到用量行时带得住归属：
// 项目、节点、Run、代际、会话、调用、用途、供应商模型、缓存归一与原始字段。
func TestWriteFactsPiCallAttribution(t *testing.T) {
	st, s := newObserveStore(t)
	in, out := int64(900), int64(120)
	cacheRead := int64(400)
	facts := observe.Facts{
		RunID:           "run_pi",
		InstanceID:      "ti_pi",
		ProjectID:       "p1",
		NodeID:          "node_pi",
		Generation:      2,
		Purpose:         observe.PurposeWorker,
		SourceNS:        observe.PiSourceNS,
		SessionID:       "run:run_pi",
		RecordUUID:      "call:pc_1",
		RecordType:      "usage",
		CallID:          "pc_1",
		OccurredAt:      time.Date(2026, 11, 18, 2, 41, 9, 0, time.UTC),
		Semantic:        "delta",
		Scope:           observe.ScopeCall,
		InputTokens:     &in,
		OutputTokens:    &out,
		CacheReadTokens: &cacheRead,
		Quality:         "observed",
		Provider:        "anthropic",
		Model:           "claude-x",
		CacheEvidence:   "runtime:C30",
		Evidence:        "runtime",
		Raw:             map[string]any{"type": "usage", "payload": map[string]any{"scope": "call"}},
	}
	included := true
	facts.CacheIncludedInInput = &included
	if err := s.WriteFacts(context.Background(), facts); err != nil {
		t.Fatalf("WriteFacts: %v", err)
	}
	if err := st.Read(func(tx *sql.Tx) error {
		rows, err := db.ListUsageFacts(tx, db.UsageQuery{RunID: "run_pi", CallID: "pc_1"})
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatalf("应写入 1 行调用用量，得到 %d", len(rows))
		}
		u := rows[0]
		if u.Scope != db.UsageScopeCall {
			t.Fatalf("口径应为 call，得到 %q", u.Scope)
		}
		if u.ProjectID != "p1" || u.NodeID != "node_pi" || u.Generation != 2 ||
			u.SessionID != "pi:run:run_pi" || u.Purpose != "worker" {
			t.Fatalf("归属字段丢失: %+v", u)
		}
		if u.Model != "claude-x" || u.Provider != "anthropic" {
			t.Fatalf("供应商模型丢失: %q/%q", u.Provider, u.Model)
		}
		if u.CacheState != db.CacheStateVerified {
			t.Fatalf("缓存归一应为 verified，得到 %q", u.CacheState)
		}
		if u.TotalTokens == nil || *u.TotalTokens != in+out {
			t.Fatalf("total 应为 input+output（不含缓存），得到 %v", u.TotalTokens)
		}
		if u.Raw["type"] != "usage" {
			t.Fatalf("原始字段应保留: %+v", u.Raw)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 重放同一次调用：身份由 call 派生，不会多记一次。
	if err := s.WriteFacts(context.Background(), facts); err != nil {
		t.Fatalf("重放 WriteFacts: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM usage_records WHERE run_id = ?`, "run_pi"); n != 1 {
		t.Fatalf("重放后用量行=%d 期望仍为 1", n)
	}
}

// TestWriteFactsCallSupersedesRunBaseline 说明运行基线与调用事实分开存：
// 基线标记 baseline，call 级证据到达后 run 级标 superseded 保留，汇总只取 call 级。
func TestWriteFactsCallSupersedesRunBaseline(t *testing.T) {
	st, s := newObserveStore(t)
	// 先落运行基线：没有任何可用量。
	if err := s.WriteFacts(context.Background(), observe.Facts{
		RunID:      "run_bl",
		InstanceID: "ti_bl",
		ProjectID:  "p1",
		Generation: 1,
		SourceNS:   observe.PiSourceNS,
		SessionID:  "run:run_bl",
		RecordUUID: "run:run_bl",
		RecordType: "result",
		OccurredAt: time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC),
		Scope:      observe.ScopeRun,
		Baseline:   true,
		Quality:    "unknown",
	}); err != nil {
		t.Fatal(err)
	}
	// 再落一次真实调用。
	in, out := int64(50), int64(10)
	if err := s.WriteFacts(context.Background(), observe.Facts{
		RunID:        "run_bl",
		InstanceID:   "ti_bl",
		ProjectID:    "p1",
		Generation:   1,
		SourceNS:     observe.PiSourceNS,
		SessionID:    "run:run_bl",
		RecordUUID:   "call:pc_bl",
		RecordType:   "usage",
		CallID:       "pc_bl",
		OccurredAt:   time.Date(2026, 11, 18, 1, 1, 0, 0, time.UTC),
		Semantic:     "delta",
		Scope:        observe.ScopeCall,
		InputTokens:  &in,
		OutputTokens: &out,
		Quality:      "observed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Read(func(tx *sql.Tx) error {
		sum, err := db.SummarizeRunUsage(tx, "run_bl", 1)
		if err != nil {
			return err
		}
		if sum.Scope != db.UsageScopeCall {
			t.Fatalf("应采用 call 口径，得到 %q", sum.Scope)
		}
		if sum.InputTokens == nil || *sum.InputTokens != 50 {
			t.Fatalf("汇总应只算 call 级，得到 %+v", sum.InputTokens)
		}
		if sum.SupersededRunRows != 0 {
			// 基线本身没有用量行（unknown 不写用量），因此没有被取代的行。
			t.Fatalf("没有 run 级用量行，不该有取代计数: %d", sum.SupersededRunRows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestWriteFactsNormalizesUnknownUsage 说明 writer 走归一：
// quality=unknown 却带了部分 token 时落库为 null（原始值留在 raw），不是 0。
func TestWriteFactsNormalizesUnknownUsage(t *testing.T) {
	st, s := newObserveStore(t)
	in := int64(9)
	if err := s.WriteFacts(context.Background(), observe.Facts{
		RunID:       "run_unk",
		InstanceID:  "ti_unk",
		ProjectID:   "p1",
		Generation:  1,
		SourceNS:    observe.PiSourceNS,
		SessionID:   "run:run_unk",
		RecordUUID:  "call:pc_unk",
		RecordType:  "usage",
		CallID:      "pc_unk",
		OccurredAt:  time.Now().UTC(),
		Semantic:    "delta",
		Scope:       observe.ScopeCall,
		InputTokens: &in,
		Quality:     "unknown",
		Evidence:    "来源只给了 input；output 缺失",
		Raw:         map[string]any{"input_tokens": 9},
	}); err != nil {
		t.Fatalf("WriteFacts: %v", err)
	}
	if err := st.Read(func(tx *sql.Tx) error {
		rows, err := db.ListUsageFacts(tx, db.UsageQuery{RunID: "run_unk"})
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatalf("应写 1 行未知用量，得到 %d", len(rows))
		}
		u := rows[0]
		if u.InputTokens != nil || u.OutputTokens != nil || u.TotalTokens != nil {
			t.Fatalf("未知用量必须保持 null（不是 0）: %+v", u)
		}
		if u.Raw["input_tokens"] == nil {
			t.Fatalf("原始值应保留在 raw: %+v", u.Raw)
		}
		if u.Basis == "" {
			t.Fatal("应写明为什么是未知")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
