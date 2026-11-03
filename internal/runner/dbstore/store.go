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
		for _, op := range req.Ops {
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
