package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kiwh77/maw/internal/goal"
)

// 本文件是目标级 Lead 的持久化层（契约 maw.lead.goal/v1 §2–§9、§11）。
//
// 三条边界：
//   - 目标、目标版本、计划修订、任务映射、入队台账各自一张表，互不覆盖；
//   - 目标的「状态 + revision」是唯一乐观锁：读到的 revision 不匹配就整体失败；
//   - 计划修订一旦批准不可改，只能新修订；同一修订至多 applied 一次。
//
// 蓝图写入、模型调用与 CLI 入口都不在这里（PI-302／PI-303 落地）。

// AnyGoalRevision 表示调用方确实没有期望版本（系统恢复动作）；业务入口必须带真实 revision。
const AnyGoalRevision = -1

var (
	// ErrGoalExists 表示同一工程里已有这个 goal_id（身份永不复用）。
	ErrGoalExists = errors.New("db: 目标已存在")
	// ErrGoalVersionConflict 表示追加的版本号不是「当前最大版本 + 1」。
	ErrGoalVersionConflict = errors.New("db: 目标版本号冲突")
	// ErrGoalPlanConflict 表示计划修订号不是「当前最大修订 + 1」。
	ErrGoalPlanConflict = errors.New("db: 计划修订号冲突")
)

// GoalRevisionMismatch 是目标乐观锁失败的稳定错误形状（对齐 revision_mismatch）。
type GoalRevisionMismatch struct {
	ProjectID string
	GoalID    string
	Expected  int
	Actual    int
}

func (e *GoalRevisionMismatch) Error() string {
	return fmt.Sprintf("revision_mismatch: goal %s/%s expected revision %d, actual %d",
		e.ProjectID, e.GoalID, e.Expected, e.Actual)
}

// Code 是给 HTTP／CLI 的稳定错误码。
func (e *GoalRevisionMismatch) Code() string { return "revision_mismatch" }

// Details 供错误响应体使用。
func (e *GoalRevisionMismatch) Details() map[string]any {
	return map[string]any{"actual_revision": e.Actual, "expected_revision": e.Expected}
}

// IsGoalRevisionMismatch 判断错误链里是否有目标乐观锁失败。
func IsGoalRevisionMismatch(err error) bool {
	var m *GoalRevisionMismatch
	return errors.As(err, &m)
}

const goalCols = `project_id, goal_id, iteration_id, status, active_version, revision, abandon_reason, created_at, updated_at`

func scanGoal(row interface{ Scan(dest ...any) error }) (*goal.Goal, error) {
	var g goal.Goal
	if err := row.Scan(&g.ProjectID, &g.GoalID, &g.IterationID, &g.Status,
		&g.ActiveVersion, &g.Revision, &g.AbandonReason, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, err
	}
	return &g, nil
}

