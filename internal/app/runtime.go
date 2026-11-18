package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/adapters/pi"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/dispatch"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/runner/process"
)

const (
	runnerTokenEnv  = "MAW_RUNNER_TOKEN"
	runnerTokenFile = "bootstrap.token"
	runnerLockFile  = "runner.lock"
)

// runMetaFile 是 `__runner` 读取的运行元数据文件名；字段与 InternalRunner 的读取形状一致。
const runMetaFile = "run_meta.json"

// 受管路径的启动来源声明：都是显式注入，不设置就保持「只写 run_meta、不写 launch_spec」的现状。
//
//   - managedLaunchSpecEnv 传完整 model.LaunchSpec JSON（字段最全，生产/系统派发用）；
//   - managedLaunchCmdEnv 传 JSON 数组 argv（第 0 个元素是可执行文件绝对路径），便于手工验证。
const (
	managedLaunchSpecEnv = "MAW_LAUNCH_SPEC"
	managedLaunchCmdEnv  = "MAW_LAUNCH_CMD"
)

// managedLaunchEnvAllowlist 是默认透传的环境变量：来源进程要能正常起跑至少需要 PATH/HOME。
var managedLaunchEnvAllowlist = []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "USER", "SHELL"}

// RunMeta 是 ops/<operation_id>/run_meta.json 的形状。
//
// 字段与 `__runner` 的读取保持一致：缺 run_id 时 Runner 会用操作号兜底，
// 缺 global_db 时事实无处提交，所以这几项必须在 Launch 前落盘。
type RunMeta struct {
	OperationID string `json:"operation_id"`
	RunID       string `json:"run_id"`
	InstanceID  string `json:"instance_id"`
	Generation  int    `json:"generation"`
	ProjectID   string `json:"project_id"`
	GlobalDB    string `json:"global_db"`
	CreatedAt   string `json:"created_at"`
}

// Runtime 是受管执行的应用侧装配：中枢协调与独立 Runner 进程分离。
//
// 中枢（本进程）只推进 Operation／意图；真正监督工具进程的是 `maw __runner`，
// 窗口或 HTTP 连接退出不会带走 Runner（独立进程组）。
type Runtime struct {
	pc       *projCtx
	stateDir string
	selfBin  string
	// modelOverride 仅用于本次进程的显式启动覆盖；自动派发保持空。
	modelOverride string
}

// NewRuntime 从工程上下文装配运行时。selfBin 为空时用当前可执行文件。
func NewRuntime(pc *projCtx, selfBin string) (*Runtime, error) {
	if pc == nil {
		return nil, errx.New("runtime: missing project", "maw init")
	}
	if selfBin == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		selfBin = exe
	}
	global, err := home.GlobalDir()
	if err != nil {
		return nil, err
	}
	state := filepath.Join(global, "runtime", pc.ID())
	if err := os.MkdirAll(state, 0o755); err != nil {
		return nil, err
	}
	return &Runtime{pc: pc, stateDir: state, selfBin: selfBin}, nil
}

// StateDir 返回本工程受管运行状态根目录。
func (rt *Runtime) StateDir() string {
	if rt == nil {
		return ""
	}
	return rt.stateDir
}

// OpDir 返回单个 Operation 的本机状态目录。
func (rt *Runtime) OpDir(operationID string) string {
	return filepath.Join(rt.stateDir, "ops", operationID)
}

// ManagedView 返回配置中的受管执行视图（供命令与看板）。
//
// 这里同时回显系统 Agent 开关（SA-004）：两个开关驱动两条不同的循环，
// 只看 auto_enabled 会让人以为「自动模式关着＝什么都不自动跑」，而系统 Agent
// 的自愈其实可能正在工作。视图给出的是**事实**，解释由调用方按需组织。
func (rt *Runtime) ManagedView() map[string]any {
	enabled, limit := false, 1
	ver := 1
	systemEnabled := false
	pollSec, maxConcurrent := 5, 1
	if rt != nil && rt.pc != nil && rt.pc.Config != nil {
		enabled, limit = rt.pc.Config.DispatchEnabled()
		ver = rt.pc.Config.Managed.ConfigVersion
		if ver == 0 {
			ver = 1
		}
		systemEnabled = rt.pc.Config.SystemAgentEnabled()
		pollSec = rt.pc.Config.SystemAgent.PollIntervalSec
		if pollSec <= 0 {
			pollSec = 5
		}
		maxConcurrent = rt.pc.Config.SystemAgent.MaxConcurrent
		if maxConcurrent <= 0 {
			maxConcurrent = 1
		}
	}
	return map[string]any{
		"config_version":          ver,
		"auto_enabled":            enabled,
		"managed_execution_limit": limit,
		"platform":                runtime.GOOS,
		"platform_supported":      runtime.GOOS == "darwin",
		// 系统 Agent 与自动派发是两个开关：这里的 enabled 只代表自愈循环开不开。
		"system_agent_enabled": systemEnabled,
		"system_agent": map[string]any{
			"enabled":           systemEnabled,
			"poll_interval_sec": pollSec,
			"max_concurrent":    maxConcurrent,
		},
	}
}

