package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// 来源会话／用量／投影元数据（架构 §10.1；契约 maw.observability.timeline/v1）。
//
// 边界：
//  1. 会话与轮次在来源命名空间内唯一；共享轮次只有一条 turn，不按任务复制。
//  2. usage 区分 cumulative／delta，累计重置升 epoch；Token 可空，unknown 不得写 0。
//  3. projection_state 与 notification_deliveries 独立于业务任务状态。

// UsageSemantic 是用量记账语义。
type UsageSemantic string

const (
	UsageCumulative UsageSemantic = "cumulative"
	UsageDelta      UsageSemantic = "delta"
)

// Valid 报告语义是否封闭。
func (s UsageSemantic) Valid() bool {
	return s == UsageCumulative || s == UsageDelta
}

// SourceSession 是来源侧会话。
type SourceSession struct {
	SessionID  string         `json:"session_id"`
	ProjectID  string         `json:"project_id,omitempty"`
	SourceNS   string         `json:"source_ns"`
	ExternalID string         `json:"external_id"`
	Adapter    string         `json:"adapter,omitempty"`
	OpenedAt   string         `json:"opened_at"`
	ClosedAt   string         `json:"closed_at,omitempty"`
	Meta       map[string]any `json:"meta,omitempty"`
}

// SourceTurn 是来源侧一轮对话；SharedWith 列出同轮关联的其它任务实例（可空）。
type SourceTurn struct {
	TurnID     string         `json:"turn_id"`
	SourceNS   string         `json:"source_ns"`
	SessionID  string         `json:"session_id"`
	ExternalID string         `json:"external_id,omitempty"`
	StartedAt  string         `json:"started_at"`
	EndedAt    string         `json:"ended_at,omitempty"`
	SharedWith []string       `json:"shared_with,omitempty"`
	Meta       map[string]any `json:"meta,omitempty"`
}

// RunSourceLink 把 Run／任务实例与来源会话／轮次显式关联；Attributed=false 表示未归属。
//
// 节点、代际、调用身份与用途随关联一起记：这样「哪次 Run 的哪一轮属于哪个节点与用途」
// 不必回头从用量行里猜。
type RunSourceLink struct {
	LinkID     string `json:"link_id"`
	ProjectID  string `json:"project_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	Generation int    `json:"generation,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	Attributed bool   `json:"attributed"`
	CreatedAt  string `json:"created_at"`
}

// 用量口径：逐次调用（call）与运行级（run）分开存放，有 call 级时 run 级不参与汇总。
const (
	UsageScopeCall = "call"
	UsageScopeRun  = "run"
)

// 缓存归一状态。
const (
	CacheStateAbsent     = "absent"
	CacheStateVerified   = "verified"
	CacheStateUnverified = "unverified"
)

// CacheStateValid 报告缓存归一状态是否封闭。
func CacheStateValid(v string) bool {
	switch v {
	case CacheStateAbsent, CacheStateVerified, CacheStateUnverified:
		return true
	}
	return false
}

// UsageRecord 是一轮用量的一代记账。
//
// Token 指针为 nil 表示未知；不得把未知写成 0。
//
// 归属字段（ProjectID／NodeID／RunID／InstanceID／Generation／CallID／Purpose）由 maw 绑定；
// 来源字段（Provider／Model／Raw）是来源自报的实际供应商模型与原始字段，缺就留空（未知）。
type UsageRecord struct {
	UsageID          string        `json:"usage_id"`
	SourceNS         string        `json:"source_ns"`
	TurnID           string        `json:"turn_id"`
	Epoch            int           `json:"epoch"`
	Scope            string        `json:"scope"`
	Semantic         UsageSemantic `json:"semantic"`
	InputTokens      *int64        `json:"input_tokens"`
	OutputTokens     *int64        `json:"output_tokens"`
	CacheReadTokens  *int64        `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64        `json:"cache_write_tokens,omitempty"`
	TotalTokens      *int64        `json:"total_tokens,omitempty"`
	Quality          model.Quality `json:"quality"`
	Basis            string        `json:"basis,omitempty"`
	ResetObserved    bool          `json:"reset_observed"`
	EventID          string        `json:"event_id,omitempty"`
	RecordedAt       string        `json:"recorded_at"`

	// 归属：项目、节点、Run、代际、会话、任务实例、模型调用与用途。
	ProjectID  string `json:"project_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	Generation int    `json:"generation,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	Purpose    string `json:"purpose,omitempty"`
	// Provider／Model 是来源自报的实际供应商与模型；空表示来源没说，不是「没有」。
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// CacheState 是缓存归一状态；CacheEvidence 指向归一证据（缺证据时要写明缺什么）。
	CacheState    string `json:"cache_state"`
	CacheEvidence string `json:"cache_evidence,omitempty"`
	// Baseline 标记运行基线：没有任何调用事实时的兜底痕迹，不是调用事实。
	Baseline bool `json:"baseline"`
	// Superseded 标记被 call 级证据取代的 run 级汇总：记录保留，但不参与汇总。
	Superseded bool           `json:"superseded"`
	Raw        map[string]any `json:"raw,omitempty"`
}

