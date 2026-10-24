package matching

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/scope"
)

// DefaultEvidenceTTL 是能力证据的默认时效，与节点探测时效保持一致。
const DefaultEvidenceTTL = 5 * time.Minute

// ReasonCode 是排队原因的稳定码，供看板与调用方分类展示。
type ReasonCode string

// 排队原因码。
const (
	// ReasonAutoDisabled 表示自动匹配没有显式开启。
	ReasonAutoDisabled ReasonCode = "auto_disabled"
	// ReasonManagedLimit 表示受管执行已达到全局自动上限。
	ReasonManagedLimit ReasonCode = "managed_execution_limit"
	// ReasonTaskNotReady 表示任务本身不在 ready。
	ReasonTaskNotReady ReasonCode = "task_not_ready"
	// ReasonDependencyUnmet 表示显式依赖尚未完成。
	ReasonDependencyUnmet ReasonCode = "dependency_unmet"
	// ReasonWriteConflict 表示写盘被进行中的任务占用，或排序更靠前的同类任务尚未完成。
	ReasonWriteConflict ReasonCode = "write_conflict"
	// ReasonQueuedBehind 表示题目本身可执行，但本轮已选走另一道题。
	ReasonQueuedBehind ReasonCode = "queued_behind"
	// ReasonNoCandidateNode 表示没有登记任何候选节点。
	ReasonNoCandidateNode ReasonCode = "no_candidate_node"
	// ReasonProjectPolicy 表示工程策略未允许该节点。
	ReasonProjectPolicy ReasonCode = "project_policy"
	// ReasonNodeDisabled 表示节点已停用，不再接收新任务。
	ReasonNodeDisabled ReasonCode = "node_disabled"
	// ReasonNodeDraining 表示节点正在排空，只停止接新任务。
	ReasonNodeDraining ReasonCode = "node_draining"
	// ReasonNodeStale 表示节点状态或能力证据已过期，不能当作永久可用。
	ReasonNodeStale ReasonCode = "node_stale"
	// ReasonNodeNotReady 表示入口、认证、连接或容量维度尚未就绪。
	ReasonNodeNotReady ReasonCode = "node_not_ready"
	// ReasonNodeOccupied 表示节点槽或共享资源组已被占满。
	ReasonNodeOccupied ReasonCode = "node_occupied"
	// ReasonCapabilityUnknown 表示要求的能力没有本版本、未过期的 observed 证据。
	ReasonCapabilityUnknown ReasonCode = "capability_unknown"
)

// QueueReason 是一条可展示的排队原因。TaskID 或 NodeID 为空表示全局原因。
type QueueReason struct {
	Code                ReasonCode   `json:"code"`
	TaskID              string       `json:"task_id,omitempty"`
	NodeID              model.NodeID `json:"node_id,omitempty"`
	Detail              string       `json:"detail,omitempty"`
	MissingCapabilities []string     `json:"missing_capabilities,omitempty"`
}

// String 把一条原因渲染成便于日志与看板的一行文本。
func (r QueueReason) String() string {
	var who []string
	if r.TaskID != "" {
		who = append(who, "task="+r.TaskID)
	}
	if r.NodeID != "" {
		who = append(who, "node="+string(r.NodeID))
	}
	prefix := string(r.Code)
	if len(who) > 0 {
		prefix += "(" + strings.Join(who, " ") + ")"
	}
	if r.Detail == "" {
		return prefix
	}
	return prefix + ": " + r.Detail
}

// Policy 是用户工程策略。零值表示自动匹配关闭。
type Policy struct {
	AutoExecution model.AutoExecution `json:"auto_execution"`
	// EvidenceTTL 覆盖能力证据时效；0 表示使用 DefaultEvidenceTTL。
	EvidenceTTL time.Duration `json:"evidence_ttl,omitempty"`
	// Projects 是按键工程覆盖的节点策略；未命中的工程不限制节点。
	Projects []ProjectRule `json:"projects,omitempty"`
}

// ProjectRule 是单个工程的节点策略：AllowedNodes 为空表示不限制，
// PreferredNodes 只在可执行候选中调整顺序，不放宽就绪或容量条件。
type ProjectRule struct {
	ProjectID      model.ProjectID `json:"project_id"`
	AllowedNodes   []model.NodeID  `json:"allowed_nodes,omitempty"`
	PreferredNodes []model.NodeID  `json:"preferred_nodes,omitempty"`
}

