package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/machine"
	"github.com/kiwh77/maw/internal/runner/recovery"
	"github.com/kiwh77/maw/internal/systemagent"
)

// leadReconcilePort 是 systemagent.ReconcilePort 的生产实现：核实受管 Run 的现场，
// 按 recovery.Evidence 的判定决定 release / keep，释放失败才升级人工。
//
// 判定口径只有 recovery.Evidence 一处；这里不按 State 另立分支，只负责把结论落库：
// 状态、租约、任务回写与事件必须同事务提交（契约 §5「同事务」）。
type leadReconcilePort struct {
	store *db.Store
	rt    *Runtime
	// probe 覆盖进程核对，测试注入；为空时 recovery 用真实进程探测。
	probe recovery.Prober
	// operationOf 覆盖 Run → 操作目录的查询，测试注入。
	operationOf func(tx *sql.Tx, projectID, runID string) (string, error)
}

func newLeadReconcilePort(store *db.Store, rt *Runtime) *leadReconcilePort {
	return &leadReconcilePort{store: store, rt: rt}
}

// Reconcile 核实一次 Run 现场并给出结论。
func (p *leadReconcilePort) Reconcile(ctx context.Context, req systemagent.ReconcileRequest) (systemagent.ReconcileOutcome, error) {
	if err := ctx.Err(); err != nil {
		return systemagent.ReconcileOutcome{}, err
	}

	var run *db.Run
	var opID string
	if err := p.store.Read(func(tx *sql.Tx) error {
		var err error
		run, err = db.GetRun(tx, req.RunID)
		if err != nil || run == nil {
			return err
		}
		opID, err = p.operationDir(tx, req.ProjectID, req.RunID)
		return err
	}); err != nil {
		return systemagent.ReconcileOutcome{}, err
	}
	if run == nil {
		// 没有 Run 就没有资源可动；不伪造 keep，升级人工。
		return p.escalate(req, "", "", recovery.Evidence{
			State:  recovery.StateUnknown,
			Reason: fmt.Sprintf("Run %q 不存在", req.RunID),
		}), nil
	}

	ev := p.evidence(run, opID)
	if !ev.Reusable() {
		return systemagent.ReconcileOutcome{Verdict: systemagent.ReconcileKeep, Evidence: ev}, p.keep(req, run, ev)
	}
	return p.release(req, run, ev)
}

// evidence 只读核实现场；读不到就返回 unknown，不猜。
func (p *leadReconcilePort) evidence(run *db.Run, opID string) recovery.Evidence {
	if opID == "" {
		if run.StartedAt == "" {
			return recovery.Evidence{
				State:  recovery.StateNotStarted,
				Reason: "没有操作目录且从未启动：确认从未启动过",
			}
		}
		return recovery.Evidence{
			State:  recovery.StateUnknown,
			Reason: "找不到该 Run 的操作目录，无法核实进程现场",
		}
	}
	ev, err := recovery.Reconcile(recovery.Options{
		Dir:   p.rt.OpDir(opID),
		RunID: run.ID,
		Probe: p.probe,
	})
	if err != nil {
		return recovery.Evidence{
			State:  recovery.StateUnknown,
			Reason: "核实失败：" + err.Error(),
		}
	}
	return ev
}

