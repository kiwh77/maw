package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// Operation 是派发协调者需要的最小操作视图。
type Operation struct {
	OperationID   model.OperationID `json:"operation_id"`
	ProjectID     model.ProjectID   `json:"project_id"`
	RunID         model.RunID       `json:"run_id"`
	Instance      model.InstanceID  `json:"task_instance"`
	Generation    model.Generation  `json:"generation"`
	RequestDigest string            `json:"request_digest,omitempty"`
}

// Validate 校验操作关键字段，避免把无法恢复的出队记录当正常操作推进。
func (o Operation) Validate() error {
	if !o.OperationID.Valid() {
		return fmt.Errorf("dispatch: operation_id %q 形态非法", o.OperationID)
	}
	if !o.RunID.Valid() {
		return fmt.Errorf("dispatch: 操作 %s 的 run_id %q 形态非法", o.OperationID, o.RunID)
	}
	if !o.Instance.Valid() {
		return fmt.Errorf("dispatch: 操作 %s 的 task_instance %q 形态非法", o.OperationID, o.Instance)
	}
	if !o.Generation.Valid() {
		return fmt.Errorf("dispatch: 操作 %s 的 generation %d 非法", o.OperationID, o.Generation)
	}
	if o.ProjectID != "" && !o.ProjectID.Valid() {
		return fmt.Errorf("dispatch: 操作 %s 的 project_id %q 形态非法", o.OperationID, o.ProjectID)
	}
	return nil
}

// Queue 是持久操作的消费端口。
//
// 语义要求：
//   - Pending 只返回 result=pending 的 dispatch 操作，按创建顺序稳定排序；
//   - Claim 必须原子，同一操作同一时刻只有一个协调者持有租约；
//   - Complete 只允许当前协调者写终态（租约换人后旧协调者必须失败）；
//   - RejectPending 只能作用在无人持有（或租约已过期）的 pending 操作上，
//     保证撤回不会打断正在启动的协调者。
type Queue interface {
	Pending(ctx context.Context) ([]Operation, error)
	Claim(ctx context.Context, operationID model.OperationID, coordinator string, ttl time.Duration) (bool, error)
	Complete(ctx context.Context, operationID model.OperationID, coordinator string, result model.OperationResult) error
	RejectPending(ctx context.Context, operationID model.OperationID) (bool, error)
}

// TxStore 是数据库事务端口，*db.Store 直接满足。
type TxStore interface {
	Read(fn func(*sql.Tx) error) error
	Write(fn func(*sql.Tx) error) error
}

// DBQueue 是 Queue 的生产实现，直接消费 operations 表里的 dispatch 操作。
//
// projectID 非空时队列按工程隔离：Pending／Claim／Complete／RejectPending 只作用于
// 该工程的 dispatch 操作。全局状态库由多工程共用，而协调锁与 op 目录都在各工程自己的
// runtime 目录下；不隔离时 A 工程的 serve leader 会领走 B 工程的操作，把 op 目录
// 写进 A 的状态目录。projectID 为空时保持全库视图（既有 CLI 与测试行为不变）。
type DBQueue struct {
	store     TxStore
	projectID model.ProjectID
}

// NewDBQueue 用给定的数据库句柄构造队列。
// 可选传入一个工程 ID 开启按工程隔离；不传则查全库，行为与引入隔离前一致。
func NewDBQueue(store TxStore, projectID ...model.ProjectID) *DBQueue {
	q := &DBQueue{store: store}
	if len(projectID) > 0 {
		q.projectID = projectID[0]
	}
	return q
}

// scope 返回队列绑定的工程（空表示全库视图）。非法 ID 显式报错，
// 避免误传时静默产出空队列，让派发看起来"没有待办"。
func (q *DBQueue) scope() (model.ProjectID, error) {
	if q.projectID != "" && !q.projectID.Valid() {
		return "", fmt.Errorf("dispatch: 队列工程 ID %q 形态非法", q.projectID)
	}
	return q.projectID, nil
}

// operationInProject 在同一事务里核对操作归属；操作不存在视为不属于本工程。
func operationInProject(tx *sql.Tx, operationID model.OperationID, project model.ProjectID) (bool, error) {
	var owner string
	err := tx.QueryRow(`SELECT project_id FROM operations WHERE operation_id = ?`, string(operationID)).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return owner == string(project), nil
}

