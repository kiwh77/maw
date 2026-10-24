package workbuddy

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// 编译期确认本适配器就是那四个端口的实现。
// 契约 §1 允许 adapter → port／model 这条边；本包不 import 另一个适配器（含 codebuddy），
// 「解析委托」落在协议语义层，不靠 Go 依赖。
var (
	_ ports.Probe     = (*Adapter)(nil)
	_ ports.Launch    = (*Adapter)(nil)
	_ ports.Decode    = (*Adapter)(nil)
	_ ports.Interpret = (*Adapter)(nil)
)

// DefaultProbeTimeout 是单次 --version／--help 探测的上限。
//
// 证据：S01 实测 65–70ms，S02 实测 1.2s（帮助清单要加载随包 bundle）。10s 只是防挂死，
// 超时按 probe_timeout（可重试）返回。
const DefaultProbeTimeout = 10 * time.Second

// OutputFormat 是启动时使用的 --output-format 取值。
//
// 取 "json"（单条结果）而不是 "stream-json"：json 是被真实调用过的取值，
// stream-json 在本机两款随包 CLI 上从未产出过一条记录（认证先行失败），
// 所以不按 stream-json 的语义开受管能力。见 capabilityMatrix 的 W06。
const OutputFormat = "json"

// ProtocolVersion 是预期记录形状的版本号（取值口径是「形状」而不是 CLI 版本）。
//
// 1 = 家族 v1 的单条 json 结果形状。注意这是**声明**而不是观测：
// 两款 WorkBuddy 从未产出过一条成功结果（认证缺失），所以 result.record_present
// 保持 unobserved，Decode／Interpret 的形状解释被这条能力挡住。
// 要放开，必须先采到样本、把矩阵里的那一行改成 observed，而不是在这里改常量。
const ProtocolVersion = "1"

// Adapter 是绑定到单个节点、单个发行包的 WorkBuddy 接入器。
//
// 发行包在 Probe 时由真实入口路径判定（Detect），之后进入 measurement：
// Launch／Decode／Interpret 只消费「这个入口、这个包」测到的事实。
// 两个包因此不可能共用一份探测结果——它们的入口路径不同，指纹就不同。
type Adapter struct {
	cfg     model.NodeConfig
	runner  commandRunner
	timeout time.Duration
	help    bool
	clock   func() time.Time

	mu   sync.Mutex
	last *measurement
}

// measurement 是一次成功探测的事实快照；Launch／Decode／Interpret 的判据都来自这里。
type measurement struct {
	entry     entry
	dist      Distribution
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
// 默认开启。关掉后 entry.help_probe 与所有依赖帮助清单的能力都保持 unobserved，
// BuildLaunchSpec 会因此拒绝启动——参数是否存在必须以实测帮助清单为准。
func WithHelpProbe(on bool) Option {
	return func(a *Adapter) { a.help = on }
}

// WithClock 替换接收时间来源（测试里固定时间）。
func WithClock(now func() time.Time) Option {
	return func(a *Adapter) {
		if now != nil {
			a.clock = now
		}
	}
}

// New 创建绑定到 cfg 的接入器。
//
// 构造阶段不碰文件系统、不起进程、也不查环境：只检查配置本身能不能用。
// 「入口不在登记范围内」「缺启动条件」都是探测结论，不是构造失败。
func New(cfg model.NodeConfig, opts ...Option) (*Adapter, error) {
	if err := model.Err(checkConfig(cfg)); err != nil {
		return nil, err
	}
	a := &Adapter{
		cfg:     cfg,
		runner:  execRunner{},
		timeout: DefaultProbeTimeout,
		help:    true,
		clock:   time.Now,
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
// 而探测一个入口并不需要它们。字段不合法的判据走 P100，不是端口错误码。
func checkConfig(cfg model.NodeConfig) []*model.Error {
	var out []*model.Error
	if cfg.Adapter != model.AdapterWorkBuddy {
		out = append(out, model.Violationf(model.CodePortFieldInvalid, "node.adapter_kind",
			"本适配器只接 %q，收到 %q：CodeBuddy 独立 CLI 请用 codebuddy 适配器，两者不互相代表",
			model.AdapterWorkBuddy, cfg.Adapter))
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
func checkPlatform(platform string) error {
	want := strings.ToLower(strings.TrimSpace(platform))
	if want == runtime.GOOS {
		return nil
	}
	return model.PortFailuref(model.PortErrPlatformUnsupported,
		"节点声明的平台是 %q，本机是 %q", platform, runtime.GOOS)
}

// fingerprint 是节点配置的稳定身份：节点标识、接入器、入口、运行时任一变化都算另一个节点。
//
// 发行包种类不进来：它由入口路径决定，入口变了指纹自然就变；
// 反过来若把它单列进来，就会出现「同一入口被声明成两个包」这种本不该存在的状态。
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
// 随包入口是带 `#!/usr/bin/env node` 的脚本，直接 exec 脚本会让宿主 PATH 决定用哪个运行时。
// 为了让同一条启动规格在任何 PATH 下都指向同一个运行时，这里把解释器固定为可执行文件，
// 脚本路径作为 argv 的第一个元素。
func commandFor(ent entry, args []string) (string, []string) {
	if !ent.ViaRuntime {
		return ent.Resolved, args
	}
	argv := make([]string, 0, len(args)+1)
	argv = append(argv, ent.Resolved)
	argv = append(argv, args...)
	return ent.Runtime, argv
}

// capabilityState 取最近一次探测里某项能力的状态。
//
// 没探测过就拿不到状态——这不是「按默认值放行」，而是「没有证据就不放行」：
// 返回 false 让调用方把它当成 capability_unsupported 处理。
func (a *Adapter) capabilityState(name string) (model.CapabilityState, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		return "", false
	}
	state, ok := a.last.caps[name]
	return state, ok
}

// now 是接收时间的唯一来源；测试用 WithClock 固定它。
func (a *Adapter) now() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now()
}

// clip 把长文本截短，避免错误文案把整段任务输入刷进日志。
func clip(s string) string {
	const max = 40
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// firstLine 取第一行并去掉首尾空白。
func firstLine(b []byte) string {
	s := string(b)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
