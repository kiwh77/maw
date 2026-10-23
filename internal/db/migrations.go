package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Migration 是一条按版本号升序、只执行一次的 schema 变更。
//
// 已发布的条目不可回改：checksum 记进台账，改动会让 VerifySchema 报 ErrSchemaDrift，
// 而不是让不同机器悄悄跑出两种表结构。
type Migration struct {
	Version int
	Name    string
	SQL     string
	// Apply 是可选的 Go 数据步骤，在同一个事务里紧接 SQL 执行；
	// 需要按行计算身份或补账的迁移用它，而不是在 SQL 里硬编码。
	Apply func(*sql.Tx) error
}

// schema 是 vnext 的基线表结构。
//
// VN-101 之前，这份 DDL 由 EnsureSchema 在每次 Open／WithTx 时无条件重放；
// 现在它只是迁移序列里的第 3 版，只在 Migrate 里执行一次。
const schema = `
CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    path TEXT NOT NULL UNIQUE,
    main_branch TEXT NOT NULL DEFAULT 'main',
    max_parallel INTEGER NOT NULL DEFAULT 4,
    created_at TEXT NOT NULL,
    last_seen TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tasks (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    title TEXT NOT NULL,
    status TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 100,
    merge_order INTEGER NOT NULL DEFAULT 100,
    owner TEXT,
    deps_json TEXT NOT NULL DEFAULT '[]',
    write_scope_json TEXT NOT NULL DEFAULT '[]',
    read_scope_json TEXT NOT NULL DEFAULT '[]',
    acceptance_json TEXT NOT NULL DEFAULT '[]',
    branch TEXT,
    worktree TEXT,
    attempts INTEGER NOT NULL DEFAULT 0,
    blocked_reason TEXT,
    heartbeat TEXT,
    prev_status TEXT,
    revision INTEGER NOT NULL DEFAULT 0,
    extra_json TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (project_id, id)
);
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts TEXT NOT NULL,
    project_id TEXT,
    actor TEXT NOT NULL,
    type TEXT NOT NULL,
    task_id TEXT,
    data_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS locks (
    project_id TEXT NOT NULL,
    path TEXT NOT NULL,
    owner TEXT NOT NULL,
    task_id TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    PRIMARY KEY (project_id, path)
);
CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts TEXT NOT NULL,
    project_id TEXT,
    sender TEXT NOT NULL,
    recipient TEXT NOT NULL,
    type TEXT NOT NULL,
    task_id TEXT,
    body_json TEXT NOT NULL DEFAULT '{}',
    read INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS shared_files (
    project_id TEXT NOT NULL,
    path TEXT NOT NULL,
    owner_task TEXT NOT NULL,
    PRIMARY KEY (project_id, path)
);
CREATE TABLE IF NOT EXISTS agents (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'worker',
    status TEXT NOT NULL DEFAULT 'idle',
    task_id TEXT,
    worktree TEXT,
    branch TEXT,
    registered_at TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    meta_json TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (project_id, id)
);
`

// identityDDL 是 v4：任务实例身份、冻结版本与 tasks 的实例引用。
//
// 三条约束落在库里而不是只写在注释里：
//   - 同一 (工程, 编号) 至多一个活跃实例（部分唯一索引）；
//   - 活跃实例必须指向一个已冻结版本，退役实例必须写时间、原因且不持活跃指针（CHECK）；
//   - tasks 行的 instance_id 非空且唯一（触发器 + 部分唯一索引），已冻结版本不可改不可删（触发器）。
const identityDDL = `
CREATE TABLE IF NOT EXISTS task_instances (
    instance_id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    display_id TEXT NOT NULL,
    state TEXT NOT NULL,
    created_at TEXT NOT NULL,
    retired_at TEXT,
    retired_reason TEXT,
    active_version INTEGER,
    ambiguous INTEGER NOT NULL DEFAULT 0,
    ambiguity_note TEXT,
    CHECK (state IN ('active', 'retired')),
    CHECK (
        (state = 'active' AND active_version IS NOT NULL AND retired_at IS NULL
            AND retired_reason IS NULL AND ambiguous = 0)
        OR
        (state = 'retired' AND active_version IS NULL AND retired_at IS NOT NULL
            AND retired_reason IS NOT NULL)
    )
);
CREATE UNIQUE INDEX IF NOT EXISTS task_instances_one_active_display
    ON task_instances(project_id, display_id) WHERE state = 'active';
CREATE INDEX IF NOT EXISTS task_instances_project_state
    ON task_instances(project_id, state);
CREATE TABLE IF NOT EXISTS task_versions (
    instance_id TEXT NOT NULL,
    version_no INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    frozen_at TEXT NOT NULL,
    deps_json TEXT NOT NULL DEFAULT '[]',
    spec_json TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (instance_id, version_no),
    FOREIGN KEY (instance_id) REFERENCES task_instances(instance_id)
);
CREATE TRIGGER IF NOT EXISTS task_versions_frozen_no_update
BEFORE UPDATE ON task_versions
BEGIN SELECT RAISE(ABORT, 'task_versions: 已冻结版本不可修改'); END;
CREATE TRIGGER IF NOT EXISTS task_versions_frozen_no_delete
BEFORE DELETE ON task_versions
BEGIN SELECT RAISE(ABORT, 'task_versions: 已冻结版本不可删除'); END;
ALTER TABLE tasks ADD COLUMN instance_id TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS tasks_instance_id_key
    ON tasks(instance_id) WHERE instance_id IS NOT NULL;
CREATE TRIGGER IF NOT EXISTS tasks_instance_id_required_insert
BEFORE INSERT ON tasks
WHEN NEW.instance_id IS NULL OR NEW.instance_id = ''
BEGIN SELECT RAISE(ABORT, 'tasks.instance_id: 当前任务必须有实例身份'); END;
CREATE TRIGGER IF NOT EXISTS tasks_instance_id_required_update
BEFORE UPDATE ON tasks
WHEN NEW.instance_id IS NULL OR NEW.instance_id = ''
BEGIN SELECT RAISE(ABORT, 'tasks.instance_id: 当前任务必须有实例身份'); END;
`

