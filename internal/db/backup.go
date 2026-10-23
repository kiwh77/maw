package db

// 升级、备份、恢复与旧写者边界。
//
// 三条硬约束：
//
//  1. 写者版本闸门：库里的 schema_version 高于当前二进制时，新写者必须拒绝写入，
//     而不是用旧 schema 去覆盖新库。见 ErrSchemaAhead / CheckWriterState。
//  2. 旧写者（0.4 及更早）不读 meta.min_writer_version，也不看 sqlite_master，
//     会无条件 CREATE TABLE IF NOT EXISTS 并 INSERT OR REPLACE schema_version。
//     所以新字段保护不了它：破坏性升级必须在窗口内确认所有旧进程已退出，
//     由新写者断言运行已排空（AssertSafeToUpgrade）。
//  3. 备份必须读到一个一致快照。WAL 模式下直接拷贝 maw.db 会丢掉尚未 checkpoint
//     的帧，因此统一走 VACUUM INTO；恢复走临时文件 + rename，并清理旧的
//     -wal/-shm，避免旧日志回放覆盖恢复结果。

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/version"
)

var (
	// ErrSchemaAhead 库的 schema 比当前二进制新，禁止写入。
	ErrSchemaAhead = errors.New("db: 库 schema 版本高于当前二进制")
	// ErrWriterTooOld 库声明了更高的 min_writer_version，当前二进制不许写入。
	ErrWriterTooOld = errors.New("db: 库要求更高的写者版本")
)

// WriterState 是判断「能不能写这个库」所需的最小事实集。
type WriterState struct {
	SchemaVersion    int    `json:"schema_version"`
	MinWriterVersion string `json:"min_writer_version"`
	BinaryVersion    string `json:"binary_version"`
}

// LegacyWriter 报告该库是否由不写 min_writer_version 的旧二进制创建/打开过。
// 为 true 时不能依赖 min_writer_version 做任何保护。
func (s WriterState) LegacyWriter() bool { return s.MinWriterVersion == "" }

// CheckWriterState 以只读方式读取库的写者状态，并判断当前二进制能否写它。
// 库文件不存在视为全新库，返回零值且不报错。
func CheckWriterState(path string) (WriterState, error) {
	if path == "" {
		return WriterState{}, errors.New("db: 库路径为空")
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return WriterState{BinaryVersion: version.Version}, nil
		}
		return WriterState{}, err
	}
	d, err := openReadOnly(path)
	if err != nil {
		return WriterState{}, err
	}
	defer d.Close()
	return checkWriterGate(d)
}

// checkWriterGate 在已有连接上做版本闸门判断。
func checkWriterGate(d *sql.DB) (WriterState, error) {
	st := WriterState{BinaryVersion: version.Version}
	v, err := schemaVersionInDB(d)
	if err != nil {
		return st, err
	}
	st.SchemaVersion = v
	if s, ok, err := metaValue(d, metaMinWriterVersionKey); err != nil {
		return st, err
	} else if ok {
		st.MinWriterVersion = strings.TrimSpace(s)
	}
	// 以本二进制能推进到的台账末端为准（与 MigrateDB 写入的 meta.schema_version 对齐）。
	binarySchema := LatestVersion()
	if st.SchemaVersion > binarySchema {
		return st, fmt.Errorf("%w：库 v%d > 二进制 v%d，请升级 maw 后再写",
			ErrSchemaAhead, st.SchemaVersion, binarySchema)
	}
	if st.MinWriterVersion != "" && compareVersions(st.MinWriterVersion, version.Version) > 0 {
		return st, fmt.Errorf("%w：库要求 >= v%s，当前 v%s",
			ErrWriterTooOld, st.MinWriterVersion, version.Version)
	}
	return st, nil
}

// UpgradeBlocker 是一个阻止破坏性升级的占用者。
type UpgradeBlocker struct {
	Kind   string `json:"kind"`   // task / agent / lock
	ID     string `json:"id"`     //
	Detail string `json:"detail"` //
}

