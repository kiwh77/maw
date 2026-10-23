package app

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/tasks"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/machine"
)

// ensureWritableDB 是写库入口：已是最新 schema 时只做写者闸门检查；
// 需要推进迁移时先 AssertSafeToUpgrade，再 MigrateWithBackup（停写 + 备份）。
// 只读查询不得调用本函数。
func ensureWritableDB(path string) error {
	if path == "" {
		return errx.New("全局库路径为空", "检查 MAW_GLOBAL 或 ~/.maw")
	}
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return db.Init(path)
	}
	if err != nil {
		return err
	}
	if s, err := db.OpenReadOnly(path); err == nil {
		_ = s.Close()
		if _, err := db.CheckWriterState(path); err != nil {
			return err
		}
		return nil
	} else if errors.Is(err, db.ErrSchemaAhead) || errors.Is(err, db.ErrWriterTooOld) {
		return err
	}
	if _, werr := db.CheckWriterState(path); werr != nil {
		return werr
	}
	raw, err := db.OpenRaw(path)
	if err != nil {
		return err
	}
	defer raw.Close()
	if err := db.AssertSafeToUpgrade(raw); err != nil {
		return errx.Wrap("库待升级但运行未排空，拒绝隐式迁移",
			"停写并排空受管 Run／占用后执行 maw init --upgrade", err)
	}
	backup := path + ".pre-migrate-" + time.Now().UTC().Format("20060102T150405Z") + ".db"
	if err := db.MigrateWithBackup(path, backup); err != nil {
		return err
	}
	return nil
}

// withGlobalWrite 先走停写／备份升级入口，再开写事务。
func withGlobalWrite(path string, fn func(*sql.Tx) error) error {
	if err := ensureWritableDB(path); err != nil {
		return err
	}
	return db.WithTx(path, fn)
}

// withGlobalRead 只读事务：不建库、不迁移、不加写锁。
func withGlobalRead(path string, fn func(*sql.Tx) error) error {
	err := db.WithReadTx(path, fn)
	if err == nil {
		return nil
	}
	if errors.Is(err, db.ErrNotMigrated) || errors.Is(err, db.ErrSchemaDrift) {
		return errx.Wrap("全局库不可读或 schema 未就绪",
			"在工程目录执行 maw init 或 maw init --upgrade（会停写并备份）", err)
	}
	return err
}

func taskIdentityService() *tasks.Service {
	return tasks.New(nil)
}

func desiredFromTasks(list []*db.Task) []tasks.Desired {
	out := make([]tasks.Desired, 0, len(list))
	for _, t := range list {
		if t == nil {
			continue
		}
		out = append(out, tasks.Desired{
			DisplayID: model.DisplayID(t.ID),
			Title:     t.Title,
			Deps:      depsFromTask(t),
		})
	}
	return out
}

func depsFromTask(t *db.Task) []model.Dep {
	if t == nil {
		return nil
	}
	out := make([]model.Dep, 0, len(t.Deps))
	for _, d := range t.Deps {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if strings.HasPrefix(d, "ti_") {
			out = append(out, model.Dep{Kind: model.DepInstance, Target: d})
			continue
		}
		out = append(out, model.Dep{Kind: model.DepLegacy, Target: d})
	}
	return out
}

func instanceDraftFromTask(t *db.Task) db.InstanceDraft {
	return db.InstanceDraft{
		Title: t.Title,
		Deps:  depsFromTask(t),
		Spec: map[string]any{
			"priority":    t.Priority,
			"merge_order": t.MergeOrder,
			"write_scope": t.WriteScope,
			"read_scope":  t.ReadScope,
			"acceptance":  t.Acceptance,
			"title":       t.Title,
		},
	}
}

func occupyingRuns(tx *sql.Tx, projectID string) ([]tasks.ActiveRun, error) {
	runs, err := db.ListRuns(tx, projectID)
	if err != nil {
		return nil, err
	}
	var out []tasks.ActiveRun
	for _, r := range runs {
		if r == nil || !r.Occupies() {
			continue
		}
		out = append(out, tasks.ActiveRun{
			RunID:    model.RunID(r.ID),
			Instance: model.InstanceID(r.InstanceID),
			Version:  r.VersionNo,
		})
	}
	return out, nil
}

func contractualDisplayIDs(list []*db.Task) bool {
	for _, t := range list {
		if t == nil {
			continue
		}
		if !model.DisplayID(t.ID).Valid() {
			return false
		}
	}
	return true
}