// keep 保留占用：写一条核实事件并刷新 Run 的 updated_at。
//
// 刷新 updated_at 是为了让「最近一次核实」成为可观察事实，否则同一现场会按冷却
// 周期反复入队；release 路径不需要，因为状态转移本身会刷新。
func (p *leadReconcilePort) keep(req systemagent.ReconcileRequest, run *db.Run, ev recovery.Evidence) error {
	kind := "run.reconcile_keep"
	if ev.State == recovery.StateUnknown {
		kind = "run.reconcile_unknown"
	}
	return p.store.Write(func(tx *sql.Tx) error {
		if _, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
			ID:            reconcileEventID(req.JobID, run.ID, kind),
			ProjectID:     req.ProjectID,
			RunID:         run.ID,
			InstanceID:    run.InstanceID,
			Kind:          kind,
			SchemaVersion: db.RunEventSchemaVersion,
			Payload: map[string]any{
				"state":           string(ev.State),
				"reason":          ev.Reason,
				"journal_pending": ev.Journal.PendingRecords,
				"job_id":          req.JobID,
			},
		}); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE runs SET updated_at = ? WHERE run_id = ?`, db.UTCNow(), run.ID)
		return err
	})
}

// escalate 升级人工：只写一条结论事件，不碰任何资源、不改任务状态。
//
// 事件写不进去也不能把「未处置」说成处置过：仍然返回 escalate，让作业落 escalated。
func (p *leadReconcilePort) escalate(req systemagent.ReconcileRequest, runID, instanceID string, ev recovery.Evidence) systemagent.ReconcileOutcome {
	_ = p.store.Write(func(tx *sql.Tx) error {
		_, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
			ID:            reconcileEventID(req.JobID, runID, "run.reconcile_escalate"),
			ProjectID:     req.ProjectID,
			RunID:         runID,
			InstanceID:    instanceID,
			Kind:          "run.reconcile_escalate",
			SchemaVersion: db.RunEventSchemaVersion,
			Payload: map[string]any{
				"mode":            string(systemagent.ReconcileEscalate),
				"reconcile_state": string(ev.State),
				"evidence_reason": ev.Reason,
				"job_id":          req.JobID,
			},
		})
		return err
	})
	return systemagent.ReconcileOutcome{Verdict: systemagent.ReconcileEscalate, Evidence: ev}
}

// release 在一个事务里收尾 Run、释放占用并把任务回到可再领状态。
//
// 失败不返回 error：同一份现场重试多少次结论都一样，升级人工比反复领取更有用。
func (p *leadReconcilePort) release(req systemagent.ReconcileRequest, run *db.Run, ev recovery.Evidence) (systemagent.ReconcileOutcome, error) {
	var released []string
	err := p.store.Write(func(tx *sql.Tx) error {
		cur, err := db.GetRun(tx, run.ID)
		if err != nil {
			return err
		}
		if cur == nil {
			return fmt.Errorf("Run %q 在释放事务里消失", run.ID)
		}

		// 1) 现场已确认：unknown 的 Run 不得直接落终态（runtime.state/v1 E232）。
		if cur.Reconciliation != model.ReconciliationConfirmed {
			if _, err := tx.Exec(`UPDATE runs SET reconciliation_state = ?, updated_at = ?
WHERE run_id = ?`, string(model.ReconciliationConfirmed), db.UTCNow(), cur.ID); err != nil {
				return err
			}
			cur.Reconciliation = model.ReconciliationConfirmed
		}

		// 2) 进程已停：释放 node_slot 的前提（E231）。
		if !cur.ProcessStopConfirmed {
			stop := "recovery: " + ev.Reason
			if ev.Stop != nil {
				stop = fmt.Sprintf("recovery: %s（%s，进程组清空=%t）", ev.Reason, ev.Stop.Outcome, ev.Stop.GroupEmpty)
			}
			if err := db.ConfirmProcessStop(tx, cur.ID, stop); err != nil {
				return err
			}
		}

		// 3) 阶段收尾：只在需要时按合法转移逐跳走，事件与状态同批提交。
		if !cur.Phase.Terminal() {
			target := terminalPhaseFor(ev)
			path, ok := runPhasePath(cur.Phase, target)
			if !ok {
				return fmt.Errorf("阶段 %s 无法合法收尾到 %s", cur.Phase, target)
			}
			for _, next := range path {
				if _, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
					ID:            reconcileEventID(req.JobID, cur.ID, db.EventRunPhase+":"+string(next)),
					ProjectID:     req.ProjectID,
					RunID:         cur.ID,
					InstanceID:    cur.InstanceID,
					Kind:          db.EventRunPhase,
					Phase:         next,
					SchemaVersion: db.RunEventSchemaVersion,
					Payload: map[string]any{
						"reason": "run_reconciled:" + string(ev.State),
					},
				}); err != nil {
					return err
				}
				evidence := append([]string{}, cur.Evidence...)
				evidence = append(evidence, "recovery:"+string(ev.State))
				updated, err := db.UpdateRunProgress(tx, cur.ID, next, next.Terminal(), cur.DeliveryFrozen, evidence)
				if err != nil {
					return err
				}
				cur = updated
			}
		}

		// 4) 释放占用：节点槽位可直接放；目录只在交付已冻结时放（E242）。
		leases, err := db.ListActiveLeases(tx, req.ProjectID)
		if err != nil {
			return err
		}
		for _, l := range leases {
			if l == nil || l.InstanceID != cur.InstanceID || l.Released() {
				continue
			}
			switch l.Kind {
			case model.LeaseNodeSlot:
				released = append(released, string(l.Kind))
			case model.LeaseWorkspace:
				if !cur.DeliveryFrozen {
					_ = db.AppendEvent(tx, req.ProjectID, "system", "run_reconcile_workspace_kept", req.TaskID, map[string]any{
						"run_id": cur.ID, "lease_id": l.ID, "reason": "delivery_not_frozen",
					})
					continue
				}
				released = append(released, string(l.Kind))
			default:
				continue
			}
			if err := db.ReleaseLease(tx, l.ID, l.Generation, "run_reconciled:"+string(ev.State),
				db.ReleaseCondition{ProcessStopConfirmed: true, DeliveryFrozen: cur.DeliveryFrozen}); err != nil {
				return err
			}
		}

		// 5) 任务回到可再领状态；转移不合法时落 blocked 并带原因，不猜下一步。
		if err := rollbackReconciledTask(tx, req, cur, ev); err != nil {
			return err
		}

		// 6) 收尾事件：release 的结论与释放清单。
		if _, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
			ID:            reconcileEventID(req.JobID, cur.ID, "run.reconciled"),
			ProjectID:     req.ProjectID,
			RunID:         cur.ID,
			InstanceID:    cur.InstanceID,
			Kind:          "run.reconciled",
			SchemaVersion: db.RunEventSchemaVersion,
			Payload: map[string]any{
				"mode":               string(systemagent.ReconcileRelease),
				"reconcile_state":    string(ev.State),
				"evidence_reason":    ev.Reason,
				"resources_released": released,
				"job_id":             req.JobID,
			},
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return p.escalate(req, run.ID, run.InstanceID,
			recovery.Evidence{State: ev.State, Reason: ev.Reason + "；释放失败：" + err.Error()}), nil
	}
	return systemagent.ReconcileOutcome{
		Verdict:           systemagent.ReconcileRelease,
		Evidence:          ev,
		ResourcesReleased: released,
	}, nil
}

// rollbackReconciledTask 把仍由该实例持有的任务放回 ready。
//
// 复用 machine.ActiveOwner 与 db.UpsertTask，和 agent 失联回收走同一套回写；
// 区别只在原因字段，便于看板区分「进程核实」与「心跳过期」。
func rollbackReconciledTask(tx *sql.Tx, req systemagent.ReconcileRequest, run *db.Run, ev recovery.Evidence) error {
	t, err := db.GetTask(tx, req.ProjectID, req.TaskID)
	if err != nil {
		return err
	}
	if t == nil {
		tasks, err := db.ListTasks(tx, req.ProjectID)
		if err != nil {
			return err
		}
		for _, cand := range tasks {
			if cand != nil && cand.InstanceID == run.InstanceID {
				t = cand
				break
			}
		}
	}
	if t == nil || t.InstanceID != run.InstanceID || !machine.ActiveOwner[t.Status] {
		return nil
	}

	reason := "run_reconciled:" + string(ev.State)
	from := t.Status
	if err := machine.AssertTransition(t.Status, "ready"); err != nil {
		t.PrevStatus = t.Status
		t.Status = "blocked"
		t.BlockedReason = reason
	} else {
		t.Status = "ready"
		t.BlockedReason = ""
	}
	t.Owner = ""
	t.Branch = ""
	t.Worktree = ""
	t.Heartbeat = ""
	t.Revision++
	if err := db.UpsertTask(tx, t); err != nil {
		return err
	}
	_ = db.AppendEvent(tx, req.ProjectID, "system", "status_changed", t.ID, map[string]any{
		"from": from, "to": t.Status, "reason": reason,
	})
	return nil
}

// terminalPhaseFor 把「确定结束」映射成 Run 终态：退出码 0 记 completed，其余记 failed。
// 从未启动一律 failed（never_started）。
func terminalPhaseFor(ev recovery.Evidence) model.RunPhase {
	if ev.State == recovery.StateFinished && ev.Stop != nil && ev.Stop.Exited && ev.Stop.ExitCode == 0 {
		return model.PhaseCompleted
	}
	return model.PhaseFailed
}

// runPhasePath 用转移表做最短路：不硬编码「应该经过哪几跳」，只走契约允许的边。
func runPhasePath(from, to model.RunPhase) ([]model.RunPhase, bool) {
	if from == to {
		return nil, true
	}
	queue := [][]model.RunPhase{{from}}
	seen := map[model.RunPhase]bool{from: true}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		cur := path[len(path)-1]
		for _, next := range model.AllRunPhases() {
			if seen[next] || !model.CanTransition(cur, next) {
				continue
			}
			nextPath := append(append([]model.RunPhase{}, path...), next)
			if next == to {
				return nextPath[1:], true
			}
			seen[next] = true
			queue = append(queue, nextPath)
		}
	}
	return nil, false
}

// operationDir 定位 Run 对应的持久操作目录（ops/<operation_id>）。
func (p *leadReconcilePort) operationDir(tx *sql.Tx, projectID, runID string) (string, error) {
	if p.operationOf != nil {
		return p.operationOf(tx, projectID, runID)
	}
	var opID string
	err := tx.QueryRow(`SELECT operation_id FROM operations
WHERE project_id = ? AND run_id = ? ORDER BY created_at DESC LIMIT 1`, projectID, runID).Scan(&opID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return opID, err
}

// reconcileEventID 由作业与结论确定性生成：同一结论重放不重复写事件。
func reconcileEventID(jobID, runID, kind string) model.EventID {
	sum := sha256.Sum256([]byte(jobID + "|" + runID + "|" + kind + "|lead-reconcile"))
	return model.EventID("evt_" + hex.EncodeToString(sum[:8]))
}
