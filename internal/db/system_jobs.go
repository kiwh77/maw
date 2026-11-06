package db

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// SystemJob 是系统 Agent 队列中的一条作业。
//
// RunID／OperationID／JournalDir 是「作业到执行」的反查链接：作业领取到受管执行时
// 立刻写进这三列，之后即使作业已结束，也能从作业找到对应的 Run、操作与日志目录。
type SystemJob struct {
	JobID          string
	ProjectID      string
	Kind           string
	Status         string
	TaskID         string
	InstanceID     string
	TriggerEventID int64
	Reason         string
	Decision       map[string]any
	Result         map[string]any
	RunID          string
	OperationID    string
	JournalDir     string
	Attempts       int
	MaxAttempts    int
	LockedBy       string
	LockedAt       string
	CreatedAt      string
	UpdatedAt      string
	FinishedAt     string
}

// systemJobRunLinkDDL 是 v12：把系统作业与它触发的 Run／操作／日志目录对上。
//
// 只加列不重建表：SQLite 的 ALTER TABLE ADD COLUMN 不触碰既有 CHECK 约束与索引。
// 迁移条目注册在本文件里，是因为这张表的写盘边界按文件划分（见 db/migrations.go 台账）。
const systemJobRunLinkDDL = `
ALTER TABLE system_jobs ADD COLUMN run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE system_jobs ADD COLUMN operation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE system_jobs ADD COLUMN journal_dir TEXT NOT NULL DEFAULT '';
`

func init() {
	migrations = append(migrations, Migration{Version: 12, Name: "system_job_run_link", SQL: systemJobRunLinkDDL})
	// v13 必须排在 v12 之后追加：v13 重建表时要回灌 v12 新增的三列。
	// MigrateDB 按注册顺序执行，因此这里不走 migrations.go 的台账字面量。
	migrations = append(migrations, Migration{Version: 13, Name: "system_agent_run_reconcile", SQL: systemAgentRunReconcileDDL})
}

// ProjectionSystemAgentEvents 是系统 Agent 事件游标在 projection_state 中的名字。
const ProjectionSystemAgentEvents = "system_agent.events"

const (
	SystemJobQueued         = "queued"
	SystemJobRunning        = "running"
	SystemJobDone           = "done"
	SystemJobFailed         = "failed"
	SystemJobEscalated      = "escalated"
	SystemJobCancelled      = "cancelled"
	SystemKindUnblock       = "unblock_assist"
	SystemKindTaskSplit     = "task_split"
	SystemKindOpsRun        = "ops_run"
	SystemKindDispatchReady = "dispatch_ready"
	// SystemKindRunReconcile 核实受管 Run 的现场并按结论释放/保留资源（v13 放行 CHECK）。
	SystemKindRunReconcile = "run_reconcile"
)

const (
	// DefaultSystemJobLease 是 running 作业的默认租约：超时未写终态视为工作进程已失联。
	DefaultSystemJobLease = 10 * time.Minute
	// 失败后退避：30s * 2^(attempts-1)，封顶 10 分钟（见 SH-201）。
	systemJobBackoffBase = 30 * time.Second
	systemJobBackoffCap  = 10 * time.Minute
	// 一次领取最多扫描多少条候选；退避中的旧作业会被跳过，不阻塞后面的新作业。
	systemJobClaimScan = 100
)

// systemJobBackoff 返回第 attempts 次失败后要等的时长。
func systemJobBackoff(attempts int) time.Duration {
	if attempts <= 0 {
		return 0
	}
	d := systemJobBackoffBase
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= systemJobBackoffCap {
			return systemJobBackoffCap
		}
	}
	return d
}

// systemJobDue 判断一条 queued 作业是否已过退避窗口。
//
// 退避基准是 updated_at：RequeueSystemJob / ReapStaleSystemJobs 都会把它写成
// 「这次失败发生的时刻」。时间戳解析失败时按可领取处理——宁可早跑一次，
// 也不要把一条作业永久卡死。
func systemJobDue(attempts int, updatedAt string, now time.Time) bool {
	if attempts <= 0 {
		return true
	}
	ts, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return true
	}
	return !now.Before(ts.Add(systemJobBackoff(attempts)))
}

func normalizeNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

