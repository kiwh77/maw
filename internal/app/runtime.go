package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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
	spec, ok, err := rt.managedLaunchSpec()
	if err != nil {
		return err
	}
	if !ok {
		return nil
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

// managedLaunchSpec 读显式来源并补默认值；第二个返回值表示「是否有来源」。
func (rt *Runtime) managedLaunchSpec() (model.LaunchSpec, bool, error) {
	var spec model.LaunchSpec
	if raw := strings.TrimSpace(os.Getenv(managedLaunchSpecEnv)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			return model.LaunchSpec{}, true, errx.New(
				managedLaunchSpecEnv+" 不是合法的 LaunchSpec JSON: "+err.Error(),
				"按 model.LaunchSpec 的字段传 JSON，或改用 "+managedLaunchCmdEnv)
		}
	} else {
		raw := strings.TrimSpace(os.Getenv(managedLaunchCmdEnv))
		if raw == "" {
			return model.LaunchSpec{}, false, nil
		}
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
	}
	rt.fillLaunchDefaults(&spec)
	if errs := spec.Validate(); len(errs) > 0 {
		return model.LaunchSpec{}, true, errx.New(
			"launch_spec 未通过契约校验: "+model.Err(errs).Error(),
			"核对 executable 绝对路径、argv 非空、protocol 与 stdin_mode")
	}
	return spec, true, nil
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
