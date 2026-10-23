package db

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/version"
)

// openWritableRaw 打开可写裸连接且不跑迁移，专供测试构造残缺/越界库。
// 生产读路径请用 OpenRaw（query_only）。
func openWritableRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := ensureParentDir(path); err != nil {
		t.Fatal(err)
	}
	db, err := openConn(path, writeConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// seed 建库并写入一份带引用的样本数据。
func seed(t *testing.T, path string) {
	t.Helper()
	if err := WithTx(path, func(tx *sql.Tx) error {
		if err := UpsertProject(tx, &Project{ID: "p1", Name: "Demo", Path: "/tmp/demo"}); err != nil {
			return err
		}
		for _, id := range []string{"T-1", "T-2"} {
			if err := UpsertTask(tx, &Task{ProjectID: "p1", ID: id, Title: id, Status: "ready"}); err != nil {
				return err
			}
		}
		if err := UpsertAgent(tx, &Agent{ProjectID: "p1", ID: "a1", Status: "idle"}); err != nil {
			return err
		}
		if err := UpsertSharedFile(tx, "p1", "internal/app/x.go", "T-1"); err != nil {
			return err
		}
		return AppendEvent(tx, "p1", "a1", "seeded", "T-1", nil)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRestoreKeepsWALData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maw.db")
	seed(t, path)

	// 保持写连接打开，让数据留在 WAL 里（不做 checkpoint）。
	live, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := WithTx(path, func(tx *sql.Tx) error {
		return UpsertTask(tx, &Task{ProjectID: "p1", ID: "T-3", Title: "wal-only", Status: "ready"})
	}); err != nil {
		t.Fatal(err)
	}
	wal := path + "-wal"
	if st, err := os.Stat(wal); err != nil || st.Size() == 0 {
		t.Fatalf("期望存在非空 WAL，实际 %v %v", st, err)
	}

	backup := filepath.Join(dir, "backup", "maw.db")
	if err := Backup(path, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backup + "-wal"); !os.IsNotExist(err) {
		t.Fatal("VACUUM INTO 产物不应带 -wal")
	}
	if err := VerifyBackup(path, backup); err != nil {
		t.Fatalf("备份校验失败: %v", err)
	}

	// 备份里必须含未 checkpoint 的那一行。
	bd, err := OpenRaw(backup)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := bd.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = 'T-3'`).Scan(&n); err != nil {
		bd.Close()
		t.Fatal(err)
	}
	bd.Close()
	if n != 1 {
		t.Fatalf("备份丢了 WAL 中的行，T-3 count=%d", n)
	}

	// 破坏现场后恢复，计数与引用必须一致。
	if err := WithTx(path, func(tx *sql.Tx) error { return DeleteTasks(tx, "p1") }); err != nil {
		t.Fatal(err)
	}
	live.Close()
	if err := Restore(backup, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-wal"); !os.IsNotExist(err) {
		t.Fatal("恢复后不应残留旧 -wal")
	}
	got, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()
	if err := got.QueryRow(`SELECT COUNT(*) FROM tasks WHERE project_id = 'p1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("恢复后 tasks=%d，期望 3", n)
	}
	probs, err := referenceProblems(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(probs) != 0 {
		t.Fatalf("恢复后引用不一致: %v", probs)
	}
}

func TestVerifyBackupDetectsDivergence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maw.db")
	seed(t, path)
	backup := filepath.Join(dir, "maw.db.bak")
	if err := Backup(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := WithTx(path, func(tx *sql.Tx) error {
		return UpsertTask(tx, &Task{ProjectID: "p1", ID: "T-9", Title: "later", Status: "ready"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(path, backup); err == nil {
		t.Fatal("源库多了行，校验应报不一致")
	}
	if err := VerifyBackup("", backup); err != nil {
		t.Fatalf("只校验备份自身应通过: %v", err)
	}
}

func TestMigrateWithBackupRollsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maw.db")
	// 构造一个 meta 表结构不对、迁移必然失败的老库。
	bad := openWritableRaw(t, path)
	if _, err := bad.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY)`); err != nil {
		bad.Close()
		t.Fatal(err)
	}
	bad.Close()

	backup := filepath.Join(dir, "maw.db.bak")
	if err := MigrateWithBackup(path, backup); err == nil {
		t.Fatal("迁移应当失败")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("迁移失败也应留下备份: %v", err)
	}
	// 回滚后的库应与备份逐字节一致，且仍是迁移前的老结构（没有被半套 schema 污染）。
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, want) {
		t.Fatal("回滚后的库应与备份逐字节一致")
	}
	if _, err := os.Stat(path + "-wal"); !os.IsNotExist(err) {
		t.Fatal("回滚后不应残留半套迁移写入的 -wal")
	}
	restored, err := OpenRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var cols int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('meta')`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if cols != 1 {
		t.Fatalf("老库 meta 应为单列，实际 %d 列（迁移残留）", cols)
	}
	var tables int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='tasks'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatal("回滚后不应出现迁移新建的 tasks 表")
	}
}

func TestMigrateWithBackupNewDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh", "maw.db")
	if err := MigrateWithBackup(path, ""); err != nil {
		t.Fatalf("全新库无需备份即可迁移: %v", err)
	}
	st, err := CheckWriterState(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.SchemaVersion != LatestVersion() || st.MinWriterVersion != version.Version {
		t.Fatalf("新库写者状态不符: %+v（期望 schema=%d）", st, LatestVersion())
	}
}

func TestCheckWriterStateRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maw.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	raw := openWritableRaw(t, path)
	if _, err := raw.Exec(`UPDATE meta SET value = '99' WHERE key = 'schema_version'`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()

	if _, err := CheckWriterState(path); !errors.Is(err, ErrSchemaAhead) {
		t.Fatalf("期望 ErrSchemaAhead，实际 %v", err)
	}
	if _, err := Open(path); !errors.Is(err, ErrSchemaAhead) {
		t.Fatalf("Open 也应拒绝更新的库，实际 %v", err)
	}
	if err := WithTx(path, func(tx *sql.Tx) error { return nil }); !errors.Is(err, ErrSchemaAhead) {
		t.Fatalf("WithTx 应拒绝写入更新的库，实际 %v", err)
	}
	// 拒绝时不得把 schema_version 写回旧值。
	ro, err := OpenRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var v string
	if err := ro.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "99" {
		t.Fatalf("schema_version 被覆盖为 %q", v)
	}
}

func TestCheckWriterStateMinWriterVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maw.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	raw := openWritableRaw(t, path)
	if _, err := raw.Exec(`UPDATE meta SET value = '99.0.0' WHERE key = 'min_writer_version'`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()
	if _, err := CheckWriterState(path); !errors.Is(err, ErrWriterTooOld) {
		t.Fatalf("期望 ErrWriterTooOld，实际 %v", err)
	}
	if err := Init(path); !errors.Is(err, ErrWriterTooOld) {
		t.Fatalf("Init 应拒绝更老的写者，实际 %v", err)
	}
}

func TestAssertSafeToUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maw.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	raw, err := OpenRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()

	if err := AssertSafeToUpgrade(raw); err != nil {
		t.Fatalf("空库应可升级: %v", err)
	}
	if err := WithTx(path, func(tx *sql.Tx) error {
		if err := UpsertProject(tx, &Project{ID: "p1", Name: "Demo", Path: "/tmp/demo"}); err != nil {
			return err
		}
		if err := UpsertTask(tx, &Task{ProjectID: "p1", ID: "T-1", Title: "wip", Status: "in_progress", Owner: "a1"}); err != nil {
			return err
		}
		if err := UpsertAgent(tx, &Agent{ProjectID: "p1", ID: "a1", Status: "working", TaskID: "T-1"}); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO locks(project_id, path, owner, task_id, expires_at) VALUES (?,?,?,?,?)`,
			"p1", "internal/db/db.go", "a1", "T-1", "2999-01-01T00:00:00Z")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err = AssertSafeToUpgrade(raw)
	var nd *NotDrainedError
	if !errors.As(err, &nd) {
		t.Fatalf("期望 NotDrainedError，实际 %v", err)
	}
	if len(nd.Blockers) != 3 {
		t.Fatalf("期望 task/agent/lock 各一条，实际 %+v", nd.Blockers)
	}

	if err := WithTx(path, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE tasks SET status='done', owner=NULL`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE agents SET status='idle', task_id=NULL`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE locks SET expires_at='2000-01-01T00:00:00Z'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := AssertSafeToUpgrade(raw); err != nil {
		t.Fatalf("排空后应可升级: %v", err)
	}
}

func TestLegacyDBWithoutMetaTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw := openWritableRaw(t, path)
	if _, err := raw.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()

	st, err := CheckWriterState(path)
	if err != nil {
		t.Fatalf("缺 meta 表的老库不应阻塞读状态: %v", err)
	}
	if st.SchemaVersion != 0 || !st.LegacyWriter() {
		t.Fatalf("老库状态不符: %+v", st)
	}
	if err := Init(path); err != nil {
		t.Fatalf("老库应能就地补 schema: %v", err)
	}
	st, err = CheckWriterState(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.SchemaVersion != LatestVersion() || st.LegacyWriter() {
		t.Fatalf("补完 schema 后状态不符: %+v（期望 schema=%d）", st, LatestVersion())
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.4.0", "0.4.0", 0},
		{"v0.10.0", "0.9.9", 1},
		{"0.4.1-rc1", "0.4.0", 1},
		{"", "0.0.1", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Fatalf("compareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func hasBlockerKind(blockers []UpgradeBlocker, kind string) bool {
	for _, b := range blockers {
		if b.Kind == kind {
			return true
		}
	}
	return false
}

// TestVerifyBackupCoversRuntimeTables 锁定 vnext 表的比对范围：
// 备份之后新写入的实例/Run/租约必须被校验发现（只比旧表会漏掉）。
func TestVerifyBackupCoversRuntimeTables(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	instanceID := seedClaim(t, path)
	mustWithTx(t, path, func(tx *sql.Tx) error {
		_, err := ClaimRun(tx, claimSpec(instanceID, "backup-1", "/tmp/ws/backup-1", 1))
		return err
	})

	backup := filepath.Join(t.TempDir(), "maw.db.bak")
	if err := Backup(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(path, backup); err != nil {
		t.Fatalf("同步状态下应通过: %v", err)
	}

	// 再领一次，只有 runs/resource_leases/task_instances 会变。
	mustWithTx(t, path, func(tx *sql.Tx) error {
		inst, err := CreateInstance(tx, testProject, "VN-601", InstanceDraft{Title: "第二条"})
		if err != nil {
			return err
		}
		_, err = ClaimRun(tx, claimSpec(string(inst.InstanceID), "backup-2", "/tmp/ws/backup-2", 1))
		return err
	})
	err := VerifyBackup(path, backup)
	if err == nil {
		t.Fatal("新增 Run/租约后，备份校验必须报不一致")
	}
	if !strings.Contains(err.Error(), "数据不一致") {
		t.Fatalf("期望行数不一致，实际 %v", err)
	}
}

// TestAssertSafeToUpgradeBlocksOnRun 锁定「未排空运行」判据来自 Run 与租约，
// 而不是只看旧表的任务状态。
func TestAssertSafeToUpgradeBlocksOnRun(t *testing.T) {
	path := freshPath(t)
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	instanceID := seedClaim(t, path)
	conn, err := OpenRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := AssertSafeToUpgrade(conn); err != nil {
		t.Fatalf("领取前应可升级: %v", err)
	}

	mustWithTx(t, path, func(tx *sql.Tx) error {
		_, err := ClaimRun(tx, claimSpec(instanceID, "drain-1", "/tmp/ws/drain", 1))
		return err
	})
	err = AssertSafeToUpgrade(conn)
	var nd *NotDrainedError
	if !errors.As(err, &nd) {
		t.Fatalf("存在占用中的 Run 时应拒绝升级，实际 %v", err)
	}
	if !hasBlockerKind(nd.Blockers, "run") {
		t.Fatalf("缺少 run 类阻断项: %+v", nd.Blockers)
	}
	if !hasBlockerKind(nd.Blockers, "lease") {
		t.Fatalf("缺少 lease 类阻断项: %+v", nd.Blockers)
	}
}
