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
	planIterations := blueprint.IterationsForDB(f)
	for _, it := range planIterations {
		it.ProjectID = pc.ID()
	}
	effectiveMain := pc.Config.Project.MainBranch
	if mainBranch != "" {
		effectiveMain = mainBranch
	}
	if dryRun {
		return Result{
			"ok": true, "dry_run": true, "reset": reset,
			"tasks": planTasks, "iterations": planIterations, "project_id": pc.ID(),
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
		staleBlocked                         []string
	)
	iterationCounts := map[string]*planIterationCount{}
	var iterationCreatedIDs, iterationUpdatedIDs []string
	err = withGlobalWrite(pc.GlobalDB, func(tx *sql.Tx) error {
		if mainBranch != "" {
			pc.Project.MainBranch = mainBranch
			pc.Project.MaxParallel = pc.Config.Project.MaxParallel
			if err := db.UpsertProject(tx, pc.Project); err != nil {
				return err
			}
		}

		var err error
		iterationCreatedIDs, iterationUpdatedIDs, err = upsertPlanIterations(tx, pc.ID(), planIterations)
		if err != nil {
			return err
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
				bumpIterationCount(iterationCounts, t.IterationID, "created")
				_ = db.AppendEvent(tx, pc.ID(), agent, "task_created", t.ID, map[string]any{
					"status": t.Status, "instance_id": t.InstanceID, "iteration_id": t.IterationID,
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
					bumpIterationCount(iterationCounts, old.IterationID, "preserved")
					_ = db.AppendEvent(tx, pc.ID(), agent, "task_plan_updated", old.ID, map[string]any{
						"status": old.Status, "kept": true, "instance_id": old.InstanceID,
						"iteration_id": old.IterationID,
					})
					continue
				}
				if old == nil {
					created++
					bumpIterationCount(iterationCounts, nt.IterationID, "created")
					if err := db.UpsertTask(tx, nt); err != nil {
						return err
					}
					_ = db.AppendEvent(tx, pc.ID(), agent, "task_created", nt.ID, map[string]any{
						"status": nt.Status, "instance_id": nt.InstanceID, "iteration_id": nt.IterationID,
					})
				} else {
					updated++
					bumpIterationCount(iterationCounts, nt.IterationID, "updated")
					nt.InstanceID = old.InstanceID
					if err := db.UpsertTask(tx, nt); err != nil {
						return err
					}
					if err := freezeIfDefinitionChanged(tx, nt); err != nil {
						return err
					}
					payload := map[string]any{
						"status": nt.Status, "from_status": old.Status,
						"instance_id": nt.InstanceID, "iteration_id": nt.IterationID,
					}
					if old.IterationID != nt.IterationID {
						payload["previous_iteration_id"] = old.IterationID
					}
					_ = db.AppendEvent(tx, pc.ID(), agent, "task_updated", nt.ID, payload)
				}
			}
			for _, old := range existing {
				if inPlan[old.ID] {
					continue
				}
				if machine.PreserveOnPlanApply(old.Status) {
					preserved++
					bumpIterationCount(iterationCounts, old.IterationID, "preserved")
					// blocked 题不在蓝图里：保留现场，但提示可 cancel / 并入别题
					if old.Status == "blocked" {
						staleBlocked = append(staleBlocked, old.ID)
					}
					continue
				}
				if err := retireIdleRemoved(tx, pc.ID(), old, agent); err != nil {
					return err
				}
				removed++
				bumpIterationCount(iterationCounts, old.IterationID, "removed")
			}
			if len(staleBlocked) > 0 {
				_ = db.AppendEvent(tx, pc.ID(), agent, "plan_stale_blocked", "", map[string]any{
					"task_ids": staleBlocked,
					"hint":     "maw task cancel <id> --reason removed_from_plan --superseded-by <new>",
				})
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
			"pinned_runs":       pinned,
			"iterations":        iterationCounts,
			"iteration_created": iterationCreatedIDs,
			"iteration_updated": iterationUpdatedIDs,
		})
	})
	if err != nil {
		return nil, err
	}
	var out []*db.Task
	var outIterations []*db.Iteration
	_ = withGlobalRead(pc.GlobalDB, func(tx *sql.Tx) error {
		out, err = db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		outIterations, err = db.ListIterations(tx, pc.ID())
		return err
	})
	html, _ := refreshBoard(pc)
	res := Result{
		"ok": true, "project_id": pc.ID(), "project_name": pc.Project.Name,
		"reset":       reset,
		"main_branch": pc.Config.Project.MainBranch,
		"created":     created, "updated": updated, "preserved": preserved, "removed": removed,
		"pinned_runs": pinned,
		"iterations":  outIterations, "iteration_counts": iterationCounts,
		"iteration_created": iterationCreatedIDs, "iteration_updated": iterationUpdatedIDs,
		"tasks": out, "board": html,
		"next": i18n.T("next.after_apply"),
	}
	if len(identityNotes) > 0 {
		res["identity_notes"] = identityNotes
	}
	if len(staleBlocked) > 0 {
		res["stale_blocked"] = staleBlocked
		res["stale_blocked_hint"] = i18n.T("next.stale_blocked")
	}
	return res, nil
}

