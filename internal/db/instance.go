package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// 任务实例与冻结版本（契约 maw.task.instance/v1）。
//
// 三条落库规则：
//
//  1. instance_id 不从 display_id 派生：重建同一编号必须换身份，历史才不会串。
//  2. 当前状态只由 tasks 持有；task_instances 只存身份与退役锚点，不是第二份可变状态。
//  3. 版本一经写入不再改：库上有触发器拦截 UPDATE／DELETE，代码里也没有改版本的语句。
//
// tasks 的物理主键仍是 (project_id, id)；id 继续充当 display_id。
const (
	// RetireRemovedFromPlan 是「蓝图外剔除」的退役原因（契约 §3.1）。
	RetireRemovedFromPlan = "removed_from_plan"
	// RetireReset 是 plan apply --reset 全量重建时的退役原因。
	RetireReset = "reset"
	// RetireAmbiguousLegacy 是「重复创建且证据不足」的归档原因（E161）。
	RetireAmbiguousLegacy = "ambiguous_legacy"
)

const instanceCols = `instance_id, project_id, display_id, state, created_at,
	retired_at, retired_reason, active_version, ambiguous, ambiguity_note`

// InstanceDraft 是一次冻结的输入：标题、依赖与规范快照。
type InstanceDraft struct {
	Title string
	Deps  []model.Dep
	Spec  map[string]any
}

// NewInstanceID 生成新的实例身份：ti_ + 12 位小写字母数字（E111）。
//
// 用随机数而不是从编号派生：同一编号被重建时必须拿到新身份（契约 §1「永不复用」）。
func NewInstanceID() (model.InstanceID, error) {
	id, err := newID("ti")
	if err != nil {
		return "", err
	}
	return model.InstanceID(id), nil
}

// newID 生成 <prefix>_<12 位小写字母数字> 的随机身份，供实例与占用共用。
func newID(prefix string) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return prefix + "_" + string(buf), nil
}

// legacyInstanceID 为存量行生成确定性身份，供迁移重放保持幂等。
func legacyInstanceID(projectID, displayID string) model.InstanceID {
	sum := sha256.Sum256([]byte(projectID + "/" + displayID))
	return model.InstanceID("ti_" + hex.EncodeToString(sum[:])[:16])
}

