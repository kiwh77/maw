package app

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/dispatch"
	"github.com/kiwh77/maw/internal/home"
)

// mustHome / mustStore / mustRuntime 把 fixture 里的重复样板收成一行。
func mustHome(t *testing.T, repo string) string {
	t.Helper()
	local, err := home.RequireHome(repo)
	if err != nil {
		t.Fatalf("require home: %v", err)
	}
	return local
}

func mustStore(t *testing.T, gdb string) *db.Store {
	t.Helper()
	st, err := db.OpenStore(gdb)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mustRuntime(t *testing.T, pc *projCtx) *Runtime {
	t.Helper()
	rt, err := NewRuntime(pc, "")
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	return rt
}

// seedAutoNode 落一个观测齐全、可被自动执行选中的节点。
func seedAutoNode(t *testing.T, gdb, pid, nodeID string, observedAt time.Time) {
	t.Helper()
	stamp := observedAt.UTC().Format(time.RFC3339)
	if err := db.WithTx(gdb, func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{
			ID: nodeID, ProjectID: pid, Label: nodeID, SlotLimit: 1,
			CreatedAt: stamp, LastSeen: stamp,
			Meta: map[string]any{
				"entry_state": "ready", "auth_state": "ready",
				"connection_state": "fresh", "capacity_state": "available",
				"observed_at": stamp, "accept_new": true,
				// 关掉自动探测：测试里没有真实 CLI，探测会把节点改回 unavailable。
				"auto_probe": false,
			},
		})
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
}

func readCfg(t *testing.T, repo string) *config.Config {
	t.Helper()
	local, err := home.RequireHome(repo)
	if err != nil {
		t.Fatalf("require home: %v", err)
	}
	cfg, err := config.Load(home.ConfigPath(local))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// TestAutoExecutionEnableWritesAllSwitches 是最核心的一条：
// 打开开关必须一次写齐 auto_enabled / auto_select / allowed_nodes / worker_node，
// 因为「只写 auto_enabled」正是「打开了却不自动领题」的原因。
func TestAutoExecutionEnableWritesAllSwitches(t *testing.T) {
	f := newFixture(t)
	seedAutoNode(t, f.pc.GlobalDB, f.pc.ID(), "dsh-lab", time.Now().UTC())

	view, err := AutoExecutionSet(AutoExecutionInput{
		ProjectID: f.pc.ID(), Enabled: true, NodeID: "dsh-lab", Actor: "tester",
	})
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !view.Enabled {
		t.Fatalf("开启后视图应为 enabled: %+v", view)
	}
	if view.NodeID != "dsh-lab" {
		t.Fatalf("生效节点应为 dsh-lab: %+v", view)
	}
	if !view.Ready {
		t.Fatalf("节点就绪且有上限，应判定 ready: %+v", view)
	}

	cfg := readCfg(t, f.repo)
	if !cfg.Managed.AutoEnabled || !cfg.Managed.AutoSelect {
		t.Fatalf("两个开关都必须写: %+v", cfg.Managed)
	}
	if cfg.Managed.Limit < 1 {
		t.Fatalf("上限至少 1: %d", cfg.Managed.Limit)
	}
	if len(cfg.Managed.AllowedNodes) != 1 || cfg.Managed.AllowedNodes[0] != "dsh-lab" {
		t.Fatalf("allowed_nodes 应收窄到选中节点: %v", cfg.Managed.AllowedNodes)
	}
	if cfg.SystemAgent.WorkerNode != "dsh-lab" {
		t.Fatalf("worker_node 应同步: %q", cfg.SystemAgent.WorkerNode)
	}

	// 事件审计：开关变更是重决定，必须能在时间轴上查到。
	var n int
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id=? AND type='auto_execution_changed'`,
			f.pc.ID()).Scan(&n)
	}); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Fatalf("应落一条 auto_execution_changed，实际 %d", n)
	}
}

// TestAutoExecutionRejectsNotReadyNode 覆盖「不允许开了却不动」：
// 节点接不了活时拒绝开启，配置保持原样，并给出稳定原因码。
func TestAutoExecutionRejectsNotReadyNode(t *testing.T) {
	f := newFixture(t)
	// 观测过期且没有 entry_path：探测必然失败 → node_not_ready。
	seedAutoNode(t, f.pc.GlobalDB, f.pc.ID(), "stale-lab", time.Now().UTC().Add(-2*time.Hour))

	view, err := AutoExecutionSet(AutoExecutionInput{
		ProjectID: f.pc.ID(), Enabled: true, NodeID: "stale-lab",
	})
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if view.Enabled {
		t.Fatalf("节点接不了活时不应真的开启: %+v", view)
	}
	if len(view.Reasons) == 0 {
		t.Fatalf("应给出未就绪原因: %+v", view)
	}
	cfg := readCfg(t, f.repo)
	if cfg.Managed.AutoEnabled || cfg.Managed.AutoSelect {
		t.Fatalf("被拒绝时不得写入开关: %+v", cfg.Managed)
	}
}

// TestAutoExecutionRejectsUnknownNode：指名的节点不存在时明确拒绝，不静默换节点。
func TestAutoExecutionRejectsUnknownNode(t *testing.T) {
	f := newFixture(t)
	seedAutoNode(t, f.pc.GlobalDB, f.pc.ID(), "dsh-lab", time.Now().UTC())

	view, err := AutoExecutionSet(AutoExecutionInput{
		ProjectID: f.pc.ID(), Enabled: true, NodeID: "ghost-node",
	})
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if view.Enabled {
		t.Fatalf("幽灵节点不应被接受: %+v", view)
	}
	if !strings.Contains(view.Detail, "ghost-node") {
		t.Fatalf("应说明是哪个节点没登记: %q", view.Detail)
	}
}

// TestAutoExecutionDisableKeepsPolicy：关闭只收两个开关，保留节点与上限。
func TestAutoExecutionDisableKeepsPolicy(t *testing.T) {
	f := newFixture(t)
	seedAutoNode(t, f.pc.GlobalDB, f.pc.ID(), "dsh-lab", time.Now().UTC())
	if _, err := AutoExecutionSet(AutoExecutionInput{
		ProjectID: f.pc.ID(), Enabled: true, NodeID: "dsh-lab",
	}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	view, err := AutoExecutionSet(AutoExecutionInput{ProjectID: f.pc.ID(), Enabled: false})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if view.Enabled {
		t.Fatalf("关闭后应为 enabled=false: %+v", view)
	}
	cfg := readCfg(t, f.repo)
	if cfg.Managed.AutoEnabled || cfg.Managed.AutoSelect {
		t.Fatalf("关闭应落两个开关: %+v", cfg.Managed)
	}
	if len(cfg.Managed.AllowedNodes) != 1 || cfg.Managed.AllowedNodes[0] != "dsh-lab" {
		t.Fatalf("关闭不应清掉节点现场: %v", cfg.Managed.AllowedNodes)
	}
}

// TestAutoExecutionGetReportsServeState：视图必须如实报告中枢在不在跑，
// 否则用户会把「已保存但没生效」当成 bug。
func TestAutoExecutionGetReportsServeState(t *testing.T) {
	f := newFixture(t)
	seedAutoNode(t, f.pc.GlobalDB, f.pc.ID(), "dsh-lab", time.Now().UTC())
	view, err := AutoExecutionGet(f.pc.ID())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.ServeRunning {
		t.Fatalf("没有 leader 持锁时不该报告 serve_running")
	}
	if view.Applied {
		t.Fatalf("中枢没跑时 applied 必须为 false")
	}
	if view.Reason != AutoReasonServeNotRunning {
		t.Fatalf("原因码应为 serve_not_running，实际 %q", view.Reason)
	}
	if !view.Ready {
		t.Fatalf("节点就绪时 ready 应为 true（ready 与 serve 状态是两件事）: %+v", view)
	}
}

// TestSystemAgentUpdateWritesLLMCommand：Router 能真正介入的前提是 llm_command 可写。
func TestSystemAgentUpdateWritesLLMCommand(t *testing.T) {
	f := newFixture(t)
	cmd := []string{"/bin/echo", "{}"}
	kinds := []string{"unblock_assist", "task_split"}
	allow := []string{"maw prune"}
	yes := true
	view, err := SystemAgentUpdate(SystemAgentUpdateInput{
		ProjectID: f.pc.ID(), Enabled: &yes,
		LLMCommand: &cmd, AutoKinds: &kinds, OpsAllowlist: &allow, Actor: "tester",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !view.LLMReady {
		t.Fatalf("配了 llm_command 应报告 llm_ready: %+v", view)
	}
	cfg := readCfg(t, f.repo)
	if len(cfg.SystemAgent.LLMCommand) != 2 || cfg.SystemAgent.LLMCommand[0] != "/bin/echo" {
		t.Fatalf("llm_command 未写回: %v", cfg.SystemAgent.LLMCommand)
	}
	if !cfg.SystemAgent.Enabled {
		t.Fatalf("enabled 未写回")
	}
	if len(cfg.SystemAgent.OpsAllowlist) != 1 || cfg.SystemAgent.OpsAllowlist[0] != "maw prune" {
		t.Fatalf("ops_allowlist 未写回: %v", cfg.SystemAgent.OpsAllowlist)
	}

	// 再读一次：读路径与写路径必须同源。
	got, err := SystemAgentGet(f.pc.ID())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.AutoKinds) != 2 || got.AutoKinds[1] != "task_split" {
		t.Fatalf("读回不一致: %+v", got)
	}
}

// TestServeLeaderHotReloadAppliesSwitch：热重载的核心保证——同一轮循环内，
// 改 config.toml 就能让 leader 的开关跟着变，不需要重启 serve。
func TestServeLeaderHotReloadAppliesSwitch(t *testing.T) {
	f := newFixture(t)
	cfgPath := home.ConfigPath(mustHome(t, f.repo))
	leader := &dispatch.Leader{}
	hot := newServeLeaderHot(f.pc, mustStore(t, f.pc.GlobalDB), mustRuntime(t, f.pc), nil, cfgPath,
		f.pc.Config.SystemAgent, leader, nil)

	// 初始：默认全关，首轮重载后三个开关都应为假。
	if err := hot.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if leader.Dispatch || leader.Producer != nil || leader.System != nil {
		t.Fatalf("默认配置下不应开任何循环: dispatch=%v", leader.Dispatch)
	}

	// 只打开自动执行（auto_enabled + auto_select），节点允许为空。
	yes := true
	if _, err := ProjectUpdate(f.repo, ProjectUpdateInput{
		Ref: f.pc.ID(), AutoEnabled: &yes, AutoSelect: &yes,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	// 二次重载走 mtime／size 判定：保证文件内容确实变了。
	if err := hot.Reload(); err != nil {
		t.Fatalf("reload 2: %v", err)
	}
	if !leader.Dispatch {
		t.Fatalf("打开自动执行后 Dispatch 应为真")
	}
	if leader.Producer == nil {
		t.Fatalf("auto_select 打开后必须装配选题生产者")
	}

	// 关掉自动执行：派发与生产者都要停，不等重启。
	no := false
	if _, err := ProjectUpdate(f.repo, ProjectUpdateInput{
		Ref: f.pc.ID(), AutoEnabled: &no, AutoSelect: &no,
	}); err != nil {
		t.Fatalf("update off: %v", err)
	}
	if err := hot.Reload(); err != nil {
		t.Fatalf("reload 3: %v", err)
	}
	if leader.Dispatch || leader.Producer != nil {
		t.Fatalf("关闭后应停掉派发与生产者: dispatch=%v", leader.Dispatch)
	}
}

// TestRouterOpsRunIsAudited 是 Router「介入并执行白名单运维命令」的端到端验收：
// 一条 unblock_assist 作业经由 llm_command 产出 ops_run 决策，命令命中白名单后
// 真的被执行，且结果里留下「命令原文／命中的白名单规则／退出码」三项审计证据。
func TestRouterOpsRunIsAudited(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("T-OPS", "blocked", "", nil)

	// 用 /bin/sh 当 LLM：stdin 的 prompt 忽略，直接吐一条合规决策 JSON。
	script := `printf '%s' '{"action":"ops_run","reason":"router smoke","ops_command":"echo router-ok"}'`
	cmd := []string{"/bin/sh", "-c", script}
	kinds := []string{"unblock_assist"}
	allow := []string{"echo"}
	yes := true
	if _, err := SystemAgentUpdate(SystemAgentUpdateInput{
		ProjectID: f.pc.ID(), Enabled: &yes,
		LLMCommand: &cmd, AutoKinds: &kinds, OpsAllowlist: &allow,
	}); err != nil {
		t.Fatalf("update system agent: %v", err)
	}

	res, err := SystemTrigger(f.repo, "unblock_assist", task.ID, "门禁环境失败 needs_human")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	jobID, _ := res["job_id"].(string)
	if jobID == "" {
		t.Fatalf("应入队一条作业: %+v", res)
	}
	if _, err := SystemRun(f.repo, jobID); err != nil {
		t.Fatalf("run job: %v", err)
	}

	var job *db.SystemJob
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		job, e = db.GetSystemJob(tx, jobID)
		return e
	}); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if job == nil {
		t.Fatal("作业不存在")
	}
	if job.Status != db.SystemJobDone {
		t.Fatalf("作业应以 done 收尾，实际 %s（result=%+v）", job.Status, job.Result)
	}
	if got, _ := job.Result["ops_command"].(string); got != "echo router-ok" {
		t.Fatalf("结果应收录命令原文: %+v", job.Result)
	}
	if got, _ := job.Result["ops_allowlist_rule"].(string); got != "echo" {
		t.Fatalf("结果应记录命中的白名单规则: %+v", job.Result)
	}
	if out, _ := job.Result["stdout"].(string); !strings.Contains(out, "router-ok") {
		t.Fatalf("命令应真的被执行: %q", out)
	}
	if _, ok := job.Result["ops_exit_code"]; !ok {
		t.Fatalf("结果应带退出码: %+v", job.Result)
	}
}

// TestRouterOpsRunRejectsNonAllowlisted：白名单之外一律拒绝，且作业不落 done。
func TestRouterOpsRunRejectsNonAllowlisted(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("T-DENY", "blocked", "", nil)
	script := `printf '%s' '{"action":"ops_run","reason":"nope","ops_command":"rm -rf /tmp/should-not-run"}'`
	cmd := []string{"/bin/sh", "-c", script}
	allow := []string{"echo"}
	yes := true
	if _, err := SystemAgentUpdate(SystemAgentUpdateInput{
		ProjectID: f.pc.ID(), Enabled: &yes, LLMCommand: &cmd, OpsAllowlist: &allow,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	res, err := SystemTrigger(f.repo, "unblock_assist", task.ID, "needs human")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	jobID, _ := res["job_id"].(string)
	// 拒绝会以错误返回并让作业重排；这里只断言「不会被执行」。
	_, _ = SystemRun(f.repo, jobID)

	var job *db.SystemJob
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		job, e = db.GetSystemJob(tx, jobID)
		return e
	}); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if job != nil && job.Status == db.SystemJobDone {
		t.Fatalf("白名单外命令不该成功: %+v", job.Result)
	}
	if job != nil {
		if got, _ := job.Result["ops_command"].(string); got != "" {
			t.Fatalf("被拒绝时不应记录执行证据: %+v", job.Result)
		}
	}
}
