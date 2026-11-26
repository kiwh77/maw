package state_test

import (
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/state"
)

const testProject = model.ProjectID("716c30878ba1b95f")

var testSHA = model.SHA256("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad fixture time %q: %v", s, err)
	}
	return v
}

func codeDeliveryFixture(t *testing.T) *model.Delivery {
	t.Helper()
	commit := model.Commit("abc1234")
	return &model.Delivery{
		DeliveryID:      "dlv_001",
		TaskInstance:    "ti_vn202a",
		TaskVersion:     1,
		RunID:           "run_a001",
		Mode:            model.DeliveryCode,
		BaselineCommit:  "dedc919",
		CandidateCommit: &commit,
		DeliveredAt:     at(t, "2026-10-23T07:09:00Z"),
		Artifacts: []model.Artifact{{
			ArtifactID:   "art_001",
			Kind:         model.ArtifactCommit,
			Ref:          "abc1234",
			DeclaredBy:   model.DeclaredByCLI,
			Exists:       true,
			InWriteScope: true,
		}},
		Verifications: []model.Verification{{
			VerificationID:    "vfy_001",
			Command:           "CGO_ENABLED=0 go test ./internal/engine/state -count=1",
			TargetTaskVersion: 1,
			TargetCommit:      &commit,
			Result:            model.VerifyPass,
			Evidence:          []string{"exit_code:0", "state: 12 tests ok"},
			RanAt:             at(t, "2026-10-23T07:10:00Z"),
		}},
		Review: model.Review{
			Required: true, Verdict: model.VerdictApproved, Reviewer: "r1",
			ReviewedAt: ptrTime(at(t, "2026-10-23T07:12:00Z")),
		},
		Merge: model.Merge{
			Required: true, Merged: true,
			MergedCommit: ptrCommit("def5678"),
			MergedAt:     ptrTime(at(t, "2026-10-23T07:15:00Z")),
		},
		Completion: model.Completion{Decision: model.DecisionDone, DecidedAt: at(t, "2026-10-23T07:16:00Z")},
	}
}

func nonCodeDeliveryFixture(t *testing.T) *model.Delivery {
	t.Helper()
	return &model.Delivery{
		DeliveryID:     "dlv_002",
		TaskInstance:   "ti_vn202b",
		TaskVersion:    2,
		RunID:          "run_a002",
		Mode:           model.DeliveryNonCode,
		BaselineCommit: "dedc919",
		DeliveredAt:    at(t, "2026-10-23T07:20:00Z"),
		Artifacts: []model.Artifact{{
			ArtifactID:   "art_002",
			Kind:         model.ArtifactFile,
			Ref:          "docs/x.md",
			DeclaredBy:   model.DeclaredByManual,
			Exists:       true,
			InWriteScope: true,
			SHA256:       &testSHA,
		}},
		Verifications: []model.Verification{{
			VerificationID:    "vfy_002",
			Command:           "人工验收：口径复核",
			TargetTaskVersion: 2,
			Result:            model.VerifyManual,
			Evidence:          []string{"评审记录 #12"},
			RanAt:             at(t, "2026-10-23T07:21:00Z"),
		}},
		Review:     model.Review{Required: true, Verdict: model.VerdictApproved, Reviewer: "r2"},
		Completion: model.Completion{Decision: model.DecisionDone, DecidedAt: at(t, "2026-10-23T07:22:00Z")},
	}
}

func ptrTime(t time.Time) *time.Time         { return &t }
func ptrCommit(c model.Commit) *model.Commit { return &c }