// GetGoal 读取一条目标；不存在时返回 (nil, nil)。
func GetGoal(tx *sql.Tx, projectID, goalID string) (*goal.Goal, error) {
	row := tx.QueryRow(`SELECT `+goalCols+` FROM goals WHERE project_id = ? AND goal_id = ?`, projectID, goalID)
	g, err := scanGoal(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return g, nil
}

// ListGoals 按创建时间列出工程内的目标；也用于重启后重建「当前目标」。
func ListGoals(tx *sql.Tx, projectID string) ([]*goal.Goal, error) {
	rows, err := tx.Query(`SELECT `+goalCols+` FROM goals WHERE project_id = ? ORDER BY created_at ASC, goal_id ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*goal.Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreateGoal 在同一事务里落目标行与它的第 1 版定义。
//
// 版本号必须从 1 开始，且 active_version 立刻指向它：契约 §2 要求「指针等于
// 已冻结最大版本」，没有中间态。
func CreateGoal(tx *sql.Tx, g *goal.Goal, v *goal.Version, actor string) (*goal.Goal, *goal.Version, error) {
	if g == nil || v == nil {
		return nil, nil, errors.New("db: 目标或目标版本为空")
	}
	if err := g.Validate(); err != nil {
		return nil, nil, err
	}
	if v.GoalVersion == 0 {
		v.GoalVersion = 1
	}
	if v.GoalVersion != 1 {
		return nil, nil, fmt.Errorf("%w: 首个版本必须是 1，收到 %d", ErrGoalVersionConflict, v.GoalVersion)
	}
	v.ProjectID, v.GoalID = g.ProjectID, g.GoalID
	if err := v.Validate(); err != nil {
		return nil, nil, err
	}
	existing, err := GetGoal(tx, g.ProjectID, g.GoalID)
	if err != nil {
		return nil, nil, err
	}
	if existing != nil {
		return nil, nil, fmt.Errorf("%w: %s/%s", ErrGoalExists, g.ProjectID, g.GoalID)
	}
	now := UTCNow()
	if g.CreatedAt == "" {
		g.CreatedAt = now
	}
	g.UpdatedAt = now
	g.Status = goal.StatusDraft
	g.ActiveVersion = 1
	g.Revision = 0
	if v.FrozenAt == "" {
		v.FrozenAt = now
	}
	if _, err := tx.Exec(`
INSERT INTO goals(project_id, goal_id, iteration_id, status, active_version, revision, abandon_reason, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
		g.ProjectID, g.GoalID, g.IterationID, g.Status, g.ActiveVersion, g.Revision,
		g.AbandonReason, g.CreatedAt, g.UpdatedAt); err != nil {
		return nil, nil, err
	}
	if err := insertGoalVersion(tx, v); err != nil {
		return nil, nil, err
	}
	if err := AppendEvent(tx, g.ProjectID, actor, "goal_created", "", map[string]any{
		"goal_id": g.GoalID, "iteration_id": g.IterationID, "status": g.Status,
		"active_version": g.ActiveVersion, "revision": g.Revision,
	}); err != nil {
		return nil, nil, err
	}
	return g, v, nil
}

func insertGoalVersion(tx *sql.Tx, v *goal.Version) error {
	accept, err := json.Marshal(v.Acceptance)
	if err != nil {
		return err
	}
	budget, err := json.Marshal(v.Budget)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
INSERT INTO goal_versions(project_id, goal_id, goal_version, title, statement, acceptance_json, budget_json, frozen_at)
VALUES (?,?,?,?,?,?,?,?)`,
		v.ProjectID, v.GoalID, v.GoalVersion, v.Title, v.Statement, string(accept), string(budget), v.FrozenAt)
	return err
}

const goalVersionCols = `project_id, goal_id, goal_version, title, statement, acceptance_json, budget_json, frozen_at`

func scanGoalVersion(row interface{ Scan(dest ...any) error }) (*goal.Version, error) {
	var (
		v      goal.Version
		accept string
		budget string
	)
	if err := row.Scan(&v.ProjectID, &v.GoalID, &v.GoalVersion, &v.Title, &v.Statement,
		&accept, &budget, &v.FrozenAt); err != nil {
		return nil, err
	}
	v.Acceptance = []goal.Acceptance{}
	_ = json.Unmarshal([]byte(accept), &v.Acceptance)
	v.Budget = goal.DefaultBudget()
	_ = json.Unmarshal([]byte(budget), &v.Budget)
	return &v, nil
}

// GetGoalVersion 读取一版目标定义；不存在返回 (nil, nil)。
func GetGoalVersion(tx *sql.Tx, projectID, goalID string, version int) (*goal.Version, error) {
	row := tx.QueryRow(`SELECT `+goalVersionCols+` FROM goal_versions
		WHERE project_id = ? AND goal_id = ? AND goal_version = ?`, projectID, goalID, version)
	v, err := scanGoalVersion(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return v, err
}

// ListGoalVersions 按版本升序列出目标定义快照。
func ListGoalVersions(tx *sql.Tx, projectID, goalID string) ([]*goal.Version, error) {
	rows, err := tx.Query(`SELECT `+goalVersionCols+` FROM goal_versions
		WHERE project_id = ? AND goal_id = ? ORDER BY goal_version ASC`, projectID, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*goal.Version
	for rows.Next() {
		v, err := scanGoalVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// AppendGoalVersion 追加一版冻结定义并把 active_version 推进到它。
//
// expected 必须等于目标的当前 revision（AnyGoalRevision 除外）；版本号必须是
// max+1，指针只前进不回退（契约 G120／G122）。
func AppendGoalVersion(tx *sql.Tx, v *goal.Version, expected int, actor string) (*goal.Goal, error) {
	if v == nil {
		return nil, errors.New("db: 目标版本为空")
	}
	g, err := GetGoal(tx, v.ProjectID, v.GoalID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("%w: %s/%s", goal.ErrGoalNotFound, v.ProjectID, v.GoalID)
	}
	if err := assertGoalRevision(g, expected); err != nil {
		return nil, err
	}
	if v.GoalVersion != g.ActiveVersion+1 {
		return nil, fmt.Errorf("%w: 期望版本 %d，收到 %d", ErrGoalVersionConflict, g.ActiveVersion+1, v.GoalVersion)
	}
	if err := v.Validate(); err != nil {
		return nil, err
	}
	if v.FrozenAt == "" {
		v.FrozenAt = UTCNow()
	}
	if err := insertGoalVersion(tx, v); err != nil {
		return nil, err
	}
	g.ActiveVersion = v.GoalVersion
	g.UpdatedAt = UTCNow()
	g.Revision++
	if err := upsertGoalRow(tx, g); err != nil {
		return nil, err
	}
	if err := AppendEvent(tx, g.ProjectID, actor, "goal_version", "", map[string]any{
		"goal_id": g.GoalID, "goal_version": v.GoalVersion, "active_version": g.ActiveVersion,
		"revision": g.Revision,
	}); err != nil {
		return nil, err
	}
	return g, nil
}

func upsertGoalRow(tx *sql.Tx, g *goal.Goal) error {
	_, err := tx.Exec(`
UPDATE goals SET iteration_id = ?, status = ?, active_version = ?, revision = ?,
  abandon_reason = ?, updated_at = ?
WHERE project_id = ? AND goal_id = ?`,
		g.IterationID, g.Status, g.ActiveVersion, g.Revision, g.AbandonReason, g.UpdatedAt,
		g.ProjectID, g.GoalID)
	return err
}

func assertGoalRevision(g *goal.Goal, expected int) error {
	if expected == AnyGoalRevision {
		return nil
	}
	if g.Revision != expected {
		return &GoalRevisionMismatch{
			ProjectID: g.ProjectID, GoalID: g.GoalID,
			Expected: expected, Actual: g.Revision,
		}
	}
	return nil
}

// UpdateGoalCAS 用「读-改-写」更新目标：expected 必须等于库里的当前 revision。
//
// 状态变化必须落在契约 §3 的迁移集合内；暂停／升级必须给原因，放弃必须写
// abandon_reason。同状态不算迁移，但仍会刷新字段并前进 revision。
func UpdateGoalCAS(tx *sql.Tx, g *goal.Goal, expected int, actor string) (*goal.Goal, error) {
	if g == nil {
		return nil, errors.New("db: 目标为空")
	}
	cur, err := GetGoal(tx, g.ProjectID, g.GoalID)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, fmt.Errorf("%w: %s/%s", goal.ErrGoalNotFound, g.ProjectID, g.GoalID)
	}
	if err := assertGoalRevision(cur, expected); err != nil {
		return nil, err
	}
	if err := validateGoalStatusChange(cur, g); err != nil {
		return nil, err
	}
	cur.Status = g.Status
	if strings.TrimSpace(g.IterationID) != "" {
		cur.IterationID = g.IterationID
	}
	if g.Status == goal.StatusAbandoned {
		cur.AbandonReason = strings.TrimSpace(g.AbandonReason)
	}
	cur.UpdatedAt = UTCNow()
	cur.Revision++
	if err := upsertGoalRow(tx, cur); err != nil {
		return nil, err
	}
	if err := AppendEvent(tx, cur.ProjectID, actor, "goal_updated", "", map[string]any{
		"goal_id": cur.GoalID, "status": cur.Status, "revision": cur.Revision,
		"iteration_id": cur.IterationID,
	}); err != nil {
		return nil, err
	}
	return cur, nil
}

func validateGoalStatusChange(cur, next *goal.Goal) error {
	if next.Status == "" {
		next.Status = cur.Status
	}
	if !goal.ValidStatus(next.Status) {
		return fmt.Errorf("%s: 非法目标状态 %q", goal.CodeGoalTransition, next.Status)
	}
	if next.Status == cur.Status {
		return nil
	}
	if !goal.CanTransition(cur.Status, next.Status) {
		return fmt.Errorf("%w: %s → %s", goal.ErrGoalTransition, cur.Status, next.Status)
	}
	if next.Status == goal.StatusAbandoned && strings.TrimSpace(next.AbandonReason) == "" {
		return fmt.Errorf("%s: 放弃目标必须写 abandon_reason", goal.CodeGoalTransition)
	}
	return nil
}

// SetGoalStatusCAS 是状态迁移的便捷入口：返回最新快照与「是否真的变更」。
//
// 同状态直接返回 changed=false（幂等），不写库、不刷 updated_at；这与契约
// §3「无事件的状态变化视为未发生」一致，重复调用不会把 revision 推高。
func SetGoalStatusCAS(tx *sql.Tx, projectID, goalID, to, reason, actor string, expected int) (*goal.Goal, bool, error) {
	cur, err := GetGoal(tx, projectID, goalID)
	if err != nil {
		return nil, false, err
	}
	if cur == nil {
		return nil, false, fmt.Errorf("%w: %s/%s", goal.ErrGoalNotFound, projectID, goalID)
	}
	to = strings.TrimSpace(to)
	if !goal.ValidStatus(to) {
		return nil, false, fmt.Errorf("%s: 非法目标状态 %q", goal.CodeGoalTransition, to)
	}
	if cur.Status == to {
		return cur, false, nil
	}
	if !goal.CanTransition(cur.Status, to) {
		return nil, false, fmt.Errorf("%w: %s → %s", goal.ErrGoalTransition, cur.Status, to)
	}
	if (to == goal.StatusPaused || to == goal.StatusEscalated) && !goal.ValidReason(reason) {
		return nil, false, fmt.Errorf("%s: %s 必须带合法原因（human_pause/permission_denied/budget_exhausted/invariant_violation）",
			goal.CodeGoalTransition, to)
	}
	next := *cur
	next.Status = to
	if to == goal.StatusAbandoned {
		if strings.TrimSpace(reason) == "" {
			return nil, false, fmt.Errorf("%s: 放弃目标必须写 reason", goal.CodeGoalTransition)
		}
		next.AbandonReason = strings.TrimSpace(reason)
	}
	updated, err := UpdateGoalCAS(tx, &next, expected, actor)
	if err != nil {
		return nil, false, err
	}
	if err := AppendEvent(tx, projectID, actor, "goal_state", "", map[string]any{
		"goal_id": goalID, "from": cur.Status, "to": to, "reason": reason,
		"revision": updated.Revision,
	}); err != nil {
		return nil, false, err
	}
	return updated, true, nil
}

const goalPlanCols = `project_id, goal_id, plan_rev, goal_version, kind, status, baseline_json, plan_digest, cards_json, approval_json, created_at, applied_at`

func scanPlanRevision(row interface{ Scan(dest ...any) error }) (*goal.PlanRevision, error) {
	var (
		r         goal.PlanRevision
		baseline  string
		cards     string
		approval  string
		appliedAt sql.NullString
	)
	if err := row.Scan(&r.ProjectID, &r.GoalID, &r.PlanRev, &r.GoalVersion, &r.Kind, &r.Status,
		&baseline, &r.PlanDigest, &cards, &approval, &r.CreatedAt, &appliedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(baseline), &r.Baseline)
	r.Cards = []goal.PlanCard{}
	_ = json.Unmarshal([]byte(cards), &r.Cards)
	_ = json.Unmarshal([]byte(approval), &r.Approval)
	if appliedAt.Valid {
		r.AppliedAt = appliedAt.String
	}
	return &r, nil
}

// GetPlanRevision 读取一份计划修订；不存在返回 (nil, nil)。
func GetPlanRevision(tx *sql.Tx, projectID, goalID string, planRev int) (*goal.PlanRevision, error) {
	row := tx.QueryRow(`SELECT `+goalPlanCols+` FROM goal_plan_revisions
		WHERE project_id = ? AND goal_id = ? AND plan_rev = ?`, projectID, goalID, planRev)
	r, err := scanPlanRevision(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// ListPlanRevisions 按修订号升序列出目标的计划修订。
func ListPlanRevisions(tx *sql.Tx, projectID, goalID string) ([]*goal.PlanRevision, error) {
	rows, err := tx.Query(`SELECT `+goalPlanCols+` FROM goal_plan_revisions
		WHERE project_id = ? AND goal_id = ? ORDER BY plan_rev ASC`, projectID, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*goal.PlanRevision
	for rows.Next() {
		r, err := scanPlanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func insertPlanRevision(tx *sql.Tx, r *goal.PlanRevision) error {
	baseline, err := json.Marshal(r.Baseline)
	if err != nil {
		return err
	}
	cards, err := json.Marshal(r.Cards)
	if err != nil {
		return err
	}
	approval, err := json.Marshal(r.Approval)
	if err != nil {
		return err
	}
	var applied any
	if r.AppliedAt != "" {
		applied = r.AppliedAt
	}
	_, err = tx.Exec(`
INSERT INTO goal_plan_revisions(project_id, goal_id, plan_rev, goal_version, kind, status,
  baseline_json, plan_digest, cards_json, approval_json, created_at, applied_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ProjectID, r.GoalID, r.PlanRev, r.GoalVersion, r.Kind, r.Status,
		string(baseline), r.PlanDigest, string(cards), string(approval), r.CreatedAt, applied)
	return err
}

func updatePlanRevision(tx *sql.Tx, r *goal.PlanRevision) error {
	approval, err := json.Marshal(r.Approval)
	if err != nil {
		return err
	}
	var applied any
	if r.AppliedAt != "" {
		applied = r.AppliedAt
	}
	_, err = tx.Exec(`
UPDATE goal_plan_revisions SET status = ?, approval_json = ?, applied_at = ?
WHERE project_id = ? AND goal_id = ? AND plan_rev = ?`,
		r.Status, string(approval), applied, r.ProjectID, r.GoalID, r.PlanRev)
	return err
}

// SavePlanRevision 落一份**新的**计划修订（proposed）。
//
// 修订号必须等于「当前最大修订 + 1」，摘要按内容补算并核对；已有修订一律不改，
// 只能新建（契约 G201／G200）。
func SavePlanRevision(tx *sql.Tx, r *goal.PlanRevision, actor string) (*goal.PlanRevision, error) {
	if r == nil {
		return nil, errors.New("db: 计划修订为空")
	}
	if r.Status == "" {
		r.Status = goal.PlanProposed
	}
	if r.Status != goal.PlanProposed {
		return nil, fmt.Errorf("%w: 新修订只能以 proposed 落库，收到 %s", goal.ErrPlanTransition, r.Status)
	}
	if err := r.EnsureDigest(); err != nil {
		return nil, err
	}
	if vs := r.Validate(); len(vs) > 0 {
		return nil, vs.Err()
	}
	existing, err := GetPlanRevision(tx, r.ProjectID, r.GoalID, r.PlanRev)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("%w: %s/%s rev %d 已存在", ErrGoalPlanConflict, r.ProjectID, r.GoalID, r.PlanRev)
	}
	latest, err := latestPlanRev(tx, r.ProjectID, r.GoalID)
	if err != nil {
		return nil, err
	}
	if r.PlanRev != latest+1 {
		return nil, fmt.Errorf("%w: 期望修订 %d，收到 %d", ErrGoalPlanConflict, latest+1, r.PlanRev)
	}
	if r.Approval.State == "" {
		r.Approval.State = goal.ApprovalPending
	}
	if r.CreatedAt == "" {
		r.CreatedAt = UTCNow()
	}
	if err := insertPlanRevision(tx, r); err != nil {
		return nil, err
	}
	if err := AppendEvent(tx, r.ProjectID, actor, "goal_plan_proposed", "", map[string]any{
		"goal_id": r.GoalID, "plan_rev": r.PlanRev, "kind": r.Kind,
		"plan_digest": r.PlanDigest, "cards": len(r.Cards),
	}); err != nil {
		return nil, err
	}
	return r, nil
}

func latestPlanRev(tx *sql.Tx, projectID, goalID string) (int, error) {
	var latest int
	err := tx.QueryRow(`SELECT COALESCE(MAX(plan_rev), 0) FROM goal_plan_revisions
		WHERE project_id = ? AND goal_id = ?`, projectID, goalID).Scan(&latest)
	return latest, err
}

// ApprovePlanRevision 记录一次审批：批准的是**这一份摘要**，不是「这个修订号」。
//
// 幂等：同摘要、同批准人重复批准返回 changed=false；摘要不符返回
// goal.ErrApprovalDigest，绝不允许「先批准再改内容」。
func ApprovePlanRevision(tx *sql.Tx, projectID, goalID string, planRev int, approver, policy, digest, actor string) (*goal.PlanRevision, bool, error) {
	if strings.TrimSpace(approver) == "" {
		return nil, false, fmt.Errorf("%s: 批准必须记录 approver", goal.CodePlanDigest)
	}
	if !goal.ValidPolicySource(policy) {
		return nil, false, fmt.Errorf("%s: 非法审批策略来源 %q（manual/auto_enqueue）", goal.CodePlanDigest, policy)
	}
	r, err := GetPlanRevision(tx, projectID, goalID, planRev)
	if err != nil {
		return nil, false, err
	}
	if r == nil {
		return nil, false, fmt.Errorf("%w: %s/%s rev %d", goal.ErrPlanNotFound, projectID, goalID, planRev)
	}
	want, err := goal.Digest(r)
	if err != nil {
		return nil, false, err
	}
	if !strings.EqualFold(strings.TrimSpace(want), strings.TrimSpace(r.PlanDigest)) {
		return nil, false, fmt.Errorf("%w: 库内摘要与内容不一致（修订被改过）", goal.ErrApprovalDigest)
	}
	if strings.TrimSpace(digest) == "" || !strings.EqualFold(strings.TrimSpace(digest), want) {
		return nil, false, fmt.Errorf("%w: 批准摘要 %q 与修订 %q 不一致", goal.ErrApprovalDigest, digest, want)
	}
	switch r.Status {
	case goal.PlanApproved:
		if strings.EqualFold(r.Approval.Approver, approver) && strings.EqualFold(r.Approval.PolicySource, policy) {
			return r, false, nil
		}
		return nil, false, fmt.Errorf("%w: 已由 %s 批准，不能改批准人", goal.ErrPlanTransition, r.Approval.Approver)
	case goal.PlanProposed:
	default:
		return nil, false, fmt.Errorf("%w: %s 不能批准", goal.ErrPlanTransition, r.Status)
	}
	r.Status = goal.PlanApproved
	r.Approval = goal.Approval{
		State: goal.ApprovalApproved, Approver: approver,
		ApprovedAt: UTCNow(), PolicySource: policy,
	}
	if err := updatePlanRevision(tx, r); err != nil {
		return nil, false, err
	}
	if err := AppendEvent(tx, projectID, actor, "goal_plan_approved", "", map[string]any{
		"goal_id": goalID, "plan_rev": planRev, "plan_digest": r.PlanDigest,
		"approver": approver, "policy_source": policy,
	}); err != nil {
		return nil, false, err
	}
	return r, true, nil
}

// InvalidatePlanRevision 让未入队的修订失效（契约 §9），已入队的一律不动。
func InvalidatePlanRevision(tx *sql.Tx, projectID, goalID string, planRev int, reason, actor string) (*goal.PlanRevision, bool, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, false, errors.New("db: 失效必须写 reason")
	}
	r, err := GetPlanRevision(tx, projectID, goalID, planRev)
	if err != nil {
		return nil, false, err
	}
	if r == nil {
		return nil, false, fmt.Errorf("%w: %s/%s rev %d", goal.ErrPlanNotFound, projectID, goalID, planRev)
	}
	switch r.Status {
	case goal.PlanInvalidated:
		return r, false, nil
	case goal.PlanProposed, goal.PlanApproved:
	default:
		return nil, false, fmt.Errorf("%w: %s 不能失效（已入队或已终结）", goal.ErrPlanTransition, r.Status)
	}
	r.Status = goal.PlanInvalidated
	r.Approval.State = goal.ApprovalExpired
	if err := updatePlanRevision(tx, r); err != nil {
		return nil, false, err
	}
	if err := AppendEvent(tx, projectID, actor, "goal_plan_invalidated", "", map[string]any{
		"goal_id": goalID, "plan_rev": planRev, "reason": reason,
	}); err != nil {
		return nil, false, err
	}
	return r, true, nil
}

