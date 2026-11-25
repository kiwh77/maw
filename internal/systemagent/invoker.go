package systemagent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/config"
)

// DiagnoseRequest 是发给 LLM 系统 Agent 的诊断输入。
type DiagnoseRequest struct {
	ProjectPath string
	ProjectID   string
	TaskID      string
	// JobID 是这次诊断对应的 system job（编排器必填）：Pi 诊断的用量归属、
	// run 关联与审计目录都按它反查；旧 Invoker 可忽略。
	JobID   string
	Title   string
	Reason  string
	Kind    string
	Message string
}

// Invoker 调用本机 LLM CLI（或测试替身）产出决策文本。
type Invoker interface {
	Diagnose(ctx context.Context, req DiagnoseRequest) (string, error)
}

// FuncInvoker 把函数包装成 Invoker（测试用）。
type FuncInvoker func(ctx context.Context, req DiagnoseRequest) (string, error)

func (f FuncInvoker) Diagnose(ctx context.Context, req DiagnoseRequest) (string, error) {
	return f(ctx, req)
}

// ConfigInvoker 按当前配置选择调用方式：llm_command 非空时起本机 CLI，
// 否则退回「无法判断则升级人工」的规则层。
//
// 与 ExecInvoker 的差别只有一条：命令每轮从 Config 重读，所以改 llm_command
// 或 llm_node 不需要重启常驻循环。
type ConfigInvoker struct {
	// Config 必填：返回当前生效的系统 Agent 配置。
	Config func() config.SystemAgent
	// Timeout 透传给 ExecInvoker。
	Timeout time.Duration
}

func (c ConfigInvoker) Diagnose(ctx context.Context, req DiagnoseRequest) (string, error) {
	var cmd []string
	if c.Config != nil {
		cmd = c.Config().LLMCommand
	}
	if len(cmd) == 0 {
		return `{"action":"escalate_human","reason":"llm_command 未配置，默认升级人工"}`, nil
	}
	return ExecInvoker{Argv: cmd, Timeout: c.Timeout}.Diagnose(ctx, req)
}

// ExecInvoker 用配置的 argv 起进程：prompt 进 stdin，stdout 为决策原文。
type ExecInvoker struct {
	Argv    []string
	Timeout time.Duration
	Env     []string
}

func (e ExecInvoker) Diagnose(ctx context.Context, req DiagnoseRequest) (string, error) {
	if len(e.Argv) == 0 {
		return "", fmt.Errorf("systemagent: llm_command 未配置")
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, e.Argv[0], e.Argv[1:]...)
	if req.ProjectPath != "" {
		cmd.Dir = req.ProjectPath
	}
	cmd.Env = append(os.Environ(), e.Env...)
	cmd.Stdin = strings.NewReader(BuildDiagnosePrompt(req))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("systemagent: llm 退出失败: %w; stderr=%s", err, truncate(stderr.String(), 512))
	}
	return stdout.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
