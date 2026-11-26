package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

func TestCleanupHistoryDryRunExports(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("T-OLD", "done", "", []string{"a/**"})
	_ = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return db.AppendEvent(tx, f.pc.ID(), "agent-x", "status_changed", task.ID, map[string]any{
			"from": "in_progress", "to": "blocked", "reason": "write_scope too narrow",
		})
	})
	_ = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		job := &db.SystemJob{
			JobID: "sjob_hist_test", ProjectID: f.pc.ID(), Kind: "unblock_assist",
			Status: db.SystemJobFailed, TaskID: task.ID, Reason: "lease expired",
			Decision: map[string]any{"signal_key": "x"}, MaxAttempts: 3,
			CreatedAt: "2020-01-01T00:00:00Z", UpdatedAt: "2020-01-01T00:00:00Z",
		}
		_, err := db.InsertSystemJob(tx, job)
		return err
	})

	res, err := CleanupHistory(f.repo, CleanupHistoryOptions{DryRun: true, OlderThanHours: 1})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if res["dry_run"] != true {
		t.Fatalf("expected dry_run: %#v", res)
	}
	path, _ := res["export_path"].(string)
	if path == "" {
		t.Fatal("missing export_path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("export missing: %v", err)
	}
	if n, _ := res["blocked_events"].(int); n < 1 {
		// JSON numbers may be float64
		if nf, ok := res["blocked_events"].(float64); !ok || nf < 1 {
			if ni, ok := res["blocked_events"].(int); !ok || ni < 1 {
				t.Fatalf("blocked_events=%v", res["blocked_events"])
			}
		}
	}

	res2, err := CleanupHistory(f.repo, CleanupHistoryOptions{Yes: true, OlderThanHours: 1})
	if err != nil {
		t.Fatalf("cleanup yes: %v", err)
	}
	if res2["dry_run"] != false {
		t.Fatalf("expected write: %#v", res2)
	}
	_ = filepath.Dir(path)
	_ = time.Now()
}
