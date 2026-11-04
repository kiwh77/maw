package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/delivery"
	"github.com/kiwh77/maw/internal/engine/claim"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
	"github.com/kiwh77/maw/internal/machine"
	"github.com/kiwh77/maw/internal/notifications"
	"github.com/kiwh77/maw/internal/schedule"
	"github.com/kiwh77/maw/internal/scope"
	"github.com/kiwh77/maw/internal/systemagent"
	"github.com/kiwh77/maw/internal/verification"
	"github.com/kiwh77/maw/internal/workspace"
)

// NextOptions 是 next 的查询／领取开关。
//
// 查询与领取必须分离：默认只预览下一题候选，只有显式 Claim 才动任务状态、
// 建 Run/租约、准备 worktree。误敲一次 next 不应该占坑。
type NextOptions struct {
	DryRun  bool
	LogPath string
	Claim   bool
}

// Next 保留旧的程序化入口：调用方明确要求领取。
//
// CLI 走 NextWith，默认不领取；这里的兼容签名让既有调用方（含受管链路测试）
// 语义不变，避免「换个名字顺手改了行为」。
func Next(cwd, agentFlag string, dryRun bool, logPath string) (Result, error) {
	return NextWith(cwd, agentFlag, NextOptions{DryRun: dryRun, LogPath: logPath, Claim: true})
}

func NextWith(cwd, agentFlag string, opts NextOptions) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	logPath := strings.TrimSpace(opts.LogPath)
	if logPath != "" {
		abs, verr := board.ValidateLogPath(logPath)
		if verr != nil {
			return nil, i18n.EW("err.log_path", "next.fix_log_path", verr)
		}
		logPath = abs
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
		// 查询模式只看不改：已持有的任务如实报出，状态收敛与目录准备留给 --claim。
		if !opts.Claim {
			res := Result{
				"ok": true, "preview": true, "held": true, "task": task, "agent": who,
				"project_id": pc.ID(), "worktree": filepath.Join(pc.Repo, task.Worktree),
				"limits": limitsOf(pc),
				"next":   i18n.T("next.preview_held", who),
			}
			attachRunMeta(res, pc, task)
			attachAgentNameMeta(res, who, allocated, nameWarn)
			return res, nil
		}
		// 卡在 claimed 的续做要把状态收敛到 in_progress，否则 submit 会拒绝
		// （它只认 in_progress／self_check／conflict），任务就永远停在中途。
		if err := resumeClaim(pc, task, who); err != nil {
			return nil, err
		}
		if task, err = getTask(pc, task.ID); err != nil {
			return nil, err
		}
		_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			return bindAgent(tx, pc.ID(), who, "worker", "working", task.ID, task.Worktree, task.Branch, logPath)
		})
		res := Result{
			"ok": true, "idempotent": true, "task": task, "agent": who,
			"project_id": pc.ID(), "worktree": filepath.Join(pc.Repo, task.Worktree),
			"limits": limitsOf(pc),
			"next":   i18n.T("next.continue_worktree", filepath.Join(pc.Repo, task.Worktree)),
		}
		if logPath != "" {
			res["log_path"] = logPath
		}
		attachRunMeta(res, pc, task)
		attachAgentNameMeta(res, who, allocated, nameWarn)
		return res, nil
	}

	wtRel := filepath.Join(".worktrees", who)
	if opts.DryRun || !opts.Claim {
		preview, err := pickClaimTarget(pc, who)
		if err != nil {
			return nil, err
		}
		res := Result{"ok": true, "preview": true, "task": preview, "agent": who,
			"project_id": pc.ID(), "limits": limitsOf(pc),
			"next": i18n.T("next.preview_only", who)}
		if opts.DryRun {
			res["dry_run"] = true
		}
		attachAgentNameMeta(res, who, allocated, nameWarn)
		return res, nil
	}

	// 唯一的领取决策点：和受管 ClaimExecution 一样走 engine/claim。
	// ready→claimed 的判定、Run/Operation 的建立、节点槽与工作目录的占用都在同一个
	// 事务里完成，与受管派发抢同一道题时只会有一个赢家。
	var outcome claim.Outcome
	task, outcome, err = claimManual(pc, who, wtRel)
	if err != nil {
		return nil, err
	}

	wt, err := ensureClaimWorktree(pc, wtRel, task.Branch, true)
	if err != nil {
		return nil, err
	}
	// 目录准备好了才推进到 in_progress：claimed 表示「已归属、尚未开工」。
	if err := advanceClaim(pc, task.ID, who); err != nil {
		return nil, err
	}
	task, err = getTask(pc, task.ID)
	if err != nil {
		return nil, err
	}
	allTasks, err := listTasks(pc)
	if err != nil {
		return nil, err
	}
	if err := writeTaskCardWithDeps(wt, task, who, allTasks); err != nil {
		// Per-agent worktree is reused across tasks; do not wipe it on card write failure.
		return nil, err
	}
	if logPath != "" {
		_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			return bindAgent(tx, pc.ID(), who, "worker", "working", task.ID, task.Worktree, task.Branch, logPath)
		})
	}
	_, _ = refreshBoard(pc)
	res := Result{
		"ok": true, "task": task, "worktree": wt, "branch": task.Branch, "agent": who,
		"project_id": pc.ID(), "registered": true, "limits": limitsOf(pc),
		"env":  map[string]string{"MAW_AGENT": who, "MAW_TASK": task.ID},
		"next": i18n.T("next.registered", who, wt),
	}
	if logPath != "" {
		res["log_path"] = logPath
	}
	// 领取同时产出了 Run 与 Operation：把身份一并报出，老字段一个不动。
	res["run_id"] = string(outcome.RunID)
	res["operation_id"] = string(outcome.OperationID)
	res["task_instance"] = string(outcome.Instance)
	res["generation"] = int(outcome.Generation)
	attachAgentNameMeta(res, who, allocated, nameWarn)
	return res, nil
}

