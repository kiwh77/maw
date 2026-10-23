package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// Run、Operation 与标准事件（契约 maw.runtime.state/v1 §3–§7）。
//
// 三条边界：
//
//  1. 领取是**一个事务**：占槽、占目录、建 Run、建 Operation、写 run.phase 事件，
//     任一步失败整笔回滚。不在这里起进程、不跑 git、不重发外部任务。
//  2. 幂等键绑定请求摘要：同键同摘要返回原结果，同键不同摘要报 E252（E252 不可重试）。
//  3. 事件游标是入库序号（AUTOINCREMENT，永不回退）；event_id 唯一，补交同一条只入库一次。

// RunEventSchemaVersion 是标准事件的契约版本（E214）。
const (
	RunEventSchemaVersion = 1
	// EventRunPhase 是阶段变更的标准事件类型（E260）。
	EventRunPhase = "run.phase"
)

// Run 是一次执行尝试的持久状态。
//
// 业务任务状态不在这里：Run 完成不等于任务 done。
type Run struct {
	ID                   string                    `json:"run_id"`
	ProjectID            string                    `json:"project_id"`
	InstanceID           string                    `json:"task_instance"`
	VersionNo            model.VersionNo           `json:"task_version"`
	Generation           model.Generation          `json:"generation"`
	Phase                model.RunPhase            `json:"phase"`
	Reconciliation       model.ReconciliationState `json:"reconciliation_state"`
	DesiredAction        model.DesiredAction       `json:"desired_action"`
	ProcessStopConfirmed bool                      `json:"process_stop_confirmed"`
	DeliveryFrozen       bool                      `json:"delivery_frozen"`
	StartedAt            string                    `json:"started_at,omitempty"`
	FinishedAt           string                    `json:"finished_at,omitempty"`
	PhaseHistory         []model.PhaseEntry        `json:"phase_history"`
	Evidence             []string                  `json:"evidence,omitempty"`
	Leases               []model.Lease             `json:"leases,omitempty"`
	CreatedAt            string                    `json:"created_at"`
	UpdatedAt            string                    `json:"updated_at"`
}

// Model 转成领域值对象，让「是否仍计入占用」只有 model 一套判定（E240）。
func (r *Run) Model() model.Run {
	return model.Run{
		RunID:                model.RunID(r.ID),
		Instance:             model.InstanceID(r.InstanceID),
		Version:              r.VersionNo,
		Generation:           r.Generation,
		Phase:                r.Phase,
		PhaseHistory:         r.PhaseHistory,
		Reconciliation:       r.Reconciliation,
		DesiredAction:        r.DesiredAction,
		ProcessStopConfirmed: r.ProcessStopConfirmed,
		DeliveryFrozen:       r.DeliveryFrozen,
		Leases:               r.Leases,
		Evidence:             r.Evidence,
	}
}

// Occupies 报告该 Run 是否仍计入资源占用（契约 §4.1）。
//
// unknown 以及「已失败但进程停止未确认」都继续算占用：只看终态字符串就重派，
// 会让两个进程写同一份产物。
func (r *Run) Occupies() bool {
	m := r.Model()
	return m.Occupies()
}

// Operation 是一次持久副作用请求。
type Operation struct {
	ID             string                `json:"operation_id"`
	ProjectID      string                `json:"project_id"`
	Kind           model.OperationKind   `json:"kind"`
	RunID          string                `json:"run_id,omitempty"`
	InstanceID     string                `json:"task_instance,omitempty"`
	IdempotencyKey string                `json:"idempotency_key"`
	RequestDigest  string                `json:"request_digest"`
	TargetPhase    model.RunPhase        `json:"target_phase,omitempty"`
	Result         model.OperationResult `json:"result"`
	Coordinator    string                `json:"coordinator,omitempty"`
	LeaseExpiresAt string                `json:"lease_expires_at,omitempty"`
	CreatedAt      string                `json:"created_at"`
	UpdatedAt      string                `json:"updated_at"`
}

