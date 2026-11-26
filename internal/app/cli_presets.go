package app

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/errx"
)

// taskPrompt 是投给本地 CLI 的固定提示：任务正文已落在 worktree 的 TASK.md，
// 不必把整张任务卡塞进 argv（argv 契约拒绝换行，且长文本不利于审计）。
const taskPrompt = "Read TASK.md in the repository root and complete the task. Work only within its write_scope, then report what you changed. Before finishing, run `maw stats report --turns <n> --input-tokens <n> --output-tokens <n>` when usage is available (omit token flags when unknown); this feeds the board statistics."

type cliPreset struct {
	Flavor string
	Label  string
	Binary string
	// BaseArgs 是进入非交互执行模式所需的子命令与输出格式参数。
	BaseArgs []string
}

var cliPresets = map[string]cliPreset{
	"claude": {
		Flavor: "claude", Label: "Claude Code", Binary: "claude",
		BaseArgs: []string{"-p"},
	},
	"codex": {
		Flavor: "codex", Label: "Codex CLI", Binary: "codex",
		BaseArgs: []string{"exec"},
	},
	"cursor": {
		Flavor: "cursor", Label: "Cursor Agent", Binary: "cursor-agent",
		BaseArgs: []string{"-p", "--output-format", "text"},
	},
	"traecli": {
		Flavor: "traecli", Label: "TRAE CLI", Binary: "traecli",
		BaseArgs: []string{"exec"},
	},
	"codebuddy": {
		Flavor: "codebuddy", Label: "CodeBuddy Code", Binary: "codebuddy",
		BaseArgs: []string{"-p", "--output-format", "text"},
	},
	"dsh": {
		Flavor: "dsh", Label: "DeepSeek Harness", Binary: "dsh",
		BaseArgs: []string{"--profile", "headless"},
	},
}

// CLIPresetNames 返回内置的 CLI 预设名，供命令帮助与测试使用。
func CLIPresetNames() []string {
	return []string{"claude", "codex", "cursor", "traecli", "codebuddy", "dsh"}
}

func cliPresetFor(name string) (cliPreset, bool) {
	p, ok := cliPresets[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

// buildCLIArgv 生成「非交互执行 + 权限档 + 任务提示」的 argv。
// 模型不进 argv，由启动时按节点 default_model 或启动覆盖注入，便于复用同一节点行。
func buildCLIArgv(cli, permission string) ([]string, error) {
	p, ok := cliPresetFor(cli)
	if !ok {
		return nil, errx.New("未知 CLI 预设: "+cli,
			"可选: "+strings.Join(CLIPresetNames(), ", "))
	}
	exe, err := resolveCLIBinary(p.Binary)
	if err != nil {
		return nil, err
	}
	argv := append([]string{exe}, p.BaseArgs...)
	argv = append(argv, permissionArgs(p.Flavor, permission)...)
	argv = append(argv, taskPrompt)
	return argv, nil
}

// permissionArgs 返回该 CLI 的权限档参数：safe 用最小权限，unattended 跳过确认。
func permissionArgs(flavor, permission string) []string {
	mode := strings.ToLower(strings.TrimSpace(permission))
	if mode == "" {
		mode = "safe"
	}
	if mode != "unattended" {
		switch flavor {
		case "claude", "traecli", "codebuddy":
			return []string{"--permission-mode", "default"}
		case "codex":
			return []string{"-s", "workspace-write"}
		}
		return nil
	}
	switch flavor {
	case "claude":
		return []string{"--dangerously-skip-permissions"}
	case "codex", "traecli":
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	case "cursor":
		return []string{"--force"}
	case "codebuddy":
		return []string{"-y"}
	}
	return nil
}

// modelArgsFor 按 CLI 的旗标习惯返回模型参数；dsh 的模型由 profile patch 注入。
func modelArgsFor(flavor, modelName string) []string {
	switch strings.ToLower(strings.TrimSpace(flavor)) {
	case "", "dsh":
		return nil
	case "codex", "traecli":
		return []string{"-m", modelName}
	default:
		return []string{"--model", modelName}
	}
}

// resolveCLIBinary 解析绝对入口，并跟随 nvmd 之类的版本管理器软链。
func resolveCLIBinary(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", errx.New(fmt.Sprintf("未找到本地 CLI: %s", name),
			"安装后重试，或用 --cmd 显式指定入口")
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != "" {
		path = resolved
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}
