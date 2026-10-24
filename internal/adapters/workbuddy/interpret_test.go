package workbuddy

import (
	"context"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

func intPtr(v int) *int { return &v }

// TestInterpretTimedOut 覆盖超时：确定结论，与来源说了什么无关。
func TestInterpretTimedOut(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{TimedOut: true})
	if err != nil {
		t.Fatalf("超时不应报错：%v", err)
	}
	if res.Outcome != model.OutcomeTimedOut {
		t.Fatalf("应判 timed_out，实际 %s", res.Outcome)
	}
}

// TestInterpretAuthFromApplicationError 是本卡验收第二条的主用例：
// 两包认证缺失的信号在 stderr，调用方以应用错误传进来，必须判成「需登录」而不是普通失败。
func TestInterpretAuthFromApplicationError(t *testing.T) {
	for _, kind := range []DistributionKind{DistributionCN, DistributionIntl} {
		a, _, dist, _ := probedAdapter(t, kind)
		res, err := a.Interpret(context.Background(), ports.InterpretRequest{
			ApplicationError: "Authentication required. Please use /login command to sign in to your account",
			ExitCode:         intPtr(0),
		})
		e := requirePortErr(t, err, model.PortErrAuthRequired)
		if res.Outcome != model.OutcomeFailed {
			t.Fatalf("%s 认证缺失应判 failed，实际 %s", kind, res.Outcome)
		}
		// 人工接入与处理信息：说清在哪个包、做什么、maw 不会做什么。
		for _, want := range []string{dist.AppName, dist.AppVersion, "/login", "不代登录"} {
			if !strings.Contains(e.Msg, want) {
				t.Errorf("%s 的 auth_required 文案应包含 %q，实际 %q", kind, want, e.Msg)
			}
		}
	}
}

// TestInterpretGenericApplicationError 覆盖普通应用错误。
func TestInterpretGenericApplicationError(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{ApplicationError: "spawn EACCES"})
	requirePortErr(t, err, model.PortErrApplication)
	if res.Outcome != model.OutcomeFailed || res.ResultText != "spawn EACCES" {
		t.Fatalf("应保留原文并判 failed，实际 %+v", res)
	}
}

// TestInterpretMissingResult 覆盖「没有结果记录」：退出码 0 不是完成依据。
func TestInterpretMissingResult(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)

	_, err := a.Interpret(context.Background(), ports.InterpretRequest{ExitCode: intPtr(0)})
	e := requirePortErr(t, err, model.PortErrMissingResult)
	if !strings.Contains(e.Msg, "stderr") {
		t.Errorf("错误文案应提示把 stderr 原文交给判定，实际 %q", e.Msg)
	}

	res, err := a.Interpret(context.Background(), ports.InterpretRequest{ExitCode: intPtr(2)})
	if err != nil {
		t.Fatalf("非零退出码不需要判定错误：%v", err)
	}
	if res.Outcome != model.OutcomeFailed {
		t.Fatalf("非零退出码且无结果记录应判 failed，实际 %s", res.Outcome)
	}
}

// TestInterpretRefusesUnobservedShape 覆盖「有结果记录但形状未观测」：
// 这是最坏的一类误判（把未知形状当成功），必须留在待核实。
func TestInterpretRefusesUnobservedShape(t *testing.T) {
	a, _, _, _ := probedAdapter(t, DistributionCN)
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: []byte(`{"type":"result","subtype":"success","result":"done"}`),
		ExitCode:    intPtr(0),
	})
	requirePortErr(t, err, model.PortErrProtocolUnknown)
	if res.Outcome != model.OutcomeUnknown {
		t.Fatalf("形状未观测时必须保持 unknown，实际 %s", res.Outcome)
	}
}

// TestInterpretRequiresProbe 覆盖形状判定缺少探测事实。
func TestInterpretRequiresProbe(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	a := newAdapter(t, cfg, nil)

	_, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: []byte(`{"type":"result","subtype":"success","result":"done"}`),
	})
	requirePortErr(t, err, model.PortErrCapabilityMissing)
}

// observedAdapter 返回一个「形状已观测」的适配器，用于验证证据到位后的判定语义。
func observedAdapter(t *testing.T, kind DistributionKind) *Adapter {
	t.Helper()
	dist, ok := Lookup(kind)
	if !ok {
		t.Fatalf("未登记的发行包 %q", kind)
	}
	cfg, _, _ := testNode(t, kind)
	a := newAdapter(t, cfg, nil)
	forceMeasurement(a, dist, observedCaps(dist, capResultRecord), parseHelpFlags([]byte(helpFixture)))
	return a
}