// resourceDDL 是 v5：节点记录与资源占用。
//
// 唯一有效持有者落在库里：未释放的 resource_key 上有部分唯一索引，
// 因此「同一目录/同一槽位两个持有者」不需要靠调用方自觉。
const resourceDDL = `
CREATE TABLE IF NOT EXISTS nodes (
    node_id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    slot_limit INTEGER NOT NULL DEFAULT 0,
    account_quota INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    meta_json TEXT NOT NULL DEFAULT '{}',
    CHECK (slot_limit >= 0 AND account_quota >= 0)
);
CREATE TABLE IF NOT EXISTS resource_leases (
    lease_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    resource_key TEXT NOT NULL,
    project_id TEXT NOT NULL DEFAULT '',
    instance_id TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    generation INTEGER NOT NULL,
    holder TEXT NOT NULL DEFAULT '',
    acquired_at TEXT NOT NULL,
    released_at TEXT,
    release_generation INTEGER,
    release_reason TEXT,
    CHECK (kind IN ('node_slot', 'workspace', 'merge')),
    CHECK (generation >= 1),
    CHECK (released_at IS NULL OR release_generation IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS resource_leases_one_holder
    ON resource_leases(resource_key) WHERE released_at IS NULL;
CREATE INDEX IF NOT EXISTS resource_leases_project_kind
    ON resource_leases(project_id, kind, released_at);
CREATE TRIGGER IF NOT EXISTS resource_leases_release_once
BEFORE UPDATE ON resource_leases
WHEN OLD.released_at IS NOT NULL AND NEW.released_at IS NOT NULL
BEGIN SELECT RAISE(ABORT, 'resource_leases: 占用已释放，不能再次释放'); END;
`

// migrations 是显式迁移序列：版本号唯一、升序执行、只往前走。
//
// 版本号从 3 起：3 是 vnext 基线（沿用旧 0.x 把 schema_version 写成 3 的数值），
// 之后每次结构变更 +1。vnext 的权威是这张台账，而不是 version.SchemaVersion
// ——后者是发布契约常量，跟随发布节奏单独维护（本卡写盘范围不包含它）。
// meta.schema_version 只是给旧读法看的镜像，由 MigrateDB 写成台账末端。
//
// 历史 0.x 二进制没有台账，只在打开时重放 IF NOT EXISTS 并把 schema_version 写成 3；
// 这类旧库按「版本 0」处理，重放幂等的基线即得到同一结构，因此不需要单独的补账步骤。
var migrations = []Migration{
	{Version: 3, Name: "baseline_core_tables", SQL: schema},
	{Version: 4, Name: "task_identity", SQL: identityDDL, Apply: backfillTaskIdentity},
	{Version: 5, Name: "resource_leases", SQL: resourceDDL},
}

