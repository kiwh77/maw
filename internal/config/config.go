package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
)

var mainBranchLine = regexp.MustCompile(`(?m)^(\s*main_branch\s*=\s*)"[^"]*"`)

type Config struct {
	Project   Project   `toml:"project"`
	Approvals Approvals `toml:"approvals"`
	Gates     Gates     `toml:"gates"`
	Locks     Locks     `toml:"locks"`
	Launcher  Launcher  `toml:"launcher"`
	Identity  Identity  `toml:"identity"`
	// Managed 是受管执行开关与硬上限（契约 auto_execution）。
	Managed Managed `toml:"managed"`
	// SystemAgent 是阻塞自愈 / 拆题编排的系统 Agent 层。
	SystemAgent SystemAgent `toml:"system_agent"`
}

// Managed 控制受管 CLI 执行。默认关闭自动模式，上限为 1。
type Managed struct {
	// ConfigVersion 是本段配置的版本号，便于迁移与看板展示。
	ConfigVersion int `toml:"config_version"`
	// AutoEnabled 为真时才允许自动派发循环；默认 false。
	// 它只管派发，不控制系统 Agent 的阻塞自愈（见 SystemAgent.Enabled）。
	AutoEnabled bool `toml:"auto_enabled"`
	// AutoSelect 是自动选题生产者开关；默认 false（契约 §9 默认全关）。
	// 与 AutoEnabled 分离：前者生产 dispatch_ready 作业，后者消费已排队的派发操作；
	// 也与 SystemAgent.Enabled 分离，不依赖阻塞自愈开关。
	AutoSelect bool `toml:"auto_select"`
	// Limit 是受管执行的全局硬上限；auto_enabled=true 时必须 ≥1。
	Limit int `toml:"limit"`
	// AllowedNodes 限制本工程自动派发可用的节点；空表示不限制。
	AllowedNodes []string `toml:"allowed_nodes"`
}

// SystemAgent 控制系统 Agent 反应总线。默认关闭。
//
// 它负责阻塞自愈 / 拆题 / 运维编排，**不负责**把任务自动派发出去——
// 那件事只看 Managed.AutoEnabled。两个开关互不推导，可各自单独打开。
type SystemAgent struct {
	ConfigVersion   int      `toml:"config_version"`
	Enabled         bool     `toml:"enabled"`
	PollIntervalSec int      `toml:"poll_interval_sec"`
	MaxConcurrent   int      `toml:"max_concurrent"`
	LLMNode         string   `toml:"llm_node"`
	WorkerNode      string   `toml:"worker_node"`
	LLMCommand      []string `toml:"llm_command"`
	// DiagnoseTimeoutSec 是单次诊断调用的超时预算（秒）。
	//
	// Pi 诊断与旧的 llm_command 共用这个预算：超时不是「没消息」，必须留下
	// 可审计结论（契约 maw.pi.execution/v1 §7）。0 或负数按 180 秒处理，
	// 这样旧配置不写这一项也能读。
	DiagnoseTimeoutSec int      `toml:"diagnose_timeout_sec"`
	AutoKinds          []string `toml:"auto_kinds"`
	EscalatePatterns   []string `toml:"escalate_patterns"`
	CooldownoutSec     int      `toml:"cooldown_sec"`
	MaxAttempts        int      `toml:"max_attempts"`
	OpsAllowlist       []string `toml:"ops_allowlist"`
}

type Project struct {
	MainBranch    string `toml:"main_branch"`
	MergeStrategy string `toml:"merge_strategy"`
	MaxParallel   int    `toml:"max_parallel"`
	Blueprint     string `toml:"blueprint"`
}

type Approvals struct {
	Plan     string `toml:"plan"`
	Merge    string `toml:"merge"`
	Teardown string `toml:"teardown"`
	Review   string `toml:"review"`
}

type Gates struct {
	Commands   []string `toml:"commands"`
	TimeoutSec int      `toml:"timeout_sec"`
}

type Locks struct {
	TTLSec          int `toml:"ttl_sec"`
	HeartbeatTTLSec int `toml:"heartbeat_ttl_sec"`
}

type Launcher struct {
	Mode string `toml:"mode"`
}

