package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/notifications"
	meter "github.com/kiwh77/maw/internal/observability"
	"github.com/kiwh77/maw/internal/projection"
	"github.com/kiwh77/maw/internal/query"
)

// 本文件是「薄适配」：把上游用例接到端口上，不重写它们的语义。
//
// 每个实现都只做三件事：把查询参数翻译成上游调用、把结果投影成本包的响应形状、
// 把上游错误原样上抛（由 respond.go 统一映射）。聚合、去重、分摊一律留在上游。

// SourceResolver 把任务实例解析成来源命名空间，供时间轴的来源过滤使用。
//
// 投影只存区间，不存来源；来源关系在 run_source_links／source_turns 里，
// 所以按来源过滤需要这一步查询。没有解析器时不猜来源，直接报不支持。
type SourceResolver interface {
	// SourcesOf 返回 instance_id → source_ns；查不到的实例不出现在结果里。
	SourcesOf(ctx context.Context, instanceIDs []string) (map[string]string, error)
}

// ProjectorTimeline 是 internal/projection 的只读适配。
//
// 投影是内存权威、可全量重建的：本适配不做裁剪以外的任何加工，
// 双水位直接取 Projector.Cursors()，因此「投影落后」能被如实报出。
type ProjectorTimeline struct {
	Projector *projection.Projector
	// Sources 可选；提供后支持 source_ns 过滤。
	Sources SourceResolver
	// Gaps 是采集缺口（升序、互不重叠），由采集侧登记。
	Gaps []Gap
	// Missing 是固定缺测说明（例如某来源没有轮次采集）。
	Missing []Missing
}

// Timeline 返回窗口内裁剪后的区间。
func (p *ProjectorTimeline) Timeline(ctx context.Context, q TimelineQuery) (TimelineData, error) {
	if p == nil || p.Projector == nil {
		return TimelineData{}, &ErrInvalid{Code: codeCapabilityOff, Field: "timeline",
			Message: "时间轴投影未配置"}
	}
	out := TimelineData{
		Cursors: p.Projector.Cursors(),
		Gaps:    p.Gaps,
		Missing: append([]Missing(nil), p.Missing...),
	}
	intervals := p.Projector.Query(q.Window, q.Loc)
	if q.SourceNS == "" {
		out.Intervals = intervals
		return out, nil
	}
	if p.Sources == nil {
		out.Unsupported = []Unsupported{{
			Capability: "timeline.source_filter",
			Reason:     "source_filter_not_configured",
			Detail:     "未配置来源解析器，无法按 source_ns 过滤区间",
		}}
		return out, nil
	}

	seen := map[string]struct{}{}
	ids := make([]string, 0, len(intervals))
	for _, iv := range intervals {
		if iv.InstanceID == "" {
			continue
		}
		if _, ok := seen[iv.InstanceID]; ok {
			continue
		}
		seen[iv.InstanceID] = struct{}{}
		ids = append(ids, iv.InstanceID)
	}
	mapping, err := p.Sources.SourcesOf(ctx, ids)
	if err != nil {
		return TimelineData{}, err
	}
	var unattributed int
	for _, iv := range intervals {
		src, ok := mapping[iv.InstanceID]
		if !ok || src == "" {
			// 没有来源归属的区间既不属于任何来源，也不能默认算进被过滤的来源。
			unattributed++
			continue
		}
		if src == q.SourceNS {
			out.Intervals = append(out.Intervals, iv)
		}
	}
	if unattributed > 0 {
		out.Missing = append(out.Missing, Missing{
			Section: "intervals", Reason: "source_unattributed",
			Detail: fmt.Sprintf("%d 条区间没有来源归属，未计入来源过滤结果", unattributed),
		})
	}
	return out, nil
}

// SQLSourceResolver 从 run_source_links 与来源会话解析实例的来源命名空间。
type SQLSourceResolver struct {
	Store query.Store
}

