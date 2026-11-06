package app

import (
	"context"
	"database/sql"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/recovery"
	"github.com/kiwh77/maw/internal/systemagent"
)

// seedRun 直接写一条 Run 行：本测试只关心「端口怎么落库」，
// 领取与启动链路由 runtime 自己的测试覆盖。
func seedRun(t *testing.T, store *db.Store, projectID, runID, instanceID, phase, reconciliation, startedAt string, deliveryFrozen bool) {
	t.Helper()
	now := db.UTCNow()
	frozen := 0
	if deliveryFrozen {
		frozen = 1
	}
	err := store.Write(func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO runs(run_id, project_id, instance_id, version_no, generation,
  phase, reconciliation_state, desired_action, process_stop_confirmed, delivery_frozen,
  started_at, finished_at, phase_history_json, evidence_json, leases_json, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			runID, projectID, instanceID, 1, 1, phase, reconciliation, "none", 0, frozen,
			startedAt, "", "[]", "[]", "[]", now, now)
		return e
	})
	if err != nil {
		t.Fatalf("seed run %s: %v", runID, err)
	}
}

// seedLease 直接写一条未释放的占用；代际从 1 起。
func seedLease(t *testing.T, store *db.Store, projectID, leaseID, kind, key, instanceID, runID string) {
	t.Helper()
	err := store.Write(func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO resource_leases(lease_id, kind, resource_key, project_id,
  instance_id, run_id, generation, holder, acquired_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
			leaseID, kind, key, projectID, instanceID, runID, 1, "test", db.UTCNow())
		return e
	})
	if err != nil {
		t.Fatalf("seed lease %s: %v", leaseID, err)
	}
}

func seedGhostTask(t *testing.T, store *db.Store, projectID, taskID, instanceID string) {
	t.Helper()
	err := store.Write(func(tx *sql.Tx) error {
		// 刻意不建 task_instances：让回写任务时 bindTaskIdentity 报错，
		// 用来验证「释放失败 → escalate 且整体回滚」。
		_, e := tx.Exec(`INSERT INTO tasks(project_id, id, title, status, owner, instance_id,
  priority, merge_order, revision)
VALUES (?,?,?,?,?,?,?,?,?)`,
			projectID, taskID, "ghost "+taskID, "in_progress", "agent-x", instanceID, 10, 10, 0)
		return e
	})
	if err != nil {
		t.Fatalf("seed ghost task %s: %v", taskID, err)
	}
}

