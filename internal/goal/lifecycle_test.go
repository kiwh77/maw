package goal

import "testing"

func cr(id, verdict string) CriterionResult {
	return CriterionResult{ID: id, Kind: AcceptanceCommand, Verdict: verdict, Evidence: []string{"cmd:" + id}}
}

func TestOverallVerdictAggregation(t *testing.T) {
	cases := []struct {
		name    string
		results []CriterionResult
		want    string
	}{
		{"全通过", []CriterionResult{cr("AC-1", VerdictPass), cr("AC-2", VerdictPass)}, VerdictPass},
		{"有一条失败", []CriterionResult{cr("AC-1", VerdictPass), cr("AC-2", VerdictFail)}, VerdictFail},
		{"失败优先于未知?", []CriterionResult{cr("AC-1", VerdictFail), cr("AC-2", VerdictUnknown)}, VerdictUnknown},
		{"只有未知", []CriterionResult{cr("AC-1", VerdictUnknown)}, VerdictUnknown},
		{"空集合不给通过", nil, VerdictUnknown},
		{"非法值按未知", []CriterionResult{cr("AC-1", "maybe")}, VerdictUnknown},
	}
	for _, tc := range cases {
		if got := OverallVerdict(tc.results); got != tc.want {
			t.Fatalf("%s: overall=%s want %s", tc.name, got, tc.want)
		}
	}
}

func TestDecideCompletion(t *testing.T) {
	room := ReworkBudget{MaxReworkRounds: 2, UsedReworkRounds: 0, MaxPlanRevisions: 5, UsedPlanRevisions: 1}
	exhausted := ReworkBudget{MaxReworkRounds: 2, UsedReworkRounds: 2, MaxPlanRevisions: 5, UsedPlanRevisions: 3}

	if d := DecideCompletion(StatusVerifying, VerdictPass, room); d.Status != StatusDone || d.ExitCode != ExitGoalDone {
		t.Fatalf("全通过应 done: %+v", d)
	}
	d := DecideCompletion(StatusVerifying, VerdictFail, room)
	if !d.NeedsRework || d.Status != StatusVerifying || d.ExitCode != ExitGoalDone {
		t.Fatalf("还能返工时应继续（不改终态）: %+v", d)
	}
	d = DecideCompletion(StatusVerifying, VerdictFail, exhausted)
	if d.Status != StatusEscalated || d.ExitCode != ExitGoalReworkExhausted || d.NeedsRework {
		t.Fatalf("返工用尽应升级: %+v", d)
	}
	d = DecideCompletion(StatusActive, VerdictUnknown, room)
	if d.Status != StatusEscalated || d.ExitCode != ExitGoalEscalated {
		t.Fatalf("未知应升级: %+v", d)
	}
	if d := BudgetExhaustedDecision(); d.Status != StatusPaused || d.ExitCode != ExitGoalBudgetExhausted {
		t.Fatalf("预算耗尽应暂停: %+v", d)
	}
}

func TestReworkBudget(t *testing.T) {
	if (ReworkBudget{MaxReworkRounds: 0, MaxPlanRevisions: 0}).ReworkAllowed() != true {
		t.Fatal("上限为 0 表示不限制")
	}
	if (ReworkBudget{MaxReworkRounds: 1, UsedReworkRounds: 1}).ReworkAllowed() {
		t.Fatal("轮次用尽应拒绝")
	}
	if (ReworkBudget{MaxPlanRevisions: 2, UsedPlanRevisions: 2}).ReworkAllowed() {
		t.Fatal("修订用尽应拒绝")
	}
}

func TestReworkProposalStableKey(t *testing.T) {
	failing := []CriterionResult{
		{ID: "AC-2", Kind: AcceptanceCommand, Verdict: VerdictFail, Evidence: []string{"vfy_b", "vfy_a"}},
		{ID: "AC-1", Kind: AcceptanceCommand, Verdict: VerdictFail, Evidence: []string{"vfy_a"}},
	}
	p1 := NewReworkProposal("gl_pi001ax9", failing)
	p2 := NewReworkProposal("gl_pi001ax9", []CriterionResult{
		{ID: "AC-1", Verdict: VerdictFail, Evidence: []string{"vfy_a"}},
		{ID: "AC-2", Verdict: VerdictFail, Evidence: []string{"vfy_a", "vfy_b"}},
	})
	if p1.Key != p2.Key || !p1.SameProposal(p2) {
		t.Fatalf("同一失败证据集应得同一提案: %s vs %s", p1.Key, p2.Key)
	}
	if len(p1.Targets) != 2 || p1.Targets[0] != "AC-1" || len(p1.Evidence) != 2 {
		t.Fatalf("提案内容不对: %+v", p1)
	}
	other := NewReworkProposal("gl_pi001ax9", []CriterionResult{
		{ID: "AC-2", Verdict: VerdictFail, Evidence: []string{"vfy_c"}},
	})
	if other.Key == p1.Key {
		t.Fatal("不同证据必须不同键")
	}
	if got := FailingResults(append(failing, cr("AC-3", VerdictPass))); len(got) != 2 {
		t.Fatalf("FailingResults 只应含未通过项: %+v", got)
	}
}
