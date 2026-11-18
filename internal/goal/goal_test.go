package goal

import (
	"errors"
	"strings"
	"testing"
)

func validGoal(id string) *Goal {
	return &Goal{
		ProjectID: "p1", GoalID: id, IterationID: "IT-PI-001",
		Status: StatusDraft, ActiveVersion: 1,
	}
}

func validVersion() *Version {
	return &Version{
		ProjectID: "p1", GoalID: "gl_pi001ax9", GoalVersion: 1,
		Title: "打通目标级 Lead", Statement: "现状：Lead 只有一次性拆卡。目标：目标可被批准与验收。",
		Acceptance: []Acceptance{{
			ID: "AC-1", Kind: AcceptanceCommand, Check: "go test ./internal/goal -count=1",
			Statement: "领域逻辑通过", EvidenceRequired: true,
		}},
	}
}

func validCard(id, cardID string, deps []string, scopes []string) PlanCard {
	return PlanCard{
		CardID: cardID, ID: id, Title: "实现 " + id,
		Goal: "现状：缺失。目标：补齐。",
		Deps: deps, WriteScope: scopes, ReadScope: []string{"README.md"},
		Acceptance: []string{"可核对"}, Verify: []string{"go test ./internal/goal -count=1"},
		NotDoing: []string{"不改主干"}, Origin: Origin{GoalID: "gl_pi001ax9", PlanRev: 1, CardID: cardID},
	}
}

func TestGoalIDAndVersionValidation(t *testing.T) {
	for _, id := range []string{"gl_pi001ax9", "gl_abcdefgh", "gl_a1b2c3d4e5"} {
		if err := ValidateGoalID(id); err != nil {
			t.Fatalf("%s 应合法：%v", id, err)
		}
	}
	for _, id := range []string{"", "gl_short", "pi001ax9", "gl_ABCDEFGH", "gl_has-dash", "VN-001"} {
		if err := ValidateGoalID(id); err == nil {
			t.Fatalf("%s 应非法", id)
		} else if !strings.Contains(err.Error(), CodeGoalIDInvalid) {
			t.Fatalf("%s 的错误应带 %s：%v", id, CodeGoalIDInvalid, err)
		}
	}

	g := validGoal("gl_pi001ax9")
	if err := g.Validate(); err != nil {
		t.Fatalf("合法目标被拒：%v", err)
	}

	v := validVersion()
	if err := v.Validate(); err != nil {
		t.Fatalf("合法版本被拒：%v", err)
	}
	if v.Budget.MaxPlanRevisions != DefaultBudget().MaxPlanRevisions {
		t.Fatalf("预算缺省未补齐：%+v", v.Budget)
	}

	bad := validVersion()
	bad.Acceptance = nil
	if err := bad.Validate(); err == nil {
		t.Fatal("空验收条件必须拒绝")
	}

	dup := validVersion()
	dup.Acceptance = append(dup.Acceptance, dup.Acceptance[0])
	if err := dup.Validate(); err == nil {
		t.Fatal("验收条件 id 重复必须拒绝")
	}

	badKind := validVersion()
	badKind.Acceptance[0].Kind = "guess"
	if err := badKind.Validate(); err == nil {
		t.Fatal("非法验收形态必须拒绝")
	}
}

func TestGoalStateMachine(t *testing.T) {
	legal := [][2]string{
		{StatusDraft, StatusActive},
		{StatusActive, StatusVerifying},
		{StatusActive, StatusPaused},
		{StatusActive, StatusEscalated},
		{StatusActive, StatusAbandoned},
		{StatusVerifying, StatusActive},
		{StatusVerifying, StatusDone},
		{StatusVerifying, StatusPaused},
		{StatusPaused, StatusActive},
		{StatusPaused, StatusAbandoned},
		{StatusEscalated, StatusPaused},
		{StatusEscalated, StatusAbandoned},
	}
	for _, tc := range legal {
		if !CanTransition(tc[0], tc[1]) {
			t.Fatalf("%s → %s 应合法", tc[0], tc[1])
		}
	}
	illegal := [][2]string{
		{StatusDraft, StatusDone},
		{StatusDraft, StatusVerifying},
		{StatusActive, StatusDraft},
		{StatusActive, StatusDone},
		{StatusDone, StatusActive},
		{StatusAbandoned, StatusActive},
		{StatusEscalated, StatusActive},
		{StatusPaused, StatusDone},
	}
	for _, tc := range illegal {
		if CanTransition(tc[0], tc[1]) {
			t.Fatalf("%s → %s 应非法", tc[0], tc[1])
		}
	}
	if CanTransition(StatusActive, StatusActive) {
		t.Fatal("同状态不算迁移")
	}
	if !IsTerminal(StatusDone) || !IsTerminal(StatusAbandoned) || IsTerminal(StatusActive) {
		t.Fatal("终态集合不对")
	}
	for _, s := range AllStatuses() {
		if !ValidStatus(s) {
			t.Fatalf("%s 应在封闭集合内", s)
		}
	}
	if ValidStatus("merged") {
		t.Fatal("迭代状态不得混进目标状态")
	}
	if !ValidReason(ReasonBudgetExhaust) || ValidReason("") || ValidReason("whatever") {
		t.Fatal("原因集合判定不对")
	}
}

