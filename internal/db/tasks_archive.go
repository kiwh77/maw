package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// 归档标记落在 tasks.extra_json，不新增 TaskStatus 枚举值。
//
// TaskStatus 是封闭集合（model.TaskStatus），加 "archived" 会牵动状态机、
// 迁移与所有按状态聚合的地方。归档是「退出日常视图」的展示决策，不是新的
// 业务状态：任务行、版本快照、历史事件一律保留，因此审计随时可查。
const (
	TaskArchivedKey   = "archived"
	TaskArchivedAtKey = "archived_at"
	TaskArchivedByKey = "archived_by"
)

// IsArchived 读归档标记。历史数据没有该键时一律视为未归档。
func IsArchived(t *Task) bool {
	if t == nil || t.Extra == nil {
		return false
	}
	v, ok := t.Extra[TaskArchivedKey]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true")
	default:
		return false
	}
}

// ArchivedInfo 返回归档的审计信息（时间、操作者）；未归档返回空串。
func ArchivedInfo(t *Task) (at, by string) {
	if !IsArchived(t) {
		return "", ""
	}
	at, _ = t.Extra[TaskArchivedAtKey].(string)
	by, _ = t.Extra[TaskArchivedByKey].(string)
	return strings.TrimSpace(at), strings.TrimSpace(by)
}

// ArchiveTask 把一条任务标记为已归档：只写 Extra 并追加审计事件。
//
// 幂等：重复归档不报错、不重复写事件。任务行与 Status 都不变，
// 因此「退出日常视图」不会抹掉历史，审计可在 tasks/events 两处核对。
func ArchiveTask(tx *sql.Tx, projectID, taskID, actor string, at time.Time) error {
	t, err := GetTask(tx, projectID, taskID)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("db: 任务 %s 不存在", taskID)
	}
	if IsArchived(t) {
		return nil
	}
	if t.Extra == nil {
		t.Extra = map[string]any{}
	}
	stamp := at.UTC().Format(time.RFC3339)
	t.Extra[TaskArchivedKey] = true
	t.Extra[TaskArchivedAtKey] = stamp
	t.Extra[TaskArchivedByKey] = strings.TrimSpace(actor)
	if err := UpsertTask(tx, t); err != nil {
		return err
	}
	return AppendEvent(tx, projectID, actor, "task_archived", taskID, map[string]any{
		"at": stamp, "task_id": taskID,
	})
}

// FilterActivity 是看板任务列表的显示范围过滤：默认排除 done 与 archived，
// 显式 include 才放行。返回非 nil 切片，保证 JSON 稳定输出 []。
//
// 归档题几乎都是已完成题，因此两条放行路径必须独立判定：
// include=archived 只放行带归档标记的题（不再被 done 规则二次挡掉），
// include=done 只放行未归档的完成题，两者都用 include=all 同时放行。
func FilterActivity(tasks []*Task, includeDone, includeArchived bool) []*Task {
	out := make([]*Task, 0, len(tasks))
	for _, t := range tasks {
		if t == nil {
			continue
		}
		if IsArchived(t) {
			if !includeArchived {
				continue
			}
		} else if !includeDone {
			// cancelled 与 done 同属终态：默认活动视图一并隐藏，避免「已完成」泳道里挂取消文案。
			st := strings.ToLower(strings.TrimSpace(t.Status))
			if st == "done" || st == "cancelled" {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// taskKey 工程内任务键：任务 ID 只在工程内唯一，跨工程可能撞号。
func taskKey(projectID, taskID string) string {
	return projectID + "\x00" + taskID
}

// FilterActivityKeepingUpstream 在活动视图过滤之上，保留「仍被活动题直接依赖」
// 的上游（含 done / cancelled / archived）。
//
// 否则默认隐藏终态后，看板 chips／等待行／依赖拓扑只能把上游标成「未知」，
// 连标题都解析不到。只补一层直接依赖，不递归整条历史链，避免 Done 泳道被灌满。
func FilterActivityKeepingUpstream(tasks []*Task, includeDone, includeArchived bool) []*Task {
	base := FilterActivity(tasks, includeDone, includeArchived)
	if len(base) == 0 || len(base) == len(tasks) {
		return base
	}

	kept := make(map[string]struct{}, len(base))
	for _, t := range base {
		kept[taskKey(t.ProjectID, t.ID)] = struct{}{}
	}

	byProj := make(map[string]map[string]*Task, 8)
	for _, t := range tasks {
		if t == nil {
			continue
		}
		m := byProj[t.ProjectID]
		if m == nil {
			m = make(map[string]*Task)
			byProj[t.ProjectID] = m
		}
		m[t.ID] = t
	}

	need := make(map[string]struct{})
	for _, t := range base {
		m := byProj[t.ProjectID]
		if m == nil {
			continue
		}
		for _, d := range t.Deps {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			up := m[d]
			if up == nil {
				continue
			}
			k := taskKey(up.ProjectID, up.ID)
			if _, ok := kept[k]; ok {
				continue
			}
			need[k] = struct{}{}
		}
	}
	if len(need) == 0 {
		return base
	}

	// 按全量列表原序输出，避免依赖补回后打乱相对顺序。
	out := make([]*Task, 0, len(base)+len(need))
	seen := make(map[string]struct{}, len(base)+len(need))
	for _, t := range tasks {
		if t == nil {
			continue
		}
		k := taskKey(t.ProjectID, t.ID)
		_, inBase := kept[k]
		_, inNeed := need[k]
		if !inBase && !inNeed {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, t)
	}
	return out
}