// Task 是匹配所需的只读任务投影。
type Task struct {
	ID                   string           `json:"id"`
	ProjectID            model.ProjectID  `json:"project_id,omitempty"`
	Status               model.TaskStatus `json:"status"`
	Priority             int              `json:"priority"`
	MergeOrder           int              `json:"merge_order"`
	Deps                 []string         `json:"deps,omitempty"`
	WriteScope           []string         `json:"write_scope,omitempty"`
	RequiredCapabilities []string         `json:"required_capabilities,omitempty"`
}

// Capability 是一项能力的验证值。State 只应填验证结果，声明值不能参与匹配。
type Capability struct {
	Name        string                `json:"name"`
	State       model.CapabilityState `json:"state"`
	Version     model.SemVer          `json:"version"`
	EvidenceRef string                `json:"evidence"`
	VerifiedAt  time.Time             `json:"verified_at,omitempty"`
}

// matchable 报告该能力能否参与自动匹配：只有带版本、带证据、未过期的 observed 可以。
func (c Capability) matchable(now time.Time, ttl time.Duration) bool {
	if c.State != model.CapabilityObserved {
		return false
	}
	if !c.Version.Valid() || strings.TrimSpace(c.EvidenceRef) == "" {
		return false
	}
	if c.VerifiedAt.IsZero() {
		return false
	}
	if ttl > 0 && now.Sub(c.VerifiedAt) > ttl {
		return false
	}
	return true
}

// Node 是匹配所需的只读节点投影。四维状态、管理侧接收策略与能力证据分开存放。
type Node struct {
	ID           model.NodeID         `json:"node_id"`
	ProjectID    model.ProjectID      `json:"project_id,omitempty"`
	Config       model.NodeConfig     `json:"config"`
	Draining     bool                 `json:"draining,omitempty"`
	Entry        model.EntryState     `json:"entry"`
	Auth         model.ReadinessState `json:"auth"`
	Connection   model.ReadinessState `json:"connection"`
	Capacity     model.ReadinessState `json:"capacity"`
	ObservedAt   time.Time            `json:"observed_at,omitempty"`
	ActiveSlots  int                  `json:"active_slots"`
	SharedGroup  string               `json:"shared_group,omitempty"`
	GroupLimit   int                  `json:"group_limit,omitempty"`
	GroupActive  int                  `json:"group_active,omitempty"`
	Capabilities []Capability         `json:"capabilities,omitempty"`
}

// Request 是一次匹配请求：全部输入只读，匹配本身不改任何状态。
type Request struct {
	Policy        Policy    `json:"policy"`
	Tasks         []Task    `json:"tasks"`
	Nodes         []Node    `json:"nodes"`
	ActiveManaged int       `json:"active_managed"`
	Now           time.Time `json:"now,omitempty"`
}

// Decision 是一次匹配结论。
type Decision struct {
	Matched bool          `json:"matched"`
	Mode    string        `json:"mode"`
	TaskID  string        `json:"task_id,omitempty"`
	NodeID  model.NodeID  `json:"node_id,omitempty"`
	Reasons []QueueReason `json:"reasons,omitempty"`
}

// 两种入口。
const (
	ModeAuto   = "auto"
	ModeManual = "manual"
)

