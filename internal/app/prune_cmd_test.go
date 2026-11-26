package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

func TestPruneRemovesIdleWorktree(t *testing.T) {
	f := newFixture(t)
	agentID := "cursor-demo-9"
	wt := filepath.Join(f.repo, ".worktrees", agentID)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "TASK.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt, ".maw"), 0o755); err != nil {
		t.Fatal(err)
	}

	// idle（即使 last_seen 很新）→ 应可 prune
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return bindAgent(tx, f.pc.ID(), agentID, "worker", "idle", "", "", "", "")
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh := time.Now().UTC().Format(time.RFC3339)
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, e := tx.Exec(`UPDATE agents SET last_seen=? WHERE project_id=? AND id=?`, fresh, f.pc.ID(), agentID)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := Prune(f.repo, true, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	removed, _ := res["removed_worktrees"].([]string)
	found := false
	want, _ := filepath.EvalSymlinks(wt)
	for _, p := range removed {
		got, _ := filepath.EvalSymlinks(p)
		if got == want || filepath.Base(p) == agentID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected remove %s, got %#v (kept_reasons=%v)", wt, res["removed_worktrees"], res["kept_reasons"])
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still on disk: %v", err)
	}
}

func TestPruneKeepsWorkingWorktree(t *testing.T) {
	f := newFixture(t)
	agentID := "cursor-demo-10"
	wt := filepath.Join(f.repo, ".worktrees", agentID)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return bindAgent(tx, f.pc.ID(), agentID, "worker", "working", "T-1", "", "", "")
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := Prune(f.repo, true, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	removed, _ := res["removed_worktrees"].([]string)
	for _, p := range removed {
		if filepath.Base(p) == agentID {
			t.Fatalf("working agent worktree should be kept, removed=%v kept=%v", removed, res["kept_reasons"])
		}
	}
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		t.Fatalf("working worktree should remain: %v", err)
	}
}

// TestPruneKeepsBlockedUnmergedTask 覆盖：blocked 题仍挂 branch/worktree 时不得 prune 删掉。
func TestPruneKeepsBlockedUnmergedTask(t *testing.T) {
	f := newFixture(t)
	agentID := "cursor-demo-11"
	task := f.seedTask("RD-PRUNE", "blocked", "", []string{"mod_prune/**"})
	wt := filepath.Join(f.repo, ".worktrees", agentID)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	branch := "agent/" + agentID + "/RD-PRUNE"
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		got, err := db.GetTask(tx, f.pc.ID(), task.ID)
		if err != nil || got == nil {
			return err
		}
		got.Branch = branch
		got.Worktree = ".worktrees/" + agentID
		got.BlockedReason = "agent_expired:" + agentID
		got.PrevStatus = "merging"
		return db.UpsertTask(tx, got)
	}); err != nil {
		t.Fatalf("seed blocked: %v", err)
	}

	res, err := Prune(f.repo, true, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	removed, _ := res["removed_worktrees"].([]string)
	for _, p := range removed {
		if filepath.Base(p) == agentID {
			t.Fatalf("blocked unmerged worktree must be kept, removed=%v kept=%v", removed, res["kept_reasons"])
		}
	}
	brRemoved, _ := res["removed_branches"].([]string)
	for _, b := range brRemoved {
		if b == branch {
			t.Fatalf("blocked unmerged branch must be kept, removed=%v", brRemoved)
		}
	}
}
