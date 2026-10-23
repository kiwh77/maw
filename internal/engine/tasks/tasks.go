package tasks

import (
	"sort"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// Mode 是蓝图应用方式。
type Mode string

// 增量（默认）与清空重建。
const (
	ModeIncremental Mode = "incremental"
	ModeReset       Mode = "reset"
)

// Valid 报告模式取值是否合法。
func (m Mode) Valid() bool {
	return m == ModeIncremental || m == ModeReset
}

// DefaultRetireReason 是蓝图移除后实例归档的默认原因。
const DefaultRetireReason = "removed_from_plan"

// Desired 是蓝图里的一条期望定义；DisplayID 用于对齐已有活跃实例。
type Desired struct {
	DisplayID model.DisplayID
	Title     string
	Deps      []model.Dep
}

// ActiveRun 是仍在运行的 Run 冻结的实例与版本；规划只读它，不改写。
type ActiveRun struct {
	RunID    model.RunID
	Instance model.InstanceID
	Version  model.VersionNo
}

// PinnedRun 说明蓝图更新后活跃 Run 仍固定在旧版本。
type PinnedRun struct {
	ActiveRun
	Note string `json:"note"`
}

// NewInstance 是计划中新建的实例。
type NewInstance struct {
	InstanceID model.InstanceID `json:"instance_id"`
	DisplayID  model.DisplayID  `json:"display_id"`
	Version    model.VersionNo  `json:"version_no"`
}

// VersionAddition 是计划中追加的冻结版本。
type VersionAddition struct {
	InstanceID model.InstanceID `json:"instance_id"`
	DisplayID  model.DisplayID  `json:"display_id"`
	Version    model.VersionNo  `json:"version_no"`
	FrozenAt   time.Time        `json:"frozen_at"`
}

// Retirement 是计划中归档的实例；versions 保留为只读快照。
type Retirement struct {
	InstanceID model.InstanceID `json:"instance_id"`
	DisplayID  model.DisplayID  `json:"display_id"`
	Reason     string           `json:"reason"`
	RetiredAt  time.Time        `json:"retired_at"`
	Versions   int              `json:"preserved_versions"`
}

// Plan 是一次规划结果；Snapshot 是应用后应落库的完整身份快照。
// 调用方必须把 Creates、Freezes、Retires 放在同一事务里应用。
type Plan struct {
	Mode      Mode                `json:"mode"`
	Creates   []NewInstance       `json:"creates,omitempty"`
	Freezes   []VersionAddition   `json:"freezes,omitempty"`
	Retires   []Retirement        `json:"retires,omitempty"`
	Unchanged []model.InstanceID  `json:"unchanged,omitempty"`
	Pinned    []PinnedRun         `json:"pinned,omitempty"`
	Snapshot  *model.TaskSnapshot `json:"snapshot"`
	Notes     []string            `json:"notes,omitempty"`
}

// PlanRequest 是一次增量或 reset 规划。
type PlanRequest struct {
	ProjectID model.ProjectID
	// Snapshot 是当前身份快照；nil 视为空工程。
	Snapshot   *model.TaskSnapshot
	Desired    []Desired
	Mode       Mode
	ActiveRuns []ActiveRun
	// RetireReason 缺省为 removed_from_plan。
	RetireReason string
	Now          time.Time
}

// Service 是任务应用服务；所有入口只读输入并返回计划。
type Service struct {
	ids IDAllocator
	now func() time.Time
}

// New 创建服务；ids 为 nil 时使用随机实例号分配器。
func New(ids IDAllocator) *Service {
	if ids == nil {
		ids = RandomIDs{}
	}
	return &Service{ids: ids, now: func() time.Time { return time.Now().UTC().Truncate(time.Second) }}
}

// Plan 计算增量或 reset 后的实例与版本变化。
//
// 默认增量保留未变化的实例与既有交付；定义变化只追加版本。reset 先把所有旧
// 活跃实例退役为快照，再用全新 InstanceID 重建，绝不复用旧实例，也不触碰
// 运行占用。
func (s *Service) Plan(req PlanRequest) (*Plan, error) {
	if !req.ProjectID.Valid() {
		return nil, model.Violation(model.CodeTaskFieldInvalid, "project_id", "必须是 16 位小写十六进制")
	}
	mode := req.Mode
	if mode == "" {
		mode = ModeIncremental
	}
	if !mode.Valid() {
		return nil, model.Violationf(model.CodeTaskFieldInvalid, "mode", "只能是 incremental 或 reset，收到 %q", mode)
	}
	if errs := checkDesired(req.Desired); len(errs) > 0 {
		return nil, errs[0]
	}
	now := req.Now
	if now.IsZero() {
		now = s.now()
	}
	reason := req.RetireReason
	if reason == "" {
		reason = DefaultRetireReason
		if mode == ModeReset {
			reason = "reset"
		}
	}
	snap := cloneSnapshot(req.Snapshot)
	if snap == nil {
		snap = &model.TaskSnapshot{}
	}
	if snap.ProjectID != "" && snap.ProjectID != req.ProjectID {
		return nil, model.Violationf(model.CodeTaskFieldInvalid, "snapshot.project_id",
			"快照属于工程 %s，请求为 %s", snap.ProjectID, req.ProjectID)
	}
	snap.Contract = model.TaskContract
	snap.ProjectID = req.ProjectID

	// 第一步：为每条期望定义确定实例。增量复用同名活跃实例；新建与 reset 一律新 ID。
	assigned := make(map[model.DisplayID]model.InstanceID, len(req.Desired))
	existing := make(map[model.InstanceID]bool, len(snap.Instances))
	for i := range snap.Instances {
		existing[snap.Instances[i].InstanceID] = true
	}

	plan := &Plan{Mode: mode}
	for _, d := range req.Desired {
		if mode == ModeIncremental {
			if inst := activeInstance(snap, d.DisplayID); inst != nil {
				assigned[d.DisplayID] = inst.InstanceID
				continue
			}
		}
		// 新建实例（reset 或无活跃同名实例）。
		id, err := s.allocate(existing)
		if err != nil {
			return nil, err
		}
		existing[id] = true
		assigned[d.DisplayID] = id
	}

	// 第二步：reset 先把全部旧活跃实例退役；增量只在最后清理蓝图外的活跃实例。
	result := &model.TaskSnapshot{
		Contract:    model.TaskContract,
		ProjectID:   req.ProjectID,
		GeneratedAt: now,
		Instances:   snap.Instances,
	}
	assignedID := make(map[model.InstanceID]bool, len(assigned))
	for _, id := range assigned {
		assignedID[id] = true
	}

	// 第三步：按期望顺序创建或追加版本，并解析依赖。
	for _, d := range req.Desired {
		id := assigned[d.DisplayID]
		if idx, ok := indexOf(result.Instances, id); ok && result.Instances[idx].State == model.InstanceStateActive {
			inst := &result.Instances[idx]
			deps, errs := convertDeps(d.Deps, assigned, snap, req.ProjectID)
			if len(errs) > 0 {
				return nil, errs[0]
			}
			if definitionEqual(inst.ActiveVersionOf(), d.Title, deps) {
				plan.Unchanged = append(plan.Unchanged, id)
				continue
			}
			ver := appendVersion(inst, d.Title, deps, now)
			plan.Freezes = append(plan.Freezes, VersionAddition{
				InstanceID: id, DisplayID: d.DisplayID, Version: ver, FrozenAt: inst.Versions[len(inst.Versions)-1].FrozenAt,
			})
			continue
		}
		deps, errs := convertDeps(d.Deps, assigned, snap, req.ProjectID)
		if len(errs) > 0 {
			return nil, errs[0]
		}
		inst := newInstance(req.ProjectID, d.DisplayID, id, d.Title, deps, now)
		result.Instances = append(result.Instances, inst)
		plan.Creates = append(plan.Creates, NewInstance{InstanceID: id, DisplayID: d.DisplayID, Version: 1})
	}

	// 第四步：归档不再需要的活跃实例；reset 下全部旧活跃实例都退休。
	// 本次新建或复用的实例在 assigned 里，一律跳过，避免把刚建的实例又归档。
	for i := range result.Instances {
		inst := &result.Instances[i]
		if inst.State != model.InstanceStateActive {
			continue
		}
		if assignedID[inst.InstanceID] {
			continue
		}
		retire(inst, reason, now)
		plan.Retires = append(plan.Retires, Retirement{
			InstanceID: inst.InstanceID, DisplayID: inst.DisplayID, Reason: reason,
			RetiredAt: *inst.RetiredAt, Versions: len(inst.Versions),
		})
	}
	if mode == ModeReset && len(req.Desired) == 0 {
		plan.Notes = append(plan.Notes, "reset 清空后没有期望任务：旧实例全部退役为快照")
	}
	if len(plan.Retires) > 0 && len(req.ActiveRuns) > 0 {
		plan.Notes = append(plan.Notes, "退役只封存身份快照：不释放占用，活跃 Run 继续固定旧版本")
	}

	// 第五步：活跃 Run 固定在旧版本；只校验、不改写。
	pinned, err := pinRuns(req.ActiveRuns, result, plan.Retires)
	if err != nil {
		return nil, err
	}
	plan.Pinned = pinned

	plan.Snapshot = result
	if errs := result.Validate(); len(errs) > 0 {
		return nil, errs[0]
	}
	return plan, nil
}

// ArchiveRequest 是显式归档一条实例。
type ArchiveRequest struct {
	ProjectID model.ProjectID
	Snapshot  *model.TaskSnapshot
	Instance  model.InstanceID
	Reason    string
	Now       time.Time
}

// Archive 归档单个实例并保留其版本快照；不会复用该 InstanceID。
func (s *Service) Archive(req ArchiveRequest) (*Plan, error) {
	if req.Reason == "" {
		return nil, model.Violation(model.CodeTaskFieldInvalid, "retired_reason", "退役必须写明原因")
	}
	if !req.Instance.Valid() {
		return nil, model.Violation(model.CodeInstanceIDShape, "instance_id", "实例号形态不合法")
	}
	now := req.Now
	if now.IsZero() {
		now = s.now()
	}
	snap := cloneSnapshot(req.Snapshot)
	if snap == nil {
		snap = &model.TaskSnapshot{Contract: model.TaskContract, ProjectID: req.ProjectID, GeneratedAt: now}
	}
	snap.GeneratedAt = now
	idx, ok := indexOf(snap.Instances, req.Instance)
	if !ok {
		return nil, model.Violationf(model.CodeTaskFieldInvalid, "instance_id", "%q 不在本工程快照内", req.Instance)
	}
	inst := &snap.Instances[idx]
	if inst.State == model.InstanceStateRetired {
		return nil, model.Violationf(model.CodeTaskFieldInvalid, "instance_id", "%q 已退役", req.Instance)
	}
	retire(inst, req.Reason, now)
	plan := &Plan{
		Mode:     ModeIncremental,
		Snapshot: snap,
		Retires: []Retirement{{
			InstanceID: inst.InstanceID, DisplayID: inst.DisplayID, Reason: req.Reason,
			RetiredAt: *inst.RetiredAt, Versions: len(inst.Versions),
		}},
		Notes: []string{"归档只封存身份快照，不复用该 InstanceID"},
	}
	if errs := snap.Validate(); len(errs) > 0 {
		return nil, errs[0]
	}
	return plan, nil
}

func (s *Service) allocate(existing map[model.InstanceID]bool) (model.InstanceID, error) {
	for i := 0; i < 8; i++ {
		id, err := s.ids.New()
		if err != nil {
			return "", err
		}
		if !id.Valid() {
			return "", model.Violationf(model.CodeInstanceIDShape, "instance_id", "分配器给出非法实例号 %q", id)
		}
		if !existing[id] {
			return id, nil
		}
	}
	return "", model.Violation(model.CodeInstanceDuplicate, "instance_id", "分配器连续给出重复实例号")
}

func checkDesired(desired []Desired) []*model.Error {
	var out []*model.Error
	seen := make(map[model.DisplayID]bool, len(desired))
	for i, d := range desired {
		p := "desired[" + itoa(i) + "]"
		if !d.DisplayID.Valid() {
			out = append(out, model.Violation(model.CodeDisplayIDShape, p+".display_id", "必须形如 VN-001"))
		}
		if seen[d.DisplayID] {
			out = append(out, model.Violationf(model.CodeTaskFieldInvalid, p+".display_id", "%q 在同一蓝图内重复", d.DisplayID))
		}
		seen[d.DisplayID] = true
	}
	return out
}

// convertDeps 把期望依赖解析成 instance 形式；legacy 只读存量且歧义时拒绝。
func convertDeps(deps []model.Dep, assigned map[model.DisplayID]model.InstanceID, snap *model.TaskSnapshot, project model.ProjectID) ([]model.Dep, []*model.Error) {
	out := make([]model.Dep, 0, len(deps))
	for i, dep := range deps {
		path := "deps[" + itoa(i) + "]"
		switch dep.Kind {
		case model.DepInstance:
			out = append(out, dep)
		case model.DepLegacy:
			display := model.DisplayID(dep.Target)
			if id, ok := assigned[display]; ok {
				out = append(out, model.Dep{Kind: model.DepInstance, Target: string(id)})
				continue
			}
			hits := instancesByDisplay(snap, display)
			switch {
			case len(hits) == 0:
				return nil, []*model.Error{model.Violationf(model.CodeDepMissing, path,
					"显示编号 %q 在本工程内不存在", display)}
			case len(hits) > 1:
				return nil, []*model.Error{model.Violationf(model.CodeDepAmbiguous, path,
					"显示编号 %q 命中 %d 个实例（含退役锚点），必须改用 instance 形式消歧", display, len(hits))}
			}
			if hits[0].Legacy.ProjectID != "" && hits[0].Legacy.ProjectID != project {
				return nil, []*model.Error{model.Violationf(model.CodeDepMissing, path,
					"%q 属于其它工程", display)}
			}
			out = append(out, model.Dep{Kind: model.DepInstance, Target: string(hits[0].InstanceID)})
		default:
			return nil, []*model.Error{model.Violationf(model.CodeTaskFieldInvalid, path+".kind",
				"只能是 instance 或 legacy，收到 %q", dep.Kind)}
		}
	}
	return out, nil
}

func newInstance(project model.ProjectID, display model.DisplayID, id model.InstanceID, title string, deps []model.Dep, at time.Time) model.TaskInstance {
	version := model.VersionNo(1)
	return model.TaskInstance{
		InstanceID:    id,
		DisplayID:     display,
		State:         model.InstanceStateActive,
		CreatedAt:     at,
		ActiveVersion: &version,
		Versions: []model.TaskVersion{{
			VersionNo: 1, FrozenAt: at, Title: title, Deps: deps,
		}},
		Legacy: model.LegacyAnchor{
			ProjectID: project, DisplayID: display, PrimaryKey: model.LegacyPrimaryKey(project, display),
		},
	}
}

func appendVersion(inst *model.TaskInstance, title string, deps []model.Dep, now time.Time) model.VersionNo {
	next := maxVersion(inst.Versions) + 1
	frozenAt := now
	if last := lastFrozen(inst.Versions); !frozenAt.After(last) {
		frozenAt = last.Add(time.Second)
	}
	inst.Versions = append(inst.Versions, model.TaskVersion{
		VersionNo: next, FrozenAt: frozenAt, Title: title, Deps: deps,
	})
	inst.ActiveVersion = &next
	return next
}

func retire(inst *model.TaskInstance, reason string, now time.Time) {
	at := now
	if last := lastFrozen(inst.Versions); at.Before(last) {
		at = last
	}
	r := reason
	inst.State = model.InstanceStateRetired
	inst.RetiredAt = &at
	inst.RetiredReason = &r
	inst.ActiveVersion = nil
}

func pinRuns(runs []ActiveRun, snap *model.TaskSnapshot, retires []Retirement) ([]PinnedRun, error) {
	retired := make(map[model.InstanceID]bool, len(retires))
	for _, r := range retires {
		retired[r.InstanceID] = true
	}
	out := make([]PinnedRun, 0, len(runs))
	for _, run := range runs {
		if !run.Instance.Valid() || !run.Version.Valid() {
			return nil, model.Violation(model.CodeTaskFieldInvalid, "active_runs", "Run 必须带合法实例与版本")
		}
		inst := snap.Task(run.Instance)
		if inst == nil {
			return nil, model.Violationf(model.CodeTaskFieldInvalid, "active_runs.instance",
				"实例 %q 不在快照内", run.Instance)
		}
		if !hasVersion(inst.Versions, run.Version) {
			return nil, model.Violationf(model.CodeActiveVersionDangling, "active_runs.version",
				"版本 %d 未冻结", run.Version)
		}
		note := "活跃 Run 固定已冻结版本，蓝图更新不改变其验收"
		if retired[run.Instance] {
			note = "实例已退役为快照，但活跃 Run 仍固定旧版本且占用保持不变"
		}
		out = append(out, PinnedRun{ActiveRun: run, Note: note})
	}
	return out, nil
}

func activeInstance(snap *model.TaskSnapshot, display model.DisplayID) *model.TaskInstance {
	for i := range snap.Instances {
		if snap.Instances[i].DisplayID == display && snap.Instances[i].State == model.InstanceStateActive {
			return &snap.Instances[i]
		}
	}
	return nil
}

func instancesByDisplay(snap *model.TaskSnapshot, display model.DisplayID) []*model.TaskInstance {
	var out []*model.TaskInstance
	for i := range snap.Instances {
		if snap.Instances[i].DisplayID == display {
			out = append(out, &snap.Instances[i])
		}
	}
	return out
}

func definitionEqual(v *model.TaskVersion, title string, deps []model.Dep) bool {
	if v == nil || v.Title != title || len(v.Deps) != len(deps) {
		return false
	}
	a, b := canonicalDeps(v.Deps), canonicalDeps(deps)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func canonicalDeps(deps []model.Dep) []string {
	out := make([]string, len(deps))
	for i, d := range deps {
		out[i] = string(d.Kind) + ":" + d.Target
	}
	sort.Strings(out)
	return out
}

func indexOf(instances []model.TaskInstance, id model.InstanceID) (int, bool) {
	for i := range instances {
		if instances[i].InstanceID == id {
			return i, true
		}
	}
	return 0, false
}

func maxVersion(versions []model.TaskVersion) model.VersionNo {
	var max model.VersionNo
	for _, v := range versions {
		if v.VersionNo > max {
			max = v.VersionNo
		}
	}
	return max
}

func lastFrozen(versions []model.TaskVersion) time.Time {
	var at time.Time
	for _, v := range versions {
		if v.FrozenAt.After(at) {
			at = v.FrozenAt
		}
	}
	return at
}

func hasVersion(versions []model.TaskVersion, n model.VersionNo) bool {
	for _, v := range versions {
		if v.VersionNo == n {
			return true
		}
	}
	return false
}

func cloneSnapshot(s *model.TaskSnapshot) *model.TaskSnapshot {
	if s == nil {
		return nil
	}
	out := &model.TaskSnapshot{
		Contract: s.Contract, ProjectID: s.ProjectID, GeneratedAt: s.GeneratedAt,
		Instances: make([]model.TaskInstance, len(s.Instances)),
	}
	for i := range s.Instances {
		inst := s.Instances[i]
		inst.Versions = make([]model.TaskVersion, len(s.Instances[i].Versions))
		for vi := range inst.Versions {
			inst.Versions[vi] = s.Instances[i].Versions[vi]
			inst.Versions[vi].Deps = append([]model.Dep(nil), s.Instances[i].Versions[vi].Deps...)
		}
		if inst.RetiredAt != nil {
			v := *inst.RetiredAt
			inst.RetiredAt = &v
		}
		if inst.RetiredReason != nil {
			v := *inst.RetiredReason
			inst.RetiredReason = &v
		}
		if inst.ActiveVersion != nil {
			v := *inst.ActiveVersion
			inst.ActiveVersion = &v
		}
		out.Instances[i] = inst
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