func TestBudgetDefaultsAndExhaustion(t *testing.T) {
	zero := Budget{}
	got := zero.WithDefaults()
	if got != DefaultBudget() {
		t.Fatalf("零值预算应取缺省：%+v", got)
	}
	custom := Budget{ModelCalls: 3}
	if custom.WithDefaults().ModelCalls != 3 {
		t.Fatal("显式预算不得被缺省覆盖")
	}
	if !DefaultBudget().Exhausted(Budget{ModelCalls: 200}) {
		t.Fatal("达到上限应判耗尽")
	}
	if DefaultBudget().Exhausted(Budget{ModelCalls: 1}) {
		t.Fatal("未达上限不应判耗尽")
	}
}

func TestPlanDigestStable(t *testing.T) {
	rev := &PlanRevision{
		ProjectID: "p1", GoalID: "gl_pi001ax9", GoalVersion: 1, PlanRev: 1,
		Kind: PlanKindInitial, Status: PlanProposed,
		Cards: []PlanCard{
			validCard("GL-001", "c01", nil, []string{"docs/a.md"}),
			validCard("GL-002", "c02", []string{"GL-001"}, []string{"internal/goal/**"}),
		},
	}
	d1, err := Digest(rev)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := Digest(rev)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatal("同一内容两次摘要必须相同")
	}
	if len(d1) != 64 {
		t.Fatalf("摘要应是 64 位 hex，得到 %q", d1)
	}

	// nil 切片与空切片必须同摘要：JSON 往返会在这两者间来回变。
	mk := func(deps []string) *PlanRevision {
		return &PlanRevision{
			ProjectID: "p1", GoalID: "gl_pi001ax9", GoalVersion: 1, PlanRev: 1,
			Kind:  PlanKindInitial,
			Cards: []PlanCard{validCard("GL-001", "c01", deps, []string{"docs/a.md"})},
		}
	}
	dNil, err := Digest(mk(nil))
	if err != nil {
		t.Fatal(err)
	}
	dEmpty, err := Digest(mk([]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if dNil != dEmpty {
		t.Fatal("nil 与空切片不得改变摘要")
	}

	// 内容变一个字符，摘要必须变。
	rev.Cards[0].Title = "实现 GL-001（改）"
	d3, err := Digest(rev)
	if err != nil {
		t.Fatal(err)
	}
	if d3 == d1 {
		t.Fatal("内容变化后摘要必须变化")
	}
}

func TestEnqueueAndReworkKeys(t *testing.T) {
	k1 := EnqueueKey("p1", "gl_pi001ax9", 1, "digest", "c01")
	k2 := EnqueueKey("p1", "gl_pi001ax9", 1, "digest", "c01")
	if k1 != k2 {
		t.Fatal("同输入必须同键")
	}
	if !strings.HasPrefix(k1, "gk_") || len(k1) != 3+64 {
		t.Fatalf("键形态不对：%q", k1)
	}
	if EnqueueKey("p1", "gl_pi001ax9", 1, "digest", "c02") == k1 {
		t.Fatal("不同卡必须不同键")
	}
	if EnqueueKey("p1", "gl_pi001ax9", 2, "digest", "c01") == k1 {
		t.Fatal("不同修订必须不同键")
	}

	r1 := ReworkKey("gl_pi001ax9", "AC-2", []string{"vfy_1", "dlv_2"})
	r2 := ReworkKey("gl_pi001ax9", "AC-2", []string{"dlv_2", "vfy_1", "vfy_1"})
	if r1 != r2 {
		t.Fatal("证据顺序与重复不得改变返工键")
	}
	if !strings.HasPrefix(r1, "rw_") {
		t.Fatalf("返工键形态不对：%q", r1)
	}
	if ReworkKey("gl_pi001ax9", "AC-1", []string{"vfy_1"}) == r1 {
		t.Fatal("不同失败断言必须不同键")
	}
}

func TestPlanValidate(t *testing.T) {
	base := func() *PlanRevision {
		r := &PlanRevision{
			ProjectID: "p1", GoalID: "gl_pi001ax9", GoalVersion: 1, PlanRev: 1,
			Kind: PlanKindInitial, Status: PlanProposed,
			Cards: []PlanCard{
				validCard("GL-001", "c01", nil, []string{"internal/goal/**"}),
				validCard("GL-002", "c02", []string{"GL-001"}, []string{"internal/goal/lifecycle.go"}),
			},
		}
		_ = r.EnsureDigest()
		return r
	}

	if vs := base().Validate(); len(vs) > 0 {
		t.Fatalf("有序重叠应合法：%v", vs.Err())
	}

	// 去掉依赖序：范围重叠但无先后，必须拒绝。
	unordered := base()
	unordered.Cards[1].Deps = nil
	_ = unordered.EnsureDigest()
	vs := unordered.Validate()
	if !vs.HasCode(CodeWriteScope) {
		t.Fatalf("无序重叠应报 %s：%v", CodeWriteScope, vs.Err())
	}

	dup := base()
	dup.Cards[1].CardID = "c01"
	_ = dup.EnsureDigest()
	if vs := dup.Validate(); !vs.HasCode(CodeCardID) {
		t.Fatalf("card_id 重复应报 %s：%v", CodeCardID, vs.Err())
	}

	dupID := base()
	dupID.Cards[1].ID = "GL-001"
	_ = dupID.EnsureDigest()
	if vs := dupID.Validate(); !vs.HasCode(CodeCardID) {
		t.Fatalf("任务编号重复应报 %s：%v", CodeCardID, vs.Err())
	}

	reserved := base()
	reserved.Cards[0].ID = "ti_abc123456"
	_ = reserved.EnsureDigest()
	if vs := reserved.Validate(); !vs.HasCode(CodeCardID) {
		t.Fatalf("保留前缀应报 %s：%v", CodeCardID, vs.Err())
	}

	placeholder := base()
	placeholder.Cards[0].Acceptance = []string{"TODO: 填写验收条件"}
	_ = placeholder.EnsureDigest()
	if vs := placeholder.Validate(); !vs.HasCode(CodeCardPlaceholder) {
		t.Fatalf("占位验收应报 %s：%v", CodeCardPlaceholder, vs.Err())
	}

	absScope := base()
	absScope.Cards[0].WriteScope = []string{"/etc/passwd"}
	_ = absScope.EnsureDigest()
	if vs := absScope.Validate(); !vs.HasCode(CodeWriteScope) {
		t.Fatalf("绝对路径应报 %s：%v", CodeWriteScope, vs.Err())
	}

	whole := base()
	whole.Cards[0].WriteScope = []string{"**"}
	_ = whole.EnsureDigest()
	if vs := whole.Validate(); !vs.HasCode(CodeWriteScope) {
		t.Fatalf("整仓通配应报 %s：%v", CodeWriteScope, vs.Err())
	}

	rework := base()
	rework.Kind = PlanKindRework
	for i := range rework.Cards {
		rework.Cards[i].Origin.Evidence = nil
	}
	_ = rework.EnsureDigest()
	if vs := rework.Validate(); !vs.HasCode(CodeReworkEvidence) {
		t.Fatalf("返工缺证据应报 %s：%v", CodeReworkEvidence, vs.Err())
	}

	self := base()
	self.Cards[0].Deps = []string{"GL-001"}
	_ = self.EnsureDigest()
	if vs := self.Validate(); !vs.HasCode(CodeCardDep) {
		t.Fatalf("自依赖应报 %s：%v", CodeCardDep, vs.Err())
	}

	cycle := base()
	cycle.Cards[0].Deps = []string{"GL-002"}
	_ = cycle.EnsureDigest()
	if vs := cycle.Validate(); !vs.HasCode(CodeCardDep) {
		t.Fatalf("依赖成环应报 %s：%v", CodeCardDep, vs.Err())
	}

	tampered := base()
	tampered.PlanDigest = strings.Repeat("0", 64)
	if vs := tampered.Validate(); !vs.HasCode(CodePlanDigest) {
		t.Fatalf("摘要不符应报 %s：%v", CodePlanDigest, vs.Err())
	}
}

func TestPlanEnsureDigestFillsOnce(t *testing.T) {
	r := &PlanRevision{
		ProjectID: "p1", GoalID: "gl_pi001ax9", GoalVersion: 1, PlanRev: 1,
		Kind: PlanKindInitial, Status: PlanProposed,
		Cards: []PlanCard{validCard("GL-001", "c01", nil, []string{"docs/a.md"})},
	}
	if err := r.EnsureDigest(); err != nil {
		t.Fatal(err)
	}
	if r.PlanDigest == "" {
		t.Fatal("摘要应被补齐")
	}
	first := r.PlanDigest
	r.PlanDigest = ""
	if err := r.EnsureDigest(); err != nil {
		t.Fatal(err)
	}
	if r.PlanDigest != first {
		t.Fatal("补齐的摘要必须可重算一致")
	}
}

func TestProgressThreeColumn(t *testing.T) {
	links := []*TaskLink{
		{InstanceID: "ti_a", State: LinkApplied},
		{InstanceID: "ti_b", State: LinkApplied},
		{InstanceID: "ti_c", State: LinkDrifted},
	}
	p := ProgressOf(links, func(l *TaskLink) bool { return l.InstanceID == "ti_a" }, 2)
	if p.MappedTotal != 2 || p.MappedDone != 1 || p.Drifted != 1 || p.PendingEnqueue != 2 {
		t.Fatalf("进度三列口径不对：%+v", p)
	}
}

func TestGoalStateErrorsStayTyped(t *testing.T) {
	if !errors.Is(ErrGoalTransition, ErrGoalTransition) {
		t.Fatal("哨兵错误应可 errors.Is")
	}
	if err := (&Goal{}).Validate(); err == nil {
		t.Fatal("空工程的 goal 应校验失败")
	}
}
