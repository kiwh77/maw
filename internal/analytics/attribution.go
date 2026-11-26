package analytics

import (
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/projection"
)

// 归属统计：按节点、模型、来源和用途查可对账的用量、覆盖率与运行结果。
//
// 三条口径在这里定死（契约 maw.observability.timeline/v1 §4–§5、
// maw.pi.execution/v1 §8–§11）：
//
//  1. 调用级（call）与运行级（run）是两类事实：同一轮只按一种口径入账，
//     call 级有效时 run 级不参与汇总（PI-201 已把被取代的 run 级行标 superseded）。
//  2. 每个比率都把分子分母写出来；分母为 0 时比率为 null，不是 0、也不是 1。
//  3. 缺测就是缺测：工具时间没有独立区间、成本没有价目表，都回 null + 原因，
//     不用 0 冒充，也不按均值补齐。

// Dimension 是归属维度。
type Dimension string

const (
	// DimensionNode 按节点归集：用量行上的 node_id（来自 Run 的节点槽占用）。
	DimensionNode Dimension = "node"
	// DimensionModel 按实际供应商模型归集：用量行上的 model。
	DimensionModel Dimension = "model"
	// DimensionSource 按来源命名空间归集（pi／codebuddy 等）。
	DimensionSource Dimension = "source"
	// DimensionPurpose 按用途归集（worker／lead／diagnose）。
	DimensionPurpose Dimension = "purpose"
)

// Valid 报告维度是否封闭。
func (d Dimension) Valid() bool {
	switch d {
	case DimensionNode, DimensionModel, DimensionSource, DimensionPurpose:
		return true
	}
	return false
}

// column 返回该维度在用量行上的列名；空维度按来源归集。
func (d Dimension) column() string {
	switch d {
	case DimensionNode:
		return "node_id"
	case DimensionModel:
		return "model"
	case DimensionPurpose:
		return "purpose"
	default:
		return "source_ns"
	}
}

// AttributionQuery 在通用范围之上加维度与归属过滤；空的过滤条件不参与筛选。
type AttributionQuery struct {
	Query
	Dimension Dimension
	NodeID    string
	Model     string
	Purpose   string
}

// normalization 把零值补齐成可执行查询。
func (q AttributionQuery) normalization() AttributionQuery {
	if !q.Dimension.Valid() {
		q.Dimension = DimensionSource
	}
	return q
}

// Denominator 是一个比率的分子分母；分母为 0 时 Ratio 为 nil。
//
// 写死分子分母是这份报表存在的理由：读者不必猜「覆盖率」的分母是什么。
type Denominator struct {
	Numerator   int      `json:"numerator"`
	Denominator int      `json:"denominator"`
	Ratio       *float64 `json:"ratio"`
}

// ratioOf 收口比率；零分母给 nil。
func ratioOf(num, den int) Denominator {
	out := Denominator{Numerator: num, Denominator: den}
	if den > 0 {
		r := float64(num) / float64(den)
		out.Ratio = &r
	}
	return out
}

// CostEstimate 是估算成本。
//
// 没有价目表就给 nil 与理由：只报 token，不编单价（供应商账单保持独立证据）。
type CostEstimate struct {
	AmountUSD *float64 `json:"amount_usd"`
	Basis     string   `json:"basis"`
}

// TimeGap 是被测缺口：哪个字段没测到、为什么。
type TimeGap struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// TimeSplit 把四类时间分开归集，不合并成一个「耗时」。
//
// RunSeconds 是 task_phase 与 cli_process 的并集（净执行），WaitSeconds 是显式等待，
// 两者分开报；ToolSeconds 目前没有独立区间来源，回 null 并记缺口。
type TimeSplit struct {
	ModelSeconds *int64    `json:"model_seconds"`
	ToolSeconds  *int64    `json:"tool_seconds"`
	WaitSeconds  *int64    `json:"wait_seconds"`
	RunSeconds   *int64    `json:"run_seconds"`
	Gaps         []TimeGap `json:"gaps,omitempty"`
}

