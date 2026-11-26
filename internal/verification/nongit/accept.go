package nongit

import (
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// Service 执行非代码产物验收。
type Service struct {
	Now   func() time.Time
	NewID func() model.VerificationID
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) newID() model.VerificationID {
	if s.NewID != nil {
		return s.NewID()
	}
	return model.VerificationID(fmt.Sprintf("vfy_%d", s.now().UnixNano()%1_000_000_000))
}

// Run 按策略执行一次非代码验收动作。
//
// Evaluate：只报告条件与 stale 旧证据。
// Accept：条件与清单齐备且有人工 reviewer 时 → VerifyManual + approved + 跳过合入 + done。
// Reject：始终写独立失败证据与 rejected（原因必填）；合入仍跳过。
func (s *Service) Run(req Request) (Outcome, error) {
	out := Outcome{
		Merge: skippedMerge(),
	}
	switch req.Action {
	case ActionEvaluate, ActionAccept, ActionReject:
	default:
		return out, model.Violationf(model.CodePortFieldInvalid, "action",
			"只能是 evaluate／accept／reject，收到 %q", req.Action)
	}
	if req.Target.TaskVersion <= 0 {
		return out, model.Violation(model.CodeVerificationVersion, "target.task_version",
			"必须绑定冻结任务版本")
	}

	missing, primary := s.checkConditions(req.Policy, req.Target)
	out.Missing = missing
	out.ConditionsOK = len(missing) == 0
	out.Stale = s.stalePriors(req.Target)

	switch req.Action {
	case ActionEvaluate:
		out.Verification = s.baseVerification(req, primary, model.VerifyPending,
			append([]string{"action:evaluate"}, conditionEvidence(missing)...))
		out.Review = model.Review{Required: true, Verdict: model.VerdictNotRequired}
		out.Completion = model.Completion{
			Decision:  model.DecisionPending,
			DecidedAt: s.now(),
			Reason:    evaluateReason(out.ConditionsOK, missing),
		}
		out.SupportsDone = false
		out.Reason = out.Completion.Reason
		return out, nil

	case ActionReject:
		reason := strings.TrimSpace(req.Reason)
		if reason == "" {
			return out, model.Violation(model.CodeRejectedWithoutReason, "reason",
				"rejected 必须写明原因")
		}
		evidence := []string{
			"action:reject",
			"reason:" + trim(reason, 200),
		}
		if r := strings.TrimSpace(req.Reviewer); r != "" {
			evidence = append(evidence, "reviewer:"+r)
		}
		evidence = append(evidence, conditionEvidence(missing)...)
		out.Verification = s.baseVerification(req, primary, model.VerifyFail, evidence)
		out.Review = model.Review{
			Required:   true,
			Verdict:    model.VerdictChangesRequired,
			Reviewer:   strings.TrimSpace(req.Reviewer),
			ReviewedAt: ptrTime(s.now()),
		}
		out.Completion = model.Completion{
			Decision:  model.DecisionRejected,
			DecidedAt: s.now(),
			Reason:    reason,
		}
		out.SupportsDone = false
		out.Reason = reason
		return out, nil

	case ActionAccept:
		reviewer := strings.TrimSpace(req.Reviewer)
		if reviewer == "" {
			return out, model.Violation(model.CodePortFieldInvalid, "reviewer",
				"人工接受必须指定 reviewer：模型自评不算验收")
		}
		note := strings.TrimSpace(req.Note)
		if note == "" {
			return out, model.Violation(model.CodeVerificationNoEvidence, "note",
				"接受必须带结果类证据说明，不得空审")
		}
		if unmet := unmetChecklist(req.Policy.Checklist, req.Satisfied); len(unmet) > 0 {
			out.Missing = append(out.Missing, unmet...)
			out.ConditionsOK = false
		}
		if !out.ConditionsOK {
			evidence := []string{
				"action:accept_blocked",
				"reviewer:" + reviewer,
				"note:" + trim(note, 200),
			}
			evidence = append(evidence, conditionEvidence(out.Missing)...)
			out.Verification = s.baseVerification(req, primary, model.VerifyFail, evidence)
			out.Review = model.Review{
				Required:   true,
				Verdict:    model.VerdictChangesRequired,
				Reviewer:   reviewer,
				ReviewedAt: ptrTime(s.now()),
			}
			out.Completion = model.Completion{
				Decision:  model.DecisionPending,
				DecidedAt: s.now(),
				Reason:    "验收条件未满足：" + strings.Join(out.Missing, "; "),
			}
			out.SupportsDone = false
			out.Reason = out.Completion.Reason
			return out, nil
		}

		evidence := []string{
			"action:accept",
			"reviewer:" + reviewer,
			"note:" + trim(note, 200),
		}
		for _, item := range req.Policy.Checklist {
			evidence = append(evidence, "checklist:"+item)
		}
		if len(req.Policy.Checklist) > 0 {
			evidence = append(evidence, fmt.Sprintf("checklist:%d/%d",
				len(req.Policy.Checklist), len(req.Policy.Checklist)))
		}
		out.Verification = s.baseVerification(req, primary, model.VerifyManual, evidence)
		out.Review = model.Review{
			Required:   true,
			Verdict:    model.VerdictApproved,
			Reviewer:   reviewer,
			ReviewedAt: ptrTime(s.now()),
		}
		out.Completion = model.Completion{
			Decision:  model.DecisionDone,
			DecidedAt: s.now(),
			Reason:    "非代码产物验收通过；合入步骤不适用已跳过",
		}
		out.SupportsDone = true
		out.Reason = out.Completion.Reason
		return out, nil
	}
	return out, nil
}

func skippedMerge() model.Merge {
	return model.Merge{Required: false, Merged: false}
}

func (s *Service) checkConditions(p Policy, t Target) (missing []string, primary *model.Artifact) {
	byID := make(map[model.ArtifactID]*model.Artifact, len(t.Artifacts))
	for i := range t.Artifacts {
		a := &t.Artifacts[i]
		byID[a.ArtifactID] = a
	}
	if len(p.Conditions) == 0 {
		// 无显式条件时：至少一个已核实的 file／content_hash 产物（E341）。
		for i := range t.Artifacts {
			a := &t.Artifacts[i]
			if (a.Kind == model.ArtifactFile || a.Kind == model.ArtifactContentHash) &&
				a.Exists && a.InWriteScope {
				return nil, a
			}
		}
		return []string{"missing_artifact:需要至少一个已核实的文件／哈希产物"}, nil
	}
	for i, c := range p.Conditions {
		if c.Kind != model.ArtifactFile && c.Kind != model.ArtifactContentHash {
			missing = append(missing, fmt.Sprintf("condition[%d]:非代码条件不得要求 commit 产物", i))
			continue
		}
		a := byID[c.ArtifactID]
		if a == nil {
			missing = append(missing, fmt.Sprintf("missing_artifact:%s", c.ArtifactID))
			continue
		}
		if a.Kind != c.Kind {
			missing = append(missing, fmt.Sprintf("kind_mismatch:%s want %s got %s",
				c.ArtifactID, c.Kind, a.Kind))
		}
		if !a.Exists {
			missing = append(missing, fmt.Sprintf("missing_artifact:%s", c.ArtifactID))
		}
		if !a.InWriteScope {
			missing = append(missing, fmt.Sprintf("out_of_scope:%s", c.ArtifactID))
		}
		if a.SHA256 == nil || !a.SHA256.Valid() {
			missing = append(missing, fmt.Sprintf("missing_sha256:%s", c.ArtifactID))
		} else if c.ExpectedSHA256 != "" && *a.SHA256 != c.ExpectedSHA256 {
			missing = append(missing, fmt.Sprintf("sha256_mismatch:%s", c.ArtifactID))
		}
		if primary == nil && a.Exists && a.InWriteScope {
			primary = a
		}
	}
	return missing, primary
}

// stalePriors 把绑定了旧产物哈希的先验验证复制为 stale（E334），不修改入参。
func (s *Service) stalePriors(t Target) []model.Verification {
	byID := make(map[model.ArtifactID]*model.Artifact, len(t.Artifacts))
	for i := range t.Artifacts {
		byID[t.Artifacts[i].ArtifactID] = &t.Artifacts[i]
	}
	var out []model.Verification
	for _, v := range t.Prior {
		if v.TargetArtifactID == nil || v.TargetArtifactSHA256 == nil {
			continue
		}
		art := byID[*v.TargetArtifactID]
		if art == nil || art.SHA256 == nil {
			continue
		}
		if *art.SHA256 == *v.TargetArtifactSHA256 {
			continue
		}
		stale := v
		stale.Result = model.VerifyStale
		stale.Evidence = append(append([]string{}, v.Evidence...),
			"stale_reason:artifact_sha256_changed",
			"current_sha256:"+string(*art.SHA256),
		)
		stale.RanAt = s.now()
		out = append(out, stale)
	}
	return out
}

func (s *Service) baseVerification(req Request, primary *model.Artifact, result model.VerificationResult, evidence []string) model.Verification {
	cmd := "manual:" + string(req.Action)
	if note := strings.TrimSpace(req.Note); note != "" && req.Action == ActionAccept {
		cmd = "manual: " + trim(note, 80)
	} else if req.Action == ActionReject {
		cmd = "manual: reject"
	}
	v := model.Verification{
		VerificationID:    s.newID(),
		Command:           cmd,
		TargetTaskVersion: req.Target.TaskVersion,
		Result:            result,
		Evidence:          evidence,
		RanAt:             s.now(),
	}
	if primary != nil {
		id := primary.ArtifactID
		v.TargetArtifactID = &id
		if primary.SHA256 != nil {
			h := *primary.SHA256
			v.TargetArtifactSHA256 = &h
		}
	}
	return v
}

func unmetChecklist(required, satisfied []string) []string {
	if len(required) == 0 {
		return nil
	}
	have := make(map[string]bool, len(satisfied))
	for _, s := range satisfied {
		have[strings.TrimSpace(s)] = true
	}
	var missing []string
	for _, item := range required {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !have[item] {
			missing = append(missing, "checklist_unmet:"+item)
		}
	}
	return missing
}

func conditionEvidence(missing []string) []string {
	if len(missing) == 0 {
		return []string{"conditions:ok"}
	}
	out := make([]string, 0, len(missing))
	for _, m := range missing {
		out = append(out, "condition_fail:"+m)
	}
	return out
}

func evaluateReason(ok bool, missing []string) string {
	if ok {
		return "产物条件满足，等待人工接受"
	}
	return "产物条件未满足：" + strings.Join(missing, "; ")
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func ptrTime(t time.Time) *time.Time { return &t }
