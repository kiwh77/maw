package app

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
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
	who, allocated, nameWarn, err := prepareWorker(pc, cwd, agentFlag)
	if err != nil {
		return nil, err
	}

	// 续做：自己仍持有的任务直接返回，不重复领取（与 ClaimExecution 的幂等重放同义）。
	task, err := activeTaskOf(pc, who)
	if err != nil {
		return nil, err
	}
	if task != nil {
		// 卡在 claimed 的续做要把状态收敛到 in_progress，否则 submit 会拒绝
		// （它只认 in_progress／self_check／conflict），任务就永远停在中途。
		if err := resumeClaim(pc, task, who); err != nil {
			return nil, err
		}
		if task, err = getTask(pc, task.ID); err != nil {
			return nil, err
		}
		_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			return bindAgent(tx, pc.ID(), who, "worker", "working", task.ID, task.Worktree, task.Branch)
		})
		res := Result{
			"ok": true, "idempotent": true, "task": task, "agent": who,
			"project_id": pc.ID(), "worktree": filepath.Join(pc.Repo, task.Worktree),
			"limits": limitsOf(pc),
			"next":   i18n.T("next.continue_worktree", filepath.Join(pc.Repo, task.Worktree)),
		}
		attachAgentNameMeta(res, who, allocated, nameWarn)
		return res, nil
	}

	wtRel := filepath.Join(".worktrees", who)
	if dryRun {
		preview, err := pickClaimTarget(pc, who)
		if err != nil {
			return nil, err
		}
		res := Result{"ok": true, "dry_run": true, "task": preview, "agent": who,
			"project_id": pc.ID(), "limits": limitsOf(pc)}
		attachAgentNameMeta(res, who, allocated, nameWarn)
		return res, nil
	}

	// 唯一的领取决策点：ready→claimed 条件写入。
	// 与受管 ClaimExecution 抢同一道题时，谁的条件先命中谁赢；输的一方看到 status
	// 已变，重新选题（题本来就少，重试有界）或报冲突，不会出现两个持有者。
	task, err = claimManual(pc, who, wtRel)
	if err != nil {
		return nil, err
	}

	wt, err := ensureClaimWorktree(pc, wtRel, task.Branch, true)
	if err != nil {
		_ = rollbackClaim(pc, who, task.ID)
		return nil, err
	}
	// 目录准备好了才推进到 in_progress：claimed 表示「已归属、尚未开工」，
	// 两步之间的空档由 rollbackClaim 负责退回 ready。
	if err := advanceClaim(pc, task.ID, who); err != nil {
		_ = rollbackClaim(pc, who, task.ID)
		return nil, err
	}
	task, err = getTask(pc, task.ID)
	if err != nil {
		_ = rollbackClaim(pc, who, task.ID)
		return nil, err
	}
	allTasks, err := listTasks(pc)
	if err != nil {
		_ = rollbackClaim(pc, who, task.ID)
		return nil, err
	}
	if err := writeTaskCardWithDeps(wt, task, who, allTasks); err != nil {
		// Per-agent worktree is reused across tasks; do not wipe it on card write failure.
		_ = rollbackClaim(pc, who, task.ID)
		return nil, err
	}
	_, _ = refreshBoard(pc)
	res := Result{
		"ok": true, "task": task, "worktree": wt, "branch": task.Branch, "agent": who,
		"project_id": pc.ID(), "registered": true, "limits": limitsOf(pc),
		"env":  map[string]string{"MAW_AGENT": who, "MAW_TASK": task.ID},
		"next": i18n.T("next.registered", who, wt),
	}
	attachAgentNameMeta(res, who, allocated, nameWarn)
	return res, nil
}

// limitsOf 把两个容易混淆的上限分开放。
//
// max_parallel 是看板参考值：它只影响展示的建议并行度，从不参与领取决策
// （领取是否放行由依赖、写盘序位与共享文件决定）。
// 受管执行的硬上限是 ClaimExecution 请求里的 Capacity.ManagedLimit，由派发方传入；
// 手工 maw next 不是受管执行，因此不受它约束，这里如实报 0 而不是拿 max_parallel 顶替。
func limitsOf(pc *projCtx) map[string]any {
	ref := 0
	if pc != nil && pc.Project != nil {
		ref = pc.Project.MaxParallel
	}
	return map[string]any{
		"max_parallel_reference": ref,
		"managed_limit":          0,
		"note":                   "max_parallel 仅看板参考；受管执行上限由 ClaimExecution 的 Capacity.ManagedLimit 决定",
	}
}