// SourcesOf 优先用轮次关联，其次用会话关联；同一实例出现多次时取先查到的非空值。
func (r *SQLSourceResolver) SourcesOf(ctx context.Context, instanceIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if r == nil || r.Store == nil || len(instanceIDs) == 0 {
		return out, nil
	}
	ids := append([]string(nil), instanceIDs...)
	sort.Strings(ids)
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	ph := placeholders(len(ids))
	err := r.Store.Read(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT DISTINCT l.instance_id, t.source_ns
FROM run_source_links l JOIN source_turns t ON t.turn_id = l.turn_id
WHERE l.instance_id IN (`+ph+`) AND l.turn_id <> ''`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var instanceID, sourceNS string
			if err := rows.Scan(&instanceID, &sourceNS); err != nil {
				rows.Close()
				return err
			}
			if _, ok := out[instanceID]; !ok && sourceNS != "" {
				out[instanceID] = sourceNS
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		sessRows, err := tx.Query(`SELECT DISTINCT l.instance_id, s.source_ns
FROM run_source_links l JOIN source_sessions s ON s.session_id = l.session_id
WHERE l.instance_id IN (`+ph+`) AND l.session_id <> ''`, args...)
		if err != nil {
			return err
		}
		defer sessRows.Close()
		for sessRows.Next() {
			var instanceID, sourceNS string
			if err := sessRows.Scan(&instanceID, &sourceNS); err != nil {
				return err
			}
			if _, ok := out[instanceID]; !ok && sourceNS != "" {
				out[instanceID] = sourceNS
			}
		}
		return sessRows.Err()
	})
	return out, err
}

// SQLStats 是 internal/analytics 的只读适配。
type SQLStats struct {
	Store query.Store
	// Service 可注入；nil 时用零值（analytics.Service 无状态）。
	Service *analytics.Service
}

// Report 在一个只读快照里产出统计报表。
func (s *SQLStats) Report(ctx context.Context, q StatsQuery, data TimelineData) (*analytics.Report, error) {
	if s == nil || s.Store == nil {
		return nil, &ErrInvalid{Code: codeCapabilityOff, Field: "stats",
			Message: "统计存储未配置"}
	}
	svc := s.Service
	if svc == nil {
		svc = &analytics.Service{}
	}
	var report *analytics.Report
	err := s.Store.Read(func(tx *sql.Tx) error {
		r, err := svc.Report(tx, analytics.Query{
			ProjectID: q.ProjectID,
			SourceNS:  q.SourceNS,
			Window:    q.Window,
			Loc:       q.Loc,
		}, data.Intervals, data.Cursors)
		if err != nil {
			return err
		}
		report = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// defaultMaxTurnsPerSession 是单会话轮次明细上限。
//
// 上限存在的意义是响应大小，不是统计口径：超出时置 TurnsTruncated 并计入缺测，
// 不静默截断成「这个会话只有 50 轮」。
const defaultMaxTurnsPerSession = 50

// SQLSessions 从来源会话／轮次表读取会话明细，用量交给 internal/observability 计量。
type SQLSessions struct {
	Store query.Store
	// Meter 是轮次计量用例；nil 时用零值（无状态）。
	Meter *meter.Service
	// MaxTurnsPerSession 覆盖单会话轮次上限。
	MaxTurnsPerSession int
}

// Sessions 返回窗口内与工程相关的会话与轮次。
func (s *SQLSessions) Sessions(ctx context.Context, q SessionQuery) (SessionPage, error) {
	if s == nil || s.Store == nil {
		return SessionPage{}, &ErrInvalid{Code: codeCapabilityOff, Field: "sessions",
			Message: "会话存储未配置"}
	}
	maxTurns := s.MaxTurnsPerSession
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurnsPerSession
	}
	limit := q.Limit
	if limit <= 0 {
		limit = query.DefaultLimit
	}
	var cursorAt, cursorID string
	if q.Cursor != "" {
		keys, err := decodeCursor(q.Cursor, kindSessions, 2)
		if err != nil {
			return SessionPage{}, err
		}
		cursorAt, cursorID = keys[0], keys[1]
	}

	page := SessionPage{Items: []SessionItem{}}
	err := s.Store.Read(func(tx *sql.Tx) error {
		wm := watermarkFromTx(tx)
		rows, err := tx.Query(`SELECT session_id, project_id, source_ns, external_id, adapter,
  opened_at, COALESCE(closed_at, '')
FROM source_sessions
WHERE (? = '' OR project_id = ?
       OR session_id IN (SELECT session_id FROM run_source_links
                         WHERE project_id = ? AND session_id <> ''))
  AND (? = '' OR source_ns = ?)
  AND opened_at < ?
  AND COALESCE(NULLIF(closed_at, ''), ?) > ?
  AND (opened_at > ? OR (opened_at = ? AND session_id > ?))
ORDER BY opened_at ASC, session_id ASC
LIMIT ?`,
			q.ProjectID, q.ProjectID, q.ProjectID,
			q.SourceNS, q.SourceNS,
			formatStamp(q.Window.End), formatStamp(q.Window.End), formatStamp(q.Window.Start),
			cursorAt, cursorAt, cursorID,
			limit+1)
		if err != nil {
			return err
		}
		var items []SessionItem
		for rows.Next() {
			var it SessionItem
			var closed string
			if err := rows.Scan(&it.SessionID, &it.ProjectID, &it.SourceNS, &it.ExternalID,
				&it.Adapter, &it.OpenedAt, &closed); err != nil {
				rows.Close()
				return err
			}
			it.ClosedAt = closed
			it.Open = closed == ""
			it.Unattributed = it.ProjectID == ""
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		if len(items) > limit {
			items = items[:limit]
			last := items[len(items)-1]
			page.Next = encodeCursor(kindSessions, last.OpenedAt, last.SessionID)
		}
		if len(items) == 0 {
			page.Watermark = wm
			page.Completeness = projection.CompletenessComplete
			return nil
		}

		ids := make([]string, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.SessionID)
		}
		turns, err := loadTurns(tx, ids, q.Window)
		if err != nil {
			return err
		}
		instances, err := loadTurnInstances(tx, turns)
		if err != nil {
			return err
		}

		svc := s.Meter
		if svc == nil {
			svc = &meter.Service{}
		}
		partial := false
		for i := range items {
			it := &items[i]
			it.SpanSeconds = spanSeconds(it.OpenedAt, it.ClosedAt, q.Window)
			if it.Open {
				partial = true
			}
			all := turns[it.SessionID]
			it.TurnCount = len(all)
			if len(all) > maxTurns {
				all = all[:maxTurns]
				it.TurnsTruncated = true
				partial = true
				page.Missing = append(page.Missing, Missing{
					Section: "turns", Reason: "turns_truncated",
					Detail: fmt.Sprintf("会话 %s 的轮次超过 %d 条，明细不完整", it.SessionID, maxTurns),
				})
			}
			for _, t := range all {
				rep, err := svc.ReportTurn(tx, t.TurnID, nil)
				if err != nil {
					return err
				}
				usage := usageFromReport(rep)
				item := TurnItem{
					TurnID:          t.TurnID,
					SessionID:       t.SessionID,
					SourceNS:        t.SourceNS,
					StartedAt:       t.StartedAt,
					EndedAt:         t.EndedAt,
					Open:            t.EndedAt == "",
					DurationSeconds: turnSeconds(t.StartedAt, t.EndedAt, q.Window),
					SharedWith:      t.SharedWith,
					InstanceIDs:     instances[t.TurnID],
					Usage:           usage,
				}
				if item.Open {
					partial = true
				}
				it.Turns = append(it.Turns, item)
				mergeUsage(&it.Usage, usage)
			}
			if it.Turns == nil {
				it.Turns = []TurnItem{}
			}
		}
		page.Items = items
		page.Watermark = wm
		page.Completeness = projection.CompletenessComplete
		if partial {
			page.Completeness = projection.CompletenessPartial
		}
		return nil
	})
	if err != nil {
		return SessionPage{}, err
	}
	if page.Watermark == nil {
		page.Watermark = watermarkFromTx(nil)
	}
	return page, nil
}

// SQLReminders 是 internal/notifications 的读投影与确认适配。
type SQLReminders struct {
	// Store 提供只读快照。
	Store query.Store
	// Writer 提供写事务（确认提醒）；nil 时确认返回不支持。
	Writer notifications.TxRunner
	// Service 是提醒用例；nil 时用 New()。
	Service *notifications.Service
}

// reminderMeta 是 meta_json 里本层要读的字段子集。
//
// 只读不改：写入语义（指纹、尝试序号、恢复标记）仍归 internal/notifications。
type reminderMeta struct {
	Kind              string `json:"kind"`
	Subject           string `json:"subject"`
	State             string `json:"state"`
	Title             string `json:"title"`
	Body              string `json:"body"`
	Attempts          int    `json:"attempts"`
	AmbiguousAttempts int    `json:"ambiguous_attempts"`
	AttemptOpen       bool   `json:"attempt_open"`
	DuplicateRisk     bool   `json:"duplicate_risk"`
}

// Reminders 返回提醒列表；窗口按 created_at 过滤。
func (s *SQLReminders) Reminders(ctx context.Context, q ReminderQuery) (ReminderPage, error) {
	if s == nil || s.Store == nil {
		return ReminderPage{}, &ErrInvalid{Code: codeCapabilityOff, Field: "reminders",
			Message: "提醒存储未配置"}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = query.DefaultLimit
	}
	var cursorAt, cursorID string
	if q.Cursor != "" {
		keys, err := decodeCursor(q.Cursor, kindReminders, 2)
		if err != nil {
			return ReminderPage{}, err
		}
		cursorAt, cursorID = keys[0], keys[1]
	}
	page := ReminderPage{Items: []ReminderItem{}, Counts: map[string]int{}}
	err := s.Store.Read(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT notification_id, project_id, event_id, channel, status,
  created_at, COALESCE(delivered_at, ''), COALESCE(acked_at, ''), meta_json
FROM notification_deliveries
WHERE (? = '' OR project_id = ?)
  AND (? = '' OR status = ?)
  AND (? = '' OR channel = ?)
  AND (? = '' OR json_extract(meta_json, '$.kind') = ?)
  AND created_at >= ? AND created_at < ?
  AND (created_at > ? OR (created_at = ? AND notification_id > ?))
ORDER BY created_at ASC, notification_id ASC
LIMIT ?`,
			q.ProjectID, q.ProjectID,
			q.Status, q.Status,
			q.Channel, q.Channel,
			q.Kind, q.Kind,
			formatStamp(q.Window.Start), formatStamp(q.Window.End),
			cursorAt, cursorAt, cursorID,
			limit+1)
		if err != nil {
			return err
		}
		for rows.Next() {
			var it ReminderItem
			var raw string
			if err := rows.Scan(&it.NotificationID, &it.ProjectID, &it.EventID, &it.Channel,
				&it.Status, &it.CreatedAt, &it.DeliveredAt, &it.AckedAt, &raw); err != nil {
				rows.Close()
				return err
			}
			var m reminderMeta
			if strings.TrimSpace(raw) != "" {
				// 解析失败按空 meta 处理：历史行可能来自更早的写法，
				// 少显示几个字段好过整页 500。
				_ = json.Unmarshal([]byte(raw), &m)
			}
			it.Kind = m.Kind
			it.Subject = m.Subject
			it.State = m.State
			it.Title = m.Title
			it.Body = m.Body
			it.Attempts = m.Attempts
			it.AmbiguousAttempts = m.AmbiguousAttempts
			it.OpenAttempt = m.AttemptOpen
			it.DuplicateRisk = m.DuplicateRisk
			page.Items = append(page.Items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(page.Items) > limit {
			page.Items = page.Items[:limit]
			last := page.Items[len(page.Items)-1]
			page.Next = encodeCursor(kindReminders, last.CreatedAt, last.NotificationID)
		}

		// 角标与分组不受分页影响：它们是「范围内有多少」，不是「这页有多少」。
		countRows, err := tx.Query(`SELECT status, COUNT(*) FROM notification_deliveries
WHERE (? = '' OR project_id = ?) AND created_at >= ? AND created_at < ?
GROUP BY status`,
			q.ProjectID, q.ProjectID, formatStamp(q.Window.Start), formatStamp(q.Window.End))
		if err != nil {
			return err
		}
		for countRows.Next() {
			var status string
			var n int
			if err := countRows.Scan(&status, &n); err != nil {
				countRows.Close()
				return err
			}
			page.Counts[status] = n
			if status != string(notifications.StatusAcked) {
				page.Unhandled += n
			}
		}
		countRows.Close()
		if err := countRows.Err(); err != nil {
			return err
		}
		page.Watermark = watermarkFromTx(tx)
		page.Completeness = projection.CompletenessComplete
		return nil
	})
	if err != nil {
		return ReminderPage{}, err
	}
	return page, nil
}

