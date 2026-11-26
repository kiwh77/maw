package codebuddy

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
)

// TestBuildLaunchSpecShape 钉住整条 argv：顺序、取值、以及「输入是一个元素」。
func TestBuildLaunchSpecShape(t *testing.T) {
	a, cfg, _ := probedAdapter(t)
	spec := testRunSpec(t)

	launch, err := a.BuildLaunchSpec(context.Background(), spec)
	if err != nil {
		t.Fatalf("构造启动规格失败：%v", err)
	}

	// 入口是 `#!/usr/bin/env node` 脚本 → executable 换成真实运行时，脚本作为 argv[0]。
	nodeReal, err := resolveInterpreter("node")
	if err != nil {
		t.Fatalf("解析 node 失败：%v", err)
	}
	if launch.Executable != nodeReal {
		t.Fatalf("executable 应是真实运行时 %q，实际 %q", nodeReal, launch.Executable)
	}

	wantArgv := []string{
		mustReal(t, cfg.EntryPath),
		"-p",
		spec.Input,
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--port", "0",
		"--permission-mode", "default",
		"--strict-mcp-config",
	}
	if !reflect.DeepEqual(launch.Argv, wantArgv) {
		t.Fatalf("argv 不符：\n期望 %#v\n实际 %#v", wantArgv, launch.Argv)
	}
	if launch.Cwd != spec.Workspace.Path {
		t.Fatalf("cwd 应是工作目录 %q，实际 %q", spec.Workspace.Path, launch.Cwd)
	}
	if launch.StdinMode != model.StdinNone {
		t.Fatalf("参数输入模式不应接 stdin，实际 %s", launch.StdinMode)
	}
	if !reflect.DeepEqual(launch.EnvAllowlist, EnvAllowlist) {
		t.Fatalf("环境白名单必须是 %v，实际 %v", EnvAllowlist, launch.EnvAllowlist)
	}
	if launch.ExpectedProtocol.Format != "stream-json" || launch.ExpectedProtocol.Version != ProtocolVersion {
		t.Fatalf("预期协议写死 stream-json／%s，实际 %+v", ProtocolVersion, launch.ExpectedProtocol)
	}
	if vs := launch.Validate(); len(vs) != 0 {
		t.Fatalf("启动规格必须自洽：%v", model.Err(vs))
	}
}

// TestBuildLaunchSpecKeepsInputOpaque 是「禁止拼 shell」的可执行证据：
// 带空格、引号、分号、$ 与反引号的输入必须原样作为一个 argv 元素，不被拆开也不被解释。
func TestBuildLaunchSpecKeepsInputOpaque(t *testing.T) {
	a, _, _ := probedAdapter(t)
	spec := testRunSpec(t)
	spec.Input = `fix the "bug" in $HOME; rm -rf /tmp/x && echo ` + "`whoami`"

	launch, err := a.BuildLaunchSpec(context.Background(), spec)
	if err != nil {
		t.Fatalf("构造启动规格失败：%v", err)
	}
	position := -1
	for i, arg := range launch.Argv {
		if arg == spec.Input {
			if position >= 0 {
				t.Fatal("输入只应出现一次")
			}
			position = i
		}
	}
	if position < 0 {
		t.Fatalf("输入没有被完整保留：%#v", launch.Argv)
	}
	if position != 2 {
		t.Fatalf("输入应紧跟在 -p 之后（下标 2），实际 %d", position)
	}
	// 任何一个元素都不该是 shell 可执行文件，argv 里也不该出现 -c。
	for _, arg := range append([]string{launch.Executable}, launch.Argv...) {
		base := filepath.Base(arg)
		if base == "sh" || base == "bash" || base == "zsh" || arg == "-c" {
			t.Fatalf("启动规格出现了 shell 拼接痕迹：%q", arg)
		}
	}
}

// TestBuildLaunchSpecStdin 覆盖管道输入：提示词不落到 argv。
func TestBuildLaunchSpecStdin(t *testing.T) {
	a, _, _ := probedAdapter(t)
	spec := testRunSpec(t)
	spec.InputMode = model.InputStdin

	launch, err := a.BuildLaunchSpec(context.Background(), spec)
	if err != nil {
		t.Fatalf("构造启动规格失败：%v", err)
	}
	if launch.StdinMode != model.StdinPipe {
		t.Fatalf("stdin 模式应为 pipe，实际 %s", launch.StdinMode)
	}
	for _, arg := range launch.Argv {
		if arg == spec.Input {
			t.Fatal("stdin 模式下提示词不应出现在 argv")
		}
	}
}

// TestBuildLaunchSpecRequiresProbe 覆盖「没探测就启动」：拒绝，而不是拿配置现拼。
func TestBuildLaunchSpecRequiresProbe(t *testing.T) {
	cfg, _, _ := testNode(t)
	a := newAdapter(t, cfg, newFakeRunner())

	_, err := a.BuildLaunchSpec(context.Background(), testRunSpec(t))
	e := requirePortErr(t, err, model.PortErrEntryUnavailable)
	if !strings.Contains(e.Msg, "尚未成功探测") {
		t.Fatalf("错误文案应说清原因，实际 %q", e.Msg)
	}
}

