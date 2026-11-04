package app

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
)

// IterationView 是迭代列表项：保留完整迭代字段，并附带任务计数与本地化状态。
type IterationView struct {
	db.Iteration
	TaskCount   int64  `json:"task_count"`
	StatusLabel string `json:"status_label"`
}

// IterationCreateInput 是 `maw iteration create` 的入参。
type IterationCreateInput struct {
	ID           string
	Name         string
	Goal         string
	TargetBranch string
	Status       string
	DryRun       bool
}

func iterationStatusLabel(status string) string {
	return i18n.T("iteration.status." + status)
}

func normalizeIterationStatusArg(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", db.IterationPlanning, "规划中":
		return db.IterationPlanning, nil
	case db.IterationRunning, "执行中":
		return db.IterationRunning, nil
	case db.IterationFinishing, "收尾中":
		return db.IterationFinishing, nil
	case db.IterationMerged, "已合入":
		return db.IterationMerged, nil
	case db.IterationAbandoned, "已放弃":
		return db.IterationAbandoned, nil
	default:
		return "", i18n.E("err.iteration_status", "next.iteration_list", strings.TrimSpace(raw))
	}
}

func newIterationID() (string, error) {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "it_" + hex.EncodeToString(buf[:]), nil
}

// IterationList 列出工程的迭代与每迭代任务数。
func IterationList(cwd string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	var views []*IterationView
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		iterations, err := db.ListIterations(tx, pc.ID())
		if err != nil {
			return err
		}
		counts, err := db.IterationTaskCounts(tx, pc.ID())
		if err != nil {
			return err
		}
		for _, it := range iterations {
			views = append(views, &IterationView{
				Iteration: *it, TaskCount: counts[it.ID],
				StatusLabel: iterationStatusLabel(it.Status),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return Result{
		"ok": true, "project_id": pc.ID(), "iterations": views,
		"next": i18n.T("next.iteration_list"),
	}, nil
}

// IterationCreate 建立迭代；未给 id 时生成随机 id，未给目标分支时继承工程主干。
func IterationCreate(cwd string, in IterationCreateInput) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, i18n.E("err.iteration_name", "next.iteration_list")
	}
	status, err := normalizeIterationStatusArg(in.Status)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(in.TargetBranch)
	if target == "" {
		target = strings.TrimSpace(pc.Config.Project.MainBranch)
	}
	if target == "" {
		target = "main"
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id, err = newIterationID()
		if err != nil {
			return nil, err
		}
	}
	it := &db.Iteration{
		ProjectID: pc.ID(), ID: id, Name: name, Goal: strings.TrimSpace(in.Goal),
		Status: status, TargetBranch: target,
	}
	if in.DryRun {
		return Result{
			"ok": true, "dry_run": true, "project_id": pc.ID(), "iteration": it,
			"next": i18n.T("next.iteration_list"),
		}, nil
	}
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		existing, err := db.GetIteration(tx, pc.ID(), id)
		if err != nil {
			return err
		}
		if existing != nil {
			return i18n.E("err.iteration_exists", "next.iteration_list", id)
		}
		if err := db.UpsertIteration(tx, it); err != nil {
			return err
		}
		actor, _ := identity.Resolve(cwd, "")
		if actor == "" {
			actor = "orchestrator"
		}
		return db.AppendEvent(tx, pc.ID(), actor, "iteration_created", "", map[string]any{
			"iteration_id": it.ID, "name": it.Name, "status": it.Status,
			"target_branch": it.TargetBranch,
		})
	})
	if err != nil {
		return nil, err
	}
	html, _ := refreshBoard(pc)
	return Result{
		"ok": true, "project_id": pc.ID(), "iteration": it,
		"board": html, "next": i18n.T("next.iteration_show", it.ID),
	}, nil
}

