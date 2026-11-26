package codebuddy

import (
	"github.com/kiwh77/maw/internal/engine/model"
)

// EvidenceRoot 是证据目录引用（P140 要求每条能力绑定版本与证据）。
const EvidenceRoot = "docs/evidence/codebuddy"

// MeasuredCLIVersion 是证据矩阵实测的 CLI 版本。
//
// 只有在这个版本上，矩阵里的 observed 行才成立。换版本后 observed 一律降级为
// unobserved（capability-matrix.json 的 version_binding 规则），Launch 随之拒绝启动。
const MeasuredCLIVersion model.SemVer = "2.173.0"

// capabilityRow 是证据矩阵的一行：编号、能力名、状态、样本目录。
// 这份表是 docs/evidence/codebuddy/capability-matrix.json 的逐行副本，
// capabilities_test.go 会把它和 JSON 对账，防止代码里悄悄多一条或少一条能力。
type capabilityRow struct {
	ID       string
	Name     string
	State    model.CapabilityState
	Evidence string
}

// 能力名常量：启动侧只引用常量，避免字符串写错导致「门禁永远不触发」。
const (
	capVersionProbe = "entry.version_probe"
	capHelpProbe    = "entry.help_probe"
	capHeadless     = "print.headless_mode"
	capStreamJSON   = "output_format.stream_json"
	capPartialMsgs  = "event_stream.partial_messages"
	capExplicitPort = "launch.explicit_port_required"
	capResume       = "session.resume"
)

// capabilityMatrix 顺序与 JSON 一致（C01…C28）。
var capabilityMatrix = []capabilityRow{
	{ID: "C01", Name: capVersionProbe, State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S01-version-probe"},
	{ID: "C02", Name: capHelpProbe, State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S02-help-probe"},
	{ID: "C03", Name: capHeadless, State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S03-print-stream-json"},
	{ID: "C04", Name: capStreamJSON, State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S03-print-stream-json"},
	{ID: "C05", Name: "output_format.json", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S04-print-json"},
	{ID: "C06", Name: "output_format.text", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S05-print-text"},
	{ID: "C07", Name: "output_format.invalid_value_accepted", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S07-invalid-output-format"},
	{ID: "C08", Name: "event_stream.record_sequence", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S03-print-stream-json"},
	{ID: "C09", Name: capPartialMsgs, State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S03-print-stream-json"},
	{ID: "C10", Name: "event_stream.assistant_repeated", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S03-print-stream-json"},
	{ID: "C11", Name: "result.record_present", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S03-print-stream-json"},
	{ID: "C12", Name: "result.exit_zero_with_is_error", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S08-print-stream-json-no-credentials"},
	{ID: "C13", Name: "result.auth_error_text_in_assistant", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S08-print-stream-json-no-credentials"},
	{ID: "C14", Name: "auth.missing_credentials_detectable", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S08-print-stream-json-no-credentials"},
	{ID: "C15", Name: capExplicitPort, State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S06-port-conflict"},
	{ID: "C16", Name: "turn.completed_turn", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S03-print-stream-json"},
	{ID: "C17", Name: "usage.fields_present", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S03-print-stream-json"},
	{ID: "C18", Name: "usage.input_includes_cache", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S03-print-stream-json"},
	{ID: "C19", Name: "usage.aggregation_semantics", State: model.CapabilityUnknown, Evidence: EvidenceRoot + "/capability-matrix.json"},
	{ID: "C20", Name: "turn.num_turns_semantics", State: model.CapabilityUnknown, Evidence: EvidenceRoot + "/capability-matrix.json"},
	{ID: "C21", Name: "cost.total_cost_usd", State: model.CapabilityUnknown, Evidence: EvidenceRoot + "/capability-matrix.json"},
	{ID: "C22", Name: "modelUsage.may_be_null", State: model.CapabilityObserved, Evidence: EvidenceRoot + "/samples/VN301-S04-print-json"},
	{ID: "C23", Name: "cancel.graceful_stop", State: model.CapabilityUnknown, Evidence: EvidenceRoot + "/capability-matrix.json"},
	{ID: "C24", Name: capResume, State: model.CapabilityUnknown, Evidence: EvidenceRoot + "/capability-matrix.json"},
	{ID: "C25", Name: "session.id_reuse", State: model.CapabilityUnobserved, Evidence: EvidenceRoot + "/capability-matrix.json"},
	{ID: "C26", Name: "error.stable_code", State: model.CapabilityUnobserved, Evidence: EvidenceRoot + "/capability-matrix.json"},
	{ID: "C27", Name: "tool_use.events", State: model.CapabilityUnobserved, Evidence: EvidenceRoot + "/capability-matrix.json"},
	{ID: "C28", Name: "stderr.carries_error", State: model.CapabilityUnobserved, Evidence: EvidenceRoot + "/capability-matrix.json"},
}

// capabilitySet 生成本次探测的能力表。
//
// 降级规则都来自证据矩阵，不是这里自创的：
//   - 版本不是实测版本 → observed 降级为 unobserved（version_binding.invalidated_by: cli_version_change）；
//   - 本次帮助探测没跑成 → entry.help_probe 降级为 unobserved（没测到就是没测到）。
//
// 两个反例：版本探测与帮助探测是「刚刚这一秒测到的」，不受上一版本证据约束；
// 而 unknown／unobserved 原样保留，不因为「安装好了」就翻成可用。
func capabilitySet(version model.SemVer, helpProbed bool) []model.Capability {
	out := make([]model.Capability, 0, len(capabilityMatrix))
	for _, row := range capabilityMatrix {
		state := row.State
		if state == model.CapabilityObserved && version != MeasuredCLIVersion {
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
		}
		out = append(out, model.Capability{
			Name:        row.Name,
			State:       state,
			Version:     version,
			EvidenceRef: row.Evidence,
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
