package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/systemagent"
)

// 本文件解析「系统 Agent 诊断后端」，是 llm_node / llm_command 两种配置的唯一去处。
//
// 三条边界：
//   - 显式配置的 Pi 节点优先：同一个 Pi 运行时也用 diagnose 模式（只读）出决策，
//     不再另建一套推理栈；
//   - 旧配置照旧：没有 llm_node 时仍走 llm_command，两者都没有就安全降级为升级人工；
//   - 未就绪不静默偷换：Pi 节点缺探测证据／入口不存在时，明确降级并写明原因，
//     不拿另一个后端冒充「配置的那个」。
const (
	// diagnoseBackendPi 是 Pi 运行时诊断（runtimes/pi 的 diagnose 模式）。
	diagnoseBackendPi = "pi"
	// diagnoseBackendCommand 是旧的 llm_command 外部 CLI。
	diagnoseBackendCommand = "command"
	// diagnoseBackendRule 是规则层安全降级（只产出 escalate_human）。
	diagnoseBackendRule = "rule"
)

// diagnoseBackend 是诊断后端解析结果：Kind 决定「实际用什么」，Reason 说明为什么不是 Pi。
type diagnoseBackend struct {
	Kind    string
	NodeID  string
	Reason  string
	Invoker systemagent.Invoker
}

// resolveDiagnoseBackend 按配置解析诊断后端。
//
// 优先级：显式 llm_node（Pi）> llm_command（旧配置）> 规则降级。
// 解析结果同时用于装配编排器与 `maw system status` 回显，两处读同一个函数，
// 避免「状态说用 Pi、实际跑的是命令行」。
func resolveDiagnoseBackend(pc *projCtx, cfg config.SystemAgent, store *db.Store) diagnoseBackend {
	if nodeID := strings.TrimSpace(cfg.LLMNode); nodeID != "" {
		return resolvePiDiagnoseBackend(pc, cfg, store, nodeID)
	}
	if len(cfg.LLMCommand) > 0 {
		return diagnoseBackend{
			Kind: diagnoseBackendCommand,
			Invoker: systemagent.ExecInvoker{
				Argv:    cfg.LLMCommand,
				Timeout: cfg.DiagnoseTimeout(),
			},
		}
	}
	reason := "llm_node 与 llm_command 都未配置，诊断按安全降级处理"
	return diagnoseBackend{Kind: diagnoseBackendRule, Reason: reason, Invoker: escalateInvoker(reason)}
}

// resolvePiDiagnoseBackend 把 llm_node 解析成可执行的 Pi 诊断后端。
//
// 就绪判据全部来自节点行里已经落盘的观测证据（PI-102 的探测结果）：
// 没有协议证据就是「未就绪」，不猜可用、也不退回 Passthrough。
func resolvePiDiagnoseBackend(pc *projCtx, cfg config.SystemAgent, store *db.Store, nodeID string) diagnoseBackend {
	degrade := func(reason string) diagnoseBackend {
		// 升到人工而不是换后端：配置的是 Pi，跑别的等于换了语义还没人知道。
		full := "Pi 诊断后端未就绪（节点 " + nodeID + "）：" + reason
		return diagnoseBackend{Kind: diagnoseBackendRule, NodeID: nodeID, Reason: full, Invoker: escalateInvoker(full)}
	}
	if store == nil {
		return degrade("状态库不可用，无法读取节点登记")
	}
	var node *db.Node
	if err := store.Read(func(tx *sql.Tx) error {
		n, e := db.GetNode(tx, nodeID)
		node = n
		return e
	}); err != nil {
		return degrade("读取节点失败: " + err.Error())
	}
	if node == nil {
		return degrade("节点未登记（先 maw node register，再用 maw node check 探测）")
	}
	meta := node.Meta
	if kind := metaString(meta, "adapter_kind"); kind != "pi" {
		return degrade("节点不是 Pi 适配器（adapter_kind=" + strings.TrimSpace(kind) + "）")
	}
	argv := metaStrings(meta["argv"])
	if len(argv) < 2 {
		return degrade("节点缺少 argv（应为 [node, runtimes/pi/src/main.mjs]）")
	}
	nodePath := strings.TrimSpace(argv[0])
	runtimePath := strings.TrimSpace(argv[1])
	if runtimePath == "" || !filepath.IsAbs(runtimePath) {
		return degrade("运行时入口不是绝对路径: " + runtimePath)
	}
	if _, err := os.Stat(runtimePath); err != nil {
		return degrade("运行时入口不存在: " + runtimePath)
	}
	// 协议证据：只有实测过 pi-stream-json/1 的节点才算就绪（契约 P110／P111）。
	format := metaString(meta, "protocol_format")
	version := metaString(meta, "protocol_version")
	if format != "pi-stream-json" || version != "1" {
		return degrade("缺少 pi-stream-json/1 探测证据（先 maw node check " + nodeID + "）")
	}
	if state := metaString(meta, "entry_state"); state != "" && state != "ready" {
		return degrade("入口状态为 " + state)
	}
	timeout := cfg.DiagnoseTimeout()
	cwd := ""
	if pc != nil {
		cwd = pc.Repo
	}
	return diagnoseBackend{
		Kind:   diagnoseBackendPi,
		NodeID: nodeID,
		Invoker: &systemagent.PiInvoker{
			NodePath:    nodePath,
			RuntimePath: runtimePath,
			Cwd:         cwd,
			Model:       effectiveModel(meta, ""),
			NodeID:      nodeID,
			Timeout:     timeout,
			Store:       store,
		},
	}
}