// Validate 拒绝不完整的策略与投影。
func (r Request) Validate() []*model.Error {
	var out []*model.Error
	ae := r.Policy.AutoExecution
	if ae.Enabled && ae.ManagedExecutionLimit < 1 {
		out = append(out, model.Violation(model.CodeAutoExecutionLimit,
			"policy.auto_execution.managed_execution_limit", "开启自动执行时必须配置正整数上限"))
	}
	if r.Policy.EvidenceTTL < 0 {
		out = append(out, model.Violation(model.CodePortFieldInvalid,
			"policy.evidence_ttl", "证据时效不得为负"))
	}
	if r.ActiveManaged < 0 {
		out = append(out, model.Violation(model.CodePortFieldInvalid,
			"active_managed", "受管执行占用数不得为负"))
	}

	seenNode := make(map[model.NodeID]bool, len(r.Nodes))
	for i, n := range r.Nodes {
		p := fmt.Sprintf("nodes[%d]", i)
		if !n.ID.Valid() {
			out = append(out, model.Violation(model.CodePortFieldInvalid, p+".node_id", "节点身份形态非法"))
		}
		if seenNode[n.ID] {
			out = append(out, model.Violationf(model.CodePortFieldInvalid, p+".node_id", "%q 重复", n.ID))
		}
		seenNode[n.ID] = true
		if n.Config.SlotLimit < 0 {
			out = append(out, model.Violation(model.CodePortFieldInvalid, p+".config.slot_limit", "槽数不得为负"))
		}
		if n.ActiveSlots < 0 || n.GroupActive < 0 {
			out = append(out, model.Violation(model.CodePortFieldInvalid, p, "占用数不得为负"))
		}
		group := strings.TrimSpace(n.SharedGroup)
		if group != n.SharedGroup {
			out = append(out, model.Violation(model.CodePortFieldInvalid, p+".shared_group", "分组名不能带首尾空白"))
		}
		if n.SharedGroup != "" && n.GroupLimit < 1 {
			out = append(out, model.Violation(model.CodePortFieldInvalid, p+".group_limit",
				"声明共享资源组时必须显式给出 >=1 的组上限"))
		}
		if n.SharedGroup == "" && n.GroupLimit != 0 {
			out = append(out, model.Violation(model.CodePortFieldInvalid, p+".group_limit",
				"未声明共享资源组时不能给组上限"))
		}
	}

	seenTask := make(map[string]bool, len(r.Tasks))
	for i, t := range r.Tasks {
		p := fmt.Sprintf("tasks[%d]", i)
		if strings.TrimSpace(t.ID) == "" {
			out = append(out, model.Violation(model.CodePortFieldInvalid, p+".id", "必须给任务编号"))
		}
		if seenTask[t.ID] {
			out = append(out, model.Violationf(model.CodePortFieldInvalid, p+".id", "%q 重复", t.ID))
		}
		seenTask[t.ID] = true
		if t.ProjectID != "" && !t.ProjectID.Valid() {
			out = append(out, model.Violation(model.CodePortFieldInvalid, p+".project_id", "必须是 16 位小写十六进制"))
		}
		if !t.Status.Valid() {
			out = append(out, model.Violationf(model.CodePortFieldInvalid, p+".status", "未知任务状态 %q", t.Status))
		}
	}

	seenProject := make(map[model.ProjectID]bool, len(r.Policy.Projects))
	for i, rule := range r.Policy.Projects {
		p := fmt.Sprintf("policy.projects[%d]", i)
		if !rule.ProjectID.Valid() {
			out = append(out, model.Violation(model.CodePortFieldInvalid, p+".project_id", "必须是 16 位小写十六进制"))
		}
		if seenProject[rule.ProjectID] {
			out = append(out, model.Violationf(model.CodePortFieldInvalid, p+".project_id", "%q 重复", rule.ProjectID))
		}
		seenProject[rule.ProjectID] = true
	}
	return out
}

// Match 执行一次自动匹配。返回 Decision 的 Reasons 解释落选题与每个未匹配任务的原因。
func Match(req Request) (Decision, error) {
	if err := model.Err(req.Validate()); err != nil {
		return Decision{}, err
	}
	dec := Decision{Mode: ModeAuto}
	if !req.Policy.AutoExecution.Enabled {
		dec.Reasons = append(dec.Reasons, QueueReason{
			Code:   ReasonAutoDisabled,
			Detail: "自动匹配默认关闭：需显式开启并配置受管执行上限",
		})
		return dec, nil
	}
	if req.ActiveManaged >= req.Policy.AutoExecution.ManagedExecutionLimit {
		dec.Reasons = append(dec.Reasons, QueueReason{
			Code: ReasonManagedLimit,
			Detail: fmt.Sprintf("受管执行已达到全局上限 %d",
				req.Policy.AutoExecution.ManagedExecutionLimit),
		})
		return dec, nil
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	ttl := req.Policy.EvidenceTTL
	if ttl == 0 {
		ttl = DefaultEvidenceTTL
	}
	done := doneSet(req.Tasks)
	for _, t := range orderedTasks(req.Tasks) {
		reasons, eligible := taskQueue(t, req.Tasks, done)
		if !eligible {
			dec.Reasons = append(dec.Reasons, reasons...)
			continue
		}
		node, nodeReasons := pickNode(req, t, now, ttl)
		if node != nil {
			if !dec.Matched {
				dec.Matched = true
				dec.TaskID = t.ID
				dec.NodeID = node.ID
			} else {
				dec.Reasons = append(dec.Reasons, QueueReason{
					Code:   ReasonQueuedBehind,
					TaskID: t.ID,
					Detail: "本轮已选中 " + dec.TaskID,
				})
			}
			continue
		}
		dec.Reasons = append(dec.Reasons, nodeReasons...)
	}
	return dec, nil
}

// PickManual 是人工 next 的选择：与自动匹配共用任务队列、依赖与写盘序位规则，
// 但完全不读自动执行开关与受管执行上限，因此自动配额不会偷换人工领取的语义。
func PickManual(tasks []Task) Decision {
	dec := Decision{Mode: ModeManual}
	done := doneSet(tasks)
	for _, t := range orderedTasks(tasks) {
		reasons, eligible := taskQueue(t, tasks, done)
		if !eligible {
			dec.Reasons = append(dec.Reasons, reasons...)
			continue
		}
		if !dec.Matched {
			dec.Matched = true
			dec.TaskID = t.ID
			continue
		}
		dec.Reasons = append(dec.Reasons, QueueReason{
			Code:   ReasonQueuedBehind,
			TaskID: t.ID,
			Detail: "本轮已选中 " + dec.TaskID,
		})
	}
	return dec
}

// orderedTasks 返回按 priority→merge_order→id 排序的副本；输入顺序不影响结果。
func orderedTasks(tasks []Task) []Task {
	out := append([]Task(nil), tasks...)
	sort.SliceStable(out, func(i, j int) bool { return before(out[i], out[j]) })
	return out
}

func before(a, b Task) bool {
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if a.MergeOrder != b.MergeOrder {
		return a.MergeOrder < b.MergeOrder
	}
	return a.ID < b.ID
}

func doneSet(tasks []Task) map[string]bool {
	done := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		if t.Status == model.TaskDone {
			done[t.ID] = true
		}
	}
	return done
}