func countRunEvents(t *testing.T, store *db.Store, runID, kind string) int {
	t.Helper()
	var n int
	err := store.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM run_events WHERE run_id = ? AND kind = ?`, runID, kind).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count run events: %v", err)
	}
	return n
}

func newTestReconcilePort(t *testing.T, f *fixture) (*leadReconcilePort, *db.Store) {
	t.Helper()
	store, err := db.OpenStore(f.pc.GlobalDB)
	if err != nil {
		t.Fatal(err)
	}
	port := newLeadReconcilePort(store, &Runtime{stateDir: t.TempDir()})
	port.operationOf = func(*sql.Tx, string, string) (string, error) { return "", nil }
	t.Cleanup(func() { _ = store.Close() })
	return port, store
}

// 验收 ①：not_started 可复用 → release：Run 落终态、租约释放、任务回 ready。
func TestLeadReconcileRelease(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("T-REC", "in_progress", "agent-x", []string{"internal/**"})
	port, store := newTestReconcilePort(t, f)

	seedRun(t, store, f.pc.ID(), "run_rec", task.InstanceID, string(model.PhaseReserved),
		string(model.ReconciliationUnknown), "", true)
	seedLease(t, store, f.pc.ID(), "ls_slot", string(model.LeaseNodeSlot), "node:test#1", task.InstanceID, "run_rec")
	seedLease(t, store, f.pc.ID(), "ls_ws", string(model.LeaseWorkspace), "ws:/tmp/ws-rec", task.InstanceID, "run_rec")

	out, err := port.Reconcile(context.Background(), systemagent.ReconcileRequest{
		ProjectID: f.pc.ID(), JobID: "job_rec", RunID: "run_rec",
		InstanceID: task.InstanceID, TaskID: task.ID,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Verdict != systemagent.ReconcileRelease {
		t.Fatalf("verdict = %s, want release", out.Verdict)
	}
	if out.Evidence.State != recovery.StateNotStarted {
		t.Fatalf("state = %s, want not_started", out.Evidence.State)
	}
	if len(out.ResourcesReleased) != 2 {
		t.Fatalf("resources_released = %v, want node_slot+workspace", out.ResourcesReleased)
	}

	err = store.Read(func(tx *sql.Tx) error {
		run, e := db.GetRun(tx, "run_rec")
		if e != nil {
			return e
		}
		if run.Phase != model.PhaseFailed {
			t.Fatalf("phase = %s, want failed（never_started）", run.Phase)
		}
		if run.Reconciliation != model.ReconciliationConfirmed {
			t.Fatalf("reconciliation = %s, want confirmed", run.Reconciliation)
		}
		if !run.ProcessStopConfirmed {
			t.Fatalf("process_stop_confirmed 未确认，节点槽不应被释放")
		}
		for _, id := range []string{"ls_slot", "ls_ws"} {
			l, e := db.GetLease(tx, id)
			if e != nil {
				return e
			}
			if l == nil || !l.Released() {
				t.Fatalf("lease %s 未释放", id)
			}
		}
		got, e := db.GetTask(tx, f.pc.ID(), task.ID)
		if e != nil {
			return e
		}
		if got.Status != "ready" {
			t.Fatalf("task status = %s, want ready", got.Status)
		}
		if got.Owner != "" {
			t.Fatalf("task owner = %q, want empty", got.Owner)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n := countRunEvents(t, store, "run_rec", "run.reconciled"); n != 1 {
		t.Fatalf("run.reconciled events = %d, want 1", n)
	}
}

// 验收 ②：unknown → keep：不退租约、Task 状态不变、写 run.reconcile_unknown。
func TestLeadReconcileKeep(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("T-KEEP", "in_progress", "agent-x", []string{"internal/**"})
	port, store := newTestReconcilePort(t, f)

	seedRun(t, store, f.pc.ID(), "run_keep", task.InstanceID, string(model.PhaseRunning),
		string(model.ReconciliationUnknown), "2026-11-06T00:00:00Z", false)
	seedLease(t, store, f.pc.ID(), "ls_slot", string(model.LeaseNodeSlot), "node:test#2", task.InstanceID, "run_keep")
	seedLease(t, store, f.pc.ID(), "ls_ws", string(model.LeaseWorkspace), "ws:/tmp/ws-keep", task.InstanceID, "run_keep")

	// 把 updated_at 回拨到过去，确认 keep 确实刷新了「最近一次核实」。
	const before = "2026-01-01T00:00:00Z"
	if err := store.Write(func(tx *sql.Tx) error {
		_, e := tx.Exec(`UPDATE runs SET updated_at = ? WHERE run_id = ?`, before, "run_keep")
		return e
	}); err != nil {
		t.Fatal(err)
	}

	out, err := port.Reconcile(context.Background(), systemagent.ReconcileRequest{
		ProjectID: f.pc.ID(), JobID: "job_keep", RunID: "run_keep",
		InstanceID: task.InstanceID, TaskID: task.ID,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Verdict != systemagent.ReconcileKeep {
		t.Fatalf("verdict = %s, want keep", out.Verdict)
	}
	if out.Evidence.State != recovery.StateUnknown {
		t.Fatalf("state = %s, want unknown", out.Evidence.State)
	}

	err = store.Read(func(tx *sql.Tx) error {
		run, e := db.GetRun(tx, "run_keep")
		if e != nil {
			return e
		}
		if run.Phase != model.PhaseRunning {
			t.Fatalf("phase = %s, want running（keep 不得落终态）", run.Phase)
		}
		if run.UpdatedAt == before {
			t.Fatalf("keep 未刷新 updated_at：%s", run.UpdatedAt)
		}
		for _, id := range []string{"ls_slot", "ls_ws"} {
			l, e := db.GetLease(tx, id)
			if e != nil {
				return e
			}
			if l.Released() {
				t.Fatalf("lease %s 被 keep 释放了", id)
			}
		}
		got, e := db.GetTask(tx, f.pc.ID(), task.ID)
		if e != nil {
			return e
		}
		if got.Status != "in_progress" {
			t.Fatalf("task status = %s, want in_progress", got.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n := countRunEvents(t, store, "run_keep", "run.reconcile_unknown"); n != 1 {
		t.Fatalf("run.reconcile_unknown events = %d, want 1", n)
	}
}

// 验收 ③：release 失败 → escalate：不碰资源、不改 Task、写 run.reconcile_escalate。
func TestLeadReconcileEscalate(t *testing.T) {
	f := newFixture(t)
	port, store := newTestReconcilePort(t, f)

	const ghost = "ti_ghost"
	seedGhostTask(t, store, f.pc.ID(), "T-GHOST", ghost)
	seedRun(t, store, f.pc.ID(), "run_esc", ghost, string(model.PhaseReserved),
		string(model.ReconciliationUnknown), "", false)
	seedLease(t, store, f.pc.ID(), "ls_slot", string(model.LeaseNodeSlot), "node:test#3", ghost, "run_esc")

	out, err := port.Reconcile(context.Background(), systemagent.ReconcileRequest{
		ProjectID: f.pc.ID(), JobID: "job_esc", RunID: "run_esc",
		InstanceID: ghost, TaskID: "T-GHOST",
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if out.Verdict != systemagent.ReconcileEscalate {
		t.Fatalf("verdict = %s, want escalate", out.Verdict)
	}

	err = store.Read(func(tx *sql.Tx) error {
		run, e := db.GetRun(tx, "run_esc")
		if e != nil {
			return e
		}
		if run.Phase != model.PhaseReserved {
			t.Fatalf("phase = %s, want reserved（escalate 不得改状态）", run.Phase)
		}
		if run.Reconciliation != model.ReconciliationUnknown {
			t.Fatalf("reconciliation = %s, want unknown", run.Reconciliation)
		}
		l, e := db.GetLease(tx, "ls_slot")
		if e != nil {
			return e
		}
		if l.Released() {
			t.Fatalf("escalate 释放了 node_slot")
		}
		got, e := db.GetTask(tx, f.pc.ID(), "T-GHOST")
		if e != nil {
			return e
		}
		if got.Status != "in_progress" {
			t.Fatalf("task status = %s, want in_progress", got.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n := countRunEvents(t, store, "run_esc", "run.reconcile_escalate"); n != 1 {
		t.Fatalf("run.reconcile_escalate events = %d, want 1", n)
	}
}

// 验收 ⑤：run.unverified 证据由 runs.reconciliation_state / updated_at 推出。
func TestHubSignalFactsUnverifiedRuns(t *testing.T) {
	f := newFixture(t)
	store, err := db.OpenStore(f.pc.GlobalDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	seedRun(t, store, f.pc.ID(), "run_unknown", "ti_missing_task", string(model.PhaseRunning),
		string(model.ReconciliationUnknown), "2026-11-06T00:00:00Z", false)
	seedRun(t, store, f.pc.ID(), "run_done", "ti_done", string(model.PhaseCompleted),
		string(model.ReconciliationConfirmed), "2026-11-06T00:00:00Z", true)

	raw := newHubSignalFacts(f.pc.GlobalDB, f.pc.Config.SystemAgent)
	facts, ok := raw.(*hubSignalFacts)
	if !ok {
		t.Fatalf("newHubSignalFacts 返回 %T，want *hubSignalFacts", raw)
	}
	runs := facts.UnverifiedRuns()
	if len(runs) != 1 {
		t.Fatalf("unverified runs = %d, want 1（已确认终态不应再产出）", len(runs))
	}
	if runs[0].RunID != "run_unknown" {
		t.Fatalf("run_id = %s, want run_unknown", runs[0].RunID)
	}
	if runs[0].State != string(model.ReconciliationUnknown) {
		t.Fatalf("state = %s, want unknown", runs[0].State)
	}
	if runs[0].CheckedAt.IsZero() {
		t.Fatalf("checked_at 未从 runs.updated_at 解析出来")
	}
}
