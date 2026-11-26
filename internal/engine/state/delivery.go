package state

import (
	"github.com/kiwh77/maw/internal/engine/model"
)

// AcceptanceApplies 校验一条验证结论是否支撑**本次**交付。
//
// 对应 delivery-manifest-contract-v1 §4：
//   - 目标版本必须等于交付版本（E330）；
//   - 代码任务的通过必须针对候选提交，旧提交不背书（E331）；
//   - 产物哈希变了，旧验证对当前产物失效（E334）；
//   - 判为通过必须有结果类证据，只有退出码不算（E332、E333）。
func AcceptanceApplies(d *model.Delivery, v *model.Verification) []*model.Error {
	var out []*model.Error
	if d == nil || v == nil {
		return append(out, model.Violation(model.CodeDeliveryFieldInvalid, "delivery", "缺少交付或验证"))
	}
	if v.TargetTaskVersion != d.TaskVersion {
		out = append(out, model.Violationf(model.CodeVerificationVersion, "verification.target_task_version",
			"必须等于交付版本 %d，收到 %d", d.TaskVersion, v.TargetTaskVersion))
	}
	if d.Mode == model.DeliveryCode && v.Result == model.VerifyPass {
		if v.TargetCommit == nil || !v.TargetCommit.Valid() {
			out = append(out, model.Violation(model.CodeVerificationStaleCommit, "verification.target_commit",
				"代码验证必须记录被测提交"))
		} else if d.CandidateCommit != nil && *v.TargetCommit != *d.CandidateCommit {
			out = append(out, model.Violationf(model.CodeVerificationStaleCommit, "verification.target_commit",
				"验证的是 %s，候选是 %s：旧提交上的验证不能给新提交背书", *v.TargetCommit, *d.CandidateCommit))
		}
	}
	if v.Result == model.VerifyPass {
		if len(v.Evidence) == 0 {
			out = append(out, model.Violation(model.CodeVerificationNoEvidence, "verification.evidence",
				"判为通过必须带证据"))
		} else if !v.HasResultEvidence() {
			out = append(out, model.Violation(model.CodeVerificationExitOnly, "verification.evidence",
				"只有退出码不算通过，必须给出结果类证据"))
		}
	}
	if v.TargetArtifactID != nil {
		if art := d.Artifact(*v.TargetArtifactID); art != nil && art.SHA256 != nil &&
			v.TargetArtifactSHA256 != nil && *art.SHA256 != *v.TargetArtifactSHA256 {
			out = append(out, model.Violationf(model.CodeVerificationArtifact,
				"verification.target_artifact_sha256",
				"产物哈希已变（当前 %s），该验证对当前产物无效", *art.SHA256))
		}
	}
	return out
}

// DeriveDecision 只从证据推导完成判定；调用方不能直接声明 done。
//
// 返回的 decision 只可能是 done 或 pending：证据不足一律 pending，
// 被取代或人工拒绝属于交付链路的显式动作，不在本函数里编造。
func DeriveDecision(d *model.Delivery) (model.Decision, []*model.Error) {
	var out []*model.Error
	if d == nil {
		return model.DecisionPending, append(out, model.Violation(model.CodeDeliveryFieldInvalid, "delivery", "缺少交付"))
	}
	if !d.Mode.Valid() {
		return model.DecisionPending, append(out, model.Violationf(model.CodeDeliveryFieldInvalid, "delivery.mode",
			"只能是 code 或 non_code，收到 %q", d.Mode))
	}

	artifacts := 0
	for i := range d.Artifacts {
		a := &d.Artifacts[i]
		if a.Exists && a.InWriteScope {
			artifacts++
		}
	}

	validPass, manual := 0, 0
	failed := false
	for i := range d.Verifications {
		v := &d.Verifications[i]
		if v.Result == model.VerifyFail {
			failed = true
		}
		if len(AcceptanceApplies(d, v)) > 0 {
			continue
		}
		switch v.Result {
		case model.VerifyPass:
			validPass++
		case model.VerifyManual:
			manual++
		}
	}

	if artifacts == 0 {
		out = append(out, model.Violation(model.CodeDoneWithoutArtifact, "delivery.artifacts",
			"done 必须至少有一个已核实产物"))
	}

	switch d.Mode {
	case model.DeliveryCode:
		if validPass == 0 {
			out = append(out, model.Violation(model.CodeDoneWithoutPass, "delivery.verification",
				"done 至少需要一条有效 pass 验证"))
		}
		if !d.Merge.Merged || !d.Merge.Required {
			out = append(out, model.Violation(model.CodeDoneWithoutMerge, "delivery.merge",
				"代码任务必须合入，且不得声明不需要合入"))
		}
		if d.Review.Required && d.Review.Verdict != model.VerdictApproved {
			out = append(out, model.Violation(model.CodeDoneWithoutApproval, "delivery.review.verdict",
				"代码任务评审未通过不得 done"))
		}
	case model.DeliveryNonCode:
		if manual == 0 || d.Review.Verdict != model.VerdictApproved {
			out = append(out, model.Violation(model.CodeNonCodeWithoutAccept, "delivery.verification",
				"非代码任务缺人工验收（或评审未通过）不得 done"))
		}
		if d.Merge.Required || d.Merge.Merged {
			out = append(out, model.Violation(model.CodeCompletionModeMismatch, "delivery.merge",
				"非代码任务不得记录合入"))
		}
	}

	if failed {
		out = append(out, model.Violation(model.CodeDoneWithFailedCheck, "delivery.verification",
			"done 时不得存在未处理的 fail 验证"))
	}

	if len(out) > 0 {
		return model.DecisionPending, out
	}
	return model.DecisionDone, nil
}

// SupersedeAllowed 校验取代链：只能取代同实例的既有交付，且不得自指。
func SupersedeAllowed(current, target *model.Delivery) []*model.Error {
	var out []*model.Error
	if current == nil || target == nil {
		return append(out, model.Violation(model.CodeSupersedeInvalid, "delivery.supersedes", "取代目标不存在"))
	}
	if current.DeliveryID == target.DeliveryID {
		out = append(out, model.Violation(model.CodeSupersedeInvalid, "delivery.supersedes", "不得自指"))
	}
	if current.TaskInstance != target.TaskInstance {
		out = append(out, model.Violation(model.CodeSupersedeInvalid, "delivery.supersedes",
			"只能取代同一任务实例的交付"))
	}
	if target.Completion.Decision == model.DecisionDone && target.SupersededBy == nil {
		// 旧交付仍是 done 时，新交付必须显式把它退为 pending（调用方负责写）
		out = append(out, model.Violation(model.CodeSupersededStillDone, "delivery.supersedes",
			"被取代的交付必须先退为 pending"))
	}
	return out
}
