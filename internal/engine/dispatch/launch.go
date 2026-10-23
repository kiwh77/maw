package dispatch

import (
	"context"
	"errors"
	"time"
)

// ErrLaunchUncertain 表示「启动请求可能已经生效，但调用方没能拿到可核对的身份」。
//
// 典型来源：进程已经 fork 出去、写启动日志前本进程崩溃，或 Runner 侧超时且无法
// 判定是否已接受请求。协调者遇到它只记录待核实（Uncertain），保持 Operation 与
// 占用，绝不以「没收到成功响应」为由重新启动工具进程。
var ErrLaunchUncertain = errors.New("dispatch: 启动结果不确定")

// PrepareRequest 是一次派发准备所需的输入。准备步骤必须幂等：
// 中断后重放同一请求不能产生第二份 worktree／第二组资源。
type PrepareRequest struct {
	Operation Operation
	Intent    Intent
}

// LaunchRequest 是一次工具进程启动所需的输入。
//
// Nonce 在启动前落盘到意图里，用于把「意图」和「进程启动日志」关联起来；
// 恢复时即使只拿到 nonce 也能人工核对，而不是盲目重发。
type LaunchRequest struct {
	Operation  Operation
	Nonce      string
	SpecDigest string
	PreparedAt time.Time
	Recovered  bool
}

// Preparer 负责启动前的幂等准备工作（例如绑定资源、准备 worktree）。
type Preparer interface {
	Prepare(ctx context.Context, req PrepareRequest) error
}

// Launcher 是 Runner 启动端口：成功时返回可核对的进程身份。
//
// 错误分类决定协调者行为：
//   - ErrLaunchUncertain（可包装）：保留 launching 意图，标记待核实，不重发；
//   - 其他错误：视为确定性失败，写 failed 并把 Operation 标为 rejected。
type Launcher interface {
	Launch(ctx context.Context, req LaunchRequest) (ProcessIdentity, error)
}
