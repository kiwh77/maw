package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 迭代状态：与文档 F52 的「规划中／执行中／收尾中／已合入／已放弃」一一对应。
const (
	IterationPlanning  = "planning"
	IterationRunning   = "running"
	IterationFinishing = "finishing"
	IterationMerged    = "merged"
	IterationAbandoned = "abandoned"
)

// AllIterationStatuses 是全部合法迭代状态，顺序即推进顺序。
func AllIterationStatuses() []string {
	return []string{IterationPlanning, IterationRunning, IterationFinishing, IterationMerged, IterationAbandoned}
}

// ValidIterationStatus 报告状态是否属于合法集合。
func ValidIterationStatus(status string) bool {
	for _, s := range AllIterationStatuses() {
		if s == status {
			return true
		}
	}
	return false
}

var (
	// ErrIterationNotFound 表示工程里没有这条迭代。
	ErrIterationNotFound = errors.New("db: 迭代不存在")
	// ErrIterationNotEmpty 表示迭代里还绑着任务，不能单独删除。
	ErrIterationNotEmpty = errors.New("db: 迭代仍有任务")
)

// Iteration 是一次加入的任务列表，也是任务的容器与合入单位。
//
// MergeCommit 在收尾合回目标分支前为空；Extra 预留给契约扩展（如范围声明），
// 避免每加一个字段就再迁移一次。
type Iteration struct {
	ProjectID    string         `json:"project_id"`
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Goal         string         `json:"goal"`
	Status       string         `json:"status"`
	TargetBranch string         `json:"target_branch"`
	MergeCommit  string         `json:"merge_commit,omitempty"`
	CreatedAt    string         `json:"created_at"`
	UpdatedAt    string         `json:"updated_at"`
	Extra        map[string]any `json:"extra"`
}

const iterationCols = `project_id, id, name, goal, status, target_branch, merge_commit, created_at, updated_at, extra_json`

func scanIteration(row interface{ Scan(dest ...any) error }) (*Iteration, error) {
	var it Iteration
	var mergeCommit, extra string
	if err := row.Scan(
		&it.ProjectID, &it.ID, &it.Name, &it.Goal, &it.Status, &it.TargetBranch,
		&mergeCommit, &it.CreatedAt, &it.UpdatedAt, &extra,
	); err != nil {
		return nil, err
	}
	it.MergeCommit = mergeCommit
	it.Extra = map[string]any{}
	_ = json.Unmarshal([]byte(extra), &it.Extra)
	return &it, nil
}

// GetIteration 读取一条迭代；不存在时返回 (nil, nil)。
func GetIteration(tx *sql.Tx, projectID, id string) (*Iteration, error) {
	row := tx.QueryRow(`SELECT `+iterationCols+` FROM iterations WHERE project_id = ? AND id = ?`, projectID, id)
	it, err := scanIteration(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return it, err
}

// ListIterations 按创建时间升序列出迭代；projectID 为空时列出全部工程的迭代。
func ListIterations(tx *sql.Tx, projectID string) ([]*Iteration, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(projectID) == "" {
		rows, err = tx.Query(`SELECT ` + iterationCols + ` FROM iterations
			ORDER BY project_id ASC, created_at ASC, id ASC`)
	} else {
		rows, err = tx.Query(`SELECT `+iterationCols+` FROM iterations
			WHERE project_id = ? ORDER BY created_at ASC, id ASC`, projectID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Iteration
	for rows.Next() {
		it, err := scanIteration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// UpsertIteration 新建或更新迭代。
//
// 空状态按 planning 处理；更新时间留空时取当前 UTC。已有迭代的 CreatedAt 不会被空值覆盖。
func UpsertIteration(tx *sql.Tx, it *Iteration) error {
	if it == nil {
		return errors.New("db: 迭代为空")
	}
	it.ProjectID = strings.TrimSpace(it.ProjectID)
	it.ID = strings.TrimSpace(it.ID)
	it.Name = strings.TrimSpace(it.Name)
	it.Status = strings.TrimSpace(it.Status)
	if it.ProjectID == "" || it.ID == "" || it.Name == "" {
		return errors.New("db: 迭代需要 project_id、id 与 name")
	}
	if it.Status == "" {
		it.Status = IterationPlanning
	}
	if !ValidIterationStatus(it.Status) {
		return fmt.Errorf("db: 非法迭代状态 %q", it.Status)
	}
	now := UTCNow()
	if it.CreatedAt == "" {
		if existing, err := GetIteration(tx, it.ProjectID, it.ID); err != nil {
			return err
		} else if existing != nil {
			it.CreatedAt = existing.CreatedAt
		} else {
			it.CreatedAt = now
		}
	}
	if it.UpdatedAt == "" {
		it.UpdatedAt = now
	}
	if it.Extra == nil {
		it.Extra = map[string]any{}
	}
	_, err := tx.Exec(`
INSERT INTO iterations(
  project_id, id, name, goal, status, target_branch, merge_commit,
  created_at, updated_at, extra_json
) VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(project_id, id) DO UPDATE SET
  name=excluded.name, goal=excluded.goal, status=excluded.status,
  target_branch=excluded.target_branch, merge_commit=excluded.merge_commit,
  updated_at=excluded.updated_at, extra_json=excluded.extra_json
`, it.ProjectID, it.ID, it.Name, it.Goal, it.Status, it.TargetBranch, it.MergeCommit,
		it.CreatedAt, it.UpdatedAt, mustJSON(it.Extra))
	return err
}

// DeleteIteration 删除空迭代；还绑着任务时拒绝删除，先显式解绑或随工程一起移除。
func DeleteIteration(tx *sql.Tx, projectID, id string) error {
	n, err := CountTasksByIteration(tx, projectID, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrIterationNotEmpty
	}
	res, err := tx.Exec(`DELETE FROM iterations WHERE project_id = ? AND id = ?`, projectID, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrIterationNotFound
	}
	return nil
}

// CountTasksByIteration 统计某迭代下的任务数。
func CountTasksByIteration(tx *sql.Tx, projectID, iterationID string) (int64, error) {
	if iterationID == "" {
		return 0, nil
	}
	var n int64
	err := tx.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE project_id = ? AND iteration_id = ?`,
		projectID, iterationID,
	).Scan(&n)
	return n, err
}

// IterationTaskCounts 返回每个迭代的任务数，供迭代列表直接展示计数。
func IterationTaskCounts(tx *sql.Tx, projectID string) (map[string]int64, error) {
	rows, err := tx.Query(
		`SELECT iteration_id, COUNT(*) FROM tasks
		 WHERE project_id = ? AND iteration_id <> '' GROUP BY iteration_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