// manualClaimAttempts 是一次 maw next 在竞争失败后重新选题的次数上限。
// 有界重试：题被抢走就该换一道，而不是无限重试同一道。
const manualClaimAttempts = 3

// errClaimLost 是「条件写入没命中」的哨兵：同一任务已被别的领取者抢先。
var errClaimLost = errors.New("claim: 任务已被他人领取")

// claimLostError 带上当时的持有者：只说「冲突」分不清是自己重复领取还是被人抢先。
type claimLostError struct {
	TaskID string
	Owner  string
}

func (e *claimLostError) Error() string {
	return "claim: 任务 " + e.TaskID + " 已被 " + e.Owner + " 领取"
}

// Is 让 errors.Is(err, errClaimLost) 成立，调用方不必关心持有者细节。
func (e *claimLostError) Is(target error) bool { return target == errClaimLost }

// prepareWorker 解析身份并做领取前的编排维护：过期心跳下线、backlog→ready 提升。
//
// 这两步刻意放在领取事务之外：它们是全局维护动作，把一个「可能扫全表」的动作塞进
// 领取事务会拉长写锁持有时间，让别的 Agent 撞 SQLITE_BUSY。
func prepareWorker(pc *projCtx, cwd, agentFlag string) (string, bool, string, error) {
	var who string
	var allocated bool
	var nameWarn string
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		agents, err := db.ListAgents(tx, pc.ID())
		if err != nil {
			return err
		}
		who, allocated, err = identity.ResolveOrAllocate(cwd, agentFlag, pc.Project.Name, agentIDs(agents))
		if err != nil {
			return err
		}
		nameWarn = workerNameWarn(who, pc)
		_ = expireStaleAgents(tx, pc)
		return promoteReady(tx, pc.ID())
	})
	if err != nil {
		return "", false, "", err
	}
	return who, allocated, nameWarn, nil
}

// activeTaskOf 返回该 Agent 仍持有的活跃任务；没有则返回 nil。
func activeTaskOf(pc *projCtx, who string) (*db.Task, error) {
	var out *db.Task
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		tasks, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.Owner == who && machine.ActiveOwner[t.Status] {
				out = t
				return nil
			}
		}
		return nil
	})
	return out, err
}

// ensureClaimWorktree 准备本次领取的工作目录。
//
// resetExisting=true 用于全新领取：目录若还在（同一 Agent 上一题的残留），必须重置到
// 主干基线，否则本卡 diff 会带上上一题的改动、submit 直接报越界。
// resetExisting=false 用于续做：目录在就原样用，绝不 reset——那会丢掉未提交的工作。
func ensureClaimWorktree(pc *projCtx, wtRel, branch string, resetExisting bool) (string, error) {
	wt := filepath.Join(pc.Repo, wtRel)
	if !resetExisting {
		if st, err := os.Stat(wt); err == nil && st.IsDir() {
			return wt, nil
		}
	}
	base, err := gitops.EnsureBranchBase(pc.Repo, pc.Config.Project.MainBranch)
	if err != nil {
		return "", err
	}
	if err := gitops.EnsureAgentWorktree(pc.Repo, wt, branch, base); err != nil {
		return "", err
	}
	return wt, nil
}

// resumeClaim 把续做时卡在 claimed 的任务收敛到 in_progress。
//
// 只在目录已存在时直接推进；目录不在（崩溃发生在建目录之前）才补建。
// 不这么做的话，任务会一直停在 claimed：submit 只认 in_progress／self_check／conflict。
func resumeClaim(pc *projCtx, task *db.Task, who string) error {
	if task == nil || task.Status != string(model.TaskClaimed) {
		return nil
	}
	wtRel := task.Worktree
	if wtRel == "" {
		wtRel = filepath.Join(".worktrees", who)
	}
	if _, err := ensureClaimWorktree(pc, wtRel, task.Branch, false); err != nil {
		return err
	}
	return advanceClaim(pc, task.ID, who)
}

