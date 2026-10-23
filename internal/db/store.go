package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const (
	// withTxMaxAttempts / withTxBackoffMin / withTxBackoffMax 是写重试的默认上界：
	// 8 次尝试、50ms 起、2s 封顶（累计退避 ≈ 5s），耗尽后返回可处理的 busy 错误。
	withTxMaxAttempts = 8
	withTxBackoffMin  = 50 * time.Millisecond
	withTxBackoffMax  = 2 * time.Second
)

// 单次尝试里 SQLite 自己等锁的上限。变量只为测试能缩短等待，
// 生产语义仍是「写等 30s、读不需要等」。
var (
	// writeBusyTimeoutMS 与「重试几次」是两件事：进程内串行靠 _txlock=immediate，
	// 跨进程靠这段等待。
	writeBusyTimeoutMS = 30000
	// readBusyTimeoutMS 只影响 close checkpoint 之类的极短争用；WAL 读不应该等写锁。
	readBusyTimeoutMS = 10000
)

// RetryPolicy 界定写事务重试的上界。MaxAttempts<=0 视为 1 次。
type RetryPolicy struct {
	MaxAttempts int
	BackoffMin  time.Duration
	BackoffMax  time.Duration
}

// DefaultRetryPolicy 返回与旧实现一致的默认重试策略。
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: withTxMaxAttempts,
		BackoffMin:  withTxBackoffMin,
		BackoffMax:  withTxBackoffMax,
	}
}

func (p RetryPolicy) normalized() RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 1
	}
	if p.BackoffMin <= 0 {
		p.BackoffMin = withTxBackoffMin
	}
	if p.BackoffMax <= 0 {
		p.BackoffMax = p.BackoffMin
	}
	if p.BackoffMax < p.BackoffMin {
		p.BackoffMax = p.BackoffMin
	}
	return p
}

// Store 是一次打开的状态库句柄，读写分池、迁移独立：
//
//   - 写池的连接带 _txlock=immediate：一次 Write 就是一个 BEGIN IMMEDIATE 短事务；
//     撞上 SQLITE_BUSY／LOCKED 时按 RetryPolicy 有界退避重试，重试循环里只有
//     BEGIN、业务语句与 ROLLBACK，没有建目录、DDL、文件或进程副作用；
//   - 读池的连接带 query_only(1) 且不设 _txlock（deferred）：Read 不开写锁、
//     不改 journal_mode、不创建库文件，也不补迁移；
//   - schema 只由 Migrate 推进。Read／Write 都不执行 DDL，也不会因查询而初始化 schema。
//
// Store 可复用：每进程开一次，不要按每个 API 查询重开。
type Store struct {
	path     string
	policy   RetryPolicy
	readOnly bool

	write *sql.DB

	readOnce sync.Once
	read     *sql.DB
	readErr  error
}

// OpenStore 打开可写句柄（默认重试策略）。
//
// 建目录与建库文件只在这里做一次；迁移仍需显式调用 Migrate，
// 因为「打开」与「升级 schema」是两件事。
func OpenStore(path string) (*Store, error) {
	return OpenStoreWith(path, DefaultRetryPolicy())
}

// OpenStoreWith 用指定重试策略打开可写句柄。
func OpenStoreWith(path string, policy RetryPolicy) (*Store, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}
	conn, err := openConn(path, writeConnOptions())
	if err != nil {
		return nil, err
	}
	return &Store{path: path, policy: policy.normalized(), write: conn}, nil
}

// OpenReadOnly 打开只读句柄：不建目录、不建库文件、不迁移。
//
// 库文件缺失或未推进到当前迁移序列时返回 ErrNotMigrated／ErrSchemaDrift，
// 而不是像旧的 Open 那样顺手把 schema 建出来。
func OpenReadOnly(path string) (*Store, error) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return nil, fmt.Errorf("%w: 库文件 %s 不可读", ErrNotMigrated, path)
	}
	s := &Store{path: path, readOnly: true}
	conn, err := s.readConn()
	if err != nil {
		return nil, err
	}
	if err := VerifySchema(conn); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// Path 返回库文件路径。
func (s *Store) Path() string { return s.path }

// Migrate 把库推进到当前迁移序列；已是最新时不做任何写操作。
func (s *Store) Migrate() error {
	if s.readOnly || s.write == nil {
		return ErrReadOnlyStore
	}
	return MigrateDB(s.write)
}

// SchemaStatus 只读体检（走读池）。
func (s *Store) SchemaStatus() (SchemaStatus, error) {
	conn, err := s.readConn()
	if err != nil {
		return SchemaStatus{}, err
	}
	return InspectSchema(conn)
}

