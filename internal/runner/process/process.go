package process

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// NonceEnv 是注入受管进程的 nonce 环境变量名；验证时按完整键值匹配。
const NonceEnv = "MAW_RUN_NONCE"

// stopGrace 是单次终止阶段的等待上限；TERM 无效后升级 KILL，再等同样时长。
const stopGrace = 2 * time.Second

// stopPoll 是等待进程组清空时的轮询间隔。
const stopPoll = 20 * time.Millisecond

var (
	// ErrLocked 表示同一 Run 已有另一个监督实例持有排他锁。
	ErrLocked = errors.New("process: 运行锁已被占用")
	// ErrUnsupported 表示当前平台没有经过验证的进程控制能力。
	ErrUnsupported = errors.New("process: 当前平台未验证或不支持")
	// ErrNotRunning 表示进程不存在或已退出。
	ErrNotRunning = errors.New("process: 进程不存在")
	// ErrIdentityMismatch 表示 PID、启动时间或 nonce 与记录不一致。
	ErrIdentityMismatch = errors.New("process: 进程身份不匹配")
	// ErrInvalidIdentity 表示身份记录本身不完整。
	ErrInvalidIdentity = errors.New("process: 身份记录不完整")
	// ErrInvalidSpec 表示启动规格不可执行。
	ErrInvalidSpec = errors.New("process: 启动规格不合法")
)

// StopReason 是发起停止的业务原因；证据里保留原因，便于对账。
type StopReason string

const (
	// StopCancel 表示用户或中枢主动取消。
	StopCancel StopReason = "cancel"
	// StopTimeout 表示超过运行时限。
	StopTimeout StopReason = "timeout"
	// StopDone 表示正常完成或已自行退出时的收尾确认。
	StopDone StopReason = "done"
	// StopLost 表示监督方丢失了运行（例如中枢重启后按记录核对）。
	StopLost StopReason = "lost"
)

func (r StopReason) valid() bool {
	switch r {
	case StopCancel, StopTimeout, StopDone, StopLost:
		return true
	}
	return false
}

// StopOutcome 是停止事实的确认等级。
type StopOutcome string

const (
	// OutcomeStopped 表示本次发送了信号并确认进程组已清空。
	OutcomeStopped StopOutcome = "stopped"
	// OutcomeExited 表示进程已自行退出，且进程组已确认清空。
	OutcomeExited StopOutcome = "exited"
	// OutcomeUnknown 表示无法确认进程或后代已经全部停止。
	OutcomeUnknown StopOutcome = "unknown"
)

// StopEvidence 是一次停止尝试的真实结果。Reusable 只依据后代确认事实，
// 不依据父进程退出、锁释放或监督进程自身的生命周期。
type StopEvidence struct {
	RunID   string      `json:"run_id"`
	Reason  StopReason  `json:"reason"`
	Outcome StopOutcome `json:"outcome"`
	// Signal 是实际发出的最后一个信号："term"、"kill"；未发信号时为空。
	Signal string `json:"signal,omitempty"`
	// Escalated 表示 TERM 未生效后升级为 KILL。
	Escalated bool `json:"escalated,omitempty"`
	// ExitCode 仅在 Exited 为真时有意义；非正常退出时为 -1。
	ExitCode int `json:"exit_code"`
	// Exited 表示父进程已确认退出。
	Exited bool `json:"exited"`
	// GroupEmpty 表示按记录的子进程组确认已无存活成员。
	GroupEmpty bool `json:"group_empty"`
	// DescendantsConfirmed 表示后代状态已被确认；只有进程组清空才为真。
	DescendantsConfirmed bool `json:"descendants_confirmed"`
	// Lost 表示按身份记录找不到原进程（进程失联）。
	Lost bool `json:"lost,omitempty"`
	// Note 记录无法确认的原因；OutcomeUnknown 时必填。
	Note string `json:"note,omitempty"`
	// Recorded 是证据生成时间（UTC）。
	Recorded time.Time `json:"recorded"`
}

// Reusable 回答“目录能否复用”。只有后代停止被确认才允许复用；
// 父进程退出、锁已释放都不构成可复用证据。
func (e StopEvidence) Reusable() bool {
	return e.DescendantsConfirmed && e.GroupEmpty
}

// Identity 是一次受管进程的启动身份。Nonce 由启动方生成并注入子进程环境。
// PGID 是本包创建的子进程组；停止时按组确认没有残留后代。
type Identity struct {
	RunID  string    `json:"run_id"`
	PID    int       `json:"pid"`
	PGID   int       `json:"pgid,omitempty"`
	Start  time.Time `json:"start"`
	Nonce  string    `json:"nonce"`
	Booted time.Time `json:"recorded_at"`
}

// procInfo 是平台层读到的进程事实。
type procInfo struct {
	Start time.Time
}

// NewNonce 生成一次运行专用的随机 nonce。
func NewNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("process: 生成 nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Capture 读取进程当前启动时间并记录身份；读不到就返回错误，不猜。
func Capture(runID string, pid int, nonce string) (Identity, error) {
	if runID == "" || pid <= 0 || nonce == "" {
		return Identity{}, fmt.Errorf("%w: run_id/pid/nonce 必填", ErrInvalidIdentity)
	}
	info, err := inspectProcess(pid)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		RunID:  runID,
		PID:    pid,
		Start:  info.Start,
		Nonce:  nonce,
		Booted: time.Now().UTC(),
	}, nil
}

