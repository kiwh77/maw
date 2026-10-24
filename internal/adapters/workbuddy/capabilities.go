package workbuddy

import (
	"github.com/kiwh77/maw/internal/engine/model"
)

// 能力名常量：启动／判定侧只引用常量，避免字符串写错导致门禁永远不触发。
const (
	capVersionProbe = "entry.version_probe"
	capHelpProbe    = "entry.help_probe"
	capRuntimeNode  = "entry.runtime_node"

	capHeadless   = "print.headless_mode"
	capOutputJSON = "output_format.json"
	capStreamJSON = "output_format.stream_json"

	capPartialMsgs = "event_stream.partial_messages"
	capRecordSeq   = "event_stream.record_sequence"

	capResultRecord = "result.record_present"
	capZeroExitAuth = "result.exit_zero_without_result"
	capAuthDetect   = "auth.missing_credentials_detectable"

	capPermission = "permission.mode_flag"
	capMCPStrict  = "mcp.strict_config_flag"
	capPortAuto   = "launch.port_auto_assign"

	capModelEnum   = "model.enumeration"
	capModelShared = "model.list_interchangeable"

	capUsage     = "usage.fields_present"
	capNumTurns  = "turn.num_turns_semantics"
	capResume    = "session.resume"
	capNoPersist = "session.no_persistence_flag"
	capCancel    = "cancel.graceful_stop"
)

// capabilityRow 是证据矩阵的一行。
//
// NeedsHelp 表示这条事实只能从实测帮助清单里读出来：帮助探测没跑成时，
// 它必须降级为 unobserved，而不是沿用上一轮的观察。
type capabilityRow struct {
	ID    string
	Name  string
	State model.CapabilityState
	// Sample 是样本标识（"S01-version-probe" 这种）；空表示证据是矩阵本身。
	Sample string
	// NeedsHelp 标记该行依赖本次 --help 探测。
	NeedsHelp bool
	Note      string
}