// ProjectionState 是某个投影的检查点与脏范围。
type ProjectionState struct {
	Projection     string         `json:"projection"`
	ProjectID      string         `json:"project_id,omitempty"`
	Version        int            `json:"version"`
	Cursor         int64          `json:"cursor"`
	CompleteBefore int64          `json:"complete_before"`
	DirtyFrom      *int64         `json:"dirty_from,omitempty"`
	DirtyTo        *int64         `json:"dirty_to,omitempty"`
	UpdatedAt      string         `json:"updated_at"`
	Meta           map[string]any `json:"meta,omitempty"`
}

// NotificationDelivery 是可去重的通知投递记录。
type NotificationDelivery struct {
	NotificationID string         `json:"notification_id"`
	ProjectID      string         `json:"project_id,omitempty"`
	EventID        string         `json:"event_id"`
	Channel        string         `json:"channel"`
	Status         string         `json:"status"`
	CreatedAt      string         `json:"created_at"`
	DeliveredAt    string         `json:"delivered_at,omitempty"`
	AckedAt        string         `json:"acked_at,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
}

// UpsertSourceSession 写入或刷新会话；同命名空间同外部身份唯一。
func UpsertSourceSession(tx *sql.Tx, s SourceSession) error {
	if strings.TrimSpace(s.SessionID) == "" || strings.TrimSpace(s.SourceNS) == "" || strings.TrimSpace(s.ExternalID) == "" {
		return model.Violation(model.CodePortFieldInvalid, "source_session", "session_id／source_ns／external_id 必填")
	}
	if s.OpenedAt == "" {
		s.OpenedAt = UTCNow()
	}
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}
	_, err := tx.Exec(`INSERT INTO source_sessions(session_id, project_id, source_ns, external_id, adapter,
  opened_at, closed_at, meta_json) VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(session_id) DO UPDATE SET
  project_id=excluded.project_id, adapter=excluded.adapter,
  closed_at=excluded.closed_at, meta_json=excluded.meta_json`,
		s.SessionID, s.ProjectID, s.SourceNS, s.ExternalID, s.Adapter,
		s.OpenedAt, nullStr(s.ClosedAt), mustJSON(s.Meta))
	return err
}

// GetSourceSessionByExternal 按来源命名空间与外部身份取会话。
func GetSourceSessionByExternal(tx *sql.Tx, sourceNS, externalID string) (*SourceSession, error) {
	row := tx.QueryRow(`SELECT session_id, project_id, source_ns, external_id, adapter, opened_at,
  COALESCE(closed_at, ''), meta_json FROM source_sessions WHERE source_ns = ? AND external_id = ?`,
		sourceNS, externalID)
	var s SourceSession
	var meta string
	if err := row.Scan(&s.SessionID, &s.ProjectID, &s.SourceNS, &s.ExternalID, &s.Adapter,
		&s.OpenedAt, &s.ClosedAt, &meta); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	s.Meta = parseJSONObj(meta)
	return &s, nil
}

// UpsertSourceTurn 写入轮次；同命名空间 turn_id 唯一，共享只记一条。
func UpsertSourceTurn(tx *sql.Tx, t SourceTurn) error {
	if strings.TrimSpace(t.TurnID) == "" || strings.TrimSpace(t.SourceNS) == "" || strings.TrimSpace(t.SessionID) == "" {
		return model.Violation(model.CodePortFieldInvalid, "source_turn", "turn_id／source_ns／session_id 必填")
	}
	if t.StartedAt == "" {
		t.StartedAt = UTCNow()
	}
	if t.SharedWith == nil {
		t.SharedWith = []string{}
	}
	if t.Meta == nil {
		t.Meta = map[string]any{}
	}
	_, err := tx.Exec(`INSERT INTO source_turns(turn_id, source_ns, session_id, external_id, started_at,
  ended_at, shared_with_json, meta_json) VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(turn_id) DO UPDATE SET
  ended_at=excluded.ended_at, shared_with_json=excluded.shared_with_json, meta_json=excluded.meta_json`,
		t.TurnID, t.SourceNS, t.SessionID, t.ExternalID, t.StartedAt,
		nullStr(t.EndedAt), mustJSON(t.SharedWith), mustJSON(t.Meta))
	return err
}

// GetSourceTurn 按 turn_id 读取。
func GetSourceTurn(tx *sql.Tx, turnID string) (*SourceTurn, error) {
	row := tx.QueryRow(`SELECT turn_id, source_ns, session_id, external_id, started_at,
  COALESCE(ended_at, ''), shared_with_json, meta_json FROM source_turns WHERE turn_id = ?`, turnID)
	var t SourceTurn
	var shared, meta string
	if err := row.Scan(&t.TurnID, &t.SourceNS, &t.SessionID, &t.ExternalID, &t.StartedAt,
		&t.EndedAt, &shared, &meta); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	t.SharedWith = parseJSONArr(shared)
	t.Meta = parseJSONObj(meta)
	return &t, nil
}

// InsertRunSourceLink 写入 Run↔来源关联。
func InsertRunSourceLink(tx *sql.Tx, l RunSourceLink) error {
	if strings.TrimSpace(l.LinkID) == "" {
		return model.Violation(model.CodePortFieldInvalid, "run_source_link", "link_id 必填")
	}
	if l.CreatedAt == "" {
		l.CreatedAt = UTCNow()
	}
	attr := 0
	if l.Attributed {
		attr = 1
	}
	_, err := tx.Exec(`INSERT INTO run_source_links(link_id, project_id, run_id, instance_id, session_id,
  turn_id, attributed, created_at, node_id, generation, call_id, purpose)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		l.LinkID, l.ProjectID, l.RunID, l.InstanceID, l.SessionID, l.TurnID, attr, l.CreatedAt,
		l.NodeID, l.Generation, l.CallID, l.Purpose)
	return err
}

