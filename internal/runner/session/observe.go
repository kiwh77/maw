package session

import (
	"context"
	"fmt"

	"strings"

	"github.com/kiwh77/maw/internal/adapters/codebuddy"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/observe"
)

// codebuddySourceNS 是 CodeBuddy 来源的命名空间。
const codebuddySourceNS = "codebuddy"

// WithObserver 注入观测落库端口。
//
// 不给就不落观测：观测落不了时宁可缺席，也不能用 0 Token 冒充。
func WithObserver(w observe.Writer) Option {
	return func(r *Runner) { r.observer = w }
}

// factsOf 从来源记录里解析观测事实。
//
// 只有 CodeBuddy 来源给得出：它的实测记录带会话身份与 usage。
// 其它来源（passthrough）没有可解释的计量字段 → 没有事实，不落库。
func (r *Runner) factsOf(cfg Config, rec journal.Record) (observe.Facts, bool) {
	if _, ok := r.source.(*codebuddy.Adapter); !ok {
		return observe.Facts{}, false
	}
	ref, err := codebuddy.ParseSessionRef(rec.Payload)
	if err != nil || ref == nil || strings.TrimSpace(ref.SessionID) == "" {
		// 没有来源会话身份就不发明：调用方保持未归属。
		return observe.Facts{}, false
	}
	facts := observe.Facts{
		RunID:      string(cfg.RunID),
		InstanceID: string(cfg.Instance),
		SourceNS:   codebuddySourceNS,
		SessionID:  ref.SessionID,
		RecordUUID: ref.RecordUUID,
		RecordType: ref.RecordType,
		RecordRef:  fmt.Sprintf("%d:%d", rec.Seq, rec.Line),
		OccurredAt: rec.ReceivedAt,
		Scope:      "run",
		Quality:    model.QualityUnknown,
	}
	if usage, err := codebuddy.ParseUsage(rec.Payload); err == nil && usage != nil {
		facts.Semantic = string(usage.Semantic)
		facts.InputTokens = usage.InputTokens
		facts.OutputTokens = usage.OutputTokens
		facts.CacheReadTokens = usage.CacheReadTokens
		facts.CacheWriteTokens = usage.CacheWriteTokens
		facts.TotalTokens = usage.TotalTokens
		facts.Quality = usage.Quality
		facts.Evidence = usage.Evidence
	}
	return facts, true
}

// observeFacts 落一次观测。
//
// 落库失败不阻塞运行：事实已经进了 journal，观测可以补交；反过来，
// 观测失败就把运行判失败是不成比例的。
func (r *Runner) observeFacts(ctx context.Context, cfg Config, rec journal.Record) {
	if r.observer == nil {
		return
	}
	facts, ok := r.factsOf(cfg, rec)
	if !ok {
		return
	}
	if err := r.observer.WriteFacts(ctx, facts); err != nil {
		// 观测失败不改变运行结论，但要计数报出：否则「没记上」会被当成「没有」。
		r.observationFailures.Add(1)
	}
}
