package systemagent

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

func TestOrchestratorBlockEscalateLoop(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	store, err := db.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	pid := "0123456789abcdef"
	err = store.Write(func(tx *sql.Tx) error {
		if err := db.UpsertProject(tx, &db.Project{
			ID: pid, Name: "demo", Path: dir, MainBranch: "main", CreatedAt: db.UTCNow(), LastSeen: db.UTCNow(),
		}); err != nil {
			return err
		}
		job := &db.SystemJob{
			JobID: "sjob_test001", ProjectID: pid, Kind: SystemKindUnblock,
			Status: db.SystemJobQueued, TaskID: "T-1", Reason: "scope too large",
			MaxAttempts: 3, Decision: map[string]any{},
		}
		_, err := db.InsertSystemJob(tx, job)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	orch := &Orchestrator{
		Store: store,
		Config: config.SystemAgent{
			Enabled: true, MaxConcurrent: 1,
			AutoKinds: []string{SystemKindUnblock},
			MaxAttempts: 3,
		},
		Invoker: FuncInvoker(func(ctx context.Context, req DiagnoseRequest) (string, error) {
			return `{"action":"escalate_human","reason":"test"}`, nil
		}),
		Effects:  NopEffects{},
		WorkerID: "test-worker",
		ResolvePath: func(string) (string, error) { return dir, nil },
		Clock: time.Now,
	}
	n, err := orch.Step(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("ran=%d", n)
	}
	var status string
	_ = store.Read(func(tx *sql.Tx) error {
		j, e := db.GetSystemJob(tx, "sjob_test001")
		if e != nil {
			return e
		}
		status = j.Status
		return nil
	})
	if status != db.SystemJobEscalated {
		t.Fatalf("status=%s want escalated", status)
	}
}