// NotDrainedError 表示还有未排空的运行/任务/锁，破坏性升级必须中止。
type NotDrainedError struct {
	Blockers []UpgradeBlocker
}

func (e *NotDrainedError) Error() string {
	if e == nil || len(e.Blockers) == 0 {
		return "db: 运行未排空"
	}
	const max = 10
	var b strings.Builder
	b.WriteString("db: 运行未排空，拒绝破坏性升级：")
	for i, bl := range e.Blockers {
		if i == max {
			fmt.Fprintf(&b, " …等 %d 项", len(e.Blockers))
			break
		}
		if i > 0 {
			b.WriteString("；")
		}
		fmt.Fprintf(&b, "%s %s(%s)", bl.Kind, bl.ID, bl.Detail)
	}
	return b.String()
}

// queryer 让断言同时适配 *sql.DB 与 *sql.Tx。
type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// AssertSafeToUpgrade 断言当前没有会与新写者并发写库的运行。
//
// 判据（宁可误报也不放过）：未结束的任务、非 idle 或仍挂任务的 agent、
// 尚未过期的路径锁。旧二进制不识别本断言，升级窗口仍须人工停掉所有旧进程。
func AssertSafeToUpgrade(q queryer) error {
	blockers, err := UpgradeBlockers(q)
	if err != nil {
		return err
	}
	if len(blockers) > 0 {
		return &NotDrainedError{Blockers: blockers}
	}
	return nil
}

// UpgradeBlockers 列出阻止破坏性升级的占用者。
func UpgradeBlockers(q queryer) ([]UpgradeBlocker, error) {
	var out []UpgradeBlocker

	rows, err := q.Query(`SELECT project_id, id, status, owner FROM tasks
		WHERE status IN ('in_progress', 'self_check', 'conflict')
		ORDER BY project_id, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid, id, status, owner sql.NullString
		if err := rows.Scan(&pid, &id, &status, &owner); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, UpgradeBlocker{Kind: "task", ID: pid.String + "/" + id.String, Detail: status.String + " owner=" + owner.String})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.Query(`SELECT project_id, id, status, COALESCE(task_id, '') FROM agents
		WHERE status <> 'idle' OR COALESCE(task_id, '') <> ''
		ORDER BY project_id, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid, id, status, taskID string
		if err := rows.Scan(&pid, &id, &status, &taskID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, UpgradeBlocker{Kind: "agent", ID: pid + "/" + id, Detail: status + " task=" + taskID})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.Query(`SELECT project_id, path, owner, task_id FROM locks
		WHERE expires_at > ? ORDER BY project_id, path`, UTCNow())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid, path, owner, taskID string
		if err := rows.Scan(&pid, &path, &owner, &taskID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, UpgradeBlocker{Kind: "lock", ID: pid + ":" + path, Detail: "owner=" + owner + " task=" + taskID})
	}
	rows.Close()
	return out, rows.Err()
}