// escalateInvoker 是规则层的安全降级：不调模型，直接升级人工并带上原因。
func escalateInvoker(reason string) systemagent.Invoker {
	raw, err := json.Marshal(systemagent.Decision{Action: systemagent.ActionEscalateHuman, Reason: reason})
	if err != nil {
		raw = []byte(`{"action":"escalate_human","reason":"诊断后端不可用"}`)
	}
	text := string(raw)
	return systemagent.FuncInvoker(func(context.Context, systemagent.DiagnoseRequest) (string, error) {
		return text, nil
	})
}

// diagnoseBackendStatus 把解析结果压成 status/看板可读的只读视图。
func diagnoseBackendStatus(cfg config.SystemAgent, b diagnoseBackend) map[string]any {
	out := map[string]any{
		"kind":                b.Kind,
		"node_id":             b.NodeID,
		"timeout_sec":         cfg.DiagnoseTimeoutSec,
		"write_scope":         []string{},
		"llm_command_entries": len(cfg.LLMCommand),
	}
	if b.Reason != "" {
		out["degraded"] = true
		out["reason"] = b.Reason
	}
	return out
}

// ---- Router（系统 Agent）配置读写（来自主干既有 WIP，与诊断后端解析共存）----

// Router（系统 Agent）配置的读写用例。
//
// 之前这些字段只能手改 config.toml：`llm_command` 没配就永远走「升级人工」，
// 于是「任务遇阻时 Router 介入」在生产里从未发生过。这里补上唯一一条写路径，
// 并把「配了没有」直接回显成 llm_ready，避免界面以为它在工作。

// SystemAgentView 是 Router 配置的可读视图。
type SystemAgentView struct {
	ProjectID        string
	Enabled          bool
	LLMNode          string
	WorkerNode       string
	LLMCommand       []string
	AutoKinds        []string
	OpsAllowlist     []string
	EscalatePatterns []string
	PollIntervalSec  int
	MaxConcurrent    int
	CooldownSec      int
	MaxAttempts      int
	LLMReady         bool
}

// SystemAgentUpdateInput 是增量更新输入：只改非 nil 字段。
type SystemAgentUpdateInput struct {
	ProjectID    string
	Enabled      *bool
	LLMNode      *string
	WorkerNode   *string
	LLMCommand   *[]string
	AutoKinds    *[]string
	OpsAllowlist *[]string
	Actor        string
}

// SystemAgentGet 读取工程的 Router 配置。
func SystemAgentGet(projectID string) (SystemAgentView, error) {
	pc, err := loadProjectCtx(projectID)
	if err != nil {
		return SystemAgentView{}, err
	}
	cfg := pc.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	return systemAgentViewOf(pc.ID(), cfg.SystemAgent), nil
}

