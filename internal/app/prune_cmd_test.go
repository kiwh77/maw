package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

func TestPruneRemovesStaleIdleWorktree(t *testing.T) {
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

	// 登记为 idle，但 last_seen 过期 → 应可 prune
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return bindAgent(tx, f.pc.ID(), agentID, "worker", "idle", "", "", "", "")
	})
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339)
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, e := tx.Exec(`UPDATE agents SET last_seen=? WHERE project_id=? AND id=?`, stale, f.pc.ID(), agentID)
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