// attachRunMeta 把续做任务对应的 Run 身份补进输出（老脚本不受影响）。
func attachRunMeta(res Result, pc *projCtx, task *db.Task) {
	if task == nil || task.InstanceID == "" {
		return
	}
	var run *db.Run
	_ = db.WithReadTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		run, e = db.ActiveRunForInstance(tx, task.InstanceID)
		return e
	})
	if run != nil {
		res["run_id"] = string(run.ID)
		res["task_instance"] = task.InstanceID
		res["generation"] = int(run.Generation)
	}
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
	wt, err := ensureClaimWorktree(pc, wtRel, task.Branch, false)
	if err != nil {
		return err
	}
	// 旧版本领取可能没有 Run：补齐后再推进，保证「claimed 的任务有对应 Run」。
	// 已有 Run 时这里只是复用，不会多建。
	if task.InstanceID != "" {
		if err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			_, _, e := ensureManualCompatRun(tx, pc, task, who, wt)
			return e
		}); err != nil {
			return err
		}
	}
	return advanceClaim(pc, task.ID, who)
}

// claimManual 用一个有界循环把「选题 + 领取」做成原子动作。
//
// 选题（只读）与领取（claim 事务）仍是两步：中间那段空档就是真实存在于
// 「手工 next 与受管 ClaimExecution 之间」的竞争窗口，由 claim 的
// ready+revision 校验兜住。竞争失败只发生在别的领取者恰好同时赢了同一道题：
// 此时那道题已经不是 ready，下一轮选题自然不会重复挑到它。
// 非竞争错误（依赖不满足、没有 ready）直接上抛。
func claimManual(pc *projCtx, who, wtRel string) (*db.Task, claim.Outcome, error) {
	var lost error
	for attempt := 0; attempt < manualClaimAttempts; attempt++ {
		t, out, err := claimOnce(pc, who, wtRel, attempt)
		if err == nil {
			return t, out, nil
		}
		if !errors.Is(err, errClaimLost) {
			return nil, out, err
		}
		lost = err
	}
	var detail *claimLostError
	if errors.As(lost, &detail) {
		return nil, claim.Outcome{}, i18n.E("err.owner_mismatch", "next.rerun_next", detail.TaskID, detail.Owner, who)
	}
	return nil, claim.Outcome{}, lost
}

// claimOnce 选题，然后交给 engine/claim 建 Run、占节点槽与目录。
func claimOnce(pc *projCtx, who, wtRel string, attempt int) (*db.Task, claim.Outcome, error) {
	t, err := pickClaimTarget(pc, who)
	if err != nil {
		return nil, claim.Outcome{}, err
	}
	return commitClaimWithOutcome(pc, who, wtRel, t, attempt)
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

// commitClaim 把选定任务交给统一的领取用例（测试与续做路径保留旧签名）。
func commitClaim(pc *projCtx, who, wtRel string, t *db.Task) (*db.Task, error) {
	claimed, _, err := commitClaimWithOutcome(pc, who, wtRel, t, 0)
	return claimed, err
}

// commitClaimWithOutcome 走 engine/claim.Claim 领取选定任务。
//
// 条件写入（ready + revision）由 claim 在同一事务内复核，命中不了就返回
// 「被别人抢先」；调用方据此重新选题，绝不整行覆盖别人已经领取的任务。
// Run、Operation、节点槽与 workspace 租约都由 claim 建立：这与受管执行是同一套
// 占用语义，区别只在于 Operation 在本事务内落 applied，不等协调者派发 Runner。
func commitClaimWithOutcome(pc *projCtx, who, wtRel string, t *db.Task, attempt int) (*db.Task, claim.Outcome, error) {
	if t == nil {
		return nil, claim.Outcome{}, i18n.E("err.no_ready", "next.board_deps")
	}
	instID := model.InstanceID(t.InstanceID)
	if instID == "" || !instID.Valid() {
		return nil, claim.Outcome{}, i18n.E("err.managed_no_instance", "next.plan_apply", t.ID)
	}
	absWT, err := filepath.Abs(filepath.Join(pc.Repo, wtRel))
	if err != nil {
		return nil, claim.Outcome{}, err
	}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil, claim.Outcome{}, err
	}
	defer store.Close()
	// 节点登记是领取的前置：manual-compat 节点与 submit 路径共用。
	if err := store.Write(func(tx *sql.Tx) error { return ensureManualNode(tx, pc) }); err != nil {
		return nil, claim.Outcome{}, err
	}

	branch := "agent/" + who + "/" + t.ID
	expected := model.Revision(t.Revision)
	out, err := claim.New(store, nil).Claim(context.Background(), claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID:        model.ProjectID(pc.ID()),
			AgentID:          model.AgentID(who),
			RequestedTask:    &instID,
			ExpectedRevision: &expected,
			IdempotencyKey: fmt.Sprintf("manual-next:%s:%s:%s:%d:%d",
				pc.ID(), who, t.ID, int(t.Revision), attempt),
		},
		NodeID:                  model.NodeID(manualCompatNode),
		Workspace:               absWT,
		Branch:                  branch,
		Worktree:                wtRel,
		LegacyAgentID:           true,
		SettleDispatchOperation: true,
		ClaimedEventData: map[string]any{
			"branch": branch, "worktree": wtRel,
			"guard": "status=ready+revision", "capacity": capacityPayload(pc),
		},
	})
	if err != nil {
		return nil, out, mapClaimSetupErr(pc, who, t.ID, err)
	}

	// 领取时同时登记 Agent 占用：心跳过期判定与看板都靠这一行。
	var claimed *db.Task
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		if e := bindAgent(tx, pc.ID(), who, "worker", "working", t.ID, wtRel, branch, ""); e != nil {
			return e
		}
		_ = db.AppendEvent(tx, pc.ID(), who, "agent_registered", t.ID, map[string]any{"status": "working"})
		var e error
		claimed, e = db.GetTask(tx, pc.ID(), t.ID)
		return e
	})
	if err != nil {
		return nil, out, err
	}
	return claimed, out, nil
}

