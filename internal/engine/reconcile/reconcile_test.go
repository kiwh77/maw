package reconcile

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

var testNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func newRun(phase model.RunPhase, gen model.Generation) *model.Run {
	return &model.Run{
		RunID:          model.RunID("run_test"),
		Instance:       model.InstanceID("tsk_demo"),
		Version:        model.VersionNo(1),
		Generation:     gen,
		Phase:          phase,
		Reconciliation: model.ReconciliationConfirmed,
		DesiredAction:  model.ActionNone,
	}
}

func autoObs() Observation {
	return Observation{
		Source: SourceProcess,
		Ref:    "journal:run_test/stop.json",
		At:     testNow,
	}
}

func assertCode(t *testing.T, err error, want model.Code) {
	t.Helper()
	var me *model.Error
	if !errors.As(err, &me) {
		t.Fatalf("期望契约错误 %s，得到 %v", want, err)
	}
	if me.Code != want {
		t.Fatalf("错误码 = %s，期望 %s：%s", me.Code, want, me.Msg)
	}
}

func TestHeartbeatWithoutStopEvidenceKeepsOccupancy(t *testing.T) {
	run := newRun(model.PhaseRunning, 1)
	obs := autoObs()
	obs.ExitCode = -1

	v, err := Heartbeat(Request{Run: run, Observation: obs, Now: testNow})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if v.Phase != model.PhaseRunning || v.Reconciliation != model.ReconciliationUnknown {
		t.Fatalf("心跳失联只应进入核实: %+v", v)
	}
	if !v.KeepOccupancy || v.StopConfirmed {
		t.Fatalf("证据不足时必须保留占用: %+v", v)
	}
	if len(v.Evidence) != 1 || v.Evidence[0].Ref != obs.Ref {
		t.Fatalf("核实结论必须带来源引用: %+v", v.Evidence)
	}
}

func TestHeartbeatWithLiveProcessStaysUnknown(t *testing.T) {
	run := newRun(model.PhaseRunning, 1)
	obs := autoObs()
	obs.Found = true
	obs.ExitCode = -1

	v, err := Heartbeat(Request{Run: run, Observation: obs, Now: testNow})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if v.Reconciliation != model.ReconciliationUnknown || !v.KeepOccupancy || v.StopConfirmed {
		t.Fatalf("进程仍在运行不得宣布停止: %+v", v)
	}
	if !strings.Contains(strings.Join(v.Notes, " "), "只进入核实") {
		t.Fatalf("应解释为何不落终态: %+v", v.Notes)
	}
}

func TestHeartbeatPIDReuseIsUnknown(t *testing.T) {
	run := newRun(model.PhaseRunning, 1)
	obs := autoObs()
	obs.PIDReused = true
	obs.Lost = true

	v, err := Heartbeat(Request{Run: run, Observation: obs, Now: testNow})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if v.Reconciliation != model.ReconciliationUnknown || v.StopConfirmed || !v.KeepOccupancy {
		t.Fatalf("PID 复用不得宣布可复用: %+v", v)
	}
	if !strings.Contains(strings.Join(v.Notes, " "), "PID 已被复用") {
		t.Fatalf("应说明 PID 复用: %+v", v.Notes)
	}
}

func TestHeartbeatManualConfirmationKeepsSource(t *testing.T) {
	run := newRun(model.PhaseRunning, 1)
	obs := autoObs()
	obs.Source = SourceManual
	obs.Ref = "manual:ticket-42"
	obs.Actor = "wuhao"
	obs.DescendantsConfirmed = true
	obs.GroupEmpty = true

	v, err := Heartbeat(Request{Run: run, Observation: obs, Now: testNow})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if !v.StopConfirmed {
		t.Fatalf("人工确认后代停止应记停止事实: %+v", v)
	}
	if v.Phase != model.PhaseRunning || !v.KeepOccupancy {
		t.Fatalf("结果未判定时不得补造终态: %+v", v)
	}
	if v.Evidence[0].Actor != "wuhao" || v.Evidence[0].Source != SourceManual {
		t.Fatalf("人工证据必须保留来源: %+v", v.Evidence[0])
	}

	obs.Actor = ""
	if _, err := Heartbeat(Request{Run: run, Observation: obs, Now: testNow}); err == nil {
		t.Fatal("人工证据缺少处理人应被拒绝")
	}
}

