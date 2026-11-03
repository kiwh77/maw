package systemagent

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

func TestEnqueueDispatchReadyKind(t *testing.T) {
	dir := t.TempDir()
	store, err := db.OpenStore(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}

	// 新库迁移后的表定义必须放行 dispatch_ready。
	var ddl string
	if err := store.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='system_jobs'`).Scan(&ddl)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "dispatch_ready") {
		t.Fatalf("system_jobs CHECK 未包含 dispatch_ready: %s", ddl)
	}

	cfg := config.SystemAgent{MaxAttempts: 3}
	var job *db.SystemJob
	err = store.Write(func(tx *sql.Tx) error {
		var e error
		job, e = Enqueue(tx, cfg, "0123456789abcdef", SystemKindDispatchReady, "", "", "有 ready 题待派发", map[string]any{}, true)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if job == nil || job.Kind != SystemKindDispatchReady || job.Status != db.SystemJobQueued {
		t.Fatalf("unexpected job: %+v", job)
	}

	err = store.Write(func(tx *sql.Tx) error {
		_, e := Enqueue(tx, cfg, "0123456789abcdef", "not_a_kind", "", "", "", map[string]any{}, true)
		return e
	})
	if err == nil || !strings.Contains(err.Error(), "未允许") {
		t.Fatalf("非法 kind 应被拒绝，got %v", err)
	}
}
