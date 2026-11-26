package workbuddy

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
)

// TestBuildLaunchSpecProducesJSONShape 钉住两包的启动形态：单条 json、固定参数顺序、不带 stream-json。
func TestBuildLaunchSpecProducesJSONShape(t *testing.T) {
	for _, kind := range []DistributionKind{DistributionCN, DistributionIntl} {
		a, cfg, dist, _ := probedAdapter(t, kind)
		spec := testRunSpec(t)

		launch, err := a.BuildLaunchSpec(context.Background(), spec)
		if err != nil {
			t.Fatalf("%s 构造启动规格失败：%v", kind, err)
		}

		want := []string{
			mustReal(t, cfg.EntryPath),
			"-p", spec.Input,
			"--output-format", OutputFormat,
			"--no-session-persistence",
			"--permission-mode", "default",
			"--strict-mcp-config",
		}
		if !slices.Equal(launch.Argv, want) {
			t.Fatalf("%s argv 不符：\n实际 %v\n期望 %v", kind, launch.Argv, want)
		}
		if filepath.Base(launch.Executable) != RuntimeCommand {
			t.Errorf("%s executable 应是真实运行时 %s，实际 %q", kind, RuntimeCommand, launch.Executable)
		}
		if launch.Cwd != spec.Workspace.Path {
			t.Errorf("%s cwd 应为工作目录，实际 %q", kind, launch.Cwd)
		}
		if launch.StdinMode != model.StdinNone {
			t.Errorf("%s arg 模式应为 stdin none，实际 %s", kind, launch.StdinMode)
		}
		if launch.ExpectedProtocol.Format != OutputFormat || launch.ExpectedProtocol.Version != ProtocolVersion {
			t.Errorf("%s 预期协议不符：%+v", kind, launch.ExpectedProtocol)
		}
		for _, name := range dist.EnvNames() {
			if !slices.Contains(launch.EnvAllowlist, name) {
				t.Errorf("%s 的环境白名单应包含启动条件 %s：%v", kind, name, launch.EnvAllowlist)
			}
		}
		if vs := launch.Validate(); len(vs) != 0 {
			t.Errorf("%s 启动规格不合契约：%v", kind, model.Err(vs))
		}
	}
}

// TestBuildLaunchSpecDoesNotInheritCodeBuddyShape 覆盖「不继承对方能力」：
// stream-json、显式 --port、--tools ""、--max-turns 都是另一条证据线的东西，不能出现在这里。
func TestBuildLaunchSpecDoesNotInheritCodeBuddyShape(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	launch, err := a.BuildLaunchSpec(context.Background(), testRunSpec(t))
	if err != nil {
		t.Fatalf("构造启动规格失败：%v", err)
	}
	forbidden := map[string]string{
		"stream-json":             "W06 unobserved：两包从未产出 stream-json 记录",
		"--port":                  "W14：帮助写明 --port 默认 auto-assign，2.173.0 的必须显式传端口不继承",
		"--tools":                 "最小调用里的 --tools \"\" 是固定文本测试的约束，不是受管启动语义",
		"--max-turns":             "同上：限制轮次属于派发策略，RunSpec 没有该字段",
		"--include-partial-msg":   "W07 unobserved",
		"--dangerously-skip-perm": "权限跳过不在 RunSpec 的表达范围内",
	}
	for _, arg := range launch.Argv {
		for bad, why := range forbidden {
			if strings.Contains(arg, bad) {
				t.Errorf("argv 出现了 %q（%s）：%v", bad, why, launch.Argv)
			}
		}
	}
}

// TestBuildLaunchSpecStdinMode 覆盖 stdin 输入：提示词不进 argv。
func TestBuildLaunchSpecStdinMode(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	spec := testRunSpec(t)
	spec.InputMode = model.InputStdin

	launch, err := a.BuildLaunchSpec(context.Background(), spec)
	if err != nil {
		t.Fatalf("构造启动规格失败：%v", err)
	}
	if launch.StdinMode != model.StdinPipe {
		t.Fatalf("stdin 模式应为 pipe，实际 %s", launch.StdinMode)
	}
	if slices.Contains(launch.Argv, spec.Input) {
		t.Fatalf("stdin 模式下提示词不应进 argv：%v", launch.Argv)
	}
}

// TestBuildLaunchSpecRefusesLeadingDash 覆盖以 - 开头的输入：不插未验证的分隔符。
func TestBuildLaunchSpecRefusesLeadingDash(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	spec := testRunSpec(t)
	spec.Input = "--version"

	_, err := a.BuildLaunchSpec(context.Background(), spec)
	requirePortErr(t, err, model.PortErrInvalidSpec)
}

// TestBuildLaunchSpecRefusesFileInput 覆盖文件输入：LaunchSpec 没有该字段。
func TestBuildLaunchSpecRefusesFileInput(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	spec := testRunSpec(t)
	spec.InputMode = model.InputFile

	_, err := a.BuildLaunchSpec(context.Background(), spec)
	requirePortErr(t, err, model.PortErrCapabilityMissing)
}

// TestBuildLaunchSpecRefusesResume 覆盖恢复会话：W19 unobserved，不接受「大概是续跑」。
func TestBuildLaunchSpecRefusesResume(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	spec := testRunSpec(t)
	spec.Session = model.SessionSpec{Isolation: "worktree", ResumeSessionID: "sess_123"}

	_, err := a.BuildLaunchSpec(context.Background(), spec)
	e := requirePortErr(t, err, model.PortErrCapabilityMissing)
	if !strings.Contains(e.Msg, capResume) {
		t.Errorf("错误文案应点名 session.resume 的能力状态，实际 %q", e.Msg)
	}
}