// AssertPlatform 在非验证平台上拒绝受管启动。
func (rt *Runtime) AssertPlatform() error {
	if runtime.GOOS != "darwin" {
		return i18n.E("err.managed_platform", "next.manual_or_observe", runtime.GOOS)
	}
	return nil
}

// runnerLauncher 通过同一二进制的内部入口拉起独立 Runner。
type runnerLauncher struct {
	rt *Runtime
}

// PrepareRunnerArtifacts 在起 Runner 之前把运行元数据与启动规格落到 op 目录。
//
// Runner 只读文件、不读中枢内存：
//   - run_meta.json 每次都覆盖（它描述本次操作的身份：run_id / project_id / global_db）；
//   - launch_spec.json 只在显式给了来源、且目录里还没有时写入（外部预置的规格优先级最高）。
//
// 没有来源时不写 launch_spec.json：`__runner` 保持「校验令牌后驻留等信号」的现状。
func (rt *Runtime) PrepareRunnerArtifacts(opDir string, op dispatch.Operation) error {
	if rt == nil {
		return errx.New("runtime missing", "maw init")
	}
	if err := os.MkdirAll(opDir, 0o755); err != nil {
		return err
	}
	if err := writeJSONFileAtomic(filepath.Join(opDir, runMetaFile), rt.RunMetaFor(op)); err != nil {
		return err
	}
	spec, ok, err := rt.managedLaunchSpec(op)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	// 本地 CLI 节点：任务卡落到任务 worktree，并把 CLI 的 cwd 指到那里。
	// 显式 launch_spec/launch_cmd 不注入任务卡，避免改变运维自定义入口的语义。
	explicit := os.Getenv(managedLaunchSpecEnv) != "" || os.Getenv(managedLaunchCmdEnv) != ""
	if !explicit {
		if node, ok := rt.assignedNode(op); ok && metaBool(node.Meta, "task_card", false) {
			if err := rt.prepareTaskWorkspace(op, opDir, node, &spec); err != nil {
				return err
			}
		}
	}
	// Pi 节点：任务目录与任务卡已就绪后再生成 stdin 配置（里面要带 worktree 路径与范围）。
	if node, ok := rt.assignedNode(op); ok && metaString(node.Meta, "adapter_kind") == "pi" {
		if err := rt.applyPiLaunch(op, node, &spec); err != nil {
			return err
		}
	}
	specPath := filepath.Join(opDir, launchSpecFile)
	if _, statErr := os.Stat(specPath); statErr == nil {
		return nil
	}
	return writeJSONFileAtomic(specPath, spec)
}

