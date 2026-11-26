package app

import (
	"database/sql"
	"os/exec"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
)

// ReconcileOptions 控制对账扫描与修复。
type ReconcileOptions struct {
	Apply         bool
	SyncCheckout  bool
	DryRun        bool // 兼容全局 --dry-run：强制只预览
}

// reconcileFinding 是一条可报告／可修复的生命周期不一致。
type reconcileFinding struct {
	Kind     string `json:"kind"`
	TaskID   string `json:"task_id,omitempty"`
	Instance string `json:"instance_id,omitempty"`
	RunID    string `json:"run_id,omitempty"`
	Detail   string `json:"detail"`
	Fixable  bool   `json:"fixable"`
	Fixed    bool   `json:"fixed,omitempty"`
}

// Reconcile 扫描（并可选修复）生命周期残留：版本漂移、终态残留占用、主检出 tip 漂移。
//
// 默认 dry-run；--apply 需 lead／system。主检出 hard reset 仅在 --sync-checkout
// 且脏状态可解释为 tip 漂移时执行。
func Reconcile(cwd string, opts ReconcileOptions) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	preview := opts.DryRun || !opts.Apply
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		who = "orchestrator"
	}
	if !preview {
		if err := identity.RequireLeadAction(who, pc.Config, "reconcile"); err != nil {
			return nil, err
		}
	}

	findings, err := scanReconcileFindings(pc)
	if err != nil {
		return nil, err
	}

	fixed := 0
	if !preview {
		for i := range findings {
			f := &findings[i]
			if !f.Fixable {
				continue
			}
			ok, err := applyReconcileFinding(pc, who, f, opts.SyncCheckout)
			if err != nil {
				return nil, err
			}
			if ok {
				f.Fixed = true
				fixed++
			}
		}
		// 修复后再扫一遍，报告仍残留项。
		after, err := scanReconcileFindings(pc)
		if err != nil {
			return nil, err
		}
		findings = mergeReconcileFindings(findings, after)
	}

	unfixed := 0
	for _, f := range findings {
		if !f.Fixed {
			unfixed++
		}
	}
	next := i18n.T("next.reconcile_preview")
	if !preview {
		next = i18n.T("next.reconcile_applied", fixed, unfixed)
	}
	return Result{
		"ok":       true,
		"dry_run":  preview,
		"project_id": pc.ID(),
		"findings": findings,
		"fixed":    fixed,
		"remaining": unfixed,
		"next":     next,
	}, nil
}

