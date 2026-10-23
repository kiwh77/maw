package db

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// 节点、执行槽与工作目录占用（契约 maw.runtime.state/v1 §4）。
//
// 三种资源分开记：node_slot 是节点上的并发位置，workspace 是规范化目录，
// merge 是工程合入位。它们的释放前置不同，所以从不联动释放——
// 进程停了就该空出槽位，但目录里可能还躺着待验收的产物（E241／E242）。
//
// 唯一有效持有者由库保证：resource_key 上「未释放」的部分唯一索引。
// 代际是条件写入：用旧代际释放新占用会被拒（E243）。
const (
	// ResourceLeaseKeyNode 是节点槽的资源键前缀。
	resourceKeyNode = "node:"
	// resourceKeyWorkspace 是工作目录的资源键前缀。
	resourceKeyWorkspace = "ws:"
	// resourceKeyMerge 是合入位的资源键前缀。
	resourceKeyMerge = "merge:"

	// EventLeaseReleased 是释放占用的标准事件类型（E261）。
	EventLeaseReleased = "lease.released"
)

// Node 是本机节点记录。
//
// SlotLimit 是调度唯一依据；AccountQuota 只记录平台账号侧的额度，
// 不参与空闲槽推断——把额度当并发上限会凭空造出并不存在的算力。
type Node struct {
	ID           string         `json:"node_id"`
	ProjectID    string         `json:"project_id,omitempty"`
	Label        string         `json:"label,omitempty"`
	SlotLimit    int            `json:"slot_limit"`
	AccountQuota int            `json:"account_quota"`
	CreatedAt    string         `json:"created_at"`
	LastSeen     string         `json:"last_seen"`
	Meta         map[string]any `json:"meta,omitempty"`
}

// NodeCapacity 是节点当前的槽位账目。
type NodeCapacity struct {
	NodeID      string `json:"node_id"`
	SlotLimit   int    `json:"slot_limit"`
	ActiveSlots int    `json:"active_slots"`
	FreeSlots   int    `json:"free_slots"`
}

// LeaseSpec 是一次占用的申请内容。
type LeaseSpec struct {
	ProjectID  string           `json:"project_id"`
	InstanceID string           `json:"task_instance"`
	RunID      string           `json:"run_id,omitempty"`
	Generation model.Generation `json:"generation"`
	Holder     string           `json:"holder,omitempty"`
}

// Resource 是一条被占用的具体资源；Key 负责规范化，避免同一目录写成两种键。
type Resource struct {
	Kind      model.LeaseKind `json:"kind"`
	NodeID    string          `json:"node_id,omitempty"`
	Slot      int             `json:"slot,omitempty"`
	Path      string          `json:"path,omitempty"`
	ProjectID string          `json:"project_id,omitempty"`
}

// Key 返回规范化资源键：
//
//	node:<node>#<slot>   —— 节点槽按 (节点, 槽号) 唯一
//	ws:<clean(abs)>      —— 目录按清理后的绝对路径唯一（尾斜杠、./、重复分隔符都等价）
//	merge:<project>      —— 合入位按工程唯一
func (r Resource) Key() (string, error) {
	switch r.Kind {
	case model.LeaseNodeSlot:
		node := strings.TrimSpace(r.NodeID)
		if node == "" || r.Slot < 1 {
			return "", model.Violation(model.CodeRuntimeFieldInvalid, "resource",
				"节点槽必须给出 node_id 与从 1 开始的 slot")
		}
		return resourceKeyNode + node + "#" + strconv.Itoa(r.Slot), nil
	case model.LeaseWorkspace:
		path := strings.TrimSpace(r.Path)
		if path == "" || !filepath.IsAbs(path) {
			return "", model.Violationf(model.CodeRuntimeFieldInvalid, "resource.path",
				"工作目录必须是绝对路径，收到 %q", r.Path)
		}
		return resourceKeyWorkspace + filepath.Clean(path), nil
	case model.LeaseMerge:
		project := strings.TrimSpace(r.ProjectID)
		if project == "" {
			return "", model.Violation(model.CodeRuntimeFieldInvalid, "resource.project_id",
				"合入位必须绑定工程")
		}
		return resourceKeyMerge + project, nil
	default:
		return "", model.Violationf(model.CodeRuntimeFieldInvalid, "resource.kind",
			"只能是 node_slot/workspace/merge，收到 %q", r.Kind)
	}
}