// ensureManualNode 登记 CLI 自领节点；与 submit 的 manual_client_compat 共用同一行。
func ensureManualNode(tx *sql.Tx, pc *projCtx) error {
	return db.UpsertNode(tx, &db.Node{
		ID: manualCompatNode, ProjectID: pc.ID(), Label: "manual CLI compat", SlotLimit: 64,
		Meta: map[string]any{"strategy": manualCompatStrategy},
	})
}

// mapClaimSetupErr 把「条件没命中且任务已被别人拿走」翻成 claimLostError，
// 让 claimManual 重新选题；其余错误保留原文，避免把容量/租约问题误报成被抢。
func mapClaimSetupErr(pc *projCtx, who, taskID string, err error) error {
	var me *model.Error
	if !errors.As(err, &me) {
		return err
	}
	if !(me.Code == model.CodeDoubleOccupancy ||
		me.Port == model.PortErrConflict || me.Port == model.PortErrRevisionMismatch) {
		return err
	}
	owner := ""
	_ = db.WithReadTx(pc.GlobalDB, func(tx *sql.Tx) error {
		owner = currentOwner(tx, pc.ID(), taskID)
		return nil
	})
	if owner != "" && owner != who {
		return &claimLostError{TaskID: taskID, Owner: owner}
	}
	return err
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
		if err := bindAgent(tx, pc.ID(), who, role, status, taskID, wt, branch, ""); err != nil {
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
		_ = bindAgent(tx, pc.ID(), who, "worker", "working", t.ID, t.Worktree, t.Branch, "")
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
	// 结构化完成物是**可选**载荷：解析失败必须整次拒绝，不能带病提交。
	result, err := loadSubmitResult(wt)
	if err != nil {
		return nil, err
	}

	auto := pc.Config.Approvals.Review == "auto"
	out, err := applyDeliveryCase(pc, deliveryCaseInput{
		Action: deliveryActionSubmit,
		TaskID: taskID,
		Actor:  who,
		WT:     wt,
		Files:  files,
		Auto:   auto,
	})
	if err != nil {
		return nil, err
	}
	if result != nil {
		if err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			return recordSubmitResult(tx, pc.ID(), who, taskID, out.DeliveryID, result)
		}); err != nil {
			return nil, err
		}
	}
	_, _ = refreshBoard(pc)
	next := "maw merge --yes"
	if !auto {
		next = "maw review --approve " + taskID
	}
	res := Result{
		"ok": true, "task_id": taskID, "agent": who, "files": out.Files, "status": out.Status,
		"delivery_id": out.DeliveryID, "strategy": out.Strategy, "project_id": pc.ID(), "next": next,
	}
	if result != nil {
		res["result_summary"] = result.Summary
		res["result_artifacts"] = len(result.Artifacts)
	}
	return res, nil
}

// —— 结构化完成物（契约 maw.collaboration.primitives/v1 §3）——
//
// 入口有两条，都不改 submit 的既有签名，也不动 CLI：
//   - 环境变量 MAW_SUBMIT_RESULT：一段 result JSON；
//   - 工作区文件 .maw/result.json：Agent 收工前写盘。
//
// 都没有 = 不带 result，行为与 v1 之前完全一致。

// SubmitResultArtifact 是一条产物声明。
type SubmitResultArtifact struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
	Note string `json:"note,omitempty"`
}

// SubmitResultCheck 是一条验收勾选。
type SubmitResultCheck struct {
	Text     string `json:"text"`
	OK       bool   `json:"ok"`
	Evidence string `json:"evidence,omitempty"`
}

// SubmitResult 是 submit 的可选结构化完成物。
type SubmitResult struct {
	Summary          string                 `json:"summary"`
	Artifacts        []SubmitResultArtifact `json:"artifacts,omitempty"`
	AcceptanceChecks []SubmitResultCheck    `json:"acceptance_checks,omitempty"`
}

// MaxResultArtifacts 是产物条数上限（契约 §3：总数 ≤ 64）。
const MaxResultArtifacts = 64

// loadSubmitResult 读取并校验可选的 result；没有则返回 (nil, nil)。
func loadSubmitResult(wt string) (*SubmitResult, error) {
	raw := strings.TrimSpace(os.Getenv("MAW_SUBMIT_RESULT"))
	source := "MAW_SUBMIT_RESULT"
	if raw == "" {
		path := filepath.Join(wt, ".maw", "result.json")
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, errx.Wrap("读取 result 失败", "检查 .maw/result.json 权限", err)
		}
		raw = strings.TrimSpace(string(b))
		source = ".maw/result.json"
		if raw == "" {
			return nil, nil
		}
	}
	var res SubmitResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return nil, &errx.Error{
			Msg:  "invalid_result: " + source + " 不是合法 JSON: " + err.Error(),
			Next: "result 形如 {\"summary\":\"...\",\"artifacts\":[{\"path\":\"...\"}],\"acceptance_checks\":[{\"text\":\"...\",\"ok\":true}]}",
			Code: 1,
		}
	}
	if err := res.validate(); err != nil {
		return nil, err
	}
	return &res, nil
}

// validate 按契约字段表逐条校验；不合法整次拒绝（不做部分写入）。
func (r *SubmitResult) validate() error {
	if strings.TrimSpace(r.Summary) == "" {
		return invalidResult("summary 不能为空")
	}
	if len(r.Artifacts) > MaxResultArtifacts {
		return invalidResult("artifacts 超过 " + strconv.Itoa(MaxResultArtifacts) + " 条")
	}
	for i, a := range r.Artifacts {
		if strings.TrimSpace(a.Path) == "" {
			return invalidResult("artifacts[" + strconv.Itoa(i) + "].path 不能为空")
		}
	}
	for i, c := range r.AcceptanceChecks {
		if strings.TrimSpace(c.Text) == "" {
			return invalidResult("acceptance_checks[" + strconv.Itoa(i) + "].text 不能为空")
		}
	}
	r.Summary = strings.TrimSpace(r.Summary)
	return nil
}

