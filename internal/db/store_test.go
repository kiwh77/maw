package db

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/version"
)

// freshPath 返回一个尚不存在的库路径（父目录已存在），用于区分「建库」与「打开」。
func freshPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "maw.db")
}

func tableNames(t *testing.T, path string) []string {
	t.Helper()
	conn, err := openConn(path, readConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func countLedger(t *testing.T, path string) int {
	t.Helper()
	conn, err := openConn(path, readConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM ` + ledgerTable).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// shortenBusy 把等待锁的时间压到毫秒级，让回归表现为「测试失败」而不是「测试很慢」。
func shortenBusy(t *testing.T) {
	t.Helper()
	prevWrite, prevRead := writeBusyTimeoutMS, readBusyTimeoutMS
	writeBusyTimeoutMS, readBusyTimeoutMS = 100, 200
	t.Cleanup(func() {
		writeBusyTimeoutMS, readBusyTimeoutMS = prevWrite, prevRead
	})
}

// holdWriteLock 用另一条连接占住写锁（BEGIN IMMEDIATE），直到 cleanup。
func holdWriteLock(t *testing.T, path string) {
	t.Helper()
	conn, err := openConn(path, writeConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES ('lock_probe', '1')`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback()
		_ = conn.Close()
	})
}

func TestMigrationsAreExplicitAndIdempotent(t *testing.T) {
	path := freshPath(t)
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}

	st, err := InspectSchema(mustOpenRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if !st.UpToDate() {
		t.Fatalf("迁移后应最新: %+v", st)
	}
	if st.Current != LatestVersion() {
		t.Fatalf("current=%d latest=%d", st.Current, LatestVersion())
	}
	// 台账是 vnext 的权威版本；发布常量只能落后于它，不能超前。
	if got := LatestVersion(); got < version.SchemaVersion {
		t.Fatalf("latest=%d 落后于 version.SchemaVersion=%d", got, version.SchemaVersion)
	}
	// meta.schema_version 只是旧读法的镜像，必须与台账末端一致。
	var metaVersion string
	if err := mustOpenRead(t, path).QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&metaVersion); err != nil {
		t.Fatal(err)
	}
	if metaVersion != strconv.Itoa(LatestVersion()) {
		t.Fatalf("meta.schema_version=%q 台账=%d", metaVersion, LatestVersion())
	}
	if n := countLedger(t, path); n != len(Migrations()) {
		t.Fatalf("台账 %d 行，迁移 %d 条", n, len(Migrations()))
	}

	var appliedAt string
	conn, err := openConn(path, readConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT applied_at FROM `+ledgerTable+` WHERE version = ?`, LatestVersion()).Scan(&appliedAt); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	conn, err = openConn(path, readConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	var appliedAtAgain string
	if err := conn.QueryRow(`SELECT applied_at FROM `+ledgerTable+` WHERE version = ?`, LatestVersion()).Scan(&appliedAtAgain); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if appliedAtAgain != appliedAt {
		t.Fatalf("重复迁移改写了台账: %q → %q", appliedAt, appliedAtAgain)
	}
	if n := countLedger(t, path); n != len(Migrations()) {
		t.Fatalf("重复迁移后台账 %d 行", n)
	}

	// 迁移序列对外只读：改副本不得影响内部注册表。
	ms := Migrations()
	orig := ms[0].Version
	ms[0].Version = -1
	if Migrations()[0].Version != orig {
		t.Fatal("Migrations() 返回的应是副本")
	}
}

func mustOpenRead(t *testing.T, path string) *sql.DB {
	t.Helper()
	conn, err := openConn(path, readConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestReadPathDoesNotMigrate(t *testing.T) {
	// 1) 库文件不存在：只读打开报未迁移，且不会顺手建文件。
	missing := freshPath(t)
	if _, err := OpenReadOnly(missing); !errors.Is(err, ErrNotMigrated) {
		t.Fatalf("期望 ErrNotMigrated，得到 %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("只读打开不应创建库文件: %v", err)
	}

	// 2) 空库文件：不允许建表，也不能把「空库」当成可查询。
	empty := freshPath(t)
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReadOnly(empty); !errors.Is(err, ErrNotMigrated) {
		t.Fatalf("期望 ErrNotMigrated，得到 %v", err)
	}
	if tables := tableNames(t, empty); len(tables) != 0 {
		t.Fatalf("只读路径建了表: %v", tables)
	}

	// 3) 显式迁移后，只读句柄可用，查询仍然不写 schema。
	if err := Init(empty); err != nil {
		t.Fatal(err)
	}
	s, err := OpenReadOnly(empty)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Write(func(*sql.Tx) error { return nil }); !errors.Is(err, ErrReadOnlyStore) {
		t.Fatalf("只读 Store 应拒绝写: %v", err)
	}
	if err := s.Migrate(); !errors.Is(err, ErrReadOnlyStore) {
		t.Fatalf("只读 Store 应拒绝迁移: %v", err)
	}
	if err := s.Read(func(tx *sql.Tx) error {
		var n int
		return tx.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
}

// TestReadTxDoesNotTakeWriteLock 是「查询不用 BEGIN IMMEDIATE」的行为证据：
// 另一个连接持有写锁时，读事务仍然立刻返回，而不是等锁超时。
func TestReadTxDoesNotTakeWriteLock(t *testing.T) {
	shortenBusy(t)
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	holdWriteLock(t, path)

	started := time.Now()
	err = s.Read(func(tx *sql.Tx) error {
		var n int
		return tx.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n)
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("写锁被占时读仍须成功（deferred 读事务）: %v（耗时 %v）", err, elapsed)
	}
	if elapsed > time.Second {
		t.Fatalf("读事务等了 %v，疑似用了 BEGIN IMMEDIATE", elapsed)
	}
}

func TestWriteRejectsUnmigratedSchema(t *testing.T) {
	path := freshPath(t)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	err = s.Write(func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES ('probe', '1')`)
		return e
	})
	if err == nil {
		t.Fatal("未迁移的库上写事务应失败，而不是自动建表")
	}
	if tables := tableNames(t, path); len(tables) != 0 {
		t.Fatalf("写路径执行了 DDL: %v", tables)
	}

	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES ('probe', '1')`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteRetryIsBounded(t *testing.T) {
	path := freshPath(t)
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStoreWith(path, RetryPolicy{MaxAttempts: 3, BackoffMin: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	calls := 0
	started := time.Now()
	err = s.Write(func(*sql.Tx) error {
		calls++
		return errString("database is locked (5)")
	})
	if err == nil {
		t.Fatal("耗尽重试后必须返回错误")
	}
	if !IsBusy(err) {
		t.Fatalf("返回的错误应仍可识别为 busy: %v", err)
	}
	if calls != 3 {
		t.Fatalf("尝试 %d 次，上限应为 3", calls)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("重试耗时 %v，退避上界失效", elapsed)
	}
}

// TestWriteRetryHasNoSchemaSideEffects 保证重试循环里没有 DDL／建目录／建表：
// 写锁被别人占住时，重试只重复 BEGIN IMMEDIATE 与业务语句。
func TestWriteRetryHasNoSchemaSideEffects(t *testing.T) {
	shortenBusy(t)
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	before := tableNames(t, path)
	ledgerBefore := countLedger(t, path)

	s, err := OpenStoreWith(path, RetryPolicy{MaxAttempts: 2, BackoffMin: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	holdWriteLock(t, path)

	err = s.Write(func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES ('blocked', '1')`)
		return e
	})
	if !IsBusy(err) {
		t.Fatalf("期望 busy，得到 %v", err)
	}
	if after := tableNames(t, path); !equalStrings(before, after) {
		t.Fatalf("重试期间表结构变了: %v → %v", before, after)
	}
	if n := countLedger(t, path); n != ledgerBefore {
		t.Fatalf("重试期间台账从 %d 行变成 %d 行", ledgerBefore, n)
	}
}

