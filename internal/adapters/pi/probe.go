package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// commandResult 是一次只读探测的原始结果；非零退出码不算调用失败，由调用方判定。
type commandResult struct {
	Stdout []byte
	Stderr []byte
	Code   int
}

// commandRunner 只服务只读探测（--probe）。
//
// 真实任务派发不在这里：那是 ProcessController（infra）的职责，且必须发生在事务外。
type commandRunner interface {
	Run(ctx context.Context, name string, args, env []string, dir string) (commandResult, error)
}

// execRunner 是生产实现。
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args, env []string, dir string) (commandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env
	if len(env) == 0 {
		// envFromNames 可能全空：显式给空环境，避免继承宿主私有变量。
		cmd.Env = []string{}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
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

// probeReport 是运行时 `--probe` 的结构化自述（runtimes/pi/src/main.mjs）。
type probeReport struct {
	OK       bool           `json:"ok"`
	Protocol model.Protocol `json:"protocol"`
	SDK      string         `json:"sdk"`
	Node     string         `json:"node"`
}

// parseProbeReport 解析 --probe 输出。
//
// 只认第一行 JSON：运行时的 stdout 只放事件，任何多余输出都说明入口不是我们那个。
func parseProbeReport(stdout []byte) (probeReport, error) {
	line := strings.TrimSpace(string(stdout))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if line == "" {
		return probeReport{}, errors.New("空输出")
	}
	var report probeReport
	if err := json.Unmarshal([]byte(line), &report); err != nil {
		return probeReport{}, err
	}
	if !report.OK {
		return probeReport{}, errors.New("运行时自报 ok=false")
	}
	if strings.TrimSpace(report.SDK) == "" {
		return probeReport{}, errors.New("运行时没有自报 SDK 版本")
	}
	return report, nil
}

// envFromNames 按白名单从宿主环境取值，返回 KEY=VALUE 列表。
//
// 取不到的白名单变量直接跳过：传空值会改变被测进程的行为。
func envFromNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}