// CheckSchema 只读校验 schema 是否可用于查询。
func (s *Store) CheckSchema() error {
	conn, err := s.readConn()
	if err != nil {
		return err
	}
	return VerifySchema(conn)
}

// Read 在只读事务里执行 fn：deferred BEGIN、不加写锁、不迁移。
func (s *Store) Read(fn func(*sql.Tx) error) error {
	conn, err := s.readConn()
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Write 在有界重试的写事务里执行 fn。
//
// busy 是唯一会自动重试的错误；fn 返回的其它错误立即上抛，不做补偿。
// 重试耗尽后返回的仍是 IsBusy 可识别的错误，便于调用方降级而不是重派任务。
func (s *Store) Write(fn func(*sql.Tx) error) error {
	if s.write == nil {
		return ErrReadOnlyStore
	}
	p := s.policy
	backoff := p.BackoffMin
	var last error
	for attempt := 1; attempt <= p.MaxAttempts; attempt++ {
		last = writeOnce(s.write, fn)
		if last == nil || !IsBusy(last) {
			return last
		}
		if attempt == p.MaxAttempts {
			break
		}
		time.Sleep(backoff)
		backoff = nextBackoff(backoff, p.BackoffMax)
	}
	return fmt.Errorf("db: 写事务 %d 次尝试后仍被占用: %w", p.MaxAttempts, last)
}

// Close 关闭读写池。不要与正在进行的 Read／Write 并发调用。
func (s *Store) Close() error {
	var first error
	if s.read != nil {
		if err := s.read.Close(); err != nil {
			first = err
		}
	}
	if s.write != nil {
		if err := s.write.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// WithReadTx 是只读事务的一次性入口：打开只读句柄、跑一个只读事务、关闭。不迁移。
func WithReadTx(path string, fn func(*sql.Tx) error) error {
	s, err := OpenReadOnly(path)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Read(fn)
}

// writeOnce 只做 BEGIN IMMEDIATE + fn + COMMIT。
// 这里刻意不放任何准备动作：重试会重复执行整个函数，任何文件或 DDL 副作用都会被放大。
func writeOnce(conn *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

func (s *Store) readConn() (*sql.DB, error) {
	s.readOnce.Do(func() {
		s.read, s.readErr = openConn(s.path, readConnOptions())
	})
	return s.read, s.readErr
}

// connOptions 决定一条连接是写还是读。
type connOptions struct {
	busyTimeoutMS int
	immediate     bool // _txlock=immediate：写池
	queryOnly     bool // PRAGMA query_only(1)：读池
	journalWAL    bool // PRAGMA journal_mode(WAL)：只有写池负责切换
	maxOpenConns  int
}

func writeConnOptions() connOptions {
	return connOptions{busyTimeoutMS: writeBusyTimeoutMS, immediate: true, journalWAL: true, maxOpenConns: 1}
}

func readConnOptions() connOptions {
	return connOptions{busyTimeoutMS: readBusyTimeoutMS, queryOnly: true, maxOpenConns: 4}
}

// sqliteDSN 把连接语义写进 DSN：每个新连接都带同一组 pragma，池里换连接也不会丢。
func sqliteDSN(path string, o connOptions) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := u.Query()
	q.Add("_pragma", "busy_timeout("+strconv.Itoa(o.busyTimeoutMS)+")")
	q.Add("_pragma", "foreign_keys(ON)")
	if o.journalWAL {
		q.Add("_pragma", "journal_mode(WAL)")
	}
	if o.queryOnly {
		q.Add("_pragma", "query_only(1)")
	}
	if o.immediate {
		q.Set("_txlock", "immediate")
	} else {
		q.Set("_txlock", "deferred")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func openConn(path string, o connOptions) (*sql.DB, error) {
	conn, err := sql.Open("sqlite", sqliteDSN(path, o))
	if err != nil {
		return nil, err
	}
	maxOpen := o.maxOpenConns
	if maxOpen <= 0 {
		maxOpen = 1
	}
	conn.SetMaxOpenConns(maxOpen)
	conn.SetMaxIdleConns(maxOpen)
	conn.SetConnMaxLifetime(0)
	// Belt-and-suspenders：DSN 已对每个新连接生效，这里保证首个连接立刻进入所需状态。
	stmts := []string{"PRAGMA busy_timeout=" + strconv.Itoa(o.busyTimeoutMS), "PRAGMA foreign_keys=ON"}
	if o.journalWAL {
		stmts = append(stmts, "PRAGMA journal_mode=WAL")
	}
	if o.queryOnly {
		stmts = append(stmts, "PRAGMA query_only=1")
	}
	for _, st := range stmts {
		if _, err := conn.Exec(st); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// ensureParentDir 是唯一允许创建目录的地方；它不在任何重试循环里。
func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