// TestBuildLaunchSpecRefusesUnprovablePermissions 覆盖三条「承诺不了就不承诺」。
func TestBuildLaunchSpecRefusesUnprovablePermissions(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*model.PermissionProfile)
	}{
		{"禁写", func(p *model.PermissionProfile) { p.AllowWrite = false }},
		{"禁网", func(p *model.PermissionProfile) { p.AllowNetwork = false }},
		{"沙箱", func(p *model.PermissionProfile) { p.Sandbox = "docker" }},
	}
	for _, c := range cases {
		a, _, _, _ := probedAdapter(t, DistributionCN)
		spec := testRunSpec(t)
		c.mut(&spec.Permission)
		if _, err := a.BuildLaunchSpec(context.Background(), spec); err == nil {
			t.Errorf("%s：应拒绝启动", c.name)
		} else {
			requirePortErr(t, err, model.PortErrCapabilityMissing)
		}
	}
}

// TestBuildLaunchSpecRejectsUnknownPermissionProfile 覆盖权限档不在实测枚举里。
func TestBuildLaunchSpecRejectsUnknownPermissionProfile(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	spec := testRunSpec(t)
	spec.Permission.Profile = "yolo"

	_, err := a.BuildLaunchSpec(context.Background(), spec)
	requirePortErr(t, err, model.PortErrInvalidSpec)
}

// TestBuildLaunchSpecMCP 覆盖 MCP 集合的三种情况。
func TestBuildLaunchSpecMCP(t *testing.T) {
	// 一份内联配置：必须带 --mcp-config + --strict-mcp-config。
	a, _, _, _ := probedAdapter(t, DistributionCN)
	spec := testRunSpec(t)
	spec.Session.MCPServers = []string{`{"mcpServers":{}}`}
	launch, err := a.BuildLaunchSpec(context.Background(), spec)
	if err != nil {
		t.Fatalf("单份 MCP 配置应可用：%v", err)
	}
	if !slices.Contains(launch.Argv, "--mcp-config") || !slices.Contains(launch.Argv, "--strict-mcp-config") {
		t.Fatalf("MCP 参数不完整：%v", launch.Argv)
	}

	// 相对路径：随调用者工作目录漂移，拒绝。
	spec.Session.MCPServers = []string{"mcp.json"}
	if _, err := a.BuildLaunchSpec(context.Background(), spec); err == nil {
		t.Fatal("相对 MCP 路径应被拒绝")
	} else {
		requirePortErr(t, err, model.PortErrInvalidSpec)
	}

	// 多份：合并语义未观测。
	spec.Session.MCPServers = []string{`{"a":1}`, `{"b":2}`}
	_, err = a.BuildLaunchSpec(context.Background(), spec)
	requirePortErr(t, err, model.PortErrCapabilityMissing)
}

// TestBuildLaunchSpecRequiresProbe 覆盖「没探测过不放行」。
func TestBuildLaunchSpecRequiresProbe(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	a := newAdapter(t, cfg, nil)

	_, err := a.BuildLaunchSpec(context.Background(), testRunSpec(t))
	requirePortErr(t, err, model.PortErrEntryUnavailable)
}

// TestBuildLaunchSpecRequiresVerifiedFlags 覆盖「帮助清单里没有就不是受管形态」。
func TestBuildLaunchSpecRequiresVerifiedFlags(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	dist, _ := Lookup(DistributionCN)
	fake := newFakeRunner(dist)
	// 帮助清单退化：只有版本与帮助两个参数，没有任何启动参数。
	fake.helpOut = "Usage: codebuddy [options]\n\nOptions:\n  -V, --version   output the version number\n  -h, --help      display help for command\n"
	a := newAdapter(t, cfg, fake)
	if _, err := a.Probe(context.Background(), cfg); err != nil {
		t.Fatalf("探测失败：%v", err)
	}

	_, err := a.BuildLaunchSpec(context.Background(), testRunSpec(t))
	e := requirePortErr(t, err, model.PortErrCapabilityMissing)
	if !strings.Contains(e.Msg, "--print") {
		t.Errorf("错误文案应点名缺失的参数，实际 %q", e.Msg)
	}
}

// TestBuildLaunchSpecRechecksLaunchConditions 覆盖探测之后宿主环境变化：
// 规格不再指向被证据覆盖的那份实现，必须重新探测。
func TestBuildLaunchSpecRechecksLaunchConditions(t *testing.T) {
	a, _, dist, _ := probedAdapter(t, DistributionCN)
	for name := range dist.RequiredEnv {
		t.Setenv(name, "")
	}

	_, err := a.BuildLaunchSpec(context.Background(), testRunSpec(t))
	e := requirePortErr(t, err, model.PortErrCapabilityMissing)
	if !strings.Contains(e.Msg, "启动条件已失效") {
		t.Fatalf("错误文案应说明启动条件失效，实际 %q", e.Msg)
	}
}

// TestBuildLaunchSpecRejectsBadWorkspace 覆盖工作目录不存在。
func TestBuildLaunchSpecRejectsBadWorkspace(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	spec := testRunSpec(t)
	spec.Workspace.Path = filepath.Join(os.TempDir(), "maw-vn305-not-a-dir-"+string(a.cfg.NodeID))

	_, err := a.BuildLaunchSpec(context.Background(), spec)
	requirePortErr(t, err, model.PortErrInvalidSpec)
}