func scanReconcileFindings(pc *projCtx) ([]reconcileFinding, error) {
	var findings []reconcileFinding
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		tasks, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		runs, err := db.ListRuns(tx, pc.ID())
		if err != nil {
			return err
		}
		byInst := map[string][]*db.Run{}
		for _, r := range runs {
			if r == nil {
				continue
			}
			byInst[r.InstanceID] = append(byInst[r.InstanceID], r)
		}
		leases, err := db.ListActiveLeases(tx, pc.ID())
		if err != nil {
			return err
		}
		leaseByInst := map[string]int{}
		for _, l := range leases {
			if l == nil || l.Released() {
				continue
			}
			leaseByInst[l.InstanceID]++
		}

		for _, t := range tasks {
			if t == nil || t.InstanceID == "" {
				continue
			}
			inst, err := db.GetInstance(tx, t.InstanceID)
			if err != nil {
				return err
			}
			for _, r := range byInst[t.InstanceID] {
				if r == nil || !r.Occupies() {
					continue
				}
				if inst != nil && inst.ActiveVersion != nil && r.VersionNo != *inst.ActiveVersion {
					findings = append(findings, reconcileFinding{
						Kind:     "version_drift",
						TaskID:   t.ID,
						Instance: t.InstanceID,
						RunID:    r.ID,
						Detail:   "run.version_no != instance.active_version (E330 risk)",
						Fixable:  false, // 不得改写占用中 Run 的冻结版本；应开新题
					})
				}
			}
			terminal := t.Status == "cancelled" || t.Status == "done"
			if !terminal {
				continue
			}
			occupying := 0
			for _, r := range byInst[t.InstanceID] {
				if r != nil && r.Occupies() {
					occupying++
				}
			}
			if occupying > 0 || leaseByInst[t.InstanceID] > 0 {
				findings = append(findings, reconcileFinding{
					Kind:     "terminal_leak",
					TaskID:   t.ID,
					Instance: t.InstanceID,
					Detail:   "terminal task still holds run/lease",
					Fixable:  true,
				})
			}
		}
		// 孤儿 lease：instance 已无对应活跃任务行仍占资源。
		taskInst := map[string]bool{}
		taskByInst := map[string]*db.Task{}
		for _, t := range tasks {
			if t != nil && t.InstanceID != "" {
				taskInst[t.InstanceID] = true
				taskByInst[t.InstanceID] = t
			}
		}
		for instID, n := range leaseByInst {
			if n == 0 || taskInst[instID] {
				continue
			}
			findings = append(findings, reconcileFinding{
				Kind:     "orphan_lease",
				Instance: instID,
				Detail:   "active lease without task row",
				Fixable:  true,
			})
		}
		// blocked / ready 仍占 workspace：常见于 agent_expired 后未释放，挡后续 claim（E240）。
		seenStale := map[string]bool{}
		for _, l := range leases {
			if l == nil || l.Released() || string(l.Kind) != "workspace" {
				continue
			}
			t := taskByInst[l.InstanceID]
			if t == nil {
				continue
			}
			if t.Status != "blocked" && t.Status != "ready" {
				continue
			}
			if seenStale[t.InstanceID] {
				continue
			}
			seenStale[t.InstanceID] = true
			findings = append(findings, reconcileFinding{
				Kind:     "stale_workspace_lease",
				TaskID:   t.ID,
				Instance: t.InstanceID,
				Detail:   "task " + t.Status + " still holds workspace lease (E240 risk)",
				Fixable:  true,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if f, ok := scanCheckoutDrift(pc); ok {
		findings = append(findings, f)
	}
	return findings, nil
}

func scanCheckoutDrift(pc *projCtx) (reconcileFinding, bool) {
	main := strings.TrimSpace(pc.Config.Project.MainBranch)
	if main == "" {
		return reconcileFinding{}, false
	}
	br, err := gitops.CurrentBranch(pc.Repo)
	if err != nil || strings.TrimSpace(br) != main {
		return reconcileFinding{}, false
	}
	// HEAD 与 index/worktree 任一不一致即视为 tip 脱节征兆。
	if exec.Command("git", "-C", pc.Repo, "diff", "--quiet", "HEAD").Run() == nil &&
		exec.Command("git", "-C", pc.Repo, "diff", "--cached", "--quiet", "HEAD").Run() == nil {
		return reconcileFinding{}, false
	}
	return reconcileFinding{
		Kind:    "checkout_drift",
		Detail:  "primary checkout on " + main + " differs from HEAD (possible post-merge reverse index)",
		Fixable: true,
	}, true
}

func applyReconcileFinding(pc *projCtx, who string, f *reconcileFinding, syncCheckout bool) (bool, error) {
	switch f.Kind {
	case "terminal_leak":
		err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			t, err := db.GetTask(tx, pc.ID(), f.TaskID)
			if err != nil || t == nil {
				return err
			}
			_, err = cascadeCancelOccupancy(tx, pc.ID(), t)
			if err != nil {
				return err
			}
			return db.AppendEvent(tx, pc.ID(), who, "reconcile_terminal_leak", f.TaskID, map[string]any{
				"instance_id": f.Instance,
			})
		})
		return err == nil, err
	case "orphan_lease":
		err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			_, err := releaseInstanceLeasesCounted(tx, pc.ID(), f.Instance, "reconcile_orphan_lease")
			return err
		})
		return err == nil, err
	case "stale_workspace_lease":
		err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			n, err := releaseInstanceLeasesSafe(tx, pc.ID(), f.Instance, "reconcile_stale_workspace_lease")
			if err != nil {
				return err
			}
			if n == 0 {
				return nil
			}
			return db.AppendEvent(tx, pc.ID(), who, "reconcile_stale_workspace_lease", f.TaskID, map[string]any{
				"instance_id": f.Instance, "released": n,
			})
		})
		return err == nil, err
	case "checkout_drift":
		if !syncCheckout {
			f.Detail += "; pass --sync-checkout to reset when tip-drift only"
			return false, nil
		}
		ok, detail, err := syncPrimaryCheckoutIfTipDrift(pc)
		if err != nil {
			return false, err
		}
		if !ok {
			f.Detail = detail
			return false, nil
		}
		f.Detail = detail
		return true, nil
	default:
		return false, nil
	}
}

// syncPrimaryCheckoutIfTipDrift 在主检出位于主干且相对「索引/工作区像旧 tip」时可 hard reset。
// 有无法解释的本地改动时拒绝，避免毁掉未提交工作。
func syncPrimaryCheckoutIfTipDrift(pc *projCtx) (bool, string, error) {
	main := strings.TrimSpace(pc.Config.Project.MainBranch)
	br, err := gitops.CurrentBranch(pc.Repo)
	if err != nil {
		return false, "", err
	}
	if strings.TrimSpace(br) != main {
		return false, "not on main branch", nil
	}
	head, err := home.Git(pc.Repo, "rev-parse", "HEAD")
	if err != nil {
		return false, "", err
	}
	head = strings.TrimSpace(head)
	// 有未跟踪文件则不自动 reset。
	untracked, _ := home.Git(pc.Repo, "ls-files", "--others", "--exclude-standard")
	if strings.TrimSpace(untracked) != "" {
		return false, "untracked files present; refuse auto reset", nil
	}
	// porcelain 非空：仅当全部是对 tracked 文件相对 HEAD 的差异（tip 前移典型症状）才同步。
	porcelain, _ := home.Git(pc.Repo, "status", "--porcelain")
	if strings.TrimSpace(porcelain) == "" {
		return true, "already clean at " + shortSHA(head), nil
	}
	if out, err := home.Git(pc.Repo, "reset", "--hard", "HEAD"); err != nil {
		return false, out, err
	}
	return true, "reset --hard to " + shortSHA(head), nil
}

func mergeReconcileFindings(before, after []reconcileFinding) []reconcileFinding {
	keyOf := func(f reconcileFinding) string {
		return f.Kind + "|" + f.TaskID + "|" + f.Instance + "|" + f.RunID
	}
	still := map[string]bool{}
	for _, f := range after {
		still[keyOf(f)] = true
	}
	var out []reconcileFinding
	seen := map[string]bool{}
	for _, f := range before {
		k := keyOf(f)
		seen[k] = true
		if f.Fixed || !still[k] {
			f.Fixed = true
			out = append(out, f)
			continue
		}
		out = append(out, f)
	}
	for _, f := range after {
		if !seen[keyOf(f)] {
			out = append(out, f)
		}
	}
	return out
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
