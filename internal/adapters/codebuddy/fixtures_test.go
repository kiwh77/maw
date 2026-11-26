package codebuddy

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
// 真实清单见 docs/evidence/codebuddy/samples/VN301-S02-help-probe/stdout.txt（13,452 字节）。
const helpFixture = `Usage: codebuddy|cbc [options] [command] [prompt]

Options:
  -p, --print                                      Print response and exit (useful for pipes)
  --output-format <format>                         Output format (only works with --print)
  --include-partial-messages                       Output raw SSE incremental messages
  --permission-mode <mode>                         Default permission mode for this process
  --mcp-config <fileOrString>                      Load MCP servers from a JSON file or string
  --strict-mcp-config                              Only use MCP servers from --mcp-config
  -r, --resume [sessionId]                         Resume a conversation
  -V, --version                                    output the version number
  -h, --help                                       display help for command
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

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		versionOut: string(MeasuredCLIVersion) + "\n",
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

// testNode 造一个可探测的节点：入口是 `#!/usr/bin/env node` 脚本，
// 与实测的本机形态一致（/Users/…/bin/codebuddy -> …/bin/codebuddy，脚本首行即此 shebang）。
func testNode(t *testing.T) (model.NodeConfig, string, string) {
	t.Helper()

	bin := t.TempDir()
	nodeBin := filepath.Join(bin, "node")
	mustWrite(t, nodeBin, "#!/bin/sh\nexit 0\n", 0o755)
	t.Setenv("PATH", bin)

	scriptDir := filepath.Join(t.TempDir(), "dir with space")
	script := filepath.Join(scriptDir, "codebuddy cli")
	mustWrite(t, script, "#!/usr/bin/env node\nconsole.log('2.173.0')\n", 0o755)

	cfg := model.NodeConfig{
		NodeID:     "mbp-codebuddy",
		Name:       "本机 CodeBuddy",
		Adapter:    model.AdapterCodeBuddy,
		EntryPath:  script,
		ProfileRef: "profiles/codebuddy-default",
		Mode:       model.NodeManagedCLI,
		Platform:   runtime.GOOS,
		SlotLimit:  1,
		AcceptNew:  true,
		Timeout:    time.Minute,
	}
	return cfg, script, nodeBin
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

// probedAdapter 返回一个已完成探测（2.173.0）的适配器。
func probedAdapter(t *testing.T, opts ...Option) (*Adapter, model.NodeConfig, *fakeRunner) {
	t.Helper()
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	a := newAdapter(t, cfg, fake, opts...)
	if _, err := a.Probe(context.Background(), cfg); err != nil {
		t.Fatalf("探测失败：%v", err)
	}
	fake.calls = nil
	return a, cfg, fake
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
			Branch:         "agent/x/VN-303",
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
