package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// StartSleep 启动一个可被信号控制的 sleep 子进程（跨进程证据，非 mock）。
func StartSleep(t *testing.T, seconds int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", itoa(seconds))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
	})
	return cmd
}

// RequireProcessTools 在本机无法执行 ps 时跳过依赖进程身份探测的用例。
//
// 进程启动时间／身份探测走 /bin/ps；受限环境（沙箱禁 setuid exec）下无法取证，
// 跳过比伪造证据诚实。与 internal/runner/process 的同类守卫保持同一口径。
func RequireProcessTools(t *testing.T) {
	t.Helper()
	cmd := exec.Command("ps", "-p", "1", "-o", "pid=")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	if err := cmd.Run(); err != nil {
		t.Skipf("本机无法执行 ps，跳过依赖进程身份探测的测试: %v", err)
	}
}

// WriteScript 写可执行 shell 脚本到 dir。
func WriteScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(n int) string {
	if n <= 0 {
		return "1"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// WaitAlive 短暂等待确保进程已启动。
func WaitAlive(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid %d never became alive", pid)
}