// planTaskIdentity 调用 VN-206 任务身份服务；DisplayID 须符合契约形态。
func planTaskIdentity(tx *sql.Tx, projectID string, desired []*db.Task, reset bool) (*tasks.Plan, error) {
	snap, err := db.TaskSnapshot(tx, projectID)
	if err != nil {
		return nil, err
	}
	runs, err := occupyingRuns(tx, projectID)
	if err != nil {
		return nil, err
	}
	mode := tasks.ModeIncremental
	if reset {
		mode = tasks.ModeReset
	}
	definitions := desiredFromTasks(desired)
	// The preflight must use the same retained set as PlanApply's incremental
	// write path. Otherwise a new independent task falsely retires completed or
	// occupied prerequisites and fails their historical dependency checks.
	if !reset {
		existing, err := db.ListTasks(tx, projectID)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, t := range desired {
			if t != nil {
				seen[t.ID] = true
			}
		}
		for _, t := range existing {
			if !seen[t.ID] && machine.PreserveOnPlanApply(t.Status) {
				definitions = append(definitions, desiredFromTasks([]*db.Task{t})...)
			}
		}
	}
	// Legacy frozen versions can refer to already retired prerequisites. Keep
	// those records immutable, but do not revalidate their old edges as new work.
	// Explicitly submitted definitions are never exempt from dependency checks.
	planning, definitions, historicalEdges := historicalPlanningSnapshot(snap, definitions, desired)
	planning, definitions, legacyCount := legacyPlanningSnapshot(planning, definitions)
	plan, err := taskIdentityService().Plan(tasks.PlanRequest{
		ProjectID:  model.ProjectID(projectID),
		Snapshot:   planning,
		Desired:    definitions,
		Mode:       mode,
		ActiveRuns: runs,
	})
	if err == nil && legacyCount > 0 {
		plan.Notes = append(plan.Notes, "历史编号仅在内存规划中使用兼容别名，持久化编号保持不变")
	}
	if err == nil && historicalEdges > 0 {
		plan.Notes = append(plan.Notes, "已有冻结版本包含退役依赖：原记录保留，新提交定义仍严格校验")
	}
	return plan, err
}

// historicalPlanningSnapshot isolates old, already-unresolvable dependency edges
// from preflight. It never removes a new dependency, changes persisted versions,
// or treats ambiguous/missing references as valid.
func historicalPlanningSnapshot(snap *model.TaskSnapshot, defs []tasks.Desired, submitted []*db.Task) (*model.TaskSnapshot, []tasks.Desired, int) {
	if snap == nil {
		return snap, defs, 0
	}
	byID := map[string]model.InstanceState{}
	byDisplay := map[string][]model.InstanceState{}
	for _, inst := range snap.Instances {
		byID[string(inst.InstanceID)] = inst.State
		byDisplay[string(inst.DisplayID)] = append(byDisplay[string(inst.DisplayID)], inst.State)
	}
	count := 0
	prune := func(input []model.Dep) []model.Dep {
		out := make([]model.Dep, 0, len(input))
		for _, dep := range input {
			retired := dep.Kind == model.DepInstance && byID[dep.Target] == model.InstanceStateRetired
			if dep.Kind == model.DepLegacy {
				hits := byDisplay[dep.Target]
				retired = len(hits) == 1 && hits[0] == model.InstanceStateRetired
			}
			if retired {
				count++
				continue
			}
			out = append(out, dep)
		}
		return out
	}
	out := *snap
	out.Instances = append([]model.TaskInstance(nil), snap.Instances...)
	for i := range out.Instances {
		inst := &out.Instances[i]
		inst.Versions = append([]model.TaskVersion(nil), inst.Versions...)
		for v := range inst.Versions {
			inst.Versions[v].Deps = prune(inst.Versions[v].Deps)
		}
	}
	explicit := map[model.DisplayID]bool{}
	for _, t := range submitted {
		if t != nil {
			explicit[model.DisplayID(t.ID)] = true
		}
	}
	desired := append([]tasks.Desired(nil), defs...)
	for i := range desired {
		if !explicit[desired[i].DisplayID] {
			desired[i].Deps = prune(desired[i].Deps)
		}
	}
	return &out, desired, count
}

