package app

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

func TestBoardSnapshotTracksEventOnlyChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maw.db")
	if err := db.Init(path); err != nil {
		t.Fatal(err)
	}
	before, err := loadBoardSnapshot(path, "", "", "", 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(path, func(tx *sql.Tx) error {
		return db.AppendEvent(tx, "project", "w1", "progress", "T-1", map[string]any{"message": "milestone"})
	}); err != nil {
		t.Fatal(err)
	}
	after, err := loadBoardSnapshot(path, "", "", "", 4)
	if err != nil {
		t.Fatal(err)
	}
	if after.EventID <= before.EventID || after.Total != before.Total {
		t.Fatal("event-only writes must invalidate snapshots without changing task totals")
	}
}
