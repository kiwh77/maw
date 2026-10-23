package app

import (
	"database/sql"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/machine"
)

// expireStaleAgents marks agents offline when LastSeen exceeds heartbeat_ttl_sec,
// and releases their ActiveOwner tasks back to ready (or blocked if transition illegal).
//
// 一条硬边界：**心跳过期不是「进程已停」的证据**。受管 Run 只要仍计入占用
// （runtime-state-contract-v1 §4.1：未终态、核实未知、终态但停止未确认），
// 对应的任务就不能被打回可重派——否则另一个 Agent 会拿到同一份工作目录，
// 和可能还在写盘的旧进程撞在一起。这类任务只下线 Agent，保留占用等人处置。
func expireStaleAgents(tx *sql.Tx, pc *projCtx) error {
	if pc == nil || pc.Config == nil {
		return nil
	}
	ttl := pc.Config.Locks.HeartbeatTTLSec
	if ttl <= 0 {
		ttl = 900
	}
	cutoff := time.Now().UTC().Add(-time.Duration(ttl) * time.Second)
	agents, err := db.ListAgents(tx, pc.ID())
	if err != nil {
		return err
	}
	for _, a := range agents {
		if a == nil || a.Status == "offline" || a.Status == "idle" {
			continue
		}
		if a.LastSeen == "" {
			continue
		}
		seen, err := time.Parse(time.RFC3339, a.LastSeen)
		if err != nil {
			continue
		}
		if seen.After(cutoff) {
			continue
		}
		lastSeen := a.LastSeen
		agentID := a.ID
		a.Status = "offline"
		a.TaskID = ""
		a.Worktree = ""
		a.Branch = ""
		a.LastSeen = db.UTCNow()
		if err := db.UpsertAgent(tx, a); err != nil {
			return err
		}
		_ = db.AppendEvent(tx, pc.ID(), "system", "agent_expired", "", map[string]any{
			"agent": agentID, "ttl_sec": ttl, "last_seen": lastSeen,
		})
		tasks, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t == nil || t.Owner != agentID || !machine.ActiveOwner[t.Status] {
				continue
			}
			if run := occupyingRun(tx, pc.ID(), t.InstanceID); run != nil {
				_ = db.AppendEvent(tx, pc.ID(), "system", "agent_expired_occupancy_kept", t.ID, map[string]any{
					"agent": agentID, "run_id": run.ID, "generation": int(run.Generation),
					"phase": string(run.Phase), "reconciliation": string(run.Reconciliation),
					"process_stop_confirmed": run.ProcessStopConfirmed,
					"reason":                 "run_still_occupying",
				})
				continue
			}
			from := t.Status
			if err := machine.AssertTransition(t.Status, "ready"); err != nil {
				t.PrevStatus = t.Status
				t.Status = "blocked"
				t.BlockedReason = "agent_expired:" + agentID
				t.Owner = ""
				t.Revision++
				_ = db.UpsertTask(tx, t)
				_ = db.AppendEvent(tx, pc.ID(), "system", "status_changed", t.ID, map[string]any{
					"from": from, "to": "blocked", "reason": "agent_expired",
				})
				continue
			}
			t.Status = "ready"
			t.Owner = ""
			t.Branch = ""
			t.Worktree = ""
			t.BlockedReason = ""
			t.Revision++
			_ = db.UpsertTask(tx, t)
			_ = db.AppendEvent(tx, pc.ID(), "system", "status_changed", t.ID, map[string]any{
				"from": from, "to": "ready", "reason": "agent_expired",
			})
		}
	}
	return nil
}

// occupyingRun 返回该任务实例上仍计入占用的 Run；没有则返回 nil。
//
// 「仍计入占用」只认 model.Run.Occupies 一套判定，这里不另立条件：
// 手工领取不建 Run，所以它的任务照旧按心跳过期退回 ready；
// 受管 Run 只要还有占用（含核实未知），就必须保留，等人工核实后再处置。
func occupyingRun(tx *sql.Tx, projectID, instanceID string) *db.Run {
	if instanceID == "" {
		return nil
	}
	runs, err := db.ListRuns(tx, projectID)
	if err != nil {
		return nil
	}
	for _, r := range runs {
		if r == nil || r.InstanceID != instanceID || r.ID == "" {
			continue
		}
		if r.Occupies() {
			return r
		}
	}
	return nil
}
