package codebuddy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// EnvAllowlist 是唯一允许传给 CodeBuddy 的宿主环境变量名。
//
// 来源：VN-301 全部样本的 `source.env_whitelist`（HOME／LANG／NO_COLOR／PATH／SHELL／TERM／TMPDIR）。
// 白名单之外一律不传——证据 §五 记录过：继承宿主私有环境（例如指向另一份配置目录的
// CODEBUDDY_CONFIG_DIR）时，同一条调用会返回认证缺失。所以这里是「只传这些」，
// 不是「除了这些以外都传」。
var EnvAllowlist = []string{"HOME", "LANG", "NO_COLOR", "PATH", "SHELL", "TERM", "TMPDIR"}

// commandResult 是一次子进程调用的原始结果；非零退出码不算调用失败，由调用方判定。
type commandResult struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

// commandRunner 只服务只读探测（--version／--help）。
//
// 真实任务派发不在这里：那是 ProcessController（infra）的职责，且必须发生在事务外（P132）。
// 探测本身是只读的，才允许在本包内直接起进程。
type commandRunner interface {
	Run(ctx context.Context, name string, args, env []string, dir string) (commandResult, error)
}

// execRunner 是生产实现。
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args, env []string, dir string) (commandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	// 超时与取消先于退出码判定：被杀掉的进程也会报 ExitError，不能当成「CLI 自己退出」。
	if ctxErr := ctx.Err(); ctxErr != nil {
		return commandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return commandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Code: exitErr.ExitCode()}, nil
	}
	if err != nil {
		return commandResult{}, err
	}
	return commandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, nil
}

// probeEnv 按白名单从宿主环境取值，返回 KEY=VALUE 列表。
//
// 取不到的白名单变量直接跳过：传 `HOME=` 与不传 HOME 在语义上不同，
// 凭空造一个空值等于改变被测进程的行为。
func probeEnv() []string {
	out := make([]string, 0, len(EnvAllowlist))
	for _, name := range EnvAllowlist {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// probeWorkDir 是探测时的工作目录。
//
// 不用宿主当前目录：那会让 CLI 去读某个工程的 project settings，
// 把「这台机器的 CLI 能不能跑」变成「这个工程能不能跑」。
func probeWorkDir() string { return os.TempDir() }

// hasFlag 报告实测帮助清单里是否出现过某个参数（含 `--flag <value>` 写法）。
func hasFlag(helpFlags map[string]bool, flag string) bool {
	return helpFlags != nil && helpFlags[strings.TrimSpace(flag)]
}
