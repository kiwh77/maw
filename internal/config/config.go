package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
}

// Managed 控制受管 CLI 执行。默认关闭自动模式，上限为 1。
type Managed struct {
	// ConfigVersion 是本段配置的版本号，便于迁移与看板展示。
	ConfigVersion int `toml:"config_version"`
	// AutoEnabled 为真时才允许自动派发循环；默认 false。
	AutoEnabled bool `toml:"auto_enabled"`
	// Limit 是受管执行的全局硬上限；auto_enabled=true 时必须 ≥1。
	Limit int `toml:"limit"`
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
	return &c, nil
}

// AutoExecution 把配置映射为运行时契约里的 auto_execution 视图。
func (c *Config) AutoExecution() (enabled bool, limit int) {
	if c == nil {
		return false, 1
	}
	return c.Managed.AutoEnabled, c.Managed.Limit
}

const managedSectionTOML = `
[managed]
# 受管执行：默认关闭自动派发；硬上限 1（与 max_parallel 看板参考值分离）
config_version = 1
auto_enabled = false
limit = 1
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
heartbeat_ttl_sec = 900

[launcher]
mode = "manual"

[managed]
# 受管执行：默认关闭自动派发；硬上限 1（与 max_parallel 看板参考值分离）
config_version = 1
auto_enabled = false
limit = 1

[identity]
# Worker 名推荐动态注册：{应用}-{工程}-{序号}，如 cursor-demo-1
workers = []
reviewers = ["r1"]
integrator = "int"
`
}