// capabilityMatrix 是 docs/evidence/workbuddy/capability-matrix.json 的逐行副本，
// capabilities_test.go 会把它和 JSON 对账，防止代码里悄悄多一条或少一条能力。
//
// 两包共用这张表，但**每条能力都按发行包挂自己的证据目录**（Distribution.Sample）：
// 状态相同不等于可以互相引用，能力归属始终是「这个包的这条事实」。
var capabilityMatrix = []capabilityRow{
	{ID: "W01", Name: capVersionProbe, State: model.CapabilityObserved, Sample: "S01-version-probe",
		Note: "两包随包入口 --version 均退出 0，stdout 单行 2.161.0（8 字节，65–70ms）"},
	{ID: "W02", Name: capHelpProbe, State: model.CapabilityObserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "两包 --help 均退出 0，104 行；CN 12706 字节、intl 12654 字节，唯一差异是 --model 枚举"},
	{ID: "W03", Name: capRuntimeNode, State: model.CapabilityObserved,
		Note: "两包入口首行均为 `#!/usr/bin/env node`；运行时身份由入口解析得出"},
	{ID: "W04", Name: capHeadless, State: model.CapabilityObserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "帮助清单列出 -p/--print；最小调用证据（X01）确认进程能跑到认证检查这一步"},
	{ID: "W05", Name: capOutputJSON, State: model.CapabilityObserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "帮助清单给出 --output-format 取值 text／json／stream-json；X01 实际使用 json 调用"},
	{ID: "W06", Name: capStreamJSON, State: model.CapabilityUnobserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "帮助清单声明了 stream-json，但两包从未产出过一条 stream-json 记录（认证先行失败），记录形状未观测"},
	{ID: "W07", Name: capPartialMsgs, State: model.CapabilityUnobserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "--include-partial-messages 已在帮助清单中，但没有 stream-json 样本可验证其增量语义"},
	{ID: "W08", Name: capRecordSeq, State: model.CapabilityUnobserved,
		Note: "没有任何记录样本，记录顺序／去重语义无从判定"},
	{ID: "W09", Name: capResultRecord, State: model.CapabilityUnobserved,
		Note: "两包均未完成过回合，result 记录从未出现；单条 json 结果形状未观测，判完成必须等证据"},
	{ID: "W10", Name: capZeroExitAuth, State: model.CapabilityObserved, Sample: "X01-auth-required",
		Note: "认证缺失形态是退出码 0 + stdout 空 + stderr 有错误：退出码不能作为成功依据"},
	{ID: "W11", Name: capAuthDetect, State: model.CapabilityObserved, Sample: "X01-auth-required",
		Note: "唯一稳定信号是 stderr 文本 `Authentication required`；与 CodeBuddy 走 result.errors 的路径不同"},
	{ID: "W12", Name: capPermission, State: model.CapabilityObserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "帮助清单列出 --permission-mode 及 acceptEdits／bypassPermissions／default／plan／dontAsk／auto"},
	{ID: "W13", Name: capMCPStrict, State: model.CapabilityObserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "帮助清单列出 --mcp-config 与 --strict-mcp-config"},
	{ID: "W14", Name: capPortAuto, State: model.CapabilityObserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "帮助清单写明 --port 默认 auto-assign；2.173.0 上必须显式 --port 0 的经验不适用于本包，故不继承"},
	{ID: "W15", Name: capModelEnum, State: model.CapabilityObserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "两包 --model 枚举分别登记：CN 9 个具体模型 ID，intl 4 个占位 ID"},
	{ID: "W16", Name: capModelShared, State: model.CapabilityUnsupported, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "两包帮助清单唯一差异行就是 --model 枚举，模型取值不可跨包复制"},
	{ID: "W17", Name: capUsage, State: model.CapabilityUnobserved,
		Note: "没有 result 记录，用量字段是否存在未观测"},
	{ID: "W18", Name: capNumTurns, State: model.CapabilityUnknown,
		Note: "--max-turns 存在（帮助清单），但其计数语义未验证"},
	{ID: "W19", Name: capResume, State: model.CapabilityUnobserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "帮助清单列出 -r/--resume，但恢复语义与会话存在性都未验证"},
	{ID: "W20", Name: capNoPersist, State: model.CapabilityObserved, Sample: "S02-help-probe", NeedsHelp: true,
		Note: "帮助清单列出 --no-session-persistence；最小调用证据里一并使用"},
	{ID: "W21", Name: capCancel, State: model.CapabilityUnknown,
		Note: "未验证取消传播与优雅停止"},
}

// capabilitySet 生成本次探测的能力表。
//
// 降级规则只来自证据，不是这里自创的：
//   - 版本不是本包实测版本 → observed 降级为 unobserved（G07：能力按版本验证）；
//   - 依赖帮助清单的行，在本次帮助探测没跑成时降级为 unobserved；
//   - 版本探测与运行时形态是「刚刚这一秒测到的」，不受上一版本证据约束；
//   - unknown／unobserved／unsupported 原样保留，不因为「装好了」就翻成可用。
func capabilitySet(dist Distribution, version model.SemVer, helpProbed bool) []model.Capability {
	out := make([]model.Capability, 0, len(capabilityMatrix))
	for _, row := range capabilityMatrix {
		state := row.State
		if state == model.CapabilityObserved && version != dist.BundledCLI {
			state = model.CapabilityUnobserved
		}
		if row.NeedsHelp && !helpProbed && state == model.CapabilityObserved {
			state = model.CapabilityUnobserved
		}
		switch row.Name {
		case capVersionProbe:
			state = model.CapabilityObserved
		case capHelpProbe:
			if helpProbed {
				state = model.CapabilityObserved
			} else {
				state = model.CapabilityUnobserved
			}
		case capRuntimeNode:
			// 运行时形态在 Probe 里已经验过（checkRuntimeShape 不过是走不到这里的）。
			state = model.CapabilityObserved
		}
		out = append(out, model.Capability{
			Name:        row.Name,
			State:       state,
			Version:     version,
			EvidenceRef: dist.Sample(row.Sample),
		})
	}
	return out
}

// capabilityIndex 把能力表压成 name → state，供热路径查表。
func capabilityIndex(caps []model.Capability) map[string]model.CapabilityState {
	out := make(map[string]model.CapabilityState, len(caps))
	for _, c := range caps {
		out[c.Name] = c.State
	}
	return out
}
