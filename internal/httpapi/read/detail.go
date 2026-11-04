package read

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/query"
)

func (a *API) handleTaskByInstance(w http.ResponseWriter, r *http.Request) {
	if rejectLegacyPagination(w, r) {
		return
	}
	instanceID := r.PathValue("instance_id")
	displayQ := strings.TrimSpace(r.URL.Query().Get("display_id"))

	// 路径参数必须是 ti_ 形态；显示编号只能走 query。
	if !model.InstanceID(instanceID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_instance_id",
			"路径参数必须是 ti_ 形态的实例身份，显示编号请用 ?display_id=", nil)
		return
	}

	var (
		item     map[string]any
		revision int64
		wm       query.Watermark
	)
	err := a.withReader(func(rd *query.Reader, tx *sql.Tx) error {
		wm = rd.Watermark()
		if displayQ != "" {
			// 同时给了 instance 与 display：校验它们是否指向同一行，歧义仍 409。
			instPage, err := rd.Instances(query.InstanceQuery{
				DisplayID: displayQ, State: "active", Limit: 2,
			})
			if err != nil {
				return err
			}
			if len(instPage.Items) > 1 {
				return ambiguousDisplay(displayQ, len(instPage.Items))
			}
		}
		row := tx.QueryRow(`
SELECT project_id, instance_id, id, title, status, priority, merge_order,
  COALESCE(owner, ''), revision, COALESCE(branch, ''), COALESCE(worktree, ''), attempts,
  COALESCE(heartbeat, ''), COALESCE(blocked_reason, ''), COALESCE(iteration_id, '')
FROM tasks WHERE instance_id = ?`, instanceID)
		var (
			projectID, inst, display, title, status, owner, branch, wt, hb, blocked string
			priority, mergeOrder, attempts                                          int
			rev                                                                     int64
			iterationID                                                             string
		)
		if err := row.Scan(&projectID, &inst, &display, &title, &status, &priority, &mergeOrder,
			&owner, &rev, &branch, &wt, &attempts, &hb, &blocked, &iterationID); err != nil {
			if err == sql.ErrNoRows {
				writeErr(w, http.StatusNotFound, "not_found", "任务实例不存在", nil)
				return errResponseSent{}
			}
			return err
		}
		if displayQ != "" && displayQ != display {
			writeErr(w, http.StatusConflict, "ambiguous_display_id",
				"display_id 与 instance 不一致", map[string]any{
					"display_id": displayQ, "task_instance": instanceID,
				})
			return errResponseSent{}
		}
		revision = rev
		item = map[string]any{
			"project_id":     projectID,
			"task_instance":  inst,
			"display_id":     display,
			"title":          title,
			"status":         status,
			"priority":       priority,
			"merge_order":    mergeOrder,
			"owner":          owner,
			"revision":       rev,
			"branch":         branch,
			"worktree":       wt,
			"attempts":       attempts,
			"heartbeat":      hb,
			"blocked_reason": blocked,
			"iteration_id":   iterationID,
		}
		w.Header().Set("ETag", etag(rev))
		return nil
	})
	if isSent(err) {
		return
	}
	if ae, ok := err.(ambiguousError); ok {
		writeErr(w, http.StatusConflict, "ambiguous_display_id",
			"显示编号命中多个活跃实例，请改用 task_instance",
			map[string]any{"display_id": ae.DisplayID, "count": ae.Count})
		return
	}
	if err != nil {
		mapQueryErr(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{
		"task": item, "revision": revision, "watermark": wm,
	})
}

func (a *API) handleOperationByID(w http.ResponseWriter, r *http.Request) {
	opID := r.PathValue("operation_id")
	if !model.OperationID(opID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_operation_id",
			"operation_id 形态不合法", nil)
		return
	}
	var (
		item map[string]any
		wm   query.Watermark
	)
	err := a.withReader(func(rd *query.Reader, tx *sql.Tx) error {
		wm = rd.Watermark()
		row := tx.QueryRow(`
SELECT operation_id, project_id, kind, COALESCE(run_id, ''), COALESCE(instance_id, ''),
  COALESCE(target_phase, ''), result, COALESCE(coordinator, ''),
  COALESCE(lease_expires_at, ''), created_at, updated_at
FROM operations WHERE operation_id = ?`, opID)
		var (
			id, projectID, kind, runID, instanceID, target, result, coord, lease, created, updated string
		)
		if err := row.Scan(&id, &projectID, &kind, &runID, &instanceID, &target, &result,
			&coord, &lease, &created, &updated); err != nil {
			if err == sql.ErrNoRows {
				writeErr(w, http.StatusNotFound, "not_found", "操作不存在", nil)
				return errResponseSent{}
			}
			return err
		}
		item = map[string]any{
			"operation_id": id, "project_id": projectID, "kind": kind,
			"run_id": runID, "task_instance": instanceID, "target_phase": target,
			"result": result, "coordinator": coord, "lease_expires_at": lease,
			"created_at": created, "updated_at": updated,
		}
		return nil
	})
	if isSent(err) {
		return
	}
	if err != nil {
		mapQueryErr(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"operation": item, "watermark": wm})
}

func (a *API) handleRunByID(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	if !model.RunID(runID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_run_id", "run_id 形态不合法", nil)
		return
	}
	var (
		item map[string]any
		wm   query.Watermark
	)
	err := a.withReader(func(rd *query.Reader, tx *sql.Tx) error {
		wm = rd.Watermark()
		var (
			projectID, instanceID, phase, recon, desired, started, finished, updated string
			version, generation                                                      int
			stopOK, frozen                                                           int
		)
		err := tx.QueryRow(`
SELECT project_id, instance_id, version_no, generation, phase, reconciliation_state,
  desired_action, process_stop_confirmed, delivery_frozen,
  COALESCE(started_at, ''), COALESCE(finished_at, ''), updated_at
FROM runs WHERE run_id = ?`, runID).Scan(
			&projectID, &instanceID, &version, &generation, &phase, &recon, &desired,
			&stopOK, &frozen, &started, &finished, &updated)
		if err == sql.ErrNoRows {
			writeErr(w, http.StatusNotFound, "not_found", "Run 不存在", nil)
			return errResponseSent{}
		}
		if err != nil {
			return err
		}
		m := model.Run{
			Phase:                model.RunPhase(phase),
			Reconciliation:       model.ReconciliationState(recon),
			ProcessStopConfirmed: stopOK != 0,
			DeliveryFrozen:       frozen != 0,
		}
		item = map[string]any{
			"run_id": runID, "project_id": projectID, "task_instance": instanceID,
			"task_version": version, "generation": generation, "phase": phase,
			"reconciliation_state": recon, "desired_action": desired,
			"process_stop_confirmed": stopOK != 0, "delivery_frozen": frozen != 0,
			"occupies": m.Occupies(), "started_at": started, "finished_at": finished,
			"updated_at": updated,
		}
		return nil
	})
	if isSent(err) {
		return
	}
	if err != nil {
		mapQueryErr(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"run": item, "watermark": wm})
}

func etag(rev int64) string {
	return `"rev-` + itoa(rev) + `"`
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