// RunMetaFor 按操作身份与工程上下文生成 run_meta 内容（导出以便测试与运维核对）。
//
// 兜底与 `__runner` 一致：缺 run_id 用操作号推导，缺实例/代际按系统 Agent 的最小值，
// 这样「文件里写的」与「Runner 自己猜的」永远是同一个值。
func (rt *Runtime) RunMetaFor(op dispatch.Operation) RunMeta {
	meta := RunMeta{
		OperationID: string(op.OperationID),
		RunID:       string(op.RunID),
		InstanceID:  string(op.Instance),
		Generation:  int(op.Generation),
		ProjectID:   string(op.ProjectID),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if rt != nil && rt.pc != nil {
		meta.GlobalDB = rt.pc.GlobalDB
		if meta.ProjectID == "" {
			meta.ProjectID = rt.pc.ID()
		}
	}
	if meta.RunID == "" {
		meta.RunID = runIDForOperation(meta.OperationID)
	}
	if meta.InstanceID == "" {
		meta.InstanceID = "ti_systemagent"
	}
	if meta.Generation < 1 {
		meta.Generation = 1
	}
	return meta
}

// managedLaunchSpec 读显式来源（环境变量 → 占用节点 meta.argv）并补默认值。
func (rt *Runtime) managedLaunchSpec(op dispatch.Operation) (model.LaunchSpec, bool, error) {
	var spec model.LaunchSpec
	if raw := strings.TrimSpace(os.Getenv(managedLaunchSpecEnv)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			return model.LaunchSpec{}, true, errx.New(
				managedLaunchSpecEnv+" 不是合法的 LaunchSpec JSON: "+err.Error(),
				"按 model.LaunchSpec 的字段传 JSON，或改用 "+managedLaunchCmdEnv)
		}
	} else if raw := strings.TrimSpace(os.Getenv(managedLaunchCmdEnv)); raw != "" {
		var argv []string
		if err := json.Unmarshal([]byte(raw), &argv); err != nil {
			return model.LaunchSpec{}, true, errx.New(
				managedLaunchCmdEnv+" 必须是 JSON 数组: "+err.Error(),
				`例如 ["/usr/bin/env","codebuddy","--headless"]`)
		}
		// 契约要求 argv 非空：只给可执行文件说明调用方少写了参数，宁可直接失败也别写坏文件。
		if len(argv) < 2 {
			return model.LaunchSpec{}, true, errx.New(
				managedLaunchCmdEnv+" 至少要有可执行文件与一个参数",
				`例如 ["/usr/bin/env","codebuddy","--headless"]`)
		}
		spec = model.LaunchSpec{Executable: argv[0], Argv: argv[1:]}
	} else if argv, ok := rt.argvFromAssignedNode(op); ok {
		if len(argv) < 2 {
			return model.LaunchSpec{}, true, errx.New(
				"节点 meta.argv 至少要有可执行文件与一个参数",
				`maw node register --cmd '["/bin/echo","ok"]'`)
		}
		spec = model.LaunchSpec{Executable: argv[0], Argv: argv[1:]}
	} else {
		return model.LaunchSpec{}, false, nil
	}
	rt.fillLaunchDefaults(&spec)
	if errs := spec.Validate(); len(errs) > 0 {
		return model.LaunchSpec{}, true, errx.New(
			"launch_spec 未通过契约校验: "+model.Err(errs).Error(),
			"核对 executable 绝对路径、argv 非空、protocol 与 stdin_mode")
	}
	return spec, true, nil
}

