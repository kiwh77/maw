package goal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/kiwh77/maw/internal/scope"
)

// 计划修订状态（契约 §5，封闭集合）。
const (
	PlanProposed    = "proposed"
	PlanApproved    = "approved"
	PlanApplied     = "applied"
	PlanRejected    = "rejected"
	PlanInvalidated = "invalidated"
	PlanSuperseded  = "superseded"
)

// 计划修订类型（契约 §5）。
const (
	PlanKindInitial = "initial"
	PlanKindRework  = "rework"
)

// 审批状态（契约 §5）。
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
	ApprovalExpired  = "expired"
)

// 审批策略来源（契约 §5、§16）。
const (
	PolicyManual      = "manual"
	PolicyAutoEnqueue = "auto_enqueue"
)

// 计划判别码：与契约 §14 的 G2xx 对齐。
const (
	CodePlanRevision    = "G200"
	CodePlanDigest      = "G202"
	CodeCardField       = "G210"
	CodeCardID          = "G211"
	CodeCardDep         = "G212"
	CodeWriteScope      = "G213"
	CodeCardPlaceholder = "G214"
	CodeReworkEvidence  = "G216"
)

// reservedIDPrefixes 是任务显示编号不得占用的身份前缀（契约 §6）。
var reservedIDPrefixes = []string{"ti_", "gl_", "run_", "op_", "dlv_"}

// placeholderMarkers 是未填标记（契约 §6.1 G214）。
var placeholderMarkers = []string{"todo", "tbd", "待补", "待定", "占位", "填本卡"}

// Baseline 是计划修订读取时的蓝图基线（契约 §5）。
type Baseline struct {
	BlueprintSHA256 string `json:"blueprint_sha256"`
	TasksRevision   int    `json:"tasks_revision"`
	MainBranchTip   string `json:"main_branch_tip"`
}

// Origin 记录一张卡从哪来（契约 §6）：目标、修订、卡号与来源引用。
type Origin struct {
	GoalID     string   `json:"goal_id"`
	PlanRev    int      `json:"plan_rev"`
	CardID     string   `json:"card_id"`
	SourceRefs []string `json:"source_refs,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
}

// PlanCard 是蓝图 [[task]] 的结构化投影（契约 §6）。
type PlanCard struct {
	CardID     string   `json:"card_id"`
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Goal       string   `json:"goal"`
	Iteration  string   `json:"iteration,omitempty"`
	Priority   int      `json:"priority"`
	MergeOrder int      `json:"merge_order"`
	Deps       []string `json:"deps"`
	WriteScope []string `json:"write_scope"`
	ReadScope  []string `json:"read_scope"`
	Acceptance []string `json:"acceptance"`
	Verify     []string `json:"verify"`
	NotDoing   []string `json:"not_doing"`
	Origin     Origin   `json:"origin"`
}

// Approval 是计划修订的审批留痕（契约 §5）。
type Approval struct {
	State        string `json:"state"`
	Approver     string `json:"approver,omitempty"`
	ApprovedAt   string `json:"approved_at,omitempty"`
	PolicySource string `json:"policy_source,omitempty"`
}

// PlanRevision 是一次不可变的计划修订（契约 §5）。
type PlanRevision struct {
	ProjectID   string     `json:"project_id"`
	GoalID      string     `json:"goal_id"`
	GoalVersion int        `json:"goal_version"`
	PlanRev     int        `json:"plan_rev"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	Baseline    Baseline   `json:"baseline"`
	PlanDigest  string     `json:"plan_digest"`
	Cards       []PlanCard `json:"cards"`
	Approval    Approval   `json:"approval"`
	CreatedAt   string     `json:"created_at"`
	AppliedAt   string     `json:"applied_at,omitempty"`
}

// ValidPlanStatus 报告计划修订状态是否属于封闭集合。
func ValidPlanStatus(status string) bool {
	switch status {
	case PlanProposed, PlanApproved, PlanApplied, PlanRejected, PlanInvalidated, PlanSuperseded:
		return true
	}
	return false
}

// ValidPlanKind 报告计划修订类型是否合法。
func ValidPlanKind(kind string) bool {
	return kind == PlanKindInitial || kind == PlanKindRework
}

// ValidPolicySource 报告审批策略来源是否合法。
func ValidPolicySource(src string) bool {
	return src == PolicyManual || src == PolicyAutoEnqueue
}