func incomplete(t Task) bool {
	return t.Status != model.TaskDone && t.Status != model.TaskCancelled
}

// taskQueue 判断任务能否进入候选：必须 ready、依赖满足、没有被写盘序位或活跃占用挡住。
func taskQueue(t Task, all []Task, done map[string]bool) ([]QueueReason, bool) {
	if t.Status != model.TaskReady {
		if t.Status.Terminal() {
			return nil, false
		}
		return []QueueReason{{
			Code:   ReasonTaskNotReady,
			TaskID: t.ID,
			Detail: "任务状态为 " + string(t.Status),
		}}, false
	}
	var reasons []QueueReason
	var missing []string
	for _, d := range t.Deps {
		if !done[d] {
			missing = append(missing, d)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		reasons = append(reasons, QueueReason{
			Code:   ReasonDependencyUnmet,
			TaskID: t.ID,
			Detail: "依赖未完成: " + strings.Join(missing, ", "),
		})
	}
	var blockers []string
	for _, o := range all {
		if o.ID == t.ID || !incomplete(o) {
			continue
		}
		if len(scope.ScopesOverlap(t.WriteScope, o.WriteScope)) == 0 {
			continue
		}
		if o.Status.IsActiveOwner() || before(o, t) {
			blockers = append(blockers, o.ID)
		}
	}
	if len(blockers) > 0 {
		sort.Strings(blockers)
		reasons = append(reasons, QueueReason{
			Code:   ReasonWriteConflict,
			TaskID: t.ID,
			Detail: "写盘被占用或序位更靠前: " + strings.Join(blockers, ", "),
		})
	}
	return reasons, len(reasons) == 0
}

// pickNode 在可执行节点中按工程偏好序与 node_id 稳定选择；无候选时返回全部阻塞原因。
func pickNode(req Request, t Task, now time.Time, ttl time.Duration) (*Node, []QueueReason) {
	if len(req.Nodes) == 0 {
		return nil, []QueueReason{{
			Code:   ReasonNoCandidateNode,
			TaskID: t.ID,
			Detail: "没有已登记的节点",
		}}
	}
	rule := projectRule(req.Policy, t.ProjectID)
	var eligible []int
	var reasons []QueueReason
	for i := range req.Nodes {
		if reason := nodeBlocker(req, rule, t, req.Nodes[i], now, ttl); reason != nil {
			reasons = append(reasons, *reason)
			continue
		}
		eligible = append(eligible, i)
	}
	if len(eligible) == 0 {
		sort.SliceStable(reasons, func(i, j int) bool {
			if reasons[i].NodeID != reasons[j].NodeID {
				return reasons[i].NodeID < reasons[j].NodeID
			}
			return reasons[i].Code < reasons[j].Code
		})
		return nil, reasons
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		a, b := req.Nodes[eligible[i]], req.Nodes[eligible[j]]
		pa, pb := preferIndex(rule, a.ID), preferIndex(rule, b.ID)
		if pa >= 0 || pb >= 0 {
			if pa < 0 {
				return false
			}
			if pb < 0 {
				return true
			}
			if pa != pb {
				return pa < pb
			}
		}
		return a.ID < b.ID
	})
	node := req.Nodes[eligible[0]]
	return &node, nil
}

