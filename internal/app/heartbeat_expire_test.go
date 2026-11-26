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

	if _, _, _, err := claimManual(f.pc, "cursor-manual", ".worktrees/cursor-manual", NextOptions{}); err != nil {
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

	if _, _, _, err := claimManual(f.pc, "cursor-manual", ".worktrees/cursor-manual", NextOptions{}); err != nil {
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

// TestExpireWriteFailureReturnsErrorAndRollsBack 覆盖 SH-202 第三条验收：
// 写事件／写任务行失败必须向上返回，且整笔事务回滚，库里不能留下
// 「Agent 已 offline、任务却还挂着 owner」这种半状态。
func TestExpireWriteFailureReturnsErrorAndRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger string
	}{
		{
			name: "AppendEvent 失败",
			trigger: `CREATE TEMP TRIGGER fail_event_write BEFORE INSERT ON events
BEGIN SELECT RAISE(ABORT, 'injected: event write failed'); END`,
		},
		{
			name: "UpsertTask 失败",
			trigger: `CREATE TEMP TRIGGER fail_task_write BEFORE UPDATE ON tasks
BEGIN SELECT RAISE(ABORT, 'injected: task write failed'); END`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			task := f.seedTask("VN-HF", "ready", "", []string{"mod_hf/**"})
			if _, _, _, err := claimManual(f.pc, "cursor-stale", ".worktrees/cursor-stale", NextOptions{}); err != nil {
				t.Fatalf("manual claim: %v", err)
			}
			staleAgent(t, f, "cursor-stale", task.ID)

			err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
				if _, e := tx.Exec(tc.trigger); e != nil {
					t.Fatalf("install trigger: %v", e)
				}
				return expireStaleAgents(tx, f.pc)
			})
			if err == nil {
				t.Fatal("写库失败必须向上返回错误，不能静默吞掉")
			}

			// 事务已回滚：Agent 仍是 working，任务仍归它，且没有过期事件。
			var status, owner, taskStatus string
			if e := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
				if e := tx.QueryRow(`SELECT status FROM agents WHERE id = ?`, "cursor-stale").Scan(&status); e != nil {
					return e
				}
				return tx.QueryRow(`SELECT status, owner FROM tasks WHERE id = ?`, task.ID).Scan(&taskStatus, &owner)
			}); e != nil {
				t.Fatalf("读回状态: %v", e)
			}
			if status != "working" {
				t.Fatalf("回滚后 Agent 不应已经 offline，实际 %s", status)
			}
			if taskStatus != string(model.TaskClaimed) || owner != "cursor-stale" {
				t.Fatalf("回滚后任务应仍为 claimed 且保留 owner，实际 status=%s owner=%s", taskStatus, owner)
			}
			if n := countEventsByType(t, f, "agent_expired"); n != 0 {
				t.Fatalf("回滚后不应留下 agent_expired 事件，实际 %d", n)
			}
		})
	}
}

// TestExpireKeepsMergingTask 覆盖：submit 后等合入期间 Agent 心跳过期，
// 只下线 Agent，任务仍保持 merging，不清 Owner/branch（避免随后被 prune 删码）。
func TestExpireKeepsMergingTask(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask("VN-HM", "merging", "cursor-merge-wait", []string{"mod_hm/**"})
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		got, err := db.GetTask(tx, f.pc.ID(), task.ID)
		if err != nil || got == nil {
			return err
		}
		got.Branch = "agent/cursor-merge-wait/" + task.ID
		got.Worktree = ".worktrees/cursor-merge-wait"
		got.Owner = "cursor-merge-wait"
		return db.UpsertTask(tx, got)
	}); err != nil {
		t.Fatalf("seed merging fields: %v", err)
	}

	staleAgent(t, f, "cursor-merge-wait", task.ID)
	if err := expire(t, f); err != nil {
		t.Fatalf("expire: %v", err)
	}

	got := f.task(task.ID)
	if got.Status != "merging" {
		t.Fatalf("merging 不得因心跳过期改状态，得到 %s（%s）", got.Status, got.BlockedReason)
	}
	if got.Owner != "cursor-merge-wait" {
		t.Fatalf("merging 应保留 Owner，得到 %q", got.Owner)
	}
	if got.Branch == "" || got.Worktree == "" {
		t.Fatalf("merging 应保留 branch/worktree，branch=%q worktree=%q", got.Branch, got.Worktree)
	}
	var agentStatus string
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT status FROM agents WHERE id = ?`, "cursor-merge-wait").Scan(&agentStatus)
	}); err != nil {
		t.Fatalf("agent status: %v", err)
	}
	if agentStatus != "offline" {
		t.Fatalf("Agent 应下线，实际 %s", agentStatus)
	}
	if n := countEvents(t, f, task.ID, "agent_expired_waiting_merge"); n != 1 {
		t.Fatalf("应写 agent_expired_waiting_merge，实际 %d", n)
	}
	if n := countEvents(t, f, task.ID, "status_changed"); n != 0 {
		t.Fatalf("不应改任务状态，status_changed=%d", n)
	}
}

func expire(t *testing.T, f *fixture) error {
	t.Helper()
	return db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return expireStaleAgents(tx, f.pc)
	})
}

// staleAgent 把 Agent 心跳放到过期之外（受管领取不登记 Agent 行，这里显式补上）。
// 同时把题 heartbeat 打旧：领取时写入的新鲜 heartbeat 不能被当成「仍在 progress」。
func staleAgent(t *testing.T, f *fixture, agentID, taskID string) {
	t.Helper()
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		if err := db.UpsertAgent(tx, &db.Agent{
			ProjectID: f.pc.ID(), ID: agentID, Role: "worker", Status: "working",
			TaskID: taskID, LastSeen: "2020-01-01T00:00:00Z",
		}); err != nil {
			return err
		}
		task, err := db.GetTask(tx, f.pc.ID(), taskID)
		if err != nil || task == nil {
			return err
		}
		task.Heartbeat = "2020-01-01T00:00:00Z"
		return db.UpsertTask(tx, task)
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