// stringsOrEmpty 把 nil 归一成空切片，保证摘要不受 JSON 往返的 null/[] 差异影响。
func stringsOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// canonicalCard 把卡片摊成键序稳定的对象。
//
// encoding/json 对 map 的字符串键按字节序输出，因此同一内容必得同一串；
// 不用结构体是为了避免 nil 切片与空切片在往返后摘要漂移。
func canonicalCard(c PlanCard) map[string]any {
	deps := append([]string{}, stringsOrEmpty(c.Deps)...)
	write := append([]string{}, stringsOrEmpty(c.WriteScope)...)
	read := append([]string{}, stringsOrEmpty(c.ReadScope)...)
	accept := append([]string{}, stringsOrEmpty(c.Acceptance)...)
	verify := append([]string{}, stringsOrEmpty(c.Verify)...)
	notDoing := append([]string{}, stringsOrEmpty(c.NotDoing)...)
	refs := append([]string{}, stringsOrEmpty(c.Origin.SourceRefs)...)
	evid := append([]string{}, stringsOrEmpty(c.Origin.Evidence)...)
	return map[string]any{
		"card_id":     c.CardID,
		"id":          c.ID,
		"title":       c.Title,
		"goal":        c.Goal,
		"iteration":   c.Iteration,
		"priority":    c.Priority,
		"merge_order": c.MergeOrder,
		"deps":        deps,
		"write_scope": write,
		"read_scope":  read,
		"acceptance":  accept,
		"verify":      verify,
		"not_doing":   notDoing,
		"origin": map[string]any{
			"goal_id":     c.Origin.GoalID,
			"plan_rev":    c.Origin.PlanRev,
			"card_id":     c.Origin.CardID,
			"source_refs": refs,
			"evidence":    evid,
		},
	}
}

// canonicalPlan 是摘要的规范化载荷（契约 §5.1）：只含修订内容，不含审批字段。
func canonicalPlan(r *PlanRevision) map[string]any {
	cards := make([]any, 0, len(r.Cards))
	for _, c := range r.Cards {
		cards = append(cards, canonicalCard(c))
	}
	return map[string]any{
		"goal_id":      r.GoalID,
		"goal_version": r.GoalVersion,
		"plan_rev":     r.PlanRev,
		"kind":         r.Kind,
		"cards":        cards,
	}
}

// CanonicalJSON 返回计划修订的规范化 JSON（键序稳定、无多余空白）。
func CanonicalJSON(r *PlanRevision) (string, error) {
	if r == nil {
		return "", fmt.Errorf("%s: 计划修订为空", CodePlanDigest)
	}
	b, err := json.Marshal(canonicalPlan(r))
	if err != nil {
		return "", fmt.Errorf("%s: 计划修订无法规范化: %w", CodePlanDigest, err)
	}
	return string(b), nil
}