// MarkPlanRevisionApplied 把已批准的修订标成 applied（契约 G204：至多一次）。
//
// 重复调用返回 changed=false：调用方据此判断「这是重放」而不是「又入队了一次」。
func MarkPlanRevisionApplied(tx *sql.Tx, projectID, goalID string, planRev int, actor string) (*goal.PlanRevision, bool, error) {
	r, err := GetPlanRevision(tx, projectID, goalID, planRev)
	if err != nil {
		return nil, false, err
	}
	if r == nil {
		return nil, false, fmt.Errorf("%w: %s/%s rev %d", goal.ErrPlanNotFound, projectID, goalID, planRev)
	}
	switch r.Status {
	case goal.PlanApplied:
		return r, false, nil
	case goal.PlanApproved:
	default:
		return nil, false, fmt.Errorf("%w: %s 不能入队", goal.ErrPlanTransition, r.Status)
	}
	r.Status = goal.PlanApplied
	if r.AppliedAt == "" {
		r.AppliedAt = UTCNow()
	}
	if err := updatePlanRevision(tx, r); err != nil {
		return nil, false, err
	}
	if err := AppendEvent(tx, projectID, actor, "goal_plan_applied", "", map[string]any{
		"goal_id": goalID, "plan_rev": planRev, "plan_digest": r.PlanDigest,
	}); err != nil {
		return nil, false, err
	}
	return r, true, nil
}