// argvFromAssignedNode 从本 Run 的节点槽占用找到节点，再读 meta.argv。
func (rt *Runtime) argvFromAssignedNode(op dispatch.Operation) ([]string, bool) {
	n, ok := rt.assignedNode(op)
	if !ok || n.Meta == nil {
		return nil, false
	}
	raw, ok := n.Meta["argv"]
	if !ok {
		return nil, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var argv []string
	if err := json.Unmarshal(b, &argv); err != nil || len(argv) == 0 {
		return nil, false
	}
	argv = withModelArgs(argv, n.Meta, rt.modelOverride)
	return argv, true
}

// assignedNode 从本 Run 的节点槽占用反查节点行。
func (rt *Runtime) assignedNode(op dispatch.Operation) (*db.Node, bool) {
	if rt == nil || rt.pc == nil || strings.TrimSpace(rt.pc.GlobalDB) == "" {
		return nil, false
	}
	runID := strings.TrimSpace(string(op.RunID))
	if runID == "" {
		return nil, false
	}
	var node *db.Node
	err := db.WithReadTx(rt.pc.GlobalDB, func(tx *sql.Tx) error {
		leases, e := db.ListActiveLeases(tx, string(op.ProjectID))
		if e != nil {
			return e
		}
		nodeID := ""
		for _, lease := range leases {
			if lease == nil || lease.RunID != runID {
				continue
			}
			if lease.Kind != model.LeaseNodeSlot {
				continue
			}
			nodeID = nodeIDFromLeaseKey(lease.ResourceKey)
			break
		}
		if nodeID == "" {
			return nil
		}
		n, e := db.GetNode(tx, nodeID)
		if e != nil {
			return e
		}
		node = n
		return nil
	})
	if err != nil || node == nil {
		return nil, false
	}
	return node, true
}

// withModelArgs 在尚未显式带模型参数时注入模型旗标。
// 预设 argv 的任务提示在末项，模型参数插到它前面，避免被 CLI 当成提示文本。
func withModelArgs(argv []string, meta map[string]any, override string) []string {
	if len(argv) == 0 || meta == nil {
		return argv
	}
	modelName := effectiveModel(meta, override)
	if modelName == "" {
		return argv
	}
	for _, a := range argv {
		if a == "--model" || a == "-m" || strings.HasPrefix(a, "--model=") {
			return argv
		}
	}
	flavor, _ := meta["cli_flavor"].(string)
	if strings.EqualFold(strings.TrimSpace(flavor), "dsh") {
		// dsh 没有 --model；模型由 prepareTaskWorkspace 写 profile patch 注入。
		return argv
	}
	args := modelArgsFor(flavor, modelName)
	if len(args) == 0 {
		return argv
	}
	out := append([]string{}, argv...)
	if metaBool(meta, "prompt_last", false) && len(out) >= 2 {
		idx := len(out) - 1
		withArgs := append([]string{}, out[:idx]...)
		withArgs = append(withArgs, args...)
		withArgs = append(withArgs, out[idx:]...)
		return withArgs
	}
	return append(out, args...)
}

// effectiveModel 优先取本次启动覆盖，其次取节点默认模型。
func effectiveModel(meta map[string]any, override string) string {
	if m := strings.TrimSpace(override); m != "" {
		return m
	}
	if meta == nil {
		return ""
	}
	m, _ := meta["default_model"].(string)
	return strings.TrimSpace(m)
}

// prepareTaskWorkspace 把任务卡落到任务 worktree，作为本地 CLI 的执行目录。
//
// 领取后的任务必须先有目录与 TASK.md：否则 CLI 即使被拉起，也只能在空目录里空转，
// 或者回到主仓改动 write_scope 之外的文件。
func (rt *Runtime) prepareTaskWorkspace(op dispatch.Operation, opDir string, node *db.Node, spec *model.LaunchSpec) error {
	if rt == nil || rt.pc == nil || spec == nil {
		return nil
	}
	projectID := strings.TrimSpace(string(op.ProjectID))
	if projectID == "" {
		projectID = rt.pc.ID()
	}
	task, all, err := rt.taskForInstance(projectID, string(op.Instance))
	if err != nil {
		return err
	}
	if task == nil {
		// 没有实例身份的系统操作不写任务卡，保持原有启动语义。
		return nil
	}
	agent := strings.TrimSpace(task.Owner)
	if agent == "" {
		agent = "system-agent-1"
	}
	wtRel := strings.TrimSpace(task.Worktree)
	branch := strings.TrimSpace(task.Branch)
	if wtRel == "" {
		wtRel = filepath.Join(".worktrees", agent)
	}
	if branch == "" {
		branch = "agent/" + agent + "/" + task.ID
	}

	fresh := task.Status == string(model.TaskClaimed)
	wt, err := ensureClaimWorktree(rt.pc, wtRel, branch, fresh)
	if err != nil {
		return err
	}
	if fresh {
		// claimed 表示「已归属、尚未开工」；目录就绪后才推进到 in_progress。
		if err := advanceClaim(rt.pc, task.ID, agent); err != nil {
			return err
		}
	}
	if task, all, err = rt.taskForInstance(projectID, string(op.Instance)); err != nil {
		return err
	}
	if task == nil {
		return errx.New("任务在准备执行目录时已不存在: "+string(op.Instance), "maw task list")
	}
	if err := writeTaskCardWithDeps(wt, task, agent, all); err != nil {
		return err
	}
	if err := rt.bindTaskWorkspace(projectID, task.ID, branch, wtRel); err != nil {
		return err
	}
	spec.Cwd = wt
	return rt.applyDshModelPatch(node, spec, wt)
}

// piLaunchConfig 是写进运行时 stdin 的一次执行配置（与 runtimes/pi 的约定一致）。
type piLaunchConfig struct {
	Mode       string   `json:"mode"`
	Cwd        string   `json:"cwd"`
	RunID      string   `json:"run_id"`
	Generation int      `json:"generation"`
	WriteScope []string `json:"write_scope"`
	ReadScope  []string `json:"read_scope"`
	AllowBash  bool     `json:"allow_bash"`
	Model      string   `json:"model,omitempty"`
	Prompt     string   `json:"prompt"`
}

// applyPiLaunch 把 Pi 节点的启动规格补成「配置走 stdin、协议走 pi-stream-json」。
//
// 关键点：
//   - write_scope 来自任务卡，运行时据此在工具边界拒绝越界写（契约 §6）；
//   - 模型提示不放 argv：设了 prompt_last 也只会让 argv 变成无意义参数；
//   - 协议显式写成 pi-stream-json/1，SourceFor 才会选 Pi 适配器。
func (rt *Runtime) applyPiLaunch(op dispatch.Operation, node *db.Node, spec *model.LaunchSpec) error {
	if rt == nil || rt.pc == nil || spec == nil || node == nil {
		return nil
	}
	projectID := strings.TrimSpace(string(op.ProjectID))
	if projectID == "" {
		projectID = rt.pc.ID()
	}
	cfg := piLaunchConfig{
		Mode:       "worker",
		Cwd:        spec.Cwd,
		RunID:      string(op.RunID),
		Generation: int(op.Generation),
		Prompt:     taskPrompt,
		Model:      effectiveModel(node.Meta, rt.modelOverride),
	}
	if task, _, err := rt.taskForInstance(projectID, string(op.Instance)); err != nil {
		return err
	} else if task != nil {
		cfg.WriteScope = append([]string{}, task.WriteScope...)
		cfg.ReadScope = append([]string{}, task.ReadScope...)
	}
	cfg.AllowBash = metaBool(node.Meta, "allow_bash", false)
	if cfg.RunID == "" {
		cfg.RunID = runIDForOperation(string(op.OperationID))
	}
	if cfg.Generation < 1 {
		cfg.Generation = 1
	}
	if strings.TrimSpace(cfg.Cwd) == "" {
		cfg.Cwd = spec.Cwd
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return errx.New("无法编码 Pi 运行配置: "+err.Error(), "检查任务 worktree 与 write_scope")
	}
	spec.StdinMode = model.StdinPipe
	spec.StdinData = string(data)
	spec.ExpectedProtocol = model.Protocol{Format: pi.ProtocolFormat, Version: pi.ProtocolVersion}
	env := append([]string{}, spec.EnvAllowlist...)
	env = append(env, pi.ModelEnvAllowlist...)
	spec.EnvAllowlist = env
	return nil
}

// taskForInstance 用实例身份反查任务行，并同时返回全量任务（写任务卡时需要 deps）。
func (rt *Runtime) taskForInstance(projectID, instanceID string) (*db.Task, []*db.Task, error) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return nil, nil, nil
	}
	var all []*db.Task
	var found *db.Task
	err := db.WithReadTx(rt.pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		all, e = db.ListTasks(tx, projectID)
		if e != nil {
			return e
		}
		for _, t := range all {
			if strings.TrimSpace(t.InstanceID) == instanceID {
				found = t
				break
			}
		}
		return nil
	})
	return found, all, err
}

