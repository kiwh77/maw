package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RunContextMode 是受管起跑的上下文策略。
//
// fresh：干净上下文，不携带任何上一题的摘要；
// fork：继承上一题摘要，必须写明摘要来源，否则证据不足、拒绝起跑。
//
// 它只描述「本次起跑用哪份上下文」，不决定派发什么、不调用模型，
// 与 system_jobs 的 kind、launch_spec 的协议是三套互不影响的概念。
type RunContextMode string

const (
	RunContextFresh RunContextMode = "fresh"
	RunContextFork  RunContextMode = "fork"
)

// ContextFile 是外部可预置的上下文文件名（落在 op 目录）。
// 之所以允许外部文件，是因为 model.LaunchSpec 是冻结值对象，
// 不为「上下文策略」这种编排侧决策新增字段。
const ContextFile = "context.json"

// RunContextSpec 是写进冻结规格的上下文决策。
type RunContextSpec struct {
	Mode          RunContextMode `json:"mode"`
	SummarySource string         `json:"summary_source,omitempty"`
	SummaryRef    string         `json:"summary_ref,omitempty"`
	RequestedBy   string         `json:"requested_by,omitempty"`
}

// FreshContext 返回干净上下文的默认决策。
func FreshContext() RunContextSpec { return RunContextSpec{Mode: RunContextFresh} }

// ParseRunContext 解析 launch_spec / context.json 里的 context 字段。
//
// 支持三种形态：空（默认 fresh）、"fresh" / "fork" 字符串、对象
// {"mode":"fork","summary_source":"ti_xxx","summary_ref":"..."}。
// 非法值一律返回错误，**绝不静默降级为 fresh**。
func ParseRunContext(raw json.RawMessage) (RunContextSpec, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return FreshContext(), nil
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil {
		return contextFromMode(mode, "", "", "")
	}
	var obj struct {
		Mode          string `json:"mode"`
		SummarySource string `json:"summary_source"`
		SummaryRef    string `json:"summary_ref"`
		RequestedBy   string `json:"requested_by"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return RunContextSpec{}, fmt.Errorf("journal: context 不是合法字符串或对象: %w", err)
	}
	return contextFromMode(obj.Mode, obj.SummarySource, obj.SummaryRef, obj.RequestedBy)
}

func contextFromMode(mode, source, ref, by string) (RunContextSpec, error) {
	m := RunContextMode(strings.ToLower(strings.TrimSpace(mode)))
	if m == "" {
		m = RunContextFresh
	}
	s := RunContextSpec{
		Mode:          m,
		SummarySource: strings.TrimSpace(source),
		SummaryRef:    strings.TrimSpace(ref),
		RequestedBy:   strings.TrimSpace(by),
	}
	if err := s.Validate(); err != nil {
		return RunContextSpec{}, err
	}
	return s, nil
}

// Validate 是上下文决策的唯一判定处：
//   - fresh 不得携带旧摘要（避免「说好干净、实际继承了上一题」）；
//   - fork 必须写明摘要来源（无证据不猜）；
//   - 其它 mode 一律拒绝。
//
// 零值 Mode 视为 fresh，保证既有调用方（未声明 context）行为不变。
func (s RunContextSpec) Validate() error {
	switch s.Mode {
	case "", RunContextFresh:
		if s.SummarySource != "" || s.SummaryRef != "" {
			return fmt.Errorf("journal: fresh 上下文不得携带旧摘要（summary_source / summary_ref 必须为空）")
		}
	case RunContextFork:
		if s.SummarySource == "" {
			return fmt.Errorf("journal: fork 上下文必须写明摘要来源 summary_source（证据不足不猜）")
		}
	default:
		return fmt.Errorf("journal: 非法 context 模式 %q（只接受 fresh / fork）", s.Mode)
	}
	return nil
}

// ResolveContext 按优先级解析本次起跑的上下文决策：
//
//  1. launch_spec.json 顶层 context 字段；
//  2. op 目录下的 context.json（外部预置，不必改冻结值对象）；
//  3. MAW_RUN_CONTEXT 环境变量（配合 MAW_CONTEXT_SUMMARY_SOURCE）；
//  4. 都没有 → fresh。
//
// 任何一处出现非法值都直接报错，不继续往下找、不降级。
func ResolveContext(opDir string, launchSpecJSON []byte) (RunContextSpec, error) {
	if len(launchSpecJSON) > 0 {
		var envelope struct {
			Context json.RawMessage `json:"context"`
		}
		if err := json.Unmarshal(launchSpecJSON, &envelope); err != nil {
			return RunContextSpec{}, fmt.Errorf("journal: 解析 launch_spec 顶层 context: %w", err)
		}
		if strings.TrimSpace(string(envelope.Context)) != "" {
			return ParseRunContext(envelope.Context)
		}
	}
	if opDir != "" {
		if b, err := os.ReadFile(filepath.Join(opDir, ContextFile)); err == nil &&
			strings.TrimSpace(string(b)) != "" {
			return ParseRunContext(b)
		}
	}
	if v := strings.TrimSpace(os.Getenv("MAW_RUN_CONTEXT")); v != "" {
		return contextFromMode(v,
			os.Getenv("MAW_CONTEXT_SUMMARY_SOURCE"),
			os.Getenv("MAW_CONTEXT_SUMMARY_REF"), "env")
	}
	return FreshContext(), nil
}
