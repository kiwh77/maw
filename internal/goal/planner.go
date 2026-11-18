package goal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PlanRequest 是一次规划的冻结输入（契约 §5）：Lead 只能看到这些字段。
type PlanRequest struct {
	ProjectID  string
	GoalID     string
	GoalVersion int
	PlanRev    int
	Kind       string
	Title      string
	Statement  string
	Acceptance []Acceptance
	Evidence   []string
	// MaxCards 是本次修订允许的最大卡数；<=0 时用预算缺省。
	MaxCards int
}

// AllowedCards 返回本次修订的卡数上限。
func (r PlanRequest) AllowedCards() int {
	if r.MaxCards > 0 {
		return r.MaxCards
	}
	return DefaultBudget().MaxCardsPerRevision
}

// ErrPlanOutput 表示 Lead 的输出不是可判定的计划。
var ErrPlanOutput = errors.New("goal: 计划输出不可解析")

// stripCodeFence 去掉模型常见的 ```json 包裹。
//
// 这不是纵容：输出协议要求 JSON，但把「多了一层围栏」判成失败只会让评审
// 反复重跑；去掉围栏后仍按严格结构校验。
func stripCodeFence(raw string) string {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// planEnvelope 是允许的外层包装：{"cards":[...]} 或裸数组。
type planEnvelope struct {
	Cards []PlanCard `json:"cards"`
}

// ParsePlanCards 把 Lead 输出解析成计划卡。
//
// 只接受两种形状：`{"cards":[...]}` 与裸数组。解析失败不猜、不修补。
func ParsePlanCards(raw []byte) ([]PlanCard, error) {
	body := stripCodeFence(string(raw))
	if body == "" {
		return nil, fmt.Errorf("%w: 输出为空", ErrPlanOutput)
	}
	if strings.HasPrefix(body, "[") {
		var cards []PlanCard
		if err := json.Unmarshal([]byte(body), &cards); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPlanOutput, err)
		}
		return cards, nil
	}
	var env planEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPlanOutput, err)
	}
	if env.Cards == nil {
		return nil, fmt.Errorf("%w: 既不是数组也没有 cards 字段", ErrPlanOutput)
	}
	return env.Cards, nil
}

// BuildPlanRevision 把请求与卡片组装成一份可落库的修订。
//
// 这里只做「组装 + 本修订内校验」：跨蓝图检查属于入队阶段（PI-302）。
func BuildPlanRevision(req PlanRequest, cards []PlanCard) (*PlanRevision, error) {
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.GoalID) == "" {
		return nil, fmt.Errorf("%w: 缺少工程或目标身份", ErrPlanOutput)
	}
	if req.PlanRev < 1 || req.GoalVersion < 1 {
		return nil, fmt.Errorf("%w: 修订号与目标版本必须 >= 1", ErrPlanOutput)
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = PlanKindInitial
	}
	if !ValidPlanKind(kind) {
		return nil, fmt.Errorf("%w: 非法计划类型 %q", ErrPlanOutput, kind)
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("%w: 计划里没有任何卡", ErrPlanOutput)
	}
	if len(cards) > req.AllowedCards() {
		return nil, fmt.Errorf("%w: 卡数 %d 超过上限 %d", ErrPlanOutput, len(cards), req.AllowedCards())
	}
	for i := range cards {
		card := &cards[i]
		if strings.TrimSpace(card.Origin.GoalID) == "" {
			card.Origin.GoalID = req.GoalID
		}
		if card.Origin.PlanRev == 0 {
			card.Origin.PlanRev = req.PlanRev
		}
		if strings.TrimSpace(card.Origin.CardID) == "" {
			card.Origin.CardID = card.CardID
		}
		if len(card.Origin.SourceRefs) == 0 && len(req.Evidence) > 0 {
			card.Origin.SourceRefs = append([]string{}, req.Evidence...)
		}
	}
	rev := &PlanRevision{
		ProjectID:   req.ProjectID,
		GoalID:      req.GoalID,
		GoalVersion: req.GoalVersion,
		PlanRev:     req.PlanRev,
		Kind:        kind,
		Status:      PlanProposed,
		Cards:       cards,
		Approval:    Approval{State: ApprovalPending},
	}
	if err := rev.EnsureDigest(); err != nil {
		return nil, err
	}
	if vs := rev.Validate(); len(vs) > 0 {
		return nil, vs.Err()
	}
	return rev, nil
}

// PlanSummary 是人读的计划预览（不落库，不改变任何状态）。
type PlanSummary struct {
	PlanRev    int      `json:"plan_rev"`
	GoalID     string   `json:"goal_id"`
	GoalVersion int     `json:"goal_version"`
	Kind       string   `json:"kind"`
	Digest     string   `json:"plan_digest"`
	CardCount  int      `json:"cards"`
	CardIDs    []string `json:"card_ids"`
	WriteScope []string `json:"write_scope"`
	NeedsApproval bool  `json:"needs_approval"`
}

// SummarizePlan 汇总计划的关键事实，供 CLI 预览。
func SummarizePlan(rev *PlanRevision) PlanSummary {
	sum := PlanSummary{NeedsApproval: true}
	if rev == nil {
		return sum
	}
	sum.PlanRev = rev.PlanRev
	sum.GoalID = rev.GoalID
	sum.GoalVersion = rev.GoalVersion
	sum.Kind = rev.Kind
	sum.Digest = rev.PlanDigest
	sum.CardCount = len(rev.Cards)
	seen := map[string]bool{}
	for _, card := range rev.Cards {
		sum.CardIDs = append(sum.CardIDs, card.ID)
		for _, scope := range card.WriteScope {
			if !seen[scope] {
				seen[scope] = true
				sum.WriteScope = append(sum.WriteScope, scope)
			}
		}
	}
	return sum
}

// ParseAcceptanceSpec 解析 CLI 的验收条件写法：`kind:check` 或 `kind:check|statement`。
//
// 例：`command:go test ./internal/goal -count=1`、
//     `manual:人工核对计划卡|计划卡字段齐全`。
func ParseAcceptanceSpec(specs []string) ([]Acceptance, error) {
	out := make([]Acceptance, 0, len(specs))
	for i, raw := range specs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		kind, rest, ok := strings.Cut(raw, ":")
		if !ok {
			return nil, fmt.Errorf("验收条件 %q 缺少 kind: 前缀（command/manual/evidence）", raw)
		}
		kind = strings.ToLower(strings.TrimSpace(kind))
		switch kind {
		case AcceptanceCommand, AcceptanceManual, AcceptanceEvidence:
		default:
			return nil, fmt.Errorf("验收条件 %q 的 kind %q 不合法", raw, kind)
		}
		check := strings.TrimSpace(rest)
		statement := check
		if c, st, ok := strings.Cut(check, "|"); ok {
			check, statement = strings.TrimSpace(c), strings.TrimSpace(st)
		}
		if check == "" || statement == "" {
			return nil, fmt.Errorf("验收条件 %q 缺 check 或 statement", raw)
		}
		out = append(out, Acceptance{
			ID: fmt.Sprintf("AC-%d", i+1), Kind: kind,
			Check: check, Statement: statement, EvidenceRequired: kind != AcceptanceManual,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("至少需要 1 条目标验收条件")
	}
	for i, a := range out {
		if err := a.Validate(); err != nil {
			return nil, err
		}
		out[i] = a
	}
	return out, nil
}

// CompactJSON 去掉多余空白，便于把计划写进审计事件。
func CompactJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	return strings.TrimSpace(buf.String())
}