// TestInterpretCompletedWhenShapeObserved 覆盖正常完成：subtype=success + 正文非空 + 退出码 0。
func TestInterpretCompletedWhenShapeObserved(t *testing.T) {
	a := observedAdapter(t, DistributionCN)
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: []byte(`{"type":"result","subtype":"success","result":"MAW-OK"}`),
		ExitCode:    intPtr(0),
	})
	if err != nil {
		t.Fatalf("完成判定不应报错：%v", err)
	}
	if res.Outcome != model.OutcomeCompleted || res.ResultText != "MAW-OK" {
		t.Fatalf("应判 completed 并保留正文，实际 %+v", res)
	}
}

// TestInterpretSuccessNeedsDeliverable 覆盖三种「有 success 标记但不可采信」。
func TestInterpretSuccessNeedsDeliverable(t *testing.T) {
	a := observedAdapter(t, DistributionCN)

	// 空正文：成功标记不等于交付。
	_, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: []byte(`{"type":"result","subtype":"success","result":"   "}`),
		ExitCode:    intPtr(0),
	})
	requirePortErr(t, err, model.PortErrMissingResult)

	// 退出码与记录冲突：证据打架，不取其中一方。
	_, err = a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: []byte(`{"type":"result","subtype":"success","result":"MAW-OK"}`),
		ExitCode:    intPtr(1),
	})
	requirePortErr(t, err, model.PortErrProtocolUnknown)

	// 自相矛盾：subtype 说成功、is_error 说错误。
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: []byte(`{"type":"result","subtype":"success","is_error":true,"errors":["boom"]}`),
		ExitCode:    intPtr(0),
	})
	requirePortErr(t, err, model.PortErrApplication)
	if res.Outcome != model.OutcomeFailed || res.Quality != model.QualityUnknown {
		t.Fatalf("自相矛盾应判 failed 且质量降级，实际 %+v", res)
	}
}

// TestInterpretErrorRecord 覆盖 error_during_execution：认证缺失与普通错误要分开。
func TestInterpretErrorRecord(t *testing.T) {
	a := observedAdapter(t, DistributionCN)

	_, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: []byte(`{"type":"result","subtype":"error_during_execution","errors":["Authentication required. Please use /login command to sign in to your account"]}`),
		ExitCode:    intPtr(0),
	})
	requirePortErr(t, err, model.PortErrAuthRequired)

	_, err = a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: []byte(`{"type":"result","subtype":"error_during_execution","errors":["tool crashed"]}`),
		ExitCode:    intPtr(1),
	})
	requirePortErr(t, err, model.PortErrApplication)
}

// TestInterpretUnknownSubtype 覆盖未识别 subtype：既不判完成也不判失败。
func TestInterpretUnknownSubtype(t *testing.T) {
	a := observedAdapter(t, DistributionCN)
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: []byte(`{"type":"result","subtype":"needs_review","result":"hm"}`),
		ExitCode:    intPtr(0),
	})
	requirePortErr(t, err, model.PortErrProtocolUnknown)
	if res.Outcome != model.OutcomeUnknown {
		t.Fatalf("未识别 subtype 应保持 unknown，实际 %s", res.Outcome)
	}
}

// TestInterpretRejectsNonResultRecord 覆盖「最终结果不是 result 记录」。
func TestInterpretRejectsNonResultRecord(t *testing.T) {
	a := observedAdapter(t, DistributionCN)
	_, err := a.Interpret(context.Background(), ports.InterpretRequest{
		FinalResult: []byte(`{"type":"assistant"}`),
	})
	requirePortErr(t, err, model.PortErrProtocolUnknown)
}

// TestAuthHintNamesDistribution 钉住人工处理信息的按包差异。
func TestAuthHintNamesDistribution(t *testing.T) {
	cn, _ := Lookup(DistributionCN)
	intl, _ := Lookup(DistributionIntl)

	for _, d := range []Distribution{cn, intl} {
		hint := AuthHint(d)
		for _, want := range []string{d.AppName, d.AppVersion, string(d.Kind), d.BundleID, "/login"} {
			if !strings.Contains(hint, want) {
				t.Errorf("%s 的指引应包含 %q，实际 %q", d.Kind, want, hint)
			}
		}
	}
	if cn.BundleID == intl.BundleID || strings.Contains(AuthHint(cn), intl.BundleID) {
		t.Fatal("两包的指引不应互相混淆：登录的是各自 App 的账号")
	}
}
