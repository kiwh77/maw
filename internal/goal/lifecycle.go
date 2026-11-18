package goal

import (
	"fmt"
	"sort"
	"strings"
)

// 验收判定三值（契约 §4）：unknown 不折算成 pass/fail。
const (
	VerdictPass    = "pass"
	VerdictFail    = "fail"
	VerdictUnknown = "unknown"
)

// ValidVerdict 报告判定值是否合法。
func ValidVerdict(v string) bool {
	return v == VerdictPass || v == VerdictFail || v == VerdictUnknown
}

// 整体验收的退出码（契约 §13，封闭集合）。
const (
	ExitGoalDone             = 0
	ExitGoalPaused           = 2
	ExitGoalEscalated        = 3
	ExitGoalBudgetExhausted  = 4
	ExitGoalReworkExhausted  = 5
	ExitGoalAbandoned        = 6
)

// CriterionResult 是一条目标验收条件的判定（契约 §4）。
type CriterionResult struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Verdict  string   `json:"verdict"`
	Detail   string   `json:"detail,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
}

// Validate 拒绝缺 id 或非法判定。
func (r CriterionResult) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("验收结果缺 id")
	}
	if !ValidVerdict(r.Verdict) {
		return fmt.Errorf("验收结果 %s 的判定 %q 非法", r.ID, r.Verdict)
	}
	return nil
}

// OverallVerdict 聚合逐条判定（契约 §13）。
//
// 规则只有三条：任一 unknown → unknown；否则任一 fail → fail；全部 pass → pass。
// 空集合不给 pass：没有证据就不是通过。
func OverallVerdict(results []CriterionResult) string {
	if len(results) == 0 {
		return VerdictUnknown
	}
	sawFail := false
	for _, r := range results {
		switch r.Verdict {
		case VerdictUnknown:
			return VerdictUnknown
		case VerdictFail:
			sawFail = true
		case VerdictPass:
		default:
			return VerdictUnknown
		}
	}
	if sawFail {
		return VerdictFail
	}
	return VerdictPass
}

// FailingResults 返回未通过（fail）与判不了的（unknown）条件，供返工提案引用。
func FailingResults(results []CriterionResult) []CriterionResult {
	out := make([]CriterionResult, 0, len(results))
	for _, r := range results {
		if r.Verdict != VerdictPass {
			out = append(out, r)
		}
	}
	return out
}

// ReworkBudget 是返工与修订上限的判据（契约 §10）。
type ReworkBudget struct {
	MaxReworkRounds    int
	UsedReworkRounds   int
	MaxPlanRevisions   int
	UsedPlanRevisions  int
}

// ReworkAllowed 报告还能不能再来一轮返工。
func (b ReworkBudget) ReworkAllowed() bool {
	if b.MaxReworkRounds > 0 && b.UsedReworkRounds >= b.MaxReworkRounds {
		return false
	}
	if b.MaxPlanRevisions > 0 && b.UsedPlanRevisions >= b.MaxPlanRevisions {
		return false
	}
	return true
}

// Decision 是一次整体收尾的结论（契约 §13）。
type Decision struct {
	Status      string `json:"status"`
	ExitCode    int    `json:"exit_code"`
	Reason      string `json:"reason"`
	NeedsRework bool   `json:"needs_rework"`
}

// DecideCompletion 由当前状态、整体判定与预算推出收尾结论。
//
// 只做判定，不写库：状态迁移由 app 层带 revision 落库（G120/G123）。
func DecideCompletion(current, overall string, budget ReworkBudget) Decision {
	switch overall {
	case VerdictPass:
		return Decision{Status: StatusDone, ExitCode: ExitGoalDone, Reason: "all_criteria_pass"}
	case VerdictFail:
		if budget.ReworkAllowed() {
			// 还能返工：目标继续跑，不是终局退出码。
			status := StatusActive
			if current == StatusVerifying {
				status = StatusVerifying
			}
			return Decision{Status: status, ExitCode: ExitGoalDone, Reason: "rework_proposed", NeedsRework: true}
		}
		return Decision{Status: StatusEscalated, ExitCode: ExitGoalReworkExhausted, Reason: "rework_exhausted"}
	case VerdictUnknown:
		return Decision{Status: StatusEscalated, ExitCode: ExitGoalEscalated, Reason: "evidence_unknown"}
	default:
		return Decision{Status: StatusEscalated, ExitCode: ExitGoalEscalated, Reason: "verdict_invalid"}
	}
}

// BudgetExhaustedDecision 是预算维度耗尽时的固定结论：暂停，不取消在飞（契约 §10 第 4 条）。
func BudgetExhaustedDecision() Decision {
	return Decision{Status: StatusPaused, ExitCode: ExitGoalBudgetExhausted, Reason: ReasonBudgetExhaust}
}

// ReworkProposal 是一次返工提案：把失败断言与证据钉在一起，作为计划修订的来源。
type ReworkProposal struct {
	GoalID    string   `json:"goal_id"`
	Targets   []string `json:"targets"`
	Evidence  []string `json:"evidence"`
	Key       string   `json:"rework_key"`
	NeedsPlan bool     `json:"needs_plan"`
}

// NewReworkProposal 由未通过的验收结果生成返工提案。
//
// Key 只由「目标 + 失败断言 + 证据」决定：同一失败重复事件、重启重放都会得到
// 同一个 Key，用来避免重复提案（契约 §12 / G603）。
func NewReworkProposal(goalID string, failing []CriterionResult) ReworkProposal {
	targets := make([]string, 0, len(failing))
	evidence := []string{}
	seen := map[string]bool{}
	for _, r := range failing {
		targets = append(targets, r.ID)
		for _, e := range r.Evidence {
			e = strings.TrimSpace(e)
			if e == "" || seen[e] {
				continue
			}
			seen[e] = true
			evidence = append(evidence, e)
		}
	}
	sort.Strings(targets)
	sort.Strings(evidence)
	return ReworkProposal{
		GoalID:    goalID,
		Targets:   targets,
		Evidence:  evidence,
		Key:       ReworkKey(goalID, strings.Join(targets, "+"), evidence),
		NeedsPlan: len(targets) > 0,
	}
}

// SameProposal 报告两份提案是否是同一次返工（用于重放去重）。
func (p ReworkProposal) SameProposal(o ReworkProposal) bool {
	return p.GoalID == o.GoalID && p.Key == o.Key
}