// mergePlanFields 把蓝图里的「计划字段」刷到已有保留态任务上，不碰领取/进度字段。
func mergePlanFields(dst, src *db.Task) {
	dst.Title = src.Title
	dst.Priority = src.Priority
	dst.MergeOrder = src.MergeOrder
	dst.Lane = src.Lane
	dst.Deps = src.Deps
	dst.WriteScope = src.WriteScope
	dst.ReadScope = src.ReadScope
	dst.Acceptance = src.Acceptance
	dst.IterationID = src.IterationID
	if src.Extra != nil {
		if dst.Extra == nil {
			dst.Extra = map[string]any{}
		}
		for k, v := range src.Extra {
			dst.Extra[k] = v
		}
	}
}

// planIterationCount 是单个迭代在本波 apply 里新建/更新/保留/移除的任务计数。
type planIterationCount struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Preserved int `json:"preserved"`
	Removed   int `json:"removed"`
}

func bumpIterationCount(counts map[string]*planIterationCount, iterationID, field string) {
	if iterationID == "" {
		return
	}
	c := counts[iterationID]
	if c == nil {
		c = &planIterationCount{}
		counts[iterationID] = c
	}
	switch field {
	case "created":
		c.Created++
	case "updated":
		c.Updated++
	case "preserved":
		c.Preserved++
	case "removed":
		c.Removed++
	}
}

// upsertPlanIterations 确保蓝图声明的迭代存在。
//
// 新 id 直接建档；已存在的 id 只刷新计划字段（名称/目标/依赖），
// 不覆盖生命周期状态与已记录的合并提交，避免重复 apply 把 running/merged 打回 planning。
func upsertPlanIterations(tx *sql.Tx, projectID string, specs []*db.Iteration) (createdIDs, updatedIDs []string, err error) {
	for _, spec := range specs {
		spec.ProjectID = projectID
		existing, err := db.GetIteration(tx, projectID, spec.ID)
		if err != nil {
			return nil, nil, err
		}
		if existing == nil {
			if err := db.UpsertIteration(tx, spec); err != nil {
				return nil, nil, err
			}
			createdIDs = append(createdIDs, spec.ID)
			continue
		}
		existing.Name = spec.Name
		existing.Goal = spec.Goal
		if existing.TargetBranch == "" && spec.TargetBranch != "" {
			existing.TargetBranch = spec.TargetBranch
		}
		if existing.Extra == nil {
			existing.Extra = map[string]any{}
		}
		for k, v := range spec.Extra {
			existing.Extra[k] = v
		}
		existing.UpdatedAt = ""
		if err := db.UpsertIteration(tx, existing); err != nil {
			return nil, nil, err
		}
		updatedIDs = append(updatedIDs, spec.ID)
	}
	return createdIDs, updatedIDs, nil
}