// Ack 记录确认。归属校验在这里做：提醒不属于本工程时按 404 处理，不泄露存在性。
func (s *SQLReminders) Ack(ctx context.Context, projectID, notificationID string, at time.Time) (AckResult, error) {
	if s == nil || s.Store == nil {
		return AckResult{}, &ErrInvalid{Code: codeCapabilityOff, Field: "reminders",
			Message: "提醒存储未配置"}
	}
	if s.Writer == nil {
		return AckResult{}, &ErrForbidden{Code: codeCapabilityOff, Message: "提醒确认未配置写入口"}
	}
	svc := s.Service
	if svc == nil {
		svc = notifications.New()
	}
	var out AckResult
	err := s.Writer.Write(func(tx *sql.Tx) error {
		var owner string
		err := tx.QueryRow(`SELECT project_id FROM notification_deliveries
WHERE notification_id = ?`, notificationID).Scan(&owner)
		if err == sql.ErrNoRows {
			return &ErrNotFound{Resource: "notification", Message: "提醒不存在"}
		}
		if err != nil {
			return err
		}
		if owner != "" && projectID != "" && owner != projectID {
			return &ErrNotFound{Resource: "notification", Message: "提醒不属于该工程"}
		}
		changed, err := svc.Ack(tx, notificationID, at)
		if err != nil {
			return err
		}
		item, err := readReminder(tx, notificationID)
		if err != nil {
			return err
		}
		out = AckResult{
			Notification:      item,
			Changed:           changed,
			IdempotentReplay:  !changed,
			ChangedTaskStatus: false,
		}
		return nil
	})
	if err != nil {
		return AckResult{}, err
	}
	return out, nil
}

