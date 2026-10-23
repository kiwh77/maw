package query

import (
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// OperationQuery 是持久操作列表的过滤与翻页参数。
type OperationQuery struct {
	ProjectID  string
	InstanceID model.InstanceID
	Kind       model.OperationKind
	Result     model.OperationResult
	Limit      int
	Cursor     string
}

// OperationRow 是一条持久操作投影；pending 且租约过期是「需要核实」的信号。
type OperationRow struct {
	OperationID    model.OperationID     `json:"operation_id"`
	ProjectID      model.ProjectID       `json:"project_id"`
	Kind           model.OperationKind   `json:"kind"`
	RunID          model.RunID           `json:"run_id,omitempty"`
	InstanceID     model.InstanceID      `json:"task_instance,omitempty"`
	TargetPhase    string                `json:"target_phase,omitempty"`
	Result         model.OperationResult `json:"result"`
	Coordinator    string                `json:"coordinator,omitempty"`
	LeaseExpiresAt string                `json:"lease_expires_at,omitempty"`
	CreatedAt      string                `json:"created_at"`
	UpdatedAt      string                `json:"updated_at"`
}

// Operations 按 (created_at, operation_id) 排序返回操作页。
func (r *Reader) Operations(q OperationQuery) (Page[OperationRow], error) {
	limit, err := normalizeLimit(q.Limit)
	if err != nil {
		return Page[OperationRow]{}, err
	}
	if q.Kind != "" && !q.Kind.Valid() {
		return Page[OperationRow]{}, unknownEnum("operations.kind", string(q.Kind))
	}
	if q.Result != "" && !q.Result.Valid() {
		return Page[OperationRow]{}, unknownEnum("operations.result", string(q.Result))
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
	if q.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, string(q.Kind))
	}
	if q.Result != "" {
		where = append(where, "result = ?")
		args = append(args, string(q.Result))
	}
	if q.Cursor != "" {
		keys, err := decodeCursor(q.Cursor, kindOperations, 2)
		if err != nil {
			return Page[OperationRow]{}, err
		}
		created, id := keys[0], keys[1]
		where = append(where, `(created_at > ? OR (created_at = ? AND operation_id > ?))`)
		args = append(args, created, created, id)
	}

	stmt := `SELECT operation_id, project_id, kind, run_id, instance_id, target_phase, result,
  coordinator, COALESCE(lease_expires_at, ''), created_at, updated_at
FROM operations WHERE ` + strings.Join(where, " AND ") + `
ORDER BY created_at ASC, operation_id ASC
LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.tx.Query(stmt, args...)
	if err != nil {
		return Page[OperationRow]{}, err
	}
	defer rows.Close()

	items := make([]OperationRow, 0, limit+1)
	for rows.Next() {
		var (
			row          OperationRow
			kind, result string
		)
		if err := rows.Scan(&row.OperationID, &row.ProjectID, &kind, &row.RunID, &row.InstanceID,
			&row.TargetPhase, &result, &row.Coordinator, &row.LeaseExpiresAt,
			&row.CreatedAt, &row.UpdatedAt); err != nil {
			return Page[OperationRow]{}, err
		}
		row.Kind = model.OperationKind(kind)
		row.Result = model.OperationResult(result)
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return Page[OperationRow]{}, err
	}
	items, next := pageOf(items, limit, kindOperations, func(row OperationRow) []string {
		return []string{row.CreatedAt, string(row.OperationID)}
	})
	return Page[OperationRow]{Items: items, Next: next, Watermark: r.wm}, nil
}

// NodeQuery 是节点列表的过滤与翻页参数。
type NodeQuery struct {
	ProjectID string
	Limit     int
	Cursor    string
}

// NodeRow 是节点投影；SlotLimit 是调度依据，AccountQuota 只是展示字段。
type NodeRow struct {
	NodeID       model.NodeID    `json:"node_id"`
	ProjectID    model.ProjectID `json:"project_id,omitempty"`
	Label        string          `json:"label,omitempty"`
	SlotLimit    int             `json:"slot_limit"`
	AccountQuota int             `json:"account_quota"`
	CreatedAt    string          `json:"created_at"`
	LastSeen     string          `json:"last_seen"`
}

// Nodes 按 node_id 排序返回节点页。
func (r *Reader) Nodes(q NodeQuery) (Page[NodeRow], error) {
	limit, err := normalizeLimit(q.Limit)
	if err != nil {
		return Page[NodeRow]{}, err
	}
	where := []string{"1=1"}
	var args []any
	if q.ProjectID != "" {
		where = append(where, "(project_id = ? OR project_id = '')")
		args = append(args, q.ProjectID)
	}
	if q.Cursor != "" {
		keys, err := decodeCursor(q.Cursor, kindNodes, 1)
		if err != nil {
			return Page[NodeRow]{}, err
		}
		where = append(where, "node_id > ?")
		args = append(args, keys[0])
	}

	stmt := `SELECT node_id, project_id, label, slot_limit, account_quota, created_at, last_seen
FROM nodes WHERE ` + strings.Join(where, " AND ") + `
ORDER BY node_id ASC
LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.tx.Query(stmt, args...)
	if err != nil {
		return Page[NodeRow]{}, err
	}
	defer rows.Close()

	items := make([]NodeRow, 0, limit+1)
	for rows.Next() {
		var row NodeRow
		if err := rows.Scan(&row.NodeID, &row.ProjectID, &row.Label, &row.SlotLimit,
			&row.AccountQuota, &row.CreatedAt, &row.LastSeen); err != nil {
			return Page[NodeRow]{}, err
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return Page[NodeRow]{}, err
	}
	items, next := pageOf(items, limit, kindNodes, func(row NodeRow) []string {
		return []string{string(row.NodeID)}
	})
	return Page[NodeRow]{Items: items, Next: next, Watermark: r.wm}, nil
}

// DeliveryQuery 是交付列表的过滤与翻页参数。
type DeliveryQuery struct {
	ProjectID  string
	InstanceID model.InstanceID
	Mode       model.DeliveryMode
	Limit      int
	Cursor     string
}

// DeliveryRow 是一次交付投影；指纹变了旧验证才失效，所以指纹必须随行返回。
type DeliveryRow struct {
	DeliveryID      model.DeliveryID   `json:"delivery_id"`
	ProjectID       model.ProjectID    `json:"project_id"`
	InstanceID      model.InstanceID   `json:"task_instance"`
	VersionNo       int                `json:"task_version"`
	RunID           model.RunID        `json:"run_id"`
	Mode            model.DeliveryMode `json:"mode"`
	BaselineCommit  string             `json:"baseline_commit"`
	CandidateCommit string             `json:"candidate_commit,omitempty"`
	DeliveredAt     string             `json:"delivered_at"`
	Fingerprint     string             `json:"fingerprint"`
	Supersedes      string             `json:"supersedes,omitempty"`
	SupersededBy    string             `json:"superseded_by,omitempty"`
}

// Deliveries 按 (delivered_at, delivery_id) 排序返回交付页。
func (r *Reader) Deliveries(q DeliveryQuery) (Page[DeliveryRow], error) {
	limit, err := normalizeLimit(q.Limit)
	if err != nil {
		return Page[DeliveryRow]{}, err
	}
	if q.Mode != "" && !q.Mode.Valid() {
		return Page[DeliveryRow]{}, unknownEnum("deliveries.mode", string(q.Mode))
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
	if q.Mode != "" {
		where = append(where, "mode = ?")
		args = append(args, string(q.Mode))
	}
	if q.Cursor != "" {
		keys, err := decodeCursor(q.Cursor, kindDeliveries, 2)
		if err != nil {
			return Page[DeliveryRow]{}, err
		}
		delivered, id := keys[0], keys[1]
		where = append(where, `(delivered_at > ? OR (delivered_at = ? AND delivery_id > ?))`)
		args = append(args, delivered, delivered, id)
	}

	stmt := `SELECT delivery_id, project_id, instance_id, version_no, run_id, mode,
  baseline_commit, COALESCE(candidate_commit, ''), delivered_at, fingerprint,
  COALESCE(supersedes, ''), COALESCE(superseded_by, '')
FROM deliveries WHERE ` + strings.Join(where, " AND ") + `
ORDER BY delivered_at ASC, delivery_id ASC
LIMIT ?`
	args = append(args, limit+1)

	rows, err := r.tx.Query(stmt, args...)
	if err != nil {
		return Page[DeliveryRow]{}, err
	}
	defer rows.Close()

	items := make([]DeliveryRow, 0, limit+1)
	for rows.Next() {
		var (
			row  DeliveryRow
			mode string
		)
		if err := rows.Scan(&row.DeliveryID, &row.ProjectID, &row.InstanceID, &row.VersionNo,
			&row.RunID, &mode, &row.BaselineCommit, &row.CandidateCommit, &row.DeliveredAt,
			&row.Fingerprint, &row.Supersedes, &row.SupersededBy); err != nil {
			return Page[DeliveryRow]{}, err
		}
		row.Mode = model.DeliveryMode(mode)
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return Page[DeliveryRow]{}, err
	}
	items, next := pageOf(items, limit, kindDeliveries, func(row DeliveryRow) []string {
		return []string{row.DeliveredAt, string(row.DeliveryID)}
	})
	return Page[DeliveryRow]{Items: items, Next: next, Watermark: r.wm}, nil
}
