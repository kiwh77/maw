package db

import (
	"database/sql"
	"strings"
	"testing"
)

// applyUpTo 按注册顺序补到 target 版本，用来构造「升级到 v13 之前」的库。
func applyUpTo(t *testing.T, conn *sql.DB, target int) {
	t.Helper()
	for _, m := range Migrations() {
		if m.Version > target {
			// 注册顺序即执行顺序；一旦越过目标版本就不能再往后应用。
			continue
		}
		tx, err := conn.Begin()
		if err != nil {
			t.Fatalf("开启迁移事务失败: %v", err)
		}
		if _, err := tx.Exec(m.SQL); err != nil {
			_ = tx.Rollback()
			t.Fatalf("应用迁移 %d（%s）失败: %v", m.Version, m.Name, err)
		}
		if m.Apply != nil {
			if err := m.Apply(tx); err != nil {
				_ = tx.Rollback()
				t.Fatalf("迁移 %d 数据步骤失败: %v", m.Version, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("提交迁移 %d 失败: %v", m.Version, err)
		}
	}
}

// TestMigrationRunReconcileKind 覆盖验收：v13 迁移后可插入 kind=run_reconcile，
// 且 v12 之前已有的作业（含 run_id/operation_id/journal_dir 反查链）完整保留。
func TestMigrationRunReconcileKind(t *testing.T) {
	path := freshPath(t)
	conn, err := openConn(path, writeConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	applyUpTo(t, conn, 12)

	legacy := []struct {
		jobID, kind, runID, opID, journal, createdAt string
	}{
		{"sjob_legacy_unblock", SystemKindUnblock, "run_legacy_1", "op_legacy_1", "/tmp/journal-1", "2026-11-01T00:00:00Z"},
		{"sjob_legacy_dispatch", SystemKindDispatchReady, "", "", "", "2026-11-02T00:00:00Z"},
	}
	for _, l := range legacy {
		if _, err := conn.Exec(`
INSERT INTO system_jobs(
  job_id, project_id, kind, status, task_id, instance_id, trigger_event_id, reason,
  decision_json, result_json, run_id, operation_id, journal_dir,
  attempts, max_attempts, locked_by, locked_at, created_at, updated_at, finished_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			l.jobID, "0123456789abcdef", l.kind, SystemJobDone, "T-1", "ti_1", 0, "legacy",
			`{"signal_key":"k1"}`, `{"mode":"legacy"}`, l.runID, l.opID, l.journal,
			1, 3, "", nil, l.createdAt, l.createdAt, l.createdAt,
		); err != nil {
			t.Fatalf("插入旧数据 %s 失败: %v", l.jobID, err)
		}
	}

	var v13 *Migration
	for _, m := range Migrations() {
		if m.Version == 13 {
			m := m
			v13 = &m
		}
	}
	if v13 == nil {
		t.Fatal("缺少 v13 迁移（run_reconcile）")
	}
	if _, err := conn.Exec(v13.SQL); err != nil {
		t.Fatalf("应用 v13 失败: %v", err)
	}

	// 旧行完整保留：字段值、反查链、JSON 都还在。
	for _, l := range legacy {
		var (
			kind, status, runID, opID, journal, decision, result, createdAt string
			attempts                                                        int
		)
		if err := conn.QueryRow(`
SELECT kind, status, run_id, operation_id, journal_dir, decision_json, result_json, attempts, created_at
FROM system_jobs WHERE job_id = ?`, l.jobID).Scan(
			&kind, &status, &runID, &opID, &journal, &decision, &result, &attempts, &createdAt,
		); err != nil {
			t.Fatalf("读取旧行 %s 失败: %v", l.jobID, err)
		}
		if kind != l.kind || status != SystemJobDone || runID != l.runID ||
			opID != l.opID || journal != l.journal || attempts != 1 || createdAt != l.createdAt {
			t.Fatalf("旧行 %s 迁移后变了: kind=%s status=%s run=%q op=%q journal=%q attempts=%d created=%s",
				l.jobID, kind, status, runID, opID, journal, attempts, createdAt)
		}
		if !strings.Contains(decision, "signal_key") || !strings.Contains(result, "legacy") {
			t.Fatalf("旧行 %s 的 JSON 内容丢失: decision=%s result=%s", l.jobID, decision, result)
		}
	}

	// 新 kind 可插入。
	if _, err := conn.Exec(`
INSERT INTO system_jobs(job_id, project_id, kind, status, task_id, created_at, updated_at)
VALUES ('sjob_reconcile', '0123456789abcdef', ?, ?, 'T-2', '2026-11-05T00:00:00Z', '2026-11-05T00:00:00Z')`,
		SystemKindRunReconcile, SystemJobQueued,
	); err != nil {
		t.Fatalf("迁移后应放行 run_reconcile: %v", err)
	}

	// 未知 kind 仍被 CHECK 拒绝。
	if _, err := conn.Exec(`
INSERT INTO system_jobs(job_id, project_id, kind, status, task_id, created_at, updated_at)
VALUES ('sjob_bad', '0123456789abcdef', 'not_a_kind', 'queued', 'T-3', '2026-11-05T00:00:00Z', '2026-11-05T00:00:00Z')`,
	); err == nil {
		t.Fatal("未知 kind 应被 CHECK 拒绝")
	}

	// 重建表后的三个索引都在。
	rows, err := conn.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='system_jobs'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got[n] = true
	}
	for _, want := range []string{"system_jobs_status", "system_jobs_project", "system_jobs_dedupe"} {
		if !got[want] {
			t.Fatalf("重建后缺少索引 %s: %v", want, got)
		}
	}
}
