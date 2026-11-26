package model

import (
	"fmt"
	"strings"
	"time"
)

// DeliveryContract 是交付与验收契约的标识（E301）。
const DeliveryContract = "maw.delivery.manifest/v1"

// exitCodePrefix 是「非结果类证据」前缀；只有这类证据不算通过（E333）。
const exitCodePrefix = "exit_code:"

// Artifact 是一条产物引用。CLI 声明只是候选，必须由 maw 侧核实才计入交付。
type Artifact struct {
	ArtifactID   ArtifactID   `json:"artifact_id"`
	Kind         ArtifactKind `json:"kind"`
	Ref          string       `json:"ref"`
	DeclaredBy   Declarer     `json:"declared_by"`
	Exists       bool         `json:"exists"`
	InWriteScope bool         `json:"in_write_scope"`
	SHA256       *SHA256      `json:"sha256,omitempty"`
}

// Verification 是一条验证结论，必须绑定任务版本与被测对象。
type Verification struct {
	VerificationID       VerificationID     `json:"verification_id"`
	Command              string             `json:"command"`
	TargetTaskVersion    VersionNo          `json:"target_task_version"`
	TargetCommit         *Commit            `json:"target_commit,omitempty"`
	TargetArtifactID     *ArtifactID        `json:"target_artifact_id,omitempty"`
	TargetArtifactSHA256 *SHA256            `json:"target_artifact_sha256,omitempty"`
	Result               VerificationResult `json:"result"`
	Evidence             []string           `json:"evidence,omitempty"`
	RanAt                time.Time          `json:"ran_at"`
}

// HasResultEvidence 报告是否存在非退出码类证据（E333）。
func (v Verification) HasResultEvidence() bool {
	for _, e := range v.Evidence {
		if !strings.HasPrefix(e, exitCodePrefix) {
			return true
		}
	}
	return false
}

