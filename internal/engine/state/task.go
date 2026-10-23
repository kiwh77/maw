package state

import (
	"errors"
	"fmt"

	"github.com/kiwh77/maw/internal/engine/model"
)

// 任务转移的两类错误。调用方只需要区分「同态重放」与「真的不合法」，
// 具体文案由 app 层（i18n）决定，本包不引 i18n。
var (
	// ErrSameStatus 表示源与目标相同；除 blocked 的解阻塞恢复外一律拒绝。
	ErrSameStatus = errors.New("state: 同态转移")
	// ErrIllegalTransition 表示矩阵里没有这条边。
	ErrIllegalTransition = errors.New("state: 非法转移")
)

// TransitionError 带上来源与目标，便于日志与测试断言。
type TransitionError struct {
	From string
	To   string
	Err  error
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%s：%s → %s", e.Err.Error(), e.From, e.To)
}

func (e *TransitionError) Unwrap() error { return e.Err }

// taskTransitions 是业务任务状态的唯一转移矩阵。
//
// 语义（沿用 internal/machine 的既有行为，不新增含义）：
//   - blocked 可解阻塞回任意状态（unblock 用 PrevStatus 恢复），因此不列入表；
//   - done／cancelled 是终态，不出现在键里，从它们出发的任何转移都被拒绝；
//   - 同态转移一律拒绝（含 done→done），只有 blocked→blocked 因解阻塞语义放行。
var taskTransitions = map[model.TaskStatus]map[model.TaskStatus]bool{
	model.TaskBacklog: {model.TaskReady: true, model.TaskCancelled: true, model.TaskBlocked: true},
	model.TaskReady: {model.TaskClaimed: true, model.TaskInProgress: true, model.TaskCancelled: true,
		model.TaskBlocked: true, model.TaskBacklog: true},
	model.TaskClaimed: {model.TaskInProgress: true, model.TaskReady: true, model.TaskCancelled: true,
		model.TaskBlocked: true},
	model.TaskInProgress: {model.TaskSelfCheck: true, model.TaskInReview: true, model.TaskBlocked: true,
		model.TaskCancelled: true, model.TaskReady: true},
	model.TaskSelfCheck: {model.TaskInReview: true, model.TaskInProgress: true, model.TaskBlocked: true,
		model.TaskCancelled: true},
	model.TaskInReview: {model.TaskMerging: true, model.TaskInProgress: true, model.TaskBlocked: true,
		model.TaskCancelled: true},
	model.TaskMerging: {model.TaskDone: true, model.TaskInProgress: true, model.TaskConflict: true,
		model.TaskBlocked: true, model.TaskCancelled: true},
	model.TaskConflict: {model.TaskInProgress: true, model.TaskInReview: true, model.TaskReady: true,
		model.TaskCancelled: true, model.TaskBlocked: true},
}

// CanTaskTransition 报告 src → dst 是否合法；同态一律非法（blocked 除外，见矩阵注释）。
func CanTaskTransition(src, dst string) bool {
	return AssertTaskTransition(src, dst) == nil
}

// AssertTaskTransition 校验一次任务状态转移。
func AssertTaskTransition(src, dst string) error {
	from, to := model.TaskStatus(src), model.TaskStatus(dst)
	if from == model.TaskBlocked {
		// 解阻塞：恢复到 PrevStatus 的语义下不限制目标，保持既有行为。
		return nil
	}
	if src == dst {
		return &TransitionError{From: src, To: dst, Err: ErrSameStatus}
	}
	if !from.Valid() || !to.Valid() {
		return &TransitionError{From: src, To: dst, Err: ErrIllegalTransition}
	}
	if taskTransitions[from][to] {
		return nil
	}
	return &TransitionError{From: src, To: dst, Err: ErrIllegalTransition}
}

// IsTerminalTaskStatus 报告业务任务是否已终结（done／cancelled 不再参与依赖满足）。
func IsTerminalTaskStatus(status string) bool {
	return model.TaskStatus(status).Terminal()
}

// IsActiveOwner 报告该状态是否仍由某个工作者持有（写盘序位仍被占用）。
func IsActiveOwner(status string) bool {
	return model.TaskStatus(status).IsActiveOwner()
}

// ActiveOwnerSet 返回「仍持有任务」的状态集合。
//
// 返回副本：调用方改不到内部规则。internal/machine 用它保持老的
// `machine.ActiveOwner[status]` 用法继续可读，但值的来源只有这里。
func ActiveOwnerSet() map[string]bool {
	out := map[string]bool{}
	for _, s := range model.AllTaskStatuses() {
		if s.IsActiveOwner() {
			out[string(s)] = true
		}
	}
	return out
}

// TaskStatuses 返回十一个业务状态，供看板与对齐测试使用。
func TaskStatuses() []string {
	out := []string{}
	for _, s := range model.AllTaskStatuses() {
		out = append(out, string(s))
	}
	return out
}