// Lease 是一条占用记录；ReleasedAt 非空表示已释放。
type Lease struct {
	ID                string           `json:"lease_id"`
	Kind              model.LeaseKind  `json:"kind"`
	ResourceKey       string           `json:"resource_key"`
	ProjectID         string           `json:"project_id"`
	InstanceID        string           `json:"task_instance"`
	RunID             string           `json:"run_id,omitempty"`
	Generation        model.Generation `json:"generation"`
	Holder            string           `json:"holder,omitempty"`
	AcquiredAt        string           `json:"acquired_at"`
	ReleasedAt        string           `json:"released_at,omitempty"`
	ReleaseGeneration model.Generation `json:"release_generation,omitempty"`
	ReleaseReason     string           `json:"release_reason,omitempty"`
}

// Released 报告该占用是否已释放。
func (l *Lease) Released() bool { return l.ReleasedAt != "" }

// ReleaseCondition 是一次释放的前置证据。
//
// 两种资源分别判断：槽位只关心进程是否停稳，目录只关心产物是否冻结。
type ReleaseCondition struct {
	// ProcessStopConfirmed 为真才允许释放节点槽（E241）。
	ProcessStopConfirmed bool
	// DeliveryFrozen 为真才允许释放工作目录（E242）。
	DeliveryFrozen bool
}

// UpsertNode 写入或更新节点记录；SlotLimit 必须非负。
func UpsertNode(tx *sql.Tx, n *Node) error {
	if strings.TrimSpace(n.ID) == "" {
		return model.Violation(model.CodeRuntimeFieldInvalid, "node.node_id", "节点必须有 id")
	}
	if n.SlotLimit < 0 || n.AccountQuota < 0 {
		return model.Violation(model.CodeRuntimeFieldInvalid, "node.slot_limit", "槽上限与账号额度不得为负")
	}
	if n.Meta == nil {
		n.Meta = map[string]any{}
	}
	now := UTCNow()
	if n.CreatedAt == "" {
		n.CreatedAt = now
	}
	n.LastSeen = now
	meta, err := json.Marshal(n.Meta)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
INSERT INTO nodes(node_id, project_id, label, slot_limit, account_quota, created_at, last_seen, meta_json)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(node_id) DO UPDATE SET
  project_id=excluded.project_id, label=excluded.label, slot_limit=excluded.slot_limit,
  account_quota=excluded.account_quota, last_seen=excluded.last_seen, meta_json=excluded.meta_json`,
		n.ID, n.ProjectID, n.Label, n.SlotLimit, n.AccountQuota, n.CreatedAt, n.LastSeen, string(meta))
	return err
}

// GetNode 返回节点；不存在返回 nil。
func GetNode(tx *sql.Tx, id string) (*Node, error) {
	var (
		n       Node
		metaRaw string
	)
	err := tx.QueryRow(`SELECT node_id, project_id, label, slot_limit, account_quota,
  created_at, last_seen, meta_json FROM nodes WHERE node_id = ?`, id).Scan(
		&n.ID, &n.ProjectID, &n.Label, &n.SlotLimit, &n.AccountQuota, &n.CreatedAt, &n.LastSeen, &metaRaw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.Meta = map[string]any{}
	_ = json.Unmarshal([]byte(metaRaw), &n.Meta)
	return &n, nil
}

// ListNode 列出节点；projectID 为空表示不限。
func ListNode(tx *sql.Tx, projectID string) ([]*Node, error) {
	q := `SELECT node_id FROM nodes`
	var args []any
	if projectID != "" {
		q += ` WHERE project_id = ? OR project_id = ''`
		args = append(args, projectID)
	}
	q += ` ORDER BY node_id ASC`
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]*Node, 0, len(ids))
	for _, id := range ids {
		n, err := GetNode(tx, id)
		if err != nil {
			return nil, err
		}
		if n != nil {
			out = append(out, n)
		}
	}
	return out, nil
}

// Capacity 只按 slot_limit 与活跃 node_slot 占用算账。
// 账号额度不参与：它约束的是账号侧，而不是本机并发位置。
func Capacity(tx *sql.Tx, nodeID string) (NodeCapacity, error) {
	n, err := GetNode(tx, nodeID)
	if err != nil {
		return NodeCapacity{}, err
	}
	if n == nil {
		return NodeCapacity{NodeID: nodeID}, nil
	}
	prefix := resourceKeyNode + nodeID + "#"
	var active int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM resource_leases
WHERE released_at IS NULL AND kind = ? AND substr(resource_key, 1, ?) = ?`,
		string(model.LeaseNodeSlot), len(prefix), prefix).Scan(&active); err != nil {
		return NodeCapacity{}, err
	}
	free := n.SlotLimit - active
	if free < 0 {
		free = 0
	}
	return NodeCapacity{NodeID: nodeID, SlotLimit: n.SlotLimit, ActiveSlots: active, FreeSlots: free}, nil
}

