package dbstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

// Store 把 ports.Store 落到 *db.Store（供 session.Runner 使用）。
type Store struct {
	DB        *db.Store
	ProjectID string
}

// Apply 在一个写事务里提交一批 StoreOp。
func (s *Store) Apply(ctx context.Context, req ports.StoreRequest) (ports.StoreResult, error) {
	_ = ctx
	if s == nil || s.DB == nil {
		return ports.StoreResult{}, fmt.Errorf("dbstore: 缺少 DB")
	}
	if errs := req.Validate(); len(errs) > 0 {
		return ports.StoreResult{}, model.Err(errs)
	}
	var out ports.StoreResult
	err := s.DB.Write(func(tx *sql.Tx) error {
		var lastCursor model.Cursor
		for _, op := range orderedOps(req.Ops) {
			cur, rev, err := applyOp(tx, s.ProjectID, op)
			if err != nil {
				return err
			}
			if cur > lastCursor {
				lastCursor = cur
			}
			if rev > out.Revision {
				out.Revision = rev
			}
			out.Applied = append(out.Applied, op.Kind)
		}
		out.Cursor = lastCursor
		return nil
	})
	return out, err
}

// orderedOps 把事件写入排在阶段更新之前。
//
// run 的阶段更新要求「同批已有对应的阶段事件」（db.UpdateRunProgress 会核对
// run_events），事件后写等于自己把自己挡在门外，阶段推进就永远落不了库。
// 顺序只影响同一批内部的先后，不改任何语义。
func orderedOps(ops []ports.StoreOp) []ports.StoreOp {
	out := make([]ports.StoreOp, 0, len(ops))
	for _, op := range ops {
		if op.Kind == ports.StoreEventAppend {
			out = append(out, op)
		}
	}
	for _, op := range ops {
		if op.Kind != ports.StoreEventAppend {
			out = append(out, op)
		}
	}
	return out
}

func applyOp(tx *sql.Tx, projectID string, op ports.StoreOp) (model.Cursor, model.Revision, error) {
	switch op.Kind {
	case ports.StoreRunPhase:
		p := op.RunPhase
		if p == nil {
			return 0, 0, fmt.Errorf("dbstore: 缺少 run_phase")
		}
		run, err := db.UpdateRunProgress(tx, string(p.RunID), p.Phase, false, false, nil)
		if err != nil {
			return 0, 0, err
		}
		// 起止时刻与阶段同批落库：run 的 started_at／finished_at 没有别的写入方，
		// 事后补写就会与阶段事实脱节。
		if err := markRunTimes(tx, string(p.RunID), p.Phase); err != nil {
			return 0, 0, err
		}
		rev := model.Revision(0)
		if run != nil {
			rev = model.Revision(run.Generation)
		}
		return 0, rev, nil
	case ports.StoreEventAppend:
		ev := op.Event
		if ev == nil {
			return 0, 0, fmt.Errorf("dbstore: 缺少 event")
		}
		var phase model.RunPhase
		if ev.Phase != nil {
			phase = *ev.Phase
		}
		cursor, inserted, err := db.AppendRunEvent(tx, db.RunEventSpec{
			ID:            ev.EventID,
			SchemaVersion: ev.SchemaVersion,
			Kind:          ev.Kind,
			RunID:         string(ev.RunID),
			ProjectID:     projectID,
			Phase:         phase,
			OccurredAt:    ev.OccurredAt,
			ReceivedAt:    ev.ReceivedAt,
			Quality:       ev.Quality,
			Payload:       map[string]any{},
		})
		if err != nil {
			return 0, 0, err
		}
		if !inserted {
			return cursor, 0, model.Violation(model.CodeEventDuplicate, "event.event_id", "事件已存在")
		}
		return cursor, 0, nil
	case ports.StoreOperationUpdate:
		ou := op.OperationUpdate
		if ou == nil {
			return 0, 0, fmt.Errorf("dbstore: 缺少 operation_update")
		}
		_, err := tx.Exec(`UPDATE operations SET result = ?, updated_at = ? WHERE operation_id = ?`,
			string(ou.Result), time.Now().UTC().Format(time.RFC3339), string(ou.OperationID))
		return 0, 0, err
	default:
		return 0, 0, fmt.Errorf("dbstore: 未实现 op %s", op.Kind)
	}
}

// markRunTimes 记 run 的起止时刻。
//
// started_at 只在第一次进入 running 时写（后续阶段不覆盖），finished_at 在终态
// 写一次。两者都只补空值：重放同一批事实不会把时间往后推。
func markRunTimes(tx *sql.Tx, runID string, phase model.RunPhase) error {
	now := time.Now().UTC().Format(time.RFC3339)
	switch {
	case phase == model.PhaseRunning:
		_, err := tx.Exec(`UPDATE runs SET started_at = COALESCE(NULLIF(started_at, ''), ?) WHERE run_id = ?`, now, runID)
		return err
	case phase.Terminal():
		_, err := tx.Exec(`UPDATE runs SET finished_at = COALESCE(NULLIF(finished_at, ''), ?) WHERE run_id = ?`, now, runID)
		return err
	default:
		return nil
	}
}
