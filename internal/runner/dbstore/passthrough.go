package dbstore

import (
	"context"
	"encoding/json"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// PassthroughSource 是最小 Decode/Interpret：把每行当原始记录引用，按退出码判定。
// 用于 InternalRunner 在无厂商 adapter 时装载 launch_spec 时仍能跑通 session.Runner。
type PassthroughSource struct{}

func (PassthroughSource) Decode(ctx context.Context, req ports.DecodeRequest) (ports.DecodeResult, error) {
	_ = ctx
	if errs := req.Validate(); len(errs) > 0 {
		return ports.DecodeResult{}, model.Err(errs)
	}
	return ports.DecodeResult{
		Events:  nil,
		RawRef:  req.RecordRef,
		Quality: model.QualityObserved,
	}, nil
}

func (PassthroughSource) Interpret(ctx context.Context, req ports.InterpretRequest) (model.InterpretResult, error) {
	_ = ctx
	if errs := req.Validate(); len(errs) > 0 {
		return model.InterpretResult{}, model.Err(errs)
	}
	res := model.InterpretResult{
		Outcome: model.OutcomeUnknown,
		Quality: model.QualityObserved,
	}
	if req.TimedOut {
		res.Outcome = model.OutcomeTimedOut
		return res, nil
	}
	if req.ApplicationError != "" {
		res.Outcome = model.OutcomeFailed
		return res, nil
	}
	if req.ExitCode != nil {
		if *req.ExitCode == 0 {
			res.Outcome = model.OutcomeCompleted
		} else {
			res.Outcome = model.OutcomeFailed
		}
		return res, nil
	}
	if len(req.FinalResult) > 0 {
		var probe map[string]any
		if json.Unmarshal(req.FinalResult, &probe) == nil {
			if subtype, ok := probe["subtype"].(string); ok && subtype == "success" {
				res.Outcome = model.OutcomeCompleted
				return res, nil
			}
		}
		res.Outcome = model.OutcomeCompleted
	}
	return res, nil
}
