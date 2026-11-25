package db

import (
	"database/sql"
	"encoding/json"
	"time"
)

// BlockedTransitionEvent 是可归档的历史 →blocked 事件摘要。
type BlockedTransitionEvent struct {
	ID        int64          `json:"id"`
	TS        string         `json:"ts"`
	ProjectID string         `json:"project_id"`
	Actor     string         `json:"actor"`
	TaskID    string         `json:"task_id"`
	Data      map[string]any `json:"data"`
	TaskStatus string        `json:"task_status,omitempty"`
}

// ListTerminalBlockedTransitions 列出 to=blocked 且关联题已 done/cancelled 的 status_changed 事件。
func ListTerminalBlockedTransitions(tx *sql.Tx, projectID string, limit int) ([]BlockedTransitionEvent, error) {
	if limit <= 0 {
		limit = 5000
	}
	q := `
SELECT e.id, e.ts, COALESCE(e.project_id,''), e.actor, COALESCE(e.task_id,''), e.data_json,
       COALESCE(t.status,'')
FROM events e
LEFT JOIN tasks t ON t.project_id = e.project_id AND t.id = e.task_id
WHERE e.type = 'status_changed'
  AND e.data_json LIKE '%"to":"blocked"%'
  AND (t.status IN ('done','cancelled') OR t.id IS NULL)`
	args := []any{}
	if projectID != "" {
		q += ` AND e.project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY e.id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockedTransitionEvent
	for rows.Next() {
		var e BlockedTransitionEvent
		var data string
		if err := rows.Scan(&e.ID, &e.TS, &e.ProjectID, &e.Actor, &e.TaskID, &data, &e.TaskStatus); err != nil {
			return nil, err
		}
		e.Data = map[string]any{}
		_ = json.Unmarshal([]byte(data), &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEventsByIDs 按主键删除事件。
func DeleteEventsByIDs(tx *sql.Tx, ids []int64) (int64, error) {
	var n int64
	for _, id := range ids {
		res, err := tx.Exec(`DELETE FROM events WHERE id = ?`, id)
		if err != nil {
			return n, err
		}
		c, _ := res.RowsAffected()
		n += c
	}
	return n, nil
}

// ListStaleUnblockJobs 列出终态 unblock_assist 作业（failed/escalated/done）且早于 before。
func ListStaleUnblockJobs(tx *sql.Tx, projectID string, before time.Time, limit int) ([]*SystemJob, error) {
	if limit <= 0 {
		limit = 5000
	}
	q := `
SELECT job_id, project_id, kind, status, task_id, instance_id, trigger_event_id, reason,
  decision_json, result_json, run_id, operation_id, journal_dir, attempts, max_attempts, locked_by, COALESCE(locked_at,''),
  created_at, updated_at, COALESCE(finished_at,'')
FROM system_jobs
WHERE kind = 'unblock_assist'
  AND status IN ('failed','escalated','done')
  AND updated_at < ?`
	args := []any{before.UTC().Format(time.RFC3339)}
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY updated_at ASC LIMIT ?`
	args = append(args, limit)
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SystemJob
	for rows.Next() {
		j, err := scanSystemJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// DeleteSystemJobsByIDs 删除指定 system_jobs。
func DeleteSystemJobsByIDs(tx *sql.Tx, ids []string) (int64, error) {
	var n int64
	for _, id := range ids {
		res, err := tx.Exec(`DELETE FROM system_jobs WHERE job_id = ?`, id)
		if err != nil {
			return n, err
		}
		c, _ := res.RowsAffected()
		n += c
	}
	return n, nil
}