func invalidResult(msg string) error {
	return &errx.Error{
		Msg:  "invalid_result: " + msg,
		Next: "按契约 collaboration-primitives-contract-v1 §3 修正 result 后重试",
		Code: 1,
	}
}

// recordSubmitResult 把 result 落成一条可查询事件（不改交付与任务状态机）。
func recordSubmitResult(tx *sql.Tx, projectID, actor, taskID, deliveryID string, res *SubmitResult) error {
	return db.AppendEvent(tx, projectID, actor, "task_result", taskID, map[string]any{
		"delivery_id":       deliveryID,
		"summary":           res.Summary,
		"artifacts":         res.Artifacts,
		"acceptance_checks": res.AcceptanceChecks,
	})
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
	action := deliveryActionReject
	if approve {
		action = deliveryActionApprove
	}
	out, err := applyDeliveryCase(pc, deliveryCaseInput{
		Action:  action,
		TaskID:  taskID,
		Actor:   who,
		WT:      wt,
		Files:   files,
		Message: message,
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	next := "author: fix and maw submit"
	if approve {
		next = "maw merge --yes"
	}
	return Result{
		"ok": true, "task_id": taskID, "status": out.Status, "delivery_id": out.DeliveryID,
		"project_id": pc.ID(), "next": next,
	}, nil
}

// —— 交付／验收共用用例（CLI 与后续 HTTP 命令的唯一写入点）——

const (
	manualCompatNode     = "manual-compat"
	manualCompatStrategy = "manual_client_compat"
	extraDeliveryID      = "delivery_id"
	extraDeliveryStrat   = "delivery_strategy"
	extraRunID           = "run_id"
)

type deliveryAction string

const (
	deliveryActionSubmit  deliveryAction = "submit"
	deliveryActionApprove deliveryAction = "approve"
	deliveryActionReject  deliveryAction = "reject"
	deliveryActionBlock   deliveryAction = "block"
	deliveryActionMerge   deliveryAction = "merge"
)

type deliveryCaseInput struct {
	Action       deliveryAction
	TaskID       string
	Actor        string
	WT           string
	Files        []string
	Message      string
	Auto         bool
	MergedCommit string
}

type deliveryCaseResult struct {
	DeliveryID string
	Status     string
	Files      []string
	Strategy   string
}

// applyDeliveryCase 是提交／拒绝／阻塞／合入的唯一验收入口；CLI 不得绕过它直接改状态。
func applyDeliveryCase(pc *projCtx, in deliveryCaseInput) (deliveryCaseResult, error) {
	switch in.Action {
	case deliveryActionSubmit:
		return deliverySubmit(pc, in)
	case deliveryActionApprove, deliveryActionReject:
		return deliveryReview(pc, in)
	case deliveryActionBlock:
		return deliveryBlock(pc, in)
	case deliveryActionMerge:
		return deliveryMergeRecord(pc, in)
	default:
		return deliveryCaseResult{}, errx.New("unknown delivery action "+string(in.Action), "")
	}
}

func deliverySubmit(pc *projCtx, in deliveryCaseInput) (deliveryCaseResult, error) {
	task, err := getTask(pc, in.TaskID)
	if err != nil || task == nil {
		return deliveryCaseResult{}, i18n.E("err.unknown_task", "next.task_list", in.TaskID)
	}
	if err := identity.RequireOwner(in.Actor, task); err != nil {
		return deliveryCaseResult{}, err
	}

	frozen, verifyOut, err := freezeManualCompatDelivery(pc, task, in.Actor, in.WT, in.Files)
	if err != nil {
		return deliveryCaseResult{}, err
	}
	if verifyOut.EnvFailed {
		_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			_ = db.AppendEvent(tx, pc.ID(), in.Actor, "gate_env_failed", in.TaskID, map[string]any{
				"error": verifyOut.Reason, "phase": "submit", "delivery_id": string(frozen.Delivery.DeliveryID),
			})
			return nil
		})
		return deliveryCaseResult{}, errx.New(verifyOut.Reason, "检查验证环境后重试 maw submit")
	}
	if !verifyOut.SupportsDone && verifyOut.Kind == verification.KindMachine && verifyOut.Verification.Result == model.VerifyFail {
		_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			t, _ := db.GetTask(tx, pc.ID(), in.TaskID)
			if t != nil {
				t.Attempts++
				t.Revision++
				_ = db.UpsertTask(tx, t)
			}
			_ = db.AppendEvent(tx, pc.ID(), in.Actor, "gate_failed", in.TaskID, map[string]any{
				"error": verifyOut.Reason, "phase": "submit", "delivery_id": string(frozen.Delivery.DeliveryID),
			})
			return nil
		})
		return deliveryCaseResult{}, errx.New(verifyOut.Reason, "修复验证失败后重试 maw submit")
	}

	var status string
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), in.TaskID)
		if err != nil || t == nil {
			return i18n.E("err.unknown_task", "next.task_list", in.TaskID)
		}
		if err := identity.RequireOwner(in.Actor, t); err != nil {
			return err
		}
		if err := db.AddVerification(tx, string(frozen.Delivery.DeliveryID), verifyOut.Verification); err != nil {
			return err
		}
		if in.Auto {
			now := time.Now().UTC()
			if err := db.AddReview(tx, string(frozen.Delivery.DeliveryID), model.Review{
				Required: true, Verdict: model.VerdictApproved, Reviewer: "system", ReviewedAt: &now,
			}); err != nil {
				return err
			}
		}
		from := t.Status
		if err := machine.AssertTransition(t.Status, "in_review"); err != nil {
			return err
		}
		t.Status = "in_review"
		t.Heartbeat = db.UTCNow()
		t.Revision++
		if t.Extra == nil {
			t.Extra = map[string]any{}
		}
		t.Extra[extraDeliveryID] = string(frozen.Delivery.DeliveryID)
		t.Extra[extraDeliveryStrat] = manualCompatStrategy
		t.Extra[extraRunID] = string(frozen.Delivery.RunID)
		_ = db.AppendEvent(tx, pc.ID(), in.Actor, "status_changed", in.TaskID, map[string]any{
			"from": from, "to": "in_review", "files": in.Files,
			"delivery_id": string(frozen.Delivery.DeliveryID), "strategy": manualCompatStrategy,
		})
		if in.Auto {
			if err := machine.AssertTransition(t.Status, "merging"); err != nil {
				return err
			}
			from2 := t.Status
			t.Status = "merging"
			t.Revision++
			_ = db.AppendEvent(tx, pc.ID(), "system", "review_auto_approved", in.TaskID, map[string]any{
				"files": in.Files, "file_count": len(in.Files), "write_scope": t.WriteScope,
				"review": "auto", "delivery_id": string(frozen.Delivery.DeliveryID),
			})
			_ = db.AppendEvent(tx, pc.ID(), "system", "status_changed", in.TaskID, map[string]any{
				"from": from2, "to": "merging",
			})
		}
		status = t.Status
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		return bindAgent(tx, pc.ID(), in.Actor, "worker", "working", t.ID, t.Worktree, t.Branch, "")
	})
	if err != nil {
		return deliveryCaseResult{}, err
	}
	return deliveryCaseResult{
		DeliveryID: string(frozen.Delivery.DeliveryID),
		Status:     status,
		Files:      in.Files,
		Strategy:   manualCompatStrategy,
	}, nil
}

