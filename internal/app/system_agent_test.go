package app

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/systemagent"
)

func TestDeliveryBlockRaisesAndEnqueues(t *testing.T) {
	f := newFixture(t)
	enableSystemAgent(t, f.repo)

	agent := "cursor-sysagent-1"
	t.Setenv("MAW_AGENT", agent)
	if _, err := Register(f.repo, agent, "worker"); err != nil {
		t.Fatal(err)
	}
	f.seedTask("T-BLOCK", "in_progress", agent, []string{"internal/**"})

	if _, err := Block(f.repo, "scope too large need split", "T-BLOCK"); err != nil {
		t.Fatal(err)
	}

	store, err := db.OpenStore(f.pc.GlobalDB)
	if err != nil {
		t.Fatal(err)
	}
	var nNotif, nJobs int
	err = store.Read(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT COUNT(*) FROM notification_deliveries WHERE project_id = ?`, f.pc.ID()).Scan(&nNotif); err != nil {
			return err
		}
		jobs, e := db.ListSystemJobs(tx, f.pc.ID(), db.SystemJobQueued, 10)
		nJobs = len(jobs)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if nNotif < 1 {
		t.Fatalf("expected notification rows, got %d", nNotif)
	}
	if nJobs < 1 {
		t.Fatalf("expected system job queued, got %d", nJobs)
	}

	pc, err := resolveProject(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	orch := &systemagent.Orchestrator{
		Store:  store,
		Config: pc.Config.SystemAgent,
		Invoker: systemagent.FuncInvoker(func(ctx context.Context, req systemagent.DiagnoseRequest) (string, error) {
			return `{"action":"escalate_human","reason":"unit"}`, nil
		}),
		Effects:     systemagent.NopEffects{},
		WorkerID:    "test",
		ResolvePath: func(string) (string, error) { return f.repo, nil },
	}
	if n, err := orch.Step(context.Background()); err != nil || n != 1 {
		t.Fatalf("step ran=%d err=%v", n, err)
	}
}

func TestV1RouteSystemJobs(t *testing.T) {
	if got := v1Route("/api/v1/system-jobs"); got != v1System {
		t.Fatalf("got %v", got)
	}
	if got := v1Route("/api/v1/projects/p1/system-jobs"); got != v1System {
		t.Fatalf("got %v", got)
	}
}

func enableSystemAgent(t *testing.T, repo string) {
	t.Helper()
	local, err := home.RequireHome(repo)
	if err != nil {
		t.Fatal(err)
	}
	path := home.ConfigPath(local)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if i := strings.Index(text, "[system_agent]"); i >= 0 {
		rest := text[i:]
		rest = strings.Replace(rest, "enabled = false", "enabled = true", 1)
		text = text[:i] + rest
	} else {
		text = strings.TrimRight(text, "\n") + `

[system_agent]
config_version = 1
enabled = true
poll_interval_sec = 5
max_concurrent = 1
auto_kinds = ["unblock_assist"]
escalate_patterns = ["needs_human"]
cooldown_sec = 1
max_attempts = 3
`
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
