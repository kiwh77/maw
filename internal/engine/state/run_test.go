package state_test

import (
	"errors"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/state"
)

func runningRun() *model.Run {
	return &model.Run{
		RunID:          "run_a001",
		Instance:       "ti_vn202a",
		Version:        1,
		Generation:     1,
		Phase:          model.PhaseRunning,
		Reconciliation: model.ReconciliationConfirmed,
		DesiredAction:  model.ActionNone,
		Leases: []model.Lease{
			{LeaseID: "ls_a001", Kind: model.LeaseNodeSlot, Generation: 1},
			{LeaseID: "ls_a002", Kind: model.LeaseWorkspace, Generation: 1},
		},
	}
}

func stoppedRun(phase model.RunPhase) *model.Run {
	r := runningRun()
	r.Phase = phase
	r.ProcessStopConfirmed = true
	r.Evidence = []string{"evidence:stop-confirmed"}
	return r
}

func TestCanEnterPhase(t *testing.T) {
	if err := state.CanEnterPhase(runningRun(), model.PhaseCompleted); err != nil {
		t.Fatalf("running → completed 应合法：%v", err)
	}
	if err := state.CanEnterPhase(runningRun(), model.PhasePreparing); err == nil {
		t.Fatal("running → preparing 必须被拒")
	} else if e, ok := model.AsError(err); !ok || e.Code != model.CodePhaseTransitionIllegal {
		t.Fatalf("期望 E221，得到 %v", err)
	}

	done := stoppedRun(model.PhaseCompleted)
	if err := state.CanEnterPhase(done, model.PhaseCancelled); err == nil {
		t.Fatal("终态之后不得再出发（E221）")
	}

	unknown := runningRun()
	unknown.Reconciliation = model.ReconciliationUnknown
	unknown.Evidence = []string{"outcome:completed"}
	if err := state.CanEnterPhase(unknown, model.PhaseCompleted); err == nil {
		t.Fatal("unknown 不得落终态（E232）")
	}

	cancelling := runningRun()
	cancelling.Phase = model.PhaseStopping
	cancelling.Evidence = []string{"user:cancel"}
	if err := state.CanEnterPhase(cancelling, model.PhaseCancelled); err == nil {
		t.Fatal("未确认停止不得写 cancelled（E231）")
	}

	cancelling.ProcessStopConfirmed = true
	cancelling.Evidence = nil
	if err := state.CanEnterPhase(cancelling, model.PhaseCancelled); err == nil {
		t.Fatal("终态无证据必须被拒（E230）")
	}
}

func TestResolveCancel(t *testing.T) {
	finished := stoppedRun(model.PhaseCompleted)
	if d, err := state.ResolveCancel(finished); err != nil || d != state.CancelAlreadyFinished {
		t.Fatalf("已完成运行 + 取消 → already_finished，得到 %v/%v", d, err)
	}
	cancelled := stoppedRun(model.PhaseCancelled)
	if d, _ := state.ResolveCancel(cancelled); d != state.CancelAlreadyFinished {
		t.Fatalf("已取消再取消应幂等返回 already_finished，得到 %v", d)
	}
	running := runningRun()
	if d, _ := state.ResolveCancel(running); d != state.CancelPending {
		t.Fatalf("运行中取消只能先记意图，得到 %v", d)
	}
	stopping := runningRun()
	stopping.Phase = model.PhaseStopping
	if d, _ := state.ResolveCancel(stopping); d != state.CancelPending {
		t.Fatalf("停止中未确认 → pending，得到 %v", d)
	}
	stopping.ProcessStopConfirmed = true
	if d, _ := state.ResolveCancel(stopping); d != state.CancelApplied {
		t.Fatalf("停止已确认 → applied，得到 %v", d)
	}
}

func TestCanRelease(t *testing.T) {
	run := runningRun()
	slot := &run.Leases[0]
	if err := state.CanRelease(run, slot, 1); err == nil {
		t.Fatal("仍在占用时释放节点槽必须被拒（E241）")
	}

	ws := &run.Leases[1]
	if err := state.CanRelease(run, ws, 1); err == nil {
		t.Fatal("产物未冻结就释放目录必须被拒（E242）")
	}

	done := stoppedRun(model.PhaseCompleted)
	done.DeliveryFrozen = true
	if err := state.CanRelease(done, &done.Leases[0], 2); err == nil {
		t.Fatal("释放代际不一致必须被拒（E243）")
	}
	if err := state.CanRelease(done, &done.Leases[0], 1); err != nil {
		t.Fatalf("停稳且已冻结应允许释放节点槽：%v", err)
	}
	if err := state.CanRelease(done, &done.Leases[1], 1); err != nil {
		t.Fatalf("产物已冻结应允许释放目录：%v", err)
	}

	released := done.Leases[0]
	released.Released = true
	if err := state.CanRelease(done, &released, 1); !errors.Is(err, state.ErrLeaseReleased) {
		t.Fatalf("重复释放应报 ErrLeaseReleased，得到 %v", err)
	}

	runningFrozen := runningRun()
	runningFrozen.DeliveryFrozen = true
	if err := state.CanRelease(runningFrozen, &runningFrozen.Leases[1], 1); err == nil {
		t.Fatal("运行未到终态不得释放任何占用")
	}
}

func TestRunOutcomeNeverYieldsDone(t *testing.T) {
	outcomes := []model.Outcome{
		model.OutcomeCompleted, model.OutcomeFailed, model.OutcomeCancelled,
		model.OutcomeTimedOut, model.OutcomeUnknown,
	}
	for _, o := range outcomes {
		status, err := state.TaskStatusAfterRun(o, state.FailureBlocks)
		if err != nil {
			t.Fatalf("%s 应可映射：%v", o, err)
		}
		if status == model.TaskDone {
			t.Fatalf("%s 直接映射成了 done——完成只能由验收链路推导", o)
		}
	}
}

func TestRunOutcomeMapping(t *testing.T) {
	cases := []struct {
		outcome model.Outcome
		policy  state.FailurePolicy
		want    model.TaskStatus
	}{
		{model.OutcomeCompleted, state.FailureBlocks, model.TaskInReview},
		{model.OutcomeCancelled, state.FailureBlocks, model.TaskCancelled},
		{model.OutcomeUnknown, state.FailureBlocks, model.TaskInProgress},
		{model.OutcomeFailed, state.FailureBlocks, model.TaskBlocked},
		{model.OutcomeFailed, state.FailureRequeues, model.TaskReady},
		{model.OutcomeTimedOut, state.FailureRequeues, model.TaskReady},
	}
	for _, c := range cases {
		got, err := state.TaskStatusAfterRun(c.outcome, c.policy)
		if err != nil || got != c.want {
			t.Fatalf("%s + %s：want %s，got %s（err=%v）", c.outcome, c.policy, c.want, got, err)
		}
	}
	if _, err := state.TaskStatusAfterRun(model.OutcomeFailed, "ignore"); err == nil {
		t.Fatal("未知失败策略必须被拒")
	}
	if _, err := state.TaskStatusAfterRun("shipped", state.FailureBlocks); err == nil {
		t.Fatal("未知运行结果必须被拒")
	}
}