type frozenDelivery struct {
	Delivery model.Delivery
}

// deliverableFiles 只保留在候选提交里还能取到内容的路径。
//
// 交付的 file 产物必须能从提交里取回：被本候选删除的文件取不到内容，收集器会核成
// exists=false 并报 E320，整次交付都被拒。删除本身由 commit 产物承载，所以这里跳过。
func deliverableFiles(repo, rev string, files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if strings.TrimSpace(f) == "" {
			continue
		}
		if _, err := home.Git(repo, "cat-file", "-e", rev+":"+f); err != nil {
			continue
		}
		out = append(out, f)
	}
	return out
}

func freezeManualCompatDelivery(pc *projCtx, task *db.Task, actor, wt string, files []string) (frozenDelivery, verification.Outcome, error) {
	var out verification.Outcome
	if strings.TrimSpace(task.InstanceID) == "" {
		return frozenDelivery{}, out, errx.New("task missing instance_id", "重新 plan apply 绑定实例后再 submit")
	}
	head, err := home.Git(wt, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head) == "" {
		return frozenDelivery{}, out, errx.New("无法读取候选提交", "确认 worktree 已 commit")
	}
	head = strings.TrimSpace(head)
	base, err := home.Git(pc.Repo, "rev-parse", pc.Config.Project.MainBranch)
	if err != nil || strings.TrimSpace(base) == "" {
		return frozenDelivery{}, out, errx.New("无法读取基线提交", "检查主干分支 "+pc.Config.Project.MainBranch)
	}
	base = strings.TrimSpace(base)
	candCommit := model.Commit(head)
	baseline := model.Commit(base)

	var run *db.Run
	var version model.VersionNo
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		run, version, e = ensureManualCompatRun(tx, pc, task, actor, wt)
		return e
	})
	if err != nil {
		return frozenDelivery{}, out, err
	}

	arts := []model.ArtifactCandidate{{
		Kind: model.ArtifactCommit, Ref: head, DeclaredBy: model.DeclaredByCLI,
	}}
	for _, f := range deliverableFiles(wt, head, files) {
		arts = append(arts, model.ArtifactCandidate{
			Kind: model.ArtifactFile, Ref: f, DeclaredBy: model.DeclaredByCLI,
		})
	}
	var supersedes *model.DeliveryID
	_ = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		list, e := db.ListDeliveries(tx, pc.ID(), task.InstanceID)
		if e != nil || len(list) == 0 {
			return e
		}
		prev := list[len(list)-1].Delivery.DeliveryID
		supersedes = &prev
		return nil
	})

	collector, err := delivery.New(model.ProjectID(pc.ID()), pc.Repo, task.WriteScope, delivery.NewMemoryStore())
	if err != nil {
		return frozenDelivery{}, out, err
	}
	cand := delivery.Candidate{
		TaskInstance:    model.InstanceID(task.InstanceID),
		TaskVersion:     version,
		RunID:           model.RunID(run.ID),
		Mode:            model.DeliveryCode,
		BaselineCommit:  baseline,
		CandidateCommit: &candCommit,
		Worktree:        wt,
		RunOutcome:      model.OutcomeCompleted,
		Artifacts:       arts,
		Review:          model.Review{Required: true},
		Merge:           model.Merge{Required: true},
		Supersedes:      supersedes,
	}
	d, collErr := collector.Collect(cand)
	if collErr != nil {
		if hard := deliveryHardFail(collErr); hard != nil {
			return frozenDelivery{}, out, hard
		}
		// pending 完成条件不足是提交阶段的常态；硬问题（越界／缺提交）才拦截。
		if d.DeliveryID == "" {
			return frozenDelivery{}, out, collErr
		}
	}
	d.Review.Required = true
	d.Merge.Required = true

	policy := verification.Policy{Kind: verification.KindManual, TimeoutSec: pc.Config.Gates.TimeoutSec}
	if len(pc.Config.Gates.Commands) > 0 {
		policy = verification.Policy{
			Kind: verification.KindMachine, Commands: pc.Config.Gates.Commands,
			TimeoutSec: pc.Config.Gates.TimeoutSec,
		}
	}
	svc := &verification.Service{
		Runner: gatesRunnerAt(wt),
	}
	out, err = svc.Run(context.Background(), policy, verification.Target{
		DeliveryID:  string(d.DeliveryID),
		TaskVersion: version,
		Commit:      candCommit,
		ScopeOK:     true,
	})
	if err != nil {
		return frozenDelivery{}, out, err
	}

	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		if _, _, e := db.SaveDelivery(tx, pc.ID(), d, "manual-compat:submit:"+string(d.DeliveryID)); e != nil {
			return e
		}
		return completeCompatRun(tx, pc.ID(), run, task.InstanceID, true)
	})
	if err != nil {
		return frozenDelivery{}, out, err
	}
	return frozenDelivery{Delivery: d}, out, nil
}

