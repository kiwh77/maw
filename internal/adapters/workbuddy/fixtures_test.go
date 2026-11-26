package workbuddy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// helpFixture 是帮助清单的最小可判形状：只保留本包会用到存在性的那几个参数。
// 真实清单见 docs/evidence/workbuddy/samples/VN305-CN-S02-help-probe/stdout.txt（12706 字节）。
const helpFixture = `Usage: codebuddy|cbc [options] [command] [prompt]

Options:
  -V, --version                                    output the version number
  -p, --print                                      Print response and exit (useful for pipes) (default: false)
  --output-format <format>                         Output format (only works with --print): "text" (default), "json" (single result), or "stream-json" (realtime streaming)
  --include-partial-messages                       Output raw SSE incremental messages from model requests
  --permission-mode <mode>                         Default permission mode for this process. Choices: acceptEdits, bypassPermissions, default, plan, dontAsk, auto.
  --mcp-config <fileOrString>                      Load MCP servers from a JSON file or string
  --strict-mcp-config                              Only use MCP servers from --mcp-config, ignoring all other MCP configurations
  --no-session-persistence                         Disable session persistence - sessions stay in memory and are not saved to disk
  -r, --resume [sessionId]                         Resume a conversation
  --port <number>                                  Port for HTTP server (default: auto-assign)
  --max-turns <number>                             Limit the number of agentic turns
`

type fakeCall struct {
	Name string
	Args []string
	Env  []string
	Dir  string
}

// fakeRunner 记录每次调用的 executable／argv／env，并按参数尾部返回固定输出。
// 用它而不是真 CLI：测试必须能离线、可重复，且能在几毫秒内造出超时与非零退出。
type fakeRunner struct {
	versionOut  string
	versionCode int
	helpOut     string
	helpCode    int
	failure     error
	block       bool
	calls       []fakeCall
}

func newFakeRunner(dist Distribution) *fakeRunner {
	return &fakeRunner{
		versionOut: string(dist.BundledCLI) + "\n",
		helpOut:    helpFixture,
	}
}

func (f *fakeRunner) Run(ctx context.Context, name string, args, env []string, dir string) (commandResult, error) {
	f.calls = append(f.calls, fakeCall{
		Name: name,
		Args: append([]string(nil), args...),
		Env:  append([]string(nil), env...),
		Dir:  dir,
	})
	if f.block {
		<-ctx.Done()
		return commandResult{}, ctx.Err()
	}
	if f.failure != nil {
		return commandResult{}, f.failure
	}
	switch lastArg(args) {
	case "--version":
		return commandResult{Stdout: []byte(f.versionOut), Code: f.versionCode}, nil
	case "--help":
		return commandResult{Stdout: []byte(f.helpOut), Code: f.helpCode}, nil
	default:
		return commandResult{}, fmt.Errorf("fakeRunner 收到意外调用：%v", args)
	}
}

func lastArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}

// testNode 造一个可探测的节点：入口放在 `<Temp>/<AppName>.app/<EntrySuffix>`，
// 与被测的两款发行包同形（入口是 `#!/usr/bin/env node` 脚本）。
func testNode(t *testing.T, kind DistributionKind) (model.NodeConfig, string, string) {
	t.Helper()

	dist, ok := Lookup(kind)
	if !ok {
		t.Fatalf("未登记的发行包 %q", kind)
	}

	bin := t.TempDir()
	nodeBin := filepath.Join(bin, "node")
	mustWrite(t, nodeBin, "#!/bin/sh\nexit 0\n", 0o755)
	t.Setenv("PATH", bin)

	app := filepath.Join(t.TempDir(), dist.AppBundle())
	script := filepath.Join(app, filepath.FromSlash(EntrySuffix))
	mustWrite(t, script, "#!/usr/bin/env node\nconsole.log('2.161.0')\n", 0o755)

	setRequiredEnv(t, dist)

	cfg := model.NodeConfig{
		NodeID:     model.NodeID("mbp-workbuddy-" + string(kind)),
		Name:       dist.AppName,
		Adapter:    model.AdapterWorkBuddy,
		EntryPath:  script,
		ProfileRef: "profiles/workbuddy-default",
		Mode:       model.NodeManagedCLI,
		Platform:   runtime.GOOS,
		SlotLimit:  1,
		AcceptNew:  true,
		Timeout:    time.Minute,
	}
	return cfg, script, nodeBin
}

