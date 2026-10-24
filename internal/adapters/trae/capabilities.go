package trae

import "github.com/kiwh77/maw/internal/engine/model"

const (
	capIDEChatPresent     = "entry.ide_chat_present"
	capCLIVersionProbe    = "entry.cli_version_probe"
	capCLIHelpProbe       = "entry.cli_help_probe"
	capManagedLaunch      = "managed.launch"
	capBackgroundResult   = "managed.background_result_recovery"
	capAuthDetect         = "auth.missing_credentials_detectable"
	capHeadless           = "print.headless_mode"
	capCancel             = "cancel.graceful_stop"
)

type capabilityRow struct {
	ID    string
	Name  string
	State model.CapabilityState
	Note  string
}

// ideMatrix：仅 IDE chat 时的能力——受管与后台回收一律 unsupported。
var ideMatrix = []capabilityRow{
	{ID: "T01", Name: capIDEChatPresent, State: model.CapabilityObserved,
		Note: "发现 Trae IDE chat 入口；人工接入可用"},
	{ID: "T02", Name: capManagedLaunch, State: model.CapabilityUnsupported,
		Note: "IDE chat 不能自动启动受管 CLI"},
	{ID: "T03", Name: capBackgroundResult, State: model.CapabilityUnsupported,
		Note: "IDE chat 不能后台回收结果；禁止宣称可回收"},
	{ID: "T04", Name: capCLIVersionProbe, State: model.CapabilityUnobserved,
		Note: "未绑定独立 CLI 入口"},
	{ID: "T05", Name: capAuthDetect, State: model.CapabilityUnknown,
		Note: "IDE 登录态不由本适配器只读探测"},
	{ID: "T06", Name: capCancel, State: model.CapabilityUnsupported,
		Note: "无受管进程可取消"},
}

// cliMatrix：独立 CLI 的基线；observed 仅在版本命中且探测成功后由 capabilitySet 确认。
var cliMatrix = []capabilityRow{
	{ID: "T10", Name: capCLIVersionProbe, State: model.CapabilityObserved,
		Note: "独立 CLI --version 只读探测"},
	{ID: "T11", Name: capCLIHelpProbe, State: model.CapabilityObserved,
		Note: "独立 CLI --help 只读探测"},
	{ID: "T12", Name: capHeadless, State: model.CapabilityObserved,
		Note: "帮助清单含非交互调用参数（证据样本）"},
	{ID: "T13", Name: capManagedLaunch, State: model.CapabilityObserved,
		Note: "版本与帮助通过后开放受管 Launch"},
	{ID: "T14", Name: capBackgroundResult, State: model.CapabilityObserved,
		Note: "CLI 结果经同一 Decode／Interpret 契约回收"},
	{ID: "T15", Name: capAuthDetect, State: model.CapabilityObserved,
		Note: "认证缺失可在真实运行 stderr／result 中识别；Probe 本身不登录"},
	{ID: "T16", Name: capIDEChatPresent, State: model.CapabilityUnobserved,
		Note: "本节点绑定的是 CLI，不继承 IDE chat 权益"},
	{ID: "T17", Name: capCancel, State: model.CapabilityUnknown,
		Note: "优雅取消未在本卡验证"},
}

func capabilitySet(class EntryClass, version model.SemVer, versionOK, helpOK, authOK bool) []model.Capability {
	var rows []capabilityRow
	switch class {
	case ClassIDEChat:
		rows = ideMatrix
	case ClassCLI:
		rows = cliMatrix
	default:
		rows = []capabilityRow{
			{ID: "T00", Name: capManagedLaunch, State: model.CapabilityUnsupported, Note: "入口分级未知"},
			{ID: "T00b", Name: capBackgroundResult, State: model.CapabilityUnsupported, Note: "入口分级未知"},
		}
	}
	out := make([]model.Capability, 0, len(rows))
	for _, row := range rows {
		state := row.State
		ver := version
		if ver == "" {
			ver = "0.0.0"
		}
		if class == ClassCLI {
			if state == model.CapabilityObserved && (!versionOK || version != MeasuredCLIVersion) {
				state = model.CapabilityUnobserved
			}
			if row.Name == capCLIHelpProbe && !helpOK && state == model.CapabilityObserved {
				state = model.CapabilityUnobserved
			}
			if (row.Name == capManagedLaunch || row.Name == capBackgroundResult) && !authOK {
				// 认证未就绪：不开放受管，但不把「未登录」写成 observed 成功
				if state == model.CapabilityObserved {
					state = model.CapabilityUnobserved
				}
			}
		}
		out = append(out, model.Capability{
			Name:        row.Name,
			State:       state,
			Version:     ver,
			EvidenceRef: EvidenceRoot + "/capability-matrix.json#" + row.ID,
		})
	}
	return out
}

func capIndex(caps []model.Capability) map[string]model.CapabilityState {
	m := map[string]model.CapabilityState{}
	for _, c := range caps {
		m[c.Name] = c.State
	}
	return m
}

func allowsManaged(caps map[string]model.CapabilityState) bool {
	return caps[capManagedLaunch] == model.CapabilityObserved &&
		caps[capBackgroundResult] == model.CapabilityObserved
}