func deliveryHardFail(err error) error {
	vs, ok := err.(delivery.Violations)
	if !ok {
		return err
	}
	for _, v := range vs {
		if v == nil {
			continue
		}
		switch v.Code {
		case model.CodeArtifactOutOfScope, model.CodeCommitRefInvalid, model.CodeArtifactMissing,
			model.CodeDeliveryTaskMissing, model.CodeDeliveryRunMissing, model.CodeDeliveryVersionMissing:
			return errx.New(v.Error(), "按交付契约修正后重试 maw submit")
		}
	}
	return nil
}

func gatesRunnerAt(wt string) verification.Runner {
	return func(ctx context.Context, commands []string, timeoutSec int) ([]verification.CommandResult, error) {
		_ = ctx
		results, err := gitops.RunGatesDetailed(commands, wt, timeoutSec)
		out := make([]verification.CommandResult, 0, len(results))
		for _, g := range results {
			cr := verification.CommandResult{Command: g.Command, OK: g.OK, Output: g.Output, EnvFail: g.EnvFailure}
			if !g.OK && !cr.EnvFail && gitops.IsEnvFailure(g.Output) {
				cr.EnvFail = true
			}
			out = append(out, cr)
		}
		return out, err
	}
}

// ensureManualCompatRun 为人工 CLI 路径显式建立／复用 Run（策略 manual_client_compat）。
//
// 没有 Run 就无法绑定交付；这条路径与受管 ClaimExecution 共用同一套占用与冻结语义，
// 只是节点固定为 manual-compat，不走厂商进程。
func ensureManualCompatRun(tx *sql.Tx, pc *projCtx, task *db.Task, actor, wt string) (*db.Run, model.VersionNo, error) {
	inst, err := db.GetInstance(tx, task.InstanceID)
	if err != nil {
		return nil, 0, err
	}
	if inst == nil {
		inst, err = db.ActiveInstance(tx, pc.ID(), task.ID)
		if err != nil || inst == nil {
			return nil, 0, errx.New("missing task instance "+task.ID, "maw plan apply")
		}
		task.InstanceID = string(inst.InstanceID)
	}
	if inst.ActiveVersion == nil {
		return nil, 0, errx.New("instance has no active version", "maw plan apply")
	}
	version := *inst.ActiveVersion

	if active, err := db.ActiveRunForInstance(tx, task.InstanceID); err != nil {
		return nil, 0, err
	} else if active != nil {
		return active, version, nil
	}
	runs, err := db.ListRuns(tx, pc.ID())
	if err != nil {
		return nil, 0, err
	}
	var gen model.Generation = 1
	for _, r := range runs {
		if r == nil || r.InstanceID != task.InstanceID {
			continue
		}
		if r.Generation >= gen {
			gen = r.Generation + 1
		}
		// 已冻结的完成 Run 可复用：同实例再次 submit 只追加新交付。
		if r.Phase == model.PhaseCompleted && r.DeliveryFrozen {
			return r, version, nil
		}
	}

	if err := ensureManualNode(tx, pc); err != nil {
		return nil, 0, err
	}
	absWT := wt
	if !filepath.IsAbs(absWT) {
		absWT = filepath.Join(pc.Repo, absWT)
	}
	absWT = filepath.Clean(absWT)
	out, err := db.ClaimRun(tx, db.ClaimSpec{
		ProjectID:      pc.ID(),
		InstanceID:     task.InstanceID,
		VersionNo:      version,
		Generation:     gen,
		NodeID:         manualCompatNode,
		Workspace:      absWT,
		Coordinator:    actor,
		IdempotencyKey: fmt.Sprintf("manual-compat:%s:%d", task.InstanceID, gen),
	})
	if err != nil {
		return nil, 0, err
	}
	return out.Run, version, nil
}

func completeCompatRun(tx *sql.Tx, projectID string, run *db.Run, instanceID string, deliveryFrozen bool) error {
	if run == nil {
		return nil
	}
	if run.Phase == model.PhaseCompleted && run.ProcessStopConfirmed && (!deliveryFrozen || run.DeliveryFrozen) {
		return nil
	}
	// 合法阶段链：reserved → preparing → launching → running → completed
	path := []model.RunPhase{
		model.PhasePreparing, model.PhaseLaunching, model.PhaseRunning, model.PhaseCompleted,
	}
	for _, next := range path {
		if run.Phase == model.PhaseCompleted {
			break
		}
		if !model.CanTransition(run.Phase, next) {
			continue
		}
		eid := compatEventID(run.ID, string(next))
		if _, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
			ID: eid, ProjectID: projectID, RunID: run.ID, InstanceID: instanceID,
			Kind: db.EventRunPhase, Phase: next, SchemaVersion: db.RunEventSchemaVersion,
			Payload: map[string]any{"strategy": manualCompatStrategy},
		}); err != nil {
			return err
		}
		stop := next == model.PhaseCompleted
		frozen := deliveryFrozen && next == model.PhaseCompleted
		updated, err := db.UpdateRunProgress(tx, run.ID, next, stop, frozen, []string{
			"strategy:" + manualCompatStrategy, "manual_compat:completed",
		})
		if err != nil {
			return err
		}
		run = updated
	}
	if run != nil && !run.ProcessStopConfirmed {
		if err := db.ConfirmProcessStop(tx, run.ID, "manual_compat:cli_submit"); err != nil {
			return err
		}
	}
	if run != nil && deliveryFrozen && run.Phase == model.PhaseCompleted && !run.DeliveryFrozen {
		return errx.New("run completed without delivery_frozen", "重新 submit 以冻结交付")
	}
	return nil
}

