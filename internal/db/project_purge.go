package db

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// ErrProjectNotFound 表示要移除的工程没有登记在本机中枢里。
var ErrProjectNotFound = errors.New("db: 工程未登记")

// ErrProjectBusy 表示工程里还有活态占位（非终态任务／在线 Agent）。
var ErrProjectBusy = errors.New("db: 工程仍有活态占位")

// ProjectBusyError 带上具体证据，调用方不用再查一次库就能说明「卡在哪」。
type ProjectBusyError struct {
	Tasks  []ProjectActiveTask  `json:"tasks"`
	Agents []ProjectOnlineAgent `json:"agents"`
}

func (e *ProjectBusyError) Error() string {
	return "工程里还有未完成的任务或在线 Agent，移除会毁掉现场"
}

// Is 让 errors.Is(err, ErrProjectBusy) 成立，调用方不必认识具体类型。
func (e *ProjectBusyError) Is(target error) bool { return target == ErrProjectBusy }

// ProjectActiveTask 是阻止移除的现场证据：仍由某个工作者持有、尚未终结的任务。
type ProjectActiveTask struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Owner  string `json:"owner"`
}

// ProjectOnlineAgent 是阻止移除的另一类现场证据：最近仍有心跳的 Agent。
type ProjectOnlineAgent struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	LastSeen string `json:"last_seen"`
}

// ProjectPurgeCounts 是移除时各表实际删除的行数。
//
// 计数只用于回报「删了什么」，不参与判定；判定看 ProjectRemovalEvidence。
type ProjectPurgeCounts struct {
	Tasks                 int64 `json:"tasks"`
	Agents                int64 `json:"agents"`
	Locks                 int64 `json:"locks"`
	Messages              int64 `json:"messages"`
	SharedFiles           int64 `json:"shared_files"`
	Nodes                 int64 `json:"nodes"`
	Leases                int64 `json:"resource_leases"`
	Runs                  int64 `json:"runs"`
	RunEvents             int64 `json:"run_events"`
	Operations            int64 `json:"operations"`
	Deliveries            int64 `json:"deliveries"`
	DeliveryVerifications int64 `json:"delivery_verifications"`
	DeliveryReviews       int64 `json:"delivery_reviews"`
	SourceSessions        int64 `json:"source_sessions"`
	SourceTurns           int64 `json:"source_turns"`
	SourceLinks           int64 `json:"run_source_links"`
	ProjectionStates      int64 `json:"projection_state"`
	Notifications         int64 `json:"notification_deliveries"`
	SystemJobs            int64 `json:"system_jobs"`
	Iterations            int64 `json:"iterations"`
	Projects              int64 `json:"projects"`
}

