package model_test

import (
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
)

const (
	testSHA = model.SHA256("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
)

func codeDelivery(t *testing.T) model.Delivery {
	t.Helper()
	return model.Delivery{
		DeliveryID:      "dlv_001",
		TaskInstance:    "ti_vn003c",
		TaskVersion:     1,
		RunID:           "run_a001",
		Mode:            model.DeliveryCode,
		BaselineCommit:  "dedc919",
		CandidateCommit: ptr(model.Commit("abc1234")),
		DeliveredAt:     mustTime(t, "2026-10-23T07:09:00Z"),
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
			Command:           "go test ./internal/engine/model",
			TargetTaskVersion: 1,
			TargetCommit:      ptr(model.Commit("abc1234")),
			Result:            model.VerifyPass,
			Evidence:          []string{"exit_code:0", "go test: 12 packages ok"},
			RanAt:             mustTime(t, "2026-10-23T07:10:00Z"),
		}},
		Review: model.Review{
			Required:   true,
			Verdict:    model.VerdictApproved,
			Reviewer:   "r1",
			ReviewedAt: ptr(mustTime(t, "2026-10-23T07:12:00Z")),
		},
		Merge: model.Merge{
			Required:     true,
			Merged:       true,
			MergedCommit: ptr(model.Commit("def5678")),
			MergedAt:     ptr(mustTime(t, "2026-10-23T07:15:00Z")),
		},
		Completion: model.Completion{
			Decision:  model.DecisionDone,
			DecidedAt: mustTime(t, "2026-10-23T07:16:00Z"),
		},
	}
}

func nonCodeDelivery(t *testing.T) model.Delivery {
	t.Helper()
	d := model.Delivery{
		DeliveryID:     "dlv_002",
		TaskInstance:   "ti_vn003d",
		TaskVersion:    2,
		RunID:          "run_a002",
		Mode:           model.DeliveryNonCode,
		BaselineCommit: "dedc919",
		DeliveredAt:    mustTime(t, "2026-10-23T07:20:00Z"),
		Artifacts: []model.Artifact{{
			ArtifactID:   "art_002",
			Kind:         model.ArtifactFile,
			Ref:          "docs/x.md",
			DeclaredBy:   model.DeclaredByManual,
			Exists:       true,
			InWriteScope: true,
			SHA256:       ptr(testSHA),
		}},
		Verifications: []model.Verification{{
			VerificationID:    "vfy_002",
			Command:           "人工验收：文档口径复核",
			TargetTaskVersion: 2,
			Result:            model.VerifyManual,
			Evidence:          []string{"评审记录 #12"},
			RanAt:             mustTime(t, "2026-10-23T07:21:00Z"),
		}},
		Review: model.Review{Required: true, Verdict: model.VerdictApproved, Reviewer: "r2"},
		Completion: model.Completion{
			Decision:  model.DecisionDone,
			DecidedAt: mustTime(t, "2026-10-23T07:22:00Z"),
		},
	}
	return d
}

func manifest(d model.Delivery) model.Manifest {
	return model.Manifest{Contract: model.DeliveryContract, ProjectID: testProject, Deliveries: []model.Delivery{d}}
}

func TestCodeDeliveryDoneIsValid(t *testing.T) {
	assertClean(t, "代码交付完成", manifest(codeDelivery(t)).Validate())
}

func TestNonCodeDeliveryDoneIsValid(t *testing.T) {
	assertClean(t, "非代码交付完成", manifest(nonCodeDelivery(t)).Validate())
}

func TestExitCodeAloneCannotPass(t *testing.T) {
	d := codeDelivery(t)
	d.Verifications[0].Evidence = []string{"exit_code:0"}
	assertHas(t, "只有退出码", manifest(d).Validate(), model.CodeVerificationExitOnly)

	d2 := codeDelivery(t)
	d2.Verifications[0].Evidence = nil
	assertHas(t, "通过无证据", manifest(d2).Validate(), model.CodeVerificationNoEvidence)
}

func TestExitCodeOnlyCanOnlyBePending(t *testing.T) {
	d := codeDelivery(t)
	d.Verifications[0].Evidence = []string{"exit_code:0"}
	d.Completion.Decision = model.DecisionPending
	vs := manifest(d).Validate()
	assertHas(t, "只有退出码", vs, model.CodeVerificationExitOnly)
	assertNotHas(t, "待定不该要求通过验证", vs, model.CodeDoneWithoutPass)
}

func TestVerificationMustTargetDeliveryVersion(t *testing.T) {
	d := codeDelivery(t)
	d.Verifications[0].TargetTaskVersion = 2
	assertHas(t, "验证版本错位", manifest(d).Validate(), model.CodeVerificationVersion)
}

func TestVerificationOnOldCommit(t *testing.T) {
	d := codeDelivery(t)
	d.Verifications[0].TargetCommit = ptr(model.Commit("9999999"))
	assertHas(t, "旧提交验证", manifest(d).Validate(), model.CodeVerificationStaleCommit)

	d2 := codeDelivery(t)
	d2.Verifications[0].TargetCommit = nil
	assertHas(t, "缺被测提交", manifest(d2).Validate(), model.CodeVerificationStaleCommit)
}

func TestArtifactMustExistAndStayInScope(t *testing.T) {
	d := codeDelivery(t)
	d.Artifacts[0].Exists = false
	assertHas(t, "产物不存在", manifest(d).Validate(), model.CodeArtifactMissing)

	d2 := codeDelivery(t)
	d2.Artifacts[0].InWriteScope = false
	assertHas(t, "产物越界", manifest(d2).Validate(), model.CodeArtifactOutOfScope)
}

