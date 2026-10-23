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
	"time"
)

// NonceEnv 是注入受管进程的 nonce 环境变量名；验证时按完整键值匹配。
const NonceEnv = "MAW_RUN_NONCE"

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

// Identity 是一次受管进程的启动身份。Nonce 由启动方生成并注入子进程环境。
type Identity struct {
	RunID  string    `json:"run_id"`
	PID    int       `json:"pid"`
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
type Process struct {
	Identity Identity
	cmd      *exec.Cmd
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
	return &Process{Identity: id, cmd: cmd}, nil
}

// Wait 等待进程结束并返回退出错误（与 exec.Cmd.Wait 一致）。
func (p *Process) Wait() error {
	if p == nil || p.cmd == nil {
		return fmt.Errorf("%w: 未启动的进程", ErrInvalidSpec)
	}
	return p.cmd.Wait()
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