// nodeBlocker 返回该节点相对于任务的第一条阻塞原因；可执行时返回 nil。
func nodeBlocker(req Request, rule *ProjectRule, t Task, n Node, now time.Time, ttl time.Duration) *QueueReason {
	blocked := func(code ReasonCode, detail string) *QueueReason {
		return &QueueReason{Code: code, TaskID: t.ID, NodeID: n.ID, Detail: detail}
	}
	if rule != nil && len(rule.AllowedNodes) > 0 && !containsNode(rule.AllowedNodes, n.ID) {
		return blocked(ReasonProjectPolicy, "工程策略未允许该节点")
	}
	if !n.Config.AcceptNew {
		return blocked(ReasonNodeDisabled, "节点已停用接收新任务")
	}
	if n.Draining {
		return blocked(ReasonNodeDraining, "节点正在排空，只停止接新任务")
	}
	if n.Config.Mode != model.NodeManagedCLI {
		return blocked(ReasonNodeNotReady, "接入模式 "+string(n.Config.Mode)+" 不参与受管自动执行")
	}
	if n.ObservedAt.IsZero() || (ttl > 0 && now.Sub(n.ObservedAt) > ttl) {
		return blocked(ReasonNodeStale, "节点观测或能力证据已过期")
	}
	switch n.Entry {
	case model.EntryReady:
	default:
		return blocked(ReasonNodeNotReady, "入口状态为 "+string(n.Entry))
	}
	if n.Auth != model.ReadyOK {
		return blocked(ReasonNodeNotReady, "认证状态为 "+string(n.Auth))
	}
	if n.Connection != model.ReadyFresh {
		return blocked(ReasonNodeNotReady, "连接状态为 "+string(n.Connection))
	}
	switch n.Capacity {
	case model.ReadyAvailable:
	case model.ReadyOccupied:
		return blocked(ReasonNodeOccupied, "容量状态为 occupied")
	default:
		return blocked(ReasonNodeNotReady, "容量状态为 "+string(n.Capacity))
	}
	if free := n.Config.SlotLimit - n.ActiveSlots; free <= 0 {
		return blocked(ReasonNodeOccupied,
			fmt.Sprintf("槽位已占满（%d/%d）", n.ActiveSlots, n.Config.SlotLimit))
	}
	if n.SharedGroup != "" && n.GroupLimit > 0 && n.GroupActive >= n.GroupLimit {
		return blocked(ReasonNodeOccupied,
			fmt.Sprintf("共享资源组 %s 已占满（%d/%d）", n.SharedGroup, n.GroupActive, n.GroupLimit))
	}
	if missing := missingCapabilities(n, t.RequiredCapabilities, now, ttl); len(missing) > 0 {
		return &QueueReason{
			Code:                ReasonCapabilityUnknown,
			TaskID:              t.ID,
			NodeID:              n.ID,
			Detail:              "缺少已验证能力: " + strings.Join(missing, ", "),
			MissingCapabilities: missing,
		}
	}
	return nil
}

func missingCapabilities(n Node, required []string, now time.Time, ttl time.Duration) []string {
	var missing []string
	for _, want := range required {
		if !hasMatchable(n.Capabilities, want, now, ttl) {
			missing = append(missing, want)
		}
	}
	sort.Strings(missing)
	return missing
}

func hasMatchable(caps []Capability, name string, now time.Time, ttl time.Duration) bool {
	for _, c := range caps {
		if c.Name == name {
			return c.matchable(now, ttl)
		}
	}
	return false
}

func projectRule(p Policy, id model.ProjectID) *ProjectRule {
	if id == "" {
		return nil
	}
	for i := range p.Projects {
		if p.Projects[i].ProjectID == id {
			return &p.Projects[i]
		}
	}
	return nil
}

func preferIndex(rule *ProjectRule, id model.NodeID) int {
	if rule == nil {
		return -1
	}
	for i, want := range rule.PreferredNodes {
		if want == id {
			return i
		}
	}
	return -1
}

func containsNode(ids []model.NodeID, id model.NodeID) bool {
	for _, want := range ids {
		if want == id {
			return true
		}
	}
	return false
}