// Digest 计算计划修订的 plan_digest（契约 §5.1）。
func Digest(r *PlanRevision) (string, error) {
	canonical, err := CanonicalJSON(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

// EnsureDigest 在未填摘要时补齐，已填则保持原值（由 Validate 核对是否一致）。
func (r *PlanRevision) EnsureDigest() error {
	if r == nil {
		return fmt.Errorf("%s: 计划修订为空", CodePlanDigest)
	}
	if strings.TrimSpace(r.PlanDigest) != "" {
		return nil
	}
	d, err := Digest(r)
	if err != nil {
		return err
	}
	r.PlanDigest = d
	return nil
}

// EnqueueKey 计算单张卡的入队幂等键（契约 §8.1）。
//
// 键只由「批准的那一份修订」决定：同一修订重放必得同键，无需读库即可算出。
func EnqueueKey(projectID, goalID string, planRev int, planDigest, cardID string) string {
	payload := map[string]any{
		"project_id":  projectID,
		"goal_id":     goalID,
		"plan_rev":    planRev,
		"plan_digest": planDigest,
		"card_id":     cardID,
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return "gk_" + hex.EncodeToString(sum[:])
}

// ReworkKey 计算返工提案的去重键（契约 §12）：同一证据集只落一个提案。
func ReworkKey(goalID, target string, evidenceIDs []string) string {
	ids := make([]string, 0, len(evidenceIDs))
	seen := map[string]bool{}
	for _, id := range evidenceIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	payload := map[string]any{"goal_id": goalID, "target": target, "evidence": ids}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return "rw_" + hex.EncodeToString(sum[:])
}

// Violation 是一条可机读的计划／目标校验失败。
type Violation struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (v Violation) Error() string {
	return fmt.Sprintf("%s %s: %s", v.Code, v.Field, v.Message)
}

// Violations 是校验失败集合，按发现顺序保存。
type Violations []Violation

// Err 在存在失败时返回聚合错误，否则返回 nil。
func (vs Violations) Err() error {
	if len(vs) == 0 {
		return nil
	}
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		parts = append(parts, v.Error())
	}
	return fmt.Errorf("goal: 校验失败: %s", strings.Join(parts, "; "))
}

// HasCode 报告是否命中某个判别码。
func (vs Violations) HasCode(code string) bool {
	for _, v := range vs {
		if v.Code == code {
			return true
		}
	}
	return false
}

// hasPlaceholder 判断文本是否含未填标记（忽略大小写）。
func hasPlaceholder(text string) bool {
	low := strings.ToLower(strings.TrimSpace(text))
	for _, m := range placeholderMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// validScopePath 判断写盘范围是否合法（契约 §6.1 G213）。
func validScopePath(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || p == "*" || p == "**" {
		return false
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || strings.HasPrefix(p, "\\") {
		return false
	}
	// 盘符（C:\ 或 C:/）与 UNC 一律拒绝。
	if len(p) >= 2 && p[1] == ':' {
		return false
	}
	if strings.Contains(p, `\`) || strings.Contains(p, "..") {
		return false
	}
	if path.IsAbs(p) {
		return false
	}
	return true
}

// cardOrder 计算「本修订内」的依赖可达关系，用于判断写盘重叠是否有序。
//
// deps 里指向蓝图既有题的 id 不在本修订内，直接忽略：它们的有序性由
// PI-302 在合并完整蓝图后校验，本包只判本修订自洽。
func cardOrder(cards []PlanCard) map[string]map[string]bool {
	byID := make(map[string]PlanCard, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	reach := make(map[string]map[string]bool, len(cards))
	for id := range byID {
		seen := map[string]bool{}
		stack := append([]string{}, byID[id].Deps...)
		for len(stack) > 0 {
			dep := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[dep] {
				continue
			}
			seen[dep] = true
			if next, ok := byID[dep]; ok {
				stack = append(stack, next.Deps...)
			}
		}
		reach[id] = seen
	}
	return reach
}

// Validate 校验计划修订的**本修订内**不变量。
//
// 跨蓝图的部分（id 与既有题冲突、deps 指向退役实例）属于 PI-302 的合并校验，
// 这里不重复实现，只保证「这一份修订自身」可被安全落库与批准。
func (r *PlanRevision) Validate() Violations {
	var out Violations
	add := func(code, field, msg string) {
		out = append(out, Violation{Code: code, Field: field, Message: msg})
	}
	if r == nil {
		add(CodePlanRevision, "plan", "计划修订为空")
		return out
	}
	if err := ValidateGoalID(r.GoalID); err != nil {
		add(CodePlanRevision, "goal_id", err.Error())
	}
	if strings.TrimSpace(r.ProjectID) == "" {
		add(CodePlanRevision, "project_id", "缺少 project_id")
	}
	if r.PlanRev < 1 {
		add(CodePlanRevision, "plan_rev", "plan_rev 必须 >= 1")
	}
	if r.GoalVersion < 1 {
		add(CodePlanRevision, "goal_version", "goal_version 必须 >= 1")
	}
	if !ValidPlanKind(r.Kind) {
		add(CodePlanRevision, "kind", fmt.Sprintf("非法计划类型 %q", r.Kind))
	}
	if r.Status == "" {
		r.Status = PlanProposed
	}
	if !ValidPlanStatus(r.Status) {
		add(CodePlanRevision, "status", fmt.Sprintf("非法计划状态 %q", r.Status))
	}
	if r.Approval.State == "" {
		r.Approval.State = ApprovalPending
	}
	if len(r.Cards) == 0 {
		add(CodeCardField, "cards", "计划修订至少需要 1 张卡")
	}

	seenCardID := map[string]bool{}
	seenTaskID := map[string]bool{}
	reach := cardOrder(r.Cards)
	for i, c := range r.Cards {
		field := fmt.Sprintf("cards[%d]", i)
		if strings.TrimSpace(c.CardID) == "" {
			add(CodeCardID, field+".card_id", "card_id 不能为空")
		} else if seenCardID[c.CardID] {
			add(CodeCardID, field+".card_id", fmt.Sprintf("card_id %q 重复", c.CardID))
		}
		seenCardID[c.CardID] = true

		if strings.TrimSpace(c.ID) == "" {
			add(CodeCardID, field+".id", "id 不能为空")
		} else {
			if seenTaskID[c.ID] {
				add(CodeCardID, field+".id", fmt.Sprintf("id %q 在本修订内重复", c.ID))
			}
			seenTaskID[c.ID] = true
			if strings.ContainsAny(c.ID, " \t\n") {
				add(CodeCardID, field+".id", fmt.Sprintf("id %q 含空白", c.ID))
			}
			for _, p := range reservedIDPrefixes {
				if strings.HasPrefix(c.ID, p) {
					add(CodeCardID, field+".id", fmt.Sprintf("id %q 占用保留前缀 %s", c.ID, p))
				}
			}
		}
		if strings.TrimSpace(c.Title) == "" {
			add(CodeCardField, field+".title", "缺少 title")
		}
		if strings.TrimSpace(c.Goal) == "" {
			add(CodeCardField, field+".goal", "缺少 goal（现状→目标）")
		}
		if len(c.WriteScope) == 0 {
			add(CodeWriteScope, field+".write_scope", "write_scope 至少 1 条")
		}
		for _, p := range c.WriteScope {
			if !validScopePath(p) {
				add(CodeWriteScope, field+".write_scope", fmt.Sprintf("非法写盘范围 %q", p))
			}
		}
		if len(c.Acceptance) == 0 {
			add(CodeCardField, field+".acceptance", "acceptance 至少 1 条")
		}
		for _, a := range c.Acceptance {
			if hasPlaceholder(a) {
				add(CodeCardPlaceholder, field+".acceptance", fmt.Sprintf("验收条件仍是占位文本 %q", a))
			}
		}
		if len(c.Verify) == 0 {
			add(CodeCardField, field+".verify", "verify 至少 1 条")
		}
		for _, v := range c.Verify {
			if hasPlaceholder(v) {
				add(CodeCardPlaceholder, field+".verify", fmt.Sprintf("验证命令仍是占位文本 %q", v))
			}
		}
		for _, d := range c.Deps {
			if strings.TrimSpace(d) == c.ID {
				add(CodeCardDep, field+".deps", "不得依赖自身")
			}
		}
		if len(c.WriteScope) > 0 && reach[c.ID][c.ID] {
			add(CodeCardDep, field+".deps", "依赖成环")
		}
		if r.Kind == PlanKindRework && len(c.Origin.Evidence) == 0 {
			add(CodeReworkEvidence, field+".origin.evidence", "返工卡必须带证据引用")
		}
	}

	// 同修订内写盘重叠必须有序（G213）：重叠两卡之间至少有一条依赖可达。
	for i := 0; i < len(r.Cards); i++ {
		for j := i + 1; j < len(r.Cards); j++ {
			a, b := r.Cards[i], r.Cards[j]
			if len(a.WriteScope) == 0 || len(b.WriteScope) == 0 {
				continue
			}
			hits := scope.ScopesOverlap(a.WriteScope, b.WriteScope)
			if len(hits) == 0 {
				continue
			}
			if reach[a.ID][b.ID] || reach[b.ID][a.ID] {
				continue
			}
			add(CodeWriteScope, fmt.Sprintf("cards[%d].write_scope", i),
				fmt.Sprintf("与 cards[%d] 写盘重叠但无依赖序（%s）；重叠卡之间必须有先后", j, strings.Join(hits, ", ")))
		}
	}

	// 摘要：已填就必须与内容一致（G202），否则「批准的是另一份计划」。
	if strings.TrimSpace(r.PlanDigest) != "" {
		want, err := Digest(r)
		if err != nil {
			add(CodePlanDigest, "plan_digest", err.Error())
		} else if !strings.EqualFold(strings.TrimSpace(r.PlanDigest), want) {
			add(CodePlanDigest, "plan_digest", "plan_digest 与修订内容不一致（批准的不是这一份）")
		}
	}
	return out
}