const goalLinkCols = `project_id, goal_id, plan_rev, card_id, instance_id, display_id, version_no, enqueue_key, state, applied_at, applied_by`

func scanTaskLink(row interface{ Scan(dest ...any) error }) (*goal.TaskLink, error) {
	var l goal.TaskLink
	if err := row.Scan(&l.ProjectID, &l.GoalID, &l.PlanRev, &l.CardID, &l.InstanceID,
		&l.DisplayID, &l.VersionNo, &l.EnqueueKey, &l.State, &l.AppliedAt, &l.AppliedBy); err != nil {
		return nil, err
	}
	return &l, nil
}

// RecordTaskLink 落一条任务映射，并保证它幂等（契约 §7、G112）。
//
// 同一个 (project, instance) 只能有一条映射：重放同样的绑定返回 created=false；
// 换成别的目标／修订／卡一律 ErrTaskLinkConflict，绝不静默覆盖。
func RecordTaskLink(tx *sql.Tx, l *goal.TaskLink) (*goal.TaskLink, bool, error) {
	if l == nil {
		return nil, false, errors.New("db: 任务映射为空")
	}
	if err := l.Validate(); err != nil {
		return nil, false, err
	}
	if l.AppliedAt == "" {
		l.AppliedAt = UTCNow()
	}
	cur, err := GetTaskLink(tx, l.ProjectID, l.InstanceID)
	if err != nil {
		return nil, false, err
	}
	if cur != nil {
		if cur.SameBinding(l) {
			return cur, false, nil
		}
		return nil, false, fmt.Errorf("%w: 实例 %s 已映射到 %s rev %d 卡 %s，不能再映射到 %s rev %d 卡 %s",
			goal.ErrTaskLinkConflict, l.InstanceID,
			cur.GoalID, cur.PlanRev, cur.CardID, l.GoalID, l.PlanRev, l.CardID)
	}
	byKey, err := getTaskLinkByKey(tx, l.ProjectID, l.EnqueueKey)
	if err != nil {
		return nil, false, err
	}
	if byKey != nil {
		return nil, false, fmt.Errorf("%w: enqueue_key %s 已绑定实例 %s",
			goal.ErrTaskLinkConflict, l.EnqueueKey, byKey.InstanceID)
	}
	if _, err := tx.Exec(`
INSERT INTO goal_task_links(project_id, goal_id, plan_rev, card_id, instance_id, display_id,
  version_no, enqueue_key, state, applied_at, applied_by)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		l.ProjectID, l.GoalID, l.PlanRev, l.CardID, l.InstanceID, l.DisplayID,
		l.VersionNo, l.EnqueueKey, l.State, l.AppliedAt, l.AppliedBy); err != nil {
		return nil, false, err
	}
	return l, true, nil
}

// GetTaskLink 按任务实例读取映射；不存在返回 (nil, nil)。
func GetTaskLink(tx *sql.Tx, projectID, instanceID string) (*goal.TaskLink, error) {
	row := tx.QueryRow(`SELECT `+goalLinkCols+` FROM goal_task_links
		WHERE project_id = ? AND instance_id = ?`, projectID, instanceID)
	l, err := scanTaskLink(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return l, err
}

func getTaskLinkByKey(tx *sql.Tx, projectID, enqueueKey string) (*goal.TaskLink, error) {
	row := tx.QueryRow(`SELECT `+goalLinkCols+` FROM goal_task_links
		WHERE project_id = ? AND enqueue_key = ?`, projectID, enqueueKey)
	l, err := scanTaskLink(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return l, err
}

// ListTaskLinks 列出目标已映射的任务，按入队时间升序。
func ListTaskLinks(tx *sql.Tx, projectID, goalID string) ([]*goal.TaskLink, error) {
	rows, err := tx.Query(`SELECT `+goalLinkCols+` FROM goal_task_links
		WHERE project_id = ? AND goal_id = ? ORDER BY applied_at ASC, card_id ASC`, projectID, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*goal.TaskLink
	for rows.Next() {
		l, err := scanTaskLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// MarkTaskLinkDrifted 把映射标成 drifted（蓝图被人在修订之外改动）。
//
// 只改状态，不删行：删掉会让「这张卡曾经入过队」这段历史消失。
func MarkTaskLinkDrifted(tx *sql.Tx, projectID, instanceID, actor string) (bool, error) {
	cur, err := GetTaskLink(tx, projectID, instanceID)
	if err != nil {
		return false, err
	}
	if cur == nil {
		return false, fmt.Errorf("%w: 实例 %s", goal.ErrTaskLinkConflict, instanceID)
	}
	if cur.State == goal.LinkDrifted {
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE goal_task_links SET state = ? WHERE project_id = ? AND instance_id = ?`,
		goal.LinkDrifted, projectID, instanceID); err != nil {
		return false, err
	}
	if err := AppendEvent(tx, projectID, actor, "goal_link_drifted", "", map[string]any{
		"goal_id": cur.GoalID, "plan_rev": cur.PlanRev, "card_id": cur.CardID,
		"instance_id": instanceID,
	}); err != nil {
		return false, err
	}
	return true, nil
}

