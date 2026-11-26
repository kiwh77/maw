package app

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

func TestFinalizeTerminalFieldsArchivesBlockedReason(t *testing.T) {
	t.Parallel()
	task := &db.Task{
		ID:            "T-1",
		Status:        "blocked",
		BlockedReason: "out of scope",
		Branch:        "agent/x/T-1",
		Worktree:      ".worktrees/x",
		PrevStatus:    "in_progress",
	}
	finalizeTerminalFields(task)
	if task.BlockedReason != "" {
		t.Fatalf("blocked_reason should clear, got %q", task.BlockedReason)
	}
	if task.Branch != "" || task.Worktree != "" || task.PrevStatus != "" {
		t.Fatalf("branch/worktree/prev should clear: %+v", task)
	}
	hist, ok := task.Extra[extraBlockHistoryKey].([]any)
	if !ok || len(hist) != 1 {
		t.Fatalf("expected block_history entry, got %#v", task.Extra)
	}
}

func TestTaskCancelClearsBlockedReasonAndAudits(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("T-1214", "blocked", "", []string{"docs/**"})
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		task.BlockedReason = "need layer remap"
		task.Branch = "agent/a/T-1214"
		task.Worktree = ".worktrees/a"
		return db.UpsertTask(tx, task)
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := TaskCancel(f.repo, "T-1214", CancelOptions{
		Reason: "removed_from_plan", SupersededBy: "T-1205",
	})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if res["status"] != "cancelled" {
		t.Fatalf("status=%v", res["status"])
	}

	got := f.task("T-1214")
	if got.Status != "cancelled" {
		t.Fatalf("status=%s", got.Status)
	}
	if got.BlockedReason != "" {
		t.Fatalf("blocked_reason residual: %q", got.BlockedReason)
	}
	if got.Extra[extraCancelReasonKey] != "removed_from_plan" {
		t.Fatalf("cancel_reason=%v", got.Extra[extraCancelReasonKey])
	}
	if got.Extra[extraSupersededByKey] != "T-1205" {
		t.Fatalf("superseded_by=%v", got.Extra[extraSupersededByKey])
	}

	var types []string
	_ = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT type FROM events WHERE project_id=? AND task_id=? ORDER BY id`, f.pc.ID(), "T-1214")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var typ string
			if err := rows.Scan(&typ); err != nil {
				return err
			}
			types = append(types, typ)
		}
		return nil
	})
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "status_changed") || !strings.Contains(joined, "task_cancelled") {
		t.Fatalf("missing audit events: %v", types)
	}
}

func TestAgentWorktreeKeepReason(t *testing.T) {
	t.Parallel()
	fresh := &db.Agent{ID: "cursor-demo-1", Status: "idle", LastSeen: "2026-11-16T11:00:00Z"}
	stale := &db.Agent{ID: "cursor-demo-2", Status: "idle", LastSeen: "2026-11-10T11:00:00Z"}
	working := &db.Agent{ID: "cursor-demo-3", Status: "working", LastSeen: "2026-11-10T11:00:00Z"}
	offline := &db.Agent{ID: "cursor-demo-4", Status: "offline", LastSeen: "2026-11-16T11:00:00Z"}

	if _, keep := agentWorktreeKeepReason(fresh); keep {
		t.Fatal("idle should prune")
	}
	if _, keep := agentWorktreeKeepReason(stale); keep {
		t.Fatal("idle should prune")
	}
	if _, keep := agentWorktreeKeepReason(working); !keep {
		t.Fatal("working should keep")
	}
	if _, keep := agentWorktreeKeepReason(offline); keep {
		t.Fatal("offline should prune")
	}
}
