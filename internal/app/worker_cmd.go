package app

import (
	"database/sql"
	"path/filepath"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/machine"
	"github.com/kiwh77/maw/internal/schedule"
	"github.com/kiwh77/maw/internal/scope"
)

func Next(cwd, agentFlag string, dryRun bool) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	var task *db.Task
	var branch, wtRel string
	idempotent := false
	var who string
	allocated := false
	nameWarn := ""
	// Short TX: claim only. Git worktree + task card happen outside so other
	// agents are not blocked on SQLITE_BUSY for seconds.
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		agents, err := db.ListAgents(tx, pc.ID())
		if err != nil {
			return err
		}
		ids := agentIDs(agents)
		who, allocated, err = identity.ResolveOrAllocate(cwd, agentFlag, pc.Project.Name, ids)
		if err != nil {
			return err
		}
		nameWarn = workerNameWarn(who, pc)
		tasks, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.Owner == who && machine.ActiveOwner[t.Status] {
				task = t
				idempotent = true
				return nil
			}
		}
		// 不再按 max_parallel 卡槽：只要有可激活 ready，来拉的 Agent 即可领取。
		// 写盘重叠仍由 schedule.CanActivate 串行；max_parallel 仅作看板参考。
		_ = expireStaleAgents(tx, pc)
		if err := promoteReady(tx, pc.ID()); err != nil {
			return err
		}
		tasks, err = db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		shared, err := db.ListSharedFiles(tx, pc.ID())
		if err != nil {
			return err
		}
		ready := schedule.PickNextReadyForShared(tasks, who, shared)
		if ready == nil {
			return errx.New("no ready tasks", "run `maw board`；检查 deps/写盘优先级，或 `maw plan apply`")
		}
		if dryRun {
			task = ready
			return nil
		}
		t := ready
		if err := machine.AssertTransition(t.Status, "in_progress"); err != nil {
			return err
		}
		_ = db.AppendEvent(tx, pc.ID(), who, "claimed", t.ID, nil)
		branch = "agent/" + who + "/" + t.ID
		wtRel = filepath.Join(".worktrees", who)
		from := t.Status
		t.Status = "in_progress"
		t.Owner = who
		t.Branch = branch
		t.Worktree = wtRel
		t.Heartbeat = db.UTCNow()
		t.Revision++
		t.Attempts++
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", t.ID, map[string]any{"from": from, "to": "in_progress"})
		if err := bindAgent(tx, pc.ID(), who, "worker", "working", t.ID, wtRel, branch); err != nil {
			return err
		}
		_ = db.AppendEvent(tx, pc.ID(), who, "agent_registered", t.ID, map[string]any{"status": "working"})
		task = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	if idempotent {
		_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			return bindAgent(tx, pc.ID(), who, "worker", "working", task.ID, task.Worktree, task.Branch)
		})
		res := Result{
			"ok": true, "idempotent": true, "task": task, "agent": who,
			"project_id": pc.ID(), "worktree": filepath.Join(pc.Repo, task.Worktree),
			"next": "cd " + filepath.Join(pc.Repo, task.Worktree) + " && continue; maw progress -m '...'; maw submit",
		}
		attachAgentNameMeta(res, who, allocated, nameWarn)
		return res, nil
	}
	if dryRun {
		res := Result{"ok": true, "dry_run": true, "task": task, "agent": who, "project_id": pc.ID()}
		attachAgentNameMeta(res, who, allocated, nameWarn)
		return res, nil
	}
	wt := filepath.Join(pc.Repo, wtRel)
	base, err := gitops.EnsureBranchBase(pc.Repo, pc.Config.Project.MainBranch)
	if err != nil {
		_ = rollbackClaim(pc, who, task.ID)
		return nil, err
	}
	if err := gitops.EnsureAgentWorktree(pc.Repo, wt, branch, base); err != nil {
		_ = rollbackClaim(pc, who, task.ID)
		return nil, err
	}
	var allTasks []*db.Task
	_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		allTasks, e = db.ListTasks(tx, pc.ID())
		return e
	})
	if err := writeTaskCardWithDeps(wt, task, who, allTasks); err != nil {
		// Per-agent worktree is reused across tasks; do not wipe it on card write failure.
		_ = rollbackClaim(pc, who, task.ID)
		return nil, err
	}
	_, _ = refreshBoard(pc)
	res := Result{
		"ok": true, "task": task, "worktree": wt, "branch": branch, "agent": who,
		"project_id": pc.ID(), "registered": true,
		"env":  map[string]string{"MAW_AGENT": who, "MAW_TASK": task.ID},
		"next": "已注册为 " + who + "。cd " + wt + " 后执行；里程碑 maw progress，完成 maw submit",
	}
	attachAgentNameMeta(res, who, allocated, nameWarn)
	return res, nil
}

