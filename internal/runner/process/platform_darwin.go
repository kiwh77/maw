//go:build darwin

package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const platformSupported = true

func acquireLock(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("%w: %s", ErrLocked, f.Name())
		}
		return fmt.Errorf("process: 获取运行锁: %w", err)
	}
	return nil
}

func releaseLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// inspectProcess 用 ps 读取进程启动时间；进程不存在或已退出时返回 ErrNotRunning。
func inspectProcess(pid int) (procInfo, error) {
	if pid <= 0 {
		return procInfo{}, fmt.Errorf("%w: pid %d", ErrNotRunning, pid)
	}
	cmd := exec.Command("ps", "-ww", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return procInfo{}, fmt.Errorf("%w: pid %d", ErrNotRunning, pid)
		}
		return procInfo{}, fmt.Errorf("process: 执行 ps: %w", err)
	}
	start, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.TrimSpace(string(out)), time.UTC)
	if err != nil {
		return procInfo{}, fmt.Errorf("process: 解析启动时间 %q: %w", out, err)
	}
	return procInfo{Start: start}, nil
}

// prepareCommand 让子进程独立成组，停止时可以按组确认后代。
func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalGroup 向进程组发信号；组已不存在时视为成功。
func signalGroup(pgid int, force bool) error {
	if pgid <= 0 {
		return fmt.Errorf("%w: 缺少进程组号", ErrInvalidIdentity)
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(-pgid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("process: 向进程组 %d 发送信号: %w", pgid, err)
	}
	return nil
}

// groupAlive 判断进程组是否仍有存活成员；无权限时按仍存在处理，不猜已停止。
func groupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}