// activeTaskStatuses 返回「非终态」状态集合，供 SQL 的 IN 子句使用。
//
// 判定源是 model.TaskStatus.Terminal()：这里不另立一份状态名单，
// 否则新增状态时会出现「状态机说是活的、移除守卫说是死的」。
func activeTaskStatuses() []string {
	out := make([]string, 0, 8)
	for _, s := range model.AllTaskStatuses() {
		if !s.Terminal() {
			out = append(out, string(s))
		}
	}
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ProjectRemovalEvidence 读取一次移除前的现场。
//
// aliveSince 之前的心跳视为过期（形态与 last_seen 相同：UTC RFC3339 秒级），
// 传入空串表示「不按时间判活、只看状态」。
func ProjectRemovalEvidence(tx *sql.Tx, projectID, aliveSince string) ([]ProjectActiveTask, []ProjectOnlineAgent, error) {
	p, err := GetProject(tx, projectID)
	if err != nil {
		return nil, nil, err
	}
	if p == nil {
		return nil, nil, ErrProjectNotFound
	}

	statuses := activeTaskStatuses()
	var tasks []ProjectActiveTask
	if len(statuses) > 0 {
		args := []any{projectID}
		for _, s := range statuses {
			args = append(args, s)
		}
		rows, err := tx.Query(
			`SELECT id, status, COALESCE(owner, '') FROM tasks
			 WHERE project_id = ? AND status IN (`+placeholders(len(statuses))+`)
			 ORDER BY priority ASC, id ASC`,
			args...,
		)
		if err != nil {
			return nil, nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var t ProjectActiveTask
			if err := rows.Scan(&t.ID, &t.Status, &t.Owner); err != nil {
				return nil, nil, err
			}
			tasks = append(tasks, t)
		}
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	}

	agentQuery := `SELECT id, status, last_seen FROM agents
		WHERE project_id = ? AND status <> 'offline'`
	agentArgs := []any{projectID}
	if aliveSince != "" {
		agentQuery += ` AND last_seen >= ?`
		agentArgs = append(agentArgs, aliveSince)
	}
	agentQuery += ` ORDER BY last_seen DESC, id ASC`
	arows, err := tx.Query(agentQuery, agentArgs...)
	if err != nil {
		return nil, nil, err
	}
	defer arows.Close()
	var agents []ProjectOnlineAgent
	for arows.Next() {
		var a ProjectOnlineAgent
		if err := arows.Scan(&a.ID, &a.Status, &a.LastSeen); err != nil {
			return nil, nil, err
		}
		agents = append(agents, a)
	}
	if err := arows.Err(); err != nil {
		return nil, nil, err
	}
	return tasks, agents, nil
}

// RetainedLedgerCounts 是移除后**故意保留**的台账行数。
//
// 三类台账不随工程移除而销毁：
//   - 任务实例与冻结版本：库里有「已冻结版本不可删」触发器，改写历史不是移除的语义；
//   - 事件流水：审计凭证，删了就查不到这次移除发生过；
//   - 用量记录：计费事实，不属于工程的活态状态。
func RetainedLedgerCounts(tx *sql.Tx, projectID string) (instances, versions, events, usage int64, err error) {
	if err = tx.QueryRow(`SELECT COUNT(*) FROM task_instances WHERE project_id = ?`, projectID).Scan(&instances); err != nil {
		return
	}
	if err = tx.QueryRow(
		`SELECT COUNT(*) FROM task_versions WHERE instance_id IN
			(SELECT instance_id FROM task_instances WHERE project_id = ?)`, projectID,
	).Scan(&versions); err != nil {
		return
	}
	if err = tx.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ?`, projectID).Scan(&events); err != nil {
		return
	}
	if err = tx.QueryRow(
		`SELECT COUNT(*) FROM usage_records WHERE turn_id IN
			(SELECT turn_id FROM source_turns WHERE session_id IN
				(SELECT session_id FROM source_sessions WHERE project_id = ?))`, projectID,
	).Scan(&usage); err != nil {
		return
	}
	return
}

// PurgeProject 在一个事务里清掉工程登记与它的活态数据。
//
// 调用方必须先过 ProjectRemovalEvidence 的守卫：本函数只执行，不判断「该不该删」。
// 删除顺序按外键依赖排：先子表（验证/评审/轮次），再父表（交付/会话），最后工程行。
//
// 删任务之前先退役活跃实例：否则 task_instances 里会留下「state=active 但没有
// tasks 行」的悬空身份，之后再建同名编号时会撞上部分唯一索引（同一工程同一编号
// 至多一个活跃实例）。
func PurgeProject(tx *sql.Tx, projectID string) (ProjectPurgeCounts, error) {
	var c ProjectPurgeCounts
	p, err := GetProject(tx, projectID)
	if err != nil {
		return c, err
	}
	if p == nil {
		return c, ErrProjectNotFound
	}
	// 任务行数必须在退役前统计：RetireInstance 释放别名时会顺带删掉实例对应的任务行，
	// 之后再数 RowsAffected 只会得到 0，回报的「删了多少任务」就成了假数。
	if c.Tasks, err = countRows(tx, `SELECT COUNT(*) FROM tasks WHERE project_id = ?`, projectID); err != nil {
		return c, err
	}
	if err := retireActiveInstances(tx, projectID, RetireProjectRemoved); err != nil {
		return c, err
	}

	steps := []struct {
		dst   *int64
		query string
	}{
		{&c.DeliveryVerifications, `DELETE FROM delivery_verifications WHERE delivery_id IN
			(SELECT delivery_id FROM deliveries WHERE project_id = ?)`},
		{&c.DeliveryReviews, `DELETE FROM delivery_reviews WHERE delivery_id IN
			(SELECT delivery_id FROM deliveries WHERE project_id = ?)`},
		{&c.Deliveries, `DELETE FROM deliveries WHERE project_id = ?`},
		{&c.SourceTurns, `DELETE FROM source_turns WHERE session_id IN
			(SELECT session_id FROM source_sessions WHERE project_id = ?)`},
		{&c.SourceSessions, `DELETE FROM source_sessions WHERE project_id = ?`},
		{&c.SourceLinks, `DELETE FROM run_source_links WHERE project_id = ?`},
		{&c.RunEvents, `DELETE FROM run_events WHERE project_id = ?`},
		{&c.Operations, `DELETE FROM operations WHERE project_id = ?`},
		{&c.Runs, `DELETE FROM runs WHERE project_id = ?`},
		{&c.Leases, `DELETE FROM resource_leases WHERE project_id = ?`},
		{&c.Nodes, `DELETE FROM nodes WHERE project_id = ?`},
		{nil, `DELETE FROM tasks WHERE project_id = ?`},
		{&c.Iterations, `DELETE FROM iterations WHERE project_id = ?`},
		{&c.Agents, `DELETE FROM agents WHERE project_id = ?`},
		{&c.Locks, `DELETE FROM locks WHERE project_id = ?`},
		{&c.Messages, `DELETE FROM messages WHERE project_id = ?`},
		{&c.SharedFiles, `DELETE FROM shared_files WHERE project_id = ?`},
		{&c.ProjectionStates, `DELETE FROM projection_state WHERE project_id = ?`},
		{&c.Notifications, `DELETE FROM notification_deliveries WHERE project_id = ?`},
		{&c.SystemJobs, `DELETE FROM system_jobs WHERE project_id = ?`},
	}
	for _, step := range steps {
		n, err := deleteRows(tx, step.query, projectID)
		if err != nil {
			return c, err
		}
		if step.dst != nil {
			*step.dst = n
		}
	}

	n, err := deleteRows(tx, `DELETE FROM projects WHERE id = ?`, projectID)
	if err != nil {
		return c, err
	}
	c.Projects = n
	return c, nil
}

func deleteRows(tx *sql.Tx, query string, args ...any) (int64, error) {
	res, err := tx.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func countRows(tx *sql.Tx, query string, args ...any) (int64, error) {
	var n int64
	if err := tx.QueryRow(query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