// InsertSystemJob 插入作业；与 UNIQUE(project,kind,task,trigger) 冲突时返回 (nil, nil) 表示幂等跳过。
func InsertSystemJob(tx *sql.Tx, j *SystemJob) (*SystemJob, error) {
	if j == nil {
		return nil, nil
	}
	now := UTCNow()
	if j.CreatedAt == "" {
		j.CreatedAt = now
	}
	if j.UpdatedAt == "" {
		j.UpdatedAt = now
	}
	if j.Status == "" {
		j.Status = SystemJobQueued
	}
	if j.MaxAttempts < 1 {
		j.MaxAttempts = 3
	}
	if j.Decision == nil {
		j.Decision = map[string]any{}
	}
	if j.Result == nil {
		j.Result = map[string]any{}
	}
	_, err := tx.Exec(`
INSERT INTO system_jobs(
  job_id, project_id, kind, status, task_id, instance_id, trigger_event_id, reason,
  decision_json, result_json, run_id, operation_id, journal_dir,
  attempts, max_attempts, locked_by, locked_at,
  created_at, updated_at, finished_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.JobID, j.ProjectID, j.Kind, j.Status, j.TaskID, j.InstanceID, j.TriggerEventID, j.Reason,
		mustJSON(j.Decision), mustJSON(j.Result), j.RunID, j.OperationID, j.JournalDir,
		j.Attempts, j.MaxAttempts, j.LockedBy, nullStr(j.LockedAt),
		j.CreatedAt, j.UpdatedAt, nullStr(j.FinishedAt),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, nil
		}
		return nil, err
	}
	return j, nil
}

// GetSystemJob 按 id 取作业。
func GetSystemJob(tx *sql.Tx, jobID string) (*SystemJob, error) {
	row := tx.QueryRow(`
SELECT job_id, project_id, kind, status, task_id, instance_id, trigger_event_id, reason,
  decision_json, result_json, run_id, operation_id, journal_dir, attempts, max_attempts, locked_by, COALESCE(locked_at,''),
  created_at, updated_at, COALESCE(finished_at,'')
FROM system_jobs WHERE job_id = ?`, jobID)
	return scanSystemJob(row)
}

// ClaimNextSystemJob 原子领取一条 queued 作业（按创建时间）。
//
// 失败过的作业要等过退避窗口（30s * 2^(attempts-1)，封顶 10 分钟）才能再领；
// 退避基准是 updated_at，即上一次失败/回收的时刻。now 由调用方注入，
// 让编排器的 Clock 决定「现在」，测试不必真等 30 秒。
func ClaimNextSystemJob(tx *sql.Tx, worker string, kinds []string, now time.Time) (*SystemJob, error) {
	worker = strings.TrimSpace(worker)
	if worker == "" {
		worker = "system"
	}
	now = normalizeNow(now)
	q := `
SELECT job_id, attempts, updated_at FROM system_jobs
WHERE status = ?`
	args := []any{SystemJobQueued}
	if len(kinds) > 0 {
		q += ` AND kind IN (` + placeholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	q += ` ORDER BY created_at ASC LIMIT ?`
	args = append(args, systemJobClaimScan)

	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var candidates []string
	for rows.Next() {
		var (
			id        string
			attempts  int
			updatedAt string
		)
		if err := rows.Scan(&id, &attempts, &updatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !systemJobDue(attempts, updatedAt, now) {
			continue
		}
		candidates = append(candidates, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	for _, id := range candidates {
		res, err := tx.Exec(`
UPDATE system_jobs
SET status = ?, locked_by = ?, locked_at = ?, attempts = attempts + 1, updated_at = ?
WHERE job_id = ? AND status = ?`,
			SystemJobRunning, worker, formatTS(now), formatTS(now), id, SystemJobQueued)
		if err != nil {
			return nil, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue
		}
		return GetSystemJob(tx, id)
	}
	return nil, nil
}

// ReapStaleSystemJobs 回收租约超时的 running 作业。
//
// 进程崩溃后作业会永远停在 running。超租约（lease<=0 时用 10 分钟）的作业：
//   - attempts 还没到 max_attempts → 回 queued，等退避窗口后重跑；
//   - attempts 已达 max_attempts → 记 failed 终态，不再重跑。
//
// 两种情况都会在 result 里留下 reaped_from=running 与回收时刻，供看板与
// 统计区分「正常结束」和「失联回收」。
func ReapStaleSystemJobs(tx *sql.Tx, now time.Time, lease time.Duration) (int, error) {
	if lease <= 0 {
		lease = DefaultSystemJobLease
	}
	now = normalizeNow(now)
	cutoff := formatTS(now.Add(-lease))

	rows, err := tx.Query(`
SELECT job_id, attempts, max_attempts, result_json FROM system_jobs
WHERE status = ? AND locked_at IS NOT NULL AND locked_at <> '' AND locked_at < ?`,
		SystemJobRunning, cutoff)
	if err != nil {
		return 0, err
	}
	type staleJob struct {
		id       string
		attempts int
		max      int
		result   map[string]any
	}
	var stale []staleJob
	for rows.Next() {
		var (
			id       string
			attempts int
			max      int
			raw      string
		)
		if err := rows.Scan(&id, &attempts, &max, &raw); err != nil {
			_ = rows.Close()
			return 0, err
		}
		result := map[string]any{}
		_ = json.Unmarshal([]byte(raw), &result)
		stale = append(stale, staleJob{id: id, attempts: attempts, max: max, result: result})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	reaped := 0
	for _, s := range stale {
		result := s.result
		if result == nil {
			result = map[string]any{}
		}
		result["reaped_from"] = SystemJobRunning
		result["reaped_at"] = formatTS(now)

		status := SystemJobQueued
		if s.attempts >= s.max {
			status = SystemJobFailed
		}
		finished := any(nil)
		if status == SystemJobFailed {
			finished = formatTS(now)
		}
		res, err := tx.Exec(`
UPDATE system_jobs
SET status = ?, result_json = ?, updated_at = ?, finished_at = ?,
    locked_by = '', locked_at = NULL
WHERE job_id = ? AND status = ?`,
			status, mustJSON(result), formatTS(now), finished, s.id, SystemJobRunning)
		if err != nil {
			return reaped, err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			reaped++
		}
	}
	return reaped, nil
}

// FinishSystemJob 写终态。
func FinishSystemJob(tx *sql.Tx, jobID, status string, decision, result map[string]any) error {
	if decision == nil {
		decision = map[string]any{}
	}
	if result == nil {
		result = map[string]any{}
	}
	now := UTCNow()
	_, err := tx.Exec(`
UPDATE system_jobs
SET status = ?, decision_json = ?, result_json = ?, updated_at = ?, finished_at = ?,
    locked_by = '', locked_at = NULL
WHERE job_id = ?`,
		status, mustJSON(decision), mustJSON(result), now, now, jobID)
	return err
}

// RequeueSystemJob 失败后重新排队（未超 max_attempts）。
func RequeueSystemJob(tx *sql.Tx, jobID string, result map[string]any) error {
	if result == nil {
		result = map[string]any{}
	}
	now := UTCNow()
	_, err := tx.Exec(`
UPDATE system_jobs
SET status = CASE WHEN attempts >= max_attempts THEN ? ELSE ? END,
    result_json = ?, updated_at = ?,
    finished_at = CASE WHEN attempts >= max_attempts THEN ? ELSE NULL END,
    locked_by = '', locked_at = NULL
WHERE job_id = ?`,
		SystemJobFailed, SystemJobQueued, mustJSON(result), now, now, jobID)
	return err
}

// ListSystemJobs 按状态列出；status 空则全部；limit<=0 默认 50。
func ListSystemJobs(tx *sql.Tx, projectID, status string, limit int) ([]*SystemJob, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `
SELECT job_id, project_id, kind, status, task_id, instance_id, trigger_event_id, reason,
  decision_json, result_json, run_id, operation_id, journal_dir, attempts, max_attempts, locked_by, COALESCE(locked_at,''),
  created_at, updated_at, COALESCE(finished_at,'')
FROM system_jobs WHERE 1=1`
	args := []any{}
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}
	if status != "" {
		q += ` AND status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SystemJob
	for rows.Next() {
		j, err := scanSystemJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// SetSystemJobRun 记录作业触发的 Run／操作／日志目录，供事后反查。
func SetSystemJobRun(tx *sql.Tx, jobID, runID, operationID, journalDir string) error {
	now := UTCNow()
	_, err := tx.Exec(`
UPDATE system_jobs
SET run_id = ?, operation_id = ?, journal_dir = ?, updated_at = ?
WHERE job_id = ?`,
		runID, operationID, journalDir, now, jobID)
	return err
}

// CountRecentSystemJobs 统计冷却窗口内同 task+kind 的作业数（含终态）。
func CountRecentSystemJobs(tx *sql.Tx, projectID, kind, taskID string, since time.Time) (int, error) {
	var n int
	err := tx.QueryRow(`
SELECT COUNT(*) FROM system_jobs
WHERE project_id = ? AND kind = ? AND task_id = ? AND created_at >= ?`,
		projectID, kind, taskID, since.UTC().Format(time.RFC3339)).Scan(&n)
	return n, err
}

func scanSystemJob(row interface{ Scan(dest ...any) error }) (*SystemJob, error) {
	var j SystemJob
	var decision, result string
	err := row.Scan(
		&j.JobID, &j.ProjectID, &j.Kind, &j.Status, &j.TaskID, &j.InstanceID, &j.TriggerEventID, &j.Reason,
		&decision, &result, &j.RunID, &j.OperationID, &j.JournalDir, &j.Attempts, &j.MaxAttempts, &j.LockedBy, &j.LockedAt,
		&j.CreatedAt, &j.UpdatedAt, &j.FinishedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.Decision = map[string]any{}
	j.Result = map[string]any{}
	_ = json.Unmarshal([]byte(decision), &j.Decision)
	_ = json.Unmarshal([]byte(result), &j.Result)
	return &j, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint")
}
