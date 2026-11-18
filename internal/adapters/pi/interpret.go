package pi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// resultPayload 是运行时 result 事件的 payload（契约 §7）。
type resultPayload struct {
	OK       bool     `json:"ok"`
	Outcome  string   `json:"outcome"`
	Reason   string   `json:"reason"`
	Text     string   `json:"text"`
	Evidence []string `json:"evidence"`
}

// Interpret 实现 ports.Interpret：判定本次运行的结果。
//
// 三条硬规则（契约 §7）：
//   - 退出码 0 不能单独产生 completed；
//   - 崩溃／错误不得被 completed 覆盖；
//   - 拿不到可判定的 result 时保持 unknown，不猜成功。
func (a *Adapter) Interpret(ctx context.Context, req ports.InterpretRequest) (model.InterpretResult, error) {
	if vs := req.Validate(); len(vs) > 0 {
		return model.InterpretResult{}, model.PortFailuref(model.PortErrInvalidSpec,
			"判定请求不合法：%v", model.Err(vs))
	}
	if err := ctx.Err(); err != nil {
		return model.InterpretResult{}, err
	}
	if req.TimedOut {
		return model.InterpretResult{Outcome: model.OutcomeTimedOut, Quality: model.QualityObserved}, nil
	}

	res, hasResult, err := parseResult(req.FinalResult)
	if err != nil {
		return model.InterpretResult{Outcome: model.OutcomeUnknown, Quality: model.QualityUnknown},
			model.PortFailuref(model.PortErrUnparseableRecord,
				"最终结果不是可判定的 result 事件：%v；保持 unknown，不按退出码推断", err)
	}
	if !hasResult {
		// 没有最终结果记录：只能凭应用错误或非零退出码判失败，不能判成功。
		if strings.TrimSpace(req.ApplicationError) != "" || (req.ExitCode != nil && *req.ExitCode != 0) {
			return model.InterpretResult{Outcome: model.OutcomeFailed, Quality: model.QualityEstimated}, nil
		}
		return model.InterpretResult{Outcome: model.OutcomeUnknown, Quality: model.QualityUnknown}, nil
	}

	out := model.InterpretResult{
		Outcome:    outcomeOf(res),
		ResultText: strings.TrimSpace(res.Text),
		Artifacts:  req.Artifacts,
		Quality:    qualityOf(res),
	}
	// 结果说成功但退出码非零：以结果为准，但质量降级（证据与进程事实矛盾）。
	if out.Outcome == model.OutcomeCompleted && req.ExitCode != nil && *req.ExitCode != 0 {
		out.Quality = model.QualityEstimated
	}
	return out, nil
}

// parseResult 读取 result 事件（运行时输出的一行信封）。
//
// 也接受裸 payload（只含 ok/outcome），便于测试与日志回放。
func parseResult(raw []byte) (resultPayload, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return resultPayload{}, false, nil
	}
	// 先按事件信封解析；失败再按裸 payload 解析。
	var env envelope
	if err := json.Unmarshal([]byte(trimmed), &env); err == nil && env.Type != "" {
		if env.Type != "result" {
			return resultPayload{}, false, nil
		}
		var res resultPayload
		if err := json.Unmarshal(env.Payload, &res); err != nil {
			return resultPayload{}, false, err
		}
		return res, true, nil
	}
	var res resultPayload
	if err := json.Unmarshal([]byte(trimmed), &res); err != nil {
		return resultPayload{}, false, err
	}
	return res, true, nil
}

// outcomeOf 把运行时结论映射成封闭集合。
//
// 认不出的结论一律 unknown：宁可让人复核，也不把未知当成功。
func outcomeOf(res resultPayload) model.Outcome {
	switch strings.ToLower(strings.TrimSpace(res.Outcome)) {
	case "completed":
		return model.OutcomeCompleted
	case "failed":
		return model.OutcomeFailed
	case "cancelled":
		return model.OutcomeCancelled
	case "timed_out":
		return model.OutcomeTimedOut
	default:
		if res.OK {
			// 只说 ok 不说 outcome：证据不足，不猜 completed。
			return model.OutcomeUnknown
		}
		return model.OutcomeUnknown
	}
}

// qualityOf：有证据引用才算 observed，否则 estimated（结论来自单一布尔值）。
func qualityOf(res resultPayload) model.Quality {
	if len(res.Evidence) > 0 {
		return model.QualityObserved
	}
	if strings.TrimSpace(res.Reason) != "" {
		return model.QualityEstimated
	}
	return model.QualityUnknown
}
