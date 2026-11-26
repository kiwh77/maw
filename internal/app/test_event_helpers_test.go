package app

import (
	"database/sql"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

func countEventsByType(t *testing.T, f *fixture, kind string) int {
	t.Helper()
	var n int
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ? AND type = ?`,
			f.pc.ID(), kind).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}
