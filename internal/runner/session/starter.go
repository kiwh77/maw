package session

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// errNoStdout 报告启动器没有给出输出管道：没有输出就没有可摄取的记录。
var errNoStdout = errors.New("session: 启动器未提供标准输出")

// child 是一个已启动的来源进程。
type child interface {
	// stdout 是逐行协议的来源（stream-json 一行一条记录）。
	stdout() io.Reader
	// stderr 返回已捕获的错误输出（有上限，只用于诊断）。
	stderr() string
	// wait 等待退出；进程被信号杀死时也返回（错误里带退出原因）。
	wait() error
	// exitCode 返回退出码；尚未退出或无法判定时为 nil。
	exitCode() *int
	// stop 终止进程并给出可追溯的停止证据。
	stop(mode model.StopMode) (model.StopEvidence, error)
}

// starter 按启动规格起一个进程。抽象出来是为了让摄取／提交／幂等这些语义
// 不依赖真实子进程也能测；生产路径用 execStarter。
type starter interface {
	start(spec model.LaunchSpec, runID model.RunID, gen model.Generation) (child, error)
}

// stopGrace 是优雅停止的等待上限；超时后强制终止。
const stopGrace = 2 * time.Second

// envFor 按白名单从宿主环境取值。
//
// 只传名单里的变量（LaunchSpec.EnvAllowlist 已经是白名单），
// 不把宿主私有环境漏给来源进程。
func envFor(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// execStarter 是生产实现：os/exec + 便携信号。
type execStarter struct{}

func (execStarter) start(spec model.LaunchSpec, runID model.RunID, gen model.Generation) (child, error) {
	if spec.Executable == "" {
		return nil, errNoStdout
	}
	cmd := exec.Command(spec.Executable, spec.Argv...)
	cmd.Dir = spec.Cwd
	cmd.Env = envFor(spec.EnvAllowlist)
	if spec.StdinMode == model.StdinPipe {
		if spec.StdinData != "" {
			// 一次性配置：写完即关，子进程读到 EOF 就知道输入结束。
			cmd.Stdin = strings.NewReader(spec.StdinData)
		} else {
			cmd.Stdin = os.Stdin
		}
	}
	// 错误输出有上限：诊断够用，别把几 MB 拉进内存。
	stderr := &boundedBuffer{limit: 8 << 10}
	cmd.Stderr = stderr
	// 自建管道而不是 cmd.StdoutPipe()：后者会由 cmd.Wait() 关闭读端，
	// 一旦 Wait 与读取并发，还没读完的行会被截断。自建管道里父进程只持有读端，
	// Wait 关的是已经关掉的写端，读取不受影响。
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = stdoutW
	if err := cmd.Start(); err != nil {
		_ = stdoutW.Close()
		_ = stdout.Close()
		return nil, err
	}
	// 父进程不留写端：子进程退出后读端才会看到 EOF。
	_ = stdoutW.Close()

	c := &execChild{cmd: cmd, out: stdout, errBuf: stderr, done: make(chan struct{})}
	go c.collect()
	return c, nil
}

type execChild struct {
	cmd    *exec.Cmd
	out    *os.File
	errBuf *boundedBuffer
	done   chan struct{}

	mu      sync.Mutex
	waited  bool
	code    *int
	waitErr error
}

// collect 独占 Wait：谁都不许再调 cmd.Wait()，退出事实只在这里记一次。
func (c *execChild) collect() {
	err := c.cmd.Wait()
	c.mu.Lock()
	c.waitErr = err
	c.waited = true
	if c.cmd.ProcessState != nil {
		code := c.cmd.ProcessState.ExitCode()
		c.code = &code
	}
	c.mu.Unlock()
	close(c.done)
}

func (c *execChild) stdout() io.Reader { return c.out }

func (c *execChild) stderr() string { return c.errBuf.String() }

func (c *execChild) wait() error {
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.waitErr
}

func (c *execChild) exitCode() *int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.code
}

// stop 先礼后兵：Interrupt，等 stopGrace，再 Kill。
//
// Method 写明实际用了什么手段；Confirmed 只在确实观察到进程退出时为真——
// 没观察到就返回未确认（stop_unconfirmed 是合法结果，不是失败）。
func (c *execChild) stop(mode model.StopMode) (model.StopEvidence, error) {
	if c.exited() {
		return model.StopEvidence{StopConfirmed: true, Method: "already-exited"}, nil
	}
	if c.cmd.Process == nil {
		return model.StopEvidence{}, errors.New("session: 进程未启动")
	}

	if mode == model.StopGraceful {
		if err := c.cmd.Process.Signal(os.Interrupt); err != nil {
			return model.StopEvidence{}, err
		}
		if c.waitExit(stopGrace) {
			return model.StopEvidence{StopConfirmed: true, Method: "interrupt"}, nil
		}
	}

	if err := c.cmd.Process.Kill(); err != nil {
		return model.StopEvidence{}, err
	}
	if !c.waitExit(stopGrace) {
		return model.StopEvidence{Method: "kill"}, nil
	}
	return model.StopEvidence{StopConfirmed: true, Method: "kill"}, nil
}

// exited 报告进程是否已经退出。
func (c *execChild) exited() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.waited
}

// waitExit 在预算内等进程退出，返回是否等到。
func (c *execChild) waitExit(budget time.Duration) bool {
	deadline := time.After(budget)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if c.exited() {
				return true
			}
		case <-deadline:
			return c.exited()
		}
	}
}

// boundedBuffer 是只保留前 limit 字节的写缓冲。
type boundedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
