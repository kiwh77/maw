package session

import (
	"context"
	"errors"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/observe"
)

// WithObserver 注入观测落库端口。
//
// 不给就不落观测：观测落不了时宁可缺席，也不能用 0 Token 冒充。
func WithObserver(w observe.Writer) Option {
	return func(r *Runner) { r.observer = w }
}

// factsOf 从一条来源记录里解析观测事实，并补上 maw 侧的归属。
//
// 两件事分得很开：来源解释器只说「这条记录报告了什么」（会话、调用、用量），
// 归属（项目／节点／Run／代际／用途）由 Runner 绑定——模型输出里任何「我属于哪题」
// 都不作为绑定依据（契约 §4）。
//
// 不认识协议的来源没有可解释的计量字段 → 没有事实，不落库。
func (r *Runner) factsOf(cfg Config, rec journal.Record) (observe.Facts, bool) {
	ext, ok := observe.ExtractorFor(cfg.Protocol)
	if !ok {
		return observe.Facts{}, false
	}
	facts, ok := ext.Extract(observe.Record{
		Payload:    rec.Payload,
		Seq:        rec.Seq,
		Line:       rec.Line,
		ReceivedAt: rec.ReceivedAt,
	})
	if !ok {
		return observe.Facts{}, false
	}
	facts.ProjectID = firstNonEmpty(cfg.ProjectID, facts.ProjectID)
	facts.RunID = string(cfg.RunID)
	facts.InstanceID = string(cfg.Instance)
	if cfg.Generation >= 1 {
		// 代际以 maw 的条件写入为准：跨代际不得合并。
		facts.Generation = int(cfg.Generation)
	}
	if node := strings.TrimSpace(cfg.NodeID); node != "" {
		facts.NodeID = node
	}
	if purpose := strings.TrimSpace(cfg.Purpose); purpose != "" {
		facts.Purpose = purpose
	}
	if facts.Scope == "" {
		facts.Scope = observe.ScopeRun
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
		r.noteObservationBacklog(rec.Seq)
		return
	}
	r.observed.Add(1)
}

// noteObservationBacklog 记下写库失败的位置，供事后补录。
//
// 只记 journal 序号：原始记录已经落盘，补录时按序号重放即可。
func (r *Runner) noteObservationBacklog(seq int64) {
	if seq <= 0 {
		return
	}
	r.backlogMu.Lock()
	defer r.backlogMu.Unlock()
	for _, seen := range r.observationBacklog {
		if seen == seq {
			return
		}
	}
	r.observationBacklog = append(r.observationBacklog, seq)
}

// observationBacklogSeqs 返回待补录位置（拷贝，避免调用方改内部状态）。
func (r *Runner) observationBacklogSeqs() []int64 {
	r.backlogMu.Lock()
	defer r.backlogMu.Unlock()
	if len(r.observationBacklog) == 0 {
		return nil
	}
	out := make([]int64, len(r.observationBacklog))
	copy(out, r.observationBacklog)
	return out
}

// clearObservationBacklog 在补录成功后清空待补录位置。
func (r *Runner) clearObservationBacklog() {
	r.backlogMu.Lock()
	defer r.backlogMu.Unlock()
	r.observationBacklog = nil
}

// BackfillReport 描述一次观测补录的结果。
type BackfillReport struct {
	Scanned int // 扫过的 journal 记录数
	Matched int // 其中产出观测事实的记录数
	Written int // 成功落库的条数
	Failed  int // 仍然失败的条数
}

// BackfillObservations 从 journal 重放观测，补上写库失败留下的缺口。
//
// 补录只重放原始记录：去重身份（来源命名空间＋会话＋记录身份）是派生出来的，
// 重放得到同一个身份，不会多记一次（契约 P180／P181）。补录不改既有 epoch 与 scope。
//
// 数据库短暂失败后的补录不依赖运行继续：只要有 journal 与观测端口就能独立执行。
func (r *Runner) BackfillObservations(ctx context.Context, cfg Config) (BackfillReport, error) {
	out := BackfillReport{}
	if r.observer == nil {
		return out, errors.New("session: 没有观测端口，观测无法补录")
	}
	if r.journal == nil {
		return out, errors.New("session: 没有 journal，原始记录已丢，观测无法补录")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, rec := range r.journal.Records() {
		out.Scanned++
		facts, ok := r.factsOf(cfg, rec)
		if !ok {
			continue
		}
		out.Matched++
		if err := r.observer.WriteFacts(ctx, facts); err != nil {
			out.Failed++
			continue
		}
		out.Written++
		r.observed.Add(1)
	}
	if out.Failed == 0 {
		r.clearObservationBacklog()
	}
	return out, nil
}

// firstNonEmpty 取第一个非空值。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// sourceNamespace 给 journal 记录标注来源命名空间。
//
// 命名空间是去重身份的第一段：按协议确定，而不是写死某个厂商名。
func sourceNamespace(p model.Protocol) string {
	if ext, ok := observe.ExtractorFor(p); ok {
		return ext.SourceNS()
	}
	if format := strings.TrimSpace(p.Format); format != "" {
		return format
	}
	return "unspecified"
}
