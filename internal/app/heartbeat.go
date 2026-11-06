package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
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
	return expireStaleAgentsAt(tx, pc, time.Now().UTC())
}

// expireStaleAgentsAt 用给定时刻判定过期，供常驻巡检注入时钟测试。
//
// 所有写（Agent、事件、任务行、租约）都在调用方给的同一个事务里；任一写失败
// 立即返回错误，由事务整体回滚，绝不允许「Agent 已下线但任务还挂在它名下」
// 这种半状态落库。TTL 语义、占用判定与收尾链路都不因主动巡检而改变。
func expireStaleAgentsAt(tx *sql.Tx, pc *projCtx, now time.Time) error {
	if pc == nil || pc.Config == nil {
		return nil
	}
	ttl := pc.Config.Locks.HeartbeatTTLSec
	if ttl <= 0 {
		ttl = 900
	}
	cutoff := now.UTC().Add(-time.Duration(ttl) * time.Second)
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
		if err := db.AppendEvent(tx, pc.ID(), "system", "agent_expired", "", map[string]any{
			"agent": agentID, "ttl_sec": ttl, "last_seen": lastSeen,
		}); err != nil {
			return err
		}
		tasks, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t == nil || t.Owner != agentID || !machine.ActiveOwner[t.Status] {
				continue
			}
			run, err := occupyingRun(tx, pc.ID(), t.InstanceID)
			if err != nil {
				return err
			}
			if run != nil {
				// 人工领取（CLI 自领）也建 Run，但它从不启动 Runner：这种 Run 没有进程
				// 在写盘，心跳过期后必须收尾放行，否则题目永久停在 claimed（挂尸）。
				// 有进程证据的受管 Run 仍按上面的硬边界保留占用。
				if !runHasNoProcessEvidence(tx, run) {
					if err := db.AppendEvent(tx, pc.ID(), "system", "agent_expired_occupancy_kept", t.ID, map[string]any{
						"agent": agentID, "run_id": run.ID, "generation": int(run.Generation),
						"phase": string(run.Phase), "reconciliation": string(run.Reconciliation),
						"process_stop_confirmed": run.ProcessStopConfirmed,
						"reason":                 "run_still_occupying",
					}); err != nil {
						return err
					}
					continue
				}
				released, err := abandonRunWithoutProcess(tx, pc.ID(), run)
				if err != nil {
					return err
				}
				if !released {
					// 阶段不在可收尾链路上（例如已进入 running 却没有启动时间），
					// 不硬改状态，保留占用等人核实。
					if err := db.AppendEvent(tx, pc.ID(), "system", "agent_expired_occupancy_kept", t.ID, map[string]any{
						"agent": agentID, "run_id": run.ID, "generation": int(run.Generation),
						"phase": string(run.Phase), "reason": "run_phase_not_abandonable",
					}); err != nil {
						return err
					}
					continue
				}
				if err := db.AppendEvent(tx, pc.ID(), "system", "agent_expired_run_released", t.ID, map[string]any{
					"agent": agentID, "run_id": run.ID, "generation": int(run.Generation),
					"from_phase": string(run.Phase), "reason": "no_process_evidence",
				}); err != nil {
					return err
				}
			}
			from := t.Status
			if err := machine.AssertTransition(t.Status, "ready"); err != nil {
				t.PrevStatus = t.Status
				t.Status = "blocked"
				t.BlockedReason = "agent_expired:" + agentID
				t.Owner = ""
				t.Revision++
				if err := db.UpsertTask(tx, t); err != nil {
					return err
				}
				if err := db.AppendEvent(tx, pc.ID(), "system", "status_changed", t.ID, map[string]any{
					"from": from, "to": "blocked", "reason": "agent_expired",
				}); err != nil {
					return err
				}
				continue
			}
			t.Status = "ready"
			t.Owner = ""
			t.Branch = ""
			t.Worktree = ""
			t.BlockedReason = ""
			t.Revision++
			if err := db.UpsertTask(tx, t); err != nil {
				return err
			}
			if err := db.AppendEvent(tx, pc.ID(), "system", "status_changed", t.ID, map[string]any{
				"from": from, "to": "ready", "reason": "agent_expired",
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// occupyingRun 返回该任务实例上仍计入占用的 Run；没有则返回 nil。
//
// 「仍计入占用」只认 model.Run.Occupies 一套判定，这里不另立条件。
func occupyingRun(tx *sql.Tx, projectID, instanceID string) (*db.Run, error) {
	if instanceID == "" {
		return nil, nil
	}
	runs, err := db.ListRuns(tx, projectID)
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r == nil || r.InstanceID != instanceID || r.ID == "" {
			continue
		}
		if r.Occupies() {
			return r, nil
		}
	}
	return nil, nil
}

// runHasNoProcessEvidence 判断该 Run 是否「从未启动受管进程、也没有产物」。
//
// 三条判据缺一不可：核实状态不是 unknown（有人知道现场）、没有启动时间
// （Runner 从未拉起进程）、没有交付记录（目录里没有待验收的产物）。
// 人工领取的 Run 天然满足这三条：它只登记占用，不启动进程。
func runHasNoProcessEvidence(tx *sql.Tx, run *db.Run) bool {
	if run == nil {
		return false
	}
	if run.Reconciliation == model.ReconciliationUnknown {
		return false
	}
	if run.StartedAt != "" {
		return false
	}
	d, err := db.DeliveryForRun(tx, run.ID)
	if err != nil || d != nil {
		return false
	}
	return true
}

// abandonRunWithoutProcess 把「无进程证据」的 Run 收尾成 cancelled 并释放租约，
// 让任务能合法回到 ready 被重新领取。返回 false 表示阶段链不允许收尾，调用方保留占用。
//
// 阶段按契约 reserved → stopping → cancelled 走，每次变更与 run.phase 事件同批提交；
// 事件 id 由 run+阶段确定性生成，重复扫描不会重复写事件。
func abandonRunWithoutProcess(tx *sql.Tx, projectID string, run *db.Run) (bool, error) {
	evidence := []string{"strategy:" + manualCompatStrategy, "agent_expired:no_process_evidence"}
	for _, next := range []model.RunPhase{model.PhaseStopping, model.PhaseCancelled} {
		if run.Phase == next {
			continue
		}
		if !model.CanTransition(run.Phase, next) {
			return false, nil
		}
		if _, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
			ID:            expiredRunEventID(run.ID, string(next)),
			ProjectID:     projectID,
			RunID:         run.ID,
			InstanceID:    run.InstanceID,
			Kind:          db.EventRunPhase,
			Phase:         next,
			SchemaVersion: db.RunEventSchemaVersion,
			Payload: map[string]any{
				"strategy": manualCompatStrategy, "reason": "agent_expired_no_process",
			},
		}); err != nil {
			return false, err
		}
		updated, err := db.UpdateRunProgress(tx, run.ID, next, next == model.PhaseCancelled, false, evidence)
		if err != nil {
			return false, err
		}
		run = updated
	}
	if err := releaseInstanceLeases(tx, projectID, run.InstanceID); err != nil {
		return false, err
	}
	return true, nil
}

// releaseInstanceLeases 释放该实例上仍有效的占用。
//
// 目录占用（E242）要求产物已冻结；这里的 Run 已证明没有交付记录，产物不存在，
// 该前置条件的保护对象为空，因此按「无产物」放行，并在原因里写明依据。
func releaseInstanceLeases(tx *sql.Tx, projectID, instanceID string) error {
	leases, err := db.ListActiveLeases(tx, projectID)
	if err != nil {
		return err
	}
	for _, l := range leases {
		if l == nil || l.InstanceID != instanceID || l.Released() {
			continue
		}
		cond := db.ReleaseCondition{ProcessStopConfirmed: true, DeliveryFrozen: true}
		if err := db.ReleaseLease(tx, l.ID, l.Generation, "agent_expired:no_process_evidence", cond); err != nil {
			return err
		}
	}
	return nil
}

func expiredRunEventID(runID, phase string) model.EventID {
	sum := sha256.Sum256([]byte(runID + "|" + phase + "|agent-expired"))
	return model.EventID("evt_" + hex.EncodeToString(sum[:8]))
}