func parseTS(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func formatTS(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// nowSeconds 取秒级当前时间，与库里 UTCNow 的精度保持一致。
func nowSeconds() time.Time { return time.Now().UTC().Truncate(time.Second) }

// CreateInstance 为 (project, display) 建立活跃实例并冻结 v1。
//
// 若该编号已有活跃实例则返回 E120，调用方必须先显式退役旧实例：
// 「同名重建」不是覆盖，而是换一个身份。
func CreateInstance(tx *sql.Tx, projectID, displayID string, draft InstanceDraft) (*model.TaskInstance, error) {
	existing, err := ActiveInstance(tx, projectID, displayID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, model.Violationf(model.CodeActiveAliasConflict, "instance.display_id",
			"%s 已由活跃实例 %s 持有", displayID, existing.InstanceID)
	}
	id, err := NewInstanceID()
	if err != nil {
		return nil, err
	}
	now := UTCNow()
	_, err = tx.Exec(`
INSERT INTO task_instances(instance_id, project_id, display_id, state, created_at,
  retired_at, retired_reason, active_version, ambiguous, ambiguity_note)
VALUES (?,?,?,'active',?,NULL,NULL,1,0,NULL)`, string(id), projectID, displayID, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, model.Violationf(model.CodeActiveAliasConflict, "instance.display_id",
				"%s 已由其它活跃实例持有", displayID)
		}
		return nil, err
	}
	if _, err := insertVersion(tx, string(id), 1, parseTS(now), draft); err != nil {
		return nil, err
	}
	return GetInstance(tx, string(id))
}

// FreezeVersion 追加一版定义并把活跃指针前进到它（E131／E132／E133／E142）。
//
// 允许回退指针会让同一任务在不同时刻读两套验收，因此这里只追加、只前进。
func FreezeVersion(tx *sql.Tx, instanceID string, draft InstanceDraft) (*model.TaskVersion, error) {
	inst, err := GetInstance(tx, instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, model.Violationf(model.CodeTaskFieldInvalid, "instance.instance_id", "%q 不存在", instanceID)
	}
	if inst.State == model.InstanceStateRetired {
		return nil, model.Violationf(model.CodeFrozenAfterRetire, "instance.versions",
			"%s 已于 %s 退役，不得再冻结新版本", instanceID, tsOrDash(inst.RetiredAt))
	}
	if len(inst.Versions) == 0 {
		return nil, model.Violationf(model.CodeVersionSequence, "instance.versions",
			"%s 没有任何冻结版本，v1 必须随实例创建写入", instanceID)
	}
	last := inst.Versions[len(inst.Versions)-1]
	// 库里的时间戳精度是秒，比较前先截断，否则「同一秒内第二次冻结」会被误判为严格递增。
	frozenAt := nowSeconds()
	if !frozenAt.After(last.FrozenAt) {
		// 同一秒内连续冻结时把时间推到上一版之后，保证 E133 严格递增。
		frozenAt = last.FrozenAt.Add(time.Second)
	}
	next := int(last.VersionNo) + 1
	if _, err := insertVersion(tx, instanceID, next, frozenAt, draft); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE task_instances SET active_version = ? WHERE instance_id = ? AND state = 'active'`,
		next, instanceID); err != nil {
		return nil, err
	}
	ver := model.TaskVersion{
		VersionNo: model.VersionNo(next),
		FrozenAt:  frozenAt,
		Title:     draft.Title,
		Deps:      normalizedDeps(draft.Deps),
	}
	return &ver, nil
}

func insertVersion(tx *sql.Tx, instanceID string, versionNo int, frozenAt time.Time, draft InstanceDraft) (int64, error) {
	deps := normalizedDeps(draft.Deps)
	depsJSON, err := json.Marshal(deps)
	if err != nil {
		return 0, err
	}
	spec := draft.Spec
	if spec == nil {
		spec = map[string]any{}
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return 0, err
	}
	res, err := tx.Exec(`
INSERT INTO task_versions(instance_id, version_no, frozen_at, title, deps_json, spec_json)
VALUES (?,?,?,?,?,?)`, instanceID, versionNo, formatTS(frozenAt), draft.Title, string(depsJSON), string(specJSON))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func normalizedDeps(deps []model.Dep) []model.Dep {
	out := make([]model.Dep, 0, len(deps))
	for _, d := range deps {
		out = append(out, model.Dep{Kind: d.Kind, Target: d.Target})
	}
	return out
}

// RetireInstance 在一个事务里完成退役归档与别名释放。
//
// 归档：实例转为 retired、写退役时间与原因、清空活跃指针；版本快照原样保留。
// 别名释放：删掉 tasks 里持有该实例的行——物理主键只能由活跃实例持有（契约 §3），
// 退役锚点不与 tasks 争主键。重复退役是幂等的，直接返回 nil。
func RetireInstance(tx *sql.Tx, instanceID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return model.Violation(model.CodeRetiredIncomplete, "instance.retired_reason", "退役必须写原因")
	}
	inst, err := GetInstance(tx, instanceID)
	if err != nil {
		return err
	}
	if inst == nil {
		return model.Violationf(model.CodeTaskFieldInvalid, "instance.instance_id", "%q 不存在", instanceID)
	}
	if inst.State == model.InstanceStateRetired {
		return nil
	}
	retiredAt := nowSeconds()
	if n := len(inst.Versions); n > 0 && !retiredAt.After(inst.Versions[n-1].FrozenAt) {
		retiredAt = inst.Versions[n-1].FrozenAt.Add(time.Second)
	}
	if _, err := tx.Exec(`
UPDATE task_instances
SET state = 'retired', retired_at = ?, retired_reason = ?, active_version = NULL
WHERE instance_id = ? AND state = 'active'`, formatTS(retiredAt), reason, instanceID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE project_id = ? AND instance_id = ?`,
		inst.Legacy.ProjectID, instanceID); err != nil {
		return err
	}
	return nil
}

// RecordAmbiguousAnchor 记录「重复创建且证据不足」的退役锚点（E161）。
//
// 歧义历史只能当锚点：它不可活跃、必须写说明，也永远不参与依赖解析的唯一命中。
func RecordAmbiguousAnchor(tx *sql.Tx, projectID, displayID, note string, draft InstanceDraft) (*model.TaskInstance, error) {
	if strings.TrimSpace(note) == "" {
		return nil, model.Violation(model.CodeTaskFieldInvalid, "instance.ambiguity_note",
			"歧义锚点必须写明为什么无法判定归属")
	}
	id, err := NewInstanceID()
	if err != nil {
		return nil, err
	}
	now := nowSeconds()
	_, err = tx.Exec(`
INSERT INTO task_instances(instance_id, project_id, display_id, state, created_at,
  retired_at, retired_reason, active_version, ambiguous, ambiguity_note)
VALUES (?,?,?,'retired',?,?,?,NULL,1,?)`,
		string(id), projectID, displayID, formatTS(now), formatTS(now), RetireAmbiguousLegacy, note)
	if err != nil {
		return nil, err
	}
	if _, err := insertVersion(tx, string(id), 1, now, draft); err != nil {
		return nil, err
	}
	return GetInstance(tx, string(id))
}