func (rt *Runtime) bindTaskWorkspace(projectID, taskID, branch, worktree string) error {
	return db.WithTx(rt.pc.GlobalDB, func(tx *sql.Tx) error {
		return db.BindTaskWorkspace(tx, projectID, taskID, branch, worktree)
	})
}

// applyDshModelPatch 用 dsh 的 profile patch 覆盖默认模型（dsh 没有 --model 旗标）。
func (rt *Runtime) applyDshModelPatch(node *db.Node, spec *model.LaunchSpec, wt string) error {
	if node == nil || node.Meta == nil {
		return nil
	}
	flavor, _ := node.Meta["cli_flavor"].(string)
	if !strings.EqualFold(strings.TrimSpace(flavor), "dsh") {
		return nil
	}
	modelName := effectiveModel(node.Meta, rt.modelOverride)
	if modelName == "" {
		return nil
	}
	for _, a := range spec.Argv {
		if a == "--patch" {
			return nil
		}
	}
	dir := filepath.Join(wt, ".maw")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "dsh-model.yml")
	content := "- id: agent-default-model\n  config:\n    model: " + strconv.Quote(modelName) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	args := []string{"--patch", path}
	if metaBool(node.Meta, "prompt_last", false) && len(spec.Argv) >= 1 {
		idx := len(spec.Argv) - 1
		next := append([]string{}, spec.Argv[:idx]...)
		next = append(next, args...)
		next = append(next, spec.Argv[idx:]...)
		spec.Argv = next
	} else {
		spec.Argv = append(spec.Argv, args...)
	}
	return nil
}