// Backup 用 VACUUM INTO 把 path 的一致快照写到 dest。
// 与直接拷贝不同，VACUUM INTO 会带上尚未 checkpoint 的 WAL 帧。
func Backup(path, dest string) error {
	if path == "" || dest == "" {
		return errors.New("db: 备份源与目标都不能为空")
	}
	src, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dst, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	if src == dst {
		return errors.New("db: 备份目标不能是源库本身")
	}
	if _, err := os.Stat(src); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// VACUUM INTO 要求目标不存在，清掉同名残留（含可能的 wal/shm）。
	for _, p := range []string{dst, dst + "-wal", dst + "-shm"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	d, err := openReadOnly(src)
	if err != nil {
		return err
	}
	defer d.Close()
	if _, err := d.Exec(`VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("db: 备份失败: %w", err)
	}
	return nil
}

// VerifyBackup 校验备份可用，并在 source 非空且与备份不同文件时交叉比对
// schema 版本、各表行数与引用完整性问题。
func VerifyBackup(source, backupPath string) error {
	if backupPath == "" {
		return errors.New("db: 备份路径为空")
	}
	if err := verifyBackupFile(backupPath); err != nil {
		return err
	}
	if source == "" {
		return nil
	}
	srcAbs, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	bakAbs, err := filepath.Abs(backupPath)
	if err != nil {
		return err
	}
	if srcAbs == bakAbs {
		return nil
	}
	if _, err := os.Stat(srcAbs); err != nil {
		return err
	}
	src, err := openReadOnly(srcAbs)
	if err != nil {
		return err
	}
	defer src.Close()
	bak, err := openReadOnly(bakAbs)
	if err != nil {
		return err
	}
	defer bak.Close()

	srcVer, err := schemaVersionInDB(src)
	if err != nil {
		return err
	}
	bakVer, err := schemaVersionInDB(bak)
	if err != nil {
		return err
	}
	if srcVer != bakVer {
		return fmt.Errorf("db: 备份 schema 版本不一致：源 v%d，备份 v%d", srcVer, bakVer)
	}
	for _, t := range backupTables {
		srcN, err := tableCount(src, t)
		if err != nil {
			return err
		}
		bakN, err := tableCount(bak, t)
		if err != nil {
			return err
		}
		if srcN != bakN {
			return fmt.Errorf("db: 备份数据不一致：表 %s 源 %d 行，备份 %d 行", t, srcN, bakN)
		}
	}
	srcProbs, err := referenceProblems(src)
	if err != nil {
		return err
	}
	bakProbs, err := referenceProblems(bak)
	if err != nil {
		return err
	}
	if !sameStrings(srcProbs, bakProbs) {
		return fmt.Errorf("db: 备份引用一致性与源库不一致：源 %v，备份 %v", srcProbs, bakProbs)
	}
	return nil
}

// Restore 用备份原子替换 target：先写 .restore-tmp，再 rename 上位，
// 并清掉 target 的 -wal/-shm，避免旧日志回放覆盖恢复结果。
// 调用方必须保证此刻没有写者。
func Restore(backupPath, targetPath string) error {
	if backupPath == "" || targetPath == "" {
		return errors.New("db: 恢复的备份与目标都不能为空")
	}
	if err := verifyBackupFile(backupPath); err != nil {
		return err
	}
	target, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp := target + ".restore-tmp"
	if err := copyFile(backupPath, tmp); err != nil {
		return err
	}
	// 旧 WAL/SHM 属于被替换的库，必须先失效。
	for _, p := range []string{target + "-wal", target + "-shm"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// MigrateWithBackup 是升级的单一入口：先备份（源库存在时），再跑迁移；
// 迁移失败立刻用备份恢复，恢复也失败则把备份路径带回错误里。
func MigrateWithBackup(path, backupPath string) error {
	if path == "" {
		return errors.New("db: 库路径为空")
	}
	_, statErr := os.Stat(path)
	existed := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if existed {
		if backupPath == "" {
			return errors.New("db: 迁移前必须指定备份路径")
		}
		if err := Backup(path, backupPath); err != nil {
			return err
		}
		if err := VerifyBackup(path, backupPath); err != nil {
			return fmt.Errorf("db: 备份校验失败，已中止迁移: %w", err)
		}
	}
	err := Init(path)
	if err == nil {
		return nil
	}
	if !existed || backupPath == "" {
		return err
	}
	if rbErr := Restore(backupPath, path); rbErr != nil {
		return fmt.Errorf("db: 迁移失败且回滚失败，请手工用备份 %s 恢复: %w（迁移错误: %v）", backupPath, rbErr, err)
	}
	return fmt.Errorf("db: 迁移失败，已用备份 %s 回滚: %w", backupPath, err)
}

// backupTables 是备份校验比对的表清单，与 schema 保持同步。
var backupTables = []string{
	"meta", "projects", "tasks", "events", "locks", "messages", "shared_files", "agents",
}

func verifyBackupFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	d, err := openReadOnly(path)
	if err != nil {
		return err
	}
	defer d.Close()
	var res string
	if err := d.QueryRow(`PRAGMA integrity_check`).Scan(&res); err != nil {
		return fmt.Errorf("db: 备份 %s 完整性校验失败: %w", path, err)
	}
	if !strings.EqualFold(strings.TrimSpace(res), "ok") {
		return fmt.Errorf("db: 备份 %s 完整性校验失败: %s", path, res)
	}
	if _, err := checkWriterGate(d); err != nil {
		return fmt.Errorf("db: 备份 %s 不可由当前二进制恢复: %w", path, err)
	}
	probs, err := referenceProblems(d)
	if err != nil {
		return err
	}
	if len(probs) > 0 {
		return fmt.Errorf("db: 备份 %s 存在引用问题: %v", path, probs)
	}
	return nil
}

func openReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(30000)")
	u.RawQuery = q.Encode()
	d, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	return d, nil
}

func schemaVersionInDB(d *sql.DB) (int, error) {
	s, ok, err := metaValue(d, metaSchemaVersionKey)
	if err != nil || !ok {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, nil
	}
	return n, nil
}

// metaValue 容忍「没有 meta 表」或「meta 表结构不对」的老库/坏库：
// 读不出来就当没有，交由后续迁移报错。
func metaValue(d *sql.DB, key string) (string, bool, error) {
	var v string
	err := d.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == nil {
		return v, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	var name string
	if e := d.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&name); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, e
	}
	return "", false, nil
}

func tableCount(d *sql.DB, table string) (int, error) {
	ok, err := tableExists(d, table)
	if err != nil || !ok {
		return 0, err
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM "` + table + `"`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func tableExists(d *sql.DB, table string) (bool, error) {
	var name string
	err := d.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// referenceProblems 用逻辑外键检查数据一致性（schema 未声明 FK，
// 历史数据可能存在游离行，因此只比对「问题清单」而不是直接判死）。
func referenceProblems(d *sql.DB) ([]string, error) {
	checks := []struct {
		name  string
		query string
	}{
		{"tasks.project_id", `SELECT COUNT(*) FROM tasks t
			WHERE NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = t.project_id)`},
		{"agents.project_id", `SELECT COUNT(*) FROM agents a
			WHERE NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = a.project_id)`},
		{"events.project_id", `SELECT COUNT(*) FROM events e
			WHERE e.project_id IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = e.project_id)`},
		{"locks.project_id", `SELECT COUNT(*) FROM locks l
			WHERE NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = l.project_id)`},
		{"messages.project_id", `SELECT COUNT(*) FROM messages m
			WHERE m.project_id IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = m.project_id)`},
		{"shared_files.project_id", `SELECT COUNT(*) FROM shared_files s
			WHERE NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = s.project_id)`},
		{"agents.task_id", `SELECT COUNT(*) FROM agents a
			WHERE COALESCE(a.task_id, '') <> ''
			  AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.project_id = a.project_id AND t.id = a.task_id)`},
		{"shared_files.owner_task", `SELECT COUNT(*) FROM shared_files s
			WHERE NOT EXISTS (SELECT 1 FROM tasks t WHERE t.project_id = s.project_id AND t.id = s.owner_task)`},
	}
	var out []string
	for _, c := range checks {
		var n int
		if err := d.QueryRow(c.query).Scan(&n); err != nil {
			// 缺表/缺列的老库直接跳过该项。
			if strings.Contains(err.Error(), "no such table") || strings.Contains(err.Error(), "no such column") {
				continue
			}
			return nil, err
		}
		if n > 0 {
			out = append(out, fmt.Sprintf("%s=%d", c.name, n))
		}
	}
	return out, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

func sameStrings(a, b []string) bool {
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

// compareVersions 只比较 x.y.z 数字段；预发布/构建元数据忽略。
// 解析失败按 0 处理，避免版本串写坏时误锁库。
func compareVersions(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(s string) [3]int {
	var out [3]int
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	for i, part := range strings.Split(s, ".") {
		if i >= 3 {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return out
		}
		out[i] = n
	}
	return out
}
