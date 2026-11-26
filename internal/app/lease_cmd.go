package app

import (
	"database/sql"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
)

// LeaseList 列出工程内有效资源租约。
func LeaseList(cwd string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	var leases []*db.Lease
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		leases, e = db.ListActiveLeases(tx, pc.ID())
		return e
	})
	if err != nil {
		return nil, err
	}
	if leases == nil {
		leases = []*db.Lease{}
	}
	items := make([]map[string]any, 0, len(leases))
	for _, l := range leases {
		if l == nil {
			continue
		}
		items = append(items, map[string]any{
			"lease_id":     l.ID,
			"kind":         string(l.Kind),
			"resource_key": l.ResourceKey,
			"instance_id":  l.InstanceID,
			"run_id":       l.RunID,
			"generation":   int(l.Generation),
			"holder":       l.Holder,
			"acquired_at":  l.AcquiredAt,
		})
	}
	return Result{
		"ok": true, "project_id": pc.ID(), "leases": items, "count": len(items),
		"next": "maw lease release --lease <id> --yes 或 --task <id> --yes",
	}, nil
}

// LeaseReleaseOptions 显式释放租约。
type LeaseReleaseOptions struct {
	LeaseID string
	TaskID  string
	Yes     bool
	DryRun  bool
}

// LeaseRelease 按 lease id 或 task id 释放有效租约（需 --yes）。
func LeaseRelease(cwd string, opts LeaseReleaseOptions) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		who = "orchestrator"
	}
	leaseID := strings.TrimSpace(opts.LeaseID)
	taskID := strings.TrimSpace(opts.TaskID)
	if leaseID == "" && taskID == "" {
		return nil, i18n.E("err.lease_release_args", "next.lease_list")
	}
	preview := opts.DryRun || !opts.Yes

	var released []string
	var previewItems []map[string]any
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var targets []*db.Lease
		if leaseID != "" {
			l, e := db.GetLease(tx, leaseID)
			if e != nil {
				return e
			}
			if l == nil || l.Released() {
				return i18n.E("err.lease_not_active", "next.lease_list", leaseID)
			}
			if l.ProjectID != "" && l.ProjectID != pc.ID() {
				return i18n.E("err.lease_wrong_project", "", leaseID)
			}
			targets = append(targets, l)
		} else {
			t, e := db.GetTask(tx, pc.ID(), taskID)
			if e != nil || t == nil {
				return i18n.E("err.unknown_task", "next.task_list", taskID)
			}
			if t.InstanceID == "" {
				return i18n.E("err.task_no_instance", "", taskID)
			}
			all, e := db.ListActiveLeases(tx, pc.ID())
			if e != nil {
				return e
			}
			for _, l := range all {
				if l != nil && l.InstanceID == t.InstanceID && !l.Released() {
					targets = append(targets, l)
				}
			}
		}
		for _, l := range targets {
			previewItems = append(previewItems, map[string]any{
				"lease_id": l.ID, "kind": string(l.Kind), "resource_key": l.ResourceKey,
				"instance_id": l.InstanceID, "holder": l.Holder,
			})
			if preview {
				continue
			}
			cond := db.ReleaseCondition{ProcessStopConfirmed: true, DeliveryFrozen: true}
			if err := db.ReleaseLease(tx, l.ID, l.Generation, "manual_lease_release:"+who, cond); err != nil {
				return err
			}
			released = append(released, l.ID)
			_ = db.AppendEvent(tx, pc.ID(), who, "lease_manual_released", taskID, map[string]any{
				"lease_id": l.ID, "kind": string(l.Kind), "resource_key": l.ResourceKey,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	next := "maw lease release --yes 确认释放"
	if !preview {
		next = "租约已释放；可 maw next --claim 或 maw prune"
	}
	if len(previewItems) == 0 {
		next = "无有效租约可释放"
	}
	return Result{
		"ok": true, "dry_run": preview, "project_id": pc.ID(),
		"leases": previewItems, "released": released, "next": next,
	}, nil
}

// releaseInstanceLeasesSafe 在无受管进程占用时释放实例上的有效租约。
// 用于 unblock / unregister / reconcile，避免 E240 死结。
func releaseInstanceLeasesSafe(tx *sql.Tx, projectID, instanceID, reason string) (int, error) {
	if instanceID == "" {
		return 0, nil
	}
	run, err := occupyingRun(tx, projectID, instanceID)
	if err != nil {
		return 0, err
	}
	if run != nil && !runHasNoProcessEvidence(tx, run) {
		// 仍有进程证据：不得强释 workspace。
		return 0, nil
	}
	return releaseInstanceLeasesCounted(tx, projectID, instanceID, reason)
}

// releaseTaskLeasesOnUnblock 解阻后释放租约以便重领。
// merging/in_review 可直接 merge，保留租约；其余目标（含 Owner 已空的 in_progress）释放，避免 E240。
func releaseTaskLeasesOnUnblock(tx *sql.Tx, projectID string, t *db.Task, target, blockedReason string) (int, error) {
	if t == nil || t.InstanceID == "" {
		return 0, nil
	}
	_ = blockedReason
	if waitingHumanMerge(target) {
		return 0, nil
	}
	return releaseInstanceLeasesSafe(tx, projectID, t.InstanceID, "unblock_release")
}

// releaseAgentLeasesOnUnregister 注销时释放该 Agent 名下、非等待合入题的租约。
func releaseAgentLeasesOnUnregister(tx *sql.Tx, projectID, agentID string) (int, error) {
	tasks, err := db.ListTasks(tx, projectID)
	if err != nil {
		return 0, err
	}
	total := 0
	seen := map[string]bool{}
	for _, t := range tasks {
		if t == nil || t.Owner != agentID || t.InstanceID == "" {
			continue
		}
		if waitingHumanMerge(t.Status) {
			continue
		}
		if seen[t.InstanceID] {
			continue
		}
		seen[t.InstanceID] = true
		n, err := releaseInstanceLeasesSafe(tx, projectID, t.InstanceID, "unregister:"+agentID)
		if err != nil {
			return total, err
		}
		total += n
	}
	// 另：holder 匹配但任务 Owner 已空的残留租约
	leases, err := db.ListActiveLeases(tx, projectID)
	if err != nil {
		return total, err
	}
	for _, l := range leases {
		if l == nil || l.Released() || l.Holder != agentID {
			continue
		}
		if seen[l.InstanceID] {
			continue
		}
		// 若对应题仍在等待合入则跳过
		keep := false
		for _, t := range tasks {
			if t != nil && t.InstanceID == l.InstanceID && waitingHumanMerge(t.Status) {
				keep = true
				break
			}
		}
		if keep {
			continue
		}
		cond := db.ReleaseCondition{ProcessStopConfirmed: true, DeliveryFrozen: true}
		if err := db.ReleaseLease(tx, l.ID, l.Generation, "unregister:"+agentID, cond); err != nil {
			// 受管占用拒绝时跳过该条，不中断其它释放
			if strings.Contains(err.Error(), "E24") || strings.Contains(err.Error(), "不得释放") {
				continue
			}
			return total, err
		}
		total++
		seen[l.InstanceID] = true
	}
	return total, nil
}
