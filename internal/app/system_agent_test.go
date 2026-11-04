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

// SA-004：受管视图与 system status 同时给出两个开关。
//
// 页面（/api/runtime → auto_execution）与 CLI（maw system status）读的是同一份视图；
// 只回显 auto_enabled 会让人把「自动模式关着」读成「什么都不自动跑」，
// 而系统 Agent 的阻塞自愈其实可能正在工作。
func TestManagedViewAndSystemStatusExposeBothSwitches(t *testing.T) {
	f := newFixture(t)

	rt, err := NewRuntime(f.pc, "")
	if err != nil {
		t.Fatal(err)
	}
	view := rt.ManagedView()
	if on, _ := view["system_agent_enabled"].(bool); on {
		t.Fatal("默认视图里系统 Agent 必须是关闭")
	}
	if on, _ := view["auto_enabled"].(bool); on {
		t.Fatal("默认视图里自动派发必须是关闭")
	}

	// 打开自愈后重新装配：视图跟随，但自动派发仍为假。
	enableSystemAgent(t, f.repo)
	pc, err := resolveProject(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	rt, err = NewRuntime(pc, "")
	if err != nil {
		t.Fatal(err)
	}
	view = rt.ManagedView()
	if on, _ := view["system_agent_enabled"].(bool); !on {
		t.Fatal("system_agent.enabled=true 时视图应回显为开")
	}
	if on, _ := view["auto_enabled"].(bool); on {
		t.Fatal("只开系统 Agent 不得让 auto_enabled 变真")
	}
	sub, _ := view["system_agent"].(map[string]any)
	if sub == nil || sub["enabled"] != true {
		t.Fatalf("视图缺少 system_agent.enabled 明细：%+v", view["system_agent"])
	}

	// CLI 状态与视图同源：两个开关并排给出，避免只看一个就下结论。
	res, err := SystemStatus(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	if on, _ := res["enabled"].(bool); !on {
		t.Fatal("system status 应报告系统 Agent 已开启")
	}
	if on, _ := res["dispatch_enabled"].(bool); on {
		t.Fatal("system status 不得把自动派发报成开启")
	}
	if lim, _ := res["managed_execution_limit"].(int); lim != 1 {
		t.Fatalf("system status 的上限=%v，期望 1", res["managed_execution_limit"])
	}
}
