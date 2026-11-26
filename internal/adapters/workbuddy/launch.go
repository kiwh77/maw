package workbuddy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// permissionModes 是 S02 实测帮助清单里 `--permission-mode` 的取值枚举（逐字抄，不增不减）。
var permissionModes = map[string]bool{
	"acceptEdits":       true,
	"bypassPermissions": true,
	"default":           true,
	"plan":              true,
	"dontAsk":           true,
	"auto":              true,
}

// BuildLaunchSpec 实现 ports.Launch：把冻结的 RunSpec 翻成 argv 数组。
//
// 四条硬约束：
//   - 必须已有成功探测：入口、发行包、版本与帮助清单都来自 measurement，不用配置里的路径现拼；
//   - 启动依赖的能力必须在该包的这个版本上 observed，否则 capability_unsupported；
//   - 只用实测帮助清单里出现过的参数，参数存在性不靠记忆；
//   - 只发 `--output-format json`，不发 stream-json（W06 unobserved）。
func (a *Adapter) BuildLaunchSpec(ctx context.Context, spec model.RunSpec) (model.LaunchSpec, error) {
	if vs := spec.Validate(); len(vs) > 0 {
		return model.LaunchSpec{}, model.PortFailuref(model.PortErrInvalidSpec,
			"运行规格不合法：%v", model.Err(vs))
	}

	m, err := a.measurement()
	if err != nil {
		return model.LaunchSpec{}, err
	}
	if err := checkWorkspace(spec.Workspace.Path); err != nil {
		return model.LaunchSpec{}, model.PortFailuref(model.PortErrInvalidSpec, "%v", err)
	}
	// 启动时也要确认启动条件还在：探测之后宿主环境可能已经变了，
	// 那时的规格就不再指向被证据覆盖的那份实现。
	if missing := m.dist.MissingEnv(os.LookupEnv); len(missing) > 0 {
		return model.LaunchSpec{}, model.PortFailuref(model.PortErrCapabilityMissing,
			"启动条件已失效：%s；请在导出这些变量后重新探测，再按新的探测结果启动",
			strings.Join(missing, "、"))
	}

	args, stdin, err := buildArgs(spec, m)
	if err != nil {
		return model.LaunchSpec{}, err
	}

	exe, argv := commandFor(m.entry, args)

	launch := model.LaunchSpec{
		Executable:   exe,
		Argv:         argv,
		Cwd:          spec.Workspace.Path,
		StdinMode:    stdin,
		EnvAllowlist: envAllowlistFor(m.dist),
		ExpectedProtocol: model.Protocol{
			Format:  OutputFormat,
			Version: ProtocolVersion,
		},
	}
	if vs := launch.Validate(); len(vs) > 0 {
		// 自检兜底：生成物不合契约说明本包有缺陷，不把不合规的规格交出去。
		return model.LaunchSpec{}, model.PortFailuref(model.PortErrInvalidSpec,
			"生成的启动规格不合契约：%v", model.Err(vs))
	}
	return launch, nil
}

