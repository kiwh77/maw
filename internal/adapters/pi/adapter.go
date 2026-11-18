// Package pi 是 Pi 运行时（runtimes/pi）的接入器实现：
// Probe／Launch／Decode／Interpret 四个端口，协议 maw.pi.execution/v1。
//
// 与 CodeBuddy 的差别只有「协议长什么样」：Pi 是我们自己的运行时，输出
// 一条一行的事件信封（NDJSON），因此解码是确定的，不需要猜厂商字段。
//
// 契约：docs/contracts/vnext/execution/pi-execution-contract-v1.md
package pi

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// 协议标识（契约 §2）：与 CodeBuddy 的 stream-json/1 不同名，两条来源不得互相解释。
const (
	ProtocolFormat  = "pi-stream-json"
	ProtocolVersion = "1"
)

// DefaultProbeTimeout 是单次 --probe 的上限；超时按 probe_timeout 处理，可重试。
const DefaultProbeTimeout = 10 * time.Second

// EnvAllowlist 是允许传给 Pi 运行时的宿主环境变量名。
//
// 模型凭据由宿主按节点配置追加（见 Launch），但工具子进程的环境由运行时
// 自己剔除密钥前缀（契约 §6）；这里只放执行必需的普通变量。
var EnvAllowlist = []string{"HOME", "LANG", "PATH", "SHELL", "TMPDIR"}

// 编译期确认本适配器实现了四个端口。
var (
	_ ports.Probe     = (*Adapter)(nil)
	_ ports.Launch    = (*Adapter)(nil)
	_ ports.Decode    = (*Adapter)(nil)
	_ ports.Interpret = (*Adapter)(nil)
)

// Adapter 绑定到单个 Pi 节点：EntryPath 是运行时入口脚本，RuntimePath 是 node 可执行文件。
type Adapter struct {
	cfg     model.NodeConfig
	timeout time.Duration
	clock   func() time.Time
	runner  commandRunner
}

// Option 是构造选项。
type Option func(*Adapter)

// WithProbeTimeout 覆盖探测超时；非正值忽略。
func WithProbeTimeout(d time.Duration) Option {
	return func(a *Adapter) {
		if d > 0 {
			a.timeout = d
		}
	}
}

// WithClock 注入时间源（测试用）。
func WithClock(fn func() time.Time) Option {
	return func(a *Adapter) {
		if fn != nil {
			a.clock = fn
		}
	}
}

// WithRunner 注入只读探测执行器（测试用）。
func WithRunner(r commandRunner) Option {
	return func(a *Adapter) {
		if r != nil {
			a.runner = r
		}
	}
}

// New 构造 Pi 适配器。
//
// RuntimePath 留空时按 "node" 解析（走宿主 PATH）；EntryPath 必须是绝对路径：
// 相对路径会让「同一个节点」随调用者目录漂移。
func New(cfg model.NodeConfig, opts ...Option) (*Adapter, error) {
	if cfg.Adapter != model.AdapterPi {
		return nil, model.PortFailuref(model.PortErrInvalidSpec,
			"本适配器只接 %q，收到 %q", model.AdapterPi, cfg.Adapter)
	}
	entry := strings.TrimSpace(cfg.EntryPath)
	if !filepath.IsAbs(entry) {
		return nil, model.PortFailuref(model.PortErrInvalidSpec,
			"entry_path 必须是绝对路径，收到 %q", cfg.EntryPath)
	}
	runtimePath := strings.TrimSpace(cfg.RuntimePath)
	if runtimePath == "" {
		runtimePath = "node"
	}
	if runtimePath != "node" && !filepath.IsAbs(runtimePath) {
		return nil, model.PortFailuref(model.PortErrInvalidSpec,
			"runtime_path 必须是绝对路径或 \"node\"，收到 %q", cfg.RuntimePath)
	}
	a := &Adapter{
		cfg:     cfg,
		timeout: DefaultProbeTimeout,
		clock:   func() time.Time { return time.Now().UTC() },
		runner:  execRunner{},
	}
	if cfg.Timeout > 0 {
		a.timeout = cfg.Timeout
	}
	for _, opt := range opts {
		opt(a)
	}
	a.cfg.EntryPath = entry
	a.cfg.RuntimePath = runtimePath
	return a, nil
}