func compatEventID(runID, phase string) model.EventID {
	sum := sha256.Sum256([]byte(runID + "|" + phase + "|manual-compat"))
	return model.EventID("evt_" + hex.EncodeToString(sum[:8]))
}

func deliveryReview(pc *projCtx, in deliveryCaseInput) (deliveryCaseResult, error) {
	var status string
	var deliveryID string
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), in.TaskID)
		if err != nil || t == nil {
			return i18n.E("err.unknown_task", "", in.TaskID)
		}
		if t.Owner == in.Actor {
			return i18n.E("err.author_review", "next.other_agent")
		}
		if t.Status != "in_review" {
			return i18n.E("err.not_in_review", "", t.Status)
		}
		rec, err := latestDelivery(tx, pc.ID(), t)
		if err != nil {
			return err
		}
		if rec == nil {
			return errx.New("delivery missing for review", "作者需先 maw submit 冻结交付")
		}
		deliveryID = string(rec.Delivery.DeliveryID)
		ready, err := db.DeliveryReadiness(tx, deliveryID)
		if err != nil {
			return err
		}
		if ready.StaleVerifications > 0 || ready.ReviewStale {
			return errx.New("delivery evidence mismatch", "证据与当前交付指纹不一致，请重新 submit")
		}
		if !ready.PassingVerification {
			return errx.New("delivery verification incomplete", "缺少有效验证结论，不能评审")
		}
		now := time.Now().UTC()
		verdict := model.VerdictChangesRequired
		if in.Action == deliveryActionApprove {
			verdict = model.VerdictApproved
		}
		if err := db.AddReview(tx, deliveryID, model.Review{
			Required: true, Verdict: verdict, Reviewer: in.Actor, ReviewedAt: &now,
		}); err != nil {
			return err
		}
		from := t.Status
		if in.Action == deliveryActionApprove {
			if err := machine.AssertTransition(t.Status, "merging"); err != nil {
				return err
			}
			t.Status = "merging"
			_ = db.AppendEvent(tx, pc.ID(), in.Actor, "review_result", in.TaskID, map[string]any{
				"result": "approve", "message": in.Message,
				"files": in.Files, "file_count": len(in.Files), "delivery_id": deliveryID,
			})
			_ = db.AppendEvent(tx, pc.ID(), in.Actor, "status_changed", in.TaskID, map[string]any{
				"from": from, "to": "merging",
			})
		} else {
			if err := machine.AssertTransition(t.Status, "in_progress"); err != nil {
				return err
			}
			t.Status = "in_progress"
			_ = db.AppendEvent(tx, pc.ID(), in.Actor, "review_result", in.TaskID, map[string]any{
				"result": "request_changes", "message": in.Message, "delivery_id": deliveryID,
			})
			_ = db.AppendEvent(tx, pc.ID(), in.Actor, "status_changed", in.TaskID, map[string]any{
				"from": from, "to": "in_progress",
			})
		}
		t.Revision++
		status = t.Status
		return db.UpsertTask(tx, t)
	})
	if err != nil {
		return deliveryCaseResult{}, err
	}
	return deliveryCaseResult{DeliveryID: deliveryID, Status: status, Files: in.Files, Strategy: manualCompatStrategy}, nil
}

func deliveryBlock(pc *projCtx, in deliveryCaseInput) (deliveryCaseResult, error) {
	var deliveryID string
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), in.TaskID)
		if err != nil || t == nil {
			return i18n.E("err.unknown_task", "", in.TaskID)
		}
		if err := identity.RequireOwner(in.Actor, t); err != nil {
			return err
		}
		if rec, err := latestDelivery(tx, pc.ID(), t); err != nil {
			return err
		} else if rec != nil {
			deliveryID = string(rec.Delivery.DeliveryID)
			now := time.Now().UTC()
			// 阻塞走同一评审表：记 changes_requested，避免 CLI 只改任务行绕过验收。
			if err := db.AddReview(tx, deliveryID, model.Review{
				Required: true, Verdict: model.VerdictChangesRequired,
				Reviewer: in.Actor, ReviewedAt: &now,
			}); err != nil {
				return err
			}
		}
		from := t.Status
		t.PrevStatus = t.Status
		t.Status = "blocked"
		t.BlockedReason = in.Message
		t.Revision++
		_ = db.AppendEvent(tx, pc.ID(), in.Actor, "status_changed", in.TaskID, map[string]any{
			"from": from, "to": "blocked", "reason": in.Message,
			"delivery_id": deliveryID, "strategy": manualCompatStrategy,
		})
		if err := db.UpsertTask(tx, t); err != nil {
			return err
		}
		// 站内/桌面提醒（事务内登记，投递在中枢侧）。
		subject := t.InstanceID
		if subject == "" {
			subject = "task_" + t.ID
		}
		eid := string(compatEventID(t.ID, "blocked|"+fmt.Sprintf("%d", t.Revision)))
		notifySvc := notifications.New()
		if _, err := notifySvc.Raise(tx, notifications.BusinessEvent{
			EventID:    eid,
			ProjectID:  pc.ID(),
			Kind:       notifications.EventTaskStatus,
			Subject:    subject,
			TaskStatus: model.TaskBlocked,
			Reason:     in.Message,
			Title:      "任务阻塞",
			Body:       t.ID + ": " + in.Message,
		}); err != nil {
			// 提醒失败不阻断 block 主路径，但写入事件便于排查。
			_ = db.AppendEvent(tx, pc.ID(), "system", "notification_raise_failed", in.TaskID, map[string]any{
				"error": err.Error(),
			})
		}
		// system_agent 启用时同事务入队（outbox）；游标 watcher 仍会扫 events，靠 UNIQUE 幂等。
		if pc.Config != nil && pc.Config.SystemAgent.Enabled {
			enqueue, escalate := systemagent.ClassifyBlocked(in.Message, pc.Config.SystemAgent)
			if enqueue && systemagent.KindAllowed(systemagent.SystemKindUnblock, pc.Config.SystemAgent, false) {
				decision := map[string]any{}
				if escalate {
					decision["action"] = string(systemagent.ActionEscalateHuman)
				}
				_, _ = systemagent.Enqueue(tx, pc.Config.SystemAgent, pc.ID(), systemagent.SystemKindUnblock,
					t.ID, t.InstanceID, in.Message, decision, false)
			}
		}
		return nil
	})
	if err != nil {
		return deliveryCaseResult{}, err
	}
	return deliveryCaseResult{DeliveryID: deliveryID, Status: "blocked", Strategy: manualCompatStrategy}, nil
}

