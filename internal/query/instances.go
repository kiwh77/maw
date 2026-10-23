package query

import (
	"database/sql"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// InstanceQuery 是任务实例（含已退役）的过滤与翻页参数。
type InstanceQuery struct {
	ProjectID string
	DisplayID string
	// State 取 active／retired；空表示两者都要。
	State  string
	Limit  int
	Cursor string
}

// InstanceRow 是一条实例记录；同一 display_id 可以有多条（退役重建）。
type InstanceRow struct {
	ProjectID     model.ProjectID  `json:"project_id"`
	InstanceID    model.InstanceID `json:"task_instance"`
	DisplayID     model.DisplayID  `json:"display_id"`
	State         string           `json:"state"`
	ActiveVersion int              `json:"active_version,omitempty"`
	CreatedAt     string           `json:"created_at"`
	RetiredAt     string           `json:"retired_at,omitempty"`
	RetiredReason string           `json:"retired_reason,omitempty"`
}

// Instances 返回同名历史的实例页：同一 display_id 的多代实例按创建时间依次列出。
//
// 排序键是 (display_id, created_at, instance_id)，游标也带全这三个字段。
// 只按 display_id 做 `> last` 的游标会在第二页跳过所有同名实例——这正是同名历史
// 最容易漏记录的地方。
func (r *Reader) Instances(q InstanceQuery) (Page[InstanceRow], error) {
	limit, err := normalizeLimit(q.Limit)
	if err != nil {
		return Page[InstanceRow]{}, err
	}
	switch q.State {
	case "", "active", "retired":
	default:
		return Page[InstanceRow]{}, unknownEnum("instances.state", q.State)
	}

	where := []string{"1=1"}
	var args []any
	if q.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, q.ProjectID)
	}
	if q.DisplayID != "" {
		where = append(where, "display_id = ?")
		args = append(args, q.DisplayID)
	}
	if q.State != "" {
		where = append(where, "state = ?")
		args = append(args, q.State)
	}
	if q.Cursor != "" {
		keys, err := decodeCursor(q.Cursor, kindInstances, 3)
		if err != nil {
			return Page[InstanceRow]{}, err
		}
		display, created, instance := keys[0], keys[1], keys[2]
		where = append(where, `(display_id > ?
      OR (display_id = ? AND created_at > ?)
      OR (display_id = ? AND created_at = ? AND instance_id > ?))`)
		args = append(args, display, display, created, display, created, instance)
	}

	stmt := `SELECT project_id, instance_id, display_id, state, active_version, created_at,
  COALESCE(retired_at, ''), COALESCE(retired_reason, '')
FROM task_instances WHERE ` + strings.Join(where, " AND ") + `
ORDER BY display_id ASC, created_at ASC, instance_id ASC
LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.tx.Query(stmt, args...)
	if err != nil {
		return Page[InstanceRow]{}, err
	}
	defer rows.Close()

	items := make([]InstanceRow, 0, limit+1)
	for rows.Next() {
		var (
			row     InstanceRow
			version sql.NullInt64
		)
		if err := rows.Scan(&row.ProjectID, &row.InstanceID, &row.DisplayID, &row.State,
			&version, &row.CreatedAt, &row.RetiredAt, &row.RetiredReason); err != nil {
			return Page[InstanceRow]{}, err
		}
		if version.Valid {
			row.ActiveVersion = int(version.Int64)
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return Page[InstanceRow]{}, err
	}
	items, next := pageOf(items, limit, kindInstances, func(row InstanceRow) []string {
		return []string{string(row.DisplayID), row.CreatedAt, string(row.InstanceID)}
	})
	return Page[InstanceRow]{Items: items, Next: next, Watermark: r.wm}, nil
}