// buildArgs 是参数构造的唯一落点；顺序固定，便于按样本逐项对照。
//
// 顺序：`-p [输入] --output-format json [--no-session-persistence] [权限] [MCP]`。
// 输入永远是一个 argv 元素，不做任何转义或拼接。
func buildArgs(spec model.RunSpec, m *measurement) ([]string, model.StdinMode, error) {
	if m.helpFlags == nil {
		return nil, "", model.PortFailure(model.PortErrCapabilityMissing,
			"没有实测帮助清单：--help 探测未执行或失败，启动参数的存在性无法确认，拒绝拼参数")
	}
	if err := requireCaps(m, capHeadless, capOutputJSON); err != nil {
		return nil, "", err
	}
	// 能力状态说「这类行为在这个版本上成立」，帮助清单说「这个参数在这份实现里存在」。
	// 两道都要过：换了一份帮助清单（例如换了 bundle）时，能力行可能还来不及降级。
	if err := requireFlags(m, "--print", "--output-format"); err != nil {
		return nil, "", err
	}

	args := []string{"-p"}

	switch spec.InputMode {
	case model.InputArg:
		// 以 - 开头的输入会被 CLI 当成选项，适配器不插未经验证的分隔符，改走 stdin。
		if strings.HasPrefix(spec.Input, "-") {
			return nil, "", model.PortFailuref(model.PortErrInvalidSpec,
				"输入以 - 开头（%q）：随包 CLI 会把它当选项解析，而 `--` 分隔符尚未实测；请改用 stdin 输入", clip(spec.Input))
		}
		args = append(args, spec.Input)
	case model.InputStdin:
		// 提示词走管道，argv 里不放输入。
	case model.InputFile:
		return nil, "", model.PortFailure(model.PortErrCapabilityMissing,
			"文件输入无法表达：LaunchSpec 没有 stdin 文件路径字段，不接受「读文件代替 stdin」的隐式行为")
	default:
		return nil, "", model.PortFailuref(model.PortErrInvalidSpec, "未知输入模式 %q", spec.InputMode)
	}

	args = append(args, "--output-format", OutputFormat)

	// 不持久化会话：最小调用证据里一直带这个参数，它会改变写入行为（会话不落盘），
	// 属于「跑起来的样子」的一部分。帮助清单里没有就不加——不凭记忆补参数。
	if hasFlag(m.helpFlags, "--no-session-persistence") {
		args = append(args, "--no-session-persistence")
	}

	perm, err := permissionArgs(spec.Permission, m)
	if err != nil {
		return nil, "", err
	}
	args = append(args, perm...)

	mcp, err := mcpArgs(spec.Session.MCPServers, m)
	if err != nil {
		return nil, "", err
	}
	args = append(args, mcp...)

	if id := strings.TrimSpace(spec.Session.ResumeSessionID); id != "" {
		return nil, "", model.PortFailuref(model.PortErrCapabilityMissing,
			"拒绝恢复会话 %q：session.resume 的能力状态是 %s（W19），恢复语义与会话存在性都未验证；按未验证语义启动会把新会话当成续跑",
			id, m.caps[capResume])
	}

	stdin := model.StdinNone
	if spec.InputMode == model.InputStdin {
		stdin = model.StdinPipe
	}
	return args, stdin, nil
}

// permissionArgs 把权限档翻成 `--permission-mode`。
//
// 三条拒绝都是「承诺不了就不承诺」：
//   - allow_write=false：帮助清单里没有能证明「写被拒」的开关，权限拒绝路径也未观测；
//   - allow_network=false：没有网络开关，不能假装禁网；
//   - 非空 Sandbox：沙箱执行未观测，不接受静默改派执行环境。
func permissionArgs(p model.PermissionProfile, m *measurement) ([]string, error) {
	if !hasFlag(m.helpFlags, "--permission-mode") {
		return nil, model.PortFailure(model.PortErrCapabilityMissing,
			"实测帮助清单里没有 --permission-mode：权限档无法下传，拒绝启动")
	}
	if !p.AllowWrite {
		return nil, model.PortFailure(model.PortErrCapabilityMissing,
			"allow_write=false 无法保证：随包 CLI 没有可验证的「禁写」开关，拒绝假装已禁写")
	}
	if !p.AllowNetwork {
		return nil, model.PortFailure(model.PortErrCapabilityMissing,
			"allow_network=false 无法保证：帮助清单里没有网络开关，拒绝假装已禁网")
	}
	if s := strings.TrimSpace(p.Sandbox); s != "" {
		return nil, model.PortFailuref(model.PortErrCapabilityMissing,
			"沙箱执行未观测（请求 %q）：不接受静默改派执行环境", s)
	}

	mode := strings.TrimSpace(p.Profile)
	if !permissionModes[mode] {
		return nil, model.PortFailuref(model.PortErrInvalidSpec,
			"未知权限档 %q：只能是帮助清单里的 acceptEdits／bypassPermissions／default／plan／dontAsk／auto", mode)
	}
	return []string{"--permission-mode", mode}, nil
}

