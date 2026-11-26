package trae

import (
	"context"
	"encoding/json"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// Decode 仅在 CLI 受管能力开放时尝试解释；IDE chat 只保留原始引用、不产生事件。
func (a *Adapter) Decode(ctx context.Context, req ports.DecodeRequest) (ports.DecodeResult, error) {
	_ = ctx
	if vs := req.Validate(); len(vs) > 0 {
		return ports.DecodeResult{}, model.PortFailuref(model.PortErrInvalidSpec,
			"解码请求不合法：%v", model.Err(vs))
	}
	a.mu.Lock()
	last := a.last
	a.mu.Unlock()
	if last == nil || last.class != ClassCLI || !allowsManaged(last.caps) {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown},
			model.PortFailure(model.PortErrCapabilityMissing,
				"IDE chat／未验证 CLI 不解码后台结果：保持人工模式，不宣称可回收")
	}
	if !json.Valid(req.Payload) {
		return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityUnknown},
			model.PortFailuref(model.PortErrUnparseableRecord,
				"记录 %s 不是合法 JSON：只保留原始引用", req.RecordRef)
	}
	// 有受管能力但仍无完整结果形状样本时：保留引用，不编造事件
	return ports.DecodeResult{RawRef: req.RecordRef, Quality: model.QualityEstimated}, nil
}
