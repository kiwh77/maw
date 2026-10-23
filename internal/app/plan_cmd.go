package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/blueprint"
	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/machine"
)

func PlanCheck(path, cwd string) (Result, error) {
	f, err := blueprint.Load(path)
	if err != nil {
		return nil, err
	}
	errs := blueprint.Validate(f)
	warns := blueprint.ValidateLint(f)
	parallel := blueprint.ParallelWidth(f)
	next := i18n.T("plan.next_fix")
	if len(errs) == 0 {
		next = "maw plan apply " + path + " --yes"
		if len(warns) > 0 {
			next = i18n.T("plan.next_warn", path)
		}
	}
	return Result{
		"ok": len(errs) == 0, "errors": errs, "warnings": warns,
		"parallel": parallel, "task_count": len(f.Tasks), "next": next,
	}, nil
}

// PlanApply 把蓝图增量合入工程任务表，并接通稳定任务实例身份。
//
// 默认（reset=false）：
//   - 保留 done / cancelled / blocked / 进行中（ActiveOwner）任务记录与状态
//   - 蓝图内新题：插入为 backlog，绑定新 instance_id
//   - 蓝图内已有 idle（backlog/ready）题：按蓝图字段覆盖并置回 backlog，再 promoteReady
//   - 蓝图内已有保留态题：只刷新计划字段；定义（标题／依赖）变化时追加冻结版本
//   - 蓝图外的 idle 题：退役实例并释放别名（不是 soft-cancel）；蓝图外的保留态题：不动
//
// reset=true（--reset）：显式退役全部活跃实例后重建；若仍有占用 Run／进行中任务则拒绝。
func PlanApply(path, cwd string, yes, dryRun, reset bool) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	f, err := blueprint.Load(path)
	if err != nil {
		return nil, err
	}
	if errs := blueprint.Validate(f); len(errs) > 0 {
		return nil, i18n.E("err.plan_check_fail", "next.fix_deps", strings.Join(errs, "\n- "))
	}
	mainBranch := strings.TrimSpace(f.Meta.MainBranch)
	if mainBranch != "" {
		if _, err := gitops.EnsureBranchBase(pc.Repo, mainBranch); err != nil {
			return nil, err
		}
	}
	planTasks := blueprint.TasksForDB(f)
	for _, t := range planTasks {
		t.ProjectID = pc.ID()
	}
	effectiveMain := pc.Config.Project.MainBranch
	if mainBranch != "" {
		effectiveMain = mainBranch
	}
	if dryRun {
		return Result{
			"ok": true, "dry_run": true, "reset": reset,
			"tasks": planTasks, "project_id": pc.ID(),
			"main_branch": effectiveMain,
			"note":        i18n.T("plan.note_incremental"),
		}, nil
	}
	if pc.Config.Approvals.Plan == "manual" && !yes {
		return nil, i18n.E("err.plan_need_yes", "next.add_yes")
	}
	if mainBranch != "" {
		cfgPath := home.ConfigPath(pc.LocalHome)
		if err := config.SetMainBranch(cfgPath, mainBranch); err != nil {
			return nil, err
		}
		reloaded, err := config.Load(cfgPath)
		if err != nil {
			return nil, err
		}
		pc.Config = reloaded
		pc.Project.MainBranch = mainBranch
	}
	dest := home.BlueprintPath(pc.LocalHome)
	if abs, _ := filepath.Abs(path); abs != dest {
		b, _ := os.ReadFile(path)
		_ = os.WriteFile(dest, b, 0o644)
	}
	agent := "orchestrator"
	if a, err := identity.Resolve(cwd, ""); err == nil {
		agent = a
	}

	var (
		created, updated, preserved, removed int
		identityNotes                        []string
		pinned                               int
	)
	err = withGlobalWrite(pc.GlobalDB, func(tx *sql.Tx) error {
		if mainBranch != "" {
			pc.Project.MainBranch = mainBranch
			pc.Project.MaxParallel = pc.Config.Project.MaxParallel
			if err := db.UpsertProject(tx, pc.Project); err != nil {
				return err
			}
		}

		// 契约形态 DisplayID 时先跑身份规划，校验重建／冻结／退役与 pinned Run。
		if contractualDisplayIDs(planTasks) {
			ip, err := planTaskIdentity(tx, pc.ID(), planTasks, reset)
			if err != nil {
				return err
			}
			if ip != nil {
				identityNotes = append(identityNotes, ip.Notes...)
				pinned = len(ip.Pinned)
			}
		}

		if reset {
			if err := refuseResetIfOccupied(tx, pc.ID()); err != nil {
				return err
			}
			if err := db.DeleteTasks(tx, pc.ID()); err != nil {
				return err
			}
			if err := db.DeleteSharedFiles(tx, pc.ID()); err != nil {
				return err
			}
			for _, t := range planTasks {
				if err := db.UpsertTask(tx, t); err != nil {
					return err
				}
				created++
				_ = db.AppendEvent(tx, pc.ID(), agent, "task_created", t.ID, map[string]any{
					"status": t.Status, "instance_id": t.InstanceID,
				})
			}
		} else {
			existing, err := db.ListTasks(tx, pc.ID())
			if err != nil {
				return err
			}
			byID := make(map[string]*db.Task, len(existing))
			for _, t := range existing {
				byID[t.ID] = t
			}
			inPlan := make(map[string]bool, len(planTasks))
			for _, nt := range planTasks {
				inPlan[nt.ID] = true
				old := byID[nt.ID]
				if old != nil && machine.PreserveOnPlanApply(old.Status) {
					mergePlanFields(old, nt)
					if err := db.UpsertTask(tx, old); err != nil {
						return err
					}
					if err := freezeIfDefinitionChanged(tx, old); err != nil {
						return err
					}
					preserved++
					_ = db.AppendEvent(tx, pc.ID(), agent, "task_plan_updated", old.ID, map[string]any{
						"status": old.Status, "kept": true, "instance_id": old.InstanceID,
					})
					continue
				}
				if old == nil {
					created++
					if err := db.UpsertTask(tx, nt); err != nil {
						return err
					}
					_ = db.AppendEvent(tx, pc.ID(), agent, "task_created", nt.ID, map[string]any{
						"status": nt.Status, "instance_id": nt.InstanceID,
					})
				} else {
					updated++
					nt.InstanceID = old.InstanceID
					if err := db.UpsertTask(tx, nt); err != nil {
						return err
					}
					if err := freezeIfDefinitionChanged(tx, nt); err != nil {
						return err
					}
					_ = db.AppendEvent(tx, pc.ID(), agent, "task_updated", nt.ID, map[string]any{
						"status": nt.Status, "from_status": old.Status, "instance_id": nt.InstanceID,
					})
				}
			}
			for _, old := range existing {
				if inPlan[old.ID] {
					continue
				}
				if machine.PreserveOnPlanApply(old.Status) {
					preserved++
					continue
				}
				if err := retireIdleRemoved(tx, pc.ID(), old, agent); err != nil {
					return err
				}
				removed++
			}
			if err := db.DeleteSharedFiles(tx, pc.ID()); err != nil {
				return err
			}
		}
		for _, sf := range f.SharedFiles {
			if err := db.UpsertSharedFile(tx, pc.ID(), sf.Path, sf.OwnerTask); err != nil {
				return err
			}
		}
		if err := promoteReady(tx, pc.ID()); err != nil {
			return err
		}
		return db.AppendEvent(tx, pc.ID(), agent, "plan_applied", "", map[string]any{
			"path": path, "reset": reset,
			"created": created, "updated": updated, "preserved": preserved, "removed": removed,
			"pinned_runs": pinned,
		})
	})
	if err != nil {
		return nil, err
	}
	var out []*db.Task
	_ = withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		out, err = db.ListTasks(tx, pc.ID())
		return err
	})
	html, _ := refreshBoard(pc)
	res := Result{
		"ok": true, "project_id": pc.ID(), "project_name": pc.Project.Name,
		"reset":       reset,
		"main_branch": pc.Config.Project.MainBranch,
		"created":     created, "updated": updated, "preserved": preserved, "removed": removed,
		"pinned_runs": pinned,
		"tasks":       out, "board": html,
		"next":        i18n.T("next.after_apply"),
	}
	if len(identityNotes) > 0 {
		res["identity_notes"] = identityNotes
	}
	return res, nil
}

// mergePlanFields 把蓝图里的「计划字段」刷到已有保留态任务上，不碰领取/进度字段。
func mergePlanFields(dst, src *db.Task) {
	dst.Title = src.Title
	dst.Priority = src.Priority
	dst.MergeOrder = src.MergeOrder
	dst.Deps = src.Deps
	dst.WriteScope = src.WriteScope
	dst.ReadScope = src.ReadScope
	dst.Acceptance = src.Acceptance
	if src.Extra != nil {
		if dst.Extra == nil {
			dst.Extra = map[string]any{}
		}
		for k, v := range src.Extra {
			dst.Extra[k] = v
		}
	}
}
