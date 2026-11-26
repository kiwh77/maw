package trae

import (
	"context"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// Interpret 仅对已开放受管能力的 CLI 结果做判定；退出码 0 不是成功依据。
func (a *Adapter) Interpret(ctx context.Context, req ports.InterpretRequest) (model.InterpretResult, error) {
	_ = ctx
	if vs := req.Validate(); len(vs) > 0 {
		return model.InterpretResult{}, model.PortFailuref(model.PortErrInvalidSpec,
			"判定请求不合法：%v", model.Err(vs))
	}
	a.mu.Lock()
	last := a.last
	a.mu.Unlock()
	if last == nil || last.class != ClassCLI || !allowsManaged(last.caps) {
		return model.InterpretResult{}, model.PortFailure(model.PortErrCapabilityMissing,
			"IDE chat／未验证 CLI 不解释后台结果")
	}

	res := model.InterpretResult{
		Outcome:   model.OutcomeUnknown,
		Quality:   model.QualityUnknown,
		Artifacts: req.Artifacts,
	}
	if req.TimedOut {
		res.Outcome = model.OutcomeTimedOut
		res.Quality = model.QualityObserved
		return res, nil
	}
	if msg := strings.TrimSpace(req.ApplicationError); msg != "" {
		res.Outcome = model.OutcomeFailed
		res.ResultText = msg
		res.Quality = model.QualityObserved
		if strings.Contains(strings.ToLower(msg), "authentication required") ||
			strings.Contains(msg, "auth_required") {
			return res, model.PortFailure(model.PortErrAuthRequired,
				"独立 CLI 报告未登录：请在该 CLI 登录，不继承 IDE 会话")
		}
		return res, model.PortFailuref(model.PortErrApplication, "应用错误：%s", msg)
	}
	if len(req.FinalResult) == 0 {
		// 退出码即使为 0 也不能当成功
		return res, model.PortFailure(model.PortErrMissingResult,
			"缺少最终结果记录：退出码不是成功依据")
	}
	res.Outcome = model.OutcomeUnknown
	res.Quality = model.QualityEstimated
	res.ResultText = string(req.FinalResult)
	return res, nil
}
