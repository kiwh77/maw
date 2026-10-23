package app

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/tasks"
	"github.com/kiwh77/maw/internal/errx"
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
	return taskIdentityService().Plan(tasks.PlanRequest{
		ProjectID:  model.ProjectID(projectID),
		Snapshot:   snap,
		Desired:    desiredFromTasks(desired),
		Mode:       mode,
		ActiveRuns: runs,
	})
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