// mcpArgs 处理 MCP 集合。
//
// 有配置时必须两个参数都在，且把集合钉死（只用 --mcp-config 给的服务器）。
// 没有配置时：帮助清单里有 --strict-mcp-config 就一并带上（多一层「忽略其他配置」的隔离），
// 没有就不加——不凭空创造一个自己没验证过的参数。
// 多份配置一律拒绝：合并语义未观测。
func mcpArgs(servers []string, m *measurement) ([]string, error) {
	switch len(servers) {
	case 0:
		if hasFlag(m.helpFlags, "--strict-mcp-config") {
			return []string{"--strict-mcp-config"}, nil
		}
		return nil, nil
	case 1:
		if !hasFlag(m.helpFlags, "--strict-mcp-config") || !hasFlag(m.helpFlags, "--mcp-config") {
			return nil, model.PortFailure(model.PortErrCapabilityMissing,
				"实测帮助清单里缺 --strict-mcp-config／--mcp-config：MCP 集合无法钉死，拒绝启动")
		}
		v, err := mcpConfigValue(servers[0])
		if err != nil {
			return nil, err
		}
		return []string{"--mcp-config", v, "--strict-mcp-config"}, nil
	default:
		return nil, model.PortFailuref(model.PortErrCapabilityMissing,
			"一次只接受一份 MCP 配置（收到 %d 份）：多份配置的合并语义未观测", len(servers))
	}
}

// mcpConfigValue 校验单份 MCP 配置：绝对路径的 JSON 文件，或内联 JSON 对象。
func mcpConfigValue(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", model.PortFailure(model.PortErrInvalidSpec, "MCP 配置项为空")
	}
	if strings.HasPrefix(v, "{") {
		if !json.Valid([]byte(v)) {
			return "", model.PortFailure(model.PortErrInvalidSpec, "内联 MCP 配置不是合法 JSON")
		}
		return v, nil
	}
	if !filepath.IsAbs(v) {
		return "", model.PortFailuref(model.PortErrInvalidSpec,
			"MCP 配置只能给绝对路径或内联 JSON，收到 %q：相对路径会随调用者工作目录漂移", v)
	}
	info, err := os.Stat(v)
	if err != nil {
		return "", model.PortFailuref(model.PortErrInvalidSpec, "MCP 配置文件不可用：%v", err)
	}
	if info.IsDir() {
		return "", model.PortFailuref(model.PortErrInvalidSpec, "MCP 配置 %q 是目录", v)
	}
	return v, nil
}

// requireCaps 检查一组能力在该包的这个版本上是否 observed。
//
// 只认 observed：unobserved／unknown／unsupported 都不参与启动（契约 §5）。
func requireCaps(m *measurement, names ...string) error {
	missing := make([]string, 0, len(names))
	for _, n := range names {
		if m.caps[n] != model.CapabilityObserved {
			missing = append(missing, fmt.Sprintf("%s=%s", n, m.caps[n]))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return model.PortFailuref(model.PortErrCapabilityMissing,
		"%s %s（随包 CLI %s）上未观测到启动依赖的能力：%s；证据只对 %s 的该包成立，拒绝按未验证参数启动",
		m.dist.AppName, m.dist.AppVersion, m.dist.BundledCLI, strings.Join(missing, ", "), m.dist.SamplePrefix())
}

// requireFlags 检查一组参数是否真的出现在本次实测帮助清单里。
func requireFlags(m *measurement, flags ...string) error {
	missing := make([]string, 0, len(flags))
	for _, f := range flags {
		if !hasFlag(m.helpFlags, f) {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return model.PortFailuref(model.PortErrCapabilityMissing,
		"实测帮助清单里没有 %s：这些参数是受管启动的最小形态，缺任意一个都不拼参数（不凭记忆补参数）",
		strings.Join(missing, "、"))
}

// checkWorkspace 确认工作目录存在且是目录。
func checkWorkspace(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("工作目录不可用：%w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("工作目录 %q 不是目录", path)
	}
	return nil
}

// measurement 返回最近一次成功探测的快照；没有就不放行。
func (a *Adapter) measurement() (*measurement, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		return nil, model.PortFailure(model.PortErrEntryUnavailable,
			"尚未成功探测：启动规格必须绑定探测确认过的入口、发行包与版本，不能用配置里的路径现拼")
	}
	snapshot := *a.last // helpFlags／caps 创建后只读，浅拷贝即可
	return &snapshot, nil
}
