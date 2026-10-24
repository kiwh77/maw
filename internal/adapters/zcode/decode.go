package zcode

import (
	"context"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// Decode 在模型调用未验证时只保留原始引用。
func (a *Adapter) Decode(ctx context.Context, req ports.DecodeRequest) (ports.DecodeResult, error) {
	_ = ctx
	if vs := req.Validate(); len(vs) > 0 {
		return ports.DecodeResult{}, model.PortFailuref(model.PortErrInvalidSpec, "%v", model.Err(vs))
	}
	a.mu.Lock()
	last := a.last
	a.mu.Unlock()
	if last == nil || last.caps[capModelInvoke] != model.CapabilityObserved {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown},
			model.PortFailure(model.PortErrCapabilityMissing,
				"模型调用未验证：不解析结果流，不开放未实测能力")
	}
	return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityEstimated}, nil
}