// AcquireLease 占用一条资源；同一资源已有有效持有者时报 E240。
func AcquireLease(tx *sql.Tx, spec LeaseSpec, res Resource) (*Lease, error) {
	key, err := res.Key()
	if err != nil {
		return nil, err
	}
	if !spec.Generation.Valid() {
		return nil, model.Violation(model.CodeRuntimeFieldInvalid, "lease.generation", "代际从 1 开始")
	}
	if strings.TrimSpace(spec.InstanceID) == "" {
		return nil, model.Violation(model.CodeRuntimeFieldInvalid, "lease.task_instance", "占用必须绑定任务实例")
	}
	held, err := ActiveLease(tx, key)
	if err != nil {
		return nil, err
	}
	if held != nil {
		return nil, model.Violationf(model.CodeDoubleOccupancy, "lease."+string(res.Kind),
			"%s 已由 %s 持有（run=%s gen=%d）", key, held.InstanceID, held.RunID, held.Generation)
	}
	id, err := newID("ls")
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(`
INSERT INTO resource_leases(lease_id, kind, resource_key, project_id, instance_id, run_id,
  generation, holder, acquired_at, released_at, release_generation, release_reason)
VALUES (?,?,?,?,?,?,?,?,?,NULL,NULL,NULL)`,
		id, string(res.Kind), key, spec.ProjectID, spec.InstanceID, spec.RunID,
		int(spec.Generation), spec.Holder, UTCNow())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, model.Violationf(model.CodeDoubleOccupancy, "lease", "%s 已被占用", key)
		}
		return nil, err
	}
	return GetLease(tx, id)
}

