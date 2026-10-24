package zcode

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

var (
	_ ports.Probe     = (*Adapter)(nil)
	_ ports.Launch    = (*Adapter)(nil)
	_ ports.Decode    = (*Adapter)(nil)
	_ ports.Interpret = (*Adapter)(nil)
)

const DefaultProbeTimeout = 10 * time.Second

// Adapter 绑定单个 ZCode 节点。
type Adapter struct {
	cfg     model.NodeConfig
	runner  commandRunner
	timeout time.Duration
	clock   func() time.Time

	mu   sync.Mutex
	last *measurement
}

type measurement struct {
	version model.SemVer
	view    ConfigView
	caps    map[string]model.CapabilityState
	at      time.Time
}

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, exitCode int, err error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), string(ee.Stderr), ee.ExitCode(), nil
	}
	if err != nil {
		return string(out), "", -1, err
	}
	return string(out), "", 0, nil
}

// Option 构造选项。
type Option func(*Adapter)

// WithRunner 注入命令执行。
func WithRunner(r commandRunner) Option { return func(a *Adapter) { a.runner = r } }

// New 构造适配器。
func New(cfg model.NodeConfig, opts ...Option) (*Adapter, error) {
	if err := model.Err(cfg.Validate()); err != nil {
		return nil, err
	}
	if cfg.Adapter != model.AdapterZCode {
		return nil, model.Violationf(model.CodePortFieldInvalid, "node.adapter_kind",
			"zcode 适配器只接受 adapter_kind=zcode，收到 %q", cfg.Adapter)
	}
	a := &Adapter{
		cfg: cfg, runner: execRunner{}, timeout: DefaultProbeTimeout,
		clock: func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// LastConfig 返回最近一次探测的配置视图（只读副本）。
func (a *Adapter) LastConfig() (ConfigView, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		return ConfigView{}, false
	}
	return a.last.view, true
}
