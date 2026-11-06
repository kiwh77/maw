package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetMainBranchReplacesLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	orig := `[project]
main_branch = "main"
merge_strategy = "squash"
# keep me
max_parallel = 0
`
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetMainBranch(p, "dev"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, `main_branch = "dev"`) {
		t.Fatalf("expected main_branch=dev, got:\n%s", text)
	}
	if strings.Contains(text, `main_branch = "main"`) {
		t.Fatal("old main_branch should be gone")
	}
	if !strings.Contains(text, "# keep me") {
		t.Fatal("comments must be preserved")
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.MainBranch != "dev" {
		t.Fatalf("Load MainBranch=%q", cfg.Project.MainBranch)
	}
}

func TestSetMainBranchInsertsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("[project]\nmax_parallel = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetMainBranch(p, "trunk"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project.MainBranch != "trunk" {
		t.Fatalf("got %q", cfg.Project.MainBranch)
	}
}

// SA-004：两个开关驱动两条不同的循环——默认都关，且互不推导。
//
// 这条用例钉住的是「语义」而不是「字段」：把 auto_enabled 打开不该顺手打开系统 Agent，
// 反过来也一样。只要有人把两个开关合并或让其中一个推导另一个，这里就会红。
func TestDispatchAndSystemAgentSwitchesAreIndependent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	write := func(body string) *Config {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		return cfg
	}

	// 默认：两条循环都不开，上限仍是 1。
	cfg := write("[project]\nmain_branch = \"main\"\n")
	if dispatch, limit := cfg.DispatchEnabled(); dispatch || limit != 1 {
		t.Fatalf("默认 dispatch=%v limit=%d，期望 false/1", dispatch, limit)
	}
	if cfg.SystemAgentEnabled() {
		t.Fatal("默认系统 Agent 必须关闭")
	}
	if dispatch, system := cfg.AutomationSummary(); dispatch || system {
		t.Fatalf("默认不应有自动行为，得到 dispatch=%v system=%v", dispatch, system)
	}

	// 只开自愈：派发仍关着。
	cfg = write("[managed]\nauto_enabled = false\nlimit = 1\n\n[system_agent]\nenabled = true\n")
	if !cfg.SystemAgentEnabled() {
		t.Fatal("system_agent.enabled=true 时系统 Agent 应为开")
	}
	if dispatch, _ := cfg.DispatchEnabled(); dispatch {
		t.Fatal("只开系统 Agent 不得把自动派发也打开")
	}

	// 只开派发：自愈仍关着。
	cfg = write("[managed]\nauto_enabled = true\nlimit = 2\n\n[system_agent]\nenabled = false\n")
	if dispatch, limit := cfg.DispatchEnabled(); !dispatch || limit != 2 {
		t.Fatalf("dispatch=%v limit=%d，期望 true/2", dispatch, limit)
	}
	if cfg.SystemAgentEnabled() {
		t.Fatal("只开自动派发不得把系统 Agent 也打开")
	}

	// nil 接收者按关闭处理：状态输出不会因为缺配置而 panic。
	var empty *Config
	if dispatch, limit := empty.DispatchEnabled(); dispatch || limit != 1 {
		t.Fatalf("nil config dispatch=%v limit=%d", dispatch, limit)
	}
	if empty.SystemAgentEnabled() {
		t.Fatal("nil config 的系统 Agent 必须按关闭处理")
	}
	if dispatch, system := empty.AutomationSummary(); dispatch || system {
		t.Fatalf("nil config 不应有自动行为，得到 dispatch=%v system=%v", dispatch, system)
	}
}

// TestAutoSelectDefaultsOffAndIsIndependent 钉住 SH-401 的第三个开关：
// auto_select 默认关闭，且与 auto_enabled / system_agent.enabled 互不推导。
func TestAutoSelectDefaultsOffAndIsIndependent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	write := func(body string) *Config {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		return cfg
	}

	// 默认全关：auto_select 不得因任何默认值变成 true。
	cfg := write("[project]\nmain_branch = \"main\"\n")
	if cfg.Managed.AutoSelect {
		t.Fatal("auto_select 默认必须关闭（契约 §9 默认全关）")
	}
	cfg = write("[managed]\nauto_enabled = true\nlimit = 1\n\n[system_agent]\nenabled = true\n")
	if cfg.Managed.AutoSelect {
		t.Fatal("打开其它两个开关不得顺手打开 auto_select")
	}

	// 单独开启 auto_select：另外两个开关保持关闭，auto_enabled 语义不变。
	cfg = write("[managed]\nauto_enabled = false\nauto_select = true\nlimit = 1\n")
	if !cfg.Managed.AutoSelect {
		t.Fatal("显式 auto_select=true 应被读到")
	}
	if dispatch, _ := cfg.DispatchEnabled(); dispatch {
		t.Fatal("开启 auto_select 不得改变 auto_enabled（派发消费）语义")
	}
	if cfg.SystemAgentEnabled() {
		t.Fatal("开启 auto_select 不得打开系统 Agent")
	}
}