// rollbackClaim releases a task claimed in DB when worktree/card setup fails.
func rollbackClaim(pc *projCtx, who, taskID string) error {
	return db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return err
		}
		if t.Owner != who || t.Status != "in_progress" {
			return nil
		}
		if err := machine.AssertTransition(t.Status, "ready"); err != nil {
			return err
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
		_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", taskID, map[string]any{"from": "in_progress", "to": "ready", "reason": "claim_setup_failed"})
		if err := db.ClearAgentTask(tx, pc.ID(), who); err != nil {
			return err
		}
		return promoteReady(tx, pc.ID())
	})
}

func Register(cwd, agentFlag, role string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	var who string
	allocated := false
	nameWarn := ""
	var taskID, wt, branch, status string
	status = "idle"
	if role == "" {
		role = "worker"
	}
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		agents, err := db.ListAgents(tx, pc.ID())
		if err != nil {
			return err
		}
		who, allocated, err = identity.ResolveOrAllocate(cwd, agentFlag, pc.Project.Name, agentIDs(agents))
		if err != nil {
			return err
		}
		if r := identity.Role(who, pc.Config); r == "integrator" || r == "reviewer" {
			role = r
		}
		nameWarn = workerNameWarn(who, pc)
		tasks, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.Owner == who && machine.ActiveOwner[t.Status] {
				taskID, wt, branch = t.ID, t.Worktree, t.Branch
				status = "working"
				break
			}
		}
		if err := bindAgent(tx, pc.ID(), who, role, status, taskID, wt, branch); err != nil {
			return err
		}
		return db.AppendEvent(tx, pc.ID(), who, "agent_registered", taskID, map[string]any{"role": role, "status": status})
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	next := "export MAW_AGENT=" + who + " && maw next 领取任务"
	if taskID != "" {
		next = "继续执行 " + taskID + "；maw progress / maw submit"
	}
	res := Result{
		"ok": true, "agent": who, "role": role, "status": status,
		"project_id": pc.ID(), "task_id": taskID, "worktree": wt, "registered": true,
		"env": map[string]string{"MAW_AGENT": who}, "next": next,
	}
	attachAgentNameMeta(res, who, allocated, nameWarn)
	return res, nil
}

