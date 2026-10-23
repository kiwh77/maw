package process

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newNonce(t *testing.T) string {
	t.Helper()
	nonce, err := NewNonce()
	if err != nil {
		t.Fatalf("NewNonce: %v", err)
	}
	if len(nonce) != 32 {
		t.Fatalf("nonce 长度 = %d", len(nonce))
	}
	return nonce
}

func startSleep(t *testing.T, nonce string) *Process {
	t.Helper()
	p, err := Start(StartSpec{
		RunID: "run_test",
		Argv:  []string{"/bin/sleep", "30"},
		Dir:   t.TempDir(),
		Nonce: nonce,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
	})
	return p
}

func TestStartCaptureAndVerifyIdentity(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("仅 macOS 实现进程身份")
	}
	nonce := newNonce(t)
	p := startSleep(t, nonce)

	if p.Identity.RunID != "run_test" || p.Identity.PID <= 0 || p.Identity.Nonce != nonce {
		t.Fatalf("捕获身份 = %+v", p.Identity)
	}
	if err := Verify(p.Identity, nonce); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	path := filepath.Join(t.TempDir(), "identity.json")
	if err := WriteIdentity(path, p.Identity); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}
	id, err := ReadIdentity(path)
	if err != nil {
		t.Fatalf("ReadIdentity: %v", err)
	}
	if id.PID != p.Identity.PID || !id.Start.Equal(p.Identity.Start) || id.Nonce != nonce {
		t.Fatalf("往返身份 = %+v", id)
	}
	if err := Verify(id, nonce); err != nil {
		t.Fatalf("往返后 Verify: %v", err)
	}

	tamperedStart := id
	tamperedStart.Start = id.Start.Add(-time.Second)
	if err := Verify(tamperedStart, nonce); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("启动时间变化应判不匹配: %v", err)
	}
	tamperedNonce := id
	tamperedNonce.Nonce = "ffffffffffffffffffffffffffffffff"
	if err := Verify(tamperedNonce, nonce); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("nonce 变化应判不匹配: %v", err)
	}
}

func TestCaptureRejectsExitedProcess(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("仅 macOS 实现进程身份")
	}
	cmd := exec.Command("/bin/sleep", "0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动短进程: %v", err)
	}
	_ = cmd.Wait()
	if _, err := Capture("run_other", cmd.Process.Pid, newNonce(t)); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("已退出进程不应被捕获: %v", err)
	}
}

func TestVerifyRejectsExitedProcess(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("仅 macOS 实现进程身份")
	}
	cmd := exec.Command("/bin/sleep", "0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动短进程: %v", err)
	}
	_ = cmd.Wait()
	nonce := newNonce(t)
	err := Verify(Identity{RunID: "run_gone", PID: cmd.Process.Pid, Start: time.Now(), Nonce: nonce}, nonce)
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("已退出进程应判不存在: %v", err)
	}
}

func TestStartUsesArgvAndFixedWorkingDirectory(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("仅 macOS 实现进程身份")
	}
	dir := filepath.Join(t.TempDir(), "dir with space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建目录: %v", err)
	}
	p, err := Start(StartSpec{
		RunID: "run_cwd",
		Argv:  []string{"/bin/sh", "-c", "pwd > marker.txt; exec sleep 30"},
		Dir:   dir,
		Nonce: newNonce(t),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
	}()

	marker := filepath.Join(dir, "marker.txt")
	var data []byte
	for i := 0; i < 50; i++ {
		data, err = os.ReadFile(marker)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("读取 cwd 标记: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("解析目录: %v", err)
	}
	if strings.TrimSpace(string(data)) != want {
		t.Fatalf("子进程 cwd = %q，期望 %q", strings.TrimSpace(string(data)), want)
	}
}

func TestStartRejectsInvalidSpec(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("仅 macOS 实现进程身份")
	}
	cases := []StartSpec{
		{RunID: "run_bad", Argv: nil, Dir: t.TempDir(), Nonce: "n"},
		{RunID: "run_bad", Argv: []string{"/bin/sleep", "1"}, Dir: "relative/path", Nonce: "n"},
		{RunID: "run_bad", Argv: []string{"/bin/sleep", "1"}, Dir: t.TempDir(), Nonce: ""},
	}
	for i, spec := range cases {
		if _, err := Start(spec); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("用例 %d 应拒绝: %v", i, err)
		}
	}
}

func TestLockExcludesSecondSupervisor(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("仅 macOS 实现排他锁")
	}
	path := filepath.Join(t.TempDir(), "runner.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("首次获取锁: %v", err)
	}
	if _, err := AcquireLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("第二个监督实例应被拒绝: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("释放锁: %v", err)
	}
	third, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("释放后应可重新获取: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("释放锁: %v", err)
	}
}

func TestUnsupportedPlatformIsExplicit(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("仅在非 macOS 构建时验证显式不支持")
	}
	if _, err := AcquireLock(filepath.Join(t.TempDir(), "runner.lock")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("非 macOS 获取锁应明确不支持: %v", err)
	}
	if _, err := Start(StartSpec{RunID: "r", Argv: []string{"/bin/true"}, Dir: t.TempDir(), Nonce: "n"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("非 macOS 启动应明确不支持: %v", err)
	}
}