// GetInstance 返回实例及其全部冻结版本；不存在返回 nil。
func GetInstance(tx *sql.Tx, instanceID string) (*model.TaskInstance, error) {
	row := tx.QueryRow(`SELECT `+instanceCols+` FROM task_instances WHERE instance_id = ?`, instanceID)
	inst, err := scanInstance(row)
	if err == sql.ErrNoRows || inst == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return inst, loadVersions(tx, inst)
}

// ActiveInstance 返回该编号当前的活跃实例；退役锚点不算。
func ActiveInstance(tx *sql.Tx, projectID, displayID string) (*model.TaskInstance, error) {
	row := tx.QueryRow(`SELECT `+instanceCols+`
FROM task_instances WHERE project_id = ? AND display_id = ? AND state = 'active'`, projectID, displayID)
	inst, err := scanInstance(row)
	if err == sql.ErrNoRows || inst == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return inst, loadVersions(tx, inst)
}

// ListInstances 按创建顺序返回某工程的全部实例（活跃 + 退役锚点）。
func ListInstances(tx *sql.Tx, projectID string) ([]*model.TaskInstance, error) {
	rows, err := tx.Query(`SELECT `+instanceCols+`
FROM task_instances WHERE project_id = ? ORDER BY created_at ASC, instance_id ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.TaskInstance
	for rows.Next() {
		inst, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		if err := loadVersions(tx, inst); err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, rows.Err()
}

// DisplayHistory 返回某编号的完整身份历史：至多一个活跃实例 + N 个退役锚点。
func DisplayHistory(tx *sql.Tx, projectID, displayID string) ([]*model.TaskInstance, error) {
	all, err := ListInstances(tx, projectID)
	if err != nil {
		return nil, err
	}
	var out []*model.TaskInstance
	for _, inst := range all {
		if string(inst.DisplayID) == displayID {
			out = append(out, inst)
		}
	}
	return out, nil
}

// ResolveDependency 按契约 §5 的顺序解析一条依赖，返回目标实例。
func ResolveDependency(tx *sql.Tx, projectID, selfInstanceID string, dep model.Dep) (string, error) {
	switch dep.Kind {
	case model.DepInstance:
		inst, err := GetInstance(tx, dep.Target)
		if err != nil {
			return "", err
		}
		if inst == nil {
			return "", model.Violationf(model.CodeDepMissing, "deps", "实例 %q 不在本工程内", dep.Target)
		}
		if inst.Legacy.ProjectID != model.ProjectID(projectID) {
			return "", model.Violationf(model.CodeDepMissing, "deps", "实例 %q 属于其它工程", dep.Target)
		}
		return checkDepTarget(inst, selfInstanceID)
	case model.DepLegacy:
		hits, err := DisplayHistory(tx, projectID, dep.Target)
		if err != nil {
			return "", err
		}
		switch len(hits) {
		case 0:
			return "", model.Violationf(model.CodeDepMissing, "deps", "显示编号 %q 在本工程内不存在", dep.Target)
		case 1:
			return checkDepTarget(hits[0], selfInstanceID)
		default:
			return "", model.Violationf(model.CodeDepAmbiguous, "deps",
				"显示编号 %q 命中 %d 个实例（含退役锚点），必须改用 instance 形式消歧", dep.Target, len(hits))
		}
	default:
		return "", model.Violationf(model.CodeTaskFieldInvalid, "deps.kind",
			"只能是 instance 或 legacy，收到 %q", dep.Kind)
	}
}

func checkDepTarget(inst *model.TaskInstance, selfInstanceID string) (string, error) {
	if string(inst.InstanceID) == selfInstanceID {
		return "", model.Violation(model.CodeDepSelf, "deps", "不得依赖自身实例")
	}
	if inst.State == model.InstanceStateRetired {
		return "", model.Violationf(model.CodeDepRetired, "deps", "%s 已退役，依赖不可满足", inst.InstanceID)
	}
	return string(inst.InstanceID), nil
}

// TaskSnapshot 组装某工程的身份快照，供 model.TaskSnapshot.Validate 机检。
func TaskSnapshot(tx *sql.Tx, projectID string) (*model.TaskSnapshot, error) {
	instances, err := ListInstances(tx, projectID)
	if err != nil {
		return nil, err
	}
	snap := &model.TaskSnapshot{
		Contract:    model.TaskContract,
		ProjectID:   model.ProjectID(projectID),
		GeneratedAt: time.Now().UTC(),
	}
	for _, inst := range instances {
		snap.Instances = append(snap.Instances, *inst)
	}
	return snap, nil
}

// ensureTaskIdentity 返回 tasks 行应持有的实例。
//
// 已有活跃实例就复用；否则新建（v1 冻结）。这里不判断「是不是重建」——
// 退役过的实例只作快照，新行一律拿新身份，历史按 instance_id 分开。
func ensureTaskIdentity(tx *sql.Tx, projectID, displayID string, draft InstanceDraft) (*model.TaskInstance, error) {
	inst, err := ActiveInstance(tx, projectID, displayID)
	if err != nil {
		return nil, err
	}
	if inst != nil {
		return inst, nil
	}
	return CreateInstance(tx, projectID, displayID, draft)
}

// bindTaskIdentity 保证 tasks 行带一个非空、唯一的实例引用（v4 起由触发器强制）。
//
// 留空表示「按编号找活跃实例，没有就新建」；已填的引用必须仍存在、属于同一行、
// 且未被退役——指向退役实例的旧引用正是「编号重建串历史」的入口。
func bindTaskIdentity(tx *sql.Tx, t *Task) error {
	if t.InstanceID == "" {
		inst, err := ensureTaskIdentity(tx, t.ProjectID, t.ID, draftFromTask(t))
		if err != nil {
			return err
		}
		t.InstanceID = string(inst.InstanceID)
		return nil
	}
	inst, err := GetInstance(tx, t.InstanceID)
	if err != nil {
		return err
	}
	if inst == nil {
		return model.Violationf(model.CodeTaskFieldInvalid, "task.instance_id", "实例 %q 不存在", t.InstanceID)
	}
	if string(inst.Legacy.ProjectID) != t.ProjectID || string(inst.DisplayID) != t.ID {
		return model.Violationf(model.CodeLegacyAnchorMissing, "task.instance_id",
			"实例 %q 属于 %s/%s，不能挂到 %s/%s",
			t.InstanceID, inst.Legacy.ProjectID, inst.DisplayID, t.ProjectID, t.ID)
	}
	if inst.State == model.InstanceStateRetired {
		return model.Violationf(model.CodeDepRetired, "task.instance_id",
			"实例 %q 已退役，不能承载当前任务", t.InstanceID)
	}
	return nil
}

// VersionSpec 返回某版本冻结时的规范快照。
//
// 冻结的意义就在这里：蓝图以后再改，已启动的 Run 仍能读出自己当时冻结的定义。
func VersionSpec(tx *sql.Tx, instanceID string, versionNo int) (map[string]any, error) {
	var raw string
	err := tx.QueryRow(`SELECT spec_json FROM task_versions WHERE instance_id = ? AND version_no = ?`,
		instanceID, versionNo).Scan(&raw)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out, nil
}

func scanInstance(row interface{ Scan(dest ...any) error }) (*model.TaskInstance, error) {
	var (
		id, project, display, state, createdAt string
		retiredAt, retiredReason, note         sql.NullString
		activeVersion                          sql.NullInt64
		ambiguous                              int
	)
	if err := row.Scan(&id, &project, &display, &state, &createdAt,
		&retiredAt, &retiredReason, &activeVersion, &ambiguous, &note); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	inst := &model.TaskInstance{
		InstanceID: model.InstanceID(id),
		DisplayID:  model.DisplayID(display),
		State:      model.InstanceState(state),
		CreatedAt:  parseTS(createdAt),
		Legacy: model.LegacyAnchor{
			ProjectID:  model.ProjectID(project),
			DisplayID:  model.DisplayID(display),
			PrimaryKey: model.LegacyPrimaryKey(model.ProjectID(project), model.DisplayID(display)),
			Ambiguous:  ambiguous != 0,
		},
	}
	if retiredAt.Valid {
		t := parseTS(retiredAt.String)
		inst.RetiredAt = &t
	}
	if retiredReason.Valid {
		r := retiredReason.String
		inst.RetiredReason = &r
	}
	if activeVersion.Valid {
		v := model.VersionNo(activeVersion.Int64)
		inst.ActiveVersion = &v
	}
	if note.Valid {
		n := note.String
		inst.Legacy.AmbiguityNote = &n
	}
	return inst, nil
}

func loadVersions(tx *sql.Tx, inst *model.TaskInstance) error {
	rows, err := tx.Query(`SELECT version_no, frozen_at, title, deps_json
FROM task_versions WHERE instance_id = ? ORDER BY version_no ASC`, string(inst.InstanceID))
	if err != nil {
		return err
	}
	defer rows.Close()
	var versions []model.TaskVersion
	for rows.Next() {
		var (
			no       int
			frozenAt string
			title    string
			depsRaw  string
		)
		if err := rows.Scan(&no, &frozenAt, &title, &depsRaw); err != nil {
			return err
		}
		deps := []model.Dep{}
		_ = json.Unmarshal([]byte(depsRaw), &deps)
		versions = append(versions, model.TaskVersion{
			VersionNo: model.VersionNo(no),
			FrozenAt:  parseTS(frozenAt),
			Title:     title,
			Deps:      deps,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	inst.Versions = versions
	return nil
}

func tsOrDash(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return formatTS(*t)
}

// draftFromTask 把 tasks 行翻成 v1 草稿：存量 deps_json 是显示编号，按 legacy 解读；
// 已经是 ti_ 形态的值按 instance 解读，避免把新写入的依赖又降级成编号。
func draftFromTask(t *Task) InstanceDraft {
	deps := make([]model.Dep, 0, len(t.Deps))
	for _, d := range t.Deps {
		if strings.HasPrefix(d, "ti_") {
			deps = append(deps, model.Dep{Kind: model.DepInstance, Target: d})
			continue
		}
		deps = append(deps, model.Dep{Kind: model.DepLegacy, Target: d})
	}
	spec := map[string]any{
		"priority":    t.Priority,
		"merge_order": t.MergeOrder,
		"write_scope": t.WriteScope,
		"read_scope":  t.ReadScope,
		"acceptance":  t.Acceptance,
		"title":       t.Title,
	}
	return InstanceDraft{Title: t.Title, Deps: deps, Spec: spec}
}

// backfillTaskIdentity 给存量 tasks 行补上实例身份（迁移 v4 的数据步骤）。
//
// 每行补一个确定性 instance_id、一个活跃实例与一条 v1 快照；已在台账里的行跳过，
// 所以重放迁移不会产生第二批身份。
func backfillTaskIdentity(tx *sql.Tx) error {
	type row struct {
		projectID, displayID string
		title                string
		deps, ws, rs, acc    string
		priority, mergeOrder int
	}
	rows, err := tx.Query(`SELECT project_id, id, title, deps_json, write_scope_json,
  read_scope_json, acceptance_json, priority, merge_order
FROM tasks WHERE instance_id IS NULL OR instance_id = ''`)
	if err != nil {
		return err
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.projectID, &r.displayID, &r.title, &r.deps, &r.ws, &r.rs, &r.acc,
			&r.priority, &r.mergeOrder); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, r := range pending {
		id := legacyInstanceID(r.projectID, r.displayID)
		t := &Task{
			ProjectID:  r.projectID,
			ID:         r.displayID,
			Title:      r.title,
			Priority:   r.priority,
			MergeOrder: r.mergeOrder,
			Deps:       parseJSONArr(r.deps),
			WriteScope: parseJSONArr(r.ws),
			ReadScope:  parseJSONArr(r.rs),
			Acceptance: parseJSONArr(r.acc),
		}
		// tasks 行没有创建时间：补账时以迁移时刻为准，不伪造历史时间。
		createdAt := UTCNow()
		if _, err := tx.Exec(`
INSERT OR IGNORE INTO task_instances(instance_id, project_id, display_id, state, created_at,
  retired_at, retired_reason, active_version, ambiguous, ambiguity_note)
VALUES (?,?,?,'active',?,NULL,NULL,1,0,NULL)`,
			string(id), r.projectID, r.displayID, createdAt); err != nil {
			return err
		}
		var versions int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM task_versions WHERE instance_id = ?`, string(id)).Scan(&versions); err != nil {
			return err
		}
		if versions == 0 {
			if _, err := insertVersion(tx, string(id), 1, parseTS(createdAt), draftFromTask(t)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE tasks SET instance_id = ? WHERE project_id = ? AND id = ?`,
			string(id), r.projectID, r.displayID); err != nil {
			return err
		}
	}
	return nil
}
