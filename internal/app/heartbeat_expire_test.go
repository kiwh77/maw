package app

import (
	"database/sql"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// TestExpireReleasesManualRunWithoutProcess 覆盖 CQ-003 的主线：
// 人工领取会建 Run，但它从不启动 Runner，没有进程在写盘；Agent 心跳过期后
// 必须把这个 Run 收尾、释放占用，让任务回到 ready 被重新领取，而不是永久停 claimed。
func TestExpireReleasesManualRunWithoutProcess(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("VN-H1", "ready", "", []string{"mod_h1/**"})

	if _, _, err := claimManual(f.pc, "cursor-manual", ".worktrees/cursor-manual"); err != nil {
		t.Fatalf("manual claim: %v", err)
	}
	if got := f.task(task.ID); got.Status != string(model.TaskClaimed) {
		t.Fatalf("领取后应为 claimed，得到 %s", got.Status)
	}

	staleAgent(t, f, "cursor-manual", task.ID)
	if err := expire(t, f); err != nil {
		t.Fatalf("expire: %v", err)
	}

	got := f.task(task.ID)
	if got.Status != string(model.TaskReady) {
		t.Fatalf("无进程证据的人工领取应退回 ready，得到 %s（%s）", got.Status, got.BlockedReason)
	}
	if got.Owner != "" {
		t.Fatalf("退回 ready 后不应保留 owner，得到 %q", got.Owner)
	}

	run := latestRun(t, f, task.InstanceID)
	if run == nil {
		t.Fatal("人工领取应留下 Run 记录")
	}
	if run.Occupies() {
		t.Fatalf("Run 收尾后不得再计入占用：phase=%s stop_confirmed=%t", run.Phase, run.ProcessStopConfirmed)
	}
	if run.Phase != model.PhaseCancelled || !run.ProcessStopConfirmed {
		t.Fatalf("应以 cancelled + 进程已停收尾：phase=%s stop_confirmed=%t", run.Phase, run.ProcessStopConfirmed)
	}

	if n := countEvents(t, f, task.ID, "agent_expired_occupancy_kept"); n != 0 {
		t.Fatalf("无进程证据时不应留下 occupancy_kept，实际 %d 条", n)
	}
	if n := countEvents(t, f, task.ID, "agent_expired_run_released"); n != 1 {
		t.Fatalf("应留下一条回收事件，实际 %d 条", n)
	}
	if n := activeLeaseCount(t, f, task.InstanceID); n != 0 {
		t.Fatalf("回收后不应残留有效占用，实际 %d 条", n)
	}
}

// TestExpireKeepsManagedRunOccupancy 守住另一半：受管 Run 启动过进程后，
// 心跳过期不是「进程已停」的证据，任务必须保留占用，不得被打回可重派。
func TestExpireKeepsManagedRunOccupancy(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("VN-H2", "ready", "", []string{"mod_h2/**"})

	if err := f.managedClaim("cursor-managed", task.ID, t.TempDir()); err != nil {
		t.Fatalf("managed claim: %v", err)
	}
	// 模拟 Runner 已拉起进程：有启动时间，就不再是「无进程证据」。
	run := latestRun(t, f, task.InstanceID)
	if run == nil {
		t.Fatal("受管领取应建 Run")
	}
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE runs SET started_at = ? WHERE run_id = ?`, db.UTCNow(), run.ID)
		return err
	}); err != nil {
		t.Fatalf("mark started: %v", err)
	}

	staleAgent(t, f, "cursor-managed", task.ID)
	if err := expire(t, f); err != nil {
		t.Fatalf("expire: %v", err)
	}

	got := f.task(task.ID)
	if got.Status != string(model.TaskClaimed) || got.Owner != "cursor-managed" {
		t.Fatalf("受管占用不得被打回可重派：status=%s owner=%q", got.Status, got.Owner)
	}
	if n := countEvents(t, f, task.ID, "agent_expired_occupancy_kept"); n != 1 {
		t.Fatalf("应留下一条保留占用事件，实际 %d 条", n)
	}
	if n := countEvents(t, f, task.ID, "agent_expired_run_released"); n != 0 {
		t.Fatalf("受管占用不应被回收，实际回收事件 %d 条", n)
	}
	if again := latestRun(t, f, task.InstanceID); again == nil || !again.Occupies() {
		t.Fatal("受管 Run 仍应计入占用")
	}
}

// TestExpireReleasesManualRunAfterRetry 覆盖重复扫描：收尾是幂等的，
// 第二次过期扫描不应再动已收尾的 Run，也不应重复写事件。
func TestExpireReleasesManualRunAfterRetry(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("VN-H3", "ready", "", []string{"mod_h3/**"})

	if _, _, err := claimManual(f.pc, "cursor-manual", ".worktrees/cursor-manual"); err != nil {
		t.Fatalf("manual claim: %v", err)
	}
	staleAgent(t, f, "cursor-manual", task.ID)
	if err := expire(t, f); err != nil {
		t.Fatalf("first expire: %v", err)
	}
	if err := expire(t, f); err != nil {
		t.Fatalf("second expire: %v", err)
	}

	if n := countEvents(t, f, task.ID, "agent_expired_run_released"); n != 1 {
		t.Fatalf("重复扫描不应重复记回收事件，实际 %d 条", n)
	}
	if got := f.task(task.ID); got.Status != string(model.TaskReady) {
		t.Fatalf("重复扫描后任务仍应为 ready，得到 %s", got.Status)
	}
}

func expire(t *testing.T, f *fixture) error {
	t.Helper()
	return db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return expireStaleAgents(tx, f.pc)
	})
}

// staleAgent 把 Agent 心跳放到过期之外（受管领取不登记 Agent 行，这里显式补上）。
func staleAgent(t *testing.T, f *fixture, agentID, taskID string) {
	t.Helper()
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return db.UpsertAgent(tx, &db.Agent{
			ProjectID: f.pc.ID(), ID: agentID, Role: "worker", Status: "working",
			TaskID: taskID, LastSeen: "2020-01-01T00:00:00Z",
		})
	}); err != nil {
		t.Fatalf("stale agent: %v", err)
	}
}

func latestRun(t *testing.T, f *fixture, instanceID string) *db.Run {
	t.Helper()
	var out *db.Run
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		runs, err := db.ListRuns(tx, f.pc.ID())
		if err != nil {
			return err
		}
		for _, r := range runs {
			if r != nil && r.InstanceID == instanceID {
				out = r
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("list runs: %v", err)
	}
	return out
}

func countEvents(t *testing.T, f *fixture, taskID, kind string) int {
	t.Helper()
	var n int
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ? AND task_id = ? AND type = ?`,
			f.pc.ID(), taskID, kind).Scan(&n)
	}); err != nil {
		t.Fatalf("count %s: %v", kind, err)
	}
	return n
}

func activeLeaseCount(t *testing.T, f *fixture, instanceID string) int {
	t.Helper()
	var n int
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM resource_leases WHERE project_id = ? AND instance_id = ? AND released_at IS NULL`,
			f.pc.ID(), instanceID).Scan(&n)
	}); err != nil {
		t.Fatalf("count leases: %v", err)
	}
	return n
}
