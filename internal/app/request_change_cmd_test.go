package app

import (
	"database/sql"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

func TestRequestChangeExpandsWriteScope(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("T-RC1", "in_progress", "cursor-demo-1", []string{"mod_a/**"})
	_ = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return bindAgent(tx, f.pc.ID(), "cursor-demo-1", "worker", "working", task.ID, "", "", "")
	})
	t.Setenv("MAW_AGENT", "cursor-demo-1")

	res, err := RequestChange(f.repo, RequestChangeOptions{
		TaskID:  "T-RC1",
		Add:     []string{"backend/internal/bootstrap/**"},
		Message: "need wire",
	})
	if err != nil {
		t.Fatalf("request-change: %v", err)
	}
	scope, _ := res["write_scope"].([]string)
	found := false
	for _, p := range scope {
		if p == "backend/internal/bootstrap/**" {
			found = true
		}
	}
	if !found {
		t.Fatalf("scope not expanded: %#v", res["write_scope"])
	}
	got := f.task("T-RC1")
	if got.Extra["scope_expand_reason"] != "need wire" {
		t.Fatalf("reason=%v", got.Extra["scope_expand_reason"])
	}
}

func TestScopeSuggestionsFromFiles(t *testing.T) {
	t.Parallel()
	got := scopeSuggestionsFromFiles([]string{
		"backend/internal/bootstrap/wire_x.go",
		"backend/internal/bootstrap/wire_y.go",
		"scripts/check.mjs",
	})
	if len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
}

func TestRenewAgentFromTaskHeartbeat(t *testing.T) {
	f := newFixture(t)
	agentID := "cursor-demo-hb"
	task := f.seedTask("T-HB1", "in_progress", agentID, []string{"mod_a/**"})
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		if err := bindAgent(tx, f.pc.ID(), agentID, "worker", "working", task.ID, "", "", ""); err != nil {
			return err
		}
		_, _ = tx.Exec(`UPDATE agents SET last_seen=? WHERE project_id=? AND id=?`,
			"2026-11-16T10:00:00Z", f.pc.ID(), agentID)
		task.Heartbeat = "2026-11-16T11:50:00Z"
		return db.UpsertTask(tx, task)
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return expireStaleAgentsAt(tx, f.pc, now)
	})
	if err != nil {
		t.Fatal(err)
	}
	var status string
	_ = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		a, e := db.GetAgent(tx, f.pc.ID(), agentID)
		if e != nil || a == nil {
			return e
		}
		status = a.Status
		return nil
	})
	if status != "working" {
		t.Fatalf("should renew not expire: status=%s", status)
	}
}

func TestBlockSuggestsRequestChange(t *testing.T) {
	f := newFixture(t)
	f.seedTask("T-BLK", "in_progress", "cursor-demo-1", []string{"mod_a/**"})
	_ = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return bindAgent(tx, f.pc.ID(), "cursor-demo-1", "worker", "working", "T-BLK", "", "", "")
	})
	t.Setenv("MAW_AGENT", "cursor-demo-1")

	res, err := Block(f.repo, "断环需要改 write_scope 之外的 bootstrap", "T-BLK")
	if err != nil {
		t.Fatalf("block: %v", err)
	}
	fu, _ := res["suggested_followups"].([]string)
	if len(fu) == 0 {
		t.Fatalf("expected followups, got %#v", res)
	}
}
