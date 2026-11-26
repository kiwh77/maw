package verification

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/gitops"
)

// Policy 是冻结在任务／工程定义里的验证策略（不是模型输出）。
type Policy struct {
	Commands   []string
	TimeoutSec int
	// Kind 区分机器自动 gate 与人工验收入口。
	Kind Kind
}

// Kind 是验证来源。
type Kind string

const (
	KindMachine Kind = "machine"
	KindManual  Kind = "manual"
)

// Target 是被测对象：必须与交付快照一致。
type Target struct {
	DeliveryID    string
	TaskVersion   model.VersionNo
	Commit        model.Commit
	ArtifactID    model.ArtifactID
	ArtifactSHA256 model.SHA256
	// DeliveryFingerprint 是收取时冻结的指纹；漂移则 stale。
	DeliveryFingerprint string
	CurrentFingerprint  string
	// ScopeOK 为 false 表示产物／改动越出 write_scope。
	ScopeOK bool
}

// CommandResult 是一条策略命令的执行证据。
type CommandResult struct {
	Command string
	OK      bool
	Output  string
	EnvFail bool // 超时／权限／工具缺失等环境问题
}

// Runner 执行策略命令；生产可接 gitops.RunGatesDetailed，测试注入假实现。
type Runner func(ctx context.Context, commands []string, timeoutSec int) ([]CommandResult, error)

// GatesRunner 把 gitops 语义适配为 Runner（可选依赖，测试可不走）。
func GatesRunner() Runner {
	return func(ctx context.Context, commands []string, timeoutSec int) ([]CommandResult, error) {
		_ = ctx
		results, err := gitops.RunGatesDetailed(commands, ".", timeoutSec)
		out := make([]CommandResult, 0, len(results))
		for _, g := range results {
			cr := CommandResult{Command: g.Command, OK: g.OK, Output: g.Output, EnvFail: g.EnvFailure}
			if !g.OK && !cr.EnvFail && gitops.IsEnvFailure(g.Output) {
				cr.EnvFail = true
			}
			out = append(out, cr)
		}
		return out, err
	}
}

// Outcome 是一次验收结论。
type Outcome struct {
	Verification model.Verification
	Kind         Kind
	SupportsDone bool
	Reason       string
	EnvFailed    bool
	ScopeError   bool
	Stale        bool
	CommandLog   []CommandResult
}

// Service 执行版本绑定验收。
type Service struct {
	Runner Runner
	Now    func() time.Time
	// NewID 分配 verification_id；测试可固定。
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

// Run 按策略验收 Target。
//
// 人工策略不跑命令，直接得到 VerifyManual（仍不算自动 done）。
// 机器策略跑完后：环境失败／范围错误／指纹或提交漂移 → SupportsDone=false。
func (s *Service) Run(ctx context.Context, policy Policy, target Target) (Outcome, error) {
	out := Outcome{Kind: policy.Kind}
	if len(policy.Commands) == 0 && policy.Kind == KindMachine {
		return out, model.Violation(model.CodePortFieldInvalid, "policy.commands",
			"机器验证必须提供用户／工程定义的命令")
	}
	if target.TaskVersion <= 0 {
		return out, model.Violation(model.CodeVerificationVersion, "target.task_version",
			"必须绑定冻结任务版本")
	}
	if strings.TrimSpace(string(target.Commit)) == "" {
		out.Stale = true
		out.SupportsDone = false
		out.Reason = "缺少被测提交"
		out.Verification = s.baseVerification(policy, target, model.VerifyStale, []string{"missing_commit"})
		return out, nil
	}
	if !target.ScopeOK {
		out.ScopeError = true
		out.SupportsDone = false
		out.Reason = "验证范围越界"
		out.Verification = s.baseVerification(policy, target, model.VerifyFail,
			[]string{"scope_error", "exit_code:1"})
		return out, nil
	}
	if target.DeliveryFingerprint != "" && target.CurrentFingerprint != "" &&
		target.DeliveryFingerprint != target.CurrentFingerprint {
		out.Stale = true
		out.SupportsDone = false
		out.Reason = "交付指纹已变，旧验证失效"
		out.Verification = s.baseVerification(policy, target, model.VerifyStale,
			[]string{"fingerprint_mismatch"})
		return out, nil
	}

	if policy.Kind == KindManual {
		out.Verification = s.baseVerification(policy, target, model.VerifyManual,
			[]string{"manual_required"})
		out.SupportsDone = false
		out.Reason = "等待人工验收"
		return out, nil
	}

	run := s.Runner
	if run == nil {
		run = GatesRunner()
	}
	timeout := policy.TimeoutSec
	if timeout <= 0 {
		timeout = 60
	}
	logs, err := run(ctx, policy.Commands, timeout)
	out.CommandLog = logs
	evidence := make([]string, 0, len(logs)*2+1)
	allOK := err == nil
	envFail := false
	for _, c := range logs {
		evidence = append(evidence, "command:"+c.Command)
		if c.OK {
			evidence = append(evidence, "output:"+trim(c.Output, 200))
		} else {
			allOK = false
			if c.EnvFail {
				envFail = true
				evidence = append(evidence, "env_failure:"+trim(c.Output, 200))
			} else {
				evidence = append(evidence, "exit_code:1")
				evidence = append(evidence, "output:"+trim(c.Output, 200))
			}
		}
	}
	if err != nil {
		allOK = false
		msg := err.Error()
		if gitops.IsEnvFailure(msg) {
			envFail = true
			evidence = append(evidence, "env_failure:"+trim(msg, 200))
		} else {
			evidence = append(evidence, "exit_code:1", "error:"+trim(msg, 200))
		}
	}
	out.EnvFailed = envFail
	result := model.VerifyFail
	if allOK {
		result = model.VerifyPass
		// 通过必须有非 exit_code 证据
		evidence = append(evidence, "gate:pass")
	}
	out.Verification = s.baseVerification(policy, target, result, evidence)
	if envFail {
		out.SupportsDone = false
		out.Reason = "验证运行环境失联或不可用"
		out.Verification.Result = model.VerifyPending
		return out, nil
	}
	if result != model.VerifyPass || !out.Verification.HasResultEvidence() {
		out.SupportsDone = false
		out.Reason = "验证未通过或证据不足"
		return out, nil
	}
	out.SupportsDone = true
	out.Reason = "机器验证通过且绑定当前版本／提交"
	return out, nil
}

func (s *Service) baseVerification(policy Policy, target Target, result model.VerificationResult, evidence []string) model.Verification {
	cmd := ""
	if len(policy.Commands) > 0 {
		cmd = strings.Join(policy.Commands, " && ")
	} else if policy.Kind == KindManual {
		cmd = "manual"
	}
	v := model.Verification{
		VerificationID:    s.newID(),
		Command:           cmd,
		TargetTaskVersion: target.TaskVersion,
		Result:            result,
		Evidence:          evidence,
		RanAt:             s.now(),
	}
	c := target.Commit
	v.TargetCommit = &c
	if target.ArtifactID != "" {
		id := target.ArtifactID
		v.TargetArtifactID = &id
	}
	if target.ArtifactSHA256 != "" {
		h := target.ArtifactSHA256
		v.TargetArtifactSHA256 = &h
	}
	return v
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
