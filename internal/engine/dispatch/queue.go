package dispatch

import (
	"context"
	"database/sql"
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
type DBQueue struct {
	store TxStore
}

// NewDBQueue 用给定的数据库句柄构造队列。
func NewDBQueue(store TxStore) *DBQueue {
	return &DBQueue{store: store}
}

// Pending 返回全部待派发的操作。
func (q *DBQueue) Pending(ctx context.Context) ([]Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []Operation
	err := q.store.Read(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT o.operation_id, o.project_id, o.run_id, o.instance_id,
       r.generation, o.request_digest
FROM operations o JOIN runs r ON r.run_id = o.run_id
WHERE o.kind = 'dispatch' AND o.result = 'pending'
ORDER BY o.created_at, o.operation_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				op        Operation
				operation string
				project   string
				run       string
				instance  string
				gen       int
				digest    string
			)
			if err := rows.Scan(&operation, &project, &run, &instance, &gen, &digest); err != nil {
				return err
			}
			op = Operation{
				OperationID:   model.OperationID(operation),
				ProjectID:     model.ProjectID(project),
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

// Claim 为当前协调者领取一个待派发操作。
func (q *DBQueue) Claim(ctx context.Context, operationID model.OperationID, coordinator string, ttl time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var ok bool
	err := q.store.Write(func(tx *sql.Tx) error {
		var err error
		ok, err = db.ClaimOperation(tx, string(operationID), coordinator, ttl)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("dispatch: 领取操作 %s: %w", operationID, err)
	}
	return ok, nil
}

// Complete 由当前协调者写入操作终态。
func (q *DBQueue) Complete(ctx context.Context, operationID model.OperationID, coordinator string, result model.OperationResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := q.store.Write(func(tx *sql.Tx) error {
		return db.CompleteOperation(tx, string(operationID), coordinator, result)
	})
	if err != nil {
		return fmt.Errorf("dispatch: 完成操作 %s: %w", operationID, err)
	}
	return nil
}

// RejectPending 撤回一个无人持有（或租约已过期）的 pending 操作。
// 返回 false 表示操作已经不在可撤回状态（已 applied/rejected 或被他人持租约）。
func (q *DBQueue) RejectPending(ctx context.Context, operationID model.OperationID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	var affected int64
	err := q.store.Write(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE operations
SET result = 'rejected', coordinator = '', lease_expires_at = NULL, updated_at = ?
WHERE operation_id = ? AND result = 'pending'
  AND (coordinator = '' OR lease_expires_at IS NULL OR lease_expires_at <= ?)`,
			now.Format(time.RFC3339), string(operationID), now.Format(time.RFC3339))
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
