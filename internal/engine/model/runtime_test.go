package model_test

import (
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

func phaseHistory(t *testing.T, phases ...model.RunPhase) []model.PhaseEntry {
	t.Helper()
	base := mustTime(t, "2026-10-23T02:00:00Z")
	out := make([]model.PhaseEntry, len(phases))
	for i, p := range phases {
		out[i] = model.PhaseEntry{Phase: p, At: base.Add(time.Duration(i) * 10 * time.Second)}
	}
	return out
}

func phaseEvent(t *testing.T, id model.EventID, cursor model.Cursor, runID model.RunID, phase model.RunPhase) model.Event {
	t.Helper()
	return model.Event{
		EventID:       id,
		Cursor:        cursor,
		SchemaVersion: 1,
		Kind:          "run.phase",
		RunID:         runID,
		Phase:         &phase,
		OccurredAt:    mustTime(t, "2026-10-23T02:00:30Z"),
		ReceivedAt:    mustTime(t, "2026-10-23T02:00:31Z"),
		Quality:       model.QualityObserved,
	}
}

func leaseReleaseEvent(t *testing.T, id model.EventID, cursor model.Cursor, runID model.RunID, lease model.LeaseID) model.Event {
	t.Helper()
	return model.Event{
		EventID:       id,
		Cursor:        cursor,
		SchemaVersion: 1,
		Kind:          "lease.released",
		RunID:         runID,
		LeaseID:       &lease,
		OccurredAt:    mustTime(t, "2026-10-23T02:05:00Z"),
		ReceivedAt:    mustTime(t, "2026-10-23T02:05:01Z"),
		Quality:       model.QualityObserved,
	}
}

func validRuntimeSnapshot(t *testing.T) model.RuntimeSnapshot {
	t.Helper()
	run := model.Run{
		RunID:          "run_a001",
		Instance:       "ti_vn001a",
		Version:        1,
		Generation:     1,
		Phase:          model.PhaseRunning,
		PhaseHistory:   phaseHistory(t, model.PhaseReserved, model.PhasePreparing, model.PhaseLaunching, model.PhaseRunning),
		Reconciliation: model.ReconciliationConfirmed,
		DesiredAction:  model.ActionNone,
		Leases: []model.Lease{
			{LeaseID: "ls_a001", Kind: model.LeaseNodeSlot, Generation: 1},
			{LeaseID: "ls_a002", Kind: model.LeaseWorkspace, Generation: 1},
		},
	}
	return model.RuntimeSnapshot{
		Contract:      model.RuntimeContract,
		ProjectID:     testProject,
		GeneratedAt:   mustTime(t, "2026-10-23T02:10:00Z"),
		AutoExecution: model.AutoExecution{Enabled: false},
		Runs:          []model.Run{run},
		Events:        []model.Event{phaseEvent(t, "evt_001", 1, run.RunID, model.PhaseRunning)},
	}
}

func TestRuntimeSnapshotMinimalIsValid(t *testing.T) {
	s := validRuntimeSnapshot(t)
	assertClean(t, "运行中的最小快照", s.Validate())
	if !s.Runs[0].Occupies() {
		t.Fatal("running 应计入占用")
	}
}

func TestPhaseTransitionsAreClosed(t *testing.T) {
	if model.CanTransition(model.PhaseRunning, model.PhasePreparing) {
		t.Fatal("running 不得回到 preparing")
	}
	if model.CanTransition(model.PhaseCompleted, model.PhaseCancelled) {
		t.Fatal("终态不得再出发任何转移（E221）")
	}
	if !model.CanTransition(model.PhaseStopping, model.PhaseCancelled) {
		t.Fatal("stopping → cancelled 应合法")
	}

	s := validRuntimeSnapshot(t)
	s.Runs[0].PhaseHistory = phaseHistory(t, model.PhaseReserved, model.PhaseRunning)
	assertHas(t, "跳阶段", s.Validate(), model.CodePhaseTransitionIllegal)
}

func TestTerminalCannotRestart(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Runs[0].PhaseHistory = phaseHistory(t,
		model.PhaseReserved, model.PhasePreparing, model.PhaseLaunching,
		model.PhaseRunning, model.PhaseCompleted, model.PhaseRunning)
	s.Runs[0].Phase = model.PhaseRunning
	s.Runs[0].Evidence = []string{"outcome:completed"}
	assertHas(t, "终态重启", s.Validate(), model.CodePhaseTransitionIllegal)
}

func TestTerminalCannotBeRewrittenAsCancelled(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Runs[0].PhaseHistory = phaseHistory(t,
		model.PhaseReserved, model.PhasePreparing, model.PhaseFailed, model.PhaseCancelled)
	s.Runs[0].Phase = model.PhaseCancelled
	s.Runs[0].ProcessStopConfirmed = true
	s.Runs[0].Evidence = []string{"exit:1"}
	assertHas(t, "终态被追改成 cancelled", s.Validate(), model.CodeTerminalRewritten)
}

func TestPhaseHistoryMustMatchCurrentPhase(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Runs[0].Phase = model.PhaseCompleted
	s.Runs[0].Evidence = []string{"outcome:completed"}
	assertHas(t, "历史末项不符", s.Validate(), model.CodePhaseHistoryMismatch)
}

func TestPhaseTimesMustIncrease(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Runs[0].PhaseHistory = []model.PhaseEntry{
		{Phase: model.PhaseReserved, At: mustTime(t, "2026-10-23T02:00:30Z")},
		{Phase: model.PhasePreparing, At: mustTime(t, "2026-10-23T02:00:10Z")},
		{Phase: model.PhaseRunning, At: mustTime(t, "2026-10-23T02:00:20Z")},
	}
	s.Runs[0].Phase = model.PhaseRunning
	assertHas(t, "阶段时间倒序", s.Validate(), model.CodeTimeOrder)
}

func TestTerminalNeedsEvidence(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Runs[0].PhaseHistory = phaseHistory(t, model.PhaseReserved, model.PhasePreparing, model.PhaseFailed)
	s.Runs[0].Phase = model.PhaseFailed
	s.Runs[0].ProcessStopConfirmed = true
	s.Events = []model.Event{phaseEvent(t, "evt_001", 1, s.Runs[0].RunID, model.PhaseFailed)}
	assertHas(t, "终态无证据", s.Validate(), model.CodeTerminalWithoutProof)
}

func TestCancelledRequiresStopConfirmation(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Runs[0].PhaseHistory = phaseHistory(t,
		model.PhaseReserved, model.PhasePreparing, model.PhaseLaunching, model.PhaseRunning,
		model.PhaseStopping, model.PhaseCancelled)
	s.Runs[0].Phase = model.PhaseCancelled
	s.Runs[0].Evidence = []string{"user:cancel"}
	s.Runs[0].ProcessStopConfirmed = false
	s.Events = []model.Event{phaseEvent(t, "evt_001", 1, s.Runs[0].RunID, model.PhaseCancelled)}
	assertHas(t, "取消未确认停止", s.Validate(), model.CodeCancelledUnconfirmed)
}

func TestUnknownMustNotBecomeTerminal(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Runs[0].Phase = model.PhaseCompleted
	s.Runs[0].Reconciliation = model.ReconciliationUnknown
	s.Runs[0].Evidence = []string{"outcome:completed"}
	s.Events = []model.Event{phaseEvent(t, "evt_001", 1, s.Runs[0].RunID, model.PhaseCompleted)}
	assertHas(t, "unknown 落终态", s.Validate(), model.CodeUnknownTerminal)
}

func TestDoubleOccupancy(t *testing.T) {
	s := validRuntimeSnapshot(t)
	second := s.Runs[0]
	second.RunID = "run_a002"
	second.Generation = 2
	s.Runs = append(s.Runs, second)
	s.Events = append(s.Events, phaseEvent(t, "evt_002", 2, second.RunID, model.PhaseRunning))
	assertHas(t, "同实例双占用", s.Validate(), model.CodeDoubleOccupancy)
}

func TestFailedWithoutStopConfirmationStillOccupies(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Runs[0].PhaseHistory = phaseHistory(t, model.PhaseReserved, model.PhasePreparing, model.PhaseFailed)
	s.Runs[0].Phase = model.PhaseFailed
	s.Runs[0].Evidence = []string{"exit:1"}
	s.Runs[0].ProcessStopConfirmed = false
	s.Events = []model.Event{phaseEvent(t, "evt_001", 1, s.Runs[0].RunID, model.PhaseFailed)}
	// 进程未确认停止：仍占用，再派发只能排队。
	if !s.Runs[0].Occupies() {
		t.Fatal("failed 且未确认停止仍应计入占用")
	}
	assertClean(t, "failed 未确认停止", s.Validate())
}

func TestLeaseReleaseConditions(t *testing.T) {
	// E241：仍在占用时释放节点槽。
	s := validRuntimeSnapshot(t)
	s.Runs[0].Leases[0].Released = true
	s.Runs[0].Leases[0].ReleaseGeneration = ptr(model.Generation(1))
	s.Events = append(s.Events, leaseReleaseEvent(t, "evt_002", 2, s.Runs[0].RunID, "ls_a001"))
	assertHas(t, "占用中释放槽", s.Validate(), model.CodeSlotReleasedBusy)

	// E242：产物未冻结就释放目录。
	s2 := completedSnapshot(t)
	s2.Runs[0].DeliveryFrozen = false
	assertHas(t, "未冻结释放目录", s2.Validate(), model.CodeWorkspaceNotFrozen)

	// E243：释放代际与持有代际不一致。
	s3 := completedSnapshot(t)
	s3.Runs[0].Leases[1].ReleaseGeneration = ptr(model.Generation(2))
	assertHas(t, "代际不匹配释放", s3.Validate(), model.CodeReleaseGeneration)

	// E261：释放缺少 lease.released 事件。
	s4 := completedSnapshot(t)
	s4.Events = s4.Events[:2] // 去掉 workspace 的释放事件
	assertHas(t, "释放无事件", s4.Validate(), model.CodeReleaseEventMissing)
}

// completedSnapshot 是一个已停稳、产物已冻结、两个占用都已按条件释放的运行。
func completedSnapshot(t *testing.T) model.RuntimeSnapshot {
	t.Helper()
	s := validRuntimeSnapshot(t)
	run := s.Runs[0]
	run.PhaseHistory = phaseHistory(t,
		model.PhaseReserved, model.PhasePreparing, model.PhaseLaunching,
		model.PhaseRunning, model.PhaseCompleted)
	run.Phase = model.PhaseCompleted
	run.ProcessStopConfirmed = true
	run.DeliveryFrozen = true
	run.Evidence = []string{"outcome:completed"}
	run.Leases[0].Released = true
	run.Leases[0].ReleaseGeneration = ptr(model.Generation(1))
	run.Leases[0].ReleaseEventID = ptr(model.EventID("evt_002"))
	run.Leases[1].Released = true
	run.Leases[1].ReleaseGeneration = ptr(model.Generation(1))
	run.Leases[1].ReleaseEventID = ptr(model.EventID("evt_003"))
	s.Runs[0] = run
	s.Events = []model.Event{
		phaseEvent(t, "evt_001", 1, run.RunID, model.PhaseCompleted),
		leaseReleaseEvent(t, "evt_002", 2, run.RunID, "ls_a001"),
		leaseReleaseEvent(t, "evt_003", 3, run.RunID, "ls_a002"),
	}
	return s
}

func TestCompletedRunWithReleasesIsValid(t *testing.T) {
	s := completedSnapshot(t)
	assertClean(t, "完成并释放", s.Validate())
	if s.Runs[0].Occupies() {
		t.Fatal("completed 且证据齐备不应继续占用")
	}
}

func TestCancelRaceFinishedWins(t *testing.T) {
	s := completedSnapshot(t)
	s.Operations = []model.Operation{{
		OperationID: "op_a001",
		Kind:        model.OpCancel,
		RunID:       "run_a001",
		Result:      model.ResultApplied, // 追改成 cancelled 的写法
	}}
	assertHas(t, "取消未认输", s.Validate(), model.CodeCancelNotReported)

	s.Operations[0].Result = model.ResultAlreadyFinished
	assertClean(t, "取消认输", s.Validate())
}

func TestIdempotencyConflict(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Operations = []model.Operation{
		{OperationID: "op_a001", Kind: model.OpDispatch, IdempotencyKey: "k1", RequestDigest: "d1", Result: model.ResultPending},
		{OperationID: "op_a002", Kind: model.OpDispatch, IdempotencyKey: "k1", RequestDigest: "d2", Result: model.ResultPending},
	}
	assertHas(t, "幂等键冲突", s.Validate(), model.CodeIdempotencyConflict)
}

func TestEventInvariants(t *testing.T) {
	// 游标必须严格递增（E213）。
	s := validRuntimeSnapshot(t)
	dup := phaseEvent(t, "evt_002", 1, s.Runs[0].RunID, model.PhaseRunning)
	s.Events = append(s.Events, dup)
	assertHas(t, "游标重复", s.Validate(), model.CodeCursorNotIncreasing)

	// 必须带 schema_version（E214）。
	s2 := validRuntimeSnapshot(t)
	s2.Events[0].SchemaVersion = 0
	assertHas(t, "缺 schema_version", s2.Validate(), model.CodeSchemaVersionMissing)

	// 发生晚于接收却标 observed（E262）。
	s3 := validRuntimeSnapshot(t)
	s3.Events[0].OccurredAt = mustTime(t, "2026-10-23T02:00:40Z")
	s3.Events[0].ReceivedAt = mustTime(t, "2026-10-23T02:00:35Z")
	assertHas(t, "时钟倒置标 observed", s3.Validate(), model.CodeClockSkewMarkedObserved)

	// 事件挂到不存在的运行（E263）。
	s4 := validRuntimeSnapshot(t)
	s4.Events[0].RunID = "run_ghost"
	assertHas(t, "事件引用未知运行", s4.Validate(), model.CodeEventUnknownRun)

	// 事件身份重复（E212）。
	s5 := validRuntimeSnapshot(t)
	s5.Events = append(s5.Events, phaseEvent(t, "evt_001", 2, s5.Runs[0].RunID, model.PhaseRunning))
	assertHas(t, "事件身份重复", s5.Validate(), model.CodeEventDuplicate)
}

func TestPhaseRequiresCommittedEvent(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Events = nil
	assertHas(t, "状态缺事件", s.Validate(), model.CodePhaseEventMissing)
}

func TestRunAndOperationIdentityUniqueness(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Runs = append(s.Runs, s.Runs[0])
	assertHas(t, "运行身份重复", s.Validate(), model.CodeRunDuplicate)

	s2 := validRuntimeSnapshot(t)
	s2.Operations = []model.Operation{
		{OperationID: "op_a001", Kind: model.OpDispatch, Result: model.ResultPending},
		{OperationID: "op_a001", Kind: model.OpDispatch, Result: model.ResultPending},
	}
	assertHas(t, "操作身份重复", s2.Validate(), model.CodeOperationDuplicate)
}

func TestAutoExecutionNeedsLimit(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.AutoExecution = model.AutoExecution{Enabled: true, ManagedExecutionLimit: 0}
	assertHas(t, "自动执行无上限", s.Validate(), model.CodeAutoExecutionLimit)

	s.AutoExecution.ManagedExecutionLimit = 2
	assertClean(t, "自动执行有上限", s.Validate())
}

func TestRuntimeContractIdentifier(t *testing.T) {
	s := validRuntimeSnapshot(t)
	s.Contract = "maw.runtime.state/v2"
	assertHas(t, "错契约版本", s.Validate(), model.CodeRuntimeContractUnknown)

	s2 := validRuntimeSnapshot(t)
	s2.Runs[0].Phase = model.RunPhase("queued")
	assertHas(t, "未知阶段", s2.Validate(), model.CodePhaseUnknown)
}