// AcquireNodeSlot 在节点上挑最小的空闲槽号并占用。
//
// 只看 slot_limit 与已占用的槽号；没有空位返回 capacity_exhausted，
// 而不是把账号额度折成槽位。
func AcquireNodeSlot(tx *sql.Tx, spec LeaseSpec, nodeID string) (*Lease, error) {
	n, err := GetNode(tx, nodeID)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, model.Violationf(model.CodeRuntimeFieldInvalid, "node.node_id", "节点 %q 未登记", nodeID)
	}
	prefix := resourceKeyNode + nodeID + "#"
	rows, err := tx.Query(`SELECT resource_key FROM resource_leases
WHERE released_at IS NULL AND kind = ? AND substr(resource_key, 1, ?) = ?`,
		string(model.LeaseNodeSlot), len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	taken := map[int]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		if slot, err := strconv.Atoi(strings.TrimPrefix(key, prefix)); err == nil {
			taken[slot] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for slot := 1; slot <= n.SlotLimit; slot++ {
		if taken[slot] {
			continue
		}
		return AcquireLease(tx, spec, Resource{Kind: model.LeaseNodeSlot, NodeID: nodeID, Slot: slot})
	}
	return nil, model.PortFailure(model.PortErrCapacityExhausted,
		"节点 "+nodeID+" 的 "+strconv.Itoa(n.SlotLimit)+" 个槽位已被占满")
}

// GetLease 按 id 返回占用记录；不存在返回 nil。
func GetLease(tx *sql.Tx, id string) (*Lease, error) {
	row := tx.QueryRow(`SELECT lease_id, kind, resource_key, project_id, instance_id, run_id,
  generation, holder, acquired_at, released_at, release_generation, release_reason
FROM resource_leases WHERE lease_id = ?`, id)
	return scanLease(row)
}

// ActiveLease 返回某资源当前的有效占用；已释放的不算。
func ActiveLease(tx *sql.Tx, key string) (*Lease, error) {
	row := tx.QueryRow(`SELECT lease_id, kind, resource_key, project_id, instance_id, run_id,
  generation, holder, acquired_at, released_at, release_generation, release_reason
FROM resource_leases WHERE resource_key = ? AND released_at IS NULL`, key)
	return scanLease(row)
}

// ListActiveLeases 返回工程内全部有效占用；projectID 为空表示不限。
func ListActiveLeases(tx *sql.Tx, projectID string) ([]*Lease, error) {
	return listLeases(tx, projectID, true)
}

// ListLeaseHistory 返回某资源的全部占用历史（含已释放）。
func ListLeaseHistory(tx *sql.Tx, key string) ([]*Lease, error) {
	rows, err := tx.Query(`SELECT lease_id, kind, resource_key, project_id, instance_id, run_id,
  generation, holder, acquired_at, released_at, release_generation, release_reason
FROM resource_leases WHERE resource_key = ? ORDER BY acquired_at ASC, lease_id ASC`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Lease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func listLeases(tx *sql.Tx, projectID string, activeOnly bool) ([]*Lease, error) {
	q := `SELECT lease_id, kind, resource_key, project_id, instance_id, run_id,
  generation, holder, acquired_at, released_at, release_generation, release_reason
FROM resource_leases WHERE 1=1`
	var args []any
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}
	if activeOnly {
		q += ` AND released_at IS NULL`
	}
	q += ` ORDER BY acquired_at ASC, lease_id ASC`
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Lease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ReleaseLease 按 id 释放占用。
//
// 三条前置缺一不可：代际必须等于持有代际（E243）；节点槽要求进程已停稳（E241）；
// 工作目录要求产物已冻结（E242）。释放同时写入 lease.released 事件（E261），
// 与占用状态在同一事务提交——时间轴上看不到「状态放了、事件没有」。
func ReleaseLease(tx *sql.Tx, leaseID string, releaseGen model.Generation, reason string, cond ReleaseCondition) error {
	lease, err := GetLease(tx, leaseID)
	if err != nil {
		return err
	}
	if lease == nil {
		return model.Violationf(model.CodeRuntimeFieldInvalid, "lease.lease_id", "%q 不存在", leaseID)
	}
	return releaseLease(tx, lease, releaseGen, reason, cond)
}

// ReleaseByResource 按规范化资源键释放。
func ReleaseByResource(tx *sql.Tx, key string, releaseGen model.Generation, reason string, cond ReleaseCondition) error {
	lease, err := ActiveLease(tx, key)
	if err != nil {
		return err
	}
	if lease == nil {
		return model.Violationf(model.CodeRuntimeFieldInvalid, "lease", "%s 当前没有有效占用", key)
	}
	return releaseLease(tx, lease, releaseGen, reason, cond)
}

func releaseLease(tx *sql.Tx, lease *Lease, releaseGen model.Generation, reason string, cond ReleaseCondition) error {
	if lease.Released() {
		if releaseGen == lease.ReleaseGeneration || releaseGen == lease.Generation {
			return nil // 幂等：同一次释放重放不算冲突
		}
		return model.Violationf(model.CodeReleaseGeneration, "lease.release_generation",
			"%s 已于代际 %d 释放，不能用代际 %d 覆盖", lease.ID, lease.ReleaseGeneration, releaseGen)
	}
	if releaseGen != lease.Generation {
		return model.Violationf(model.CodeReleaseGeneration, "lease.release_generation",
			"必须等于持有代际 %d，收到 %d", lease.Generation, releaseGen)
	}
	switch lease.Kind {
	case model.LeaseNodeSlot:
		if !cond.ProcessStopConfirmed {
			return model.Violation(model.CodeSlotReleasedBusy, "lease.node_slot",
				"进程停止未确认，不得释放节点槽")
		}
	case model.LeaseWorkspace:
		if !cond.DeliveryFrozen {
			return model.Violation(model.CodeWorkspaceNotFrozen, "lease.workspace",
				"产物未冻结，不得释放工作目录")
		}
	}
	now := UTCNow()
	res, err := tx.Exec(`UPDATE resource_leases
SET released_at = ?, release_generation = ?, release_reason = ?
WHERE lease_id = ? AND released_at IS NULL`, now, int(releaseGen), reason, lease.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Violationf(model.CodeReleaseGeneration, "lease.lease_id", "%s 已被其它代际释放", lease.ID)
	}
	return AppendEvent(tx, lease.ProjectID, lease.Holder, EventLeaseReleased, "", map[string]any{
		"lease_id":           lease.ID,
		"kind":               string(lease.Kind),
		"resource_key":       lease.ResourceKey,
		"task_instance":      lease.InstanceID,
		"run_id":             lease.RunID,
		"release_generation": int(releaseGen),
		"reason":             reason,
	})
}

func scanLease(row interface{ Scan(dest ...any) error }) (*Lease, error) {
	var (
		l                  Lease
		kind               string
		releasedAt, reason sql.NullString
		releaseGen         sql.NullInt64
		generation         int
	)
	err := row.Scan(&l.ID, &kind, &l.ResourceKey, &l.ProjectID, &l.InstanceID, &l.RunID,
		&generation, &l.Holder, &l.AcquiredAt, &releasedAt, &releaseGen, &reason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	l.Kind = model.LeaseKind(kind)
	l.Generation = model.Generation(generation)
	l.ReleasedAt = releasedAt.String
	l.ReleaseReason = reason.String
	if releaseGen.Valid {
		l.ReleaseGeneration = model.Generation(releaseGen.Int64)
	}
	return &l, nil
}