func Unregister(cwd, agentFlag string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, agentFlag)
	if err != nil {
		return nil, err
	}
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		if err := db.ClearAgentTask(tx, pc.ID(), who); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE agents SET status = 'offline', last_seen = ? WHERE project_id = ? AND id = ?`, db.UTCNow(), pc.ID(), who)
		if err != nil {
			return err
		}
		return db.AppendEvent(tx, pc.ID(), who, "agent_unregistered", "", nil)
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	return Result{"ok": true, "agent": who, "status": "offline", "project_id": pc.ID(), "next": "重新加入时 maw register"}, nil
}

func AgentsList(cwd string) (Result, error) {
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	projectID := ""
	if pc, err := resolveProject(cwd); err == nil {
		projectID = pc.ID()
	}
	var agents []*db.Agent
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		var e error
		agents, e = db.ListAgentsFiltered(tx, projectID, "")
		return e
	})
	if err != nil {
		return nil, err
	}
	return Result{"ok": true, "agents": agents, "project_id": projectID, "next": "maw board --serve 查看全局看板"}, nil
}

func Progress(cwd, message, taskID string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	if taskID == "" {
		taskID, err = inferTaskID(cwd)
		if err != nil {
			return nil, err
		}
	}
	var hb string
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return errx.New("unknown task "+taskID, "maw task list")
		}
		if err := identity.RequireOwner(who, t); err != nil {
			return err
		}
		t.Heartbeat = db.UTCNow()
		t.Revision++
		hb = t.Heartbeat
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		_ = bindAgent(tx, pc.ID(), who, "worker", "working", t.ID, t.Worktree, t.Branch)
		return db.AppendEvent(tx, pc.ID(), who, "progress", taskID, map[string]any{"message": message})
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	return Result{"ok": true, "task_id": taskID, "agent": who, "heartbeat": hb, "project_id": pc.ID(), "next": "继续或 maw submit"}, nil
}

func Submit(cwd, taskID string, dryRun bool) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	if taskID == "" {
		taskID, err = inferTaskID(cwd)
		if err != nil {
			return nil, err
		}
	}
	wt, err := home.WorktreeRoot(cwd)
	if err != nil {
		wt = pc.Repo
	}
	files, err := gitops.ChangedFiles(wt, pc.Config.Project.MainBranch)
	if err != nil {
		return nil, err
	}
	files = filterScopeFiles(files)

	var fromStatus string
	var writeScope []string
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return errx.New("unknown task "+taskID, "maw task list")
		}
		if err := identity.RequireOwner(who, t); err != nil {
			return err
		}
		if t.Status != "in_progress" && t.Status != "self_check" && t.Status != "conflict" {
			return errx.New("task "+taskID+" status "+t.Status+" cannot submit", "only in_progress tasks can be submitted")
		}
		fromStatus = t.Status
		writeScope = t.WriteScope
		if bad := scope.OutOfScopeFiles(files, t.WriteScope); len(bad) > 0 {
			_ = db.AppendEvent(tx, pc.ID(), who, "scope_violation", taskID, map[string]any{
				"files": files, "out_of_scope": bad, "write_scope": t.WriteScope,
			})
			return scope.AssertFilesInScope(files, t.WriteScope, taskID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if dryRun {
		return Result{"ok": true, "dry_run": true, "files": files, "status": fromStatus}, nil
	}
	// 未 commit 的 staged/unstaged 不能进 merging：否则 merge rebase 必因 dirty 掉 conflict
	if err := gitops.AssertWorkingTreeClean(wt); err != nil {
		return nil, err
	}

	auto := pc.Config.Approvals.Review == "auto"
	var gateResults []gitops.GateResult
	if auto && len(pc.Config.Gates.Commands) > 0 {
		gateCwd := wt
		var gateErr error
		gateResults, gateErr = gitops.RunGatesDetailed(pc.Config.Gates.Commands, gateCwd, pc.Config.Gates.TimeoutSec)
		if gateErr != nil {
			msg := gateErr.Error()
			if e, ok := gateErr.(*errx.Error); ok {
				msg = e.Msg
			}
			envFail := gitops.IsEnvFailure(msg)
			_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
				ev := map[string]any{"error": msg, "gates": gateResults, "env_failure": envFail, "phase": "submit"}
				typ := "gate_failed"
				if envFail {
					typ = "gate_env_failed"
				}
				_ = db.AppendEvent(tx, pc.ID(), who, typ, taskID, ev)
				if !envFail {
					t, _ := db.GetTask(tx, pc.ID(), taskID)
					if t != nil {
						t.Attempts++
						t.Revision++
						_ = db.UpsertTask(tx, t)
					}
				}
				return nil
			})
			return nil, gateErr
		}
	}

	var status string
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return errx.New("unknown task "+taskID, "maw task list")
		}
		if err := identity.RequireOwner(who, t); err != nil {
			return err
		}
		from := t.Status
		if err := machine.AssertTransition(t.Status, "in_review"); err != nil {
			return err
		}
		t.Status = "in_review"
		t.Heartbeat = db.UTCNow()
		t.Revision++
		_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", taskID, map[string]any{
			"from": from, "to": "in_review", "files": files,
		})
		if auto {
			if err := machine.AssertTransition(t.Status, "merging"); err != nil {
				return err
			}
			from2 := t.Status
			t.Status = "merging"
			t.Revision++
			evidence := map[string]any{
				"files": files, "file_count": len(files), "write_scope": writeScope,
				"gates": gateResults, "review": "auto",
			}
			_ = db.AppendEvent(tx, pc.ID(), "system", "review_auto_approved", taskID, evidence)
			_ = db.AppendEvent(tx, pc.ID(), "system", "status_changed", taskID, map[string]any{
				"from": from2, "to": "merging",
			})
		}
		status = t.Status
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		return bindAgent(tx, pc.ID(), who, "worker", "working", t.ID, t.Worktree, t.Branch)
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	next := "maw merge --yes"
	if !auto {
		next = "maw review --approve " + taskID
	}
	return Result{"ok": true, "task_id": taskID, "agent": who, "files": files, "status": status, "project_id": pc.ID(), "next": next}, nil
}

func Review(cwd, taskID string, approve bool, message string) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	who, err := identity.Resolve(cwd, "")
	if err != nil {
		return nil, err
	}
	var files []string
	wt, werr := home.WorktreeRoot(cwd)
	if werr != nil {
		wt = pc.Repo
	}
	if fl, ferr := gitops.ChangedFiles(wt, pc.Config.Project.MainBranch); ferr == nil {
		files = filterScopeFiles(fl)
	}
	var status string
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return errx.New("unknown task "+taskID, "")
		}
		if t.Owner == who {
			return errx.New("author cannot review own task", "use another MAW_AGENT")
		}
		if t.Status != "in_review" {
			return errx.New("task not in_review ("+t.Status+")", "")
		}
		from := t.Status
		if approve {
			if err := machine.AssertTransition(t.Status, "merging"); err != nil {
				return err
			}
			t.Status = "merging"
			_ = db.AppendEvent(tx, pc.ID(), who, "review_result", taskID, map[string]any{
				"result": "approve", "message": message,
				"files": files, "file_count": len(files),
			})
			_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", taskID, map[string]any{
				"from": from, "to": "merging",
			})
		} else {
			if err := machine.AssertTransition(t.Status, "in_progress"); err != nil {
				return err
			}
			t.Status = "in_progress"
			_ = db.AppendEvent(tx, pc.ID(), who, "review_result", taskID, map[string]any{
				"result": "request_changes", "message": message,
			})
			_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", taskID, map[string]any{
				"from": from, "to": "in_progress",
			})
		}
		t.Revision++
		status = t.Status
		return db.UpsertTask(tx, t)
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	next := "author: fix and maw submit"
	if approve {
		next = "maw merge --yes"
	}
	return Result{"ok": true, "task_id": taskID, "status": status, "project_id": pc.ID(), "next": next}, nil
}
