package query

import (
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// RunQuery 是 Run 列表的过滤与翻页参数。
type RunQuery struct {
	ProjectID  string
	InstanceID model.InstanceID
	Phase      model.RunPhase
	Limit      int
	Cursor     string
}

// RunRow 是一次执行的列表投影。
//
// Occupies 由 model.Run.Occupies 现算，不在这里另写一份「算不算占用」的规则：
// 结构校验、领取判定与查询展示必须用同一条规则，否则会出现
// 「看板说空闲、领取却报 E240」。
type RunRow struct {
	RunID                model.RunID               `json:"run_id"`
	ProjectID            model.ProjectID           `json:"project_id"`
	InstanceID           model.InstanceID          `json:"task_instance"`
	VersionNo            int                       `json:"task_version"`
	Generation           model.Generation          `json:"generation"`
	Phase                model.RunPhase            `json:"phase"`
	Reconciliation       model.ReconciliationState `json:"reconciliation_state"`
	DesiredAction        model.DesiredAction       `json:"desired_action"`
	ProcessStopConfirmed bool                      `json:"process_stop_confirmed"`
	DeliveryFrozen       bool                      `json:"delivery_frozen"`
	Occupies             bool                      `json:"occupies"`
	StartedAt            string                    `json:"started_at,omitempty"`
	FinishedAt           string                    `json:"finished_at,omitempty"`
	UpdatedAt            string                    `json:"updated_at"`
}

// Runs 按 (instance_id, generation) 排序返回执行页；同一实例的多次执行按代际递增。
func (r *Reader) Runs(q RunQuery) (Page[RunRow], error) {
	limit, err := normalizeLimit(q.Limit)
	if err != nil {
		return Page[RunRow]{}, err
	}
	if q.Phase != "" && !q.Phase.Valid() {
		return Page[RunRow]{}, unknownEnum("runs.phase", string(q.Phase))
	}

	where := []string{"1=1"}
	var args []any
	if q.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, q.ProjectID)
	}
	if q.InstanceID != "" {
		where = append(where, "instance_id = ?")
		args = append(args, string(q.InstanceID))
	}
	if q.Phase != "" {
		where = append(where, "phase = ?")
		args = append(args, string(q.Phase))
	}
	if q.Cursor != "" {
		keys, err := decodeCursor(q.Cursor, kindRuns, 3)
		if err != nil {
			return Page[RunRow]{}, err
		}
		generation, err := intKey(keys[1], kindRuns)
		if err != nil {
			return Page[RunRow]{}, err
		}
		instance, run := keys[0], keys[2]
		where = append(where, `(instance_id > ?
      OR (instance_id = ? AND generation > ?)
      OR (instance_id = ? AND generation = ? AND run_id > ?))`)
		args = append(args, instance, instance, generation, instance, generation, run)
	}

	stmt := `SELECT run_id, project_id, instance_id, version_no, generation, phase,
  reconciliation_state, desired_action, process_stop_confirmed, delivery_frozen,
  COALESCE(started_at, ''), COALESCE(finished_at, ''), updated_at
FROM runs WHERE ` + strings.Join(where, " AND ") + `
ORDER BY instance_id ASC, generation ASC, run_id ASC
LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.tx.Query(stmt, args...)
	if err != nil {
		return Page[RunRow]{}, err
	}
	defer rows.Close()

	items := make([]RunRow, 0, limit+1)
	for rows.Next() {
		var (
			row     RunRow
			phase   string
			recon   string
			desired string
			stop    int
			frozen  int
		)
		if err := rows.Scan(&row.RunID, &row.ProjectID, &row.InstanceID, &row.VersionNo,
			&row.Generation, &phase, &recon, &desired, &stop, &frozen,
			&row.StartedAt, &row.FinishedAt, &row.UpdatedAt); err != nil {
			return Page[RunRow]{}, err
		}
		row.Phase = model.RunPhase(phase)
		row.Reconciliation = model.ReconciliationState(recon)
		row.DesiredAction = model.DesiredAction(desired)
		row.ProcessStopConfirmed = stop != 0
		row.DeliveryFrozen = frozen != 0
		probe := model.Run{
			Phase:                row.Phase,
			Reconciliation:       row.Reconciliation,
			ProcessStopConfirmed: row.ProcessStopConfirmed,
			DeliveryFrozen:       row.DeliveryFrozen,
		}
		row.Occupies = probe.Occupies()
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return Page[RunRow]{}, err
	}
	items, next := pageOf(items, limit, kindRuns, func(row RunRow) []string {
		return []string{string(row.InstanceID), itoa(int(row.Generation)), string(row.RunID)}
	})
	return Page[RunRow]{Items: items, Next: next, Watermark: r.wm}, nil
}