// Verify 校验 PID 仍在运行、启动时间未变，且 nonce 属于本次 Run。
// expectedNonce 来自该 Run 的冻结规格，防止旧身份记录被挪用。
func Verify(id Identity, expectedNonce string) error {
	if err := id.validate(); err != nil {
		return err
	}
	if expectedNonce == "" || id.Nonce != expectedNonce {
		return fmt.Errorf("%w: nonce 不属于本次 Run", ErrIdentityMismatch)
	}
	info, err := inspectProcess(id.PID)
	if err != nil {
		return err
	}
	if !info.Start.Equal(id.Start) {
		// PID 被回收复用：启动时间不同即不是同一次运行。
		return fmt.Errorf("%w: pid %d 启动时间 %s != %s",
			ErrIdentityMismatch, id.PID, info.Start.Format(time.RFC3339), id.Start.Format(time.RFC3339))
	}
	return nil
}

func (id Identity) validate() error {
	if id.RunID == "" || id.PID <= 0 || id.Start.IsZero() || id.Nonce == "" {
		return fmt.Errorf("%w: 需要 run_id、pid、start 和 nonce", ErrInvalidIdentity)
	}
	return nil
}

// group 返回停止时使用的进程组号；旧记录没有 PGID 时退回 PID。
func (id Identity) group() int {
	if id.PGID > 0 {
		return id.PGID
	}
	return id.PID
}

// WriteIdentity 原子记录进程身份，供中枢重启后核实。
func WriteIdentity(path string, id Identity) error {
	if err := id.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(id)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("process: 写进程身份: %w", err)
	}
	return nil
}

// ReadIdentity 读取进程身份记录。
func ReadIdentity(path string) (Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, fmt.Errorf("process: 读进程身份: %w", err)
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return Identity{}, fmt.Errorf("process: 进程身份损坏: %w", err)
	}
	if err := id.validate(); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// StartSpec 是一次受管启动：argv 数组、固定工作目录、可选标准输入。
// 不拼 shell 字符串，避免路径空格和注入改变真实命令。
type StartSpec struct {
	RunID string
	// Argv 必须非空；Argv[0] 是可执行文件，其余是参数，原样传递。
	Argv []string
	// Dir 必须是绝对路径；启动后不再随调用方工作目录漂移。
	Dir   string
	Stdin []byte
	// Env 是额外环境变量；nonce 会以 MAW_RUN_NONCE 注入。
	Env   []string
	Nonce string
}

// Process 是已启动的受管进程；身份在启动成功后立即捕获。
// 子进程独立成组（PGID == 启动时的 PID），停止按整组确认。
type Process struct {
	Identity Identity
	cmd      *exec.Cmd
	done     chan struct{}
	waitErr  error
	stopMu   sync.Mutex
	evidence *StopEvidence
}