// TestBuildLaunchSpecInvalidSpec 覆盖非法规格：RunSpec 自己就不成立。
func TestBuildLaunchSpecInvalidSpec(t *testing.T) {
	a, _, _ := probedAdapter(t)
	if _, err := a.BuildLaunchSpec(context.Background(), model.RunSpec{}); err == nil {
		t.Fatal("空规格必须被拒绝")
	} else {
		requirePortErr(t, err, model.PortErrInvalidSpec)
	}
}

// TestBuildLaunchSpecRejectsBadWorkspace 覆盖工作目录问题：相对路径与不存在的目录。
func TestBuildLaunchSpecRejectsBadWorkspace(t *testing.T) {
	a, _, _ := probedAdapter(t)

	relative := testRunSpec(t)
	relative.Workspace.Path = "relative/dir"
	_, err := a.BuildLaunchSpec(context.Background(), relative)
	requirePortErr(t, err, model.PortErrInvalidSpec)

	missing := testRunSpec(t)
	missing.Workspace.Path = filepath.Join(t.TempDir(), "not-there")
	_, err = a.BuildLaunchSpec(context.Background(), missing)
	requirePortErr(t, err, model.PortErrInvalidSpec)
}

// TestBuildLaunchSpecPermissionRefusals 覆盖「承诺不了就不承诺」的三条权限拒绝。
func TestBuildLaunchSpecPermissionRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*model.PermissionProfile)
		want   model.PortErrorCode
	}{
		{"禁写无法证明", func(p *model.PermissionProfile) { p.AllowWrite = false }, model.PortErrCapabilityMissing},
		{"禁网无开关", func(p *model.PermissionProfile) { p.AllowNetwork = false }, model.PortErrCapabilityMissing},
		{"沙箱未观测", func(p *model.PermissionProfile) { p.Sandbox = "container" }, model.PortErrCapabilityMissing},
		{"未知权限档", func(p *model.PermissionProfile) { p.Profile = "readonly-ish" }, model.PortErrInvalidSpec},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _, _ := probedAdapter(t)
			spec := testRunSpec(t)
			c.mutate(&spec.Permission)
			_, err := a.BuildLaunchSpec(context.Background(), spec)
			requirePortErr(t, err, c.want)
		})
	}
}

// TestBuildLaunchSpecAcceptsAllDocumentedModes 覆盖 S02 帮助清单里的六个权限档都能下传。
func TestBuildLaunchSpecAcceptsAllDocumentedModes(t *testing.T) {
	for mode := range permissionModes {
		t.Run(mode, func(t *testing.T) {
			a, _, _ := probedAdapter(t)
			spec := testRunSpec(t)
			spec.Permission.Profile = mode
			launch, err := a.BuildLaunchSpec(context.Background(), spec)
			if err != nil {
				t.Fatalf("%s 应可下传：%v", mode, err)
			}
			if !containsPair(launch.Argv, "--permission-mode", mode) {
				t.Fatalf("argv 缺 --permission-mode %s：%v", mode, launch.Argv)
			}
		})
	}
}

// TestBuildLaunchSpecRefusesResume 覆盖 C24：恢复会话是 unknown，不做未验证的事。
func TestBuildLaunchSpecRefusesResume(t *testing.T) {
	a, _, _ := probedAdapter(t)
	spec := testRunSpec(t)
	spec.Session = model.SessionSpec{Isolation: "session", ResumeSessionID: "9f1c2b7a-0000-4000-8000-000000000001"}

	_, err := a.BuildLaunchSpec(context.Background(), spec)
	e := requirePortErr(t, err, model.PortErrCapabilityMissing)
	if !strings.Contains(e.Msg, "session.resume") {
		t.Fatalf("错误文案应指出是哪条能力没观测，实际 %q", e.Msg)
	}
}

// TestBuildLaunchSpecRefusesFileInput 覆盖文件输入：LaunchSpec 表达不了，不猜。
func TestBuildLaunchSpecRefusesFileInput(t *testing.T) {
	a, _, _ := probedAdapter(t)
	spec := testRunSpec(t)
	spec.InputMode = model.InputFile

	_, err := a.BuildLaunchSpec(context.Background(), spec)
	requirePortErr(t, err, model.PortErrCapabilityMissing)
}

// TestBuildLaunchSpecRejectsDashInput 覆盖以 - 开头的输入：会被 CLI 当选项，不插未验证的分隔符。
func TestBuildLaunchSpecRejectsDashInput(t *testing.T) {
	a, _, _ := probedAdapter(t)
	spec := testRunSpec(t)
	spec.Input = "--dangerously-skip-permissions do it"

	_, err := a.BuildLaunchSpec(context.Background(), spec)
	requirePortErr(t, err, model.PortErrInvalidSpec)
}

