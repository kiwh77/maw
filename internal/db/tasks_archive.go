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
		} else if !includeDone && strings.EqualFold(strings.TrimSpace(t.Status), "done") {
			continue
		}
		out = append(out, t)
	}
	return out
}