// setRequiredEnv 按该包登记的启动条件设置宿主环境（生产里由 App 的启动脚本提供）。
func setRequiredEnv(t *testing.T, dist Distribution) {
	t.Helper()
	for name, want := range dist.RequiredEnv {
		t.Setenv(name, want)
	}
}

// mustWrite 写文件并建目录，失败即终止。
func mustWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录 %s 失败：%v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("写文件 %s 失败：%v", path, err)
	}
}

// mustReal 返回符号链接展开后的路径，用于断言「真实绝对入口」。
func mustReal(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("展开 %s 失败：%v", path, err)
	}
	return real
}

// newAdapter 创建绑定 cfg 的适配器并注入假执行器。
func newAdapter(t *testing.T, cfg model.NodeConfig, r commandRunner, opts ...Option) *Adapter {
	t.Helper()
	a, err := New(cfg, opts...)
	if err != nil {
		t.Fatalf("构造适配器失败：%v", err)
	}
	if r != nil {
		a.runner = r
	}
	return a
}

// probedAdapter 返回一个已完成探测的适配器。
func probedAdapter(t *testing.T, kind DistributionKind, opts ...Option) (*Adapter, model.NodeConfig, Distribution, *fakeRunner) {
	t.Helper()
	cfg, _, _ := testNode(t, kind)
	dist, ok := Lookup(kind)
	if !ok {
		t.Fatalf("未登记的发行包 %q", kind)
	}
	fake := newFakeRunner(dist)
	a := newAdapter(t, cfg, fake, opts...)
	if _, err := a.Probe(context.Background(), cfg); err != nil {
		t.Fatalf("探测失败：%v", err)
	}
	fake.calls = nil
	return a, cfg, dist, fake
}

// forceMeasurement 直接把一份探测快照装进适配器。
//
// 用途：把「形状已观测」这一分支单独测出来。默认矩阵里 W09 是 unobserved
// （证据不足），所以要验证「证据到位后会怎么判」必须显式构造那个状态，
// 而不是去改生产常量。
func forceMeasurement(a *Adapter, dist Distribution, caps map[string]model.CapabilityState, helpFlags map[string]bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = &measurement{
		dist:      dist,
		version:   dist.BundledCLI,
		caps:      caps,
		helpFlags: helpFlags,
		at:        time.Unix(0, 0).UTC(),
	}
}

// observedCaps 取默认能力表并把指定行改成 observed。
func observedCaps(dist Distribution, override ...string) map[string]model.CapabilityState {
	caps := capabilityIndex(capabilitySet(dist, dist.BundledCLI, true))
	for _, name := range override {
		caps[name] = model.CapabilityObserved
	}
	return caps
}

// testRunSpec 造一个合法的冻结规格。
func testRunSpec(t *testing.T) model.RunSpec {
	t.Helper()
	return model.RunSpec{
		Instance:   "ti_abcdef",
		Version:    1,
		RunID:      "run_abc",
		Generation: 1,
		Workspace: model.WorkspaceSpec{
			Path:           t.TempDir(),
			BaselineCommit: "92ebe87",
			Branch:         "agent/x/VN-305",
		},
		Input:     "Reply with exactly: MAW-OK",
		InputMode: model.InputArg,
		Permission: model.PermissionProfile{
			Profile:      "default",
			AllowNetwork: true,
			AllowWrite:   true,
		},
		Session: model.SessionSpec{},
		Timeout: 5 * time.Minute,
	}
}

// requirePortErr 断言错误是期望的端口错误码。
func requirePortErr(t *testing.T, err error, want model.PortErrorCode) *model.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("期望端口错误 %s，实际没有错误", want)
	}
	e, ok := model.AsError(err)
	if !ok {
		t.Fatalf("期望结构化端口错误 %s，实际 %T：%v", want, err, err)
	}
	if e.Port != want {
		t.Fatalf("期望码 %s，实际 %s（%v）", want, e.Port, e)
	}
	return e
}

// requireCode 断言错误带期望的不变量码（P100 这类配置错误）。
func requireCode(t *testing.T, err error, want model.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误码 %s，实际没有错误", want)
	}
	e, ok := model.AsError(err)
	if !ok {
		t.Fatalf("期望结构化错误 %s，实际 %T：%v", want, err, err)
	}
	if e.Code != want {
		t.Fatalf("期望码 %s，实际 %s（%v）", want, e.Code, e)
	}
}
