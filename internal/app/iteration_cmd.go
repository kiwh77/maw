package app

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"strings"

	"github.com/kiwh77/maw/internal/db"
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
