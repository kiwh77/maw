package state

import (
	"errors"
	"fmt"

	"github.com/kiwh77/maw/internal/engine/model"
)

// ErrLeaseReleased 表示这次占用已经释放过；重复释放不该被当成一次新的资源动作。
var ErrLeaseReleased = errors.New("state: 占用已释放")

// CancelDecision 是「取消请求」此刻的结论。取消是意图，cancelled 是事实。
type CancelDecision string

const (
	// CancelPending 意图已记录，但进程尚未确认停止，不能写 cancelled。
	CancelPending CancelDecision = "pending"
	// CancelApplied 停止已确认，可以落 cancelled。
	CancelApplied CancelDecision = "applied"
	// CancelAlreadyFinished 结束事实先到，取消必须认输并保留原结果。
	CancelAlreadyFinished CancelDecision = "already_finished"
)

// CanEnterPhase 校验一次运行阶段推进；终态还要求证据与停止确认。
//
// 规则来自 runtime-state-contract-v1 §2／§3：
//   - 转移必须落在封闭集合里，终态不再出发任何转移（E221）；
//   - 核实状态 unknown 时不得落终态（E232）——观测缺失不是停止证据；
//   - cancelled 必须先确认进程已停止（E231）；
//   - failed／cancelled／timed_out 必须带证据（E230）。
func CanEnterPhase(run *model.Run, to model.RunPhase) error {
	if run == nil {
		return model.Violation(model.CodeRuntimeFieldInvalid, "run", "缺少运行")
	}
	if !to.Valid() {
		return model.Violationf(model.CodePhaseUnknown, "run.phase", "不属于十个阶段：%q", to)
	}
	if !model.CanTransition(run.Phase, to) {
		return model.Violationf(model.CodePhaseTransitionIllegal, "run.phase",
			"%s → %s 非法（终态不再出发任何转移）", run.Phase, to)
	}
	if !to.Terminal() {
		return nil
	}
	if run.Reconciliation == model.ReconciliationUnknown {
		return model.Violation(model.CodeUnknownTerminal, "run.phase",
			"核实状态 unknown 时不得落终态，应保留占用并标注待核实")
	}
	switch to {
	case model.PhaseCancelled:
		if !run.ProcessStopConfirmed {
			return model.Violation(model.CodeCancelledUnconfirmed, "run.process_stop_confirmed",
				"cancel 是意图、cancelled 是事实，中间必须隔着停止确认")
		}
	case model.PhaseFailed, model.PhaseTimedOut:
		// 证据检查在下面统一做。
	}
	if (to == model.PhaseFailed || to == model.PhaseCancelled || to == model.PhaseTimedOut) && len(run.Evidence) == 0 {
		return model.Violationf(model.CodeTerminalWithoutProof, "run.evidence", "%s 必须带证据", to)
	}
	return nil
}

// ResolveCancel 裁定一次取消请求：已结束的运行不允许被追改成 cancelled。
//
// 顺序按持久事实（阶段与停止确认）裁定，不按请求到达顺序；调用方拿到
// CancelAlreadyFinished 时必须保留原结果（E250、E251）。
func ResolveCancel(run *model.Run) (CancelDecision, error) {
	if run == nil {
		return "", model.Violation(model.CodeRuntimeFieldInvalid, "run", "缺少运行")
	}
	if run.Phase.Finished() || run.Phase == model.PhaseCancelled {
		return CancelAlreadyFinished, nil
	}
	if run.Phase == model.PhaseStopping {
		if run.ProcessStopConfirmed {
			return CancelApplied, nil
		}
		return CancelPending, nil
	}
	// 还没进入 stopping：取消意图先落盘，等 Runner 执行停止并留下证据。
	return CancelPending, nil
}

// CanRelease 校验资源释放：三种资源各有前置条件，释放代际必须与持有代际一致。
//
//   - node_slot：进程停止且已确认、运行不再计入占用（E241）；
//   - workspace：产物已冻结（E242）；
//   - merge：同样要求产物已冻结，合入算法本身由交付链路负责。
func CanRelease(run *model.Run, lease *model.Lease, releaseGen model.Generation) error {
	if run == nil || lease == nil {
		return model.Violation(model.CodeRuntimeFieldInvalid, "lease", "缺少运行或占用")
	}
	if lease.Released {
		return fmt.Errorf("%w：%s", ErrLeaseReleased, lease.LeaseID)
	}
	if !releaseGen.Valid() || releaseGen != lease.Generation {
		return model.Violationf(model.CodeReleaseGeneration, "lease.release_generation",
			"必须等于持有代际 %d，收到 %d", lease.Generation, releaseGen)
	}
	switch lease.Kind {
	case model.LeaseNodeSlot:
		if run.Occupies() {
			return model.Violation(model.CodeSlotReleasedBusy, "lease",
				"运行仍计入占用时不得释放节点槽")
		}
	case model.LeaseWorkspace:
		if !run.DeliveryFrozen {
			return model.Violation(model.CodeWorkspaceNotFrozen, "lease",
				"产物未冻结就释放工作目录")
		}
	case model.LeaseMerge:
		if !run.DeliveryFrozen {
			return model.Violation(model.CodeWorkspaceNotFrozen, "lease",
				"产物未冻结就释放合入位")
		}
	default:
		return model.Violationf(model.CodeRuntimeFieldInvalid, "lease.kind", "未知占用类型 %q", lease.Kind)
	}
	if !run.Phase.Terminal() {
		return model.Violation(model.CodeSlotReleasedBusy, "lease",
			"运行未到终态，任何占用都不得释放")
	}
	return nil
}

// FailurePolicy 决定 Run 失败后业务任务去哪：阻塞等人处理，或确认原执行结束后重新排队。
type FailurePolicy string

const (
	// FailureBlocks 进入 blocked，保留现场等人工处置。
	FailureBlocks FailurePolicy = "blocked"
	// FailureRequeues 回到 ready，重新排队（仅在确认原执行已结束时使用）。
	FailureRequeues FailurePolicy = "ready"
)

// Valid 报告策略是否合法。
func (p FailurePolicy) Valid() bool { return p == FailureBlocks || p == FailureRequeues }

// TaskStatusAfterRun 把运行结果映射成业务任务状态。
//
// 硬规则：**completed 只到 in_review，永远不直接到 done**——done 只能由交付与验收链路
// 推导（见 DeriveDecision）。unknown 不改变任务状态，保持占用与待核实。
func TaskStatusAfterRun(outcome model.Outcome, policy FailurePolicy) (model.TaskStatus, error) {
	if !outcome.Valid() {
		return "", model.Violationf(model.CodeRuntimeFieldInvalid, "outcome", "未知运行结果 %q", outcome)
	}
	switch outcome {
	case model.OutcomeCompleted:
		return model.TaskInReview, nil
	case model.OutcomeCancelled:
		return model.TaskCancelled, nil
	case model.OutcomeUnknown:
		return model.TaskInProgress, nil
	case model.OutcomeFailed, model.OutcomeTimedOut:
		if !policy.Valid() {
			return "", model.Violationf(model.CodeRuntimeFieldInvalid, "failure_policy",
				"只能是 blocked 或 ready，收到 %q", policy)
		}
		return model.TaskStatus(policy), nil
	}
	return "", model.Violationf(model.CodeRuntimeFieldInvalid, "outcome", "未知运行结果 %q", outcome)
}
