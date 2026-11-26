package zcode

import (
	"context"

	"github.com/kiwh77/maw/internal/engine/model"
)

// BuildLaunchSpec 仅在 managed.launch observed 时生成；缺模型不改配置、不替用户选模型。
func (a *Adapter) BuildLaunchSpec(ctx context.Context, spec model.RunSpec) (model.LaunchSpec, error) {
	_ = ctx
	if err := model.Err(spec.Validate()); err != nil {
		return model.LaunchSpec{}, err
	}
	a.mu.Lock()
	last := a.last
	a.mu.Unlock()
	if last == nil {
		return model.LaunchSpec{}, model.PortFailure(model.PortErrCapabilityMissing, "尚未 Probe")
	}
	if last.view.ConfigMutated {
		return model.LaunchSpec{}, model.PortFailure(model.PortErrInvalidSpec, "配置被改动：适配器禁止改用户配置")
	}
	if !last.view.ModelReady {
		return model.LaunchSpec{}, model.PortFailure(model.PortErrCapabilityMissing,
			"缺模型：请在受控 profile_ref 中显式给出 model=，适配器不会自动改用户配置")
	}
	if last.caps[capManagedLaunch] != model.CapabilityObserved {
		return model.LaunchSpec{}, model.PortFailure(model.PortErrCapabilityMissing,
			"受管启动未验证或版本不匹配")
	}
	if last.caps[capModelInvoke] == model.CapabilityObserved {
		// 防御：本卡矩阵不应把 model.invoke 标 observed
		return model.LaunchSpec{}, model.PortFailure(model.PortErrCapabilityMissing,
			"模型调用能力未在本卡验证，拒绝开放")
	}
	_, modelID := ParseProfile(a.cfg.ProfileRef)
	argv := []string{a.cfg.EntryPath, "--print", spec.Input, "--model", modelID, "--output-format", "json"}
	launch := model.LaunchSpec{
		Executable:   a.cfg.EntryPath,
		Argv:         argv,
		Cwd:          spec.Workspace.Path,
		StdinMode:    model.StdinNone,
		EnvAllowlist: []string{"HOME", "PATH", "LANG", "TMPDIR"},
		ExpectedProtocol: model.Protocol{Format: "json", Version: "1"},
	}
	if vs := launch.Validate(); len(vs) > 0 {
		return model.LaunchSpec{}, model.PortFailuref(model.PortErrInvalidSpec, "%v", model.Err(vs))
	}
	return launch, nil
}
