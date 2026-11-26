package read

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/kiwh77/maw/internal/query"
)

// taskItem 是任务列表的一行：形状沿用 query.TaskRow，额外带上迭代归属。
// 迭代只是 tasks 上的一列，列表本来就走 query.TaskRow——在这里补字段比再拆一条
// 查询路径更省，也不改变既有字段（内嵌结构体按扁平展开编码）。
type taskItem struct {
	query.TaskRow
	IterationID string `json:"iteration_id"`
}

// iterationFilter 解析按迭代筛选的参数。
//
// 判定依据是「参数出现」而不是「参数非空」：?iteration= 是「未归属任何迭代」这个
// 合法筛选值，不能与「不按迭代筛」混成同一个空字符串。
// 同时接受 iteration 与 iteration_id（快照字段叫 iteration_id，调用方容易照抄）。
func iterationFilter(r *http.Request) (string, bool) {
	q := r.URL.Query()
	for _, key := range []string{"iteration", "iteration_id"} {
		if q.Has(key) {
			return strings.TrimSpace(q.Get(key)), true
		}
	}
	return "", false
}

// loadTaskIterations 取这批实例当前的迭代归属：与列表同一个只读事务，口径一致。
func loadTaskIterations(tx *sql.Tx, projectID string, instanceIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(instanceIDs))
	args := make([]any, 0, len(instanceIDs)+1)
	for _, id := range instanceIDs {
		if strings.TrimSpace(id) != "" {
			args = append(args, id)
		}
	}
	if len(args) == 0 {
		return out, nil
	}
	stmt := `SELECT COALESCE(instance_id, ''), COALESCE(iteration_id, '')
FROM tasks WHERE instance_id IN (?` + strings.Repeat(",?", len(args)-1) + `)`
	if projectID != "" {
		stmt += ` AND project_id = ?`
		args = append(args, projectID)
	}
	rows, err := tx.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var instanceID, iterationID string
		if err := rows.Scan(&instanceID, &iterationID); err != nil {
			return nil, err
		}
		out[instanceID] = iterationID
	}
	return out, rows.Err()
}

// tasksPageBody 返回任务页：每行补 iteration_id；iteration 非 nil 时只保留该迭代的行。
//
// 过滤在页内完成（列表走 query.TaskRow，查询层没有 iteration 条件）：游标仍按原始
// 排序键前进，被筛掉的行不会让下一页漏行；整个页都被筛空时 items=[] 且 next_cursor
// 照旧，调用方继续翻页即可，不会静默丢结果。
func tasksPageBody(tx *sql.Tx, projectID string, page query.Page[query.TaskRow], iteration *string, revision int64) (map[string]any, error) {
	instanceIDs := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		instanceIDs = append(instanceIDs, string(item.InstanceID))
	}
	iterByInstance, err := loadTaskIterations(tx, projectID, instanceIDs)
	if err != nil {
		return nil, err
	}
	items := make([]taskItem, 0, len(page.Items))
	for _, item := range page.Items {
		iterationID := iterByInstance[string(item.InstanceID)]
		if iteration != nil && iterationID != *iteration {
			continue
		}
		items = append(items, taskItem{TaskRow: item, IterationID: iterationID})
	}
	body := pageBodyWith(page, revision, items)
	// 回显筛选：nil 表示没按迭代筛，"" 表示只筛「未归属任何迭代」的任务。
	if iteration == nil {
		body["filter_iteration"] = nil
	} else {
		body["filter_iteration"] = *iteration
	}
	return body, nil
}
