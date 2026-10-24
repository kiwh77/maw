package nongit_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/verification/nongit"
)

const (
	shaA = model.SHA256("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	shaB = model.SHA256("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
)

func svc() *nongit.Service {
	return &nongit.Service{
		Now:   func() time.Time { return time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC) },
		NewID: func() model.VerificationID { return "vfy_nongit01" },
	}
}

func fileArt(id model.ArtifactID, sha model.SHA256) model.Artifact {
	h := sha
	return model.Artifact{
		ArtifactID:   id,
		Kind:         model.ArtifactFile,
		Ref:          "reports/draft.md",
		DeclaredBy:   model.DeclaredByCLI,
		Exists:       true,
		InWriteScope: true,
		SHA256:       &h,
	}
}

func TestAcceptSupportsDoneAndSkipsMerge(t *testing.T) {
	out, err := svc().Run(nongit.Request{
		Action: nongit.ActionAccept,
		Policy: nongit.Policy{
			Conditions: []nongit.Condition{{
				ArtifactID: "art_doc01", Kind: model.ArtifactFile, ExpectedSHA256: shaA,
			}},
			Checklist: []string{"口径齐全", "引用可追溯"},
		},
		Target: nongit.Target{
			TaskVersion: 3,
			Artifacts:   []model.Artifact{fileArt("art_doc01", shaA)},
		},
		Reviewer:  "boss",
		Note:      "逐条核对契约表述",
		Satisfied: []string{"口径齐全", "引用可追溯"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.SupportsDone || out.Completion.Decision != model.DecisionDone {
		t.Fatalf("应支撑 done: %+v", out)
	}
	if out.Verification.Result != model.VerifyManual {
		t.Fatalf("非代码接受应为 manual，实际 %s", out.Verification.Result)
	}
	if out.Review.Verdict != model.VerdictApproved || out.Review.Reviewer != "boss" {
		t.Fatalf("评审应 approved: %+v", out.Review)
	}
	if out.Merge.Required || out.Merge.Merged {
		t.Fatalf("合入应对非代码跳过: %+v", out.Merge)
	}
	if out.Verification.TargetTaskVersion != 3 || out.Verification.TargetArtifactID == nil {
		t.Fatal("必须绑定版本与产物")
	}
	if !out.Verification.HasResultEvidence() {
		t.Fatal("接受必须有结果类证据")
	}
}

func TestMissingArtifactBlocksAccept(t *testing.T) {
	out, err := svc().Run(nongit.Request{
		Action: nongit.ActionAccept,
		Policy: nongit.Policy{
			Conditions: []nongit.Condition{{
				ArtifactID: "art_missing", Kind: model.ArtifactFile,
			}},
		},
		Target: nongit.Target{
			TaskVersion: 1,
			Artifacts: []model.Artifact{{
				ArtifactID: "art_missing", Kind: model.ArtifactFile, Ref: "gone.md",
				DeclaredBy: model.DeclaredByCLI, Exists: false, InWriteScope: true,
			}},
		},
		Reviewer: "boss",
		Note:     "试图接受",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.SupportsDone || out.ConditionsOK || out.Completion.Decision != model.DecisionPending {
		t.Fatalf("缺产物不得接受: %+v", out)
	}
	if out.Verification.Result != model.VerifyFail {
		t.Fatalf("阻塞接受应记 fail 证据，实际 %s", out.Verification.Result)
	}
	if !containsPrefix(out.Missing, "missing_artifact:") {
		t.Fatalf("应列出缺产物: %v", out.Missing)
	}
}

func TestSHAMismatchAndStalePrior(t *testing.T) {
	prior := model.Verification{
		VerificationID:       "vfy_old0001",
		Command:              "manual: old",
		TargetTaskVersion:    2,
		TargetArtifactID:     ptrID("art_doc01"),
		TargetArtifactSHA256: ptrSHA(shaA),
		Result:               model.VerifyManual,
		Evidence:             []string{"reviewer:boss", "note:旧快照"},
		RanAt:                time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC),
	}
	out, err := svc().Run(nongit.Request{
		Action: nongit.ActionEvaluate,
		Policy: nongit.Policy{
			Conditions: []nongit.Condition{{
				ArtifactID: "art_doc01", Kind: model.ArtifactFile, ExpectedSHA256: shaA,
			}},
		},
		Target: nongit.Target{
			TaskVersion: 2,
			Artifacts:   []model.Artifact{fileArt("art_doc01", shaB)},
			Prior:       []model.Verification{prior},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.ConditionsOK || out.SupportsDone {
		t.Fatalf("哈希不符不得放行: %+v", out)
	}
	if len(out.Stale) != 1 || out.Stale[0].Result != model.VerifyStale {
		t.Fatalf("旧快照应降为 stale: %+v", out.Stale)
	}
	if prior.Result != model.VerifyManual {
		t.Fatal("不得就地改写入参 Prior")
	}

	// 针对新哈希重新接受 → 新 verification_id，合入仍跳过。
	s := &nongit.Service{
		Now:   svc().Now,
		NewID: func() model.VerificationID { return "vfy_new0001" },
	}
	acc, err := s.Run(nongit.Request{
		Action: nongit.ActionAccept,
		Policy: nongit.Policy{
			Conditions: []nongit.Condition{{
				ArtifactID: "art_doc01", Kind: model.ArtifactFile, ExpectedSHA256: shaB,
			}},
		},
		Target: nongit.Target{
			TaskVersion: 2,
			Artifacts:   []model.Artifact{fileArt("art_doc01", shaB)},
			Prior:       []model.Verification{prior},
		},
		Reviewer: "boss",
		Note:     "按新哈希复核",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !acc.SupportsDone || acc.Verification.VerificationID == prior.VerificationID {
		t.Fatalf("重新验收须独立证据: %+v", acc)
	}
	if *acc.Verification.TargetArtifactSHA256 != shaB {
		t.Fatal("新证据须绑定当前哈希")
	}
	if len(acc.Stale) != 1 {
		t.Fatalf("重新验收仍应带回 stale 旧证: %d", len(acc.Stale))
	}
	if acc.Merge.Required || acc.Merge.Merged {
		t.Fatal("重新验收仍跳过合入")
	}
}

func TestRejectKeepsIndependentEvidence(t *testing.T) {
	out, err := svc().Run(nongit.Request{
		Action: nongit.ActionReject,
		Policy: nongit.Policy{
			Conditions: []nongit.Condition{{
				ArtifactID: "art_doc01", Kind: model.ArtifactFile,
			}},
		},
		Target: nongit.Target{
			TaskVersion: 1,
			Artifacts:   []model.Artifact{fileArt("art_doc01", shaA)},
		},
		Reviewer: "boss",
		Reason:   "样本口径与需求不符",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.SupportsDone || out.Completion.Decision != model.DecisionRejected {
		t.Fatalf("拒绝应 rejected: %+v", out)
	}
	if out.Completion.Reason == "" {
		t.Fatal("拒绝必须写原因")
	}
	if out.Verification.Result != model.VerifyFail {
		t.Fatalf("拒绝证据应为 fail，实际 %s", out.Verification.Result)
	}
	if out.Merge.Required || out.Merge.Merged {
		t.Fatal("拒绝也不得记合入")
	}
	found := false
	for _, e := range out.Verification.Evidence {
		if strings.HasPrefix(e, "reason:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("拒绝证据须含 reason: %+v", out.Verification.Evidence)
	}
}

func TestRejectRequiresReason(t *testing.T) {
	_, err := svc().Run(nongit.Request{
		Action: nongit.ActionReject,
		Target: nongit.Target{TaskVersion: 1, Artifacts: []model.Artifact{fileArt("art_doc01", shaA)}},
	})
	if err == nil {
		t.Fatal("空原因应拒绝")
	}
}

func TestAcceptRequiresReviewerAndNote(t *testing.T) {
	base := nongit.Request{
		Action: nongit.ActionAccept,
		Policy: nongit.Policy{Conditions: []nongit.Condition{{
			ArtifactID: "art_doc01", Kind: model.ArtifactFile,
		}}},
		Target: nongit.Target{TaskVersion: 1, Artifacts: []model.Artifact{fileArt("art_doc01", shaA)}},
	}
	if _, err := svc().Run(base); err == nil {
		t.Fatal("缺 reviewer 应拒绝（防模型自评）")
	}
	base.Reviewer = "boss"
	if _, err := svc().Run(base); err == nil {
		t.Fatal("缺 note 应拒绝")
	}
}

func TestChecklistUnmetBlocksAccept(t *testing.T) {
	out, err := svc().Run(nongit.Request{
		Action: nongit.ActionAccept,
		Policy: nongit.Policy{
			Conditions: []nongit.Condition{{
				ArtifactID: "art_doc01", Kind: model.ArtifactFile,
			}},
			Checklist: []string{"口径齐全", "引用可追溯"},
		},
		Target: nongit.Target{
			TaskVersion: 1,
			Artifacts:   []model.Artifact{fileArt("art_doc01", shaA)},
		},
		Reviewer:  "boss",
		Note:      "只勾了一项",
		Satisfied: []string{"口径齐全"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.SupportsDone || out.Completion.Decision != model.DecisionPending {
		t.Fatalf("清单未齐不得 done: %+v", out)
	}
	if !containsPrefix(out.Missing, "checklist_unmet:") {
		t.Fatalf("应点名未勾项: %v", out.Missing)
	}
}

func TestOutOfScopeAndCommitConditionRejected(t *testing.T) {
	out, err := svc().Run(nongit.Request{
		Action: nongit.ActionEvaluate,
		Policy: nongit.Policy{
			Conditions: []nongit.Condition{
				{ArtifactID: "art_doc01", Kind: model.ArtifactFile},
				{ArtifactID: "art_code01", Kind: model.ArtifactCommit},
			},
		},
		Target: nongit.Target{
			TaskVersion: 1,
			Artifacts: []model.Artifact{{
				ArtifactID: "art_doc01", Kind: model.ArtifactFile, Ref: "x.md",
				DeclaredBy: model.DeclaredByCLI, Exists: true, InWriteScope: false,
				SHA256: ptrSHA(shaA),
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.ConditionsOK {
		t.Fatal("越界与 commit 条件应失败")
	}
	if !containsPrefix(out.Missing, "out_of_scope:") {
		t.Fatalf("应报越界: %v", out.Missing)
	}
	if !containsPrefix(out.Missing, "condition[1]:") {
		t.Fatalf("应拒 commit 条件: %v", out.Missing)
	}
}

func TestVersionRequired(t *testing.T) {
	_, err := svc().Run(nongit.Request{
		Action: nongit.ActionEvaluate,
		Target: nongit.Target{Artifacts: []model.Artifact{fileArt("art_doc01", shaA)}},
	})
	if err == nil {
		t.Fatal("缺任务版本应拒绝")
	}
}

func containsPrefix(xs []string, prefix string) bool {
	for _, x := range xs {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}

func ptrID(id model.ArtifactID) *model.ArtifactID { return &id }
func ptrSHA(h model.SHA256) *model.SHA256         { return &h }
