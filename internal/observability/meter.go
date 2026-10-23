package observability

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// LinkRequest 把一次 Run 挂到来源会话／轮次。
type LinkRequest struct {
	LinkID     string
	ProjectID  string
	RunID      string
	InstanceID string
	SessionID  string
	TurnID     string
	// Attributed 为 false 时表示看到了关联线索但未验证归属。
	Attributed bool
}

// UsageSnapshot 是来源给出的一轮累计或增量快照。
type UsageSnapshot struct {
	SourceNS         string
	TurnID           string
	Scope            string
	Semantic         db.UsageSemantic
	InputTokens      *int64
	OutputTokens     *int64
	CacheReadTokens  *int64
	CacheWriteTokens *int64
	Quality          model.Quality
	Basis            string
	EventID          string
}

// Allocation 是把一轮用量分到任务实例的可选结果。
type Allocation struct {
	InstanceID string
	// Share 是 0–1 的分摊系数；未知时整轮进 Unallocated。
	Share float64
}

// MeterReport 是一次计量查询结果。
type MeterReport struct {
	TurnID string `json:"turn_id"`
	// Total 是按 turn 去重后的已知消耗；Token 为 null 表示该分量未知。
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	CacheReadTokens  *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
	TotalTokens      *int64 `json:"total_tokens,omitempty"`
	// Unallocated 为 true 表示有关联 Run／任务但没有合法分摊依据。
	Unallocated bool `json:"unallocated"`
	// Allocations 仅在有分摊依据时非空；份额之和必须为 1。
	Allocations []Allocation `json:"allocations,omitempty"`
	// Coverage 只对已观测分母：ObservedTurns / ObservedDenom。
	Coverage Coverage `json:"coverage"`
	Quality  model.Quality `json:"quality"`
}

// Coverage 描述观测覆盖率；分母不含「从未观测」的轮次。
type Coverage struct {
	ObservedTurns int     `json:"observed_turns"`
	ObservedDenom int     `json:"observed_denom"`
	Ratio         float64 `json:"ratio"` // ObservedDenom==0 时为 0，不假装 1
}

// Service 是会话归属与用量计量用例。
type Service struct {
	// 可选：自定义现在时间戳（测试注入）。
}

// EnsureSession 登记来源会话（命名空间内外部身份唯一）。
func (s *Service) EnsureSession(tx *sql.Tx, sess db.SourceSession) error {
	return db.UpsertSourceSession(tx, sess)
}

// EnsureTurn 登记轮次；SharedWith 列出同轮其它任务，不复制 turn 行。
func (s *Service) EnsureTurn(tx *sql.Tx, turn db.SourceTurn) error {
	return db.UpsertSourceTurn(tx, turn)
}

// LinkRun 写入 Run↔来源关联。
func (s *Service) LinkRun(tx *sql.Tx, req LinkRequest) error {
	if strings.TrimSpace(req.TurnID) == "" && strings.TrimSpace(req.SessionID) == "" {
		return model.Violation(model.CodePortFieldInvalid, "link", "至少提供 session_id 或 turn_id")
	}
	id := req.LinkID
	if id == "" {
		id = fmt.Sprintf("link_%s_%s_%s", req.RunID, req.TurnID, req.InstanceID)
	}
	return db.InsertRunSourceLink(tx, db.RunSourceLink{
		LinkID: id, ProjectID: req.ProjectID, RunID: req.RunID, InstanceID: req.InstanceID,
		SessionID: req.SessionID, TurnID: req.TurnID, Attributed: req.Attributed,
	})
}