// EnsureRunSourceLink 幂等写 Run↔来源关联。
//
// 重放同一条关联是常态（补交、崩溃恢复都会再来一遍）：重复写不能让整批事务失败。
func EnsureRunSourceLink(tx *sql.Tx, l RunSourceLink) error {
	if strings.TrimSpace(l.LinkID) == "" {
		return model.Violation(model.CodePortFieldInvalid, "run_source_link", "link_id 必填")
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM run_source_links WHERE link_id = ?`, l.LinkID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return InsertRunSourceLink(tx, l)
}

// usageColumns 是用量行读写的列序（读写共用一份，避免顺序漂移）。
const usageColumns = `usage_id, source_ns, turn_id, epoch, scope, semantic,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, total_tokens,
  quality, basis, reset_observed, event_id, recorded_at,
  project_id, node_id, run_id, instance_id, generation, call_id, session_id, purpose,
  provider, model, cache_state, cache_evidence, baseline, superseded, raw_json`

// PutUsageRecord 写入一代用量。
//
// unknown 质量下 Token 必须全为 nil；cumulative 重置时 ResetObserved=true 且 Epoch 递增。
//
// 去重身份是 (source_ns, turn_id, epoch, scope)：重放同一条观测只会更新同一行，
// 不会多记一次（契约 P180）。冲突更新时 baseline／superseded 只升不降——
// 重放一条旧的 run 级记录不该把「已被 call 级取代」的标记洗掉。
func PutUsageRecord(tx *sql.Tx, u UsageRecord) error {
	if strings.TrimSpace(u.UsageID) == "" || strings.TrimSpace(u.SourceNS) == "" ||
		strings.TrimSpace(u.TurnID) == "" || strings.TrimSpace(u.Scope) == "" {
		return model.Violation(model.CodePortFieldInvalid, "usage", "usage_id／source_ns／turn_id／scope 必填")
	}
	if u.Epoch < 1 {
		return model.Violation(model.CodePortFieldInvalid, "usage.epoch", "epoch 从 1 开始")
	}
	if !u.Semantic.Valid() {
		return model.Violationf(model.CodePortFieldInvalid, "usage.semantic",
			"只能是 cumulative／delta，收到 %q", u.Semantic)
	}
	if !u.Quality.Valid() {
		return model.Violationf(model.CodePortFieldInvalid, "usage.quality",
			"只能是 observed／estimated／unknown，收到 %q", u.Quality)
	}
	if !u.PurposeValid() {
		return model.Violationf(model.CodePortFieldInvalid, "usage.purpose",
			"用途只能是 worker／lead／diagnose 或留空，收到 %q", u.Purpose)
	}
	if u.CacheState == "" {
		u.CacheState = CacheStateAbsent
	}
	if !CacheStateValid(u.CacheState) {
		return model.Violationf(model.CodePortFieldInvalid, "usage.cache_state",
			"缓存归一状态只能是 absent／verified／unverified，收到 %q", u.CacheState)
	}
	if u.Quality == model.QualityUnknown {
		if u.InputTokens != nil || u.OutputTokens != nil || u.TotalTokens != nil ||
			u.CacheReadTokens != nil || u.CacheWriteTokens != nil {
			return model.Violation(model.CodePortFieldInvalid, "usage.tokens",
				"quality=unknown 时 Token 必须为 null，不得填 0")
		}
	}
	if (u.InputTokens != nil && *u.InputTokens < 0) ||
		(u.OutputTokens != nil && *u.OutputTokens < 0) {
		return model.Violation(model.CodePortFieldInvalid, "usage.tokens", "Token 不得为负")
	}
	if u.TotalTokens != nil && u.InputTokens != nil && u.OutputTokens != nil {
		if *u.TotalTokens != *u.InputTokens+*u.OutputTokens {
			return model.Violation(model.CodePortFieldInvalid, "usage.total_tokens",
				"total 必须等于 input + output（不含缓存）")
		}
	}
	if u.RecordedAt == "" {
		u.RecordedAt = UTCNow()
	}
	if u.Raw == nil {
		u.Raw = map[string]any{}
	}
	reset, baseline, superseded := 0, 0, 0
	if u.ResetObserved {
		reset = 1
	}
	if u.Baseline {
		baseline = 1
	}
	if u.Superseded {
		superseded = 1
	}
	_, err := tx.Exec(`INSERT INTO usage_records(`+usageColumns+`)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(source_ns, turn_id, epoch, scope) DO UPDATE SET
  usage_id=excluded.usage_id,
  semantic=excluded.semantic,
  input_tokens=excluded.input_tokens, output_tokens=excluded.output_tokens,
  cache_read_tokens=excluded.cache_read_tokens, cache_write_tokens=excluded.cache_write_tokens,
  total_tokens=excluded.total_tokens, quality=excluded.quality, basis=excluded.basis,
  reset_observed=excluded.reset_observed, event_id=excluded.event_id, recorded_at=excluded.recorded_at,
  project_id=excluded.project_id, node_id=excluded.node_id, run_id=excluded.run_id,
  instance_id=excluded.instance_id, generation=excluded.generation, call_id=excluded.call_id,
  session_id=excluded.session_id, purpose=excluded.purpose, provider=excluded.provider,
  model=excluded.model, cache_state=excluded.cache_state, cache_evidence=excluded.cache_evidence,
  baseline=MAX(usage_records.baseline, excluded.baseline),
  superseded=MAX(usage_records.superseded, excluded.superseded),
  raw_json=excluded.raw_json`,
		u.UsageID, u.SourceNS, u.TurnID, u.Epoch, u.Scope, string(u.Semantic),
		nullInt64(u.InputTokens), nullInt64(u.OutputTokens),
		nullInt64(u.CacheReadTokens), nullInt64(u.CacheWriteTokens), nullInt64(u.TotalTokens),
		string(u.Quality), u.Basis, reset, u.EventID, u.RecordedAt,
		u.ProjectID, u.NodeID, u.RunID, u.InstanceID, u.Generation, u.CallID, u.SessionID, u.Purpose,
		u.Provider, u.Model, u.CacheState, u.CacheEvidence, baseline, superseded, mustJSON(u.Raw))
	return err
}

// PurposeValid 报告用量用途是否为空或属于封闭集合（worker／lead／diagnose）。
func (u UsageRecord) PurposeValid() bool {
	switch strings.TrimSpace(u.Purpose) {
	case "", "worker", "lead", "diagnose":
		return true
	}
	return false
}

// NormalizeUsage 按证据归一一条用量记录（契约 §10）。
//
// 只做三件有依据的事：缺 total 时按 input+output 算出来；total 与分量对不上就丢弃；
// 缓存分量没有归一证据时标 unverified，绝不自行拆分或并入 input。
// 归一不删原始字段：记录里的 raw 字段保留来源原样，note 写进 basis。
func NormalizeUsage(u UsageRecord) UsageRecord {
	var notes []string
	if u.CacheState == "" {
		u.CacheState = CacheStateAbsent
	}
	hasCache := u.CacheReadTokens != nil || u.CacheWriteTokens != nil
	if !hasCache {
		u.CacheState = CacheStateAbsent
	} else if u.CacheState == CacheStateAbsent {
		u.CacheState = CacheStateUnverified
		notes = append(notes, "缓存分量缺归一证据：不拆分、不并入 input")
	}
	switch {
	case u.TotalTokens == nil && u.InputTokens != nil && u.OutputTokens != nil:
		sum := *u.InputTokens + *u.OutputTokens
		u.TotalTokens = &sum
	case u.TotalTokens != nil && (u.InputTokens == nil || u.OutputTokens == nil):
		u.TotalTokens = nil
		notes = append(notes, "total 缺 input/output 分量，无法核对，已丢弃")
	case u.TotalTokens != nil && *u.TotalTokens != *u.InputTokens+*u.OutputTokens:
		u.TotalTokens = nil
		notes = append(notes, "来源 total 与 input+output 不一致，已丢弃（不猜数值）")
	}
	if u.Quality == model.QualityUnknown &&
		(u.InputTokens != nil || u.OutputTokens != nil || u.CacheReadTokens != nil ||
			u.CacheWriteTokens != nil || u.TotalTokens != nil) {
		u.InputTokens, u.OutputTokens = nil, nil
		u.CacheReadTokens, u.CacheWriteTokens, u.TotalTokens = nil, nil, nil
		notes = append(notes, "quality=unknown，Token 按契约置空；原始值保留在 raw 字段")
	}
	if !u.Quality.Valid() {
		u.Quality = model.QualityUnknown
		notes = append(notes, "quality 非法，按 unknown 记")
	}
	if !u.Semantic.Valid() {
		u.Semantic = UsageDelta
	}
	if strings.TrimSpace(u.Scope) == "" {
		if strings.TrimSpace(u.CallID) != "" {
			u.Scope = UsageScopeCall
		} else {
			u.Scope = UsageScopeRun
		}
	}
	if !u.PurposeValid() {
		notes = append(notes, "用途不在封闭集合内，按未声明保留")
		u.Purpose = ""
	}
	if len(notes) > 0 {
		u.Basis = strings.TrimSpace(strings.TrimSuffix(u.Basis+sepNotes(notes), "；"))
	}
	return u
}

// sepNotes 把归一说明接进 basis。
func sepNotes(notes []string) string {
	out := ""
	for _, n := range notes {
		out += "；" + n
	}
	return out
}

// SupersedeRunLevelUsage 把一次 Run 的 run 级汇总标为「被 call 级取代」。
//
// 记录不删：run 级事实仍然是真的，只是不再参与汇总（契约 P176）。
func SupersedeRunLevelUsage(tx *sql.Tx, runID string, generation int) (int, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return 0, nil
	}
	q := `UPDATE usage_records SET superseded = 1
WHERE run_id = ? AND scope = ? AND superseded = 0`
	args := []any{runID, UsageScopeRun}
	if generation > 0 {
		q += ` AND generation = ?`
		args = append(args, generation)
	}
	res, err := tx.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// LatestUsage 返回某轮某 scope 最新一代用量。
func LatestUsage(tx *sql.Tx, sourceNS, turnID, scope string) (*UsageRecord, error) {
	row := tx.QueryRow(`SELECT `+usageColumns+`
FROM usage_records WHERE source_ns = ? AND turn_id = ? AND scope = ?
ORDER BY epoch DESC LIMIT 1`, sourceNS, turnID, scope)
	return scanUsage(row)
}

// ListUsageByTurn 列出某轮全部代数（共享轮次计量主键）。
func ListUsageByTurn(tx *sql.Tx, turnID string) ([]*UsageRecord, error) {
	rows, err := tx.Query(`SELECT `+usageColumns+`
FROM usage_records WHERE turn_id = ? ORDER BY epoch ASC, scope ASC`, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UsageRecord
	for rows.Next() {
		u, err := scanUsage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ObserveCumulativeUsage 根据累计快照写增量／重置。
//
// 当前累计小于上一代累计 → 新 epoch，ResetObserved=true，本代按当前累计记 delta。
// 否则在同 epoch 上写 delta = max(0, current - previous)。
func ObserveCumulativeUsage(tx *sql.Tx, base UsageRecord, currentInput, currentOutput *int64) (*UsageRecord, error) {
	if !base.Semantic.Valid() {
		base.Semantic = UsageCumulative
	}
	prev, err := LatestUsage(tx, base.SourceNS, base.TurnID, base.Scope)
	if err != nil {
		return nil, err
	}
	rec := base
	rec.Semantic = UsageCumulative
	if rec.UsageID == "" {
		rec.UsageID = fmt.Sprintf("usage_%s_%s_%d", base.TurnID, base.Scope, 1)
	}
	if prev == nil {
		rec.Epoch = 1
		rec.InputTokens, rec.OutputTokens = currentInput, currentOutput
		if currentInput != nil && currentOutput != nil {
			sum := *currentInput + *currentOutput
			rec.TotalTokens = &sum
		}
		if err := PutUsageRecord(tx, rec); err != nil {
			return nil, err
		}
		return &rec, nil
	}
	rec.Epoch = prev.Epoch
	rec.ResetObserved = false
	if declined(prev.InputTokens, currentInput) || declined(prev.OutputTokens, currentOutput) {
		rec.Epoch = prev.Epoch + 1
		rec.ResetObserved = true
		rec.InputTokens, rec.OutputTokens = currentInput, currentOutput
	} else {
		rec.InputTokens = deltaNonNeg(prev.InputTokens, currentInput)
		rec.OutputTokens = deltaNonNeg(prev.OutputTokens, currentOutput)
		rec.Semantic = UsageDelta
	}
	if rec.InputTokens != nil && rec.OutputTokens != nil {
		sum := *rec.InputTokens + *rec.OutputTokens
		rec.TotalTokens = &sum
	} else {
		rec.TotalTokens = nil
	}
	rec.UsageID = fmt.Sprintf("usage_%s_%s_%d", base.TurnID, base.Scope, rec.Epoch)
	if err := PutUsageRecord(tx, rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func declined(prev, cur *int64) bool {
	if prev == nil || cur == nil {
		return false
	}
	return *cur < *prev
}

func deltaNonNeg(prev, cur *int64) *int64 {
	if cur == nil {
		return nil
	}
	if prev == nil {
		v := *cur
		return &v
	}
	d := *cur - *prev
	if d < 0 {
		d = 0
	}
	return &d
}

// UpsertProjectionState 写入／推进投影检查点。
func UpsertProjectionState(tx *sql.Tx, p ProjectionState) error {
	if strings.TrimSpace(p.Projection) == "" {
		return model.Violation(model.CodePortFieldInvalid, "projection", "projection 名必填")
	}
	if p.Version < 1 {
		p.Version = 1
	}
	if p.UpdatedAt == "" {
		p.UpdatedAt = UTCNow()
	}
	if p.Meta == nil {
		p.Meta = map[string]any{}
	}
	_, err := tx.Exec(`INSERT INTO projection_state(projection, project_id, version, cursor, complete_before,
  dirty_from, dirty_to, updated_at, meta_json) VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT(projection, project_id) DO UPDATE SET
  version=excluded.version, cursor=excluded.cursor, complete_before=excluded.complete_before,
  dirty_from=excluded.dirty_from, dirty_to=excluded.dirty_to, updated_at=excluded.updated_at,
  meta_json=excluded.meta_json`,
		p.Projection, p.ProjectID, p.Version, p.Cursor, p.CompleteBefore,
		nullInt64(p.DirtyFrom), nullInt64(p.DirtyTo), p.UpdatedAt, mustJSON(p.Meta))
	return err
}

// GetProjectionState 读取投影检查点。
func GetProjectionState(tx *sql.Tx, projection, projectID string) (*ProjectionState, error) {
	row := tx.QueryRow(`SELECT projection, project_id, version, cursor, complete_before,
  dirty_from, dirty_to, updated_at, meta_json
FROM projection_state WHERE projection = ? AND project_id = ?`, projection, projectID)
	var p ProjectionState
	var dirtyFrom, dirtyTo sql.NullInt64
	var meta string
	if err := row.Scan(&p.Projection, &p.ProjectID, &p.Version, &p.Cursor, &p.CompleteBefore,
		&dirtyFrom, &dirtyTo, &p.UpdatedAt, &meta); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if dirtyFrom.Valid {
		v := dirtyFrom.Int64
		p.DirtyFrom = &v
	}
	if dirtyTo.Valid {
		v := dirtyTo.Int64
		p.DirtyTo = &v
	}
	p.Meta = parseJSONObj(meta)
	return &p, nil
}

// PutNotificationDelivery 写入通知；同 (event_id, channel) 去重，返回是否新插入。
func PutNotificationDelivery(tx *sql.Tx, n NotificationDelivery) (inserted bool, err error) {
	if strings.TrimSpace(n.NotificationID) == "" || strings.TrimSpace(n.EventID) == "" ||
		strings.TrimSpace(n.Channel) == "" {
		return false, model.Violation(model.CodePortFieldInvalid, "notification",
			"notification_id／event_id／channel 必填")
	}
	switch n.Status {
	case "pending", "delivered", "acked", "failed":
	default:
		return false, model.Violationf(model.CodePortFieldInvalid, "notification.status",
			"非法状态 %q", n.Status)
	}
	if n.CreatedAt == "" {
		n.CreatedAt = UTCNow()
	}
	if n.Meta == nil {
		n.Meta = map[string]any{}
	}
	res, err := tx.Exec(`INSERT INTO notification_deliveries(notification_id, project_id, event_id, channel,
  status, created_at, delivered_at, acked_at, meta_json) VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT(event_id, channel) DO NOTHING`,
		n.NotificationID, n.ProjectID, n.EventID, n.Channel, n.Status, n.CreatedAt,
		nullStr(n.DeliveredAt), nullStr(n.AckedAt), mustJSON(n.Meta))
	if err != nil {
		return false, err
	}
	nRows, _ := res.RowsAffected()
	return nRows > 0, nil
}

// GetNotificationByEventChannel 按去重键读取。
func GetNotificationByEventChannel(tx *sql.Tx, eventID, channel string) (*NotificationDelivery, error) {
	row := tx.QueryRow(`SELECT notification_id, project_id, event_id, channel, status, created_at,
  COALESCE(delivered_at, ''), COALESCE(acked_at, ''), meta_json
FROM notification_deliveries WHERE event_id = ? AND channel = ?`, eventID, channel)
	var n NotificationDelivery
	var meta string
	if err := row.Scan(&n.NotificationID, &n.ProjectID, &n.EventID, &n.Channel, &n.Status,
		&n.CreatedAt, &n.DeliveredAt, &n.AckedAt, &meta); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	n.Meta = parseJSONObj(meta)
	return &n, nil
}

func scanUsage(row interface{ Scan(dest ...any) error }) (*UsageRecord, error) {
	var u UsageRecord
	var in, out, cr, cw, total sql.NullInt64
	var reset, baseline, superseded int
	var semantic, quality, raw string
	if err := row.Scan(&u.UsageID, &u.SourceNS, &u.TurnID, &u.Epoch, &u.Scope, &semantic,
		&in, &out, &cr, &cw, &total, &quality, &u.Basis, &reset, &u.EventID, &u.RecordedAt,
		&u.ProjectID, &u.NodeID, &u.RunID, &u.InstanceID, &u.Generation, &u.CallID, &u.SessionID,
		&u.Purpose, &u.Provider, &u.Model, &u.CacheState, &u.CacheEvidence, &baseline, &superseded,
		&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	u.Semantic = UsageSemantic(semantic)
	u.Quality = model.Quality(quality)
	u.ResetObserved = reset != 0
	u.Baseline = baseline != 0
	u.Superseded = superseded != 0
	u.InputTokens = fromNullInt64(in)
	u.OutputTokens = fromNullInt64(out)
	u.CacheReadTokens = fromNullInt64(cr)
	u.CacheWriteTokens = fromNullInt64(cw)
	u.TotalTokens = fromNullInt64(total)
	u.Raw = parseJSONObj(raw)
	return &u, nil
}

func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func fromNullInt64(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

func parseJSONObj(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// UsageQuery 是按归属查用量事实的过滤条件；零值表示不限。
type UsageQuery struct {
	ProjectID  string
	NodeID     string
	RunID      string
	SessionID  string
	CallID     string
	InstanceID string
	Purpose    string
	Scope      string
	Generation int
	// EffectiveOnly 只要参与汇总的行（不含被取代的 run 级汇总）。
	EffectiveOnly bool
}

// ListUsageFacts 按归属读用量事实，按记录时间升序。
//
// 归属字段都是「有就按它筛」：空的过滤条件不参与，避免把「未声明」当成「等于空串」。
func ListUsageFacts(tx *sql.Tx, q UsageQuery) ([]*UsageRecord, error) {
	where := []string{"1 = 1"}
	var args []any
	add := func(cond string, v any) {
		where = append(where, cond)
		args = append(args, v)
	}
	if v := strings.TrimSpace(q.ProjectID); v != "" {
		add("project_id = ?", v)
	}
	if v := strings.TrimSpace(q.NodeID); v != "" {
		add("node_id = ?", v)
	}
	if v := strings.TrimSpace(q.RunID); v != "" {
		add("run_id = ?", v)
	}
	if v := strings.TrimSpace(q.SessionID); v != "" {
		add("session_id = ?", v)
	}
	if v := strings.TrimSpace(q.CallID); v != "" {
		add("call_id = ?", v)
	}
	if v := strings.TrimSpace(q.InstanceID); v != "" {
		add("instance_id = ?", v)
	}
	if v := strings.TrimSpace(q.Purpose); v != "" {
		add("purpose = ?", v)
	}
	if v := strings.TrimSpace(q.Scope); v != "" {
		add("scope = ?", v)
	}
	if q.Generation > 0 {
		add("generation = ?", q.Generation)
	}
	if q.EffectiveOnly {
		where = append(where, "superseded = 0")
	}
	rows, err := tx.Query(`SELECT `+usageColumns+` FROM usage_records WHERE `+
		strings.Join(where, " AND ")+` ORDER BY recorded_at ASC, turn_id ASC, epoch ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UsageRecord
	for rows.Next() {
		u, err := scanUsage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// RunUsageSummary 是一次 Run 的有效用量与覆盖情况。
//
// 口径优先级：有 call 级证据就只认 call 级（run 级行标 superseded，保留但不参与）；
// 没有 call 级时才用 run 级兜底。Token 为 nil 表示该分量未知，不是 0。
type RunUsageSummary struct {
	RunID      string
	Generation int
	// Scope 是实际采用的口径（call／run）；空表示没有任何有效用量。
	Scope string

	InputTokens      *int64
	OutputTokens     *int64
	CacheReadTokens  *int64
	CacheWriteTokens *int64
	TotalTokens      *int64

	// Turns 是去重后的轮次数（按 turn_id + epoch 计数）。
	Turns int
	// CallsTotal／CallsWithUsage 是这次 Run 的调用数与其中有用量证据的调用数。
	CallsTotal     int
	CallsWithUsage int
	// Coverage 是调用覆盖率；分母为 0 时是 nil（不是 0，契约 §9 第 4 条）。
	Coverage *float64
	// UsageUnknown 表示有效用量里存在未知分量：报表必须显示「部分未知」。
	UsageUnknown bool
	// SupersededRunRows 是被 call 级取代的 run 级行数。
	SupersededRunRows int
}

// SummarizeRunUsage 汇总一次 Run 的有效用量（去重后求和，口径不混算）。
func SummarizeRunUsage(tx *sql.Tx, runID string, generation int) (RunUsageSummary, error) {
	out := RunUsageSummary{RunID: strings.TrimSpace(runID), Generation: generation}
	if out.RunID == "" {
		return out, nil
	}
	genCond := ""
	args := []any{out.RunID}
	if generation > 0 {
		genCond = " AND generation = ?"
		args = append(args, generation)
	}
	// 先判定口径：有 call 级有效行就只认 call 级。
	var callRows int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM usage_records
WHERE run_id = ?`+genCond+` AND scope = ? AND superseded = 0`, append(args, UsageScopeCall)...).
		Scan(&callRows); err != nil {
		return out, err
	}
	out.Scope = UsageScopeCall
	if callRows == 0 {
		out.Scope = UsageScopeRun
	}
	var superseded int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM usage_records
WHERE run_id = ?`+genCond+` AND scope = ? AND superseded = 1`, append(args, UsageScopeRun)...).
		Scan(&superseded); err != nil {
		return out, err
	}
	out.SupersededRunRows = superseded

	var in, outTok, cacheRead, cacheWrite, total sql.NullInt64
	var inN, outN, cacheReadN, cacheWriteN, totalN int
	var turns int
	row := tx.QueryRow(`SELECT
  SUM(input_tokens), COUNT(input_tokens),
  SUM(output_tokens), COUNT(output_tokens),
  SUM(cache_read_tokens), COUNT(cache_read_tokens),
  SUM(cache_write_tokens), COUNT(cache_write_tokens),
  SUM(total_tokens), COUNT(total_tokens),
  COUNT(DISTINCT turn_id || '#' || CAST(epoch AS TEXT))
FROM usage_records WHERE run_id = ?`+genCond+` AND scope = ? AND superseded = 0`,
		append(args, out.Scope)...)
	if err := row.Scan(&in, &inN, &outTok, &outN, &cacheRead, &cacheReadN,
		&cacheWrite, &cacheWriteN, &total, &totalN, &turns); err != nil {
		return out, err
	}
	out.InputTokens = sumOrNil(in, inN)
	out.OutputTokens = sumOrNil(outTok, outN)
	out.CacheReadTokens = sumOrNil(cacheRead, cacheReadN)
	out.CacheWriteTokens = sumOrNil(cacheWrite, cacheWriteN)
	out.TotalTokens = sumOrNil(total, totalN)
	out.Turns = turns
	out.UsageUnknown = (inN == 0 && outN == 0) && turns > 0

	if err := tx.QueryRow(`SELECT COUNT(DISTINCT turn_id) FROM run_source_links
WHERE run_id = ? AND turn_id <> ''`, out.RunID).Scan(&out.CallsTotal); err != nil {
		return out, err
	}
	if err := tx.QueryRow(`SELECT COUNT(DISTINCT turn_id) FROM usage_records
WHERE run_id = ?`+genCond+` AND scope = ? AND superseded = 0`, append(args, UsageScopeCall)...).
		Scan(&out.CallsWithUsage); err != nil {
		return out, err
	}
	if out.CallsTotal > 0 {
		ratio := float64(out.CallsWithUsage) / float64(out.CallsTotal)
		out.Coverage = &ratio
	}
	return out, nil
}

// sumOrNil 区分「求和为 0」与「没有分量」：计数为 0 时返回 nil。
func sumOrNil(sum sql.NullInt64, n int) *int64 {
	if n == 0 || !sum.Valid {
		return nil
	}
	v := sum.Int64
	return &v
}

// NodeForRun 从 Run 持有的节点槽租约反查节点身份。
//
// 节点归属要有证据：Run 创建时按节点槽占用（resource_key = node:<id>#<slot>）并挂回本 Run，
// 这里顺着这条占有链读回来。没有占用就返回空——未声明比编造一个节点更安全。
func NodeForRun(tx *sql.Tx, runID string) (string, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "", nil
	}
	var key string
	err := tx.QueryRow(`SELECT resource_key FROM resource_leases
WHERE run_id = ? AND kind = ? ORDER BY acquired_at ASC LIMIT 1`, runID, "node_slot").Scan(&key)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	key = strings.TrimPrefix(strings.TrimSpace(key), "node:")
	if i := strings.Index(key, "#"); i >= 0 {
		key = key[:i]
	}
	return strings.TrimSpace(key), nil
}