// claimManual 用一个有界循环把「选题 + 条件领取」做成原子动作。
//
// 竞争失败只发生在别的领取者恰好同时赢了同一道题：此时那道题已经不是 ready，
// 下一轮选题自然不会重复挑到它。非竞争错误（依赖不满足、没有 ready）直接上抛。
func claimManual(pc *projCtx, who, wtRel string) (*db.Task, error) {
	var lost error
	for attempt := 0; attempt < manualClaimAttempts; attempt++ {
		t, err := claimOnce(pc, who, wtRel)
		if err == nil {
			return t, nil
		}
		if !errors.Is(err, errClaimLost) {
			return nil, err
		}
		lost = err
	}
	var detail *claimLostError
	if errors.As(lost, &detail) {
		return nil, i18n.E("err.owner_mismatch", "next.rerun_next", detail.TaskID, detail.Owner, who)
	}
	return nil, lost
}

// claimOnce 选题并以「ready + revision」为条件把任务写成 claimed。
//
// 为什么不是先读再 UpsertTask：UpsertTask 是整行覆盖，写入时不看旧状态，两个领取者
// 都能成功；条件 UPDATE 把「还是我读到的那一行」交给 SQLite 判定，只会有一个命中。
//
// 选题与写入刻意分成两个短事务（pickClaimTarget 只读、commitClaim 只写）：
// 中间那段空档就是真实存在于「手工 next 与 managed ClaimExecution 之间」的竞争窗口，
// 由条件写入负责兜住；把它遮起来反而会让测试无法复现真正的竞争。
func claimOnce(pc *projCtx, who, wtRel string) (*db.Task, error) {
	t, err := pickClaimTarget(pc, who)
	if err != nil {
		return nil, err
	}
	return commitClaim(pc, who, wtRel, t)
}

// pickClaimTarget 只读选题：依赖／写盘／亲和策略全部沿用同一套调度规则。
func pickClaimTarget(pc *projCtx, who string) (*db.Task, error) {
	var out *db.Task
	err := db.WithReadTx(pc.GlobalDB, func(tx *sql.Tx) error {
		tasks, err := db.ListTasks(tx, pc.ID())
		if err != nil {
			return err
		}
		shared, err := db.ListSharedFiles(tx, pc.ID())
		if err != nil {
			return err
		}
		pick := schedule.PickNextReadyForShared(tasks, who, shared)
		if pick == nil {
			return i18n.E("err.no_ready", "next.board_deps")
		}
		out = pick
		return nil
	})
	return out, err
}

// commitClaim 把选定任务以「仍是 ready 且 revision 未变」为条件写成 claimed。
//
// 条件不成立时返回 claimLostError（带当前持有者），调用方据此重新选题；
// 绝不做「先读后整行覆盖」，那正是两个 Agent 同时持有同一道题的来源。
func commitClaim(pc *projCtx, who, wtRel string, t *db.Task) (*db.Task, error) {
	if t == nil {
		return nil, i18n.E("err.no_ready", "next.board_deps")
	}
	var claimed *db.Task
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		branch := "agent/" + who + "/" + t.ID
		res, err := tx.Exec(`
UPDATE tasks SET status = 'claimed', owner = ?, branch = ?, worktree = ?, heartbeat = ?,
  attempts = attempts + 1, revision = revision + 1, blocked_reason = ''
WHERE project_id = ? AND id = ? AND status = 'ready' AND revision = ?`,
			who, branch, wtRel, db.UTCNow(), pc.ID(), t.ID, t.Revision)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// 命不中就是有人先赢了：不覆盖、不重试同一道题，交给上层重新选题。
			return &claimLostError{TaskID: t.ID, Owner: currentOwner(tx, pc.ID(), t.ID)}
		}
		_ = db.AppendEvent(tx, pc.ID(), who, "claimed", t.ID, map[string]any{
			"from": "ready", "branch": branch, "worktree": wtRel,
			"guard": "status=ready+revision", "capacity": capacityPayload(pc),
		})
		_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", t.ID,
			map[string]any{"from": "ready", "to": "claimed"})
		// 领取同时登记 Agent 占用：心跳过期判定与看板都靠这一行。
		if err := bindAgent(tx, pc.ID(), who, "worker", "working", t.ID, wtRel, branch); err != nil {
			return err
		}
		_ = db.AppendEvent(tx, pc.ID(), who, "agent_registered", t.ID, map[string]any{"status": "working"})
		claimed, err = db.GetTask(tx, pc.ID(), t.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// capacityPayload 把两个上限一并记进领取事件，便于事后分辨「谁卡了谁」。
func capacityPayload(pc *projCtx) map[string]any {
	ref := 0
	if pc != nil && pc.Project != nil {
		ref = pc.Project.MaxParallel
	}
	return map[string]any{
		"max_parallel_reference": ref,
		"managed_limit_applies":  false,
	}
}

// currentOwner 读某任务当前的持有者（竞争失败时用于报文案）。
func currentOwner(tx *sql.Tx, projectID, taskID string) string {
	var owner sql.NullString
	if err := tx.QueryRow(`SELECT owner FROM tasks WHERE project_id = ? AND id = ?`,
		projectID, taskID).Scan(&owner); err != nil {
		return ""
	}
	return owner.String
}

// advanceClaim 把 claimed 推进到 in_progress，表示「目录已就绪、开始干活」。
func advanceClaim(pc *projCtx, taskID, who string) error {
	return db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil {
			return err
		}
		if t == nil {
			return i18n.E("err.unknown_task", "next.task_list", taskID)
		}
		if t.Owner != who {
			return i18n.E("err.owner_mismatch", "next.rerun_next", taskID, t.Owner, who)
		}
		if t.Status == "in_progress" {
			return nil // 重入：已是目标状态
		}
		from := t.Status
		if err := machine.AssertTransition(t.Status, "in_progress"); err != nil {
			return err
		}
		t.Status = "in_progress"
		t.Heartbeat = db.UTCNow()
		t.Revision++
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", taskID,
			map[string]any{"from": from, "to": "in_progress"})
		return nil
	})
}

