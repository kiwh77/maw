package projection

import (
	"database/sql"

	"github.com/kiwh77/maw/internal/db"
)

// Checkpoint 把当前双水位与脏范围写入 projection_state（可重建，非业务权威）。
func (p *Projector) Checkpoint(tx *sql.Tx) error {
	c := p.Cursors()
	st := db.ProjectionState{
		Projection:     Name,
		ProjectID:      p.projectID,
		Version:        c.Version,
		Cursor:         c.StateCursor,
		CompleteBefore: c.ProjectionCursor,
		DirtyFrom:      p.dirtyFrom,
		DirtyTo:        p.dirtyTo,
		Meta: map[string]any{
			"completeness": string(c.Completeness),
		},
	}
	return db.UpsertProjectionState(tx, st)
}

// LoadCheckpoint 从库恢复水位／脏范围（区间本身须 Rebuild 重放事实）。
func (p *Projector) LoadCheckpoint(tx *sql.Tx) error {
	st, err := db.GetProjectionState(tx, Name, p.projectID)
	if err != nil || st == nil {
		return err
	}
	p.version = st.Version
	p.stateCursor = st.Cursor
	p.projCursor = st.CompleteBefore
	p.dirtyFrom = st.DirtyFrom
	p.dirtyTo = st.DirtyTo
	return nil
}