// IterationShow 展示单个迭代的状态与任务 id 列表。
func IterationShow(cwd, id string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	var it *db.Iteration
	var tasks []*db.Task
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var err error
		it, err = db.GetIteration(tx, pc.ID(), id)
		if err != nil || it == nil {
			return err
		}
		all, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		for _, t := range all {
			if t.IterationID == id {
				tasks = append(tasks, t)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if it == nil {
		return nil, i18n.E("err.unknown_iteration", "next.iteration_list", id)
	}
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return Result{
		"ok": true, "project_id": pc.ID(), "iteration": it,
		"status_label": iterationStatusLabel(it.Status),
		"task_count":   len(tasks), "task_ids": ids, "tasks": tasks,
		"next": i18n.T("next.iteration_list"),
	}, nil
}

// IterationCloseInput 是 `maw iteration close` 的入参。
type IterationCloseInput struct {
	ID          string
	Status      string
	MergeCommit string
	Force       bool
	DryRun      bool
}

// normalizeCloseStatus 只接受两种收口去向：已合入（默认）与已放弃。
//
// 收口不是「随便改状态」：规划/执行/收尾之间的推进由 create 与后续流程决定，
// 这里限定终态，避免借 close 绕过状态机。
func normalizeCloseStatus(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", db.IterationMerged, "已合入":
		return db.IterationMerged, nil
	case db.IterationAbandoned, "已放弃":
		return db.IterationAbandoned, nil
	default:
		return "", i18n.EN("err.iteration_close_status", []any{strings.TrimSpace(raw)},
			"next.iteration_close_usage")
	}
}

// IterationClose 显式收口一个迭代：把 running/finishing 推到 merged 或 abandoned。
//
// 三条守卫：
//  1. 状态机——非法迁移直接拒绝，不改库（终态不可回退，也不允许从 planning 直接收口）；
//  2. 任务完整性——迭代内仍有非终态任务时默认拒绝并列出任务 id，--force 才放行；
//  3. 幂等——已经是目标状态时返回 changed=false，不刷 updated_at、不追加第二条事件。
func IterationClose(cwd string, in IterationCloseInput) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, i18n.E("err.iteration_id_required", "next.iteration_list")
	}
	target, err := normalizeCloseStatus(in.Status)
	if err != nil {
		return nil, err
	}

	var (
		out     *db.Iteration
		from    string
		changed bool
		open    []string
	)
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		it, err := db.GetIteration(tx, pc.ID(), id)
		if err != nil {
			return err
		}
		if it == nil {
			return i18n.E("err.unknown_iteration", "next.iteration_list", id)
		}
		from = it.Status
		if in.DryRun || it.Status == target {
			out = it
			return nil
		}
		if !db.CanTransitionIteration(it.Status, target) {
			return i18n.EN("err.iteration_transition", []any{it.Status, target},
				"next.iteration_show", id)
		}
		// 未完成任务清单始终算出来：强制收口时要用它回报「跳过了什么」，
		// 不能因为加了 --force 就悄悄吞掉这条证据。
		all, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		for _, t := range all {
			if t == nil || t.IterationID != id {
				continue
			}
			if !model.TaskStatus(t.Status).Terminal() {
				open = append(open, t.ID)
			}
		}
		if !in.Force && len(open) > 0 {
			return i18n.EN("err.iteration_open_tasks", []any{id, strings.Join(open, ", ")},
				"next.iteration_close_force", id)
		}
		updated, didChange, err := db.SetIterationStatus(tx, pc.ID(), id, target, in.MergeCommit)
		if err != nil {
			return err
		}
		out, changed = updated, didChange
		if !didChange {
			return nil
		}
		actor, _ := identity.Resolve(cwd, "")
		if actor == "" {
			actor = "orchestrator"
		}
		return db.AppendEvent(tx, pc.ID(), actor, "iteration_status_changed", "", map[string]any{
			"iteration_id": id, "from": from, "to": target,
			"merge_commit": strings.TrimSpace(in.MergeCommit), "forced": in.Force,
		})
	})
	if err != nil {
		return nil, err
	}
	if in.DryRun {
		return Result{
			"ok": true, "dry_run": true, "project_id": pc.ID(), "iteration": out,
			"from": from, "to": target, "next": i18n.T("next.iteration_show", id),
		}, nil
	}
	html, _ := refreshBoard(pc)
	res := Result{
		"ok": true, "project_id": pc.ID(), "iteration": out,
		"from": from, "to": out.Status, "changed": changed,
		"status_label": iterationStatusLabel(out.Status),
		"board":        html, "next": i18n.T("next.iteration_show", id),
	}
	if in.Force && len(open) > 0 {
		// 强制收口时把被跳过的未完成任务写进结果，别让它们悄悄消失。
		res["forced_open_tasks"] = open
	}
	return res, nil
}