// OperationSpec 是创建持久操作的输入；摘要留空时按内容自动绑定。
type OperationSpec struct {
	ProjectID      string
	Kind           model.OperationKind
	RunID          string
	InstanceID     string
	IdempotencyKey string
	RequestDigest  string
	TargetPhase    model.RunPhase
}

// RunEvent 是一条标准事实；Cursor 是入库序号，不是发生顺序。
type RunEvent struct {
	Cursor        model.Cursor   `json:"cursor"`
	ID            model.EventID  `json:"event_id"`
	ProjectID     string         `json:"project_id"`
	RunID         string         `json:"run_id,omitempty"`
	InstanceID    string         `json:"task_instance,omitempty"`
	Kind          string         `json:"kind"`
	Phase         model.RunPhase `json:"phase,omitempty"`
	LeaseID       string         `json:"lease_id,omitempty"`
	SchemaVersion int            `json:"schema_version"`
	Quality       model.Quality  `json:"quality"`
	OccurredAt    string         `json:"occurred_at"`
	ReceivedAt    string         `json:"received_at"`
	Payload       map[string]any `json:"payload,omitempty"`
}

// RunEventSpec 是追加事件的输入。
type RunEventSpec struct {
	ID            model.EventID
	ProjectID     string
	RunID         string
	InstanceID    string
	Kind          string
	Phase         model.RunPhase
	LeaseID       string
	SchemaVersion int
	Quality       model.Quality
	OccurredAt    time.Time
	ReceivedAt    time.Time
	Payload       map[string]any
}

// RequestDigest 把请求内容压成摘要，供幂等键绑定（同键不同摘要即冲突）。
func RequestDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// ClaimSpec 是一次领取的完整输入。
type ClaimSpec struct {
	ProjectID      string
	InstanceID     string
	VersionNo      model.VersionNo
	Generation     model.Generation
	NodeID         string
	Workspace      string
	Coordinator    string
	IdempotencyKey string
	RequestDigest  string
}

// ClaimOutcome 是一次领取的结果；Replay 为真表示命中幂等重放，没有新写任何行。
type ClaimOutcome struct {
	Run       *Run
	Operation *Operation
	Leases    []*Lease
	Cursor    model.Cursor
	Replay    bool
}

func (s ClaimSpec) digest() string {
	if s.RequestDigest != "" {
		return s.RequestDigest
	}
	return RequestDigest(s.ProjectID, s.InstanceID, strconv.Itoa(int(s.VersionNo)),
		strconv.Itoa(int(s.Generation)), s.NodeID, s.Workspace)
}

