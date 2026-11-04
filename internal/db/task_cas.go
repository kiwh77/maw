package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// 本文件是「任务变更乐观锁」的唯一写入通道（契约：
// docs/contracts/vnext/collaboration/collaboration-primitives-contract-v1.md §1）。
//
// 规则：修改**已存在任务**的元数据或归属时，调用方必须给出它在读到的 revision；
// 事务内再读一次当前值，相等才写、并把 revision 加 1；不等就整体回滚并返回
// *RevisionMismatch（code=revision_mismatch，带 actual_revision）。
//
// 仍然**由各自事务内部守卫**、不要再用本文件重复实现的入口：
//
//	入口                        守卫位置
//	领取 claim                  internal/engine/claim（同一事务里建 Run、占目录、改状态）
//	心跳过期回收                internal/app/heartbeat.go expireStaleAgents（系统被动动作，无外部期望值）
//	受管 Run 阶段推进           runtime-state-contract-v1 的 generation 校验
//	事件追加                    events 表自增 id（事件不可变，没有更新语义）
//
// 换句话说：本文件只覆盖「有人拿着快照回来改一行」的路径——改派、放回、
// 描述/scope 刷新、归档标记。复合事务与系统动作继续用它们自己的守卫。

// AnyRevision 表示调用方确实没有期望版本（系统动作）。业务入口不应使用它：
// 用它等于放弃并发保护，契约要求人工/CLI/HTTP 入口一律带真实 revision。
const AnyRevision = -1

// RevisionMismatch 是乐观锁失败的稳定错误形状。
//
// Code() 与 Details() 直接对接 HTTP 409 revision_mismatch 与 CLI 退出码，
// 上层不用再翻译一遍。
type RevisionMismatch struct {
	ProjectID string
	TaskID    string
	Expected  int
	Actual    int
}

func (e *RevisionMismatch) Error() string {
	return fmt.Sprintf("revision_mismatch: task %s/%s expected revision %d, actual %d",
		e.ProjectID, e.TaskID, e.Expected, e.Actual)
}

// Code 是稳定错误码，与契约 §7 一致。
func (e *RevisionMismatch) Code() string { return "revision_mismatch" }

// Details 供 HTTP 响应 error.details 使用。
func (e *RevisionMismatch) Details() map[string]any {
	return map[string]any{"actual_revision": e.Actual, "expected_revision": e.Expected}
}

// IsRevisionMismatch 判断错误链里是否有乐观锁失败。
func IsRevisionMismatch(err error) bool {
	var m *RevisionMismatch
	return errors.As(err, &m)
}

// TaskRevision 读取任务当前 revision；任务不存在时返回错误（不是 0）。
func TaskRevision(tx *sql.Tx, projectID, taskID string) (int, error) {
	var rev int
	err := tx.QueryRow(`SELECT revision FROM tasks WHERE project_id = ? AND id = ?`, projectID, taskID).Scan(&rev)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("task %s/%s not found", projectID, taskID)
	}
	if err != nil {
		return 0, err
	}
	return rev, nil
}

// UpdateTaskCAS 以「读-改-写」形式更新一行任务。
//
// expected 必须等于库里的当前 revision（AnyRevision 除外）。mutate 里改字段，
// 不需要自己碰 Revision：成功后返回的任务 revision 已经前进 1。
func UpdateTaskCAS(tx *sql.Tx, projectID, taskID string, expected int, mutate func(*Task) error) (*Task, error) {
	cur, err := GetTask(tx, projectID, taskID)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, fmt.Errorf("task %s/%s not found", projectID, taskID)
	}
	if err := assertRevision(cur, expected); err != nil {
		return nil, err
	}
	if mutate != nil {
		if err := mutate(cur); err != nil {
			return nil, err
		}
	}
	cur.Revision++
	if err := UpsertTask(tx, cur); err != nil {
		return nil, err
	}
	return cur, nil
}

// UpsertTaskCAS 用于调用方已经持有完整任务快照的场景：它只校验 revision，
// 然后照快照整行写回。
//
// 与 UpdateTaskCAS 的差别是「谁提供新值」：这里是快照，那里是回调。两条路径
// 共用同一套校验，冲突都不会落盘。
func UpsertTaskCAS(tx *sql.Tx, t *Task, expected int) error {
	if t == nil {
		return errors.New("nil task")
	}
	if t.ProjectID == "" || t.ID == "" {
		return errors.New("task without project_id/id")
	}
	cur, err := TaskRevision(tx, t.ProjectID, t.ID)
	if err != nil {
		return err
	}
	if expected != AnyRevision && cur != expected {
		return &RevisionMismatch{ProjectID: t.ProjectID, TaskID: t.ID, Expected: expected, Actual: cur}
	}
	t.Revision = cur + 1
	return UpsertTask(tx, t)
}

func assertRevision(cur *Task, expected int) error {
	if expected == AnyRevision {
		return nil
	}
	if cur.Revision != expected {
		return &RevisionMismatch{
			ProjectID: cur.ProjectID, TaskID: cur.ID,
			Expected: expected, Actual: cur.Revision,
		}
	}
	return nil
}
