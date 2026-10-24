package analytics

import (
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/projection"
)

// Query 描述一次统计的范围；全部字段都会在报表 Scope 里回显。
type Query struct {
	ProjectID string // 空 = 全部工程
	SourceNS  string // 空 = 全部来源
	Window    projection.Window
	// Loc 用于日切与小时桶；nil 时按 UTC。展示层按契约 §3 传入本地时区。
	Loc *time.Location
}

// TokenTotals 是按轮去重后的 token 汇总；nil 分量表示该分量全部未知。
type TokenTotals struct {
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	CacheReadTokens  *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
	TotalTokens      *int64 `json:"total_tokens,omitempty"`
}

// Coverage 是观测覆盖率；分母为 0 时 Ratio 记 0，不假装完整。
type Coverage struct {
	ObservedTurns int     `json:"observed_turns"`
	ObservedDenom int     `json:"observed_denom"`
	Ratio         float64 `json:"ratio"`
}

// UsageSummary 是一批轮次的去重用量与质量元数据。
type UsageSummary struct {
	TurnCount      int         `json:"turn_count"`
	SharedTurns    int         `json:"shared_turns"`
	UnknownTurns   int         `json:"unknown_turns"`
	EstimatedTurns int         `json:"estimated_turns"`
	Tokens         TokenTotals `json:"tokens"`
	// UsageUnknown 为 true 表示分母里存在未知用量的轮次；报表必须显示「部分未知」。
	UsageUnknown bool     `json:"usage_unknown"`
	Coverage     Coverage `json:"coverage"`
}