func TestAcceptanceApplies(t *testing.T) {
	d := codeDeliveryFixture(t)
	if vs := state.AcceptanceApplies(d, &d.Verifications[0]); len(vs) != 0 {
		t.Fatalf("有效通过应被接受：%v", vs)
	}

	old := d.Verifications[0]
	old.TargetCommit = ptrCommit("9999999")
	if !hasCode(state.AcceptanceApplies(d, &old), model.CodeVerificationStaleCommit) {
		t.Fatal("旧提交上的验证不得给当前候选背书（E331）")
	}

	wrongVersion := d.Verifications[0]
	wrongVersion.TargetTaskVersion = 2
	if !hasCode(state.AcceptanceApplies(d, &wrongVersion), model.CodeVerificationVersion) {
		t.Fatal("跨版本的验证必须被拒（E330）")
	}

	exitOnly := d.Verifications[0]
	exitOnly.Evidence = []string{"exit_code:0"}
	if !hasCode(state.AcceptanceApplies(d, &exitOnly), model.CodeVerificationExitOnly) {
		t.Fatal("只有退出码不算通过（E333）")
	}

	nc := nonCodeDeliveryFixture(t)
	hashOld := model.SHA256("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	changed := nc.Verifications[0]
	changed.TargetArtifactID = ptrArtifact("art_002")
	changed.TargetArtifactSHA256 = &hashOld
	if !hasCode(state.AcceptanceApplies(nc, &changed), model.CodeVerificationArtifact) {
		t.Fatal("产物哈希变了，旧验证失效（E334）")
	}
}

func TestDeriveDecision(t *testing.T) {
	d := codeDeliveryFixture(t)
	if got, vs := state.DeriveDecision(d); got != model.DecisionDone || len(vs) != 0 {
		t.Fatalf("证据齐备应 done，得到 %s（%v）", got, vs)
	}

	noMerge := codeDeliveryFixture(t)
	noMerge.Merge = model.Merge{Required: true, Merged: false}
	if got, vs := state.DeriveDecision(noMerge); got != model.DecisionPending ||
		!hasCode(vs, model.CodeDoneWithoutMerge) {
		t.Fatalf("未合入只能 pending，得到 %s（%v）", got, vs)
	}

	failed := codeDeliveryFixture(t)
	failed.Verifications = append(failed.Verifications, model.Verification{
		VerificationID:    "vfy_003",
		Command:           "go vet ./...",
		TargetTaskVersion: 1,
		TargetCommit:      ptrCommit("abc1234"),
		Result:            model.VerifyFail,
		RanAt:             at(t, "2026-10-23T07:11:00Z"),
	})
	if got, vs := state.DeriveDecision(failed); got != model.DecisionPending ||
		!hasCode(vs, model.CodeDoneWithFailedCheck) {
		t.Fatalf("存在 fail 验证只能 pending，得到 %s（%v）", got, vs)
	}

	noArtifact := codeDeliveryFixture(t)
	noArtifact.Artifacts = nil
	if got, vs := state.DeriveDecision(noArtifact); got != model.DecisionPending ||
		!hasCode(vs, model.CodeDoneWithoutArtifact) {
		t.Fatalf("没有产物只能 pending，得到 %s（%v）", got, vs)
	}

	nc := nonCodeDeliveryFixture(t)
	if got, vs := state.DeriveDecision(nc); got != model.DecisionDone || len(vs) != 0 {
		t.Fatalf("非代码人工验收通过应 done，得到 %s（%v）", got, vs)
	}
	ncWithMerge := nonCodeDeliveryFixture(t)
	ncWithMerge.Merge = model.Merge{Required: true, Merged: true}
	if got, vs := state.DeriveDecision(ncWithMerge); got != model.DecisionPending ||
		!hasCode(vs, model.CodeCompletionModeMismatch) {
		t.Fatalf("非代码任务不得记合入，得到 %s（%v）", got, vs)
	}
}

// TestDeriveDecisionMatchesManifestValidation 是防漂移闸：state 的判定与 model 的
// 交付校验器必须对同一份交付给出同一个结论，否则两套规则会各说各话。
func TestDeriveDecisionMatchesManifestValidation(t *testing.T) {
	cases := map[string]func(t *testing.T) *model.Delivery{
		"code":     codeDeliveryFixture,
		"non-code": nonCodeDeliveryFixture,
	}
	for name, build := range cases {
		d := build(t)
		decision, _ := state.DeriveDecision(d)
		vs := model.Manifest{Contract: model.DeliveryContract, ProjectID: testProject,
			Deliveries: []model.Delivery{*d}}.Validate()
		if decision == model.DecisionDone && len(vs) != 0 {
			t.Errorf("%s：state 判 done，但交付校验器报 %v", name, vs)
		}
		if decision != model.DecisionDone && len(vs) == 0 {
			t.Errorf("%s：交付校验器零违规，但 state 判 %s", name, decision)
		}
	}
}

func TestSupersedeAllowed(t *testing.T) {
	old := codeDeliveryFixture(t)
	newer := codeDeliveryFixture(t)
	newer.DeliveryID = "dlv_003"
	if vs := state.SupersedeAllowed(newer, old); !hasCode(vs, model.CodeSupersededStillDone) {
		t.Fatalf("旧交付仍 done 时取代应先降级，得到 %v", vs)
	}
	old.Completion.Decision = model.DecisionPending
	old.SupersededBy = ptrDelivery("dlv_003")
	if vs := state.SupersedeAllowed(newer, old); len(vs) != 0 {
		t.Fatalf("正常取代应允许：%v", vs)
	}
	if vs := state.SupersedeAllowed(old, old); !hasCode(vs, model.CodeSupersedeInvalid) {
		t.Fatalf("自指取代必须被拒，得到 %v", vs)
	}
}

func hasCode(vs []*model.Error, code model.Code) bool { return model.HasCode(vs, code) }

func ptrArtifact(id model.ArtifactID) *model.ArtifactID { return &id }
func ptrDelivery(id model.DeliveryID) *model.DeliveryID { return &id }