// nodeIDFromLeaseKey 解析 node:<id>#<slot>。
func nodeIDFromLeaseKey(key string) string {
	key = strings.TrimSpace(key)
	if !strings.HasPrefix(key, "node:") {
		return ""
	}
	rest := strings.TrimPrefix(key, "node:")
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// fillLaunchDefaults 只填「调用方没写」的字段，绝不覆盖显式值。
func (rt *Runtime) fillLaunchDefaults(spec *model.LaunchSpec) {
	if spec.Cwd == "" && rt != nil && rt.pc != nil && rt.pc.Repo != "" {
		cwd := rt.pc.Repo
		if abs, err := filepath.Abs(cwd); err == nil {
			cwd = abs
		}
		spec.Cwd = cwd
	}
	if spec.StdinMode == "" {
		spec.StdinMode = model.StdinNone
	}
	if len(spec.EnvAllowlist) == 0 {
		spec.EnvAllowlist = append([]string{}, managedLaunchEnvAllowlist...)
	}
	if spec.ExpectedProtocol.Format == "" {
		spec.ExpectedProtocol.Format = "passthrough"
	}
	if spec.ExpectedProtocol.Version == "" {
		spec.ExpectedProtocol.Version = "1"
	}
}

// runIDForOperation 与 `__runner` 的兜底推导保持一致：op_xxx → run_xxx。
func runIDForOperation(opID string) string {
	id := strings.TrimSpace(opID)
	if strings.HasPrefix(id, "run_") {
		return id
	}
	return "run_" + strings.TrimPrefix(id, "op_")
}

// writeJSONFileAtomic 先写同目录临时文件再 rename，避免 Runner 读到半截 JSON。
func writeJSONFileAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".maw-launch-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func (l runnerLauncher) Launch(ctx context.Context, req dispatch.LaunchRequest) (dispatch.ProcessIdentity, error) {
	if err := ctx.Err(); err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	if l.rt == nil {
		return dispatch.ProcessIdentity{}, errx.New("runtime missing", "")
	}
	if err := l.rt.AssertPlatform(); err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	opID := string(req.Operation.OperationID)
	opDir := l.rt.OpDir(opID)
	if err := os.MkdirAll(opDir, 0o755); err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	token, err := process.NewNonce()
	if err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	tokenPath := filepath.Join(opDir, runnerTokenFile)
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	// Runner 只读文件：元数据与启动规格必须在起进程之前落盘，否则本次启动必然空转。
	if err := l.rt.PrepareRunnerArtifacts(opDir, req.Operation); err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	repo := l.rt.pc.Repo
	if !filepath.IsAbs(repo) {
		abs, err := filepath.Abs(repo)
		if err != nil {
			return dispatch.ProcessIdentity{}, err
		}
		repo = abs
	}
	// process.Start 会 Setpgid：中枢退出不会带走 Runner。
	p, err := process.Start(process.StartSpec{
		RunID: opID,
		Argv: []string{
			l.rt.selfBin,
			"__runner",
			"--operation", opID,
			"--nonce", req.Nonce,
			"--state-dir", l.rt.stateDir,
			"--token", token,
		},
		Dir:   repo,
		Nonce: req.Nonce,
		Env:   []string{runnerTokenEnv + "=" + token},
	})
	if err != nil {
		return dispatch.ProcessIdentity{}, err
	}
	started := p.Identity.Start
	if started.IsZero() {
		started = time.Now().UTC()
	}
	return dispatch.ProcessIdentity{
		PID:       p.Identity.PID,
		PGID:      p.Identity.PGID,
		Nonce:     req.Nonce,
		StartedAt: started,
	}, nil
}

// BuildCoordinator 装配一个使用独立 Runner 启动端口的协调者。
func (rt *Runtime) BuildCoordinator(queue dispatch.Queue) (*dispatch.Coordinator, error) {
	if queue == nil {
		return nil, errx.New("runtime: missing queue", "")
	}
	return dispatch.New(dispatch.Config{
		StateDir:    rt.stateDir,
		Coordinator: fmt.Sprintf("maw-hub-%d", os.Getpid()),
		Queue:       queue,
		Launcher:    runnerLauncher{rt: rt},
	})
}

// EnsureManagedAllowed 检查配置与平台。
//
// explicitTask=true 表示调用方指定了任务／节点：即使 auto_enabled=false 也允许
// （显式启动 ≠ 自动选题）。未指定任务时要求 auto_enabled。
func (rt *Runtime) EnsureManagedAllowed(explicitTask bool) error {
	if rt == nil || rt.pc == nil || rt.pc.Config == nil {
		return errx.New("runtime: no config", "maw init")
	}
	view := rt.ManagedView()
	if !explicitTask {
		if enabled, _ := view["auto_enabled"].(bool); !enabled {
			return i18n.E("err.managed_auto_off", "next.managed_explicit")
		}
	}
	limit, _ := view["managed_execution_limit"].(int)
	if limit < 1 {
		return i18n.E("err.managed_limit_required", "next.fix_toml")
	}
	return rt.AssertPlatform()
}