const goalEnqueueCols = `project_id, enqueue_key, goal_id, plan_rev, card_id, plan_digest, state, reason, updated_at`

func scanEnqueueRecord(row interface{ Scan(dest ...any) error }) (*goal.EnqueueRecord, error) {
	var e goal.EnqueueRecord
	if err := row.Scan(&e.ProjectID, &e.EnqueueKey, &e.GoalID, &e.PlanRev, &e.CardID,
		&e.PlanDigest, &e.State, &e.Reason, &e.UpdatedAt); err != nil {
		return nil, err
	}
	return &e, nil
}

// GetEnqueueRecord 按幂等键读取台账行；不存在返回 (nil, nil)。
func GetEnqueueRecord(tx *sql.Tx, projectID, enqueueKey string) (*goal.EnqueueRecord, error) {
	row := tx.QueryRow(`SELECT `+goalEnqueueCols+` FROM goal_enqueues
		WHERE project_id = ? AND enqueue_key = ?`, projectID, enqueueKey)
	e, err := scanEnqueueRecord(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return e, err
}

// ListEnqueueRecords 列出目标的台账行，按更新时间升序。
func ListEnqueueRecords(tx *sql.Tx, projectID, goalID string) ([]*goal.EnqueueRecord, error) {
	rows, err := tx.Query(`SELECT `+goalEnqueueCols+` FROM goal_enqueues
		WHERE project_id = ? AND goal_id = ? ORDER BY updated_at ASC, enqueue_key ASC`, projectID, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*goal.EnqueueRecord
	for rows.Next() {
		e, err := scanEnqueueRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecordEnqueue 写一条入队台账并给出「是否重放」。
//
// 同键同摘要：
//   - 状态不变 → replayed=true（调用方直接返回原映射）；
//   - appended → applied/failed、failed → appended 允许推进，replayed=false；
//   - applied → 其它一律拒绝：applied 是终态，重放不得再改结论。
//
// 同键异摘要 → goal.ErrEnqueueKeyReuse（契约 G301，不得以最新请求为准）。
func RecordEnqueue(tx *sql.Tx, e *goal.EnqueueRecord) (*goal.EnqueueRecord, bool, error) {
	if e == nil {
		return nil, false, errors.New("db: 入队台账为空")
	}
	if err := e.Validate(); err != nil {
		return nil, false, err
	}
	if e.UpdatedAt == "" {
		e.UpdatedAt = UTCNow()
	}
	cur, err := GetEnqueueRecord(tx, e.ProjectID, e.EnqueueKey)
	if err != nil {
		return nil, false, err
	}
	if cur == nil {
		_, err := tx.Exec(`
INSERT INTO goal_enqueues(project_id, enqueue_key, goal_id, plan_rev, card_id, plan_digest, state, reason, updated_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
			e.ProjectID, e.EnqueueKey, e.GoalID, e.PlanRev, e.CardID, e.PlanDigest, e.State, e.Reason, e.UpdatedAt)
		if err != nil {
			return nil, false, err
		}
		return e, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(cur.PlanDigest), strings.TrimSpace(e.PlanDigest)) {
		return nil, false, fmt.Errorf("%w: 键 %s 已绑定摘要 %s，收到 %s",
			goal.ErrEnqueueKeyReuse, e.EnqueueKey, cur.PlanDigest, e.PlanDigest)
	}
	if cur.State == e.State {
		return cur, true, nil
	}
	if cur.State == goal.EnqueueApplied {
		return nil, false, fmt.Errorf("%w: 键 %s 已 applied，不能再改成 %s",
			goal.ErrPlanTransition, e.EnqueueKey, e.State)
	}
	if cur.State == goal.EnqueueFailed && e.State == goal.EnqueueApplied {
		return nil, false, fmt.Errorf("%w: 失败台账不能直接改 applied，请重新入队并换新键",
			goal.ErrPlanTransition)
	}
	if _, err := tx.Exec(`UPDATE goal_enqueues SET state = ?, reason = ?, updated_at = ?
		WHERE project_id = ? AND enqueue_key = ?`,
		e.State, e.Reason, e.UpdatedAt, e.ProjectID, e.EnqueueKey); err != nil {
		return nil, false, err
	}
	cur.State, cur.Reason, cur.UpdatedAt = e.State, e.Reason, e.UpdatedAt
	return cur, false, nil
}

// GoalSnapshot 是重启后重建目标状态所需的全部持久化证据（契约 §11 第 1–3 步）。
type GoalSnapshot struct {
	Goal      *goal.Goal            `json:"goal"`
	Active    *goal.Version         `json:"active_version"`
	Versions  []*goal.Version       `json:"versions"`
	Plans     []*goal.PlanRevision  `json:"plans"`
	TaskLinks []*goal.TaskLink      `json:"task_links"`
	Enqueues  []*goal.EnqueueRecord `json:"enqueues"`
	Tasks     []*Task               `json:"tasks"`
}

// LoadGoalSnapshot 一次性读出目标、当前定义、全部修订、映射、台账与所关联任务。
//
// 重启恢复只信这份证据：内存里的进度一律丢弃（契约 G500）。
func LoadGoalSnapshot(tx *sql.Tx, projectID, goalID string) (*GoalSnapshot, error) {
	g, err := GetGoal(tx, projectID, goalID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("%w: %s/%s", goal.ErrGoalNotFound, projectID, goalID)
	}
	v, err := GetGoalVersion(tx, projectID, goalID, g.ActiveVersion)
	if err != nil {
		return nil, err
	}
	versions, err := ListGoalVersions(tx, projectID, goalID)
	if err != nil {
		return nil, err
	}
	plans, err := ListPlanRevisions(tx, projectID, goalID)
	if err != nil {
		return nil, err
	}
	links, err := ListTaskLinks(tx, projectID, goalID)
	if err != nil {
		return nil, err
	}
	enqueues, err := ListEnqueueRecords(tx, projectID, goalID)
	if err != nil {
		return nil, err
	}
	tasks, err := tasksByInstances(tx, projectID, links)
	if err != nil {
		return nil, err
	}
	return &GoalSnapshot{
		Goal: g, Active: v, Versions: versions, Plans: plans,
		TaskLinks: links, Enqueues: enqueues, Tasks: tasks,
	}, nil
}

func tasksByInstances(tx *sql.Tx, projectID string, links []*goal.TaskLink) ([]*Task, error) {
	if len(links) == 0 {
		return []*Task{}, nil
	}
	seen := map[string]bool{}
	var args []any
	args = append(args, projectID)
	for _, l := range links {
		if l == nil || l.InstanceID == "" || seen[l.InstanceID] {
			continue
		}
		seen[l.InstanceID] = true
		args = append(args, l.InstanceID)
	}
	if len(args) == 1 {
		return []*Task{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)-1), ",")
	rows, err := tx.Query(`SELECT `+taskCols+` FROM tasks WHERE project_id = ? AND instance_id IN (`+placeholders+`)
		ORDER BY instance_id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
