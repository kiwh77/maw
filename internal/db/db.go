package db

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wuhao/goal-agents/internal/version"
)

const schema = `
CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
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
    extra_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts TEXT NOT NULL,
    actor TEXT NOT NULL,
    type TEXT NOT NULL,
    task_id TEXT,
    data_json TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS locks (
    path TEXT PRIMARY KEY,
    owner TEXT NOT NULL,
    task_id TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts TEXT NOT NULL,
    sender TEXT NOT NULL,
    recipient TEXT NOT NULL,
    type TEXT NOT NULL,
    task_id TEXT,
    body_json TEXT NOT NULL DEFAULT '{}',
    read INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS shared_files (
    path TEXT PRIMARY KEY,
    owner_task TEXT NOT NULL
);
`

type Task struct {
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

func UTCNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
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

func Init(path string) error {
	db, err := Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('schema_version', ?)`, version.SchemaVersion)
	return err
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

func scanTask(row interface{ Scan(dest ...any) error }) (*Task, error) {
	var t Task
	var owner, branch, wt, blocked, hb, prev sql.NullString
	var deps, ws, rs, acc, extra string
	err := row.Scan(
		&t.ID, &t.Title, &t.Status, &t.Priority, &t.MergeOrder, &owner,
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

const taskCols = `id, title, status, priority, merge_order, owner, deps_json, write_scope_json, read_scope_json, acceptance_json, branch, worktree, attempts, blocked_reason, heartbeat, prev_status, revision, extra_json`

func GetTask(tx *sql.Tx, id string) (*Task, error) {
	row := tx.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

func ListTasks(tx *sql.Tx) ([]*Task, error) {
	rows, err := tx.Query(`SELECT ` + taskCols + ` FROM tasks ORDER BY priority ASC, merge_order ASC, id ASC`)
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

func UpsertTask(tx *sql.Tx, t *Task) error {
	if t.Extra == nil {
		t.Extra = map[string]any{}
	}
	_, err := tx.Exec(`
INSERT INTO tasks(
  id, title, status, priority, merge_order, owner,
  deps_json, write_scope_json, read_scope_json, acceptance_json,
  branch, worktree, attempts, blocked_reason, heartbeat, prev_status, revision, extra_json
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  title=excluded.title, status=excluded.status, priority=excluded.priority,
  merge_order=excluded.merge_order, owner=excluded.owner,
  deps_json=excluded.deps_json, write_scope_json=excluded.write_scope_json,
  read_scope_json=excluded.read_scope_json, acceptance_json=excluded.acceptance_json,
  branch=excluded.branch, worktree=excluded.worktree, attempts=excluded.attempts,
  blocked_reason=excluded.blocked_reason, heartbeat=excluded.heartbeat,
  prev_status=excluded.prev_status, revision=excluded.revision, extra_json=excluded.extra_json
`, t.ID, t.Title, t.Status, t.Priority, t.MergeOrder, nullStr(t.Owner),
		mustJSON(t.Deps), mustJSON(t.WriteScope), mustJSON(t.ReadScope), mustJSON(t.Acceptance),
		nullStr(t.Branch), nullStr(t.Worktree), t.Attempts, nullStr(t.BlockedReason),
		nullStr(t.Heartbeat), nullStr(t.PrevStatus), t.Revision, mustJSON(t.Extra))
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func AppendEvent(tx *sql.Tx, actor, typ, taskID string, data any) error {
	if data == nil {
		data = map[string]any{}
	}
	_, err := tx.Exec(
		`INSERT INTO events(ts, actor, type, task_id, data_json) VALUES (?,?,?,?,?)`,
		UTCNow(), actor, typ, nullStr(taskID), mustJSON(data),
	)
	return err
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

func WithTx(path string, fn func(*sql.Tx) error) error {
	db, err := Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