// AttributionRollup 是一个维度取值上的可核对统计。
type AttributionRollup struct {
	Dimension Dimension `json:"dimension"`
	// Value 是维度取值；空字符串表示来源未声明该字段（不是「恰好等于空」）。
	Value string `json:"value"`
	// Scopes 记录这批记账实际用到的口径（call／run），混用会列两个。
	Scopes []string `json:"scopes,omitempty"`

	// 轮次与用量：同一 (turn_id, epoch, scope) 只入账一次。
	Turns           int         `json:"turns"`
	Tokens          TokenTotals `json:"tokens"`
	UsageUnknown    bool        `json:"usage_unknown"`
	EstimatedTurns  int         `json:"estimated_turns"`
	CacheNormalized int         `json:"cache_normalized"`
	CacheUnverified int         `json:"cache_unverified"`

	// 用量观测比：有记账的调用里，真正拿到 token 的比例。
	UsageObserved Denominator `json:"usage_observed"`

	Evidence []string `json:"evidence,omitempty"`
}

// AttributionReport 是整份归属统计。
type AttributionReport struct {
	Scope        Scope                   `json:"scope"`
	GeneratedAt  time.Time               `json:"generated_at"`
	Dimension    Dimension               `json:"dimension"`
	Watermark    Watermark               `json:"watermark"`
	Completeness projection.Completeness `json:"completeness"`

	Rollups []AttributionRollup `json:"rollups"`
	// Totals 是全量合计：分母在这里定义（调用、轮次、Run、交付）。
	Totals AttributionTotals `json:"totals"`
	Time   TimeSplit         `json:"time"`
	Gaps   []TimeGap         `json:"gaps,omitempty"`
}

// AttributionTotals 是分母定义所在：每个比率的分子分母都写明。
type AttributionTotals struct {
	Tokens TokenTotals `json:"tokens"`
	// Calls 是一次运行内的模型调用数（分母来自 run_source_links 的去重轮次）。
	Calls Denominator `json:"calls_with_usage"`
	// Turns 是来源轮次总数与其中有用量证据的轮次（分母来自 source_turns）。
	Turns Denominator `json:"turns_with_usage"`
	// RunSuccess 是 completed / 全部 Run（分母来自 runs）。
	RunSuccess Denominator `json:"run_success"`
	// DeliveryPass 是验证 pass / 全部交付（分母来自 deliveries）。
	DeliveryPass Denominator `json:"delivery_pass"`
	// UsageUnknown 为 true 表示合计里存在未知用量：报表必须显示「部分未知」。
	UsageUnknown bool         `json:"usage_unknown"`
	Cost         CostEstimate `json:"estimated_cost"`
}

// Attribution 生成按维度归集的统计。
//
// intervals 与 Report 用的是同一份时间轴投影：时间分区按同一套并集口径统计，
// 不再另算一套，避免「用量报表」和「时间报表」给出两个不同的时长。
func (s *Service) Attribution(tx *sql.Tx, q AttributionQuery, intervals []projection.Interval,
	cursors projection.Cursors) (*AttributionReport, error) {
	q = q.normalization()
	if !q.Window.Valid() {
		return nil, model.Violation(model.CodePortFieldInvalid, "query.window",
			"窗口必须给出行且 End 晚于 Start")
	}
	loc := orUTC(q.Loc)
	rollups, err := s.attributionRollups(tx, q)
	if err != nil {
		return nil, err
	}
	totals, err := s.attributionTotals(tx, q)
	if err != nil {
		return nil, err
	}
	report := &AttributionReport{
		Scope: Scope{
			ProjectID: q.ProjectID,
			SourceNS:  q.SourceNS,
			Start:     q.Window.Start.UTC(),
			End:       q.Window.End.UTC(),
			Timezone:  loc.String(),
		},
		GeneratedAt: time.Now().UTC(),
		Dimension:   q.Dimension,
		Watermark: Watermark{
			Cursor:         cursors.StateCursor,
			CompleteBefore: cursors.ProjectionCursor,
		},
		Completeness: cursors.Completeness,
		Rollups:      rollups,
		Totals:       totals,
		Time:         splitTimes(intervals, q.Window),
	}
	report.Gaps = append(report.Gaps, report.Time.Gaps...)
	if q.Dimension != DimensionNode {
		// Run 结果只能按节点归集：节点占用链是唯一证据，其它维度不编归属。
		report.Gaps = append(report.Gaps, TimeGap{
			Field:  "runs_by_dimension",
			Reason: "Run 结果只按节点归集：其它维度没有归属证据，分维度留空只报合计",
		})
	}
	if report.Completeness == "" {
		report.Completeness = projection.CompletenessUnknown
	}
	return report, nil
}