// readReminder 读一条提醒并投影成本层结构。
func readReminder(tx *sql.Tx, id string) (*ReminderItem, error) {
	var it ReminderItem
	var raw string
	err := tx.QueryRow(`SELECT notification_id, project_id, event_id, channel, status,
  created_at, COALESCE(delivered_at, ''), COALESCE(acked_at, ''), meta_json
FROM notification_deliveries WHERE notification_id = ?`, id).Scan(
		&it.NotificationID, &it.ProjectID, &it.EventID, &it.Channel, &it.Status,
		&it.CreatedAt, &it.DeliveredAt, &it.AckedAt, &raw)
	if err == sql.ErrNoRows {
		return nil, &ErrNotFound{Resource: "notification", Message: "提醒不存在"}
	}
	if err != nil {
		return nil, err
	}
	var m reminderMeta
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	it.Kind, it.Subject, it.State = m.Kind, m.Subject, m.State
	it.Title, it.Body = m.Title, m.Body
	it.Attempts, it.AmbiguousAttempts = m.Attempts, m.AmbiguousAttempts
	it.OpenAttempt, it.DuplicateRisk = m.AttemptOpen, m.DuplicateRisk
	return &it, nil
}

// turnRow 是会话下钻用的轮次最小视图。
type turnRow struct {
	TurnID     string
	SourceNS   string
	SessionID  string
	StartedAt  string
	EndedAt    string
	SharedWith []string
}