type Identity struct {
	Workers    []string `toml:"workers"`
	Reviewers  []string `toml:"reviewers"`
	Integrator string   `toml:"integrator"`
	// Leads 是本机「lead 能力标记」名单（toml 键 leads）。
	// 它只是给同一个 worker 多一项授权，不新增角色：不改 Role() 返回值、
	// 不改 agents 表结构；integrator / reviewer 也不会自动成为 lead。
	Leads []string `toml:"leads"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, i18n.E("err.config_missing", "next.run_init", path)
	}
	var c Config
	if err := toml.Unmarshal(b, &c); err != nil {
		return nil, i18n.EW("err.config_invalid", "next.fix_toml", err)
	}
	if c.Project.MainBranch == "" {
		c.Project.MainBranch = "main"
	}
	// max_parallel：看板参考值；0 表示不展示建议槽位。领取不再受此限制。
	if c.Identity.Integrator == "" {
		c.Identity.Integrator = "int"
	}
	if c.Approvals.Review == "" {
		c.Approvals.Review = "auto"
	}
	if c.Approvals.Plan == "" {
		c.Approvals.Plan = "manual"
	}
	if c.Approvals.Merge == "" {
		c.Approvals.Merge = "manual"
	}
	if c.Gates.TimeoutSec == 0 {
		c.Gates.TimeoutSec = 900
	}
	if c.Managed.ConfigVersion == 0 {
		c.Managed.ConfigVersion = 1
	}
	// 默认关闭自动模式；显式开启时 Limit 至少为 1（E270）。
	if c.Managed.Limit <= 0 {
		c.Managed.Limit = 1
	}
	if c.Managed.AutoEnabled && c.Managed.Limit < 1 {
		return nil, i18n.E("err.managed_limit_required", "next.fix_toml")
	}
	if c.SystemAgent.ConfigVersion == 0 {
		c.SystemAgent.ConfigVersion = 1
	}
	if c.SystemAgent.PollIntervalSec <= 0 {
		c.SystemAgent.PollIntervalSec = 5
	}
	if c.SystemAgent.MaxConcurrent <= 0 {
		c.SystemAgent.MaxConcurrent = 1
	}
	if c.SystemAgent.CooldownoutSec <= 0 {
		c.SystemAgent.CooldownoutSec = 300
	}
	if c.SystemAgent.MaxAttempts <= 0 {
		c.SystemAgent.MaxAttempts = 3
	}
	if c.SystemAgent.DiagnoseTimeoutSec <= 0 {
		c.SystemAgent.DiagnoseTimeoutSec = 180
	}
	if len(c.SystemAgent.AutoKinds) == 0 {
		c.SystemAgent.AutoKinds = []string{"unblock_assist"}
	}
	if len(c.SystemAgent.EscalatePatterns) == 0 {
		c.SystemAgent.EscalatePatterns = []string{"needs_human", "auth_required"}
	}
	if len(c.SystemAgent.OpsAllowlist) == 0 {
		c.SystemAgent.OpsAllowlist = []string{"maw next", "maw merge --yes", "maw prune", "maw task unblock"}
	}
	return &c, nil
}

// AutoExecution 把配置映射为运行时契约里的 auto_execution 视图。
func (c *Config) AutoExecution() (enabled bool, limit int) {
	if c == nil {
		return false, 1
	}
	return c.Managed.AutoEnabled, c.Managed.Limit
}

// ---- 两个开关的分工（SA-004）----
//
// 配置里有两处看起来都像「要不要自动干活」的开关，它们驱动的是**两条不同的循环**，
// 谁也推导不出谁：
//
//	managed.auto_enabled    管「自动选题派发」：只有它为真，serve 里的 leader 才推进
//	                        派发队列，把可领取的任务自动派给受管执行；它不决定系统
//	                        Agent 是否工作。
//	system_agent.enabled    管「阻塞自愈 / 拆题 / 运维编排」：只有它为真，leader 才按
//	                        poll_interval_sec 轮询系统 Agent；它不推进派发队列。
//
// 两者共用同一个 leader 与同一把 coordinator.lock（一个状态目录只允许一个 leader），
// 但开关互不影响：只开自愈＝任务卡住会被处理、但不会被自动派出去；只开派发＝
// 任务会被自动派出去、但卡住不会自愈；两个都关＝完全不自动跑（默认）。
//
// 名字里带 Dispatch / SystemAgent 的两个方法就是这两句话的唯一实现处：循环与视图
// 都从这里取值，别再各自去读 c.Managed.AutoEnabled / c.SystemAgent.Enabled——
// 那样一改语义就要全仓库找一遍，而漏掉的那一处正是事故来源。

// DispatchEnabled 报告自动选题派发是否应当推进，以及它受哪个硬上限约束。
//
// 它只回答「派发循环开不开」，不回答「受管执行在这台机器上跑不跑得起来」：
// 平台与上限的可用性由调用方（serve leader / matching）再判一次。
func (c *Config) DispatchEnabled() (enabled bool, limit int) {
	return c.AutoExecution()
}

// SystemAgentEnabled 报告系统 Agent 循环（阻塞自愈 / 拆题 / 运维编排）是否开启。
//
// 与 DispatchEnabled 无关：自愈开着不代表任务会被自动派发，反之亦然。
func (c *Config) SystemAgentEnabled() bool {
	if c == nil {
		return false
	}
	return c.SystemAgent.Enabled
}

// AutomationSummary 一次给出两条循环的开关，供状态输出与看板解释「为什么没自动跑」。
//
// 返回顺序固定为 (dispatch, system)：两个都为假时，任何自动行为都不该发生。
func (c *Config) AutomationSummary() (dispatch bool, system bool) {
	dispatch, _ = c.DispatchEnabled()
	return dispatch, c.SystemAgentEnabled()
}

// DiagnoseNode 返回显式配置的诊断节点（system_agent.llm_node）。
//
// 空表示没有配置 Pi 诊断后端：此时系统 Agent 按旧路径（llm_command）或安全降级。
func (c *Config) DiagnoseNode() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.SystemAgent.LLMNode)
}

// DiagnoseTimeout 返回单次诊断调用的超时预算；非正值回落到 180 秒。
func (c *Config) DiagnoseTimeout() time.Duration {
	if c == nil {
		return defaultDiagnoseTimeout
	}
	return c.SystemAgent.DiagnoseTimeout()
}

// DiagnoseTimeout 返回单次诊断调用的超时预算；非正值回落到 180 秒。
func (s SystemAgent) DiagnoseTimeout() time.Duration {
	if s.DiagnoseTimeoutSec <= 0 {
		return defaultDiagnoseTimeout
	}
	return time.Duration(s.DiagnoseTimeoutSec) * time.Second
}

// defaultDiagnoseTimeout 与旧 llm_command 的 3 分钟预算保持一致。
const defaultDiagnoseTimeout = 180 * time.Second

const managedSectionTOML = `
[managed]
# 受管执行：默认关闭自动派发；硬上限 1（与 max_parallel 看板参考值分离）
config_version = 1
auto_enabled = false
# 自动选题生产者：默认关闭；开启后由 leader 每轮入队一条 dispatch_ready
auto_select = false
limit = 1
# 空数组 = 不限制节点；填写 node_id 后自动派发只打这些节点
allowed_nodes = []
`

const systemAgentSectionTOML = `
[system_agent]
# 系统 Agent：默认关闭；打开后可对 blocked 自动入队并编排拆题/运维
config_version = 1
enabled = false
poll_interval_sec = 5
max_concurrent = 1
# 诊断后端：填已登记的 Pi 节点 ID 时，诊断走 Pi 运行时的 diagnose 模式（只读）；
# 留空则沿用 llm_command，未配置任何后端时安全降级为升级人工。
llm_node = ""
worker_node = ""
llm_command = []
# 单次诊断调用的超时（秒）；超时会留下可审计结论，不会被当成「还在跑」
diagnose_timeout_sec = 180
auto_kinds = ["unblock_assist"]
escalate_patterns = ["needs_human", "auth_required"]
cooldown_sec = 300
max_attempts = 3
ops_allowlist = ["maw next", "maw merge --yes", "maw prune", "maw task unblock"]
`

// EnsureManagedSection 为旧配置补写 [managed] 段；已存在则不动。
// 返回 true 表示写入了迁移内容。
func EnsureManagedSection(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, i18n.E("err.config_missing", "next.run_init", path)
	}
	text := string(b)
	if regexp.MustCompile(`(?m)^\s*\[managed\]\s*$`).MatchString(text) {
		return false, nil
	}
	trimmed := strings.TrimRight(text, "\n") + "\n" + managedSectionTOML
	if err := os.WriteFile(path, []byte(trimmed), 0o644); err != nil {
		return false, i18n.EW("err.write_config", "next.check_perms", err)
	}
	return true, nil
}

// EnsureSystemAgentSection 为旧配置补写 [system_agent] 段；已存在则不动。
func EnsureSystemAgentSection(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, i18n.E("err.config_missing", "next.run_init", path)
	}
	text := string(b)
	if regexp.MustCompile(`(?m)^\s*\[system_agent\]\s*$`).MatchString(text) {
		return false, nil
	}
	trimmed := strings.TrimRight(text, "\n") + "\n" + systemAgentSectionTOML
	if err := os.WriteFile(path, []byte(trimmed), 0o644); err != nil {
		return false, i18n.EW("err.write_config", "next.check_perms", err)
	}
	return true, nil
}

func DetectGates(repo string) []string {
	if _, err := os.Stat(filepath.Join(repo, "package.json")); err == nil {
		b, _ := os.ReadFile(filepath.Join(repo, "package.json"))
		s := string(b)
		var cmds []string
		if strings.Contains(s, `"lint"`) {
			cmds = append(cmds, "npm run lint")
		}
		if strings.Contains(s, `"typecheck"`) {
			cmds = append(cmds, "npm run typecheck")
		}
		if strings.Contains(s, `"test"`) {
			cmds = append(cmds, "npm test")
		}
		if len(cmds) == 0 {
			return []string{"npm test"}
		}
		return cmds
	}
	if _, err := os.Stat(filepath.Join(repo, "go.mod")); err == nil {
		return []string{"go test ./..."}
	}
	if _, err := os.Stat(filepath.Join(repo, "pyproject.toml")); err == nil {
		return []string{"pytest"}
	}
	if _, err := os.Stat(filepath.Join(repo, "Cargo.toml")); err == nil {
		return []string{"cargo test"}
	}
	return nil
}

func DetectMainBranch(repo string) string {
	if out, err := home.Git(repo, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		parts := strings.Split(out, "/")
		if n := parts[len(parts)-1]; n != "" {
			return n
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := home.Git(repo, "show-ref", "--verify", "refs/heads/"+name); err == nil {
			return name
		}
	}
	return "main"
}

// SetMainBranch 行级改写 config.toml 的 main_branch，保留其余内容与注释。
func SetMainBranch(path, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return i18n.E("err.main_branch_empty", "next.set_main_branch")
	}
	if strings.ContainsAny(branch, " \t\n\"") {
		return i18n.E("err.main_branch_invalid", "next.branch_name", branch)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return i18n.E("err.config_missing", "next.run_init", path)
	}
	text := string(b)
	repl := `${1}"` + branch + `"`
	if mainBranchLine.MatchString(text) {
		text = mainBranchLine.ReplaceAllString(text, repl)
	} else {
		// 无该行时插到 [project] 段首行之后
		proj := regexp.MustCompile(`(?m)^\[project\]\s*$`)
		loc := proj.FindStringIndex(text)
		if loc == nil {
			return i18n.E("err.config_no_project", "next.restore_config")
		}
		insert := "\nmain_branch = \"" + branch + "\""
		text = text[:loc[1]] + insert + text[loc[1]:]
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return i18n.EW("err.write_config", "next.check_perms", err)
	}
	return nil
}

func RenderDefault(repo string) string {
	gates := DetectGates(repo)
	var gateLit string
	if len(gates) == 0 {
		gateLit = "[]"
	} else {
		parts := make([]string, len(gates))
		for i, g := range gates {
			parts[i] = `"` + g + `"`
		}
		gateLit = "[" + strings.Join(parts, ", ") + "]"
	}
	main := DetectMainBranch(repo)
	return `[project]
# init 探测占位；正式主干在任务清单 / 蓝图 [meta] main_branch，由 plan apply 同步
main_branch = "` + main + `"
merge_strategy = "squash"
max_parallel = 0
blueprint = ".multiagent/blueprint.toml"

[approvals]
plan = "manual"
merge = "manual"
teardown = "manual"
review = "auto"

[gates]
commands = ` + gateLit + `
timeout_sec = 900

[locks]
ttl_sec = 1800
heartbeat_ttl_sec = 1800

[launcher]
mode = "manual"

[managed]
# 受管执行：默认关闭自动派发；硬上限 1（与 max_parallel 看板参考值分离）
config_version = 1
auto_enabled = false
auto_select = false
limit = 1

[system_agent]
# 系统 Agent：默认关闭；打开后可对 blocked 自动入队并编排拆题/运维
config_version = 1
enabled = false
poll_interval_sec = 5
max_concurrent = 1
# 诊断后端：填已登记的 Pi 节点 ID 时，诊断走 Pi 运行时的 diagnose 模式（只读）；
# 留空则沿用 llm_command，未配置任何后端时安全降级为升级人工。
llm_node = ""
worker_node = ""
llm_command = []
# 单次诊断调用的超时（秒）；超时会留下可审计结论，不会被当成「还在跑」
diagnose_timeout_sec = 180
auto_kinds = ["unblock_assist"]
escalate_patterns = ["needs_human", "auth_required"]
cooldown_sec = 300
max_attempts = 3
ops_allowlist = ["maw next", "maw merge --yes", "maw prune", "maw task unblock"]

[identity]
# Worker 名推荐动态注册：{应用}-{工程}-{序号}，如 cursor-demo-1
workers = []
reviewers = ["r1"]
integrator = "int"
# leads 是本机 lead 能力标记；空数组表示默认无人可调用 maw lead *
leads = []
`
}