// TestBuildLaunchSpecMCP 覆盖 MCP 集合的三种形态。
func TestBuildLaunchSpecMCP(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "mcp servers.json")
	mustWrite(t, conf, `{"mcpServers":{}}`, 0o644)

	t.Run("单份文件", func(t *testing.T) {
		a, _, _ := probedAdapter(t)
		spec := testRunSpec(t)
		spec.Session = model.SessionSpec{MCPServers: []string{conf}}
		launch, err := a.BuildLaunchSpec(context.Background(), spec)
		if err != nil {
			t.Fatalf("单份配置应可下传：%v", err)
		}
		if !containsPair(launch.Argv, "--mcp-config", conf) {
			t.Fatalf("argv 缺 --mcp-config %q：%v", conf, launch.Argv)
		}
		if !contains(launch.Argv, "--strict-mcp-config") {
			t.Fatalf("必须同时钉死 --strict-mcp-config：%v", launch.Argv)
		}
	})

	t.Run("内联 JSON", func(t *testing.T) {
		a, _, _ := probedAdapter(t)
		spec := testRunSpec(t)
		spec.Session = model.SessionSpec{MCPServers: []string{`{"mcpServers":{"x":{"command":"/bin/echo"}}}`}}
		launch, err := a.BuildLaunchSpec(context.Background(), spec)
		if err != nil {
			t.Fatalf("内联 JSON 应可下传：%v", err)
		}
		if !containsPair(launch.Argv, "--mcp-config", spec.Session.MCPServers[0]) {
			t.Fatalf("argv 缺内联配置：%v", launch.Argv)
		}
	})

	t.Run("无配置也要钉死", func(t *testing.T) {
		a, _, _ := probedAdapter(t)
		launch, err := a.BuildLaunchSpec(context.Background(), testRunSpec(t))
		if err != nil {
			t.Fatalf("构造失败：%v", err)
		}
		if !contains(launch.Argv, "--strict-mcp-config") {
			t.Fatalf("空 MCP 集合也必须 --strict-mcp-config：%v", launch.Argv)
		}
	})

	t.Run("多份配置", func(t *testing.T) {
		a, _, _ := probedAdapter(t)
		spec := testRunSpec(t)
		spec.Session = model.SessionSpec{MCPServers: []string{conf, conf}}
		_, err := a.BuildLaunchSpec(context.Background(), spec)
		requirePortErr(t, err, model.PortErrCapabilityMissing)
	})

	t.Run("相对路径", func(t *testing.T) {
		a, _, _ := probedAdapter(t)
		spec := testRunSpec(t)
		spec.Session = model.SessionSpec{MCPServers: []string{"mcp.json"}}
		_, err := a.BuildLaunchSpec(context.Background(), spec)
		requirePortErr(t, err, model.PortErrInvalidSpec)
	})

	t.Run("内联 JSON 非法", func(t *testing.T) {
		a, _, _ := probedAdapter(t)
		spec := testRunSpec(t)
		spec.Session = model.SessionSpec{MCPServers: []string{`{"mcpServers":`}}
		_, err := a.BuildLaunchSpec(context.Background(), spec)
		requirePortErr(t, err, model.PortErrInvalidSpec)
	})
}

// TestBuildLaunchSpecVersionDrift 是 G07 在启动侧的落点：换版本后拒绝启动，直到重新采集证据。
func TestBuildLaunchSpecVersionDrift(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	fake.versionOut = "2.174.0\n"
	a := newAdapter(t, cfg, fake)
	if _, err := a.Probe(context.Background(), cfg); err != nil {
		t.Fatalf("探测失败：%v", err)
	}

	_, err := a.BuildLaunchSpec(context.Background(), testRunSpec(t))
	e := requirePortErr(t, err, model.PortErrCapabilityMissing)
	for _, want := range []string{capHeadless, capStreamJSON, string(MeasuredCLIVersion)} {
		if !strings.Contains(e.Msg, want) {
			t.Errorf("错误文案应包含 %q，实际 %q", want, e.Msg)
		}
	}
}

// TestBuildLaunchSpecNeedsHelpList 覆盖「没实测帮助清单就不拼参数」。
func TestBuildLaunchSpecNeedsHelpList(t *testing.T) {
	a, _, _ := probedAdapter(t, WithHelpProbe(false))
	_, err := a.BuildLaunchSpec(context.Background(), testRunSpec(t))
	e := requirePortErr(t, err, model.PortErrCapabilityMissing)
	if !strings.Contains(e.Msg, "帮助清单") {
		t.Fatalf("错误文案应指向帮助清单缺失，实际 %q", e.Msg)
	}
}

// TestBuildLaunchSpecParameterDrift 覆盖帮助清单缺参数：参数存在性以实测清单为准。
func TestBuildLaunchSpecParameterDrift(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	fake.helpOut = strings.ReplaceAll(helpFixture, "--permission-mode <mode>", "")
	a := newAdapter(t, cfg, fake)
	if _, err := a.Probe(context.Background(), cfg); err != nil {
		t.Fatalf("探测失败：%v", err)
	}

	_, err := a.BuildLaunchSpec(context.Background(), testRunSpec(t))
	e := requirePortErr(t, err, model.PortErrCapabilityMissing)
	if !strings.Contains(e.Msg, "--permission-mode") {
		t.Fatalf("错误文案应点名缺失的参数，实际 %q", e.Msg)
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