// ---- 扩展能力开关（VN-956）----
//
// 原则只有一条：**证据决定上限，配置只能往下压**。
//
//   - `unsupported` 没有实测证据，任何配置、任何环境变量都开不起来；
//   - `experimental` 有接口与单测但缺真实闭环证据，默认跟随「扩展开关」，
//     且仍受平台（受管执行只在 macOS 验证过）与工程类型（非 Git 没有基线可比）
//     这两条工程策略约束；
//   - `verified` 是已验收的闭环（人工领取、CodeBuddy 来源、只读观测 API），
//     关闭扩展也不影响它们——这正是「关闭扩展仍保留人工与首个闭环」的实现处。

// ExtensionLevel 是能力的证据级别。
type ExtensionLevel string

const (
	// ExtVerified 有已通过的真实闭环证据。
	ExtVerified ExtensionLevel = "verified"
	// ExtExperimental 已接线但缺真实闭环证据，只能随扩展开关开启。
	ExtExperimental ExtensionLevel = "experimental"
	// ExtUnsupported 没有证据（或平台根本不支持），配置不得强行开启。
	ExtUnsupported ExtensionLevel = "unsupported"
)

// ExtensionSpec 是一个扩展能力／来源的注册项。
type ExtensionSpec struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Level ExtensionLevel `json:"level"`
	// Platforms 为空表示不限平台；否则只在这些 GOOS 上有效。
	Platforms []string `json:"platforms,omitempty"`
	// RequiresGit 表示该能力依赖 Git 基线（非 Git 工程下不成立）。
	RequiresGit bool `json:"requires_git,omitempty"`
	// RequiresAuto 表示该能力只在 auto_enabled 打开时才有意义。
	RequiresAuto bool   `json:"requires_auto,omitempty"`
	Evidence     string `json:"evidence,omitempty"`
}

// ExtensionState 是注册项在当前环境下的有效开关与原因。
type ExtensionState struct {
	ExtensionSpec
	Enabled bool `json:"enabled"`
	// Reason 是稳定机器码，便于看板与测试断言，不用文案猜。
	Reason string `json:"reason"`
}

// 原因码（封闭集合）。
const (
	ReasonEnabled      = "enabled"
	ReasonNoEvidence   = "no_evidence"
	ReasonPlatform     = "platform_unsupported"
	ReasonNotGit       = "workspace_not_git"
	ReasonAutoDisabled = "auto_disabled"
	ReasonExtendedOff  = "extended_disabled"
)

// ExtendedEnvVar 是「扩展开关」的环境变量名：`off`／`0`／`false` 关闭全部实验性能力。
//
// 用环境变量而不是配置字段：本卡不动配置契约（internal/config 不在 Surface 内），
// 但关闭扩展必须能立刻生效且不需要改仓库文件。
const ExtendedEnvVar = "MAW_EXTENDED"

// Extensions 返回扩展能力注册表。
//
// 注册表是封闭的：能力必须有 ID、级别与证据说明；级别不在这三个取值里的配置
// 不该存在，扩展一律按 unsupported 处理（失败安全）。
func Extensions() []ExtensionSpec {
	return []ExtensionSpec{
		{
			ID: "source.manual", Name: "人工领取闭环", Level: ExtVerified,
			Evidence: "docs/maw_vnext_可执行任务清单.md#VN-902（人工领取／提交／合入全链路已验收）",
		},
		{
			ID: "source.codebuddy", Name: "CodeBuddy 来源", Level: ExtVerified,
			Evidence: "docs/maw_vnext_可执行任务清单.md#VN-901（首个 CodeBuddy 闭环）",
		},
		{
			ID: "observability.api", Name: "只读观测与提醒 API", Level: ExtVerified,
			Evidence: "internal/httpapi/observability（VN-606 单测 + 本卡接线）",
		},
		{
			ID: "source.zcode", Name: "ZCode 来源", Level: ExtExperimental,
			RequiresGit: true,
			Evidence:    "docs/evidence/zcode/capability-matrix.json（模型调用仍 unobserved）",
		},
		{
			ID: "source.trae", Name: "Trae 来源", Level: ExtExperimental,
			RequiresGit: true,
			Evidence:    "internal/adapters/trae（仅接口与单测）",
		},
		{
			ID: "source.workbuddy", Name: "WorkBuddy 来源", Level: ExtExperimental,
			RequiresGit: true,
			Evidence:    "internal/adapters/workbuddy（仅接口与单测）",
		},
		{
			ID: "dispatch.auto", Name: "自动匹配与派发", Level: ExtExperimental,
			Platforms:    []string{"darwin"},
			RequiresGit:  true,
			RequiresAuto: true,
			Evidence:     "docs/maw_vnext_可执行任务清单.md#VN-802（受管执行仅 macOS 实测）",
		},
		{
			ID: "workspace.nongit", Name: "非 Git 工作区", Level: ExtExperimental,
			RequiresAuto: true,
			Evidence:     "internal/workspace/nongit（无远端基线，不能自动合入）",
		},
		{
			ID: "dispatch.model_invoke", Name: "受管模型调用", Level: ExtUnsupported,
			Evidence: "来源适配器均报 unobserved（见各 capability-matrix.json），无实测证据",
		},
	}
}