func deliveryMergeRecord(pc *projCtx, in deliveryCaseInput) (deliveryCaseResult, error) {
	err := db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		t, err := db.GetTask(tx, pc.ID(), in.TaskID)
		if err != nil || t == nil {
			return i18n.E("err.unknown_task", "", in.TaskID)
		}
		rec, err := latestDelivery(tx, pc.ID(), t)
		if err != nil {
			return err
		}
		if rec == nil {
			return errx.New("delivery missing for merge", "先 maw submit / review")
		}
		d := rec.Delivery
		now := time.Now().UTC()
		var merged *model.Commit
		if c := model.Commit(strings.TrimSpace(in.MergedCommit)); c.Valid() {
			merged = &c
		}
		d.Merge = model.Merge{Required: true, Merged: true, MergedCommit: merged, MergedAt: &now}
		d.Review.Required = true
		if d.Review.Verdict == "" {
			d.Review.Verdict = model.VerdictApproved
		}
		if _, err := db.UpdateDeliveryManifest(tx, d); err != nil {
			return err
		}
		// 释放占用：产物已冻结且进程已停。
		if runID := extraString(t, extraRunID); runID != "" {
			if run, _ := db.GetRun(tx, runID); run != nil {
				for _, l := range run.Leases {
					if l.Released {
						continue
					}
					cond := db.ReleaseCondition{}
					switch l.Kind {
					case model.LeaseWorkspace:
						cond.DeliveryFrozen = true
					case model.LeaseNodeSlot:
						cond.ProcessStopConfirmed = true
					}
					_ = db.ReleaseLease(tx, string(l.LeaseID), run.Generation, "merged", cond)
				}
			}
		}
		return nil
	})
	if err != nil {
		return deliveryCaseResult{}, err
	}
	return deliveryCaseResult{DeliveryID: "", Status: "done", Strategy: manualCompatStrategy}, nil
}

func latestDelivery(tx *sql.Tx, projectID string, t *db.Task) (*db.DeliveryRecord, error) {
	if id := extraString(t, extraDeliveryID); id != "" {
		return db.GetDelivery(tx, id)
	}
	if t.InstanceID == "" {
		return nil, nil
	}
	list, err := db.ListDeliveries(tx, projectID, t.InstanceID)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return list[len(list)-1], nil
}

// assertDeliveryMergeReady 合入前检查：验证／评审齐全，且无指纹漂移、无残留占用风险。
func assertDeliveryMergeReady(pc *projCtx, task *db.Task) error {
	return db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		rec, err := latestDelivery(tx, pc.ID(), task)
		if err != nil {
			return err
		}
		if rec == nil {
			return errx.New("delivery missing for merge", "先 maw submit 并完成评审")
		}
		ready, err := db.DeliveryReadiness(tx, string(rec.Delivery.DeliveryID))
		if err != nil {
			return err
		}
		if ready.StaleVerifications > 0 || ready.ReviewStale {
			return errx.New("delivery evidence mismatch", "验证／评审指纹已过期，请重新 submit")
		}
		if !ready.PassingVerification || !ready.ApprovedReview {
			return errx.New("delivery not accepted", "缺少有效验证或评审通过结论")
		}
		if wt := absWorktree(pc.Repo, task.Worktree); wt != "" {
			if reason, blocked := workspaceHoldReason(tx, wt); blocked {
				return errx.New(reason, "占用解除前不能 merge")
			}
		}
		if runID := extraString(task, extraRunID); runID != "" {
			run, err := db.GetRun(tx, runID)
			if err != nil {
				return err
			}
			if run != nil {
				if run.Reconciliation == model.ReconciliationUnknown {
					return errx.New("run reconciliation unknown", "核实状态未知时不能 merge")
				}
				if (run.Phase == model.PhaseFailed || run.Phase == model.PhaseCancelled) && !run.ProcessStopConfirmed {
					return errx.New("process stop unconfirmed", "残留进程未确认停止时不能 merge")
				}
			}
		}
		return nil
	})
}

// workspaceHoldReason 用工作空间占用分类判断目录能否清理／合入重置。
func workspaceHoldReason(tx *sql.Tx, absPath string) (string, bool) {
	key, err := (db.Resource{Kind: model.LeaseWorkspace, Path: filepath.Clean(absPath)}).Key()
	if err != nil {
		return "", false
	}
	lease, err := db.ActiveLease(tx, key)
	if err != nil || lease == nil {
		return "", false
	}
	if lease.RunID == "" {
		return fmt.Sprintf("workspace %s still leased (no run)", absPath), true
	}
	run, err := db.GetRun(tx, lease.RunID)
	if err != nil || run == nil {
		return fmt.Sprintf("workspace %s lease run missing", absPath), true
	}
	occ := workspace.Occupant{
		Instance: model.InstanceID(run.InstanceID),
		Run:      run.Model(),
	}
	if !occ.Blocks() {
		return "", false
	}
	return occ.Describe() + "：不得 prune/reset/merge", true
}
