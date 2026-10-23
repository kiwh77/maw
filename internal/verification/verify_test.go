package verification_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/verification"
)

func TestMachinePassSupportsDone(t *testing.T) {
	svc := &verification.Service{
		Now:   func() time.Time { return time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC) },
		NewID: func() model.VerificationID { return "vfy_pass001" },
		Runner: func(ctx context.Context, commands []string, timeoutSec int) ([]verification.CommandResult, error) {
			return []verification.CommandResult{{
				Command: commands[0], OK: true, Output: "ok all green",
			}}, nil
		},
	}
	out, err := svc.Run(context.Background(), verification.Policy{
		Commands: []string{"go test ./..."}, Kind: verification.KindMachine,
	}, verification.Target{
		TaskVersion: 3, Commit: "abc1234", ScopeOK: true,
		DeliveryFingerprint: "fp1", CurrentFingerprint: "fp1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.SupportsDone || out.Verification.Result != model.VerifyPass {
		t.Fatalf("应支持 done: %+v", out)
	}
	if out.Verification.TargetTaskVersion != 3 || out.Verification.TargetCommit == nil {
		t.Fatal("必须绑定版本与提交")
	}
	if !out.Verification.HasResultEvidence() {
		t.Fatal("通过必须有非 exit_code 证据")
	}
}

func TestStaleCommitAndFingerprintCannotSupportDone(t *testing.T) {
	svc := &verification.Service{NewID: func() model.VerificationID { return "vfy_stale01" }}
	out, err := svc.Run(context.Background(), verification.Policy{
		Commands: []string{"true"}, Kind: verification.KindMachine,
	}, verification.Target{
		TaskVersion: 1, Commit: "oldc0de", ScopeOK: true,
		DeliveryFingerprint: "fp-old", CurrentFingerprint: "fp-new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.SupportsDone || !out.Stale || out.Verification.Result != model.VerifyStale {
		t.Fatalf("指纹漂移不能 done: %+v", out)
	}

	out, err = svc.Run(context.Background(), verification.Policy{
		Commands: []string{"true"}, Kind: verification.KindMachine,
	}, verification.Target{TaskVersion: 1, ScopeOK: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.SupportsDone || !out.Stale {
		t.Fatalf("缺提交不能 done: %+v", out)
	}
}

func TestEnvFailureAndScopeError(t *testing.T) {
	svc := &verification.Service{
		NewID: func() model.VerificationID { return "vfy_env0001" },
		Runner: func(ctx context.Context, commands []string, timeoutSec int) ([]verification.CommandResult, error) {
			return []verification.CommandResult{{
				Command: commands[0], OK: false, Output: "permission denied", EnvFail: true,
			}}, errors.New("permission denied")
		},
	}
	out, err := svc.Run(context.Background(), verification.Policy{
		Commands: []string{"go test"}, Kind: verification.KindMachine,
	}, verification.Target{
		TaskVersion: 2, Commit: "c0ffee", ScopeOK: true,
		DeliveryFingerprint: "a", CurrentFingerprint: "a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.SupportsDone || !out.EnvFailed || out.Verification.Result != model.VerifyPending {
		t.Fatalf("环境失联不能 done: %+v", out)
	}

	out, err = svc.Run(context.Background(), verification.Policy{
		Commands: []string{"go test"}, Kind: verification.KindMachine,
	}, verification.Target{
		TaskVersion: 2, Commit: "c0ffee", ScopeOK: false,
		DeliveryFingerprint: "a", CurrentFingerprint: "a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.SupportsDone || !out.ScopeError {
		t.Fatalf("越界不能 done: %+v", out)
	}
}

func TestManualIsSeparateFromMachinePass(t *testing.T) {
	svc := &verification.Service{NewID: func() model.VerificationID { return "vfy_manual1" }}
	out, err := svc.Run(context.Background(), verification.Policy{
		Kind: verification.KindManual,
	}, verification.Target{
		TaskVersion: 1, Commit: "abc", ScopeOK: true,
		DeliveryFingerprint: "x", CurrentFingerprint: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Verification.Result != model.VerifyManual || out.SupportsDone {
		t.Fatalf("人工验收不能自动 done: %+v", out)
	}
	if out.Kind != verification.KindManual {
		t.Fatal("应标记人工")
	}
}

func TestRejectEmptyMachinePolicy(t *testing.T) {
	svc := &verification.Service{}
	_, err := svc.Run(context.Background(), verification.Policy{Kind: verification.KindMachine},
		verification.Target{TaskVersion: 1, Commit: "x", ScopeOK: true})
	if err == nil {
		t.Fatal("空机器策略应拒绝")
	}
}
