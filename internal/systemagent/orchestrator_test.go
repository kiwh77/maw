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
			AutoKinds:   []string{SystemKindUnblock},
			MaxAttempts: 3,
		},
		Invoker: FuncInvoker(func(ctx context.Context, req DiagnoseRequest) (string, error) {
			return `{"action":"escalate_human","reason":"test"}`, nil
		}),
		Effects:     NopEffects{},
		WorkerID:    "test-worker",
		ResolvePath: func(string) (string, error) { return dir, nil },
		Clock:       time.Now,
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

// TestOrchestratorRespectsFailureBackoff 覆盖第三条验收：编排器用注入时钟判断
// 「是否已过退避窗口」，窗口内不领取，窗口结束后才重跑。
func TestOrchestratorRespectsFailureBackoff(t *testing.T) {
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
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)
	// attempts=2 → 60s 退避；只过了 45s，这次 Step 不应领取。
	lastFailure := now.Add(-45 * time.Second)
	err = store.Write(func(tx *sql.Tx) error {
		if err := db.UpsertProject(tx, &db.Project{
			ID: pid, Name: "demo", Path: dir, MainBranch: "main", CreatedAt: db.UTCNow(), LastSeen: db.UTCNow(),
		}); err != nil {
			return err
		}
		_, e := db.InsertSystemJob(tx, &db.SystemJob{
			JobID: "sjob_backoff", ProjectID: pid, Kind: SystemKindUnblock,
			Status: db.SystemJobQueued, TaskID: "T-1", Reason: "retry later",
			Attempts: 2, MaxAttempts: 3,
			CreatedAt: lastFailure.Add(-2 * time.Minute).Format(time.RFC3339),
			UpdatedAt: lastFailure.Format(time.RFC3339),
		})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}

	orch := &Orchestrator{
		Store: store,
		Config: config.SystemAgent{
			Enabled: true, MaxConcurrent: 1,
			AutoKinds:   []string{SystemKindUnblock},
			MaxAttempts: 3,
		},
		Invoker: FuncInvoker(func(ctx context.Context, req DiagnoseRequest) (string, error) {
			return `{"action":"escalate_human","reason":"test"}`, nil
		}),
		Effects:     NopEffects{},
		WorkerID:    "test-worker",
		ResolvePath: func(string) (string, error) { return dir, nil },
		Clock:       func() time.Time { return now },
	}
	if n, err := orch.Step(context.Background()); err != nil || n != 0 {
		t.Fatalf("退避窗口内不应领取：n=%d err=%v", n, err)
	}
	if j := readAgentOrchJob(t, store); j.Status != db.SystemJobQueued || j.Attempts != 2 {
		t.Fatalf("退避窗口内作业被改动：status=%s attempts=%d", j.Status, j.Attempts)
	}

	// 再等 20s（距上次失败 65s > 60s），同一条件应能领取并执行完。
	now = now.Add(20 * time.Second)
	if n, err := orch.Step(context.Background()); err != nil || n != 1 {
		t.Fatalf("退避窗口结束后应领取：n=%d err=%v", n, err)
	}
	if j := readAgentOrchJob(t, store); j.Status != db.SystemJobEscalated {
		t.Fatalf("重跑后应写终态 escalated，实际 %s", j.Status)
	}
}

func readAgentOrchJob(t *testing.T, store *db.Store) *db.SystemJob {
	t.Helper()
	var job *db.SystemJob
	err := store.Read(func(tx *sql.Tx) error {
		var e error
		job, e = db.GetSystemJob(tx, "sjob_backoff")
		return e
	})
	if err != nil || job == nil {
		t.Fatalf("read backoff job: %v", err)
	}
	return job
}