// ObserveUsage 写入／推进一轮用量。
//
// cumulative：走重置检测；delta：直接落库。cache_* 原样保留，不并入 input。
func (s *Service) ObserveUsage(tx *sql.Tx, snap UsageSnapshot) (*db.UsageRecord, error) {
	if strings.TrimSpace(snap.TurnID) == "" || strings.TrimSpace(snap.SourceNS) == "" {
		return nil, model.Violation(model.CodePortFieldInvalid, "usage", "source_ns／turn_id 必填")
	}
	if snap.Scope == "" {
		snap.Scope = "model"
	}
	if snap.Quality == "" {
		snap.Quality = model.QualityObserved
	}
	if snap.Semantic == db.UsageCumulative || snap.Semantic == "" {
		base := db.UsageRecord{
			SourceNS: snap.SourceNS, TurnID: snap.TurnID, Scope: snap.Scope,
			Quality: snap.Quality, Basis: snap.Basis, EventID: snap.EventID,
			CacheReadTokens: snap.CacheReadTokens, CacheWriteTokens: snap.CacheWriteTokens,
		}
		rec, err := db.ObserveCumulativeUsage(tx, base, snap.InputTokens, snap.OutputTokens)
		if err != nil {
			return nil, err
		}
		// 累计路径不带 cache；若快照有 cache，补写同 epoch。
		if snap.CacheReadTokens != nil || snap.CacheWriteTokens != nil {
			rec.CacheReadTokens = snap.CacheReadTokens
			rec.CacheWriteTokens = snap.CacheWriteTokens
			if err := db.PutUsageRecord(tx, *rec); err != nil {
				return nil, err
			}
		}
		return rec, nil
	}
	rec := db.UsageRecord{
		UsageID: fmt.Sprintf("usage_%s_%s_d", snap.TurnID, snap.Scope),
		SourceNS: snap.SourceNS, TurnID: snap.TurnID, Epoch: 1, Scope: snap.Scope,
		Semantic: db.UsageDelta, InputTokens: snap.InputTokens, OutputTokens: snap.OutputTokens,
		CacheReadTokens: snap.CacheReadTokens, CacheWriteTokens: snap.CacheWriteTokens,
		Quality: snap.Quality, Basis: snap.Basis, EventID: snap.EventID,
	}
	if snap.InputTokens != nil && snap.OutputTokens != nil {
		sum := *snap.InputTokens + *snap.OutputTokens
		rec.TotalTokens = &sum
	}
	if err := db.PutUsageRecord(tx, rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// ReportTurn 按 turn 去重汇总用量，并可附带分摊结果。
//
// allocations 为空或份额非法 → Unallocated=true，不发明分摊。
// 多条 Run 链接同一 turn 不影响总量（只查 usage_records by turn_id）。
func (s *Service) ReportTurn(tx *sql.Tx, turnID string, allocations []Allocation) (MeterReport, error) {
	out := MeterReport{TurnID: turnID, Quality: model.QualityObserved}
	rows, err := db.ListUsageByTurn(tx, turnID)
	if err != nil {
		return out, err
	}
	if len(rows) == 0 {
		out.Coverage = Coverage{}
		return out, nil
	}
	// 同 scope 取最大 epoch 的记录相加（各 scope 独立）；同一 turn 不因多 Run 翻倍。
	latest := map[string]*db.UsageRecord{}
	for _, r := range rows {
		prev := latest[r.Scope]
		if prev == nil || r.Epoch >= prev.Epoch {
			latest[r.Scope] = r
		}
	}
	var inSum, outSum, crSum, cwSum int64
	var hasIn, hasOut, hasCR, hasCW bool
	unknown := false
	estimated := false
	for _, r := range latest {
		switch r.Quality {
		case model.QualityUnknown:
			unknown = true
		case model.QualityEstimated:
			estimated = true
		}
		if r.InputTokens != nil {
			inSum += *r.InputTokens
			hasIn = true
		}
		if r.OutputTokens != nil {
			outSum += *r.OutputTokens
			hasOut = true
		}
		if r.CacheReadTokens != nil {
			crSum += *r.CacheReadTokens
			hasCR = true
		}
		if r.CacheWriteTokens != nil {
			cwSum += *r.CacheWriteTokens
			hasCW = true
		}
	}
	if hasIn {
		out.InputTokens = &inSum
	}
	if hasOut {
		out.OutputTokens = &outSum
	}
	if hasCR {
		out.CacheReadTokens = &crSum
	}
	if hasCW {
		out.CacheWriteTokens = &cwSum
	}
	if hasIn && hasOut {
		sum := inSum + outSum
		out.TotalTokens = &sum
	}
	switch {
	case unknown:
		out.Quality = model.QualityUnknown
	case estimated:
		out.Quality = model.QualityEstimated
	default:
		out.Quality = model.QualityObserved
	}

	if ok, norm := normalizeAllocations(allocations); ok {
		out.Allocations = norm
		out.Unallocated = false
	} else {
		out.Unallocated = true
		out.Allocations = nil
	}
	out.Coverage = Coverage{ObservedTurns: 1, ObservedDenom: 1, Ratio: 1}
	return out, nil
}

// CoverageOf 计算一批 turn 的观测覆盖率。
//
// observedDenom 是「至少有一条 usage 记录」的 turn 数；从未观测的不进分母。
func (s *Service) CoverageOf(tx *sql.Tx, turnIDs []string) (Coverage, error) {
	seen := map[string]struct{}{}
	observed := 0
	for _, id := range turnIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		rows, err := db.ListUsageByTurn(tx, id)
		if err != nil {
			return Coverage{}, err
		}
		if len(rows) > 0 {
			observed++
		}
	}
	denom := len(seen)
	c := Coverage{ObservedTurns: observed, ObservedDenom: denom}
	if denom > 0 {
		c.Ratio = float64(observed) / float64(denom)
	}
	return c, nil
}

func normalizeAllocations(in []Allocation) (bool, []Allocation) {
	if len(in) == 0 {
		return false, nil
	}
	var sum float64
	out := make([]Allocation, 0, len(in))
	for _, a := range in {
		if strings.TrimSpace(a.InstanceID) == "" || a.Share <= 0 {
			return false, nil
		}
		sum += a.Share
		out = append(out, a)
	}
	if sum < 0.999 || sum > 1.001 {
		return false, nil
	}
	return true, out
}
