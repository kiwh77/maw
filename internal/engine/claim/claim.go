package claim

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/engine/state"
	"github.com/kiwh77/maw/internal/schedule"
	"github.com/kiwh77/maw/internal/scope"
)

// Store 是领取依赖的事务端口：一次 Write 就是一个 BEGIN IMMEDIATE 短事务。
//
// 领取的全部读写都在 Write 里完成，不用 Read 预检：预检与写入之间的事实变化
// 会让「检查通过」失去意义。
type Store interface {
	Read(fn func(*sql.Tx) error) error
	Write(fn func(*sql.Tx) error) error
}

var _ Store = (*db.Store)(nil)

// NodeGuard 是节点就绪与能力检查（由 internal/nodes 实现）。
//
// 只读内存投影，可以放在事务内调用；不得在这里探测进程或访问网络。
type NodeGuard interface {
	Dispatchable(id model.NodeID, required []string) (bool, []string, error)
}

// Request 是一次领取的输入。
//
// 内嵌 ports.ClaimRequest（契约 DTO），额外带上契约 DTO 里没有的资源绑定：
// 节点、工作目录、代际、任务分支与 worktree。接线层负责把这两部分拼起来。
type Request struct {
	ports.ClaimRequest

	// NodeID 为空表示未绑定受管节点的人工领取：不占节点槽，不建 Run。
	NodeID model.NodeID `json:"node_id,omitempty"`
	// Workspace 是本次执行独占的工作目录（绝对路径）。
	Workspace string `json:"workspace"`
	// Generation 留空时按该实例现有 Run 推导：仍计入占用则沿用其代际（重放稳定），
	// 否则取最大代际 + 1。
	Generation model.Generation `json:"generation,omitempty"`
	// RequiredCapabilities 是本次执行要求节点具备的能力名。
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	// Branch 与 Worktree 只写进任务行，真正的 git 操作在事务外。
	Branch   string `json:"branch,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	// SettleDispatchOperation 声明「这次领取由 CLI 自己承接」：Operation 照旧落库
	// （幂等与审计需要），但在同一事务里直接标 applied，不留在待派发队列里
	// 等协调者来启动 Runner。maw next 用这个开关，受管派发保持 pending。
	SettleDispatchOperation bool `json:"settle_dispatch_operation,omitempty"`
	// ClaimedEventData 是接线层补充到 claimed 事件的审计字段（容量参考、guard 等）。
	// 只影响事件内容，不参与领取判定。
	ClaimedEventData map[string]any `json:"-"`
	// LegacyAgentID 保留 CLI 自领的旧命名（不强制 {应用}-{工程slug}-{序号}）。
	// 老脚本用 MAW_AGENT=w1 这类名字领取仍然可用；受管派发继续走契约校验。
	LegacyAgentID bool `json:"legacy_agent_id,omitempty"`
}

// Validate 拒绝不完整的领取：缺幂等键、缺目录、节点身份非法都直接报错。
func (r Request) Validate() []*model.Error {
	out := r.ClaimRequest.Validate()
	if r.LegacyAgentID {
		kept := out[:0]
		for _, e := range out {
			if e.Path == "claim.agent_id" {
				continue
			}
			kept = append(kept, e)
		}
		out = kept
	}
	if strings.TrimSpace(r.Workspace) == "" || !filepath.IsAbs(r.Workspace) {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "claim.workspace",
			"领取必须给出绝对工作目录，否则无法保证同目录只有一个写入者"))
	}
	if r.NodeID != "" && !r.NodeID.Valid() {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "claim.node_id", "节点身份形态非法"))
	}
	if r.Generation < 0 {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "claim.generation", "代际不得为负"))
	}
	return out
}

// Outcome 是一次领取的结果。
type Outcome struct {
	TaskID      string
	Instance    model.InstanceID
	Version     model.VersionNo
	Revision    model.Revision
	RunID       model.RunID
	Generation  model.Generation
	OperationID model.OperationID
	Leases      []*db.Lease
	Cursor      model.Cursor
	// Replay 为真表示命中幂等重放：本次没有新写任何行。
	Replay bool
}

// Service 是统一领取用例。
type Service struct {
	store Store
	nodes NodeGuard
}

// New 组装领取服务；nodes 可为 nil（未接入节点模型时只做任务与目录检查）。
func New(store Store, nodes NodeGuard) *Service {
	return &Service{store: store, nodes: nodes}
}

// Claim 执行一次领取。
//
// 返回的错误区分 no_ready、conflict、revision_mismatch、capacity_exhausted 与
// idempotent_replay；指定题失败不会改领别的题。
func (s *Service) Claim(ctx context.Context, req Request) (Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if err := model.Err(req.Validate()); err != nil {
		return Outcome{}, err
	}
	var out Outcome
	err := s.store.Write(func(tx *sql.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		got, err := s.claimTx(tx, req)
		if err != nil {
			return err
		}
		out = *got
		return nil
	})
	if err != nil {
		return Outcome{}, err
	}
	return out, nil
}

type claimState struct {
	task   *db.Task
	inst   *model.TaskInstance
	all    []*db.Task
	shared []db.SharedFileRow
}

func (s *Service) claimTx(tx *sql.Tx, req Request) (*Outcome, error) {
	projectID := string(req.ProjectID)
	all, err := db.ListTasks(tx, projectID)
	if err != nil {
		return nil, err
	}
	shared, err := db.ListSharedFiles(tx, projectID)
	if err != nil {
		return nil, err
	}

	cs := &claimState{all: all, shared: shared}
	if req.RequestedTask != nil {
		cs.task, cs.inst, err = specifiedTarget(tx, projectID, *req.RequestedTask, all)
	} else {
		cs.task, err = selectTarget(all, shared, string(req.AgentID))
		if err == nil {
			cs.inst, err = db.GetInstance(tx, cs.task.InstanceID)
		}
	}
	if err != nil {
		return nil, err
	}
	if cs.inst == nil {
		return nil, model.Violationf(model.CodeTaskFieldInvalid, "claim.task_instance",
			"任务 %q 没有可用的实例身份，先完成身份迁移", cs.task.ID)
	}
	if string(cs.inst.Legacy.ProjectID) != projectID {
		return nil, model.PortFailuref(model.PortErrUnknownIdentity, "实例 %q 不属于工程 %s",
			cs.inst.InstanceID, projectID)
	}
	if cs.inst.State == model.InstanceStateRetired {
		return nil, model.PortFailuref(model.PortErrConflict, "实例 %q 已退役，不能领取",
			cs.inst.InstanceID)
	}
	if cs.inst.ActiveVersion == nil {
		return nil, model.Violation(model.CodeActiveVersionDangling, "claim.task_version",
			"实例没有活跃版本，拒绝以不明定义领取")
	}
	version := *cs.inst.ActiveVersion
	gen, err := resolveGeneration(tx, projectID, string(cs.inst.InstanceID), req.Generation)
	if err != nil {
		return nil, err
	}
	digest := db.RequestDigest(projectID, string(cs.inst.InstanceID),
		strconv.Itoa(int(version)), strconv.Itoa(int(gen)),
		string(req.NodeID), req.Workspace, string(req.AgentID))

	if req.NodeID == "" {
		return s.claimUnbound(tx, req, cs, version, gen, digest)
	}
	return s.claimManaged(tx, req, cs, version, gen, digest)
}

// claimManaged 走受管路径：占节点槽与目录、建 Run 与 Dispatch Operation。
func (s *Service) claimManaged(tx *sql.Tx, req Request, cs *claimState, version model.VersionNo,
	gen model.Generation, digest string) (*Outcome, error) {
	// 只有 ready 的新领取才做资源预检；重放时任务已是 claimed，交给 ClaimRun 的
	// 幂等分支返回原结果，避免「节点刚好占满」把一次合法重放判成失败。
	if cs.task.Status == string(model.TaskReady) {
		if err := s.checkNode(tx, req); err != nil {
			return nil, err
		}
		if err := checkClaimable(cs, req.ExpectedRevision); err != nil {
			return nil, err
		}
	}
	out, err := db.ClaimRun(tx, db.ClaimSpec{
		ProjectID:      string(req.ProjectID),
		InstanceID:     string(cs.inst.InstanceID),
		VersionNo:      version,
		Generation:     gen,
		NodeID:         string(req.NodeID),
		Workspace:      req.Workspace,
		Coordinator:    string(req.AgentID),
		IdempotencyKey: req.IdempotencyKey,
		RequestDigest:  digest,
	})
	if err != nil {
		return nil, err
	}
	res := &Outcome{
		TaskID:      cs.task.ID,
		Instance:    cs.inst.InstanceID,
		Version:     version,
		Revision:    model.Revision(cs.task.Revision),
		RunID:       model.RunID(out.Run.ID),
		Generation:  out.Run.Generation,
		OperationID: model.OperationID(out.Operation.ID),
		Leases:      out.Leases,
		Cursor:      out.Cursor,
		Replay:      out.Replay,
	}
	if out.Replay {
		return res, nil
	}
	if err := markClaimed(tx, req, cs.task, map[string]any{
		"run_id": out.Run.ID, "generation": int(out.Run.Generation),
		"node_id": string(req.NodeID), "workspace": req.Workspace,
	}); err != nil {
		return nil, err
	}
	// CLI 自领的 Operation 不再等协调者：本事务内落 applied，语义是「这次领取已登记」，
	// 而不是「还有一个待启动的 Runner」。
	if req.SettleDispatchOperation {
		if err := db.CompleteOperation(tx, out.Operation.ID, "", model.ResultApplied); err != nil {
			return nil, err
		}
	}
	res.Revision = model.Revision(cs.task.Revision)
	return res, nil
}

// claimUnbound 是未绑定受管节点的人工领取：只占目录，不占节点槽、不建 Run。
//
// Operation 在本事务内直接标 applied：它记录「这次领取已经发生」供幂等重放，
// 不是一个仍待派发的待办，因此不会把人工领取误当成受管执行。
func (s *Service) claimUnbound(tx *sql.Tx, req Request, cs *claimState, version model.VersionNo,
	gen model.Generation, digest string) (*Outcome, error) {
	op, replay, err := db.CreateOperation(tx, db.OperationSpec{
		ProjectID:      string(req.ProjectID),
		Kind:           model.OpDispatch,
		InstanceID:     string(cs.inst.InstanceID),
		IdempotencyKey: req.IdempotencyKey,
		RequestDigest:  digest,
	})
	if err != nil {
		return nil, err
	}
	if replay {
		leases, err := activeLeasesOf(tx, string(req.ProjectID), string(cs.inst.InstanceID))
		if err != nil {
			return nil, err
		}
		return &Outcome{
			TaskID: cs.task.ID, Instance: cs.inst.InstanceID, Version: version,
			Revision: model.Revision(cs.task.Revision), Generation: gen,
			OperationID: model.OperationID(op.ID), Leases: leases, Replay: true,
		}, nil
	}
	if err := checkClaimable(cs, req.ExpectedRevision); err != nil {
		return nil, err
	}
	lease, err := db.AcquireLease(tx, db.LeaseSpec{
		ProjectID:  string(req.ProjectID),
		InstanceID: string(cs.inst.InstanceID),
		Generation: gen,
		Holder:     string(req.AgentID),
	}, db.Resource{Kind: model.LeaseWorkspace, Path: req.Workspace})
	if err != nil {
		return nil, err
	}
	if err := db.CompleteOperation(tx, op.ID, "", model.ResultApplied); err != nil {
		return nil, err
	}
	if err := markClaimed(tx, req, cs.task, map[string]any{
		"workspace": req.Workspace, "unbound": true,
	}); err != nil {
		return nil, err
	}
	return &Outcome{
		TaskID: cs.task.ID, Instance: cs.inst.InstanceID, Version: version,
		Revision: model.Revision(cs.task.Revision), Generation: gen,
		OperationID: model.OperationID(op.ID), Leases: []*db.Lease{lease},
	}, nil
}

// checkNode 在领取前核对节点能否接活：容量、就绪、能力。
func (s *Service) checkNode(tx *sql.Tx, req Request) error {
	if req.Capacity.ManagedLimit > 0 {
		runs, err := db.ListRuns(tx, string(req.ProjectID))
		if err != nil {
			return err
		}
		active := 0
		for _, r := range runs {
			if r.Occupies() {
				active++
			}
		}
		if active >= req.Capacity.ManagedLimit {
			return model.PortFailuref(model.PortErrCapacityExhausted,
				"受管执行已达到全局上限 %d", req.Capacity.ManagedLimit)
		}
	}
	node, err := db.GetNode(tx, string(req.NodeID))
	if err != nil {
		return err
	}
	if node == nil {
		return model.PortFailuref(model.PortErrInvalidSpec, "节点 %q 未登记", req.NodeID)
	}
	capacity, err := db.Capacity(tx, string(req.NodeID))
	if err != nil {
		return err
	}
	if req.Capacity.NodeSlotLimit > 0 && capacity.ActiveSlots >= req.Capacity.NodeSlotLimit {
		return model.PortFailuref(model.PortErrCapacityExhausted,
			"节点 %q 已用 %d 个槽，达到请求上限 %d",
			req.NodeID, capacity.ActiveSlots, req.Capacity.NodeSlotLimit)
	}
	if capacity.FreeSlots <= 0 {
		return model.PortFailuref(model.PortErrCapacityExhausted,
			"节点 %q 的 %d 个槽位已占满", req.NodeID, capacity.SlotLimit)
	}
	if s.nodes == nil {
		return nil
	}
	ok, missing, err := s.nodes.Dispatchable(req.NodeID, req.RequiredCapabilities)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return model.PortFailuref(model.PortErrCapabilityMissing,
			"节点 %q 缺少能力：%s", req.NodeID, strings.Join(missing, ", "))
	}
	if !ok {
		return model.PortFailuref(model.PortErrConflict,
			"节点 %q 当前不接收新任务（未就绪或 draining/disabled）", req.NodeID)
	}
	return nil
}

// markClaimed 把任务从 ready 改成 claimed 并留下两条事件。
func markClaimed(tx *sql.Tx, req Request, t *db.Task, data map[string]any) error {
	from := t.Status
	if err := state.AssertTaskTransition(from, string(model.TaskClaimed)); err != nil {
		return model.PortFailuref(model.PortErrConflict,
			"任务 %s 当前状态 %s 不能领取：%v", t.ID, from, err)
	}
	t.Status = string(model.TaskClaimed)
	t.Owner = string(req.AgentID)
	if req.Branch != "" {
		t.Branch = req.Branch
	}
	if req.Worktree != "" {
		t.Worktree = req.Worktree
	}
	t.Heartbeat = db.UTCNow()
	t.Attempts++
	t.Revision++

	payload := map[string]any{"agent": string(req.AgentID), "idempotency_key": req.IdempotencyKey, "from": from}
	for k, v := range req.ClaimedEventData {
		payload[k] = v
	}
	for k, v := range data {
		payload[k] = v
	}
	payload["revision"] = t.Revision
	if err := db.AppendEvent(tx, t.ProjectID, string(req.AgentID), "claimed", t.ID, payload); err != nil {
		return err
	}
	if err := db.UpsertTask(tx, t); err != nil {
		return err
	}
	return db.AppendEvent(tx, t.ProjectID, string(req.AgentID), "status_changed", t.ID,
		map[string]any{"from": from, "to": string(model.TaskClaimed)})
}

// checkClaimable 复述一遍领取规则：任务必须是 ready，revision 对得上，依赖／写盘／
// 共享文件都没有挡住。
func checkClaimable(cs *claimState, expected *model.Revision) error {
	t := cs.task
	if t.Status != string(model.TaskReady) {
		return model.PortFailuref(model.PortErrConflict,
			"任务 %s 当前状态 %s，只有 ready 可以被领取", t.ID, t.Status)
	}
	if expected != nil && model.Revision(t.Revision) != *expected {
		return model.PortFailuref(model.PortErrRevisionMismatch,
			"任务 %s 的 revision 已从 %d 变为 %d", t.ID, *expected, t.Revision)
	}
	if !schedule.CanActivateShared(t, cs.all, cs.shared) {
		return model.PortFailuref(model.PortErrConflict,
			"任务 %s 被资源挡住：%s", t.ID, strings.Join(blockers(t, cs.all, cs.shared), "；"))
	}
	return nil
}

// selectTarget 在 ready 任务里按调度规则选题；找不到时区分 no_ready 与 conflict。
func selectTarget(all []*db.Task, shared []db.SharedFileRow, agent string) (*db.Task, error) {
	if pick := schedule.PickNextReadyForShared(all, agent, shared); pick != nil {
		return pick, nil
	}
	ready := 0
	var blocked []string
	for _, t := range all {
		if t.Status != string(model.TaskReady) {
			continue
		}
		ready++
		blocked = append(blocked, t.ID+"（"+strings.Join(blockers(t, all, shared), ", ")+"）")
	}
	if ready == 0 {
		return nil, model.PortFailure(model.PortErrNoReady, "没有处于 ready 的任务")
	}
	sort.Strings(blocked)
	return nil, model.PortFailuref(model.PortErrConflict,
		"ready 任务都被依赖、写盘或共享文件挡住：%s", strings.Join(blocked, "；"))
}

func specifiedTarget(tx *sql.Tx, projectID string, id model.InstanceID,
	all []*db.Task) (*db.Task, *model.TaskInstance, error) {
	inst, err := db.GetInstance(tx, string(id))
	if err != nil {
		return nil, nil, err
	}
	if inst == nil {
		return nil, nil, model.PortFailuref(model.PortErrUnknownIdentity, "实例 %q 不存在", id)
	}
	if string(inst.Legacy.ProjectID) != projectID {
		return nil, nil, model.PortFailuref(model.PortErrUnknownIdentity,
			"实例 %q 属于工程 %s，不在 %s 内", id, inst.Legacy.ProjectID, projectID)
	}
	for _, t := range all {
		if t.InstanceID == string(inst.InstanceID) {
			return t, inst, nil
		}
	}
	t, err := db.GetTask(tx, projectID, string(inst.DisplayID))
	if err != nil {
		return nil, nil, err
	}
	if t == nil {
		return nil, nil, model.PortFailuref(model.PortErrUnknownIdentity,
			"实例 %q 没有对应的任务行", id)
	}
	return t, inst, nil
}

// resolveGeneration 推导本次 Run 的代际。
//
// 仍计入占用的 Run 沿用其代际：同幂等键重放时请求必须与首次一致，代际不能漂移。
// 没有占用时取最大代际 + 1；实例首次领取得到 1。
func resolveGeneration(tx *sql.Tx, projectID, instanceID string, want model.Generation) (model.Generation, error) {
	if want > 0 {
		return want, nil
	}
	runs, err := db.ListRuns(tx, projectID)
	if err != nil {
		return 0, err
	}
	var maxAll, maxOccupying model.Generation
	for _, r := range runs {
		if string(r.InstanceID) != instanceID {
			continue
		}
		if r.Generation > maxAll {
			maxAll = r.Generation
		}
		if r.Occupies() && r.Generation > maxOccupying {
			maxOccupying = r.Generation
		}
	}
	if maxOccupying > 0 {
		return maxOccupying, nil
	}
	return maxAll + 1, nil
}

// blockers 列出某任务当前被什么挡住，供 conflict 文案说明「被谁挡住」。
func blockers(t *db.Task, all []*db.Task, shared []db.SharedFileRow) []string {
	done := map[string]bool{}
	for _, o := range all {
		if o.Status == string(model.TaskDone) {
			done[o.ID] = true
		}
	}
	var out []string
	for _, d := range t.Deps {
		if !done[d] {
			out = append(out, "依赖未完成:"+d)
		}
	}
	for _, o := range all {
		if o.ID == t.ID || len(scope.ScopesOverlap(t.WriteScope, o.WriteScope)) == 0 {
			continue
		}
		if state.IsActiveOwner(o.Status) {
			out = append(out, "写盘被占用:"+o.ID)
			continue
		}
		if schedule.Incomplete(o) && schedule.Before(o, t) {
			out = append(out, "写盘序位:"+o.ID)
		}
	}
	mine := sharedPaths(t.WriteScope, shared)
	if len(mine) > 0 {
		for _, o := range all {
			if o.ID == t.ID || !state.IsActiveOwner(o.Status) {
				continue
			}
			for p := range sharedPaths(o.WriteScope, shared) {
				if mine[p] {
					out = append(out, "共享文件:"+p)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func sharedPaths(writeScope []string, shared []db.SharedFileRow) map[string]bool {
	out := map[string]bool{}
	for _, sf := range shared {
		if sf.Path == "" {
			continue
		}
		if scope.PathInScope(sf.Path, writeScope) ||
			len(scope.ScopesOverlap(writeScope, []string{sf.Path})) > 0 {
			out[sf.Path] = true
		}
	}
	return out
}

func activeLeasesOf(tx *sql.Tx, projectID, instanceID string) ([]*db.Lease, error) {
	all, err := db.ListActiveLeases(tx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]*db.Lease, 0, len(all))
	for _, l := range all {
		if l.InstanceID == instanceID {
			out = append(out, l)
		}
	}
	return out, nil
}