func TestHeartbeatConfirmsStopAndOutcome(t *testing.T) {
	run := newRun(model.PhaseRunning, 1)
	obs := autoObs()
	obs.DescendantsConfirmed = true
	obs.GroupEmpty = true
	obs.ExitCode = 0
	outcome := model.OutcomeCompleted

	v, err := Heartbeat(Request{Run: run, Observation: obs, Outcome: &outcome, Now: testNow})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if v.Phase != model.PhaseCompleted || v.Reconciliation != model.ReconciliationConfirmed {
		t.Fatalf("停止与结果都确认后可补录终态: %+v", v)
	}
	if !v.StopConfirmed || v.KeepOccupancy {
		t.Fatalf("终态且已确认停止时不再占用: %+v", v)
	}
}

func TestHeartbeatCancelledOutcomeRequiresStopping(t *testing.T) {
	run := newRun(model.PhaseRunning, 1)
	obs := autoObs()
	obs.DescendantsConfirmed = true
	obs.GroupEmpty = true
	outcome := model.OutcomeCancelled

	_, err := Heartbeat(Request{Run: run, Observation: obs, Outcome: &outcome, Now: testNow})
	assertCode(t, err, model.CodePhaseTransitionIllegal)
}

func TestCancelRace(t *testing.T) {
	key := "cancel:run_test:1"

	finished := newRun(model.PhaseCompleted, 1)
	plan, err := Cancel(CancelRequest{Run: finished, Generation: 1, Key: key})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if plan.Result != model.ResultAlreadyFinished || plan.Phase != model.PhaseCompleted {
		t.Fatalf("结束事实先到时必须认输: %+v", plan)
	}

	running := newRun(model.PhaseRunning, 1)
	plan, err = Cancel(CancelRequest{Run: running, Generation: 1, Key: key})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if plan.Result != model.ResultPending || plan.Phase != model.PhaseStopping ||
		plan.DesiredAction != model.ActionStop || !plan.KeepOccupancy {
		t.Fatalf("取消意图应先落 stopping: %+v", plan)
	}

	stopping := newRun(model.PhaseStopping, 1)
	stopping.ProcessStopConfirmed = true
	plan, err = Cancel(CancelRequest{Run: stopping, Generation: 1, Key: key})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if plan.Result != model.ResultApplied || plan.Phase != model.PhaseCancelled || plan.KeepOccupancy {
		t.Fatalf("停止确认后才可落 cancelled: %+v", plan)
	}

	stopping.ProcessStopConfirmed = false
	plan, err = Cancel(CancelRequest{Run: stopping, Generation: 1, Key: key})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if plan.Result != model.ResultPending || plan.Phase != model.PhaseStopping {
		t.Fatalf("停止未确认时取消保持 pending: %+v", plan)
	}
}

func TestCancelRejectsStaleGenerationAndMissingKey(t *testing.T) {
	run := newRun(model.PhaseRunning, 2)
	if _, err := Cancel(CancelRequest{Run: run, Generation: 1, Key: "k"}); err == nil {
		t.Fatal("旧代际取消必须拒绝")
	} else {
		assertCode(t, err, model.CodeReleaseGeneration)
	}
	if _, err := Cancel(CancelRequest{Run: run, Generation: 2}); err == nil {
		t.Fatal("取消缺少幂等键必须拒绝")
	} else {
		assertCode(t, err, model.CodeClaimWithoutGuards)
	}
}

func TestPlanStop(t *testing.T) {
	run := newRun(model.PhaseRunning, 1)
	plan, err := PlanStop(run, 1)
	if err != nil {
		t.Fatalf("PlanStop: %v", err)
	}
	if !plan.Allowed || plan.Phase != model.PhaseStopping || plan.DesiredAction != model.ActionStop {
		t.Fatalf("显式停止应进入 stopping: %+v", plan)
	}

	done := newRun(model.PhaseCompleted, 1)
	plan, err = PlanStop(done, 1)
	if err != nil {
		t.Fatalf("PlanStop: %v", err)
	}
	if plan.Allowed || plan.Phase != model.PhaseCompleted {
		t.Fatalf("已结束的运行不再停止: %+v", plan)
	}

	if _, err := PlanStop(run, 2); err == nil {
		t.Fatal("旧代际停止必须拒绝")
	} else {
		assertCode(t, err, model.CodeReleaseGeneration)
	}
}

func TestPlanTaskCancelRequiresSettledRun(t *testing.T) {
	if plan := PlanTaskCancel(nil); !plan.Allowed || plan.Status != model.TaskCancelled {
		t.Fatalf("无进行中运行可直接取消任务: %+v", plan)
	}

	running := newRun(model.PhaseRunning, 1)
	if plan := PlanTaskCancel(running); plan.Allowed {
		t.Fatalf("运行未停稳不得取消任务: %+v", plan)
	}

	settled := newRun(model.PhaseCompleted, 1)
	settled.ProcessStopConfirmed = true
	if plan := PlanTaskCancel(settled); !plan.Allowed || plan.Status != model.TaskCancelled {
		t.Fatalf("运行结束且占用解除后应允许取消任务: %+v", plan)
	}
}