// legacyPlanningSnapshot adapts migrated display aliases only for the pure planning
// preflight. PlanApply persists definitions through the original db.Task rows;
// this synthetic snapshot is never saved. All identity, dependency, version and
// occupancy checks still run, and malformed legacy anchors are not repaired here.
func legacyPlanningSnapshot(snap *model.TaskSnapshot, desired []tasks.Desired) (*model.TaskSnapshot, []tasks.Desired, int) {
	if snap == nil {
		return snap, desired, 0
	}
	aliases := map[model.DisplayID]model.DisplayID{}
	used := map[model.DisplayID]bool{}
	for _, inst := range snap.Instances {
		used[inst.DisplayID] = true
	}
	for _, d := range desired {
		used[d.DisplayID] = true
	}
	next := 0
	for _, inst := range snap.Instances {
		if inst.DisplayID.Valid() || aliases[inst.DisplayID] != "" {
			continue
		}
		var alias model.DisplayID
		for {
			next++
			alias = model.DisplayID(fmt.Sprintf("MAWLEGACY-%06d", next))
			if !used[alias] {
				break
			}
		}
		used[alias] = true
		aliases[inst.DisplayID] = alias
	}
	if len(aliases) == 0 {
		return snap, desired, 0
	}
	remap := func(id model.DisplayID) model.DisplayID {
		if alias := aliases[id]; alias != "" {
			return alias
		}
		return id
	}
	deps := func(input []model.Dep) []model.Dep {
		out := append([]model.Dep(nil), input...)
		for i := range out {
			if out[i].Kind == model.DepLegacy {
				out[i].Target = string(remap(model.DisplayID(out[i].Target)))
			}
		}
		return out
	}
	out := *snap
	out.Instances = append([]model.TaskInstance(nil), snap.Instances...)
	for i := range out.Instances {
		inst := &out.Instances[i]
		original := inst.DisplayID
		inst.DisplayID = remap(original)
		if inst.Legacy.DisplayID == original {
			if inst.Legacy.PrimaryKey == model.LegacyPrimaryKey(inst.Legacy.ProjectID, original) {
				inst.Legacy.PrimaryKey = model.LegacyPrimaryKey(inst.Legacy.ProjectID, inst.DisplayID)
			}
			inst.Legacy.DisplayID = inst.DisplayID
		}
		inst.Versions = append([]model.TaskVersion(nil), inst.Versions...)
		for v := range inst.Versions {
			inst.Versions[v].Deps = deps(inst.Versions[v].Deps)
		}
	}
	definitions := append([]tasks.Desired(nil), desired...)
	for i := range definitions {
		definitions[i].DisplayID = remap(definitions[i].DisplayID)
		definitions[i].Deps = deps(definitions[i].Deps)
	}
	return &out, definitions, len(aliases)
}

func refuseResetIfOccupied(tx *sql.Tx, projectID string) error {
	runs, err := occupyingRuns(tx, projectID)
	if err != nil {
		return err
	}
	if len(runs) > 0 {
		return errx.New("plan apply --reset 拒绝：仍有占用中的 Run",
			"确认进程停止并释放占用后再 --reset；或先增量 apply")
	}
	return nil
}

// freezeIfDefinitionChanged 在标题／依赖相对活跃版本有变时追加冻结版本。
// 普通状态字段更新不冻结；已退役实例跳过。
func freezeIfDefinitionChanged(tx *sql.Tx, t *db.Task) error {
	if t == nil || t.InstanceID == "" {
		return nil
	}
	inst, err := db.GetInstance(tx, t.InstanceID)
	if err != nil {
		return err
	}
	if inst == nil || inst.State != model.InstanceStateActive {
		return nil
	}
	cur := inst.ActiveVersionOf()
	draft := instanceDraftFromTask(t)
	if definitionUnchanged(cur, draft) {
		return nil
	}
	_, err = db.FreezeVersion(tx, t.InstanceID, draft)
	return err
}

func definitionUnchanged(cur *model.TaskVersion, draft db.InstanceDraft) bool {
	if cur == nil {
		return false
	}
	if cur.Title != draft.Title || len(cur.Deps) != len(draft.Deps) {
		return false
	}
	want := make(map[string]bool, len(draft.Deps))
	for _, d := range draft.Deps {
		want[string(d.Kind)+":"+d.Target] = true
	}
	for _, d := range cur.Deps {
		if !want[string(d.Kind)+":"+d.Target] {
			return false
		}
	}
	return true
}

// retireIdleRemoved 把蓝图外的空闲题退役（归档身份 + 释放别名），不软删为 cancelled。
func retireIdleRemoved(tx *sql.Tx, projectID string, old *db.Task, agent string) error {
	if old == nil {
		return nil
	}
	from := old.Status
	instID := old.InstanceID
	if err := db.DeleteTask(tx, projectID, old.ID); err != nil {
		return err
	}
	_ = db.AppendEvent(tx, projectID, agent, "task_removed_from_plan", old.ID, map[string]any{
		"from": from, "to": "retired", "instance_id": instID, "reason": db.RetireRemovedFromPlan,
	})
	_ = db.AppendEvent(tx, projectID, agent, "task_retired", old.ID, map[string]any{
		"instance_id": instID, "reason": db.RetireRemovedFromPlan,
	})
	return nil
}
