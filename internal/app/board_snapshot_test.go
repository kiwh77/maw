package app

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
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

func TestBuildBoardHubWiresAuthAndAutoOff(t *testing.T) {
	t.Setenv("MAW_GLOBAL", t.TempDir())
	gdir, err := home.GlobalDir()
	if err != nil {
		t.Fatal(err)
	}
	gdb := filepath.Join(gdir, "maw.db")
	if err := db.Init(gdb); err != nil {
		t.Fatal(err)
	}
	opts, err := buildBoardHub(t.TempDir(), gdb, gdir, BoardFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Auth == nil || opts.Auth.Tokens == nil || opts.Auth.Sessions == nil {
		t.Fatal("hub must wire shared auth for legacy and v1 writes")
	}
	if opts.V1 == nil {
		t.Fatal("v1 API must be mounted")
	}
	view := opts.Runtime()
	if view["enabled"] != false {
		t.Fatalf("auto_execution must default off: %+v", view)
	}
}