// now 返回注入的时钟，统一 UTC。
func (a *Adapter) now() time.Time { return a.clock().UTC() }

// checkProtocol 只接受本运行时实测的协议：pi-stream-json 的单条记录，版本 1。
func checkProtocol(p model.Protocol) error {
	if strings.TrimSpace(p.Format) != ProtocolFormat {
		return model.PortFailuref(model.PortErrVersionUnsupported,
			"只支持 %s 单条记录，收到格式 %q", ProtocolFormat, p.Format)
	}
	if strings.TrimSpace(p.Version) != ProtocolVersion {
		return model.PortFailuref(model.PortErrVersionUnsupported,
			"协议版本 %q 未观测：只认版本 %s", p.Version, ProtocolVersion)
	}
	return nil
}

// Probe 实现 ports.Probe：执行 `<runtime> <entry> --probe` 读结构化自述。
//
// 只读探测，允许在本包内起进程；真实任务派发不走这里。
func (a *Adapter) Probe(ctx context.Context, cfg model.NodeConfig) (model.ProbeResult, error) {
	entry := strings.TrimSpace(cfg.EntryPath)
	if entry == "" {
		entry = a.cfg.EntryPath
	}
	runtimePath := strings.TrimSpace(cfg.RuntimePath)
	if runtimePath == "" {
		runtimePath = a.cfg.RuntimePath
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	res, err := a.runner.Run(ctx, runtimePath, []string{entry, "--probe"}, probeEnv(), "")
	if err != nil {
		if ctx.Err() != nil {
			return model.ProbeResult{}, model.PortFailuref(model.PortErrProbeTimeout,
				"探测超时（%s）：运行时没有在预算内回答 --probe", a.timeout)
		}
		return model.ProbeResult{}, model.PortFailuref(model.PortErrEntryMissing,
			"无法执行 %s %s --probe：%v", runtimePath, entry, err)
	}
	if res.Code != 0 {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrEntryMissing,
			"运行时 --probe 退出码 %d：%s", res.Code, strings.TrimSpace(string(res.Stderr)))
	}
	report, err := parseProbeReport(res.Stdout)
	if err != nil {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrVersionUnparsed,
			"--probe 输出不是可判定的 JSON：%v", err)
	}
	if report.Protocol.Format != ProtocolFormat || report.Protocol.Version != ProtocolVersion {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrVersionUnsupported,
			"运行时自报协议 %s/%s，期望 %s/%s",
			report.Protocol.Format, report.Protocol.Version, ProtocolFormat, ProtocolVersion)
	}
	out := model.ProbeResult{
		EntryState: model.EntryReady,
		Version:    strings.TrimSpace(report.SDK),
		Readiness: model.Readiness{
			Entry:      model.EntryReady,
			Auth:       model.ReadyOK,
			Connection: model.ReadyFresh,
			Capacity:   model.ReadyAvailable,
		},
		EvidenceRef: "docs/contracts/vnext/execution/pi-execution-contract-v1.md#2",
	}
	for _, name := range []string{"worker", "lead", "diagnose", "pi-stream-json"} {
		out.Capabilities = append(out.Capabilities, model.Capability{
			Name: name, State: model.CapabilityObserved,
			Version:     model.SemVer(report.SDK),
			EvidenceRef: out.EvidenceRef,
		})
	}
	if errs := out.Validate(); len(errs) > 0 {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrVersionUnparsed,
			"探测结果自校验失败：%v", model.Err(errs))
	}
	return out, nil
}

// probeEnv 按白名单从宿主环境取值。
func probeEnv() []string {
	return envFromNames(EnvAllowlist)
}