// splitTimes 把四类时间分开统计。
func splitTimes(intervals []projection.Interval, w projection.Window) TimeSplit {
	model := unionSeconds(intervals, w, kindMatch(projection.KindTurn))
	wait := unionSeconds(intervals, w, kindMatch(projection.KindExplicitWait))
	run := sumSpans(mergeByIdentity(intervals, w,
		kindMatch(projection.KindTaskPhase, projection.KindCLIProcess)))
	out := TimeSplit{
		ModelSeconds: &model,
		WaitSeconds:  &wait,
		RunSeconds:   &run,
		Gaps: []TimeGap{{
			Field:  "tool_seconds",
			Reason: "工具调用只有 journal 证据，没有独立区间来源：不拿 Run 时间去顶替",
		}},
	}
	return out
}

// attributionRollups 按维度分组聚合有效用量。
//
// 来源维度额外并入「只有 source_turns、没有 usage_records」的轮次：
// Agent 常只报 --turns 省略 token，否则看板归属表会整页空。
func (s *Service) attributionRollups(tx *sql.Tx, q AttributionQuery) ([]AttributionRollup, error) {
	col := q.Dimension.column()
	stmt := `SELECT ` + col + ` AS dim_value,
  COUNT(DISTINCT turn_id || '#' || CAST(epoch AS TEXT) || '#' || scope) AS turns,
  SUM(input_tokens), COUNT(input_tokens),
  SUM(output_tokens), COUNT(output_tokens),
  SUM(cache_read_tokens), COUNT(cache_read_tokens),
  SUM(cache_write_tokens), COUNT(cache_write_tokens),
  SUM(total_tokens), COUNT(total_tokens),
  SUM(CASE WHEN quality = 'estimated' THEN 1 ELSE 0 END),
  SUM(CASE WHEN cache_state = 'verified' THEN 1 ELSE 0 END),
  SUM(CASE WHEN cache_state = 'unverified' THEN 1 ELSE 0 END),
  GROUP_CONCAT(DISTINCT scope)
FROM usage_records
WHERE superseded = 0 AND recorded_at >= ? AND recorded_at < ?` + attributionFilters(q) + `
GROUP BY ` + col + `
ORDER BY ` + col
	args := attributionArgs(q)
	rows, err := tx.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byValue := map[string]AttributionRollup{}
	var order []string
	for rows.Next() {
		var value string
		var turns, inN, outN, cacheReadN, cacheWriteN, totalN, est, verified, unverified int
		var in, outTok, cacheRead, cacheWrite, total sql.NullInt64
		var scopes sql.NullString
		if err := rows.Scan(&value, &turns, &in, &inN, &outTok, &outN, &cacheRead, &cacheReadN,
			&cacheWrite, &cacheWriteN, &total, &totalN, &est, &verified, &unverified, &scopes); err != nil {
			return nil, err
		}
		r := AttributionRollup{
			Dimension: q.Dimension,
			Value:     value,
			Turns:     turns,
			Tokens: TokenTotals{
				InputTokens:      sumOrNil(in, inN),
				OutputTokens:     sumOrNil(outTok, outN),
				CacheReadTokens:  sumOrNil(cacheRead, cacheReadN),
				CacheWriteTokens: sumOrNil(cacheWrite, cacheWriteN),
				TotalTokens:      sumOrNil(total, totalN),
			},
			EstimatedTurns:  est,
			CacheNormalized: verified,
			CacheUnverified: unverified,
			// 有记账的调用：真正拿到 token 的比例（分母是这批发现在案的全部记账）。
			UsageObserved: ratioOf(minInt(inN, outN), turns),
			Scopes:        splitScopes(scopes.String),
		}
		r.UsageUnknown = inN == 0 && outN == 0 && turns > 0
		r.Evidence = []string{
			"dimension:" + string(q.Dimension),
			"scope:" + strings.Join(r.Scopes, "+"),
			"rows:" + itoa(turns),
		}
		byValue[value] = r
		order = append(order, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if q.Dimension == DimensionSource || q.Dimension == "" {
		if err := s.mergeTurnOnlySourceRollups(tx, q, byValue, &order); err != nil {
			return nil, err
		}
	}
	out := make([]AttributionRollup, 0, len(order))
	seen := map[string]bool{}
	for _, v := range order {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, byValue[v])
	}
	return out, nil
}

// mergeTurnOnlySourceRollups 把仅有轮次、无用量记账的来源并入 rollups。
func (s *Service) mergeTurnOnlySourceRollups(tx *sql.Tx, q AttributionQuery, byValue map[string]AttributionRollup, order *[]string) error {
	stmt := `SELECT source_ns, COUNT(DISTINCT turn_id) FROM source_turns
WHERE started_at >= ? AND started_at < ?`
	args := []any{q.Window.Start.UTC().Format(time.RFC3339Nano), q.Window.End.UTC().Format(time.RFC3339Nano)}
	if q.SourceNS != "" {
		stmt += ` AND source_ns = ?`
		args = append(args, q.SourceNS)
	}
	if q.ProjectID != "" {
		// 有工程过滤时：只计挂到该工程 Run 的轮次，或会话归属兜底。
		stmt += ` AND (
  turn_id IN (SELECT DISTINCT turn_id FROM run_source_links WHERE project_id = ? AND turn_id <> '')
  OR session_id IN (SELECT session_id FROM source_sessions WHERE project_id = ?)
)`
		args = append(args, q.ProjectID, q.ProjectID)
	}
	stmt += ` GROUP BY source_ns ORDER BY source_ns`
	rows, err := tx.Query(stmt, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ns string
		var turns int
		if err := rows.Scan(&ns, &turns); err != nil {
			return err
		}
		if turns <= 0 {
			continue
		}
		if existing, ok := byValue[ns]; ok {
			// 用量行已覆盖该来源：轮次分母至少不少于 source_turns。
			if turns > existing.Turns {
				existing.Turns = turns
				existing.UsageUnknown = true
				existing.UsageObserved = ratioOf(existing.UsageObserved.Numerator, turns)
				existing.Evidence = append(existing.Evidence, "source_turns:"+itoa(turns))
				byValue[ns] = existing
			}
			continue
		}
		r := AttributionRollup{
			Dimension:     DimensionSource,
			Value:         ns,
			Turns:         turns,
			UsageUnknown:  true,
			UsageObserved: ratioOf(0, turns),
			Evidence: []string{
				"dimension:source",
				"source_turns:" + itoa(turns),
				"usage_records:0",
			},
		}
		byValue[ns] = r
		*order = append(*order, ns)
	}
	return rows.Err()
}

// attributionFilters 拼归属过滤条件；空的过滤条件不进 SQL。
func attributionFilters(q AttributionQuery) string {
	out := ""
	if q.ProjectID != "" {
		out += ` AND project_id = ?`
	}
	if q.SourceNS != "" {
		out += ` AND source_ns = ?`
	}
	if q.NodeID != "" && q.Dimension != DimensionNode {
		out += ` AND node_id = ?`
	}
	if q.Model != "" && q.Dimension != DimensionModel {
		out += ` AND model = ?`
	}
	if q.Purpose != "" && q.Dimension != DimensionPurpose {
		out += ` AND purpose = ?`
	}
	return out
}

// attributionArgs 与 attributionFilters 一一对应。
func attributionArgs(q AttributionQuery) []any {
	args := []any{
		q.Window.Start.UTC().Format(time.RFC3339Nano),
		q.Window.End.UTC().Format(time.RFC3339Nano),
	}
	if q.ProjectID != "" {
		args = append(args, q.ProjectID)
	}
	if q.SourceNS != "" {
		args = append(args, q.SourceNS)
	}
	if q.NodeID != "" && q.Dimension != DimensionNode {
		args = append(args, q.NodeID)
	}
	if q.Model != "" && q.Dimension != DimensionModel {
		args = append(args, q.Model)
	}
	if q.Purpose != "" && q.Dimension != DimensionPurpose {
		args = append(args, q.Purpose)
	}
	return args
}

// attributionTotals 汇总全量用量并给出四个分母。
func (s *Service) attributionTotals(tx *sql.Tx, q AttributionQuery) (AttributionTotals, error) {
	out := AttributionTotals{Cost: CostEstimate{
		Basis: "未配置价目表：只报 token 与质量，不编单价（供应商账单保持独立证据）",
	}}
	var in, outTok, cacheRead, cacheWrite, total sql.NullInt64
	var inN, outN, cacheReadN, cacheWriteN, totalN int
	row := tx.QueryRow(`SELECT
  SUM(input_tokens), COUNT(input_tokens),
  SUM(output_tokens), COUNT(output_tokens),
  SUM(cache_read_tokens), COUNT(cache_read_tokens),
  SUM(cache_write_tokens), COUNT(cache_write_tokens),
  SUM(total_tokens), COUNT(total_tokens)
FROM usage_records WHERE superseded = 0 AND recorded_at >= ? AND recorded_at < ?`+attributionFilters(q),
		attributionArgs(q)...)
	if err := row.Scan(&in, &inN, &outTok, &outN, &cacheRead, &cacheReadN,
		&cacheWrite, &cacheWriteN, &total, &totalN); err != nil {
		return out, err
	}
	out.Tokens = TokenTotals{
		InputTokens:      sumOrNil(in, inN),
		OutputTokens:     sumOrNil(outTok, outN),
		CacheReadTokens:  sumOrNil(cacheRead, cacheReadN),
		CacheWriteTokens: sumOrNil(cacheWrite, cacheWriteN),
		TotalTokens:      sumOrNil(total, totalN),
	}

	// 调用分母：窗口内建立了来源关联的调用（分母来自关联，不是来自有记账的行）。
	callsTotal, callsWithUsage, err := s.callCounts(tx, q)
	if err != nil {
		return out, err
	}
	out.Calls = ratioOf(callsWithUsage, callsTotal)

	// 轮次分母：窗口内的来源轮次，以及其中有用量记账的轮次。
	turnsTotal, turnsWithUsage, err := s.turnCounts(tx, q)
	if err != nil {
		return out, err
	}
	out.Turns = ratioOf(turnsWithUsage, turnsTotal)

	runsTotal, runsCompleted, err := s.runCounts(tx, q)
	if err != nil {
		return out, err
	}
	out.RunSuccess = ratioOf(runsCompleted, runsTotal)

	deliveriesTotal, deliveriesPassed, err := s.deliveryCounts(tx, q)
	if err != nil {
		return out, err
	}
	out.DeliveryPass = ratioOf(deliveriesPassed, deliveriesTotal)

	out.UsageUnknown = inN == 0 && outN == 0 && turnsWithUsage > 0
	return out, nil
}

// callCounts 返回窗口内的调用数与其中有用量证据的调用数。
//
// 分母来自「Run↔来源关联」而不是记账行：没拿到 token 的调用也是调用，
// 把它从分母里剔掉，覆盖率就会永远好看。节点与用途过滤用关联上的列，
// 来源过滤用轮次上的列；模型只有记账行有，因此对分母的约束以子查询表达。
func (s *Service) callCounts(tx *sql.Tx, q AttributionQuery) (int, int, error) {
	winStart := q.Window.Start.UTC().Format(time.RFC3339Nano)
	winEnd := q.Window.End.UTC().Format(time.RFC3339Nano)
	stmt := `SELECT COUNT(DISTINCT l.turn_id) FROM run_source_links l
JOIN runs r ON r.run_id = l.run_id
JOIN source_turns t ON t.turn_id = l.turn_id
WHERE l.turn_id <> '' AND l.attributed = 1
  AND r.started_at >= ? AND r.started_at < ?`
	args := []any{winStart, winEnd}
	if q.ProjectID != "" {
		stmt += ` AND l.project_id = ?`
		args = append(args, q.ProjectID)
	}
	if q.NodeID != "" {
		stmt += ` AND l.node_id = ?`
		args = append(args, q.NodeID)
	}
	if q.Purpose != "" {
		stmt += ` AND l.purpose = ?`
		args = append(args, q.Purpose)
	}
	if q.SourceNS != "" {
		stmt += ` AND t.source_ns = ?`
		args = append(args, q.SourceNS)
	}
	if q.Model != "" {
		stmt += ` AND EXISTS (SELECT 1 FROM usage_records u WHERE u.turn_id = l.turn_id
  AND u.model = ? AND u.superseded = 0 AND u.recorded_at >= ? AND u.recorded_at < ?)`
		args = append(args, q.Model, winStart, winEnd)
	}
	var total int
	if err := tx.QueryRow(stmt, args...).Scan(&total); err != nil {
		return 0, 0, err
	}
	withUsage, err := s.distinctTurnsWithUsage(tx, q)
	if err != nil {
		return 0, 0, err
	}
	if withUsage > total {
		// 记账里有调用没有关联（历史数据）：分母按实际出现的调用算，不假装更小。
		total = withUsage
	}
	return total, withUsage, nil
}

// turnCounts 返回窗口内的来源轮次数与其中有用量记账的轮次数。
func (s *Service) turnCounts(tx *sql.Tx, q AttributionQuery) (int, int, error) {
	stmt := `SELECT COUNT(DISTINCT turn_id) FROM source_turns
WHERE started_at >= ? AND started_at < ?`
	args := []any{q.Window.Start.UTC().Format(time.RFC3339Nano), q.Window.End.UTC().Format(time.RFC3339Nano)}
	if q.SourceNS != "" {
		stmt += ` AND source_ns = ?`
		args = append(args, q.SourceNS)
	}
	var total int
	if err := tx.QueryRow(stmt, args...).Scan(&total); err != nil {
		return 0, 0, err
	}
	withUsage, err := s.distinctTurnsWithUsage(tx, q)
	if err != nil {
		return 0, 0, err
	}
	return total, withUsage, nil
}

// distinctTurnsWithUsage 是有用量记账的轮次数（按 turn_id 去重）。
func (s *Service) distinctTurnsWithUsage(tx *sql.Tx, q AttributionQuery) (int, error) {
	stmt := `SELECT COUNT(DISTINCT turn_id) FROM usage_records
WHERE superseded = 0 AND recorded_at >= ? AND recorded_at < ?` + attributionFilters(q)
	var n int
	err := tx.QueryRow(stmt, attributionArgs(q)...).Scan(&n)
	return n, err
}

// runCounts 返回窗口内的 Run 数与其中 completed 的数量。
func (s *Service) runCounts(tx *sql.Tx, q AttributionQuery) (int, int, error) {
	stmt := `SELECT COUNT(*), SUM(CASE WHEN phase = 'completed' THEN 1 ELSE 0 END)
FROM runs WHERE started_at >= ? AND started_at < ?`
	args := []any{q.Window.Start.UTC().Format(time.RFC3339Nano), q.Window.End.UTC().Format(time.RFC3339Nano)}
	if q.ProjectID != "" {
		stmt += ` AND project_id = ?`
		args = append(args, q.ProjectID)
	}
	var total int
	var completed sql.NullInt64
	if err := tx.QueryRow(stmt, args...).Scan(&total, &completed); err != nil {
		return 0, 0, err
	}
	return total, int(completed.Int64), nil
}

// deliveryCounts 返回窗口内的交付数与其中验证通过的数量。
func (s *Service) deliveryCounts(tx *sql.Tx, q AttributionQuery) (int, int, error) {
	stmt := `SELECT COUNT(*), SUM(CASE WHEN latest.result = 'pass' THEN 1 ELSE 0 END)
FROM deliveries d
LEFT JOIN (
  SELECT delivery_id, result FROM delivery_verifications v
  WHERE v.ran_at = (SELECT MAX(ran_at) FROM delivery_verifications x WHERE x.delivery_id = v.delivery_id)
) latest ON latest.delivery_id = d.delivery_id
WHERE d.delivered_at >= ? AND d.delivered_at < ? AND d.superseded_by IS NULL`
	args := []any{q.Window.Start.UTC().Format(time.RFC3339Nano), q.Window.End.UTC().Format(time.RFC3339Nano)}
	if q.ProjectID != "" {
		stmt += ` AND d.project_id = ?`
		args = append(args, q.ProjectID)
	}
	var total int
	var passed sql.NullInt64
	if err := tx.QueryRow(stmt, args...).Scan(&total, &passed); err != nil {
		return 0, 0, err
	}
	return total, int(passed.Int64), nil
}

// splitScopes 把 GROUP_CONCAT 的口径拆开并排序。
func splitScopes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// sumOrNil 给可空求和列收口：计数为 0 表示该分量全部未知（nil），不是 0。
func sumOrNil(sum sql.NullInt64, n int) *int64 {
	if n == 0 || !sum.Valid {
		return nil
	}
	v := sum.Int64
	return &v
}