// Review 是评审结论。required=false 时必须配 not_required。
type Review struct {
	Required   bool       `json:"required"`
	Verdict    Verdict    `json:"verdict"`
	Reviewer   string     `json:"reviewer,omitempty"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
}

// Merge 是合入结论：只记录事实，不定义合入算法。
type Merge struct {
	Required     bool       `json:"required"`
	Merged       bool       `json:"merged"`
	MergedCommit *Commit    `json:"merged_commit,omitempty"`
	MergedAt     *time.Time `json:"merged_at,omitempty"`
}

// Completion 是完成判定：只能从产物、验证、评审、合入的证据推导。
type Completion struct {
	Decision  Decision  `json:"decision"`
	DecidedAt time.Time `json:"decided_at"`
	Reason    string    `json:"reason,omitempty"`
}

// Delivery 一次交付；同一任务实例可以多次交付，用 supersedes 串起来。
type Delivery struct {
	DeliveryID      DeliveryID     `json:"delivery_id"`
	TaskInstance    InstanceID     `json:"task_instance"`
	TaskVersion     VersionNo      `json:"task_version"`
	RunID           RunID          `json:"run_id"`
	Mode            DeliveryMode   `json:"mode"`
	BaselineCommit  Commit         `json:"baseline_commit"`
	CandidateCommit *Commit        `json:"candidate_commit,omitempty"`
	DeliveredAt     time.Time      `json:"delivered_at"`
	Artifacts       []Artifact     `json:"artifacts"`
	Verifications   []Verification `json:"verification"`
	Review          Review         `json:"review"`
	Merge           Merge          `json:"merge"`
	Completion      Completion     `json:"completion"`
	Supersedes      *DeliveryID    `json:"supersedes,omitempty"`
	SupersededBy    *DeliveryID    `json:"superseded_by,omitempty"`
}

// Manifest 是一次交付申报。
type Manifest struct {
	Contract   string     `json:"contract"`
	ProjectID  ProjectID  `json:"project_id"`
	Deliveries []Delivery `json:"deliveries"`
}

// Validate 按 delivery-manifest-contract-v1 §8 的不变量逐条检查。
func (m Manifest) Validate() []*Error {
	var out []*Error
	if m.Contract != DeliveryContract {
		out = append(out, Violationf(CodeDeliveryContractUnknown, "contract",
			"必须是 %s，收到 %q", DeliveryContract, m.Contract))
	}
	if !m.ProjectID.Valid() {
		out = append(out, Violation(CodeDeliveryFieldInvalid, "project_id", "必须是 16 位小写十六进制"))
	}
	byID := make(map[DeliveryID]bool, len(m.Deliveries))
	for i := range m.Deliveries {
		byID[m.Deliveries[i].DeliveryID] = true
	}
	for i := range m.Deliveries {
		out = append(out, m.Deliveries[i].validate(fmt.Sprintf("deliveries[%d]", i), byID)...)
	}
	return out
}

func (d *Delivery) validate(p string, byID map[DeliveryID]bool) []*Error {
	var out []*Error
	if !d.DeliveryID.Valid() {
		out = append(out, Violation(CodeDeliveryFieldInvalid, p+".delivery_id", "必须形如 dlv_<小写字母数字，至少 3 位>"))
	}
	// E310–E312：三项绑定不可省。
	if !d.TaskInstance.Valid() {
		out = append(out, Violation(CodeDeliveryTaskMissing, p+".task_instance", "必须绑定任务实例"))
	}
	if !d.TaskVersion.Valid() {
		out = append(out, Violation(CodeDeliveryVersionMissing, p+".task_version", "必须绑定任务定义版本"))
	}
	if !d.RunID.Valid() {
		out = append(out, Violation(CodeDeliveryRunMissing, p+".run_id", "必须绑定产生它的运行"))
	}
	if !d.Mode.Valid() {
		out = append(out, Violationf(CodeDeliveryFieldInvalid, p+".mode", "只能是 code 或 non_code，收到 %q", d.Mode))
	}
	if d.DeliveredAt.IsZero() {
		out = append(out, Violation(CodeDeliveryFieldInvalid, p+".delivered_at", "必须是可解析的 ISO8601 时间"))
	}
	// E313：基线必填；代码任务还要候选提交。
	if !d.BaselineCommit.Valid() {
		out = append(out, Violation(CodeCommitRefInvalid, p+".baseline_commit", "必须记录基线提交（7–40 位小写十六进制）"))
	}
	if d.Mode == DeliveryCode {
		if d.CandidateCommit == nil || !d.CandidateCommit.Valid() {
			out = append(out, Violation(CodeCommitRefInvalid, p+".candidate_commit", "代码交付必须记录候选提交"))
		}
	}

	out = append(out, d.validateArtifacts(p)...)
	passes, vErrs := d.validateVerifications(p)
	out = append(out, vErrs...)
	out = append(out, d.validateCompletion(p, passes)...)

	// E370、E371：重复交付。
	if d.SupersededBy != nil && d.Completion.Decision == DecisionDone {
		out = append(out, Violation(CodeSupersededStillDone, p+".completion.decision",
			"已被取代的交付不得保留 done，应退为 pending 或 rejected"))
	}
	if d.Supersedes != nil {
		switch {
		case *d.Supersedes == d.DeliveryID:
			out = append(out, Violation(CodeSupersedeInvalid, p+".supersedes", "不得自指"))
		case !byID[*d.Supersedes]:
			out = append(out, Violationf(CodeSupersedeInvalid, p+".supersedes", "指向不存在的交付 %q", *d.Supersedes))
		}
	}
	return out
}

func (d *Delivery) validateArtifacts(p string) []*Error {
	var out []*Error
	for i, a := range d.Artifacts {
		ap := fmt.Sprintf("%s.artifacts[%d]", p, i)
		if !a.ArtifactID.Valid() {
			out = append(out, Violation(CodeDeliveryFieldInvalid, ap+".artifact_id", "必须形如 art_<小写字母数字，至少 3 位>"))
		}
		if !a.Kind.Valid() {
			out = append(out, Violationf(CodeDeliveryFieldInvalid, ap+".kind", "未知产物类型 %q", a.Kind))
		}
		if !a.DeclaredBy.Valid() {
			out = append(out, Violationf(CodeDeliveryFieldInvalid, ap+".declared_by", "只能是 cli 或 manual，收到 %q", a.DeclaredBy))
		}
		if a.Kind == ArtifactCommit && !Commit(a.Ref).Valid() {
			out = append(out, Violation(CodeCommitRefInvalid, ap+".ref", "提交类产物的 ref 必须是合法提交哈希"))
		}
		if (a.Kind == ArtifactFile || a.Kind == ArtifactContentHash) &&
			(a.SHA256 == nil || !a.SHA256.Valid()) {
			out = append(out, Violation(CodeDeliveryFieldInvalid, ap+".sha256", "文件类产物必须带 64 位小写十六进制哈希"))
		}
		// E320、E321：候选不核实就不算产物。
		if !a.Exists {
			out = append(out, Violation(CodeArtifactMissing, ap+".exists", "声明了产物却查不到实体，不计入交付"))
		}
		if !a.InWriteScope {
			out = append(out, Violation(CodeArtifactOutOfScope, ap+".in_write_scope", "产物越出本卡写盘范围"))
		}
	}
	return out
}

// validateVerifications 返回可支撑 done 的通过条数。
func (d *Delivery) validateVerifications(p string) (int, []*Error) {
	var out []*Error
	passed := 0
	for i, v := range d.Verifications {
		vp := fmt.Sprintf("%s.verification[%d]", p, i)
		if !v.VerificationID.Valid() {
			out = append(out, Violation(CodeDeliveryFieldInvalid, vp+".verification_id", "必须形如 vfy_<小写字母数字，至少 3 位>"))
		}
		if !v.Result.Valid() {
			out = append(out, Violationf(CodeDeliveryFieldInvalid, vp+".result", "未知验证等级 %q", v.Result))
			continue
		}
		if v.RanAt.IsZero() {
			out = append(out, Violation(CodeDeliveryFieldInvalid, vp+".ran_at", "必须是可解析的 ISO8601 时间"))
		}
		// E330：验证必须针对本次交付的版本。
		if v.TargetTaskVersion != d.TaskVersion {
			out = append(out, Violationf(CodeVerificationVersion, vp+".target_task_version",
				"必须等于交付版本 %d，收到 %d", d.TaskVersion, v.TargetTaskVersion))
		}
		// E331：代码任务的通过结论必须针对候选提交。
		if d.Mode == DeliveryCode && v.Result == VerifyPass {
			if v.TargetCommit == nil || !v.TargetCommit.Valid() {
				out = append(out, Violation(CodeVerificationStaleCommit, vp+".target_commit", "代码验证必须记录被测提交"))
			} else if d.CandidateCommit != nil && *v.TargetCommit != *d.CandidateCommit {
				out = append(out, Violationf(CodeVerificationStaleCommit, vp+".target_commit",
					"验证的是 %s，候选是 %s：旧提交上的验证不能给新提交背书", *v.TargetCommit, *d.CandidateCommit))
			}
		}
		// E332、E333：通过必须有结果类证据。
		if v.Result == VerifyPass {
			if len(v.Evidence) == 0 {
				out = append(out, Violation(CodeVerificationNoEvidence, vp+".evidence", "判为通过必须带证据"))
			} else if !v.HasResultEvidence() {
				out = append(out, Violation(CodeVerificationExitOnly, vp+".evidence", "只有退出码不算通过，必须给出结果类证据"))
			}
		}
		// E334：产物已变，旧哈希上的验证失效。
		if v.TargetArtifactID != nil {
			if art := d.Artifact(*v.TargetArtifactID); art != nil && v.TargetArtifactSHA256 != nil &&
				art.SHA256 != nil && *art.SHA256 != *v.TargetArtifactSHA256 {
				out = append(out, Violationf(CodeVerificationArtifact, vp+".target_artifact_sha256",
					"产物哈希已变（当前 %s），该验证对当前产物无效", *art.SHA256))
			}
		}
		if v.Result == VerifyPass && v.TargetTaskVersion == d.TaskVersion && v.HasResultEvidence() {
			passed++
		}
	}
	return passed, out
}

func (d *Delivery) validateCompletion(p string, passed int) []*Error {
	var out []*Error
	c := d.Completion
	if !c.Decision.Valid() {
		out = append(out, Violationf(CodeDeliveryFieldInvalid, p+".completion.decision", "未知判定 %q", c.Decision))
		return out
	}
	if c.DecidedAt.IsZero() {
		out = append(out, Violation(CodeDeliveryFieldInvalid, p+".completion.decided_at", "必须是可解析的 ISO8601 时间"))
	}
	// E360：拒绝必须写原因。
	if c.Decision == DecisionRejected && strings.TrimSpace(c.Reason) == "" {
		out = append(out, Violation(CodeRejectedWithoutReason, p+".completion.reason", "rejected 必须写明原因"))
	}
	if c.Decision != DecisionDone {
		return out
	}

	// E341：done 必须有已核实产物。
	artifacts := 0
	for _, a := range d.Artifacts {
		if a.Exists && a.InWriteScope {
			artifacts++
		}
	}
	if artifacts == 0 {
		out = append(out, Violation(CodeDoneWithoutArtifact, p+".artifacts", "done 必须至少有一个产物"))
	}
	// E361：done 时不得存在未处理的失败验证。
	for i, v := range d.Verifications {
		if v.Result == VerifyFail {
			out = append(out, Violationf(CodeDoneWithFailedCheck,
				fmt.Sprintf("%s.verification[%d]", p, i), "done 时不得存在 fail 验证"))
		}
	}
	// E340：代码任务的 done 必须有有效通过的验证（非代码任务按 E350 走人工验收）。
	if d.Mode == DeliveryCode && passed == 0 {
		out = append(out, Violation(CodeDoneWithoutPass, p+".verification", "done 至少需要一条有效 pass 验证"))
	}
	if !d.Review.Verdict.Valid() {
		out = append(out, Violationf(CodeDeliveryFieldInvalid, p+".review.verdict", "未知评审结论 %q", d.Review.Verdict))
	}

	switch d.Mode {
	case DeliveryCode:
		// E342、E351：代码任务必须合入，不得声明「不需要合入」。
		if !d.Merge.Merged {
			out = append(out, Violation(CodeDoneWithoutMerge, p+".merge.merged", "代码任务未合入不得 done"))
		}
		if !d.Merge.Required {
			out = append(out, Violation(CodeCompletionModeMismatch, p+".merge.required", "代码任务不得声明不需要合入"))
		}
		// E343：评审必须通过。
		if d.Review.Required && d.Review.Verdict != VerdictApproved {
			out = append(out, Violation(CodeDoneWithoutApproval, p+".review.verdict",
				"代码任务评审未通过不得 done"))
		}
	case DeliveryNonCode:
		// E350：非代码任务必须有 manual 验收结论。
		manual := 0
		for _, v := range d.Verifications {
			if v.Result == VerifyManual && v.TargetTaskVersion == d.TaskVersion {
				manual++
			}
		}
		if manual == 0 || d.Review.Verdict != VerdictApproved {
			out = append(out, Violation(CodeNonCodeWithoutAccept, p+".completion.decision",
				"非代码任务缺人工/机器验收不得 done"))
		}
		// E351：非代码任务不得记录合入。
		if d.Merge.Required || d.Merge.Merged {
			out = append(out, Violation(CodeCompletionModeMismatch, p+".merge", "非代码任务不得记录合入"))
		}
	}
	return out
}

// Artifact 按身份查产物；不存在返回 nil。
func (d *Delivery) Artifact(id ArtifactID) *Artifact {
	for i := range d.Artifacts {
		if d.Artifacts[i].ArtifactID == id {
			return &d.Artifacts[i]
		}
	}
	return nil
}
