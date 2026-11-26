package analytics

import (
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// heartbeatFreshnessWindow 与 lead 契约 `maw.lead.selfheal/v1` §2 的
// agentHeartbeatWindow 同值：15 分钟内视为心跳新鲜。
const heartbeatFreshnessWindow = 15 * time.Minute

// RatioMetric 是带分子分母的比率；分母为 0 时 Ratio 为 nil 且 Quality=unknown，
// 表示「无法计量」，绝不填 0（契约 §6 L240）。
type RatioMetric struct {
	Numerator   int64         `json:"numerator"`
	Denominator int64         `json:"denominator"`
	Ratio       *float64      `json:"ratio"`
	Quality     model.Quality `json:"quality"`
	Note        string        `json:"note,omitempty"`
}

// DurationMetric 是耗时分位数；样本为 0 时全部为 nil，
// 样本 < 3 时只给均值并标 unknown（契约 §6 #7/#9）。
type DurationMetric struct {
	Sample      int           `json:"sample"`
	P50Seconds  *float64      `json:"p50_seconds"`
	P95Seconds  *float64      `json:"p95_seconds"`
	MeanSeconds *float64      `json:"mean_seconds"`
	Quality     model.Quality `json:"quality"`
	Note        string        `json:"note,omitempty"`
}

// SystemJobHealth 是自愈作业的分布、成功率、积压与重试/回收。
type SystemJobHealth struct {
	// Total 为 nil 表示窗口内没有任何作业（未采集），而非 0（契约 §6 #1）。
	Total               *int64           `json:"total"`
	ByKind              map[string]int64 `json:"by_kind"`
	ByStatus            map[string]int64 `json:"by_status"`
	SuccessRate         RatioMetric      `json:"success_rate"`
	SuccessRateStrict   RatioMetric      `json:"success_rate_strict"`
	Backlog             int64            `json:"backlog"`
	BacklogOldestSecond *float64         `json:"backlog_oldest_seconds"`
	RetryRate           RatioMetric      `json:"retry_rate"`
	LeaseReclaimRate    RatioMetric      `json:"lease_reclaim_rate"`
	FailureReasons      map[string]int64 `json:"failure_reasons"`
	UnspecifiedFailures int64            `json:"unspecified_failures"`
}

// ReconcileHealth 是 run_reconcile 的结论分布、释放率与 MTTR。
type ReconcileHealth struct {
	Release  int64          `json:"release"`
	Keep     int64          `json:"keep"`
	Escalate int64          `json:"escalate"`
	Unwired  int64          `json:"unwired"`
	Rate     RatioMetric    `json:"rate"`
	MTTR     DurationMetric `json:"mttr"`
}

// DispatchHealth 是派发排队与领取到启动的耗时分位数。
type DispatchHealth struct {
	QueueWait    DurationMetric `json:"queue_wait"`
	ClaimToStart DurationMetric `json:"claim_to_start"`
}

// AgentHealth 是心跳新鲜度与节点成功率。
type AgentHealth struct {
	HeartbeatFreshness RatioMetric `json:"heartbeat_freshness"`
	// NodeSuccessRate 在 v1 恒为 unknown：runs 表没有节点归属字段，
	// 按契约 §6 L242 报未知而不是编造（补归属字段后再给数值）。
	NodeSuccessRate RatioMetric `json:"node_success_rate"`
}

// SystemHealthReport 是 lead 自愈与系统统计的只读报表。
type SystemHealthReport struct {
	Scope       Scope           `json:"scope"`
	GeneratedAt time.Time       `json:"generated_at"`
	Jobs        SystemJobHealth `json:"jobs"`
	Reconcile   ReconcileHealth `json:"reconcile"`
	Dispatch    DispatchHealth  `json:"dispatch"`
	Agents      AgentHealth     `json:"agents"`
	// Evidence 是实际消费的行数，便于把指标追回明细。
	JobRows   int `json:"job_rows"`
	RunRows   int `json:"run_rows"`
	AgentRows int `json:"agent_rows"`
}

// jobRow 是 self-heal 统计所需的作业最小视图。
type jobRow struct {
	Kind       string
	Status     string
	Attempts   int
	CreatedAt  time.Time
	FinishedAt time.Time
	RunID      string
	Result     map[string]any
	Decision   map[string]any
}

// runRow 是 Run 的最小视图；StartedAt/FinishedAt 为空表示未记录。
type runRow struct {
	RunID      string
	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// SystemHealth 按 `maw.lead.selfheal/v1` §6 计算系统健康指标。
// 只读、单快照；时间窗为半开区间 [Start, End)，所有「截至时刻」取窗口 End，
// 这样同一夹具无论何时重算结果都一致。
func (s *Service) SystemHealth(tx *sql.Tx, q Query) (*SystemHealthReport, error) {
	if !q.Window.Valid() {
		return nil, model.Violation(model.CodePortFieldInvalid, "query.window",
			"窗口必须给出行且 End 晚于 Start")
	}
	asOf := q.Window.End.UTC()
	loc := orUTC(q.Loc)
	report := &SystemHealthReport{
		Scope: Scope{
			ProjectID: q.ProjectID,
			Start:     q.Window.Start.UTC(),
			End:       q.Window.End.UTC(),
			Timezone:  loc.String(),
		},
		GeneratedAt: time.Now().UTC(),
	}
	jobs, err := s.loadSystemJobs(tx, q)
	if err != nil {
		return nil, err
	}
	runs, err := s.loadHealthRuns(tx, q, jobs)
	if err != nil {
		return nil, err
	}
	agents, err := s.loadHealthAgents(tx, q)
	if err != nil {
		return nil, err
	}

	report.JobRows = len(jobs)
	report.RunRows = len(runs)
	report.AgentRows = len(agents)

	report.Jobs = summarizeJobs(jobs, asOf)
	report.Reconcile = summarizeReconcile(jobs, runs)
	report.Dispatch = summarizeDispatch(jobs, runs)
	report.Agents = summarizeAgents(agents, asOf)
	return report, nil
}

// loadSystemJobs 读取窗口内创建的作业；窗口按 created_at 半开判定。
func (s *Service) loadSystemJobs(tx *sql.Tx, q Query) ([]jobRow, error) {
	stmt := `SELECT kind, status, attempts, created_at, COALESCE(finished_at,''),
		run_id, result_json, decision_json
		FROM system_jobs WHERE created_at >= ? AND created_at < ?`
	args := []any{
		q.Window.Start.UTC().Format(time.RFC3339Nano),
		q.Window.End.UTC().Format(time.RFC3339Nano),
	}
	if q.ProjectID != "" {
		stmt += ` AND project_id = ?`
		args = append(args, q.ProjectID)
	}
	stmt += ` ORDER BY created_at, job_id`
	rows, err := tx.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []jobRow{}
	for rows.Next() {
		var r jobRow
		var created, finished, resultJSON, decisionJSON string
		if err := rows.Scan(&r.Kind, &r.Status, &r.Attempts, &created, &finished,
			&r.RunID, &resultJSON, &decisionJSON); err != nil {
			return nil, err
		}
		if r.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if finished != "" {
			if r.FinishedAt, err = parseTime(finished); err != nil {
				return nil, err
			}
		}
		r.Result = decodeObject(resultJSON)
		r.Decision = decodeObject(decisionJSON)
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadHealthRuns 读取窗口内的 Run，并补齐被作业引用但创建于窗口外的 Run，
// 供 MTTR 与派发时延使用（两条指标的时间差可以跨窗口）。
func (s *Service) loadHealthRuns(tx *sql.Tx, q Query, jobs []jobRow) (map[string]runRow, error) {
	out := map[string]runRow{}
	stmt := `SELECT run_id, created_at, COALESCE(started_at,''), COALESCE(finished_at,'')
		FROM runs WHERE created_at >= ? AND created_at < ?`
	args := []any{
		q.Window.Start.UTC().Format(time.RFC3339Nano),
		q.Window.End.UTC().Format(time.RFC3339Nano),
	}
	if q.ProjectID != "" {
		stmt += ` AND project_id = ?`
		args = append(args, q.ProjectID)
	}
	rows, err := tx.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	if err := scanRuns(rows, out); err != nil {
		return nil, err
	}
	missing := []string{}
	seen := map[string]struct{}{}
	for _, j := range jobs {
		if j.RunID == "" {
			continue
		}
		if _, ok := out[j.RunID]; ok {
			continue
		}
		if _, ok := seen[j.RunID]; ok {
			continue
		}
		seen[j.RunID] = struct{}{}
		missing = append(missing, j.RunID)
	}
	if len(missing) == 0 {
		return out, nil
	}
	rows, err = tx.Query(`SELECT run_id, created_at, COALESCE(started_at,''),
		COALESCE(finished_at,'') FROM runs WHERE run_id IN (`+placeholders(len(missing))+`)`,
		toAny(missing)...)
	if err != nil {
		return nil, err
	}
	if err := scanRuns(rows, out); err != nil {
		return nil, err
	}
	return out, nil
}

// scanRuns 读取并关闭 runs 结果集。
func scanRuns(rows *sql.Rows, out map[string]runRow) error {
	defer rows.Close()
	for rows.Next() {
		var r runRow
		var created, started, finished string
		if err := rows.Scan(&r.RunID, &created, &started, &finished); err != nil {
			return err
		}
		var err error
		if r.CreatedAt, err = parseTime(created); err != nil {
			return err
		}
		if started != "" {
			if r.StartedAt, err = parseTime(started); err != nil {
				return err
			}
		}
		if finished != "" {
			if r.FinishedAt, err = parseTime(finished); err != nil {
				return err
			}
		}
		out[r.RunID] = r
	}
	return rows.Err()
}

// loadHealthAgents 读取参与心跳统计的 Agent；空工程表示全部工程。
func (s *Service) loadHealthAgents(tx *sql.Tx, q Query) ([]time.Time, error) {
	stmt := `SELECT last_seen FROM agents`
	args := []any{}
	if q.ProjectID != "" {
		stmt += ` WHERE project_id = ?`
		args = append(args, q.ProjectID)
	}
	rows, err := tx.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []time.Time{}
	for rows.Next() {
		var lastSeen string
		if err := rows.Scan(&lastSeen); err != nil {
			return nil, err
		}
		if at, err := parseTime(lastSeen); err == nil {
			out = append(out, at)
		}
	}
	return out, rows.Err()
}

// summarizeJobs 计算作业分布、成功率、积压、重试与回收。
//
// 口径要点：
//   - 成功率分母不含 cancelled（人工取消不是自愈失败）。
//   - attempts 由领取时 +1（db.ClaimNextSystemJob），因此 attempts>0 即「被领取过」，
//     attempts>1 即「重试过」；完成时 locked_at 被清空，不能拿它当领取证据。
//   - 回收只能从 result.reaped_from 还原，没有标记就不计入。
func summarizeJobs(jobs []jobRow, asOf time.Time) SystemJobHealth {
	h := SystemJobHealth{
		ByKind:         map[string]int64{},
		ByStatus:       map[string]int64{},
		FailureReasons: map[string]int64{},
	}
	if len(jobs) == 0 {
		h.SuccessRate = RatioMetric{Quality: model.QualityUnknown, Note: "窗口内无作业"}
		h.SuccessRateStrict = RatioMetric{Quality: model.QualityUnknown, Note: "窗口内无作业"}
		h.RetryRate = RatioMetric{Quality: model.QualityUnknown, Note: "窗口内无作业"}
		h.LeaseReclaimRate = RatioMetric{Quality: model.QualityUnknown, Note: "窗口内无作业"}
		return h
	}
	total := int64(len(jobs))
	h.Total = &total

	var done, failed, escalated, claimed, retried, reclaimed int64
	var oldestQueued time.Time
	for _, j := range jobs {
		h.ByKind[j.Kind]++
		h.ByStatus[j.Status]++
		if j.Attempts > 0 {
			claimed++
			if j.Attempts > 1 {
				retried++
			}
			if reaped, _ := j.Result["reaped_from"].(string); reaped == "running" {
				reclaimed++
			}
		}
		switch j.Status {
		case "done":
			done++
		case "failed":
			failed++
			h.recordFailureReason(j)
		case "escalated":
			escalated++
			h.recordFailureReason(j)
		case "queued":
			h.Backlog++
			if oldestQueued.IsZero() || j.CreatedAt.Before(oldestQueued) {
				oldestQueued = j.CreatedAt
			}
		}
	}
	h.SuccessRate = ratio(done, done+failed+escalated)
	h.SuccessRateStrict = ratio(done, done+failed)
	h.RetryRate = ratio(retried, claimed)
	h.LeaseReclaimRate = ratio(reclaimed, claimed)
	if !oldestQueued.IsZero() && asOf.After(oldestQueued) {
		age := asOf.Sub(oldestQueued).Seconds()
		h.BacklogOldestSecond = &age
	}
	return h
}

// recordFailureReason 先取 result.reason，再退化到 decision.action；
// 两者都缺失时单独记 unspecified，不并入任何具名原因。
func (h *SystemJobHealth) recordFailureReason(j jobRow) {
	if reason, _ := j.Result["reason"].(string); reason != "" {
		h.FailureReasons[reason]++
		return
	}
	if action, _ := j.Decision["action"].(string); action != "" {
		h.FailureReasons[action]++
		return
	}
	h.UnspecifiedFailures++
}

// summarizeReconcile 计算 run_reconcile 的结论分布、释放率与 MTTR。
// MTTR 取被释放 Run 的 finished_at - created_at，而不是作业自身耗时。
func summarizeReconcile(jobs []jobRow, runs map[string]runRow) ReconcileHealth {
	var h ReconcileHealth
	var durations []float64
	for _, j := range jobs {
		if j.Kind != "run_reconcile" {
			continue
		}
		if j.Status != "done" && j.Status != "failed" && j.Status != "escalated" {
			continue
		}
		mode, _ := j.Result["mode"].(string)
		switch mode {
		case "release":
			h.Release++
			if r, ok := runs[j.RunID]; ok {
				if d, ok := spanSeconds(r.CreatedAt, r.FinishedAt); ok {
					durations = append(durations, d)
				}
			}
		case "keep":
			h.Keep++
		case "escalate":
			h.Escalate++
		case "unwired":
			h.Unwired++
		}
	}
	h.Rate = ratio(h.Release, h.Release+h.Keep+h.Escalate)
	h.MTTR = durationStats(durations, "被释放 Run 的 finished_at/created_at")
	return h
}

// summarizeDispatch 计算 dispatch_ready 的排队时延与 Run 的领取到启动时延。
func summarizeDispatch(jobs []jobRow, runs map[string]runRow) DispatchHealth {
	var queue, claim []float64
	for _, j := range jobs {
		if j.Kind != "dispatch_ready" || j.RunID == "" {
			continue
		}
		r, ok := runs[j.RunID]
		if !ok {
			continue
		}
		if d, ok := spanSeconds(j.CreatedAt, r.CreatedAt); ok {
			queue = append(queue, d)
		}
	}
	for _, r := range runs {
		if d, ok := spanSeconds(r.CreatedAt, r.StartedAt); ok {
			claim = append(claim, d)
		}
	}
	return DispatchHealth{
		QueueWait:    durationStats(queue, "dispatch_ready 作业创建到 Run 创建"),
		ClaimToStart: durationStats(claim, "Run 创建到启动"),
	}
}

// summarizeAgents 计算心跳新鲜度；节点成功率在 v1 无证据，固定 unknown。
func summarizeAgents(lastSeen []time.Time, asOf time.Time) AgentHealth {
	fresh := int64(0)
	cutoff := asOf.Add(-heartbeatFreshnessWindow)
	for _, at := range lastSeen {
		if !at.Before(cutoff) {
			fresh++
		}
	}
	return AgentHealth{
		HeartbeatFreshness: ratio(fresh, int64(len(lastSeen))),
		NodeSuccessRate: RatioMetric{
			Quality: model.QualityUnknown,
			Note:    "runs 表没有节点归属字段，v1 不计量",
		},
	}
}

// ratio 生成比率；分母为 0 时返回 unknown，绝不返回 0。
func ratio(numerator, denominator int64) RatioMetric {
	m := RatioMetric{Numerator: numerator, Denominator: denominator}
	if denominator <= 0 {
		m.Quality = model.QualityUnknown
		m.Note = "分母为 0"
		return m
	}
	v := float64(numerator) / float64(denominator)
	m.Ratio = &v
	m.Quality = model.QualityObserved
	return m
}

// durationStats 用最近秩法给分位数；样本 < 3 时只给均值并标 unknown。
func durationStats(values []float64, note string) DurationMetric {
	m := DurationMetric{Sample: len(values), Note: note}
	if len(values) == 0 {
		m.Quality = model.QualityUnknown
		return m
	}
	sorted := append([]float64{}, values...)
	sort.Float64s(sorted)
	sum := 0.0
	for _, v := range sorted {
		sum += v
	}
	mean := sum / float64(len(sorted))
	m.MeanSeconds = &mean
	if len(sorted) < 3 {
		m.Quality = model.QualityUnknown
		m.Note = note + "；样本少于 3，只给均值"
		return m
	}
	p50 := nearestRank(sorted, 0.50)
	p95 := nearestRank(sorted, 0.95)
	m.P50Seconds = &p50
	m.P95Seconds = &p95
	m.Quality = model.QualityObserved
	return m
}

// nearestRank 取最近秩分位数（1-based ceil(p·n)）。
func nearestRank(sorted []float64, p float64) float64 {
	idx := int(math.Ceil(p*float64(len(sorted))) - 1)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// spanSeconds 返回 from→to 的秒数；任一端为空或时钟倒退时返回 false。
func spanSeconds(from, to time.Time) (float64, bool) {
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return 0, false
	}
	return to.Sub(from).Seconds(), true
}

// decodeObject 解析 JSON 对象；坏数据按空对象处理，不中断整张报表。
func decodeObject(raw string) map[string]any {
	if raw == "" {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}

// toAny 把字符串切片转成 sql 参数。
func toAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, v)
	}
	return out
}
