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
type RunSourceLink struct {
	LinkID     string `json:"link_id"`
	ProjectID  string `json:"project_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	Attributed bool   `json:"attributed"`
	CreatedAt  string `json:"created_at"`
}

// UsageRecord 是一轮用量的一代记账。
//
// Token 指针为 nil 表示未知；不得把未知写成 0。
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
  turn_id, attributed, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		l.LinkID, l.ProjectID, l.RunID, l.InstanceID, l.SessionID, l.TurnID, attr, l.CreatedAt)
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

// PutUsageRecord 写入一代用量。
//
// unknown 质量下 Token 必须全为 nil；cumulative 重置时 ResetObserved=true 且 Epoch 递增。
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
	reset := 0
	if u.ResetObserved {
		reset = 1
	}
	_, err := tx.Exec(`INSERT INTO usage_records(usage_id, source_ns, turn_id, epoch, scope, semantic,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, total_tokens,
  quality, basis, reset_observed, event_id, recorded_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(source_ns, turn_id, epoch, scope) DO UPDATE SET
  usage_id=excluded.usage_id,
  semantic=excluded.semantic,
  input_tokens=excluded.input_tokens, output_tokens=excluded.output_tokens,
  cache_read_tokens=excluded.cache_read_tokens, cache_write_tokens=excluded.cache_write_tokens,
  total_tokens=excluded.total_tokens, quality=excluded.quality, basis=excluded.basis,
  reset_observed=excluded.reset_observed, event_id=excluded.event_id, recorded_at=excluded.recorded_at`,
		u.UsageID, u.SourceNS, u.TurnID, u.Epoch, u.Scope, string(u.Semantic),
		nullInt64(u.InputTokens), nullInt64(u.OutputTokens),
		nullInt64(u.CacheReadTokens), nullInt64(u.CacheWriteTokens), nullInt64(u.TotalTokens),
		string(u.Quality), u.Basis, reset, u.EventID, u.RecordedAt)
	return err
}

// LatestUsage 返回某轮某 scope 最新一代用量。
func LatestUsage(tx *sql.Tx, sourceNS, turnID, scope string) (*UsageRecord, error) {
	row := tx.QueryRow(`SELECT usage_id, source_ns, turn_id, epoch, scope, semantic,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, total_tokens,
  quality, basis, reset_observed, event_id, recorded_at
FROM usage_records WHERE source_ns = ? AND turn_id = ? AND scope = ?
ORDER BY epoch DESC LIMIT 1`, sourceNS, turnID, scope)
	return scanUsage(row)
}

// ListUsageByTurn 列出某轮全部代数（共享轮次计量主键）。
func ListUsageByTurn(tx *sql.Tx, turnID string) ([]*UsageRecord, error) {
	rows, err := tx.Query(`SELECT usage_id, source_ns, turn_id, epoch, scope, semantic,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, total_tokens,
  quality, basis, reset_observed, event_id, recorded_at
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
	var reset int
	var semantic, quality string
	if err := row.Scan(&u.UsageID, &u.SourceNS, &u.TurnID, &u.Epoch, &u.Scope, &semantic,
		&in, &out, &cr, &cw, &total, &quality, &u.Basis, &reset, &u.EventID, &u.RecordedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	u.Semantic = UsageSemantic(semantic)
	u.Quality = model.Quality(quality)
	u.ResetObserved = reset != 0
	u.InputTokens = fromNullInt64(in)
	u.OutputTokens = fromNullInt64(out)
	u.CacheReadTokens = fromNullInt64(cr)
	u.CacheWriteTokens = fromNullInt64(cw)
	u.TotalTokens = fromNullInt64(total)
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
