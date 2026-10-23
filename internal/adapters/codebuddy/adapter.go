package codebuddy

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// 编译期确认本适配器就是那两个端口的实现。
// 契约 §1 允许 adapter → port／model 这条边，且只允许这条边的内部依赖。
var (
	_ ports.Probe  = (*Adapter)(nil)
	_ ports.Launch = (*Adapter)(nil)
)

// DefaultProbeTimeout 是单次 --version／--help 探测的上限。
// 证据：S01 实测 32ms、S02 实测 823ms；10s 只是防挂死，超时按 probe_timeout（可重试）返回。
const DefaultProbeTimeout = 10 * time.Second

// ProtocolVersion 是预期输出协议的版本号，取值口径是「记录形状」而不是 CLI 版本。
//
// 1 = VN-301 在 CLI 2.173.0 上实测到的 stream-json 记录形状（S03／S08）。
// 记录形状若随 CLI 变化，必须在这里加新版本号并由解析侧（VN-304）分支，
// 不能沿用旧号「大概兼容」。
const ProtocolVersion = "1"

// Adapter 是绑定到单个节点的 CodeBuddy 接入器，实现 ports.Probe 与 ports.Launch。
//
// 为什么在构造时绑定节点：Launch 端口的签名是 BuildLaunchSpec(RunSpec)，
// 里面既没有入口也没有版本；而「参数能不能拼」「协议版本写什么」恰恰取决于
// 这台机器上的这个入口。所以入口在构造时固定，版本与能力由 Probe 写入，
// BuildLaunchSpec 只消费最近一次探测结果——没探测过就不放行。
type Adapter struct {
	cfg     model.NodeConfig
	runner  commandRunner
	timeout time.Duration
	help    bool

	mu   sync.Mutex
	last *measurement
}

// measurement 是一次成功探测的事实快照；BuildLaunchSpec 的全部判据都来自这里。
type measurement struct {
	entry     entry
	version   model.SemVer
	helpFlags map[string]bool
	caps      map[string]model.CapabilityState
	at        time.Time
}

// Option 是构造选项。
type Option func(*Adapter)

// WithProbeTimeout 覆盖单次探测超时；非正值忽略。
func WithProbeTimeout(d time.Duration) Option {
	return func(a *Adapter) {
		if d > 0 {
			a.timeout = d
		}
	}
}

// WithHelpProbe 控制是否执行 `--help` 探测。
//
// 默认开启。关掉后 entry.help_probe 保持 unobserved，BuildLaunchSpec 会以
// capability_unsupported 拒绝启动——启动参数是否存在必须以实测帮助清单为准，
// 不能凭记忆或凭版本号推断。
func WithHelpProbe(on bool) Option {
	return func(a *Adapter) { a.help = on }
}

// New 创建绑定到 cfg 的接入器。
//
// 构造阶段不碰文件系统、不起进程：只检查配置能不能用（适配器种类、绝对入口、平台）。
// 真正的解析发生在 Probe 里，这样「入口不存在」是探测结论，而不是构造失败。
func New(cfg model.NodeConfig, opts ...Option) (*Adapter, error) {
	if err := model.Err(checkConfig(cfg)); err != nil {
		return nil, err
	}
	a := &Adapter{
		cfg:     cfg,
		runner:  execRunner{},
		timeout: DefaultProbeTimeout,
		help:    true,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// Config 返回绑定的节点配置副本（不含探测结果）。
func (a *Adapter) Config() model.NodeConfig { return a.cfg }

// checkConfig 只校验 Probe／Launch 真正依赖的字段。
//
// 不用 model.NodeConfig.Validate 全量校验：那会要求展示名、配置档引用、接入模式与槽数，
// 而探测一个入口并不需要它们。字段不合法的判据走 P100，不是端口错误码——
// 端口错误码描述的是「调用结果」，而这里是「配置本身不成立」。
func checkConfig(cfg model.NodeConfig) []*model.Error {
	var out []*model.Error
	if cfg.Adapter != model.AdapterCodeBuddy {
		out = append(out, model.Violationf(model.CodePortFieldInvalid, "node.adapter_kind",
			"本适配器只接 %q，收到 %q", model.AdapterCodeBuddy, cfg.Adapter))
	}
	if p := strings.TrimSpace(cfg.EntryPath); p == "" || !filepath.IsAbs(p) {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "node.entry_path",
			"必须是绝对路径：相对入口会随调用者工作目录漂移"))
	}
	if p := strings.TrimSpace(cfg.RuntimePath); p != "" && !filepath.IsAbs(p) {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "node.runtime_path",
			"必须是绝对路径"))
	}
	if strings.TrimSpace(cfg.Platform) == "" {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "node.platform",
			"必须写明平台：不写就无法判断这个入口能不能在本机跑"))
	}
	return out
}

// checkPlatform 拒绝在错误平台上探测／启动。
//
// 不做「差不多能跑」的推断：跨平台的入口形态、信号与运行时都不同，
// 在错平台上拿到的版本号同样是错的。
func checkPlatform(platform string) error {
	want := strings.ToLower(strings.TrimSpace(platform))
	if want == runtime.GOOS {
		return nil
	}
	return model.PortFailuref(model.PortErrPlatformUnsupported,
		"节点声明的平台是 %q，本机是 %q", platform, runtime.GOOS)
}

// fingerprint 是节点配置的稳定身份：节点标识、适配器、入口、运行时任一变化都算另一个节点。
//
// 平台不进来：它是一条「待校验的声明」而不是身份——声明错了要报 platform_unsupported，
// 而不是报「探测的不是同一个节点」。
func fingerprint(cfg model.NodeConfig) string {
	return strings.Join([]string{
		string(cfg.NodeID),
		string(cfg.Adapter),
		filepath.Clean(cfg.EntryPath),
		filepath.Clean(cfg.RuntimePath),
	}, "|")
}

// commandFor 把一次调用拼成「可执行文件 + argv」。
//
// 入口是带 shebang 的脚本（本机实测：`#!/usr/bin/env node`）时，直接 exec 脚本会让
// 宿主 PATH 决定用哪个运行时。为了让同一条启动规格在任何 PATH 下都指向同一个运行时，
// 这里把解释器固定为可执行文件，脚本路径作为 argv 的第一个元素。
func commandFor(ent entry, args []string) (string, []string) {
	if !ent.ViaRuntime {
		return ent.Resolved, args
	}
	argv := make([]string, 0, len(args)+1)
	argv = append(argv, ent.Resolved)
	argv = append(argv, args...)
	return ent.Runtime, argv
}
