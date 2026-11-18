package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
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

// ---- PI-305：诊断后端解析 ----------------------------------------------

// configureDiagnose 写入 system_agent 的诊断后端配置（llm_node / llm_command）。
func configureDiagnose(t *testing.T, repo, llmNode string, llmCommand []string) {
	t.Helper()
	enableSystemAgent(t, repo)
	local, err := home.RequireHome(repo)
	if err != nil {
		t.Fatal(err)
	}
	path := home.ConfigPath(local)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := setTOMLString(string(raw), "system_agent", "llm_node", llmNode)
	text = setTOMLStringArray(text, "system_agent", "llm_command", llmCommand)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// registerPiNode 在 fixture 里登记一个 Pi 节点，并把运行时入口落到真实文件。
func registerPiNode(t *testing.T, f *fixture, nodeID string) string {
	t.Helper()
	runtimePath := filepath.Join(f.repo, "runtimes", "pi", "src", "main.mjs")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte("// pi runtime placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	argv := `["node",` + strconv.Quote(runtimePath) + `]`
	if _, err := NodeRegister(f.repo, NodeRegisterInput{ID: nodeID, ArgvJSON: argv}); err != nil {
		t.Fatalf("NodeRegister: %v", err)
	}
	return runtimePath
}

// markPiProbed 模拟 PI-102 的成功探测：写入协议与模式证据。
func markPiProbed(t *testing.T, f *fixture, nodeID string) {
	t.Helper()
	err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		n, e := db.GetNode(tx, nodeID)
		if e != nil || n == nil {
			return e
		}
		if n.Meta == nil {
			n.Meta = map[string]any{}
		}
		n.Meta["protocol_format"] = "pi-stream-json"
		n.Meta["protocol_version"] = "1"
		n.Meta["version"] = "1.5.2"
		n.Meta["entry_state"] = "ready"
		return db.UpsertNode(tx, n)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// 显式配置的 Pi 节点就是诊断后端：同一个运行时、diagnose 模式、超时取配置。
func TestDiagnoseBackendUsesConfiguredPiNode(t *testing.T) {
	f := newFixture(t)
	runtimePath := registerPiNode(t, f, "pi-1")
	markPiProbed(t, f, "pi-1")
	configureDiagnose(t, f.repo, "pi-1", nil)

	pc, err := resolveProject(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		t.Fatal(err)
	}
	backend := resolveDiagnoseBackend(pc, pc.Config.SystemAgent, store)
	if backend.Kind != diagnoseBackendPi {
		t.Fatalf("显式配置 Pi 后诊断后端应为 pi，得到 %q（%s）", backend.Kind, backend.Reason)
	}
	if backend.Reason != "" {
		t.Fatalf("就绪时不应有降级原因: %s", backend.Reason)
	}
	inv, ok := backend.Invoker.(*systemagent.PiInvoker)
	if !ok {
		t.Fatalf("应为 PiInvoker，得到 %T", backend.Invoker)
	}
	if inv.RuntimePath != runtimePath || inv.NodeID != "pi-1" {
		t.Fatalf("PiInvoker 绑定不对: runtime=%q node=%q", inv.RuntimePath, inv.NodeID)
	}
	if inv.Timeout != pc.Config.DiagnoseTimeout() || inv.Timeout <= 0 {
		t.Fatalf("诊断超时应取配置，得到 %v", inv.Timeout)
	}

	res, err := SystemStatus(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	if res["diagnose_backend"] != diagnoseBackendPi {
		t.Fatalf("system status 应回显 diagnose_backend=pi：%v", res["diagnose_backend"])
	}
	view, _ := res["diagnose"].(map[string]any)
	if view == nil || view["node_id"] != "pi-1" {
		t.Fatalf("system status 缺少诊断明细：%+v", res["diagnose"])
	}
	if _, degraded := view["degraded"]; degraded {
		t.Fatalf("就绪时不得标降级：%+v", view)
	}
}

// 未就绪的 Pi 节点必须清楚降级：不偷换后端、不留「假装跑过」。
func TestDiagnoseBackendDegradesWhenPiNotReady(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, f *fixture)
		reason  string
	}{
		{"缺探测证据", func(t *testing.T, f *fixture) { registerPiNode(t, f, "pi-1") }, "探测证据"},
		{"节点未登记", func(t *testing.T, f *fixture) {}, "未登记"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.prepare(t, f)
			configureDiagnose(t, f.repo, "pi-1", nil)
			pc, err := resolveProject(f.repo)
			if err != nil {
				t.Fatal(err)
			}
			store, err := db.OpenStore(pc.GlobalDB)
			if err != nil {
				t.Fatal(err)
			}
			backend := resolveDiagnoseBackend(pc, pc.Config.SystemAgent, store)
			if backend.Kind != diagnoseBackendRule {
				t.Fatalf("未就绪时必须降级为规则层，得到 %q", backend.Kind)
			}
			if !strings.Contains(backend.Reason, tc.reason) {
				t.Fatalf("降级原因应点明 %q：%s", tc.reason, backend.Reason)
			}
			raw, err := backend.Invoker.Diagnose(context.Background(), systemagent.DiagnoseRequest{JobID: "sjob_x"})
			if err != nil {
				t.Fatal(err)
			}
			d, err := systemagent.ParseDecision(raw)
			if err != nil || d.Action != systemagent.ActionEscalateHuman {
				t.Fatalf("降级决策应是 escalate_human：%v / %+v", err, d)
			}
			if !strings.Contains(d.Reason, "Pi 诊断后端未就绪") {
				t.Fatalf("决策原因应保留降级解释：%s", d.Reason)
			}
		})
	}
}

// 节点登记了但不是 Pi 适配器：同样降级，不把 local_cli 当 Pi 跑。
func TestDiagnoseBackendDegradesForNonPiNode(t *testing.T) {
	f := newFixture(t)
	if _, err := NodeRegister(f.repo, NodeRegisterInput{ID: "cli-1", ArgvJSON: `["/bin/echo","ok"]`}); err != nil {
		t.Fatal(err)
	}
	configureDiagnose(t, f.repo, "cli-1", nil)
	pc, err := resolveProject(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		t.Fatal(err)
	}
	backend := resolveDiagnoseBackend(pc, pc.Config.SystemAgent, store)
	if backend.Kind != diagnoseBackendRule || !strings.Contains(backend.Reason, "Pi 适配器") {
		t.Fatalf("非 Pi 节点应降级并说明原因：kind=%s reason=%s", backend.Kind, backend.Reason)
	}
}

// 旧配置仍可读：没有 llm_node 时继续走 llm_command。
func TestDiagnoseBackendKeepsLegacyLLMCommand(t *testing.T) {
	f := newFixture(t)
	configureDiagnose(t, f.repo, "", []string{"echo", "ok"})
	pc, err := resolveProject(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		t.Fatal(err)
	}
	backend := resolveDiagnoseBackend(pc, pc.Config.SystemAgent, store)
	if backend.Kind != diagnoseBackendCommand {
		t.Fatalf("旧配置应走 command，得到 %q", backend.Kind)
	}
	exec, ok := backend.Invoker.(systemagent.ExecInvoker)
	if !ok || len(exec.Argv) != 2 || exec.Timeout != pc.Config.DiagnoseTimeout() {
		t.Fatalf("ExecInvoker 装配不对：%+v", backend.Invoker)
	}
}

// 什么都没配时安全降级，且状态里能看出「为什么没模型在跑」。
func TestDiagnoseBackendDefaultsToRuleWithReason(t *testing.T) {
	f := newFixture(t)
	configureDiagnose(t, f.repo, "", nil)
	pc, err := resolveProject(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		t.Fatal(err)
	}
	backend := resolveDiagnoseBackend(pc, pc.Config.SystemAgent, store)
	if backend.Kind != diagnoseBackendRule || backend.Reason == "" {
		t.Fatalf("缺后端时应降级并带原因：kind=%s reason=%s", backend.Kind, backend.Reason)
	}
	res, err := SystemStatus(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := res["diagnose"].(map[string]any)
	if view == nil || view["degraded"] != true {
		t.Fatalf("system status 应标出降级：%+v", res["diagnose"])
	}
}