func TestArtifactChangedInvalidatesVerification(t *testing.T) {
	d := nonCodeDelivery(t)
	d.Verifications[0].TargetArtifactID = ptr(model.ArtifactID("art_002"))
	d.Verifications[0].TargetArtifactSHA256 = ptr(model.SHA256("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"))
	assertHas(t, "产物哈希已变", manifest(d).Validate(), model.CodeVerificationArtifact)
}

func TestDoneRequiresArtifactAndVerificationAndMerge(t *testing.T) {
	d := codeDelivery(t)
	d.Artifacts = nil
	assertHas(t, "无产物 done", manifest(d).Validate(), model.CodeDoneWithoutArtifact)

	d2 := codeDelivery(t)
	d2.Verifications[0].Result = model.VerifyPending
	d2.Verifications[0].Evidence = []string{"exit_code:0"}
	assertHas(t, "无通过验证 done", manifest(d2).Validate(), model.CodeDoneWithoutPass)

	d3 := codeDelivery(t)
	d3.Merge.Merged = false
	assertHas(t, "未合入 done", manifest(d3).Validate(), model.CodeDoneWithoutMerge)

	d4 := codeDelivery(t)
	d4.Merge.Required = false
	d4.Merge.Merged = false
	assertHas(t, "代码声明不需合入", manifest(d4).Validate(), model.CodeCompletionModeMismatch)

	d5 := codeDelivery(t)
	d5.Review.Verdict = model.VerdictChangesRequired
	assertHas(t, "评审未通过 done", manifest(d5).Validate(), model.CodeDoneWithoutApproval)

	d6 := codeDelivery(t)
	d6.Completion.Decision = model.DecisionPending
	d6.Completion.Reason = ""
	assertClean(t, "退为待定", manifest(d6).Validate())
}

func TestNonCodeCompletionPath(t *testing.T) {
	d := nonCodeDelivery(t)
	d.Verifications[0].Result = model.VerifyPass
	assertHas(t, "非代码缺人工验收", manifest(d).Validate(), model.CodeNonCodeWithoutAccept)

	d2 := nonCodeDelivery(t)
	d2.Merge = model.Merge{Required: true, Merged: true}
	assertHas(t, "非代码记录合入", manifest(d2).Validate(), model.CodeCompletionModeMismatch)
}

func TestRejectedNeedsReasonAndNoFailures(t *testing.T) {
	d := codeDelivery(t)
	d.Completion.Decision = model.DecisionRejected
	assertHas(t, "拒绝无原因", manifest(d).Validate(), model.CodeRejectedWithoutReason)

	d2 := codeDelivery(t)
	d2.Verifications = append(d2.Verifications, model.Verification{
		VerificationID:    "vfy_003",
		Command:           "go vet ./...",
		TargetTaskVersion: 1,
		TargetCommit:      ptr(model.Commit("abc1234")),
		Result:            model.VerifyFail,
		RanAt:             mustTime(t, "2026-10-23T07:11:00Z"),
	})
	assertHas(t, "done 仍有失败验证", manifest(d2).Validate(), model.CodeDoneWithFailedCheck)
}

func TestSupersedeRules(t *testing.T) {
	old := codeDelivery(t)
	old.SupersededBy = ptr(model.DeliveryID("dlv_002"))
	newer := codeDelivery(t)
	newer.DeliveryID = "dlv_002"
	newer.Supersedes = ptr(model.DeliveryID("dlv_001"))

	m := model.Manifest{Contract: model.DeliveryContract, ProjectID: testProject, Deliveries: []model.Delivery{old, newer}}
	assertHas(t, "被取代仍 done", m.Validate(), model.CodeSupersededStillDone)

	newer.Supersedes = ptr(model.DeliveryID("dlv_999"))
	old.SupersededBy = nil
	m.Deliveries = []model.Delivery{old, newer}
	assertHas(t, "取代指向不存在", m.Validate(), model.CodeSupersedeInvalid)

	newer.Supersedes = ptr(model.DeliveryID("dlv_002"))
	assertHas(t, "取代自指", m.Validate(), model.CodeSupersedeInvalid)
}

func TestDeliveryBindingAndCommitShape(t *testing.T) {
	d := codeDelivery(t)
	d.TaskInstance = ""
	assertHas(t, "缺实例绑定", manifest(d).Validate(), model.CodeDeliveryTaskMissing)

	d2 := codeDelivery(t)
	d2.TaskVersion = 0
	assertHas(t, "缺版本绑定", manifest(d2).Validate(), model.CodeDeliveryVersionMissing)

	d3 := codeDelivery(t)
	d3.RunID = ""
	assertHas(t, "缺运行绑定", manifest(d3).Validate(), model.CodeDeliveryRunMissing)

	d4 := codeDelivery(t)
	d4.CandidateCommit = nil
	assertHas(t, "代码缺候选提交", manifest(d4).Validate(), model.CodeCommitRefInvalid)

	d5 := codeDelivery(t)
	d5.BaselineCommit = "HEAD"
	assertHas(t, "基线写成别名", manifest(d5).Validate(), model.CodeCommitRefInvalid)
}

func TestDeliveryContractIdentifier(t *testing.T) {
	m := manifest(codeDelivery(t))
	m.Contract = "maw.delivery.manifest/v2"
	assertHas(t, "错契约版本", m.Validate(), model.CodeDeliveryContractUnknown)
}