// Pending 返回待派发的操作；绑定工程时只返回该工程的操作。
func (q *DBQueue) Pending(ctx context.Context) ([]Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	project, err := q.scope()
	if err != nil {
		return nil, err
	}
	var out []Operation
	err = q.store.Read(func(tx *sql.Tx) error {
		query := `SELECT o.operation_id, o.project_id, o.run_id, o.instance_id,
       r.generation, o.request_digest
FROM operations o JOIN runs r ON r.run_id = o.run_id
WHERE o.kind = 'dispatch' AND o.result = 'pending'`
		var args []any
		if project != "" {
			query += ` AND o.project_id = ?`
			args = append(args, string(project))
		}
		query += ` ORDER BY o.created_at, o.operation_id`
		rows, err := tx.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				op        Operation
				operation string
				owner     string
				run       string
				instance  string
				gen       int
				digest    string
			)
			if err := rows.Scan(&operation, &owner, &run, &instance, &gen, &digest); err != nil {
				return err
			}
			op = Operation{
				OperationID:   model.OperationID(operation),
				ProjectID:     model.ProjectID(owner),
				RunID:         model.RunID(run),
				Instance:      model.InstanceID(instance),
				Generation:    model.Generation(gen),
				RequestDigest: digest,
			}
			out = append(out, op)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("dispatch: 列出待派发操作: %w", err)
	}
	return out, nil
}

// Claim 为当前协调者领取一个待派发操作；绑定工程时只领本工程的操作。
func (q *DBQueue) Claim(ctx context.Context, operationID model.OperationID, coordinator string, ttl time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	project, err := q.scope()
	if err != nil {
		return false, err
	}
	var ok bool
	err = q.store.Write(func(tx *sql.Tx) error {
		if project != "" {
			belongs, err := operationInProject(tx, operationID, project)
			if err != nil || !belongs {
				ok = false
				return err
			}
		}
		var err error
		ok, err = db.ClaimOperation(tx, string(operationID), coordinator, ttl)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("dispatch: 领取操作 %s: %w", operationID, err)
	}
	return ok, nil
}

// Complete 由当前协调者写入操作终态；绑定工程时只写本工程的操作。
func (q *DBQueue) Complete(ctx context.Context, operationID model.OperationID, coordinator string, result model.OperationResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	project, err := q.scope()
	if err != nil {
		return err
	}
	err = q.store.Write(func(tx *sql.Tx) error {
		if project != "" {
			belongs, err := operationInProject(tx, operationID, project)
			if err != nil {
				return err
			}
			if !belongs {
				return fmt.Errorf("dispatch: 操作 %s 不属于工程 %s，拒绝写终态", operationID, project)
			}
		}
		return db.CompleteOperation(tx, string(operationID), coordinator, result)
	})
	if err != nil {
		return fmt.Errorf("dispatch: 完成操作 %s: %w", operationID, err)
	}
	return nil
}

// RejectPending 撤回一个无人持有（或租约已过期）的 pending 操作。
// 返回 false 表示操作已经不在可撤回状态（已 applied/rejected、被他人持租约，
// 或绑定了工程但操作属于别的工程）。
func (q *DBQueue) RejectPending(ctx context.Context, operationID model.OperationID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	project, err := q.scope()
	if err != nil {
		return false, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	var affected int64
	err = q.store.Write(func(tx *sql.Tx) error {
		query := `UPDATE operations
SET result = 'rejected', coordinator = '', lease_expires_at = NULL, updated_at = ?
WHERE operation_id = ? AND result = 'pending'
  AND (coordinator = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)`
		args := []any{now.Format(time.RFC3339), string(operationID), now.Format(time.RFC3339)}
		if project != "" {
			query += ` AND project_id = ?`
			args = append(args, string(project))
		}
		res, err := tx.Exec(query, args...)
		if err != nil {
			return err
		}
		affected, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, fmt.Errorf("dispatch: 撤回操作 %s: %w", operationID, err)
	}
	return affected == 1, nil
}

var _ Queue = (*DBQueue)(nil)
