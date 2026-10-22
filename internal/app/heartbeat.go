package app

import (
	"database/sql"
	"time"

	"github.com/wuhao/maw/internal/db"
	"github.com/wuhao/maw/internal/machine"
)

// expireStaleAgents marks agents offline when LastSeen exceeds heartbeat_ttl_sec,
// and releases their ActiveOwner tasks back to ready (or blocked if transition illegal).
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