// addTurn 并入一轮的去重用量；recs 为该轮全部 scope 的最新 epoch 记账。
//
// 从未观测的轮次（recs 为空）不计入 usage_unknown——缺数据由 Coverage 如实呈现，
// 不与「观测到但未知」混为一谈。
func (u *UsageSummary) addTurn(recs []*db.UsageRecord, shared bool) {
	u.TurnCount++
	if shared {
		u.SharedTurns++
	}
	u.Coverage.ObservedDenom++
	if len(recs) > 0 {
		u.Coverage.ObservedTurns++
	} else {
		return
	}
	var inSum, outSum, crSum, cwSum int64
	var hasIn, hasOut, hasCR, hasCW bool
	unknown, estimated := false, false
	for _, r := range recs {
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
	if estimated {
		u.EstimatedTurns++
	}
	if unknown || !hasIn || !hasOut {
		// 缺任一主分量即视为部分未知：不填 0，不并入总量。
		u.UnknownTurns++
		u.UsageUnknown = true
	}
	if hasIn {
		if u.Tokens.InputTokens == nil {
			v := inSum
			u.Tokens.InputTokens = &v
		} else {
			*u.Tokens.InputTokens += inSum
		}
	}
	if hasOut {
		if u.Tokens.OutputTokens == nil {
			v := outSum
			u.Tokens.OutputTokens = &v
		} else {
			*u.Tokens.OutputTokens += outSum
		}
	}
	if hasCR {
		if u.Tokens.CacheReadTokens == nil {
			v := crSum
			u.Tokens.CacheReadTokens = &v
		} else {
			*u.Tokens.CacheReadTokens += crSum
		}
	}
	if hasCW {
		if u.Tokens.CacheWriteTokens == nil {
			v := cwSum
			u.Tokens.CacheWriteTokens = &v
		} else {
			*u.Tokens.CacheWriteTokens += cwSum
		}
	}
	if hasIn && hasOut {
		if u.Tokens.TotalTokens == nil {
			v := inSum + outSum
			u.Tokens.TotalTokens = &v
		} else {
			*u.Tokens.TotalTokens += inSum + outSum
		}
	}
}

// latestPerScope 与 observability.ReportTurn 同规：同 scope 只取最大 epoch，
// 累计重置的新代际取代旧代际，不跨代相减。
func latestPerScope(recs []*db.UsageRecord) []*db.UsageRecord {
	latest := map[string]*db.UsageRecord{}
	out := make([]*db.UsageRecord, 0, len(recs))
	for _, r := range recs {
		prev := latest[r.Scope]
		if prev == nil {
			latest[r.Scope] = r
			out = append(out, r)
			continue
		}
		if r.Epoch > prev.Epoch {
			*prev = *r
		}
	}
	return out
}

// SourceRollup 是按来源命名空间聚合的用量；搜索过滤后不与其它来源混算。
type SourceRollup struct {
	SourceNS string `json:"source_ns"`
	UsageSummary
}

// DayRollup 是按本地日聚合的用量与交付数；Day 为 YYYY-MM-DD。
type DayRollup struct {
	Day           string `json:"day"`
	DeliveryCount int    `json:"delivery_count"`
	UsageSummary
}

// DeliverySummary 是窗口内交付的计数与身份证据。
type DeliverySummary struct {
	Count      int            `json:"count"`
	Superseded int            `json:"superseded"`
	ByMode     map[string]int `json:"by_mode"`
	// IDs 是去重后的交付身份，用于把计数追到证据。
	IDs []string `json:"ids"`
}

// TimelineSummary 是窗口内时间区间的并集与并发汇总（契约 §6 totals）。
type TimelineSummary struct {
	TaskPhaseSeconds    int64        `json:"task_phase_seconds"`
	CLIProcessSeconds   int64        `json:"cli_process_seconds"`
	TurnSeconds         int64        `json:"turn_seconds"`
	ExplicitWaitSeconds int64        `json:"explicit_wait_seconds"`
	EngineeringSeconds  int64        `json:"engineering_seconds"`
	AvgConcurrency      float64      `json:"avg_concurrency"`
	Hours               []HourBucket `json:"hours,omitempty"`
}

// Watermark 回显投影水位；CompleteBefore < Cursor 表示之后可能有补算。
type Watermark struct {
	Cursor         int64 `json:"cursor"`
	CompleteBefore int64 `json:"complete_before"`
}

// Evidence 把报表指标追到消费明细的数量。
type Evidence struct {
	Intervals    int `json:"intervals"`     // 消费的区间条数（含未闭合）
	Turns        int `json:"turns"`         // 去重后的轮次身份数
	UsageRecords int `json:"usage_records"` // 消费的记账条数
	Deliveries   int `json:"deliveries"`    // 窗口内交付条数
	Identities   int `json:"identities"`    // 贡献时长的不同身份数
}

// Scope 是报表回显的查询范围；子集统计据此说明「不与全集混算」。
type Scope struct {
	ProjectID string    `json:"project_id,omitempty"`
	SourceNS  string    `json:"source_ns,omitempty"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Timezone  string    `json:"timezone"`
}

// Report 是一次统计的完整报表；Watermark/Completeness 缺失视为不可比较（契约 §5）。
type Report struct {
	Scope        Scope                   `json:"scope"`
	GeneratedAt  time.Time               `json:"generated_at"`
	Watermark    Watermark               `json:"watermark"`
	Completeness projection.Completeness `json:"completeness"`
	Timeline     TimelineSummary         `json:"timeline"`
	Usage        UsageSummary            `json:"usage"`
	BySource     []SourceRollup          `json:"by_source,omitempty"`
	ByDay        []DayRollup             `json:"by_day,omitempty"`
	Deliveries   DeliverySummary         `json:"deliveries"`
	Evidence     Evidence                `json:"evidence"`
}

// turnRow 是聚合用的轮次最小视图。
type turnRow struct {
	TurnID    string
	SourceNS  string
	StartedAt time.Time
	Shared    bool
}

// Service 是统计查询用例；只读，不复用内部状态。
type Service struct{}

// projectTurnIDs 若按工程过滤，返回该工程关联的轮次身份集合（run_source_links 优先，
// 会话归属兜底）。空过滤返回 nil 表示不限制。
func (s *Service) projectTurnIDs(tx *sql.Tx, projectID string) (map[string]struct{}, error) {
	if projectID == "" {
		return nil, nil
	}
	ids := map[string]struct{}{}
	rows, err := tx.Query(`SELECT DISTINCT turn_id FROM run_source_links
		WHERE project_id = ? AND turn_id <> ''`, projectID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids[id] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sessRows, err := tx.Query(`SELECT DISTINCT t.turn_id FROM source_turns t
		JOIN source_sessions s ON s.session_id = t.session_id
		WHERE s.project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer sessRows.Close()
	for sessRows.Next() {
		var id string
		if err := sessRows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = struct{}{}
	}
	return ids, sessRows.Err()
}

// loadTurns 读取窗口内开始、且通过工程/来源过滤的轮次。
func (s *Service) loadTurns(tx *sql.Tx, q Query) ([]turnRow, error) {
	ids, err := s.projectTurnIDs(tx, q.ProjectID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT turn_id, source_ns, started_at, shared_with_json
		FROM source_turns WHERE started_at >= ? AND started_at < ?
		ORDER BY started_at, turn_id`,
		q.Window.Start.UTC().Format(time.RFC3339Nano),
		q.Window.End.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []turnRow{}
	for rows.Next() {
		var r turnRow
		var started, sharedJSON string
		if err := rows.Scan(&r.TurnID, &r.SourceNS, &started, &sharedJSON); err != nil {
			return nil, err
		}
		if q.SourceNS != "" && r.SourceNS != q.SourceNS {
			continue
		}
		if ids != nil {
			if _, ok := ids[r.TurnID]; !ok {
				continue
			}
		}
		at, err := parseTime(started)
		if err != nil {
			return nil, err
		}
		r.StartedAt = at
		var shared []string
		if sharedJSON != "" && sharedJSON != "[]" {
			if err := json.Unmarshal([]byte(sharedJSON), &shared); err != nil {
				return nil, err
			}
		}
		r.Shared = len(shared) > 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadUsageByTurn 一次性取回轮次集合的全部记账并按 turn 分组。
func (s *Service) loadUsageByTurn(tx *sql.Tx, turnIDs []string) (map[string][]*db.UsageRecord, int, error) {
	grouped := map[string][]*db.UsageRecord{}
	if len(turnIDs) == 0 {
		return grouped, 0, nil
	}
	// SQLite 变量数上限充足；轮次集合来自同一快照，规模可控。
	stmt := `SELECT source_ns, turn_id, epoch, scope, input_tokens, output_tokens,
		cache_read_tokens, cache_write_tokens, quality
		FROM usage_records WHERE turn_id IN (` + placeholders(len(turnIDs)) + `)`
	args := make([]any, 0, len(turnIDs))
	for _, id := range turnIDs {
		args = append(args, id)
	}
	rows, err := tx.Query(stmt, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		r := &db.UsageRecord{}
		var in, out, cr, cw sql.NullInt64
		if err := rows.Scan(&r.SourceNS, &r.TurnID, &r.Epoch, &r.Scope, &in, &out, &cr, &cw, &r.Quality); err != nil {
			return nil, 0, err
		}
		if in.Valid {
			v := in.Int64
			r.InputTokens = &v
		}
		if out.Valid {
			v := out.Int64
			r.OutputTokens = &v
		}
		if cr.Valid {
			v := cr.Int64
			r.CacheReadTokens = &v
		}
		if cw.Valid {
			v := cw.Int64
			r.CacheWriteTokens = &v
		}
		grouped[r.TurnID] = append(grouped[r.TurnID], r)
		count++
	}
	return grouped, count, rows.Err()
}

func placeholders(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += ","
		}
		out += "?"
	}
	return out
}

// loadDeliveries 读取窗口内交付并做去重计数；同时按查询时区归日。
func (s *Service) loadDeliveries(tx *sql.Tx, q Query, loc *time.Location) (DeliverySummary, map[string]int, error) {
	summary := DeliverySummary{ByMode: map[string]int{}}
	byDay := map[string]int{}
	stmt := `SELECT delivery_id, mode, superseded_by, delivered_at FROM deliveries
		WHERE delivered_at >= ? AND delivered_at < ?`
	args := []any{q.Window.Start.UTC().Format(time.RFC3339Nano), q.Window.End.UTC().Format(time.RFC3339Nano)}
	if q.ProjectID != "" {
		stmt += ` AND project_id = ?`
		args = append(args, q.ProjectID)
	}
	stmt += ` ORDER BY delivered_at, delivery_id`
	rows, err := tx.Query(stmt, args...)
	if err != nil {
		return summary, byDay, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, mode, deliveredAt string
		var supersededBy sql.NullString
		if err := rows.Scan(&id, &mode, &supersededBy, &deliveredAt); err != nil {
			return summary, byDay, err
		}
		summary.Count++
		summary.ByMode[mode]++
		summary.IDs = append(summary.IDs, id)
		if supersededBy.Valid && supersededBy.String != "" {
			summary.Superseded++
		}
		if at, perr := parseTime(deliveredAt); perr == nil {
			byDay[at.In(orUTC(loc)).Format("2006-01-02")]++
		}
	}
	return summary, byDay, rows.Err()
}

// summarizeUsage 把一批轮次聚合成去重用量，并同时按来源与本地日分组。
func (s *Service) summarizeUsage(turns []turnRow, usage map[string][]*db.UsageRecord, loc *time.Location,
	bySource map[string]*SourceRollup, byDay map[string]*DayRollup) UsageSummary {
	loc = orUTC(loc)
	var total UsageSummary
	for _, t := range turns {
		recs := latestPerScope(usage[t.TurnID])
		total.addTurn(recs, t.Shared)
		if bySource != nil {
			sr := bySource[t.SourceNS]
			if sr == nil {
				sr = &SourceRollup{SourceNS: t.SourceNS}
				bySource[t.SourceNS] = sr
			}
			sr.addTurn(recs, t.Shared)
		}
		if byDay != nil {
			day := t.StartedAt.In(loc).Format("2006-01-02")
			dr := byDay[day]
			if dr == nil {
				dr = &DayRollup{Day: day}
				byDay[day] = dr
			}
			dr.addTurn(recs, t.Shared)
		}
	}
	finalizeCoverage(&total)
	for _, sr := range bySource {
		finalizeCoverage(&sr.UsageSummary)
	}
	for _, dr := range byDay {
		finalizeCoverage(&dr.UsageSummary)
	}
	return total
}

// finalizeCoverage 收口覆盖率；零分母记 0，不假装完整。
func finalizeCoverage(u *UsageSummary) {
	if u.Coverage.ObservedDenom > 0 {
		u.Coverage.Ratio = float64(u.Coverage.ObservedTurns) / float64(u.Coverage.ObservedDenom)
	}
}

func orUTC(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	return time.Parse(time.RFC3339, s)
}

// Report 汇总一次统计；窗口必须有效，区间与水位由调用方从投影层传入。
//
// intervals 允许为 nil（纯用量报表）；cursors 为零值时完整性记 unknown。
func (s *Service) Report(tx *sql.Tx, q Query, intervals []projection.Interval, cursors projection.Cursors) (*Report, error) {
	if !q.Window.Valid() {
		return nil, model.Violation(model.CodePortFieldInvalid, "query.window",
			"窗口必须给出行且 End 晚于 Start")
	}
	loc := orUTC(q.Loc)
	turns, err := s.loadTurns(tx, q)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(turns))
	for _, t := range turns {
		ids = append(ids, t.TurnID)
	}
	usage, recordCount, err := s.loadUsageByTurn(tx, ids)
	if err != nil {
		return nil, err
	}
	deliveries, deliveryDays, err := s.loadDeliveries(tx, q, loc)
	if err != nil {
		return nil, err
	}

	bySource := map[string]*SourceRollup{}
	byDay := map[string]*DayRollup{}
	usageTotal := s.summarizeUsage(turns, usage, loc, bySource, byDay)
	for day, count := range deliveryDays {
		dr := byDay[day]
		if dr == nil {
			dr = &DayRollup{Day: day}
			byDay[day] = dr
		}
		dr.DeliveryCount = count
	}

	engSpans := mergeByIdentity(intervals, q.Window,
		kindMatch(projection.KindTaskPhase, projection.KindCLIProcess))
	identities := map[string]struct{}{}
	for _, iv := range intervals {
		identities[identity(iv)] = struct{}{}
	}

	timeline := TimelineSummary{
		TaskPhaseSeconds:    unionSeconds(intervals, q.Window, kindMatch(projection.KindTaskPhase)),
		CLIProcessSeconds:   unionSeconds(intervals, q.Window, kindMatch(projection.KindCLIProcess)),
		TurnSeconds:         unionSeconds(intervals, q.Window, kindMatch(projection.KindTurn)),
		ExplicitWaitSeconds: unionSeconds(intervals, q.Window, kindMatch(projection.KindExplicitWait)),
		EngineeringSeconds:  sumSpans(engSpans),
		AvgConcurrency:      concurrency(intervals, q.Window, kindMatch(projection.KindTaskPhase, projection.KindCLIProcess)),
		Hours:               hourBuckets(intervals, q.Window, loc),
	}

	report := &Report{
		Scope: Scope{
			ProjectID: q.ProjectID,
			SourceNS:  q.SourceNS,
			Start:     q.Window.Start.UTC(),
			End:       q.Window.End.UTC(),
			Timezone:  loc.String(),
		},
		GeneratedAt: time.Now().UTC(),
		Watermark: Watermark{
			Cursor:         cursors.StateCursor,
			CompleteBefore: cursors.ProjectionCursor,
		},
		Completeness: cursors.Completeness,
		Timeline:     timeline,
		Usage:        usageTotal,
		Deliveries:   deliveries,
		Evidence: Evidence{
			Intervals:    len(intervals),
			Turns:        len(turns),
			UsageRecords: recordCount,
			Deliveries:   deliveries.Count,
			Identities:   len(identities),
		},
	}
	for _, day := range sortedDays(byDay) {
		report.ByDay = append(report.ByDay, *byDay[day])
	}
	for _, ns := range sortedSources(bySource) {
		report.BySource = append(report.BySource, *bySource[ns])
	}
	if report.Completeness == "" {
		report.Completeness = projection.CompletenessUnknown
	}
	return report, nil
}

func sortedDays(m map[string]*DayRollup) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSources(m map[string]*SourceRollup) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
