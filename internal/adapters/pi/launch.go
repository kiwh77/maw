package pi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// piRunConfig 是写进运行时 stdin 的一次执行配置（runtimes/pi 的约定）。
//
// 用 stdin 而不是 argv：任务正文与范围都是长文本，argv 契约拒绝换行；
// 而且 stdin 的 JSON 结构可以随契约演进，不必改节点登记里的 argv。
type piRunConfig struct {
	Mode       string   `json:"mode"`
	Cwd        string   `json:"cwd"`
	RunID      string   `json:"run_id"`
	Generation int      `json:"generation"`
	WriteScope []string `json:"write_scope"`
	ReadScope  []string `json:"read_scope"`
	AllowBash  bool     `json:"allow_bash"`
	Prompt     string   `json:"prompt"`
}

// ModelEnvAllowlist 是允许传给运行时的模型配置变量名。
//
// 凭据只走环境白名单：不进 argv、不进 stdin 之外的任何文件，也不写日志。
// 供应商自己的密钥变量名由节点配置追加（PI-103／PI-902 的运维口径）。
var ModelEnvAllowlist = []string{"PI_MODEL", "PI_PROVIDER", "PI_BASE_URL", "PI_API_KEY"}

// BuildLaunchSpec 实现 ports.Launch：把冻结的 RunSpec 翻成本运行时的启动规格。
//
// 不拼 shell、不写临时文件：argv 只有运行时入口一个元素，配置走 stdin。
func (a *Adapter) BuildLaunchSpec(ctx context.Context, spec model.RunSpec) (model.LaunchSpec, error) {
	if errs := spec.Validate(); len(errs) > 0 {
		return model.LaunchSpec{}, model.PortFailuref(model.PortErrInvalidSpec,
			"运行规格不合法：%v", model.Err(errs))
	}
	if err := ctx.Err(); err != nil {
		return model.LaunchSpec{}, err
	}
	cfg := piRunConfig{
		Mode:       "worker",
		Cwd:        spec.Workspace.Path,
		RunID:      string(spec.RunID),
		Generation: int(spec.Generation),
		WriteScope: append([]string{}, spec.WriteScope...),
		ReadScope:  append([]string{}, spec.ReadScope...),
		AllowBash:  strings.EqualFold(strings.TrimSpace(spec.Permission.Profile), "unattended"),
		Prompt:     spec.Input,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return model.LaunchSpec{}, model.PortFailuref(model.PortErrInvalidSpec,
			"无法编码运行配置：%v", err)
	}
	env := append([]string{}, EnvAllowlist...)
	env = append(env, ModelEnvAllowlist...)
	return model.LaunchSpec{
		Executable:       a.cfg.RuntimePath,
		Argv:             []string{a.cfg.EntryPath},
		Cwd:              spec.Workspace.Path,
		StdinMode:        model.StdinPipe,
		StdinData:        string(data),
		EnvAllowlist:     env,
		ExpectedProtocol: model.Protocol{Format: ProtocolFormat, Version: ProtocolVersion},
	}, nil
}
