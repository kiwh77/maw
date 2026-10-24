package trae

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

var versionLine = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Probe 只读探测：IDE chat 不跑 CLI；CLI 只跑 --version／--help，不登录、不发模型任务。
func (a *Adapter) Probe(ctx context.Context, cfg model.NodeConfig) (model.ProbeResult, error) {
	if err := model.Err(cfg.Validate()); err != nil {
		return model.ProbeResult{}, err
	}
	if cfg.Adapter != model.AdapterTrae {
		return model.ProbeResult{}, model.Violation(model.CodePortFieldInvalid, "probe.node", "adapter_kind 必须是 trae")
	}
	class := Classify(cfg)

	switch class {
	case ClassIDEChat:
		caps := capabilitySet(ClassIDEChat, "0.0.0", false, false, false)
		res := model.ProbeResult{
			EntryState: model.EntryReady,
			Version:    "ide-chat",
			Readiness: model.Readiness{
				Entry: model.EntryReady, Auth: model.ReadyUnknown,
				Connection: model.ReadyUnknown, Capacity: model.ReadyAvailable,
			},
			Capabilities: caps,
			EvidenceRef:  EvidenceRoot + "/capability-matrix.json#ide",
		}
		if vs := res.Validate(); len(vs) > 0 {
			return model.ProbeResult{}, model.Err(vs)
		}
		a.mu.Lock()
		a.last = &measurement{class: ClassIDEChat, version: "0.0.0", caps: capIndex(caps), authOK: false, at: a.clock()}
		a.mu.Unlock()
		return res, nil

	case ClassCLI:
		return a.probeCLI(ctx, cfg)

	default:
		caps := capabilitySet(ClassUnknown, "0.0.0", false, false, false)
		res := model.ProbeResult{
			EntryState: model.EntryUnknown,
			Version:    "unknown",
			Readiness: model.Readiness{
				Entry: model.EntryUnknown, Auth: model.ReadyUnknown,
				Connection: model.ReadyUnknown, Capacity: model.ReadyDisabled,
			},
			Capabilities: caps,
			EvidenceRef:  EvidenceRoot + "/capability-matrix.json#unknown",
		}
		return res, nil
	}
}

func (a *Adapter) probeCLI(ctx context.Context, cfg model.NodeConfig) (model.ProbeResult, error) {
	if err := checkAbsExecutable(cfg.EntryPath); err != nil {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrEntryMissing, "CLI 入口不可用：%v", err)
	}
	pctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	stdout, stderr, code, err := a.runner.Run(pctx, cfg.EntryPath, "--version")
	if err != nil {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrProbeTimeout, "version 探测失败：%v", err)
	}
	verStr := strings.TrimSpace(stdout)
	if code != 0 || !versionLine.MatchString(verStr) {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrEntryUnavailable,
			"CLI --version 无法解析（exit=%d stderr=%q）", code, trim(stderr, 200))
	}
	version := model.SemVer(verStr)
	versionOK := version == MeasuredCLIVersion

	helpOK := false
	hctx, hcancel := context.WithTimeout(ctx, a.timeout)
	defer hcancel()
	hout, _, hcode, herr := a.runner.Run(hctx, cfg.EntryPath, "--help")
	if herr == nil && hcode == 0 && strings.Contains(hout, "--print") {
		helpOK = true
	}

	// Probe 不登录：auth 维度保持 unknown／needs-login 由显式标记（配置 profile 含 authed）决定
	authOK := strings.Contains(cfg.ProfileRef, "authed")
	authState := model.ReadyUnknown
	if strings.Contains(cfg.ProfileRef, "needs-login") {
		authState = model.ReadyNeedsLogin
		authOK = false
	} else if authOK {
		authState = model.ReadyOK
	}

	caps := capabilitySet(ClassCLI, version, versionOK, helpOK, authOK)
	entryState := model.EntryReady
	if !versionOK {
		entryState = model.EntryIncompatible
	}
	res := model.ProbeResult{
		EntryState: entryState,
		Version:    string(version),
		Readiness: model.Readiness{
			Entry: entryState, Auth: authState,
			Connection: model.ReadyUnknown,
			Capacity:   model.ReadyAvailable,
		},
		Capabilities: caps,
		EvidenceRef:  EvidenceRoot + "/capability-matrix.json#cli",
	}
	if vs := res.Validate(); len(vs) > 0 {
		return model.ProbeResult{}, model.Err(vs)
	}
	a.mu.Lock()
	a.last = &measurement{
		class: ClassCLI, version: version, caps: capIndex(caps), authOK: authOK, at: a.clock(),
	}
	a.mu.Unlock()
	return res, nil
}

func checkAbsExecutable(path string) error {
	if !filepath.IsAbs(path) {
		return os.ErrInvalid
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() || st.Mode().Perm()&0o111 == 0 {
		return os.ErrPermission
	}
	return nil
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ManualOnly 报告当前绑定是否只能人工接入（IDE chat 或未知）。
func (a *Adapter) ManualOnly() bool {
	switch a.Class() {
	case ClassIDEChat, ClassUnknown:
		return true
	default:
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.last == nil {
			return true
		}
		return !allowsManaged(a.last.caps)
	}
}