// TestWithTxLegacyEntryPoint 锁住旧入口的兼容语义：库未初始化也能用，但 schema 只迁移一次。
func TestWithTxLegacyEntryPoint(t *testing.T) {
	path := freshPath(t)
	if err := WithTx(path, func(tx *sql.Tx) error {
		return UpsertProject(tx, &Project{
			ID: "p1", Name: "Demo", Path: "/tmp/demo",
			MainBranch: "main", MaxParallel: 2,
			CreatedAt: UTCNow(), LastSeen: UTCNow(),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if n := countLedger(t, path); n != len(Migrations()) {
		t.Fatalf("台账 %d 行", n)
	}
	if err := WithTx(path, func(tx *sql.Tx) error {
		p, err := GetProject(tx, "p1")
		if err != nil {
			return err
		}
		if p == nil || p.Name != "Demo" {
			t.Fatalf("project=%+v", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := countLedger(t, path); n != len(Migrations()) {
		t.Fatalf("重复调用后仍应只有 %d 行台账，实为 %d", len(Migrations()), n)
	}
}

func TestVerifySchemaDetectsDrift(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	w, err := openConn(path, writeConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if _, err := w.Exec(`UPDATE `+ledgerTable+` SET checksum = 'tampered' WHERE version = ?`, LatestVersion()); err != nil {
		t.Fatal(err)
	}
	if err := VerifySchema(mustOpenRead(t, path)); !errors.Is(err, ErrSchemaDrift) {
		t.Fatalf("摘要被改应报 ErrSchemaDrift，得到 %v", err)
	}
	if _, err := w.Exec(`INSERT INTO `+ledgerTable+`(version, name, checksum, applied_at) VALUES (?, 'future', 'x', ?)`, LatestVersion()+1, UTCNow()); err != nil {
		t.Fatal(err)
	}
	if err := VerifySchema(mustOpenRead(t, path)); !errors.Is(err, ErrSchemaDrift) {
		t.Fatalf("库比二进制新应报 ErrSchemaDrift，得到 %v", err)
	}
	if _, err := OpenReadOnly(path); !errors.Is(err, ErrSchemaDrift) {
		t.Fatalf("OpenReadOnly 应拒绝漂移的库，得到 %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