const (
	// ledgerTable 是迁移台账：每个版本一行，含内容摘要与应用时间。
	ledgerTable = "schema_migrations"

	ledgerDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    checksum TEXT NOT NULL,
    applied_at TEXT NOT NULL
)`

	// metaSchemaVersionKey 是旧读法的兼容键，只由 MigrateDB 推进。
	metaSchemaVersionKey = "schema_version"
)

var (
	// ErrNotMigrated 表示库文件缺失或尚未推进到当前迁移序列。
	ErrNotMigrated = errors.New("db: schema 未迁移")
	// ErrSchemaDrift 表示台账里的版本／摘要与当前二进制不一致（被手改或库比二进制新）。
	ErrSchemaDrift = errors.New("db: schema 台账与迁移序列不一致")
	// ErrReadOnlyStore 表示在只读 Store 上请求写或迁移。
	ErrReadOnlyStore = errors.New("db: 只读 Store 不允许写或迁移")
)

// querier 是 inspect 需要的最小连接面：只读查询，不需要事务。
type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Migrations 返回迁移序列的副本：调用方改切片不会动到内部注册表。
func Migrations() []Migration {
	out := make([]Migration, len(migrations))
	copy(out, migrations)
	return out
}

// LatestVersion 是本二进制的最新迁移版本，必须等于 version.SchemaVersion。
func LatestVersion() int {
	latest := 0
	for _, m := range migrations {
		if m.Version > latest {
			latest = m.Version
		}
	}
	return latest
}

func migrationChecksum(sqlText string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(sqlText)))
	return hex.EncodeToString(sum[:])
}

// SchemaStatus 是一次只读体检的结果；零值表示「什么都不知道」。
type SchemaStatus struct {
	Current   int   // 台账里已应用的最大版本；无台账为 0
	Latest    int   // 本二进制的最新版本
	Pending   []int // 尚未应用的版本（升序）
	Drift     []int // 台账存在但与迁移序列不符的版本
	HasLedger bool  // 台账表是否存在
}

// UpToDate 报告库是否正好停在当前迁移序列的末端。
func (s SchemaStatus) UpToDate() bool {
	return s.HasLedger && len(s.Pending) == 0 && len(s.Drift) == 0 && s.Current == s.Latest
}

// InspectSchema 只读检查迁移台账：不建表、不写 meta、不开写事务。
//
// 这是查询侧唯一允许的 schema 检查方式；它不会顺手把旧库升级掉。
func InspectSchema(q querier) (SchemaStatus, error) {
	st := SchemaStatus{Latest: LatestVersion()}
	known := make(map[int]string, len(migrations))
	for _, m := range migrations {
		known[m.Version] = migrationChecksum(m.SQL)
	}

	var name string
	err := q.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, ledgerTable).Scan(&name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		for _, m := range migrations {
			st.Pending = append(st.Pending, m.Version)
		}
		return st, nil
	case err != nil:
		return st, err
	}
	st.HasLedger = true

	rows, err := q.Query(`SELECT version, checksum FROM ` + ledgerTable + ` ORDER BY version ASC`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	applied := make(map[int]bool, len(migrations))
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return st, err
		}
		applied[v] = true
		if v > st.Current {
			st.Current = v
		}
		if want, ok := known[v]; !ok || want != sum {
			st.Drift = append(st.Drift, v)
		}
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	for _, m := range migrations {
		if !applied[m.Version] {
			st.Pending = append(st.Pending, m.Version)
		}
	}
	return st, nil
}

// VerifySchema 只读校验：台账齐备、摘要一致、已停到最新版本。
func VerifySchema(q querier) error {
	st, err := InspectSchema(q)
	if err != nil {
		return err
	}
	if !st.HasLedger {
		return fmt.Errorf("%w: 缺少 %s 台账", ErrNotMigrated, ledgerTable)
	}
	if len(st.Drift) > 0 {
		return fmt.Errorf("%w: 版本 %v", ErrSchemaDrift, st.Drift)
	}
	if len(st.Pending) > 0 {
		return fmt.Errorf("%w: 待应用迁移 %v", ErrNotMigrated, st.Pending)
	}
	return nil
}

// Migrate 打开（必要时创建目录）并把库推进到最新迁移版本。
//
// 这是唯一会改 schema 的入口：读路径与普通写事务都不调用它。
func Migrate(path string) error {
	if err := ensureParentDir(path); err != nil {
		return err
	}
	conn, err := openConn(path, writeConnOptions())
	if err != nil {
		return err
	}
	defer conn.Close()
	return MigrateDB(conn)
}

// MigrateDB 在已有连接上按序补齐未应用的迁移。
//
// 已是最新时只做只读体检、不发任何 DDL；需要补账时全部迁移与台账写入落在同一个
// 写事务里，中断即整体回滚，不留「表建了一半、台账没记」的中间态。
func MigrateDB(conn *sql.DB) error {
	st, err := InspectSchema(conn)
	if err != nil {
		return err
	}
	if len(st.Drift) > 0 {
		return fmt.Errorf("%w: 版本 %v", ErrSchemaDrift, st.Drift)
	}
	if st.HasLedger && len(st.Pending) == 0 {
		return nil
	}

	tx, err := conn.Begin() // _txlock=immediate → BEGIN IMMEDIATE
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ledgerDDL); err != nil {
		_ = tx.Rollback()
		return err
	}
	// 另一个进程可能刚补齐同一批迁移，进事务后重读，避免主键冲突与重复执行。
	applied, err := appliedVersions(tx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if _, err := tx.Exec(m.SQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("db: 应用迁移 %d（%s）失败: %w", m.Version, m.Name, err)
		}
		if m.Apply != nil {
			if err := m.Apply(tx); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("db: 迁移 %d（%s）的数据步骤失败: %w", m.Version, m.Name, err)
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO `+ledgerTable+`(version, name, checksum, applied_at) VALUES (?,?,?,?)`,
			m.Version, m.Name, migrationChecksum(m.SQL), UTCNow(),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	// 兼容旧读法：board／status 仍可能读 meta.schema_version。它只在这里推进。
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`,
		metaSchemaVersionKey, strconv.Itoa(LatestVersion())); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func appliedVersions(q querier) (map[int]bool, error) {
	rows, err := q.Query(`SELECT version FROM ` + ledgerTable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}