// extendedEnabled 读扩展开关；未设置按开启（默认行为不变）。
func extendedEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ExtendedEnvVar))) {
	case "off", "0", "false", "no", "disabled":
		return false
	}
	return true
}

// workspaceIsGit 报告工程目录是否有 Git 基线。
//
// 只看 `.git` 存在（文件或目录都算：worktree 的 `.git` 是文件），
// 不去跑 `git rev-parse`：这里只是能力判定，不需要也不该启动外部进程。
func workspaceIsGit(repo string) bool {
	if strings.TrimSpace(repo) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(repo, ".git"))
	return err == nil
}

// ExtensionStates 按当前平台、工程与配置算出每个注册项的有效开关。
//
// 判定顺序固定，原因码只报第一条不成立的：越基础的条件越先判定，
// 这样「为什么没开」永远是可解释的一句话。
func (rt *Runtime) ExtensionStates() []ExtensionState {
	// 没有工程上下文就没有「这个工程开了什么」：返回空表而不是一堆 enabled，
	// 否则看板会把「不知道」显示成「全开」。
	if rt == nil || rt.pc == nil {
		return []ExtensionState{}
	}
	extended := extendedEnabled()
	platform := runtime.GOOS
	git := workspaceIsGit(rt.pc.Repo)
	auto := false
	if rt.pc.Config != nil {
		auto, _ = rt.pc.Config.AutoExecution()
	}

	specs := Extensions()
	out := make([]ExtensionState, 0, len(specs))
	for _, spec := range specs {
		state := ExtensionState{ExtensionSpec: spec, Enabled: true, Reason: ReasonEnabled}
		switch {
		case spec.Level == ExtUnsupported || (spec.Level != ExtVerified && spec.Level != ExtExperimental):
			state.Enabled, state.Reason = false, ReasonNoEvidence
		case len(spec.Platforms) > 0 && !containsString(spec.Platforms, platform):
			state.Enabled, state.Reason = false, ReasonPlatform
		case spec.RequiresGit && !git:
			state.Enabled, state.Reason = false, ReasonNotGit
		case spec.RequiresAuto && !auto:
			state.Enabled, state.Reason = false, ReasonAutoDisabled
		case spec.Level == ExtExperimental && !extended:
			state.Enabled, state.Reason = false, ReasonExtendedOff
		}
		out = append(out, state)
	}
	return out
}

// ExtensionEnabled 报告单个能力当前是否有效开启；未注册的 ID 一律 false。
func (rt *Runtime) ExtensionEnabled(id string) bool {
	for _, st := range rt.ExtensionStates() {
		if st.ID == id {
			return st.Enabled
		}
	}
	return false
}

// ExtensionsView 是给命令与看板用的视图（与 ManagedView 同风格）。
func (rt *Runtime) ExtensionsView() map[string]any {
	states := rt.ExtensionStates()
	items := make([]map[string]any, 0, len(states))
	for _, st := range states {
		items = append(items, map[string]any{
			"id": st.ID, "name": st.Name, "level": string(st.Level),
			"enabled": st.Enabled, "reason": st.Reason, "evidence": st.Evidence,
		})
	}
	return map[string]any{
		"extended_enabled": extendedEnabled(),
		"extensions":       items,
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