// SystemAgentUpdate 增量写回 Router 配置。
//
// 白名单类字段（auto_kinds／ops_allowlist／llm_command）做了去空与去重：
// 空串会被当成「放行一切」的隐性入口，必须挡在写盘之前。
func SystemAgentUpdate(in SystemAgentUpdateInput) (SystemAgentView, error) {
	pc, err := loadProjectCtx(in.ProjectID)
	if err != nil {
		return SystemAgentView{}, err
	}
	cfgPath := home.ConfigPath(pc.LocalHome)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return SystemAgentView{}, err
	}
	next := *cfg
	sa := &next.SystemAgent
	if in.Enabled != nil {
		sa.Enabled = *in.Enabled
	}
	if in.LLMNode != nil {
		sa.LLMNode = strings.TrimSpace(*in.LLMNode)
	}
	if in.WorkerNode != nil {
		sa.WorkerNode = strings.TrimSpace(*in.WorkerNode)
	}
	if in.LLMCommand != nil {
		sa.LLMCommand = cleanList(*in.LLMCommand)
	}
	if in.AutoKinds != nil {
		sa.AutoKinds = cleanList(*in.AutoKinds)
	}
	if in.OpsAllowlist != nil {
		sa.OpsAllowlist = cleanList(*in.OpsAllowlist)
	}
	if err := writeSystemAgentConfig(cfgPath, cfg, sa); err != nil {
		return SystemAgentView{}, err
	}
	writeSystemAgentEvent(pc, in.Actor, "system_agent_changed", map[string]any{
		"enabled":       sa.Enabled,
		"llm_node":      sa.LLMNode,
		"worker_node":   sa.WorkerNode,
		"llm_command":   sa.LLMCommand,
		"auto_kinds":    sa.AutoKinds,
		"ops_allowlist": sa.OpsAllowlist,
	})
	return systemAgentViewOf(pc.ID(), *sa), nil
}

func systemAgentViewOf(projectID string, sa config.SystemAgent) SystemAgentView {
	view := SystemAgentView{
		ProjectID:        projectID,
		Enabled:          sa.Enabled,
		LLMNode:          sa.LLMNode,
		WorkerNode:       sa.WorkerNode,
		LLMCommand:       append([]string{}, sa.LLMCommand...),
		AutoKinds:        append([]string{}, sa.AutoKinds...),
		OpsAllowlist:     append([]string{}, sa.OpsAllowlist...),
		EscalatePatterns: append([]string{}, sa.EscalatePatterns...),
		PollIntervalSec:  sa.PollIntervalSec,
		MaxConcurrent:    sa.MaxConcurrent,
		CooldownSec:      sa.CooldownoutSec,
		MaxAttempts:      sa.MaxAttempts,
	}
	view.LLMReady = len(cleanList(sa.LLMCommand)) > 0
	return view
}

// writeSystemAgentConfig 只改 system_agent 段里的可写字段，保留其余现场。
func writeSystemAgentConfig(path string, prev *config.Config, sa *config.SystemAgent) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(raw)
	text = setTOMLBool(text, "system_agent", "enabled", sa.Enabled)
	text = setTOMLString(text, "system_agent", "llm_node", sa.LLMNode)
	text = setTOMLString(text, "system_agent", "worker_node", sa.WorkerNode)
	text = setTOMLStringArray(text, "system_agent", "llm_command", sa.LLMCommand)
	text = setTOMLStringArray(text, "system_agent", "auto_kinds", sa.AutoKinds)
	text = setTOMLStringArray(text, "system_agent", "ops_allowlist", sa.OpsAllowlist)
	// 轮询与重试参数不在界面里改，但要保证写回后仍是合法值。
	text = setTOMLInt(text, "system_agent", "poll_interval_sec", sa.PollIntervalSec)
	text = setTOMLInt(text, "system_agent", "cooldown_sec", sa.CooldownoutSec)
	text = setTOMLInt(text, "system_agent", "max_attempts", sa.MaxAttempts)
	_ = prev
	return os.WriteFile(path, []byte(text), 0o644)
}

// cleanList 去空白、去重、保序。
func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// writeSystemAgentEvent 记录配置变更。
func writeSystemAgentEvent(pc *projCtx, actor, typ string, data map[string]any) {
	if pc == nil {
		return
	}
	if strings.TrimSpace(actor) == "" {
		actor = "user"
	}
	_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		return db.AppendEvent(tx, pc.ID(), actor, typ, "", data)
	})
}

// intText 是 TOML 整数字面量。
func intText(v int) string { return strconv.Itoa(v) }
