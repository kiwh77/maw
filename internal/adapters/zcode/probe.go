package zcode

import (
	"context"
	"regexp"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

var versionLine = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Probe 只读探测运行时版本，并分开报告 provider／模型就绪；不改用户配置。
func (a *Adapter) Probe(ctx context.Context, cfg model.NodeConfig) (model.ProbeResult, error) {
	if err := model.Err(cfg.Validate()); err != nil {
		return model.ProbeResult{}, err
	}
	if cfg.Adapter != model.AdapterZCode {
		return model.ProbeResult{}, model.Violation(model.CodePortFieldInvalid, "probe.node", "adapter_kind 必须是 zcode")
	}
	entry, err := ResolveEntry(cfg)
	if err != nil {
		view := InspectConfig(cfg, false)
		_ = view
		return model.ProbeResult{}, err
	}

	pctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	stdout, stderr, code, err := a.runner.Run(pctx, entry, "--version")
	if err != nil {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrProbeTimeout, "version 探测失败：%v", err)
	}
	verStr := strings.TrimSpace(stdout)
	runtimeOK := code == 0 && versionLine.MatchString(verStr)
	if !runtimeOK {
		return model.ProbeResult{}, model.PortFailuref(model.PortErrVersionUnparsed,
			"无法解析 --version（exit=%d stderr=%q）", code, strings.TrimSpace(stderr))
	}
	version := model.SemVer(verStr)
	view := InspectConfig(cfg, true)
	caps := capabilitySet(version, view.RuntimeReady, view.ProviderReady, view.ModelReady)

	auth := model.ReadyUnknown
	if view.ModelReady && view.ProviderReady {
		auth = model.ReadyOK
	} else if !view.ModelReady {
		auth = model.ReadyNeedsModel
	}

	entryState := model.EntryReady
	if version != MeasuredCLIVersion {
		entryState = model.EntryIncompatible
		// 版本不符时降级受管
		for i := range caps {
			if caps[i].Name == capManagedLaunch && caps[i].State == model.CapabilityObserved {
				caps[i].State = model.CapabilityUnobserved
			}
		}
	}

	res := model.ProbeResult{
		EntryState: entryState,
		Version:    string(version),
		Readiness: model.Readiness{
			Entry: entryState, Auth: auth,
			Connection: model.ReadyUnknown, Capacity: model.ReadyAvailable,
		},
		Capabilities: caps,
		EvidenceRef:  EvidenceRoot + "/capability-matrix.json",
	}
	if vs := res.Validate(); len(vs) > 0 {
		return model.ProbeResult{}, model.Err(vs)
	}
	a.mu.Lock()
	a.last = &measurement{version: version, view: view, caps: capIndex(caps), at: a.clock()}
	a.mu.Unlock()
	return res, nil
}