// Start 以参数数组启动进程，固定 cwd，并从环境注入 nonce 后捕获身份。
func Start(spec StartSpec) (*Process, error) {
	if !platformSupported {
		return nil, ErrUnsupported
	}
	if spec.RunID == "" || len(spec.Argv) == 0 || spec.Argv[0] == "" || spec.Nonce == "" {
		return nil, fmt.Errorf("%w: run_id/argv/nonce 必填", ErrInvalidSpec)
	}
	if !filepath.IsAbs(spec.Dir) {
		return nil, fmt.Errorf("%w: 工作目录必须是绝对路径", ErrInvalidSpec)
	}
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	prepareCommand(cmd)
	env := append([]string{}, os.Environ()...)
	env = append(env, spec.Env...)
	env = append(env, NonceEnv+"="+spec.Nonce)
	cmd.Env = env
	if len(spec.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("process: 启动 %s: %w", spec.Argv[0], err)
	}
	id, err := Capture(spec.RunID, cmd.Process.Pid, spec.Nonce)
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, err
	}
	// 子进程是新建进程组的组长，PGID 等于 PID。
	id.PGID = cmd.Process.Pid
	p := &Process{Identity: id, cmd: cmd, done: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// Wait 等待进程结束并返回退出错误（与 exec.Cmd.Wait 一致）。
func (p *Process) Wait() error {
	if p == nil || p.done == nil {
		return fmt.Errorf("%w: 未启动的进程", ErrInvalidSpec)
	}
	<-p.done
	return p.waitErr
}

// Stop 终止本次运行的整个进程组并记录停止证据。
// 返回的 error 只表示调用方式或平台能力有问题；进程层面的不确定
// 用 OutcomeUnknown 表达，不伪装成成功。
func (p *Process) Stop(reason StopReason) (StopEvidence, error) {
	if !platformSupported {
		return StopEvidence{}, ErrUnsupported
	}
	if p == nil || p.done == nil {
		return StopEvidence{}, fmt.Errorf("%w: 未启动的进程", ErrInvalidSpec)
	}
	if !reason.valid() {
		return StopEvidence{}, fmt.Errorf("%w: 未知停止原因 %q", ErrInvalidSpec, reason)
	}
	p.stopMu.Lock()
	defer p.stopMu.Unlock()
	if p.evidence != nil {
		return *p.evidence, nil
	}

	pgid := p.Identity.group()
	ev := StopEvidence{
		RunID:    p.Identity.RunID,
		Reason:   reason,
		ExitCode: -1,
		Recorded: time.Now().UTC(),
	}

	parentExited := p.waitDone(0)
	if parentExited {
		ev.Exited = true
		ev.ExitCode = exitCode(p.waitErr)
	}
	if parentExited && !groupAlive(pgid) {
		ev.Outcome = OutcomeExited
		ev.GroupEmpty = true
		ev.DescendantsConfirmed = true
		p.evidence = &ev
		return ev, nil
	}

	if err := signalGroup(pgid, false); err != nil {
		ev.Outcome = OutcomeUnknown
		ev.Note = "发送 TERM 失败: " + err.Error()
		p.evidence = &ev
		return ev, nil
	}
	ev.Signal = "term"

	if !parentExited {
		parentExited = p.waitDone(stopGrace)
		if parentExited {
			ev.Exited = true
			ev.ExitCode = exitCode(p.waitErr)
		}
	}
	waitEmpty(pgid, stopGrace)

	if groupAlive(pgid) {
		ev.Escalated = true
		ev.Signal = "kill"
		if err := signalGroup(pgid, true); err != nil {
			ev.Outcome = OutcomeUnknown
			ev.Note = "升级 KILL 失败: " + err.Error()
			p.evidence = &ev
			return ev, nil
		}
		if !parentExited {
			parentExited = p.waitDone(stopGrace)
			if parentExited {
				ev.Exited = true
				ev.ExitCode = exitCode(p.waitErr)
			}
		}
		waitEmpty(pgid, stopGrace)
	}

	if !parentExited {
		ev.Exited = false
	}
	if !groupAlive(pgid) {
		ev.Outcome = OutcomeStopped
		ev.GroupEmpty = true
		ev.DescendantsConfirmed = true
	} else {
		ev.Outcome = OutcomeUnknown
		ev.Note = "进程组仍有存活成员，无法确认后代已停止"
	}
	p.evidence = &ev
	return ev, nil
}

// Recover 在监督进程重启后按身份记录核对运行是否失联，并给出只读证据。
// 它不发送信号，也不清理未知归属的进程组。
func Recover(id Identity) (StopEvidence, error) {
	if !platformSupported {
		return StopEvidence{}, ErrUnsupported
	}
	if err := id.validate(); err != nil {
		return StopEvidence{}, err
	}
	ev := StopEvidence{
		RunID:    id.RunID,
		Reason:   StopLost,
		ExitCode: -1,
		Recorded: time.Now().UTC(),
	}
	info, err := inspectProcess(id.PID)
	switch {
	case err == nil && info.Start.Equal(id.Start):
		// 进程仍在，但监督关系已丢失：停止状态无法确认。
		ev.Outcome = OutcomeUnknown
		ev.Note = "进程仍在运行，停止状态未确认"
		return ev, nil
	case err == nil:
		// PID 已被复用：原进程确定退出，但进程组可能也被复用。
		ev.Lost = true
		ev.Exited = true
	case errors.Is(err, ErrNotRunning):
		ev.Lost = true
		ev.Exited = true
	default:
		return StopEvidence{}, err
	}

	alive := groupAlive(id.group())
	ev.GroupEmpty = !alive
	ev.DescendantsConfirmed = ev.GroupEmpty
	if ev.GroupEmpty {
		ev.Outcome = OutcomeExited
	} else {
		ev.Outcome = OutcomeUnknown
		ev.Note = "进程已失联，但进程组仍有存活成员，无法确认归属"
	}
	return ev, nil
}

func (p *Process) waitDone(d time.Duration) bool {
	if d <= 0 {
		select {
		case <-p.done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-p.done:
		return true
	case <-timer.C:
		return false
	}
}

func waitEmpty(pgid int, d time.Duration) {
	deadline := time.Now().Add(d)
	for groupAlive(pgid) && time.Now().Before(deadline) {
		time.Sleep(stopPoll)
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// Lock 是同一 Run 的排他执行锁；第二个监督实例只能拿到 ErrLocked。
type Lock struct {
	file *os.File
	path string
}

// AcquireLock 打开（必要时创建）锁文件并尝试非阻塞排他锁。
func AcquireLock(path string) (*Lock, error) {
	if !platformSupported {
		return nil, ErrUnsupported
	}
	if path == "" {
		return nil, fmt.Errorf("%w: 缺少锁文件路径", ErrInvalidSpec)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("process: 创建锁目录: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("process: 打开锁文件: %w", err)
	}
	if err := acquireLock(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{file: f, path: path}, nil
}

// Release 释放排他锁；可重复调用。
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	if err := releaseLock(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("process: 释放运行锁: %w", err)
	}
	return f.Close()
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if name != "" {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	name = ""
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
