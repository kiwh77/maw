package trae

import (
	"context"

	"github.com/kiwh77/maw/internal/engine/model"
)

// BuildLaunchSpec 仅在独立 CLI 且受管能力 observed 时生成 argv；IDE chat 一律拒绝。
func (a *Adapter) BuildLaunchSpec(ctx context.Context, spec model.RunSpec) (model.LaunchSpec, error) {
	_ = ctx
	if err := model.Err(spec.Validate()); err != nil {
		return model.LaunchSpec{}, err
	}
	a.mu.Lock()
	last := a.last
	a.mu.Unlock()
	if last == nil {
		return model.LaunchSpec{}, model.PortFailure(model.PortErrCapabilityMissing,
			"尚未 Probe：不能在未知能力上启动")
	}
	if last.class != ClassCLI || !allowsManaged(last.caps) {
		return model.LaunchSpec{}, model.PortFailure(model.PortErrCapabilityMissing,
			"当前入口是 IDE chat 或受管能力未验证：保持 manual，不启动后台 CLI、不宣称可回收结果")
	}
	if last.version != MeasuredCLIVersion {
		return model.LaunchSpec{}, model.PortFailuref(model.PortErrVersionUnsupported,
			"CLI 版本 %s 不在证据矩阵（需要 %s）", last.version, MeasuredCLIVersion)
	}
	if !last.authOK {
		return model.LaunchSpec{}, model.PortFailure(model.PortErrAuthRequired,
			"CLI 认证未就绪：请人工登录该独立 CLI 后重新 Probe（不继承 IDE 登录态）")
	}

	argv := []string{a.cfg.EntryPath, "--print", spec.Input, "--output-format", "json"}
	launch := model.LaunchSpec{
		Executable:   a.cfg.EntryPath,
		Argv:         argv,
		Cwd:          spec.Workspace.Path,
		StdinMode:    model.StdinNone,
		EnvAllowlist: []string{"HOME", "PATH", "LANG", "TMPDIR"},
		ExpectedProtocol: model.Protocol{
			Format:  "json",
			Version: "1",
		},
	}
	if vs := launch.Validate(); len(vs) > 0 {
		return model.LaunchSpec{}, model.PortFailuref(model.PortErrInvalidSpec,
			"生成的启动规格不合契约：%v", model.Err(vs))
	}
	return launch, nil
}
