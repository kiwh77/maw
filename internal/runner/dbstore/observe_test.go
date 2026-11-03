package dbstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

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