func TestPlanRetryChecksPredecessorOccupancyAndBound(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 4 * time.Second}

	running := newRun(model.PhaseRunning, 1)
	plan, err := PlanRetry(RetryRequest{Run: running, Generation: 1, Attempts: 0, Policy: policy})
	if err != nil {
		t.Fatalf("PlanRetry: %v", err)
	}
	if plan.Allowed {
		t.Fatalf("前驱未确认结束不得重试: %+v", plan)
	}

	settled := newRun(model.PhaseFailed, 1)
	settled.ProcessStopConfirmed = true
	settled.Evidence = []string{"journal:run_test/stop.json"}
	plan, err = PlanRetry(RetryRequest{Run: settled, Generation: 1, Attempts: 1, Policy: policy})
	if err != nil {
		t.Fatalf("PlanRetry: %v", err)
	}
	if !plan.Allowed || plan.Generation != 2 || plan.Attempt != 2 || plan.Delay != time.Second {
		t.Fatalf("重试应开新代际并有界退避: %+v", plan)
	}

	plan, err = PlanRetry(RetryRequest{Run: settled, Generation: 1, Attempts: 3, Policy: policy})
	if err != nil {
		t.Fatalf("PlanRetry: %v", err)
	}
	if plan.Allowed {
		t.Fatalf("重试次数用尽必须拒绝: %+v", plan)
	}

	if _, err := PlanRetry(RetryRequest{Run: settled, Generation: 0, Attempts: 1, Policy: policy}); err == nil {
		t.Fatal("过期代际重试必须拒绝")
	}

	if _, err := PlanRetry(RetryRequest{Run: nil, Generation: 1, Attempts: 0,
		Policy: RetryPolicy{MaxAttempts: 0, BaseDelay: time.Second, MaxDelay: time.Second}}); err == nil {
		t.Fatal("无边界重试策略必须拒绝")
	}
}

func TestRetryDelayIsBounded(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 10, BaseDelay: time.Second, MaxDelay: 3 * time.Second}
	want := []time.Duration{0, time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second}
	for i, w := range want {
		if got := policy.delay(i + 1); got != w {
			t.Fatalf("attempt %d delay = %s，期望 %s", i+1, got, w)
		}
	}
}

func TestPlanResumeRequiresExplicitSession(t *testing.T) {
	settled := newRun(model.PhaseCompleted, 1)
	settled.ProcessStopConfirmed = true

	plan, err := PlanResume(ResumeRequest{Run: settled, Generation: 1, SessionQuality: model.QualityObserved})
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	if plan.Allowed {
		t.Fatalf("缺少来源会话时不得恢复: %+v", plan)
	}

	plan, err = PlanResume(ResumeRequest{Run: settled, Generation: 1,
		SessionRef: "codebuddy:session-1", SessionQuality: model.QualityUnknown})
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	if plan.Allowed {
		t.Fatalf("会话质量未知时不得恢复: %+v", plan)
	}

	running := newRun(model.PhaseRunning, 1)
	plan, err = PlanResume(ResumeRequest{Run: running, Generation: 1,
		SessionRef: "codebuddy:session-1", SessionQuality: model.QualityObserved})
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	if plan.Allowed {
		t.Fatalf("前驱未停稳时不得恢复: %+v", plan)
	}

	plan, err = PlanResume(ResumeRequest{Run: settled, Generation: 1,
		SessionRef: "codebuddy:session-1", SessionQuality: model.QualityObserved, Now: testNow})
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	if !plan.Allowed || plan.Generation != 2 || len(plan.Evidence) != 1 ||
		plan.Evidence[0].Ref != "codebuddy:session-1" {
		t.Fatalf("明确来源会话应可恢复为新代际: %+v", plan)
	}

	if _, err := PlanResume(ResumeRequest{Run: settled, Generation: 0,
		SessionRef: "codebuddy:session-1", SessionQuality: model.QualityObserved}); err == nil {
		t.Fatal("过期代际恢复必须拒绝")
	}
}

func TestManualEvidenceRequiresActor(t *testing.T) {
	if _, err := ManualEvidence("manual:42", "", testNow, "人工确认进程组已清空"); err == nil {
		t.Fatal("缺少处理人的人工证据必须拒绝")
	}
	ev, err := ManualEvidence("manual:42", "wuhao", testNow, "人工确认进程组已清空")
	if err != nil {
		t.Fatalf("ManualEvidence: %v", err)
	}
	if ev.Source != SourceManual || ev.Actor != "wuhao" {
		t.Fatalf("人工证据 = %+v", ev)
	}
}
