package zcode

import (
	"context"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// Interpret 不把退出码 0 当成功；取消／恢复未验证时不伪造结论。
func (a *Adapter) Interpret(ctx context.Context, req ports.InterpretRequest) (model.InterpretResult, error) {
	_ = ctx
	if vs := req.Validate(); len(vs) > 0 {
		return model.InterpretResult{}, model.PortFailuref(model.PortErrInvalidSpec, "%v", model.Err(vs))
	}
	a.mu.Lock()
	last := a.last
	a.mu.Unlock()
	if last == nil || last.caps[capModelInvoke] != model.CapabilityObserved {
		return model.InterpretResult{Outcome: model.OutcomeUnknown, Quality: model.QualityUnknown},
			model.PortFailure(model.PortErrCapabilityMissing, "模型调用未验证：不判定完成")
	}
	res := model.InterpretResult{Outcome: model.OutcomeUnknown, Quality: model.QualityUnknown, Artifacts: req.Artifacts}
	if req.TimedOut {
		res.Outcome = model.OutcomeTimedOut
		res.Quality = model.QualityObserved
		return res, nil
	}
	return res, model.PortFailure(model.PortErrMissingResult, "缺少可判定结果记录")
}
