package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuhao/maw/internal/blueprint"
	"github.com/wuhao/maw/internal/config"
	"github.com/wuhao/maw/internal/db"
	"github.com/wuhao/maw/internal/errx"
	"github.com/wuhao/maw/internal/gitops"
	"github.com/wuhao/maw/internal/home"
	"github.com/wuhao/maw/internal/identity"
	"github.com/wuhao/maw/internal/machine"
)

func PlanCheck(path, cwd string) (Result, error) {
	f, err := blueprint.Load(path)
	if err != nil {
		return nil, err
	}
	errs := blueprint.Validate(f)
	warns := blueprint.ValidateLint(f)
	parallel := blueprint.ParallelWidth(f)
	next := "修复蓝图错误"
	if len(errs) == 0 {
		next = "maw plan apply " + path + " --yes"
		if len(warns) > 0 {
			next = "有告警但仍可 apply；建议按 TASK-SPEC 收紧后: maw plan apply " + path + " --yes"
		}
	}
	return Result{
		"ok": len(errs) == 0, "errors": errs, "warnings": warns,
		"parallel": parallel, "task_count": len(f.Tasks), "next": next,
	}, nil
}

// PlanApply 把蓝图增量合入工程任务表。
//
// 默认（reset=false）：
//   - 保留 done / cancelled / blocked / 进行中（ActiveOwner）任务记录与状态
//   - 蓝图内新题：插入为 backlog
//   - 蓝图内已有 idle（backlog/ready）题：按蓝图字段覆盖并置回 backlog，再 promoteReady
//   - 蓝图内已有保留态题：只刷新标题/优先级/deps/scope/acceptance，不改状态与领取字段
//   - 蓝图外的 idle 题：删除（表示从计划中拿掉）；蓝图外的保留态题：不动
//
// reset=true（--reset）：恢复旧行为，清空该工程全部任务后再灌入蓝图（显式销毁历史）。
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
		return nil, errx.New("蓝图校验失败:\n- "+strings.Join(errs, "\n- "), "修复依赖环或必填字段后重试")
	}
	mainBranch := strings.TrimSpace(f.Meta.MainBranch)
	if mainBranch != "" {
		if _, err := gitops.EnsureBranchBase(pc.Repo, mainBranch); err != nil {
			return nil, err
		}
	}
	tasks := blueprint.TasksForDB(f)
	for _, t := range tasks {
		t.ProjectID = pc.ID()
	}
	effectiveMain := pc.Config.Project.MainBranch
	if mainBranch != "" {
		effectiveMain = mainBranch
	}
	if dryRun {
		return Result{
			"ok": true, "dry_run": true, "reset": reset,
			"tasks": tasks, "project_id": pc.ID(),
			"main_branch": effectiveMain,
			"note": "默认增量合并；加 --reset 才会清空历史任务；meta.main_branch 非空时会同步 config",
		}, nil
	}
	if pc.Config.Approvals.Plan == "manual" && !yes {
		return nil, errx.New("plan apply 需要确认", "复查任务后加 --yes")
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
	)
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		if mainBranch != "" {
			pc.Project.MainBranch = mainBranch
			pc.Project.MaxParallel = pc.Config.Project.MaxParallel
			if err := db.UpsertProject(tx, pc.Project); err != nil {
				return err
			}
		}
		if reset {
			if err := db.DeleteTasks(tx, pc.ID()); err != nil {
				return err
			}
			if err := db.DeleteSharedFiles(tx, pc.ID()); err != nil {
				return err
			}
			for _, t := range tasks {
				if err := db.UpsertTask(tx, t); err != nil {
					return err
				}
				created++
				_ = db.AppendEvent(tx, pc.ID(), agent, "task_created", t.ID, map[string]any{"status": t.Status})
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
			inPlan := make(map[string]bool, len(tasks))
			for _, nt := range tasks {
				inPlan[nt.ID] = true
				old := byID[nt.ID]
				if old != nil && machine.PreserveOnPlanApply(old.Status) {
					mergePlanFields(old, nt)
					if err := db.UpsertTask(tx, old); err != nil {
						return err
					}
					preserved++
					_ = db.AppendEvent(tx, pc.ID(), agent, "task_plan_updated", old.ID, map[string]any{
						"status": old.Status, "kept": true,
					})
					continue
				}
				if old == nil {
					created++
					if err := db.UpsertTask(tx, nt); err != nil {
						return err
					}
					_ = db.AppendEvent(tx, pc.ID(), agent, "task_created", nt.ID, map[string]any{"status": nt.Status})
				} else {
					updated++
					if err := db.UpsertTask(tx, nt); err != nil {
						return err
					}
					// B2：更新已有题发 task_updated，不再误报 task_created
					_ = db.AppendEvent(tx, pc.ID(), agent, "task_updated", nt.ID, map[string]any{
						"status": nt.Status, "from_status": old.Status,
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
				// B3：软删除为 cancelled，保留事件链；不再物理 DeleteTask
				from := old.Status
				old.Status = "cancelled"
				old.BlockedReason = "removed_from_plan"
				old.Owner = ""
				old.Branch = ""
				old.Worktree = ""
				old.Revision++
				if err := db.UpsertTask(tx, old); err != nil {
					return err
				}
				removed++
				_ = db.AppendEvent(tx, pc.ID(), agent, "task_removed_from_plan", old.ID, map[string]any{
					"from": from, "to": "cancelled",
				})
				_ = db.AppendEvent(tx, pc.ID(), agent, "status_changed", old.ID, map[string]any{
					"from": from, "to": "cancelled", "reason": "removed_from_plan",
				})
			}
			// shared_files 是计划配置，按蓝图全量替换（与任务历史无关）
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
		})
	})
	if err != nil {
		return nil, err
	}
	var out []*db.Task
	_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		out, err = db.ListTasks(tx, pc.ID())
		return err
	})
	html, _ := refreshBoard(pc)
	return Result{
		"ok": true, "project_id": pc.ID(), "project_name": pc.Project.Name,
		"reset": reset,
		"main_branch": pc.Config.Project.MainBranch,
		"created": created, "updated": updated, "preserved": preserved, "removed": removed,
		"tasks": out, "board": html,
		"next": "maw register && maw next  # 自动分配 {应用}-{工程}-{序号}，或 export MAW_AGENT=cursor-<工程slug>-1",
	}, nil
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
