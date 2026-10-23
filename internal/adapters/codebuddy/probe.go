package codebuddy

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// versionLine 只接受 x.y.z。证据里的 `--version` 输出是单行 `2.173.0`（S01，8 字节）；
// 出现别的形状就按 version_unparsed 报，不猜、不截断。
var versionLine = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// helpFlagRE 从实测帮助清单里抠出参数名，用于「这个参数在不在」的存在性校验。
var helpFlagRE = regexp.MustCompile(`--[A-Za-z0-9][A-Za-z0-9-]*`)

// Probe 实现 ports.Probe：只读探测入口、版本与能力。
//
// 副作用边界（契约 §2.3）：只跑 `--version`／`--help`，不登录、不发模型任务、不改配置。
// 环境只传 EnvAllowlist，工作目录用临时目录。
//
// 认证与模型就绪一律报 unknown：只读探测判不出来（证据 C14 的唯一稳定信号在 result.errors 里，
// 那需要一次真实回合）。这里宁可让节点显示「认证未知」，也不报一个没测过的 ready。
func (a *Adapter) Probe(ctx context.Context, cfg model.NodeConfig) (model.ProbeResult, error) {
	if err := model.Err(checkConfig(cfg)); err != nil {
		return model.ProbeResult{}, err
	}
	if fingerprint(cfg) != fingerprint(a.cfg) {
		return model.ProbeResult{}, model.Violation(model.CodePortFieldInvalid, "probe.node",
			"探测配置与适配器绑定的节点不一致：Launch 端口拿不到节点配置，只能沿用构造时绑定的入口，混用会让启动规格指向另一台机器的入口")
	}
	if err := checkPlatform(cfg.Platform); err != nil {
		return model.ProbeResult{}, err
	}

	ent, err := resolveEntry(cfg.EntryPath, cfg.RuntimePath)
	if err != nil {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrEntryMissing, "入口不可用：%v", err)
	}

	version, err := a.probeVersion(ctx, ent)
	if err != nil {
		return model.ProbeResult{}, err
	}

	helpProbed, helpFlags := a.probeHelp(ctx, ent)
	caps := capabilitySet(version, helpProbed)

	res := model.ProbeResult{
		EntryState:   model.EntryReady,
		Version:      string(version),
		Readiness:    probeReadiness(cfg),
		Capabilities: caps,
		EvidenceRef:  EvidenceRoot,
	}
	if vs := res.Validate(); len(vs) > 0 {
		// 走到这里说明本包自己产出了不合契约的结果，属于实现缺陷，不冒充探测结论。
		return model.ProbeResult{}, model.Err(vs)
	}

	a.mu.Lock()
	a.last = &measurement{
		entry:     ent,
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
// entry 已确实跑通 --version，所以是 ready；认证／连接／容量都还没有证据，保持 unknown。
// 容量维度只有一条能直接下结论的配置事实：节点声明不收新任务 → disabled。
// 「还有几个空槽」属于节点注册表的账，适配器不越界去猜。
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
func (a *Adapter) probeVersion(ctx context.Context, ent entry) (model.SemVer, error) {
	res, err := a.invoke(ctx, ent, []string{"--version"})
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

// probeHelp 取参数清单。失败不致命：帮助探测没跑成，只是 entry.help_probe 保持 unobserved，
// BuildLaunchSpec 会因此拒绝启动——不确认参数存在就不拼参数。
func (a *Adapter) probeHelp(ctx context.Context, ent entry) (bool, map[string]bool) {
	if !a.help {
		return false, nil
	}
	res, err := a.invoke(ctx, ent, []string{"--help"})
	if err != nil || res.Code != 0 || len(res.Stdout) == 0 {
		return false, nil
	}
	return true, parseHelpFlags(res.Stdout)
}

// parseHelpFlags 抽出帮助清单里出现过的参数名。
//
// 只做存在性判断，不解析取值枚举：枚举随版本变化，硬编码会变成「记忆驱动的启动规格」。
func parseHelpFlags(help []byte) map[string]bool {
	matches := helpFlagRE.FindAll(help, -1)
	out := make(map[string]bool, len(matches))
	for _, m := range matches {
		out[string(m)] = true
	}
	return out
}

// invoke 统一包住一次只读调用：超时、白名单环境、临时工作目录。
func (a *Adapter) invoke(ctx context.Context, ent entry, args []string) (commandResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	exe, argv := commandFor(ent, args)
	return a.runner.Run(runCtx, exe, argv, probeEnv(), probeWorkDir())
}

// isTimeout 报告错误是否来自超时或取消；两者都可重试，按 probe_timeout 上报。
func isTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// firstLine 取第一行并去掉首尾空白，避免把多行输出整段写进错误文案。
func firstLine(b []byte) string {
	s := string(b)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// now 让测试可以固定时间源（当前只有 measurement.at 使用）。
func (a *Adapter) now() time.Time { return time.Now() }
