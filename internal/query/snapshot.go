package query

import (
	"database/sql"
	"errors"
	"time"
)

// Store 是只读入口：internal/db 的 *Store 直接满足它（Read 走只读池、deferred BEGIN）。
//
// 这里只依赖「能开一个只读事务」，不依赖具体库类型：查询层不需要知道写池、重试策略
// 或迁移台账，也不会误用写事务。
type Store interface {
	Read(fn func(*sql.Tx) error) error
}

// Watermark 是一次读取看到的一致水位。
//
// 字段对齐可观测性契约 §2 的命名：Cursor 是已入库的最大事件游标，ReceivedAt 是水位
// 生成时间。该契约里属于计量快照的 complete_before／gaps／completeness 不在这里出现
// ——查询快照不做完整性推断，也不该假装自己做了。
type Watermark struct {
	// Cursor 是快照内 run_events 的最大入库游标（没有事件为 0）。
	Cursor int64 `json:"cursor"`
	// Revision 是全库任务行的最大乐观锁版本；工程级版本用 Reader.ProjectRevision。
	Revision int64 `json:"revision"`
	// SchemaVersion 是本次读到库文件时迁移台账的末端版本，便于发现读到了旧库。
	SchemaVersion int `json:"schema_version"`
	// ReceivedAt 是水位生成时间（UTC）。
	ReceivedAt time.Time `json:"received_at"`
}

// Reader 是一次一致快照上的查询句柄；所有方法都只读。
type Reader struct {
	tx *sql.Tx
	wm Watermark
}

// Read 在一个只读快照里执行 fn：水位与所有查询同源，翻页不会跨快照。
//
// 注意「一次调用＝一个快照」：不要把 Read 拆成多次调用去拼一页数据，
// 那样两半可能来自不同时刻，游标也就失去了意义。
func Read(store Store, fn func(*Reader) error) error {
	if store == nil {
		return errors.New("query: 需要只读 Store")
	}
	return store.Read(func(tx *sql.Tx) error {
		wm, err := captureWatermark(tx)
		if err != nil {
			return err
		}
		return fn(&Reader{tx: tx, wm: wm})
	})
}

// Watermark 返回本次快照的水位。
func (r *Reader) Watermark() Watermark { return r.wm }

// ProjectRevision 返回工程级聚合版本：该工程任务行的最大 revision（没有任务为 0）。
//
// HTTP 契约 §2 允许列表带 revision；它必须与列表同一快照，否则客户端拿到的版本
// 与数据对不上。
func (r *Reader) ProjectRevision(projectID string) (int64, error) {
	var rev int64
	q := `SELECT COALESCE(MAX(revision), 0) FROM tasks`
	args := []any{}
	if projectID != "" {
		q += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	if err := r.tx.QueryRow(q, args...).Scan(&rev); err != nil {
		return 0, err
	}
	return rev, nil
}

func captureWatermark(tx *sql.Tx) (Watermark, error) {
	wm := Watermark{ReceivedAt: time.Now().UTC()}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(cursor), 0) FROM run_events`).Scan(&wm.Cursor); err != nil {
		return wm, err
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(revision), 0) FROM tasks`).Scan(&wm.Revision); err != nil {
		return wm, err
	}
	var schema sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&schema); err != nil {
		return wm, err
	}
	if schema.Valid {
		wm.SchemaVersion = int(schema.Int64)
	}
	return wm, nil
}

// pageOf 收尾：多取的那一行只用来判断「还有下一页」，不返回给调用方。
// 游标取最后一条「已返回」记录的排序键，因此下一页从它之后开始，不重不漏。
func pageOf[T any](items []T, limit int, kind string, keysOf func(T) []string) ([]T, string) {
	if len(items) <= limit {
		return items, ""
	}
	items = items[:limit]
	return items, encodeCursor(kind, keysOf(items[len(items)-1])...)
}
