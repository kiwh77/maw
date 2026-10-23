package query

import (
	"database/sql"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// TaskQuery 是任务列表的过滤与翻页参数；空字段表示不过滤。
type TaskQuery struct {
	ProjectID string
	Status    model.TaskStatus
	Owner     string
	// Limit 省略（0）按 DefaultLimit，允许 1–MaxLimit。
	Limit  int
	Cursor string
}

// TaskRow 是任务列表的一行。
//
// 稳定身份是 InstanceID（永不复用）；DisplayID 只是给人看的编号，
// 同名历史可能指向多个实例，所以两者都带、且都以 InstanceID 为准。
type TaskRow struct {
	ProjectID  model.ProjectID  `json:"project_id"`
	InstanceID model.InstanceID `json:"task_instance"`
	DisplayID  model.DisplayID  `json:"display_id"`
	Title      string           `json:"title"`
	Status     model.TaskStatus `json:"status"`
	Priority   int              `json:"priority"`
	MergeOrder int              `json:"merge_order"`
	Owner      string           `json:"owner,omitempty"`
	Revision   int64            `json:"revision"`
	Branch     string           `json:"branch,omitempty"`
	Worktree   string           `json:"worktree,omitempty"`
	Attempts   int              `json:"attempts"`
	Heartbeat  string           `json:"heartbeat,omitempty"`
}

// Tasks 按 priority, merge_order, display_id 排序返回任务页（HTTP 契约 §2 的任务排序）。
//
// 排序键末尾补 project_id：不带工程过滤时同一 display_id 会出现在多个工程里，
// 只按 display_id 排会让翻页漏记录。
func (r *Reader) Tasks(q TaskQuery) (Page[TaskRow], error) {
	limit, err := normalizeLimit(q.Limit)
	if err != nil {
		return Page[TaskRow]{}, err
	}
	if q.Status != "" && !q.Status.Valid() {
		return Page[TaskRow]{}, unknownEnum("tasks.status", string(q.Status))
	}

	where := []string{"1=1"}
	var args []any
	if q.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, q.ProjectID)
	}
	if q.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(q.Status))
	}
	if q.Owner != "" {
		where = append(where, "COALESCE(owner, '') = ?")
		args = append(args, q.Owner)
	}
	if q.Cursor != "" {
		keys, err := decodeCursor(q.Cursor, kindTasks, 4)
		if err != nil {
			return Page[TaskRow]{}, err
		}
		priority, err := intKey(keys[0], kindTasks)
		if err != nil {
			return Page[TaskRow]{}, err
		}
		mergeOrder, err := intKey(keys[1], kindTasks)
		if err != nil {
			return Page[TaskRow]{}, err
		}
		display, project := keys[2], keys[3]
		// 逐级展开的行值比较：SQLite 对 (a,b) > (?,?) 的支持随版本而变，
		// 展开写法在任何版本上都一样，也便于对照 ORDER BY 读。
		where = append(where, `(priority > ?
      OR (priority = ? AND merge_order > ?)
      OR (priority = ? AND merge_order = ? AND id > ?)
      OR (priority = ? AND merge_order = ? AND id = ? AND project_id > ?))`)
		args = append(args,
			priority,
			priority, mergeOrder,
			priority, mergeOrder, display,
			priority, mergeOrder, display, project)
	}

	stmt := `SELECT project_id, instance_id, id, title, status, priority, merge_order,
  COALESCE(owner, ''), revision, COALESCE(branch, ''), COALESCE(worktree, ''), attempts,
  COALESCE(heartbeat, '')
FROM tasks WHERE ` + strings.Join(where, " AND ") + `
ORDER BY priority ASC, merge_order ASC, id ASC, project_id ASC
LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.tx.Query(stmt, args...)
	if err != nil {
		return Page[TaskRow]{}, err
	}
	defer rows.Close()

	items := make([]TaskRow, 0, limit+1)
	for rows.Next() {
		var (
			row   TaskRow
			inst  sql.NullString
			owner string
		)
		if err := rows.Scan(&row.ProjectID, &inst, &row.DisplayID, &row.Title, &row.Status,
			&row.Priority, &row.MergeOrder, &owner, &row.Revision, &row.Branch, &row.Worktree,
			&row.Attempts, &row.Heartbeat); err != nil {
			return Page[TaskRow]{}, err
		}
		row.InstanceID = model.InstanceID(inst.String)
		row.Owner = owner
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return Page[TaskRow]{}, err
	}
	items, next := pageOf(items, limit, kindTasks, taskKeys)
	return Page[TaskRow]{Items: items, Next: next, Watermark: r.wm}, nil
}

func taskKeys(row TaskRow) []string {
	return []string{itoa(row.Priority), itoa(row.MergeOrder), string(row.DisplayID), string(row.ProjectID)}
}
