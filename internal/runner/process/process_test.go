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

// requireDarwinProcessTools 跳过非 macOS，以及本机禁止执行 ps 的环境。
// 身份捕获／核对依赖 /bin/ps；部分受限环境会报 operation not permitted。
func requireDarwinProcessTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("仅 macOS 实现进程身份与停止")
	}
	cmd := exec.Command("ps", "-p", "1", "-o", "pid=")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	if err := cmd.Run(); err != nil {
		t.Skipf("本机无法执行 ps，跳过依赖进程身份探测的测试: %v", err)
	}
}

func startSleep(t *testing.T, nonce string) *Process {
	t.Helper()
	requireDarwinProcessTools(t)
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
		_, _ = p.Stop(StopCancel)
	})
	return p
}

func TestStartCaptureAndVerifyIdentity(t *testing.T) {
	requireDarwinProcessTools(t)
	nonce := newNonce(t)
	p := startSleep(t, nonce)

	if p.Identity.RunID != "run_test" || p.Identity.PID <= 0 || p.Identity.Nonce != nonce {
		t.Fatalf("捕获身份 = %+v", p.Identity)
	}
	if p.Identity.PGID != p.Identity.PID {
		t.Fatalf("子进程应独立成组: %+v", p.Identity)
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
	requireDarwinProcessTools(t)
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
	requireDarwinProcessTools(t)
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
	requireDarwinProcessTools(t)
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
		_, _ = p.Stop(StopCancel)
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
	requireDarwinProcessTools(t)
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

func TestStopRecordsCancelEvidence(t *testing.T) {
	requireDarwinProcessTools(t)
	p := startSleep(t, newNonce(t))
	ev, err := p.Stop(StopCancel)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ev.Outcome != OutcomeStopped || ev.Reason != StopCancel {
		t.Fatalf("取消证据 = %+v", ev)
	}
	if !ev.Exited || !ev.GroupEmpty || !ev.DescendantsConfirmed || !ev.Reusable() {
		t.Fatalf("应确认整组停止: %+v", ev)
	}
	if ev.Signal != "term" || ev.Escalated {
		t.Fatalf("常规取消不应升级: %+v", ev)
	}

	again, err := p.Stop(StopCancel)
	if err != nil {
		t.Fatalf("重复 Stop: %v", err)
	}
	if again.Recorded != ev.Recorded {
		t.Fatalf("重复 Stop 应返回同一份证据: %+v vs %+v", again, ev)
	}
}

func TestStopEscalatesWhenTermIgnored(t *testing.T) {
	requireDarwinProcessTools(t)
	// sh 的 trap 在进程组 SIGTERM 下仍可能整组退出；用忽略 SIGTERM 的常驻进程验证升级。
	python := "/usr/bin/python3"
	if _, err := os.Stat(python); err != nil {
		t.Skip("需要 /usr/bin/python3 构造忽略 TERM 的进程")
	}
	dir := t.TempDir()
	p, err := Start(StartSpec{
		RunID: "run_stubborn",
		Argv: []string{python, "-c",
			"import pathlib,signal,time\nsignal.signal(signal.SIGTERM, signal.SIG_IGN)\npathlib.Path('ready').write_text('1')\nwhile True:\n time.sleep(30)\n"},
		Dir:   dir,
		Nonce: newNonce(t),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_, _ = p.Stop(StopCancel)
	}()
	ready := filepath.Join(dir, "ready")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("子进程未就绪（忽略 TERM 的 handler 尚未安装）")
		}
		time.Sleep(20 * time.Millisecond)
	}

	ev, err := p.Stop(StopTimeout)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !ev.Escalated || ev.Signal != "kill" {
		t.Fatalf("TERM 无效时应升级 KILL: %+v", ev)
	}
	if ev.Reason != StopTimeout || ev.Outcome != OutcomeStopped || !ev.Reusable() {
		t.Fatalf("超时证据 = %+v", ev)
	}
}

func TestStopRecordsAlreadyExited(t *testing.T) {
	requireDarwinProcessTools(t)
	p, err := Start(StartSpec{
		RunID: "run_done",
		Argv:  []string{"/bin/sh", "-c", "exit 3"},
		Dir:   t.TempDir(),
		Nonce: newNonce(t),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = p.Wait()

	ev, err := p.Stop(StopDone)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ev.Outcome != OutcomeExited || !ev.Exited || ev.ExitCode != 3 {
		t.Fatalf("已完成证据 = %+v", ev)
	}
	if ev.Signal != "" || !ev.DescendantsConfirmed || !ev.Reusable() {
		t.Fatalf("已完成且组已清空应可复用: %+v", ev)
	}
}

func TestRecoverRunningRunIsUnknown(t *testing.T) {
	requireDarwinProcessTools(t)
	p := startSleep(t, newNonce(t))
	ev, err := Recover(p.Identity)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if ev.Outcome != OutcomeUnknown || ev.Exited || ev.Reusable() {
		t.Fatalf("仍在运行的运行不应宣布可复用: %+v", ev)
	}
}

func TestRecoverLostRunAfterExternalKill(t *testing.T) {
	requireDarwinProcessTools(t)
	p := startSleep(t, newNonce(t))
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatalf("外部终止: %v", err)
	}
	_ = p.Wait()

	ev, err := Recover(p.Identity)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !ev.Lost || !ev.Exited {
		t.Fatalf("应记录进程失联: %+v", ev)
	}
	if ev.Outcome != OutcomeExited || !ev.Reusable() {
		t.Fatalf("组已清空时失联也可复用: %+v", ev)
	}
}

func TestRecoverUnknownWhenGroupUnconfirmed(t *testing.T) {
	requireDarwinProcessTools(t)
	live := startSleep(t, newNonce(t))
	dead := exec.Command("/bin/sh", "-c", "exit 0")
	if err := dead.Start(); err != nil {
		t.Fatalf("启动短进程: %v", err)
	}
	_ = dead.Wait()

	// 父进程 PID 已消失，但记录里的组号仍指向一个存活组：无法确认归属。
	id := Identity{
		RunID: "run_lost",
		PID:   dead.Process.Pid,
		PGID:  live.Identity.PGID,
		Start: time.Now().UTC(),
		Nonce: newNonce(t),
	}
	ev, err := Recover(id)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if ev.Outcome != OutcomeUnknown || ev.Reusable() || ev.DescendantsConfirmed {
		t.Fatalf("进程组未确认时不得宣布可复用: %+v", ev)
	}
}

func TestReusableRequiresConfirmedDescendants(t *testing.T) {
	cases := []struct {
		name string
		ev   StopEvidence
		want bool
	}{
		{"父进程退出但组未确认", StopEvidence{Exited: true}, false},
		{"组已空但未确认后代", StopEvidence{GroupEmpty: true}, false},
		{"后代已确认", StopEvidence{GroupEmpty: true, DescendantsConfirmed: true}, true},
	}
	for _, c := range cases {
		if got := c.ev.Reusable(); got != c.want {
			t.Fatalf("%s: Reusable = %v，期望 %v", c.name, got, c.want)
		}
	}
}

func TestStopRejectsUnknownReason(t *testing.T) {
	requireDarwinProcessTools(t)
	p := startSleep(t, newNonce(t))
	if _, err := p.Stop(StopReason("panic")); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("未知停止原因应拒绝: %v", err)
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
	if _, err := Recover(Identity{RunID: "r", PID: 1, Start: time.Now(), Nonce: "n"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("非 macOS 恢复核对应明确不支持: %v", err)
	}
}