// ClaimRun 在同一个事务里完成：占槽、占目录、建 Run、建 Operation、写 run.phase 事件。
//
// 幂等：同键同摘要返回原 Run 与占用；同键不同摘要返回 E252。
// 风险占用：同一实例已有仍计入占用的 Run（含 unknown、失败未确认停止）时拒绝（E240）。
func ClaimRun(tx *sql.Tx, spec ClaimSpec) (*ClaimOutcome, error) {
	if strings.TrimSpace(spec.ProjectID) == "" || strings.TrimSpace(spec.InstanceID) == "" {
		return nil, model.Violation(model.CodeRuntimeFieldInvalid, "claim",
			"领取必须给出 project_id 与 task_instance")
	}
	if !spec.Generation.Valid() {
		return nil, model.Violation(model.CodeRuntimeFieldInvalid, "claim.generation", "代际从 1 开始")
	}
	if strings.TrimSpace(spec.IdempotencyKey) == "" {
		return nil, model.Violation(model.CodeClaimWithoutGuards, "claim.idempotency_key",
			"领取必须带幂等键，否则重放会双开")
	}
	digest := spec.digest()

	inst, err := GetInstance(tx, spec.InstanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil || string(inst.Legacy.ProjectID) != spec.ProjectID {
		return nil, model.Violationf(model.CodeDepMissing, "claim.task_instance",
			"实例 %q 不在工程 %s 内", spec.InstanceID, spec.ProjectID)
	}
	if inst.State == model.InstanceStateRetired {
		return nil, model.Violationf(model.CodeDepRetired, "claim.task_instance",
			"实例 %q 已退役，不能领取", spec.InstanceID)
	}
	if spec.VersionNo == 0 && inst.ActiveVersion != nil {
		spec.VersionNo = *inst.ActiveVersion
	}
	if inst.ActiveVersion == nil || spec.VersionNo != *inst.ActiveVersion {
		return nil, model.Violationf(model.CodeTaskFieldInvalid, "claim.task_version",
			"领取必须冻结当前活跃版本 %v，收到 %v", inst.ActiveVersion, spec.VersionNo)
	}

	if existing, err := findOperationByKey(tx, spec.ProjectID, model.OpDispatch, spec.IdempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.RequestDigest != digest {
			return nil, model.Violationf(model.CodeIdempotencyConflict, "claim.idempotency_key",
				"同键不同内容：已登记摘要 %s，本次 %s", existing.RequestDigest, digest)
		}
		run, err := GetRun(tx, existing.RunID)
		if err != nil {
			return nil, err
		}
		if run == nil {
			return nil, model.Violationf(model.CodeEventUnknownRun, "claim.run_id",
				"幂等记录指向的 Run %q 不存在", existing.RunID)
		}
		if run.Generation != spec.Generation {
			return nil, model.Violationf(model.CodeIdempotencyConflict, "claim.generation",
				"同键重放不得换代际：已登记 %d，本次 %d", run.Generation, spec.Generation)
		}
		leases, err := activeLeasesOfInstance(tx, spec.InstanceID)
		if err != nil {
			return nil, err
		}
		cursor, err := maxCursorOfRun(tx, run.ID)
		if err != nil {
			return nil, err
		}
		return &ClaimOutcome{Run: run, Operation: existing, Leases: leases, Cursor: cursor, Replay: true}, nil
	}

	if occupied, err := ActiveRunForInstance(tx, spec.InstanceID); err != nil {
		return nil, err
	} else if occupied != nil {
		return nil, model.Violationf(model.CodeDoubleOccupancy, "claim.task_instance",
			"%s 仍由 %s 占用（phase=%s reconciliation=%s stop_confirmed=%t）",
			spec.InstanceID, occupied.ID, occupied.Phase, occupied.Reconciliation, occupied.ProcessStopConfirmed)
	}

	leaseSpec := LeaseSpec{
		ProjectID:  spec.ProjectID,
		InstanceID: spec.InstanceID,
		Generation: spec.Generation,
		Holder:     spec.Coordinator,
	}
	slot, err := AcquireNodeSlot(tx, leaseSpec, spec.NodeID)
	if err != nil {
		return nil, err
	}
	leaseSpec.RunID = ""
	workspace, err := AcquireLease(tx, leaseSpec, Resource{Kind: model.LeaseWorkspace, Path: spec.Workspace})
	if err != nil {
		return nil, err
	}
	// Run 的 run_id 在占用之后才能定：把占用挂回本 Run。
	runID, err := newID("run")
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE resource_leases SET run_id = ? WHERE lease_id IN (?, ?)`,
		runID, slot.ID, workspace.ID); err != nil {
		return nil, err
	}

	now := nowSeconds()
	run := &Run{
		ID:             runID,
		ProjectID:      spec.ProjectID,
		InstanceID:     spec.InstanceID,
		VersionNo:      spec.VersionNo,
		Generation:     spec.Generation,
		Phase:          model.PhaseReserved,
		Reconciliation: model.ReconciliationConfirmed,
		DesiredAction:  model.ActionNone,
		PhaseHistory:   []model.PhaseEntry{{Phase: model.PhaseReserved, At: now}},
		Evidence:       []string{},
		Leases: []model.Lease{
			{LeaseID: model.LeaseID(slot.ID), Kind: model.LeaseNodeSlot, Generation: spec.Generation},
			{LeaseID: model.LeaseID(workspace.ID), Kind: model.LeaseWorkspace, Generation: spec.Generation},
		},
		CreatedAt: formatTS(now),
		UpdatedAt: formatTS(now),
	}
	if err := createRun(tx, run); err != nil {
		return nil, err
	}

	op, _, err := CreateOperation(tx, OperationSpec{
		ProjectID:      spec.ProjectID,
		Kind:           model.OpDispatch,
		RunID:          run.ID,
		InstanceID:     spec.InstanceID,
		IdempotencyKey: spec.IdempotencyKey,
		RequestDigest:  digest,
	})
	if err != nil {
		return nil, err
	}

	cursor, _, err := AppendRunEvent(tx, RunEventSpec{
		ID:            model.EventID(mustID("evt")),
		ProjectID:     spec.ProjectID,
		RunID:         run.ID,
		InstanceID:    spec.InstanceID,
		Kind:          EventRunPhase,
		Phase:         model.PhaseReserved,
		SchemaVersion: RunEventSchemaVersion,
		Quality:       model.QualityObserved,
		OccurredAt:    now,
		ReceivedAt:    now,
		Payload:       map[string]any{"generation": int(spec.Generation), "operation_id": op.ID},
	})
	if err != nil {
		return nil, err
	}

	leases, err := activeLeasesOfInstance(tx, spec.InstanceID)
	if err != nil {
		return nil, err
	}
	return &ClaimOutcome{Run: run, Operation: op, Leases: leases, Cursor: cursor}, nil
}

// GetRun 按 id 返回 Run；不存在返回 nil。
func GetRun(tx *sql.Tx, id string) (*Run, error) {
	row := tx.QueryRow(`SELECT run_id, project_id, instance_id, version_no, generation, phase,
  reconciliation_state, desired_action, process_stop_confirmed, delivery_frozen,
  started_at, finished_at, phase_history_json, evidence_json, leases_json, created_at, updated_at
FROM runs WHERE run_id = ?`, id)
	return scanRun(row)
}

// ListRuns 按代际返回某工程的 Run。
func ListRuns(tx *sql.Tx, projectID string) ([]*Run, error) {
	q := `SELECT run_id, project_id, instance_id, version_no, generation, phase,
  reconciliation_state, desired_action, process_stop_confirmed, delivery_frozen,
  started_at, finished_at, phase_history_json, evidence_json, leases_json, created_at, updated_at
FROM runs`
	var args []any
	if projectID != "" {
		q += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY created_at ASC, run_id ASC`
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ActiveRunForInstance 返回仍计入占用的 Run（含 unknown、失败但停止未确认）。
func ActiveRunForInstance(tx *sql.Tx, instanceID string) (*Run, error) {
	rows, err := tx.Query(`SELECT run_id, project_id, instance_id, version_no, generation, phase,
  reconciliation_state, desired_action, process_stop_confirmed, delivery_frozen,
  started_at, finished_at, phase_history_json, evidence_json, leases_json, created_at, updated_at
FROM runs WHERE instance_id = ? ORDER BY generation DESC`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		if r.Occupies() {
			return r, nil
		}
	}
	return nil, rows.Err()
}

// UpdateRunProgress 写阶段与停止/冻结事实。
//
// 阶段变更必须同时追加 run.phase 事件（E260），因此这里强制要求事件已在同一事务里
// 追加过：没写事件就返回 E260，而不是让状态先落库。
func UpdateRunProgress(tx *sql.Tx, runID string, phase model.RunPhase, processStopConfirmed, deliveryFrozen bool, evidence []string) (*Run, error) {
	run, err := GetRun(tx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, model.Violationf(model.CodeEventUnknownRun, "run.run_id", "%q 不存在", runID)
	}
	if phase == run.Phase {
		return nil, model.Violationf(model.CodeRuntimeFieldInvalid, "run.phase",
			"阶段未变化（%s），不要用重复状态掩盖缺失的转移", phase)
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM run_events WHERE run_id = ? AND kind = ? AND phase = ?`,
		runID, EventRunPhase, string(phase)).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, model.Violationf(model.CodePhaseEventMissing, "run.phase",
			"阶段 %s 没有对应的 %s 事件，状态与事实必须同批提交", phase, EventRunPhase)
	}
	now := nowSeconds()
	history := append(run.PhaseHistory, model.PhaseEntry{Phase: phase, At: now})
	historyJSON, err := json.Marshal(history)
	if err != nil {
		return nil, err
	}
	if evidence == nil {
		evidence = run.Evidence
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE runs SET phase = ?, process_stop_confirmed = ?, delivery_frozen = ?,
  phase_history_json = ?, evidence_json = ?, updated_at = ? WHERE run_id = ?`,
		string(phase), boolInt(processStopConfirmed), boolInt(deliveryFrozen),
		string(historyJSON), string(evidenceJSON), formatTS(now), runID); err != nil {
		return nil, err
	}
	return GetRun(tx, runID)
}

// ConfirmProcessStop 记录「进程确实已停止」这一事实（E231）。
//
// 释放节点槽以它为前置：退出码拿到不等于进程退干净。
func ConfirmProcessStop(tx *sql.Tx, runID, evidence string) error {
	run, err := GetRun(tx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return model.Violationf(model.CodeEventUnknownRun, "run.run_id", "%q 不存在", runID)
	}
	if run.ProcessStopConfirmed {
		return nil
	}
	items := append([]string{}, run.Evidence...)
	if evidence != "" {
		items = append(items, evidence)
	}
	evidenceJSON, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE runs SET process_stop_confirmed = 1, evidence_json = ?, updated_at = ?
WHERE run_id = ?`, string(evidenceJSON), UTCNow(), runID)
	return err
}

// CreateOperation 创建持久操作，幂等键绑定请求摘要。
//
// 返回 replay=true 表示同键同摘要命中已有记录；同键不同摘要返回 E252。
func CreateOperation(tx *sql.Tx, spec OperationSpec) (*Operation, bool, error) {
	if strings.TrimSpace(spec.IdempotencyKey) == "" {
		return nil, false, model.Violation(model.CodeClaimWithoutGuards, "operation.idempotency_key",
			"持久操作必须带幂等键")
	}
	if !spec.Kind.Valid() {
		return nil, false, model.Violationf(model.CodeRuntimeFieldInvalid, "operation.kind",
			"未知操作类型 %q", spec.Kind)
	}
	digest := spec.RequestDigest
	if digest == "" {
		digest = RequestDigest(spec.ProjectID, spec.RunID, string(spec.Kind), string(spec.TargetPhase))
	}
	existing, err := findOperationByKey(tx, spec.ProjectID, spec.Kind, spec.IdempotencyKey)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		if existing.RequestDigest != digest {
			return nil, false, model.Violationf(model.CodeIdempotencyConflict, "operation.idempotency_key",
				"同键不同内容：已登记摘要 %s，本次 %s", existing.RequestDigest, digest)
		}
		return existing, true, nil
	}
	id, err := newID("op")
	if err != nil {
		return nil, false, err
	}
	now := UTCNow()
	_, err = tx.Exec(`INSERT INTO operations(operation_id, project_id, kind, run_id, instance_id,
  idempotency_key, request_digest, target_phase, result, coordinator, lease_expires_at, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,'',NULL,?,?)`,
		id, spec.ProjectID, string(spec.Kind), spec.RunID, spec.InstanceID,
		spec.IdempotencyKey, digest, string(spec.TargetPhase), string(model.ResultPending), now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, false, model.Violationf(model.CodeIdempotencyConflict, "operation.idempotency_key",
				"幂等键 %q 已被并发写入", spec.IdempotencyKey)
		}
		return nil, false, err
	}
	op, err := GetOperation(tx, id)
	return op, false, err
}

// GetOperation 按 id 返回操作；不存在返回 nil。
func GetOperation(tx *sql.Tx, id string) (*Operation, error) {
	row := tx.QueryRow(`SELECT operation_id, project_id, kind, run_id, instance_id,
  idempotency_key, request_digest, target_phase, result, coordinator, lease_expires_at,
  created_at, updated_at FROM operations WHERE operation_id = ?`, id)
	return scanOperation(row)
}

// ClaimOperation 让协调者独占执行一个待办操作（协调者租约）。
//
// 只有 pending 且未被占用或租约已过期的操作能被领走；并发下只有一个协调者成功。
func ClaimOperation(tx *sql.Tx, operationID, coordinator string, ttl time.Duration) (bool, error) {
	if strings.TrimSpace(coordinator) == "" {
		return false, model.Violation(model.CodeRuntimeFieldInvalid, "operation.coordinator",
			"领取操作必须说明协调者")
	}
	if ttl <= 0 {
		return false, model.Violation(model.CodeRuntimeFieldInvalid, "operation.lease_ttl",
			"协调者租约必须为正")
	}
	now := nowSeconds()
	res, err := tx.Exec(`UPDATE operations
SET coordinator = ?, lease_expires_at = ?, updated_at = ?
WHERE operation_id = ? AND result = ?
  AND (coordinator = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)`,
		coordinator, formatTS(now.Add(ttl)), formatTS(now), operationID,
		string(model.ResultPending), formatTS(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// CompleteOperation 由当前协调者写入操作结果。
//
// 租约换人后旧协调者的完成请求会被拒（E252 之外的确定性冲突），
// 因为「谁领的谁写结果」是撤销重复派发的最后一道闸。
func CompleteOperation(tx *sql.Tx, operationID, coordinator string, result model.OperationResult) error {
	if !result.Valid() || result == model.ResultPending {
		return model.Violationf(model.CodeRuntimeFieldInvalid, "operation.result",
			"结果必须是 applied/already_finished/rejected，收到 %q", result)
	}
	now := UTCNow()
	res, err := tx.Exec(`UPDATE operations SET result = ?, coordinator = '', lease_expires_at = NULL,
  updated_at = ? WHERE operation_id = ? AND coordinator = ? AND result = ?`,
		string(result), now, operationID, coordinator, string(model.ResultPending))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		op, err := GetOperation(tx, operationID)
		if err != nil {
			return err
		}
		if op == nil {
			return model.Violationf(model.CodeRuntimeFieldInvalid, "operation.operation_id", "%q 不存在", operationID)
		}
		return model.Violationf(model.CodeIdempotencyConflict, "operation.coordinator",
			"%s 不是当前协调者或已完成（现有 coordinator=%q result=%s）", operationID, op.Coordinator, op.Result)
	}
	return nil
}

// AppendRunEvent 追加一条标准事件，返回入库游标。
//
// 防重：event_id 唯一，同一身份补交只入库一次（inserted=false 返回原游标）。
// 时钟：occurred_at 晚于 received_at 说明来源时钟不可信，标 observed 会被拒（E262）。
func AppendRunEvent(tx *sql.Tx, spec RunEventSpec) (model.Cursor, bool, error) {
	if strings.TrimSpace(string(spec.ID)) == "" {
		return 0, false, model.Violation(model.CodeRuntimeFieldInvalid, "event.event_id", "事件必须有身份")
	}
	if spec.SchemaVersion <= 0 {
		return 0, false, model.Violation(model.CodeRuntimeFieldInvalid, "event.schema_version",
			"事件必须带正整数 schema_version")
	}
	if strings.TrimSpace(spec.Kind) == "" {
		return 0, false, model.Violation(model.CodeRuntimeFieldInvalid, "event.kind", "事件必须有类型")
	}
	quality := spec.Quality
	if quality == "" {
		quality = model.QualityObserved
	}
	if !quality.Valid() {
		return 0, false, model.Violationf(model.CodeRuntimeFieldInvalid, "event.quality",
			"只能是 observed/estimated/unknown，收到 %q", quality)
	}
	received := spec.ReceivedAt
	if received.IsZero() {
		received = nowSeconds()
	}
	occurred := spec.OccurredAt
	if occurred.IsZero() {
		occurred = received
	}
	if occurred.After(received) && quality == model.QualityObserved {
		return 0, false, model.Violationf(model.CodeClockSkewMarkedObserved, "event.quality",
			"发生时间 %s 晚于接收时间 %s，必须降级为 estimated/unknown",
			formatTS(occurred), formatTS(received))
	}
	if spec.RunID != "" {
		run, err := GetRun(tx, spec.RunID)
		if err != nil {
			return 0, false, err
		}
		if run == nil {
			return 0, false, model.Violationf(model.CodeEventUnknownRun, "event.run_id",
				"事件挂到不存在的 Run %q", spec.RunID)
		}
	}
	payload := spec.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return 0, false, err
	}
	res, err := tx.Exec(`INSERT INTO run_events(event_id, project_id, run_id, instance_id, kind,
  phase, lease_id, schema_version, quality, occurred_at, received_at, payload_json)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(event_id) DO NOTHING`,
		string(spec.ID), spec.ProjectID, spec.RunID, spec.InstanceID, spec.Kind,
		string(spec.Phase), spec.LeaseID, spec.SchemaVersion, string(quality),
		formatTS(occurred), formatTS(received), string(payloadJSON))
	if err != nil {
		return 0, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var cursor int64
		if err := tx.QueryRow(`SELECT cursor FROM run_events WHERE event_id = ?`, string(spec.ID)).Scan(&cursor); err != nil {
			return 0, false, err
		}
		return model.Cursor(cursor), false, nil
	}
	cursor, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	return model.Cursor(cursor), true, nil
}

// ReadRunEvents 按入库游标分页读取；after 为 0 表示从头。limit<=0 默认 200。
func ReadRunEvents(tx *sql.Tx, projectID string, after model.Cursor, limit int) ([]*RunEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT cursor, event_id, project_id, run_id, instance_id, kind, phase, lease_id,
  schema_version, quality, occurred_at, received_at, payload_json
FROM run_events WHERE cursor > ?`
	args := []any{int64(after)}
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY cursor ASC LIMIT ?`
	args = append(args, limit)
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RunEvent
	for rows.Next() {
		ev, err := scanRunEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// MaxRunCursor 返回当前最大入库游标（没有事件时为 0）。
func MaxRunCursor(tx *sql.Tx, projectID string) (model.Cursor, error) {
	q := `SELECT COALESCE(MAX(cursor), 0) FROM run_events`
	var args []any
	if projectID != "" {
		q += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	var cursor int64
	if err := tx.QueryRow(q, args...).Scan(&cursor); err != nil {
		return 0, err
	}
	return model.Cursor(cursor), nil
}

func createRun(tx *sql.Tx, r *Run) error {
	history, err := json.Marshal(r.PhaseHistory)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(r.Evidence)
	if err != nil {
		return err
	}
	leases, err := json.Marshal(r.Leases)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO runs(run_id, project_id, instance_id, version_no, generation,
  phase, reconciliation_state, desired_action, process_stop_confirmed, delivery_frozen,
  started_at, finished_at, phase_history_json, evidence_json, leases_json, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.ProjectID, r.InstanceID, int(r.VersionNo), int(r.Generation), string(r.Phase),
		string(r.Reconciliation), string(r.DesiredAction), boolInt(r.ProcessStopConfirmed),
		boolInt(r.DeliveryFrozen), nullStr(r.StartedAt), nullStr(r.FinishedAt),
		string(history), string(evidence), string(leases), r.CreatedAt, r.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return model.Violationf(model.CodeDoubleOccupancy, "run.generation",
			"实例 %s 的代际 %d 已存在 Run", r.InstanceID, r.Generation)
	}
	return err
}

func scanRun(row interface{ Scan(dest ...any) error }) (*Run, error) {
	var (
		r                                  Run
		phase, recon, action               string
		stopConfirmed, deliveryFrozen      int
		startedAt, finishedAt              sql.NullString
		historyRaw, evidenceRaw, leasesRaw string
		version, generation                int
	)
	err := row.Scan(&r.ID, &r.ProjectID, &r.InstanceID, &version, &generation, &phase,
		&recon, &action, &stopConfirmed, &deliveryFrozen, &startedAt, &finishedAt,
		&historyRaw, &evidenceRaw, &leasesRaw, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.VersionNo = model.VersionNo(version)
	r.Generation = model.Generation(generation)
	r.Phase = model.RunPhase(phase)
	r.Reconciliation = model.ReconciliationState(recon)
	r.DesiredAction = model.DesiredAction(action)
	r.ProcessStopConfirmed = stopConfirmed != 0
	r.DeliveryFrozen = deliveryFrozen != 0
	r.StartedAt = startedAt.String
	r.FinishedAt = finishedAt.String
	r.PhaseHistory = []model.PhaseEntry{}
	_ = json.Unmarshal([]byte(historyRaw), &r.PhaseHistory)
	r.Evidence = []string{}
	_ = json.Unmarshal([]byte(evidenceRaw), &r.Evidence)
	r.Leases = []model.Lease{}
	_ = json.Unmarshal([]byte(leasesRaw), &r.Leases)
	return &r, nil
}

func scanOperation(row interface{ Scan(dest ...any) error }) (*Operation, error) {
	var (
		op          Operation
		kind, res   string
		targetPhase string
		expires     sql.NullString
	)
	err := row.Scan(&op.ID, &op.ProjectID, &kind, &op.RunID, &op.InstanceID,
		&op.IdempotencyKey, &op.RequestDigest, &targetPhase, &res, &op.Coordinator, &expires,
		&op.CreatedAt, &op.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	op.Kind = model.OperationKind(kind)
	op.TargetPhase = model.RunPhase(targetPhase)
	op.Result = model.OperationResult(res)
	op.LeaseExpiresAt = expires.String
	return &op, nil
}

func scanRunEvent(row interface{ Scan(dest ...any) error }) (*RunEvent, error) {
	var (
		ev        RunEvent
		cursor    int64
		phase     string
		quality   string
		payloadJS string
	)
	err := row.Scan(&cursor, &ev.ID, &ev.ProjectID, &ev.RunID, &ev.InstanceID, &ev.Kind, &phase,
		&ev.LeaseID, &ev.SchemaVersion, &quality, &ev.OccurredAt, &ev.ReceivedAt, &payloadJS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ev.Cursor = model.Cursor(cursor)
	ev.Phase = model.RunPhase(phase)
	ev.Quality = model.Quality(quality)
	ev.Payload = map[string]any{}
	_ = json.Unmarshal([]byte(payloadJS), &ev.Payload)
	return &ev, nil
}

func findOperationByKey(tx *sql.Tx, projectID string, kind model.OperationKind, key string) (*Operation, error) {
	row := tx.QueryRow(`SELECT operation_id, project_id, kind, run_id, instance_id,
  idempotency_key, request_digest, target_phase, result, coordinator, lease_expires_at,
  created_at, updated_at FROM operations
WHERE project_id = ? AND kind = ? AND idempotency_key = ?`, projectID, string(kind), key)
	return scanOperation(row)
}

func activeLeasesOfInstance(tx *sql.Tx, instanceID string) ([]*Lease, error) {
	rows, err := tx.Query(`SELECT lease_id, kind, resource_key, project_id, instance_id, run_id,
  generation, holder, acquired_at, released_at, release_generation, release_reason
FROM resource_leases WHERE instance_id = ? AND released_at IS NULL
ORDER BY acquired_at ASC, lease_id ASC`, instanceID)
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

func maxCursorOfRun(tx *sql.Tx, runID string) (model.Cursor, error) {
	var cursor int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(cursor), 0) FROM run_events WHERE run_id = ?`, runID).
		Scan(&cursor); err != nil {
		return 0, err
	}
	return model.Cursor(cursor), nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// mustID 生成事件身份；随机源失败时退回时间戳，仍保证唯一性由库的 event_id 唯一索引兜底。
func mustID(prefix string) string {
	id, err := newID(prefix)
	if err != nil {
		return prefix + "_" + formatTS(nowSeconds())
	}
	return id
}
