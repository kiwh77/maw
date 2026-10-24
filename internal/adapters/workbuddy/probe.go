package workbuddy

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// versionLine 只接受 x.y.z。实测两包的 `--version` 都是单行 `2.161.0`（S01，8 字节）；
// 出现别的形状就按 version_unparsed 报，不猜、不截断。
var versionLine = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// helpFlagRE 从实测帮助清单里抠出参数名，用于「这个参数在不在」的存在性校验。
var helpFlagRE = regexp.MustCompile(`--[A-Za-z0-9][A-Za-z0-9-]*`)

// Probe 实现 ports.Probe：只读探测入口、发行包、版本与能力。
//
// 副作用边界（契约 §2.3）：只跑 `--version`／`--help`，不登录、不发模型任务、不改配置。
// 环境只传白名单（含本包登记的启动条件），工作目录用临时目录。
//
// 判定顺序是有意的：入口 → 运行时形态 → 发行包登记 → 启动条件 → 版本 → 能力。
// 前四步任一不成立，后面的版本号即使拿到了也不代表「这个包能受管」，
// 所以在拿到版本之前就把结论说清楚，而不是先报一个 ready 再解释。
func (a *Adapter) Probe(ctx context.Context, cfg model.NodeConfig) (model.ProbeResult, error) {
	if err := model.Err(checkConfig(cfg)); err != nil {
		return model.ProbeResult{}, err
	}
	if fingerprint(cfg) != fingerprint(a.cfg) {
		return model.ProbeResult{}, model.Violation(model.CodePortFieldInvalid, "probe.node",
			"探测配置与适配器绑定的节点不一致：Launch 端口拿不到节点配置，只能沿用构造时绑定的入口，混用会把两包的探测结果串在一起")
	}
	if err := checkPlatform(cfg.Platform); err != nil {
		return model.ProbeResult{}, err
	}

	ent, err := resolveEntry(cfg.EntryPath, cfg.RuntimePath)
	if err != nil {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrEntryMissing, "入口不可用：%v", err)
	}
	if err := checkRuntimeShape(ent); err != nil {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrEntryUnavailable, "%v", err)
	}

	dist, err := Detect(ent.Resolved)
	if err != nil {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrEntryUnavailable,
			"%v；只对登记过且有证据的随包入口开放受管能力", err)
	}
	if missing := dist.MissingEnv(os.LookupEnv); len(missing) > 0 {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrCapabilityMissing,
			"%s %s 的随包入口缺少启动条件：%s。这些变量由 App 自己的启动脚本提供，直接 exec 随包 CLI 时不会自动带上；"+
				"请在启动 maw 的进程里导出它们后重新探测（maw 不改宿主环境、不代改 App）",
			dist.AppName, dist.AppVersion, strings.Join(missing, "、"))
	}

	version, err := a.probeVersion(ctx, ent, dist)
	if err != nil {
		return model.ProbeResult{}, err
	}

	helpProbed, helpFlags := a.probeHelp(ctx, ent, dist)
	caps := capabilitySet(dist, version, helpProbed)

	res := model.ProbeResult{
		EntryState:   model.EntryReady,
		Version:      string(version),
		Readiness:    probeReadiness(cfg),
		Capabilities: caps,
		EvidenceRef:  dist.ManifestRef(),
	}
	if vs := res.Validate(); len(vs) > 0 {
		// 走到这里说明本包自己产出了不合契约的结果，属于实现缺陷，不冒充探测结论。
		return model.ProbeResult{}, model.Err(vs)
	}

	a.mu.Lock()
	a.last = &measurement{
		entry:     ent,
		dist:      dist,
		version:   version,
		helpFlags: helpFlags,
		caps:      capabilityIndex(caps),
		at:        a.now(),
	}
	a.mu.Unlock()

	return res, nil
}

// probeReadiness 是四维就绪的初始观测。
//
// entry 已确实跑通 --version，所以是 ready；认证／连接／容量都没有只读证据，保持 unknown。
// 特别注意认证维度：**不要**因为证据里出现过「未登录」就把这里写成 needs-login。
// 那只说明过去某一次调用没登录，不说明现在这个入口的登录态；把它写死会变成
// 「用一次历史观测代替当前事实」。真正的判据只有一次真实运行的结果（见 Interpret）。
func probeReadiness(cfg model.NodeConfig) model.Readiness {
	capacity := model.ReadyUnknown
	if !cfg.AcceptNew {
		capacity = model.ReadyDisabled
	}
	return model.Readiness{
		Entry:      model.EntryReady,
		Auth:       model.ReadyUnknown,
		Connection: model.ReadyUnknown,
		Capacity:   capacity,
	}
}

// probeVersion 取版本号，失败时按版本无法解析／入口不可用／超时分别报错。
func (a *Adapter) probeVersion(ctx context.Context, ent entry, dist Distribution) (model.SemVer, error) {
	res, err := a.invoke(ctx, ent, dist, []string{"--version"})
	if err != nil {
		if isTimeout(err) {
			return "", model.PortFailuref(model.PortErrProbeTimeout,
				"--version 在 %s 内没有返回：%v", a.timeout, err)
		}
		return "", model.PortFailuref(model.PortErrEntryMissing, "无法执行入口：%v", err)
	}
	if res.Code != 0 {
		return "", model.PortFailuref(model.PortErrVersionUnparsed,
			"--version 退出码 %d，stderr：%q", res.Code, firstLine(res.Stderr))
	}
	line := strings.TrimSpace(strings.TrimPrefix(firstLine(res.Stdout), "v"))
	if !versionLine.MatchString(line) {
		return "", model.PortFailuref(model.PortErrVersionUnparsed,
			"--version 输出不是 x.y.z：%q", firstLine(res.Stdout))
	}
	return model.SemVer(line), nil
}

// probeHelp 取参数清单。失败不致命：帮助探测没跑成，依赖帮助清单的能力全部保持 unobserved，
// BuildLaunchSpec 会因此拒绝启动——不确认参数存在就不拼参数。
func (a *Adapter) probeHelp(ctx context.Context, ent entry, dist Distribution) (bool, map[string]bool) {
	if !a.help {
		return false, nil
	}
	res, err := a.invoke(ctx, ent, dist, []string{"--help"})
	if err != nil || res.Code != 0 || len(res.Stdout) == 0 {
		return false, nil
	}
	return true, parseHelpFlags(res.Stdout)
}

// parseHelpFlags 抽出帮助清单里出现过的参数名。
//
// 只做存在性判断，不解析取值枚举的语义：枚举随包变化（两包 --model 就不同），
// 硬编码会变成「记忆驱动的启动规格」。取值枚举单独登记在 Distribution.ModelIDs。
func parseHelpFlags(help []byte) map[string]bool {
	matches := helpFlagRE.FindAll(help, -1)
	out := make(map[string]bool, len(matches))
	for _, m := range matches {
		out[string(m)] = true
	}
	return out
}

// invoke 统一包住一次只读调用：超时、白名单环境、临时工作目录。
func (a *Adapter) invoke(ctx context.Context, ent entry, dist Distribution, args []string) (commandResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	exe, argv := commandFor(ent, args)
	return a.runner.Run(runCtx, exe, argv, probeEnv(dist), probeWorkDir())
}

// isTimeout 报告错误是否来自超时或取消；两者都可重试，按 probe_timeout 上报。
func isTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
