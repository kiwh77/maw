package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/kiwh77/maw/internal/version"
)

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

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	MainBranch  string `json:"main_branch"`
	MaxParallel int    `json:"max_parallel"`
	CreatedAt   string `json:"created_at"`
	LastSeen    string `json:"last_seen"`
}

type Task struct {
	ProjectID     string         `json:"project_id"`
	ProjectName   string         `json:"project_name,omitempty"`
	ID            string         `json:"id"`
	Title         string         `json:"title"`
	Status        string         `json:"status"`
	Priority      int            `json:"priority"`
	MergeOrder    int            `json:"merge_order"`
	Owner         string         `json:"owner"`
	Deps          []string       `json:"deps"`
	WriteScope    []string       `json:"write_scope"`
	ReadScope     []string       `json:"read_scope"`
	Acceptance    []string       `json:"acceptance"`
	Branch        string         `json:"branch"`
	Worktree      string         `json:"worktree"`
	Attempts      int            `json:"attempts"`
	BlockedReason string         `json:"blocked_reason"`
	Heartbeat     string         `json:"heartbeat"`
	PrevStatus    string         `json:"prev_status"`
	Revision      int            `json:"revision"`
	Extra         map[string]any `json:"extra"`
}

type Agent struct {
	ProjectID    string         `json:"project_id"`
	ProjectName  string         `json:"project_name,omitempty"`
	ID           string         `json:"id"`
	Role         string         `json:"role"`
	Status       string         `json:"status"`
	TaskID       string         `json:"task_id"`
	Worktree     string         `json:"worktree"`
	Branch       string         `json:"branch"`
	RegisteredAt string         `json:"registered_at"`
	LastSeen     string         `json:"last_seen"`
	Meta         map[string]any `json:"meta"`
}

// Event 任务/系统事件日志（progress、status_changed 等）
type Event struct {
	ID        int64          `json:"id"`
	TS        string         `json:"ts"`
	ProjectID string         `json:"project_id"`
	Actor     string         `json:"actor"`
	Type      string         `json:"type"`
	TaskID    string         `json:"task_id"`
	Data      map[string]any `json:"data"`
}

func UTCNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// sqliteDSN builds a modernc DSN with busy_timeout (wait for locks) and
// BEGIN IMMEDIATE so writers queue instead of racing deferred upgrades.
func sqliteDSN(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(30000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	return u.String()
}

func openSQLite(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	// Belt-and-suspenders: DSN already sets these on each new connection.
	if _, err := db.Exec("PRAGMA busy_timeout=30000"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// IsBusy reports SQLITE_BUSY / SQLITE_LOCKED (incl. extended codes) or the
// common "database is locked" message from drivers that wrap the code away.
func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() & 0xff {
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked")
}

func Open(path string) (*sql.DB, error) {
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	if err := EnsureSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenRaw 打开数据库但不跑 schema 迁移（用于读取旧库）
func OpenRaw(path string) (*sql.DB, error) {
	return openSQLite(path)
}

func EnsureSchema(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('schema_version', ?)`, version.SchemaVersion)
	return err
}

func Init(path string) error {
	db, err := Open(path)
	if err != nil {
		return err
	}
	return db.Close()
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	if len(b) == 0 {
		return "[]"
	}
	return string(b)
}

func parseJSONArr(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const (
	withTxMaxAttempts = 8
	withTxBackoffMin  = 50 * time.Millisecond
	withTxBackoffMax  = 2 * time.Second
)

func withTxOnce(path string, fn func(*sql.Tx) error) error {
	db, err := Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin() // _txlock=immediate → BEGIN IMMEDIATE
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// WithTx runs fn in a write transaction. On SQLITE_BUSY it retries with
// exponential backoff (busy_timeout already waits up to 30s per attempt).
func WithTx(path string, fn func(*sql.Tx) error) error {
	backoff := withTxBackoffMin
	var last error
	for attempt := 0; attempt < withTxMaxAttempts; attempt++ {
		last = withTxOnce(path, fn)
		if last == nil || !IsBusy(last) {
			return last
		}
		if attempt+1 == withTxMaxAttempts {
			break
		}
		time.Sleep(backoff)
		if backoff < withTxBackoffMax {
			backoff *= 2
			if backoff > withTxBackoffMax {
				backoff = withTxBackoffMax
			}
		}
	}
	return last
}

func UpsertProject(tx *sql.Tx, p *Project) error {
	now := UTCNow()
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	p.LastSeen = now
	// MaxParallel=0 表示不设建议槽位（领取本就不卡槽）；勿强制改成 4
	if p.MainBranch == "" {
		p.MainBranch = "main"
	}
	_, err := tx.Exec(`
INSERT INTO projects(id, name, path, main_branch, max_parallel, created_at, last_seen)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name, path=excluded.path, main_branch=excluded.main_branch,
  max_parallel=excluded.max_parallel, last_seen=excluded.last_seen
`, p.ID, p.Name, p.Path, p.MainBranch, p.MaxParallel, p.CreatedAt, p.LastSeen)
	return err
}

func GetProject(tx *sql.Tx, id string) (*Project, error) {
	row := tx.QueryRow(`SELECT id, name, path, main_branch, max_parallel, created_at, last_seen FROM projects WHERE id = ?`, id)
	return scanProject(row)
}

func GetProjectByPath(tx *sql.Tx, path string) (*Project, error) {
	row := tx.QueryRow(`SELECT id, name, path, main_branch, max_parallel, created_at, last_seen FROM projects WHERE path = ?`, path)
	return scanProject(row)
}

func ListProjects(tx *sql.Tx) ([]*Project, error) {
	rows, err := tx.Query(`SELECT id, name, path, main_branch, max_parallel, created_at, last_seen FROM projects ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanProject(row interface{ Scan(dest ...any) error }) (*Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Name, &p.Path, &p.MainBranch, &p.MaxParallel, &p.CreatedAt, &p.LastSeen)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

const taskCols = `project_id, id, title, status, priority, merge_order, owner, deps_json, write_scope_json, read_scope_json, acceptance_json, branch, worktree, attempts, blocked_reason, heartbeat, prev_status, revision, extra_json`

func scanTask(row interface{ Scan(dest ...any) error }) (*Task, error) {
	var t Task
	var owner, branch, wt, blocked, hb, prev sql.NullString
	var deps, ws, rs, acc, extra string
	err := row.Scan(
		&t.ProjectID, &t.ID, &t.Title, &t.Status, &t.Priority, &t.MergeOrder, &owner,
		&deps, &ws, &rs, &acc, &branch, &wt, &t.Attempts, &blocked, &hb, &prev, &t.Revision, &extra,
	)
	if err != nil {
		return nil, err
	}
	t.Owner = owner.String
	t.Branch = branch.String
	t.Worktree = wt.String
	t.BlockedReason = blocked.String
	t.Heartbeat = hb.String
	t.PrevStatus = prev.String
	t.Deps = parseJSONArr(deps)
	t.WriteScope = parseJSONArr(ws)
	t.ReadScope = parseJSONArr(rs)
	t.Acceptance = parseJSONArr(acc)
	t.Extra = map[string]any{}
	_ = json.Unmarshal([]byte(extra), &t.Extra)
	return &t, nil
}

func GetTask(tx *sql.Tx, projectID, id string) (*Task, error) {
	row := tx.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE project_id = ? AND id = ?`, projectID, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func ListTasks(tx *sql.Tx, projectID string) ([]*Task, error) {
	rows, err := tx.Query(`SELECT `+taskCols+` FROM tasks WHERE project_id = ? ORDER BY priority ASC, merge_order ASC, id ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListTasksFiltered 全局/筛选列表；空字符串表示不限
func ListTasksFiltered(tx *sql.Tx, projectID, agent, status string) ([]*Task, error) {
	q := `SELECT t.project_id, t.id, t.title, t.status, t.priority, t.merge_order, t.owner,
		t.deps_json, t.write_scope_json, t.read_scope_json, t.acceptance_json, t.branch, t.worktree,
		t.attempts, t.blocked_reason, t.heartbeat, t.prev_status, t.revision, t.extra_json,
		COALESCE(p.name,'')
		FROM tasks t LEFT JOIN projects p ON p.id = t.project_id WHERE 1=1`
	var args []any
	if projectID != "" {
		q += ` AND t.project_id = ?`
		args = append(args, projectID)
	}
	if agent != "" {
		q += ` AND t.owner = ?`
		args = append(args, agent)
	}
	if status != "" {
		q += ` AND t.status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY p.name ASC, t.priority ASC, t.merge_order ASC, t.id ASC`
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		var t Task
		var owner, branch, wt, blocked, hb, prev sql.NullString
		var deps, ws, rs, acc, extra, pname string
		err := rows.Scan(
			&t.ProjectID, &t.ID, &t.Title, &t.Status, &t.Priority, &t.MergeOrder, &owner,
			&deps, &ws, &rs, &acc, &branch, &wt, &t.Attempts, &blocked, &hb, &prev, &t.Revision, &extra,
			&pname,
		)
		if err != nil {
			return nil, err
		}
		t.Owner = owner.String
		t.Branch = branch.String
		t.Worktree = wt.String
		t.BlockedReason = blocked.String
		t.Heartbeat = hb.String
		t.PrevStatus = prev.String
		t.Deps = parseJSONArr(deps)
		t.WriteScope = parseJSONArr(ws)
		t.ReadScope = parseJSONArr(rs)
		t.Acceptance = parseJSONArr(acc)
		t.Extra = map[string]any{}
		_ = json.Unmarshal([]byte(extra), &t.Extra)
		t.ProjectName = pname
		out = append(out, &t)
	}
	return out, rows.Err()
}

func UpsertTask(tx *sql.Tx, t *Task) error {
	if t.Extra == nil {
		t.Extra = map[string]any{}
	}
	_, err := tx.Exec(`
INSERT INTO tasks(
  project_id, id, title, status, priority, merge_order, owner,
  deps_json, write_scope_json, read_scope_json, acceptance_json,
  branch, worktree, attempts, blocked_reason, heartbeat, prev_status, revision, extra_json
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(project_id, id) DO UPDATE SET
  title=excluded.title, status=excluded.status, priority=excluded.priority,
  merge_order=excluded.merge_order, owner=excluded.owner,
  deps_json=excluded.deps_json, write_scope_json=excluded.write_scope_json,
  read_scope_json=excluded.read_scope_json, acceptance_json=excluded.acceptance_json,
  branch=excluded.branch, worktree=excluded.worktree, attempts=excluded.attempts,
  blocked_reason=excluded.blocked_reason, heartbeat=excluded.heartbeat,
  prev_status=excluded.prev_status, revision=excluded.revision, extra_json=excluded.extra_json
`, t.ProjectID, t.ID, t.Title, t.Status, t.Priority, t.MergeOrder, nullStr(t.Owner),
		mustJSON(t.Deps), mustJSON(t.WriteScope), mustJSON(t.ReadScope), mustJSON(t.Acceptance),
		nullStr(t.Branch), nullStr(t.Worktree), t.Attempts, nullStr(t.BlockedReason),
		nullStr(t.Heartbeat), nullStr(t.PrevStatus), t.Revision, mustJSON(t.Extra))
	return err
}

func DeleteTasks(tx *sql.Tx, projectID string) error {
	_, err := tx.Exec(`DELETE FROM tasks WHERE project_id = ?`, projectID)
	return err
}

// DeleteTask 删除单条任务（plan apply 剔除蓝图外的 idle 题时用）。
func DeleteTask(tx *sql.Tx, projectID, id string) error {
	_, err := tx.Exec(`DELETE FROM tasks WHERE project_id = ? AND id = ?`, projectID, id)
	return err
}

func AppendEvent(tx *sql.Tx, projectID, actor, typ, taskID string, data any) error {
	if data == nil {
		data = map[string]any{}
	}
	_, err := tx.Exec(
		`INSERT INTO events(ts, project_id, actor, type, task_id, data_json) VALUES (?,?,?,?,?,?)`,
		UTCNow(), nullStr(projectID), actor, typ, nullStr(taskID), mustJSON(data),
	)
	return err
}

// FindTask 按任务 ID 查找；projectID 非空时限定工程，否则取第一条匹配并带上 project_name
func FindTask(tx *sql.Tx, projectID, id string) (*Task, error) {
	if projectID != "" {
		t, err := GetTask(tx, projectID, id)
		if err != nil || t == nil {
			return t, err
		}
		if p, e := GetProject(tx, projectID); e == nil && p != nil {
			t.ProjectName = p.Name
		}
		return t, nil
	}
	row := tx.QueryRow(`
SELECT t.project_id, t.id, t.title, t.status, t.priority, t.merge_order, t.owner,
  t.deps_json, t.write_scope_json, t.read_scope_json, t.acceptance_json, t.branch, t.worktree,
  t.attempts, t.blocked_reason, t.heartbeat, t.prev_status, t.revision, t.extra_json,
  COALESCE(p.name,'')
FROM tasks t LEFT JOIN projects p ON p.id = t.project_id
WHERE t.id = ? ORDER BY t.project_id LIMIT 1`, id)
	t, err := scanTaskWithName(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func scanTaskWithName(row interface{ Scan(dest ...any) error }) (*Task, error) {
	var t Task
	var owner, branch, wt, blocked, hb, prev sql.NullString
	var deps, ws, rs, acc, extra, pname string
	err := row.Scan(
		&t.ProjectID, &t.ID, &t.Title, &t.Status, &t.Priority, &t.MergeOrder, &owner,
		&deps, &ws, &rs, &acc, &branch, &wt, &t.Attempts, &blocked, &hb, &prev, &t.Revision, &extra,
		&pname,
	)
	if err != nil {
		return nil, err
	}
	t.Owner = owner.String
	t.Branch = branch.String
	t.Worktree = wt.String
	t.BlockedReason = blocked.String
	t.Heartbeat = hb.String
	t.PrevStatus = prev.String
	t.ProjectName = pname
	t.Deps = parseJSONArr(deps)
	t.WriteScope = parseJSONArr(ws)
	t.ReadScope = parseJSONArr(rs)
	t.Acceptance = parseJSONArr(acc)
	t.Extra = map[string]any{}
	_ = json.Unmarshal([]byte(extra), &t.Extra)
	return &t, nil
}

// ListEventsByTask 返回某任务的事件日志（按时间正序）；limit<=0 默认 200
func ListEventsByTask(tx *sql.Tx, projectID, taskID string, limit int) ([]*Event, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT id, ts, COALESCE(project_id,''), actor, type, COALESCE(task_id,''), data_json
		FROM events WHERE task_id = ?`
	args := []any{taskID}
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY ts ASC, id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		var e Event
		var data string
		if err := rows.Scan(&e.ID, &e.TS, &e.ProjectID, &e.Actor, &e.Type, &e.TaskID, &data); err != nil {
			return nil, err
		}
		e.Data = map[string]any{}
		_ = json.Unmarshal([]byte(data), &e.Data)
		out = append(out, &e)
	}
	return out, rows.Err()
}

func SetMeta(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`, key, value)
	return err
}

func GetMeta(db *sql.DB, key string) (string, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func UpsertAgent(tx *sql.Tx, a *Agent) error {
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	now := UTCNow()
	if a.RegisteredAt == "" {
		a.RegisteredAt = now
	}
	if a.LastSeen == "" {
		a.LastSeen = now
	}
	_, err := tx.Exec(`
INSERT INTO agents(project_id, id, role, status, task_id, worktree, branch, registered_at, last_seen, meta_json)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(project_id, id) DO UPDATE SET
  role=excluded.role, status=excluded.status, task_id=excluded.task_id,
  worktree=excluded.worktree, branch=excluded.branch,
  last_seen=excluded.last_seen, meta_json=excluded.meta_json
`, a.ProjectID, a.ID, a.Role, a.Status, nullStr(a.TaskID), nullStr(a.Worktree), nullStr(a.Branch),
		a.RegisteredAt, a.LastSeen, mustJSON(a.Meta))
	return err
}

func GetAgent(tx *sql.Tx, projectID, id string) (*Agent, error) {
	row := tx.QueryRow(`SELECT project_id, id, role, status, task_id, worktree, branch, registered_at, last_seen, meta_json FROM agents WHERE project_id = ? AND id = ?`, projectID, id)
	return scanAgent(row)
}

func ListAgents(tx *sql.Tx, projectID string) ([]*Agent, error) {
	return ListAgentsFiltered(tx, projectID, "")
}

func ListAgentsFiltered(tx *sql.Tx, projectID, agent string) ([]*Agent, error) {
	q := `SELECT a.project_id, a.id, a.role, a.status, a.task_id, a.worktree, a.branch, a.registered_at, a.last_seen, a.meta_json, COALESCE(p.name,'')
		FROM agents a LEFT JOIN projects p ON p.id = a.project_id WHERE 1=1`
	var args []any
	if projectID != "" {
		q += ` AND a.project_id = ?`
		args = append(args, projectID)
	}
	if agent != "" {
		q += ` AND a.id = ?`
		args = append(args, agent)
	}
	q += ` ORDER BY a.last_seen DESC`
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Agent
	for rows.Next() {
		var a Agent
		var taskID, wt, branch sql.NullString
		var meta, pname string
		if err := rows.Scan(&a.ProjectID, &a.ID, &a.Role, &a.Status, &taskID, &wt, &branch, &a.RegisteredAt, &a.LastSeen, &meta, &pname); err != nil {
			return nil, err
		}
		a.TaskID = taskID.String
		a.Worktree = wt.String
		a.Branch = branch.String
		a.ProjectName = pname
		a.Meta = map[string]any{}
		_ = json.Unmarshal([]byte(meta), &a.Meta)
		out = append(out, &a)
	}
	return out, rows.Err()
}

func scanAgent(row interface{ Scan(dest ...any) error }) (*Agent, error) {
	var a Agent
	var taskID, wt, branch sql.NullString
	var meta string
	err := row.Scan(&a.ProjectID, &a.ID, &a.Role, &a.Status, &taskID, &wt, &branch, &a.RegisteredAt, &a.LastSeen, &meta)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.TaskID = taskID.String
	a.Worktree = wt.String
	a.Branch = branch.String
	a.Meta = map[string]any{}
	_ = json.Unmarshal([]byte(meta), &a.Meta)
	return &a, nil
}

func ClearAgentTask(tx *sql.Tx, projectID, id string) error {
	_, err := tx.Exec(`UPDATE agents SET status = 'idle', task_id = NULL, worktree = NULL, branch = NULL, last_seen = ? WHERE project_id = ? AND id = ?`, UTCNow(), projectID, id)
	return err
}

func UpsertSharedFile(tx *sql.Tx, projectID, path, ownerTask string) error {
	_, err := tx.Exec(`INSERT OR REPLACE INTO shared_files(project_id, path, owner_task) VALUES (?,?,?)`, projectID, path, ownerTask)
	return err
}

func DeleteSharedFiles(tx *sql.Tx, projectID string) error {
	_, err := tx.Exec(`DELETE FROM shared_files WHERE project_id = ?`, projectID)
	return err
}

// SharedFileRow is a declared shared path that only one active task may touch.
type SharedFileRow struct {
	Path      string
	OwnerTask string
}

func ListSharedFiles(tx *sql.Tx, projectID string) ([]SharedFileRow, error) {
	rows, err := tx.Query(`SELECT path, owner_task FROM shared_files WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SharedFileRow
	for rows.Next() {
		var r SharedFileRow
		if err := rows.Scan(&r.Path, &r.OwnerTask); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
