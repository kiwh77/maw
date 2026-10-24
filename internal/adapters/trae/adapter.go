package trae

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

// DefaultProbeTimeout 是单次只读探测上限。
const DefaultProbeTimeout = 10 * time.Second

// Adapter 绑定单个 Trae 节点；IDE 与 CLI 分级后能力不共享。
type Adapter struct {
	cfg     model.NodeConfig
	runner  commandRunner
	timeout time.Duration
	clock   func() time.Time

	mu   sync.Mutex
	last *measurement
}

type measurement struct {
	class   EntryClass
	version model.SemVer
	caps    map[string]model.CapabilityState
	authOK  bool
	at      time.Time
}

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, exitCode int, err error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	stderr := ""
	if ee, ok := err.(*exec.ExitError); ok {
		stderr = string(ee.Stderr)
		return string(out), stderr, ee.ExitCode(), nil
	}
	if err != nil {
		return string(out), stderr, -1, err
	}
	return string(out), stderr, 0, nil
}

// Option 构造选项。
type Option func(*Adapter)

// WithRunner 注入命令执行（测试用）。
func WithRunner(r commandRunner) Option {
	return func(a *Adapter) { a.runner = r }
}

// WithProbeTimeout 覆盖探测超时。
func WithProbeTimeout(d time.Duration) Option {
	return func(a *Adapter) {
		if d > 0 {
			a.timeout = d
		}
	}
}

// New 构造适配器；cfg.Adapter 必须是 trae。
func New(cfg model.NodeConfig, opts ...Option) (*Adapter, error) {
	if err := model.Err(cfg.Validate()); err != nil {
		return nil, err
	}
	if cfg.Adapter != model.AdapterTrae {
		return nil, model.Violationf(model.CodePortFieldInvalid, "node.adapter_kind",
			"trae 适配器只接受 adapter_kind=trae，收到 %q", cfg.Adapter)
	}
	a := &Adapter{
		cfg:     cfg,
		runner:  execRunner{},
		timeout: DefaultProbeTimeout,
		clock:   func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// Class 返回最近一次探测的分级；未探测时按配置即时分类。
func (a *Adapter) Class() EntryClass {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last != nil {
		return a.last.class
	}
	return Classify(a.cfg)
}