// getTask 在短事务里读一条任务行。
func getTask(pc *projCtx, taskID string) (*db.Task, error) {
	var out *db.Task
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		out, e = db.GetTask(tx, pc.ID(), taskID)
		return e
	})
	return out, err
}

// listTasks 在短事务里读全工程任务（写任务卡时算前置用）。
func listTasks(pc *projCtx) ([]*db.Task, error) {
	var out []*db.Task
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		out, e = db.ListTasks(tx, pc.ID())
		return e
	})
	return out, err
}

// rollbackClaim 在目录／任务卡准备失败时把已领取的任务退回 ready。
//
// 两种中间状态都要认：claimed（已归属、目录还没建好）与 in_progress（目录已建、
// 后续步骤失败）。只退自己名下的行，避免误放别人的任务。
func rollbackClaim(pc *projCtx, who, taskID string) error {
	return db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), taskID)
		if err != nil || t == nil {
			return err
		}
		if t.Owner != who || (t.Status != "claimed" && t.Status != "in_progress") {
			return nil
		}
		if err := machine.AssertTransition(t.Status, "ready"); err != nil {
			return err
		}
		from := t.Status
		t.Status = "ready"
		t.Owner = ""
		t.Branch = ""
		t.Worktree = ""
		t.BlockedReason = ""
		t.Revision++
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		_ = db.AppendEvent(tx, pc.ID(), who, "status_changed", taskID,
			map[string]any{"from": from, "to": "ready", "reason": "claim_setup_failed"})
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
	return Result{"ok": true, "agent": who, "status": "offline", "project_id": pc.ID(), "next": i18n.T("next.reregister")}, nil
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
	return Result{"ok": true, "agents": agents, "project_id": projectID, "next": i18n.T("next.board_serve")}, nil
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
			return i18n.E("err.unknown_task", "next.task_list", taskID)
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
	return Result{"ok": true, "task_id": taskID, "agent": who, "heartbeat": hb, "project_id": pc.ID(), "next": i18n.T("next.continue_or_submit")}, nil
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
			return i18n.E("err.unknown_task", "next.task_list", taskID)
		}
		if err := identity.RequireOwner(who, t); err != nil {
			return err
		}
		if t.Status != "in_progress" && t.Status != "self_check" && t.Status != "conflict" {
			return i18n.E("err.cannot_submit", "next.only_in_progress", taskID, t.Status)
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
			return i18n.E("err.unknown_task", "next.task_list", taskID)
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
			return i18n.E("err.unknown_task", "", taskID)
		}
		if t.Owner == who {
			return i18n.E("err.author_review", "next.other_agent")
		}
		if t.Status != "in_review" {
			return i18n.E("err.not_in_review", "", t.Status)
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