// loadTurns 批量读取一页会话的轮次，按会话分组并保持时间升序。
func loadTurns(tx *sql.Tx, sessionIDs []string, window projection.Window) (map[string][]turnRow, error) {
	out := map[string][]turnRow{}
	if len(sessionIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(sessionIDs)+2)
	for _, id := range sessionIDs {
		args = append(args, id)
	}
	args = append(args, formatStamp(window.Start), formatStamp(window.End))
	rows, err := tx.Query(`SELECT turn_id, source_ns, session_id, started_at,
  COALESCE(ended_at, ''), shared_with_json
FROM source_turns
WHERE session_id IN (`+placeholders(len(sessionIDs))+`)
  AND started_at >= ? AND started_at < ?
ORDER BY started_at ASC, turn_id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r turnRow
		var sharedJSON string
		if err := rows.Scan(&r.TurnID, &r.SourceNS, &r.SessionID, &r.StartedAt,
			&r.EndedAt, &sharedJSON); err != nil {
			return nil, err
		}
		if strings.TrimSpace(sharedJSON) != "" && sharedJSON != "[]" {
			_ = json.Unmarshal([]byte(sharedJSON), &r.SharedWith)
		}
		out[r.SessionID] = append(out[r.SessionID], r)
	}
	return out, rows.Err()
}

// loadTurnInstances 读取轮次关联的任务实例，供「一轮服务多题」时下钻。
func loadTurnInstances(tx *sql.Tx, turns map[string][]turnRow) (map[string][]string, error) {
	out := map[string][]string{}
	ids := make([]string, 0, len(turns))
	for _, list := range turns {
		for _, t := range list {
			ids = append(ids, t.TurnID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.Query(`SELECT turn_id, instance_id FROM run_source_links
WHERE turn_id IN (`+placeholders(len(ids))+`) AND instance_id <> ''
ORDER BY turn_id, instance_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	for rows.Next() {
		var turnID, instanceID string
		if err := rows.Scan(&turnID, &instanceID); err != nil {
			return nil, err
		}
		key := turnID + "|" + instanceID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out[turnID] = append(out[turnID], instanceID)
	}
	return out, rows.Err()
}

// usageFromReport 把计量结果投影成本层用量；未知保持 nil，不填 0。
func usageFromReport(rep meter.MeterReport) TurnUsage {
	u := TurnUsage{
		InputTokens:      rep.InputTokens,
		OutputTokens:     rep.OutputTokens,
		CacheReadTokens:  rep.CacheReadTokens,
		CacheWriteTokens: rep.CacheWriteTokens,
		TotalTokens:      rep.TotalTokens,
		Quality:          rep.Quality,
		Unallocated:      rep.Unallocated,
	}
	if rep.Quality == model.QualityUnknown || rep.InputTokens == nil || rep.OutputTokens == nil {
		u.UnknownTurns = 1
		// 主分量缺失时质量必须降为 unknown：契约 §4.3 要求「部分未知」可被看见，
		// 而计量用例在「从未观测」时会返回零值质量（observed），不能照抄。
		u.Quality = model.QualityUnknown
	}
	return u
}

// mergeUsage 把一轮用量并入会话累计。
//
// 每个分量独立累计：某轮 input 未知不影响已知轮的 output 相加，
// 但会话级 quality 会降为 unknown，并累计 unknown_turns，让报表能说「部分未知」。
func mergeUsage(dst *TurnUsage, src TurnUsage) {
	dst.InputTokens = addPtr(dst.InputTokens, src.InputTokens)
	dst.OutputTokens = addPtr(dst.OutputTokens, src.OutputTokens)
	dst.CacheReadTokens = addPtr(dst.CacheReadTokens, src.CacheReadTokens)
	dst.CacheWriteTokens = addPtr(dst.CacheWriteTokens, src.CacheWriteTokens)
	dst.TotalTokens = addPtr(dst.TotalTokens, src.TotalTokens)
	dst.UnknownTurns += src.UnknownTurns
	dst.Unallocated = dst.Unallocated || src.Unallocated
	switch {
	case src.Quality == model.QualityUnknown:
		dst.Quality = model.QualityUnknown
	case src.Quality == model.QualityEstimated && dst.Quality != model.QualityUnknown:
		dst.Quality = model.QualityEstimated
	case dst.Quality == "":
		dst.Quality = src.Quality
	}
}

// addPtr 相加两个可空计数：两边都未知才返回 nil。
func addPtr(a, b *int64) *int64 {
	if a == nil && b == nil {
		return nil
	}
	var sum int64
	if a != nil {
		sum += *a
	}
	if b != nil {
		sum += *b
	}
	return &sum
}

// spanSeconds 计算会话跨度；未关闭时算到窗口终点，并由此保证非负。
func spanSeconds(openedAt, closedAt string, window projection.Window) int64 {
	start, err := parseStamp(openedAt)
	if err != nil {
		return 0
	}
	end := window.End
	if closedAt != "" {
		if t, err := parseStamp(closedAt); err == nil {
			end = t
		}
	}
	if !end.After(start) {
		return 0
	}
	return int64(end.Sub(start) / time.Second)
}

// turnSeconds 计算一轮时长；未结束返回 0（不参与已完成时长）。
func turnSeconds(startedAt, endedAt string, window projection.Window) int64 {
	if endedAt == "" {
		return 0
	}
	start, err := parseStamp(startedAt)
	if err != nil {
		return 0
	}
	end, err := parseStamp(endedAt)
	if err != nil || !end.After(start) {
		return 0
	}
	return int64(end.Sub(start) / time.Second)
}

// watermarkFromTx 读取事实水位。会话与提醒是事实直读、不经投影，
// 因此 complete_before 与 cursor 相同——这里不制造「投影落后」的假象。
func watermarkFromTx(tx *sql.Tx) *Watermark {
	wm := &Watermark{ReceivedAt: time.Now().UTC(), Gaps: []Gap{}}
	if tx == nil {
		return wm
	}
	var cursor int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(cursor), 0) FROM run_events`).Scan(&cursor); err != nil {
		return wm
	}
	wm.Cursor = cursor
	wm.CompleteBefore = cursor
	return wm
}

// formatStamp 统一 UTC 定长秒级时间戳：定长串按字典序比较即按时间比较，
// 与 internal/db 的写入口径一致。
func formatStamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// parseStamp 解析 UTC 定长串，兼容带小数与带偏移的历史写法。
func parseStamp(raw string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04:05Z", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间 %q", raw)
}

// placeholders 生成 IN 子句占位符。
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
	}
	return b.String()
}
