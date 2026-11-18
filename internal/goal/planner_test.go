package goal

import (
	"strings"
	"testing"
)

func planCardJSON(id string) string {
	return `{"card_id":"c-` + id + `","id":"` + id + `","title":"实现 ` + id + `",` +
		`"goal":"现状：缺。目标：齐。","priority":10,"merge_order":10,"deps":[],` +
		`"write_scope":["internal/goal/**"],"read_scope":["README.md"],` +
		`"acceptance":["测试通过"],"verify":["go test ./internal/goal -count=1"],"not_doing":["不改主干"]}`
}

func TestParsePlanCardsShapes(t *testing.T) {
	bare := `[` + planCardJSON("GL-001") + `]`
	cards, err := ParsePlanCards([]byte(bare))
	if err != nil || len(cards) != 1 || cards[0].ID != "GL-001" {
		t.Fatalf("裸数组解析失败: %v %+v", err, cards)
	}

	wrapped := `{"cards":[` + planCardJSON("GL-001") + `,` + planCardJSON("GL-002") + `]}`
	cards, err = ParsePlanCards([]byte(wrapped))
	if err != nil || len(cards) != 2 {
		t.Fatalf("外层包装解析失败: %v %+v", err, cards)
	}

	fenced := "```json\n" + wrapped + "\n```"
	cards, err = ParsePlanCards([]byte(fenced))
	if err != nil || len(cards) != 2 {
		t.Fatalf("代码围栏解析失败: %v %+v", err, cards)
	}

	if _, err := ParsePlanCards(nil); err == nil {
		t.Fatal("空输出必须报错")
	}
	if _, err := ParsePlanCards([]byte(`{"nope":1}`)); err == nil {
		t.Fatal("缺 cards 必须报错")
	}
	if _, err := ParsePlanCards([]byte(`[{"card_id":`)); err == nil {
		t.Fatal("坏 JSON 必须报错")
	}
}

func TestBuildPlanRevisionFillsOriginAndValidates(t *testing.T) {
	req := PlanRequest{
		ProjectID: "p1", GoalID: "gl_pi001ax9", GoalVersion: 1, PlanRev: 1,
		Kind: PlanKindInitial, Title: "目标", Statement: "现状：无。目标：有。",
		Evidence: []string{"docs/plan.md#1"}, MaxCards: 5,
	}
	cards, err := ParsePlanCards([]byte(`[` + planCardJSON("GL-001") + `]`))
	if err != nil {
		t.Fatal(err)
	}
	rev, err := BuildPlanRevision(req, cards)
	if err != nil {
		t.Fatalf("组装失败: %v", err)
	}
	if rev.PlanDigest == "" || rev.Status != PlanProposed {
		t.Fatalf("修订不完整: %+v", rev)
	}
	origin := rev.Cards[0].Origin
	if origin.GoalID != req.GoalID || origin.PlanRev != 1 || origin.CardID != "c-GL-001" {
		t.Fatalf("origin 未补齐: %+v", origin)
	}
	if len(origin.SourceRefs) != 1 || origin.SourceRefs[0] != "docs/plan.md#1" {
		t.Fatalf("证据未挂到卡上: %+v", origin.SourceRefs)
	}

	// 超过卡数上限。
	req.MaxCards = 0
	many := make([]PlanCard, 0, 3)
	for i := 0; i < 3; i++ {
		cards, _ := ParsePlanCards([]byte(`[` + planCardJSON("GL-10"+string(rune('0'+i))) + `]`))
		many = append(many, cards[0])
	}
	req.MaxCards = 2
	if _, err := BuildPlanRevision(req, many); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超上限应拒绝: %v", err)
	}

	// 重复 card_id：本修订内校验必须拦住。
	req.MaxCards = 5
	dup := []PlanCard{cards[0], cards[0]}
	if _, err := BuildPlanRevision(req, dup); err == nil {
		t.Fatal("重复 card_id 应被拒绝")
	}

	// 空计划。
	if _, err := BuildPlanRevision(req, nil); err == nil {
		t.Fatal("空计划应被拒绝")
	}
}

func TestParseAcceptanceSpec(t *testing.T) {
	got, err := ParseAcceptanceSpec([]string{
		"command:go test ./internal/goal -count=1",
		"manual:人工核对计划卡|计划卡字段齐全",
		"evidence:delivery.merged=true|交付已合入",
	})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != 3 || got[0].ID != "AC-1" || got[1].Kind != AcceptanceManual {
		t.Fatalf("解析结果不对: %+v", got)
	}
	if !got[0].EvidenceRequired || got[1].EvidenceRequired {
		t.Fatalf("证据要求不对: %+v", got)
	}
	if got[1].Check != "人工核对计划卡" || got[1].Statement != "计划卡字段齐全" {
		t.Fatalf("check/statement 拆分不对: %+v", got[1])
	}

	if _, err := ParseAcceptanceSpec(nil); err == nil {
		t.Fatal("空验收必须拒绝")
	}
	if _, err := ParseAcceptanceSpec([]string{"go test ./..."}); err == nil {
		t.Fatal("缺 kind 前缀必须拒绝")
	}
	if _, err := ParseAcceptanceSpec([]string{"guess:whatever"}); err == nil {
		t.Fatal("未知 kind 必须拒绝")
	}
	if _, err := ParseAcceptanceSpec([]string{"command:"}); err == nil {
		t.Fatal("空 check 必须拒绝")
	}
}

func TestSummarizePlan(t *testing.T) {
	req := PlanRequest{ProjectID: "p1", GoalID: "gl_pi001ax9", GoalVersion: 1, PlanRev: 2, Title: "t", Statement: "s"}
	cards, _ := ParsePlanCards([]byte(`[` + planCardJSON("GL-001") + `,` + planCardJSON("GL-002") + `]`))
	// 同范围的两张卡必须有依赖序，否则就是计划缺陷（G213）。
	cards[1].Deps = []string{"GL-001"}
	rev, err := BuildPlanRevision(req, cards)
	if err != nil {
		t.Fatal(err)
	}
	sum := SummarizePlan(rev)
	if sum.PlanRev != 2 || sum.CardCount != 2 || len(sum.CardIDs) != 2 || !sum.NeedsApproval {
		t.Fatalf("汇总不对: %+v", sum)
	}
	if len(sum.WriteScope) != 1 || sum.WriteScope[0] != "internal/goal/**" {
		t.Fatalf("写盘范围应去重: %+v", sum.WriteScope)
	}
	if strings.TrimSpace(CompactJSON(sum)) == "" || !strings.HasPrefix(CompactJSON(sum), "{") {
		t.Fatal("CompactJSON 应输出对象")
	}
}
