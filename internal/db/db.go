package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/kiwh77/maw/internal/engine/model"
)

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
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name,omitempty"`
	ID          string `json:"id"`
	// InstanceID 是当前行的稳定实例身份（ti_...）。写入时留空表示「按编号找活跃实例，
	// 没有就新建」；已填的引用必须仍存在、未被退役，否则拒绝写入。
	InstanceID    string         `json:"instance_id"`
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

// Open 打开并推进迁移，返回裸连接（旧入口，需要自行 Close）。
//
// 它会改 schema，所以只给「本来就是写入口」的调用方用；纯查询请走
// OpenReadOnly／WithReadTx，那条路径不建表、不升级、不加写锁。
func Open(path string) (*sql.DB, error) {
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}
	db, err := openConn(path, writeConnOptions())
	if err != nil {
		return nil, err
	}
	if err := MigrateDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenRaw 打开数据库但不跑 schema 迁移（用于读取旧库）。
func OpenRaw(path string) (*sql.DB, error) {
	return openConn(path, readConnOptions())
}

// EnsureSchema 兼容旧名：等价于 MigrateDB，已是最新时不做任何写操作。
func EnsureSchema(db *sql.DB) error {
	return MigrateDB(db)
}

// Init 建目录、建库并把 schema 推进到最新版本。
func Init(path string) error {
	return Migrate(path)
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

// WithTx 是旧的一次性写入口：一次调用＝一个写事务。
//
// 迁移在重试循环之外、每次调用至多尝试一次补齐（已最新时只是两条只读查询）；
// 重试循环内只有 BEGIN IMMEDIATE 与业务语句，不建目录、不跑 DDL、不起进程。
// 需要复用的调用方应改用 OpenStore。
func WithTx(path string, fn func(*sql.Tx) error) error {
	s, err := OpenStore(path)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return err
	}
	return s.Write(fn)
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

const taskCols = `project_id, id, title, status, priority, merge_order, owner, deps_json, write_scope_json, read_scope_json, acceptance_json, branch, worktree, attempts, blocked_reason, heartbeat, prev_status, revision, extra_json, instance_id`

func scanTask(row interface{ Scan(dest ...any) error }) (*Task, error) {
	var t Task
	var owner, branch, wt, blocked, hb, prev sql.NullString
	var deps, ws, rs, acc, extra, instanceID string
	err := row.Scan(
		&t.ProjectID, &t.ID, &t.Title, &t.Status, &t.Priority, &t.MergeOrder, &owner,
		&deps, &ws, &rs, &acc, &branch, &wt, &t.Attempts, &blocked, &hb, &prev, &t.Revision, &extra,
		&instanceID,
	)
	if err != nil {
		return nil, err
	}
	t.InstanceID = instanceID
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
		t.instance_id, COALESCE(p.name,'')
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
		var deps, ws, rs, acc, extra, instanceID, pname string
		err := rows.Scan(
			&t.ProjectID, &t.ID, &t.Title, &t.Status, &t.Priority, &t.MergeOrder, &owner,
			&deps, &ws, &rs, &acc, &branch, &wt, &t.Attempts, &blocked, &hb, &prev, &t.Revision, &extra,
			&instanceID, &pname,
		)
		if err != nil {
			return nil, err
		}
		t.InstanceID = instanceID
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
	if err := bindTaskIdentity(tx, t); err != nil {
		return err
	}
	_, err := tx.Exec(`
INSERT INTO tasks(
  project_id, id, title, status, priority, merge_order, owner,
  deps_json, write_scope_json, read_scope_json, acceptance_json,
  branch, worktree, attempts, blocked_reason, heartbeat, prev_status, revision, extra_json,
  instance_id
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(project_id, id) DO UPDATE SET
  title=excluded.title, status=excluded.status, priority=excluded.priority,
  merge_order=excluded.merge_order, owner=excluded.owner,
  deps_json=excluded.deps_json, write_scope_json=excluded.write_scope_json,
  read_scope_json=excluded.read_scope_json, acceptance_json=excluded.acceptance_json,
  branch=excluded.branch, worktree=excluded.worktree, attempts=excluded.attempts,
  blocked_reason=excluded.blocked_reason, heartbeat=excluded.heartbeat,
  prev_status=excluded.prev_status, revision=excluded.revision, extra_json=excluded.extra_json,
  instance_id=excluded.instance_id
`, t.ProjectID, t.ID, t.Title, t.Status, t.Priority, t.MergeOrder, nullStr(t.Owner),
		mustJSON(t.Deps), mustJSON(t.WriteScope), mustJSON(t.ReadScope), mustJSON(t.Acceptance),
		nullStr(t.Branch), nullStr(t.Worktree), t.Attempts, nullStr(t.BlockedReason),
		nullStr(t.Heartbeat), nullStr(t.PrevStatus), t.Revision, mustJSON(t.Extra), t.InstanceID)
	return err
}

// DeleteTasks 清空某工程的任务行，并把它们的实例一并退役（plan apply --reset 用）。
//
// 退役与别名释放同事务：不会留下「实例还是 active、tasks 行却没了」的半状态。
func DeleteTasks(tx *sql.Tx, projectID string) error {
	if err := retireActiveInstances(tx, projectID, RetireReset); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM tasks WHERE project_id = ?`, projectID)
	return err
}

// DeleteTask 删除单条任务，并退役它持有的实例（plan apply 剔除蓝图外的 idle 题时用）。
func DeleteTask(tx *sql.Tx, projectID, id string) error {
	t, err := GetTask(tx, projectID, id)
	if err != nil {
		return err
	}
	if t != nil && t.InstanceID != "" {
		if err := RetireInstance(tx, t.InstanceID, RetireRemovedFromPlan); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`DELETE FROM tasks WHERE project_id = ? AND id = ?`, projectID, id)
	return err
}

// retireActiveInstances 退役某工程全部活跃实例，别名释放由 RetireInstance 完成。
func retireActiveInstances(tx *sql.Tx, projectID, reason string) error {
	insts, err := ListInstances(tx, projectID)
	if err != nil {
		return err
	}
	for _, inst := range insts {
		if inst.State != model.InstanceStateActive {
			continue
		}
		if err := RetireInstance(tx, string(inst.InstanceID), reason); err != nil {
			return err
		}
	}
	return nil
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
  t.instance_id, COALESCE(p.name,'')
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
	var deps, ws, rs, acc, extra, instanceID, pname string
	err := row.Scan(
		&t.ProjectID, &t.ID, &t.Title, &t.Status, &t.Priority, &t.MergeOrder, &owner,
		&deps, &ws, &rs, &acc, &branch, &wt, &t.Attempts, &blocked, &hb, &prev, &t.Revision, &extra,
		&instanceID, &pname,
	)
	if err != nil {
		return nil, err
	}
	t.InstanceID = instanceID
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

// ListEventsSince 返回 id > since 的全局/筛选事件（按 id 升序）；limit<=0 默认 200。
// projectID / actor 为空表示不限。
func ListEventsSince(tx *sql.Tx, since int64, projectID, actor string, limit int) ([]*Event, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT id, ts, COALESCE(project_id,''), actor, type, COALESCE(task_id,''), data_json
		FROM events WHERE id > ?`
	args := []any{since}
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}
	if actor != "" {
		q += ` AND actor = ?`
		args = append(args, actor)
	}
	q += ` ORDER BY id ASC LIMIT ?`
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
