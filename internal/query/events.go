package query

import (
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// EventQuery 是事件历史的增量拉取参数。
type EventQuery struct {
	ProjectID string
	// Kinds 为空表示不限类型；非空时只返回这些 kind（SQL IN 过滤，不是取回来再筛）。
	Kinds []string
	// Cursor 是上一次返回的 next_cursor；空表示从最早一条开始。
	Cursor string
	Limit  int
}

// EventRow 是一条标准事实的列表投影。
//
// 排序只按入库游标：迟到事件不改游标、不重排已发出的页（可观测性契约 §2／§3）。
type EventRow struct {
	Cursor        int64            `json:"cursor"`
	EventID       model.EventID    `json:"event_id"`
	ProjectID     model.ProjectID  `json:"project_id"`
	RunID         model.RunID      `json:"run_id,omitempty"`
	InstanceID    model.InstanceID `json:"task_instance,omitempty"`
	Kind          string           `json:"kind"`
	Phase         string           `json:"phase,omitempty"`
	LeaseID       string           `json:"lease_id,omitempty"`
	SchemaVersion int              `json:"schema_version"`
	Quality       string           `json:"quality"`
	OccurredAt    string           `json:"occurred_at"`
	ReceivedAt    string           `json:"received_at"`
}

// EventCursor 把一个已知的入库游标包成不透明游标，供「从某条之后继续拉」使用。
//
// 事件游标本身就是自增序号，但对外仍然走不透明串：客户端不许解析或自造游标，
// 否则「跳过几条」这类改动会变成客户端兼容性问题。
func EventCursor(cursor int64) string {
	return encodeCursor(kindEvents, strconv.FormatInt(cursor, 10))
}

// Events 按入库游标严格递增返回事件页。
//
// 游标是 (cursor) 单键，天然唯一，所以不存在同名歧义；但同样先过滤再分页，
// 筛掉的类型不会占掉一页的名额。
func (r *Reader) Events(q EventQuery) (Page[EventRow], error) {
	limit, err := normalizeLimit(q.Limit)
	if err != nil {
		return Page[EventRow]{}, err
	}

	where := []string{"1=1"}
	var args []any
	if q.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, q.ProjectID)
	}
	if len(q.Kinds) > 0 {
		marks := make([]string, 0, len(q.Kinds))
		for _, kind := range q.Kinds {
			kind = strings.TrimSpace(kind)
			if kind == "" {
				continue
			}
			marks = append(marks, "?")
			args = append(args, kind)
		}
		if len(marks) > 0 {
			where = append(where, "kind IN ("+strings.Join(marks, ", ")+")")
		}
	}
	if q.Cursor != "" {
		keys, err := decodeCursor(q.Cursor, kindEvents, 1)
		if err != nil {
			return Page[EventRow]{}, err
		}
		after, err := strconv.ParseInt(keys[0], 10, 64)
		if err != nil {
			return Page[EventRow]{}, invalidPagination("cursor", "events 列表的游标缺少入库游标")
		}
		where = append(where, "cursor > ?")
		args = append(args, after)
	}

	stmt := `SELECT cursor, event_id, project_id, run_id, instance_id, kind, phase, lease_id,
  schema_version, quality, occurred_at, received_at
FROM run_events WHERE ` + strings.Join(where, " AND ") + `
ORDER BY cursor ASC
LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.tx.Query(stmt, args...)
	if err != nil {
		return Page[EventRow]{}, err
	}
	defer rows.Close()

	items := make([]EventRow, 0, limit+1)
	for rows.Next() {
		var row EventRow
		if err := rows.Scan(&row.Cursor, &row.EventID, &row.ProjectID, &row.RunID,
			&row.InstanceID, &row.Kind, &row.Phase, &row.LeaseID, &row.SchemaVersion,
			&row.Quality, &row.OccurredAt, &row.ReceivedAt); err != nil {
			return Page[EventRow]{}, err
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return Page[EventRow]{}, err
	}
	items, next := pageOf(items, limit, kindEvents, func(row EventRow) []string {
		return []string{strconv.FormatInt(row.Cursor, 10)}
	})
	return Page[EventRow]{Items: items, Next: next, Watermark: r.wm}, nil
}
