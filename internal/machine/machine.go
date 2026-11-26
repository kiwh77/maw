package machine

import (
	"github.com/kiwh77/maw/internal/engine/state"
	"github.com/kiwh77/maw/internal/i18n"
)

// ActiveOwner 保留原有用法（按状态名查表）。内容来自 internal/engine/state，
// 规则只有一份；这里不再维护第二张表，避免两套 ActiveOwner 漂移。
var ActiveOwner = state.ActiveOwnerSet()

// PreserveOnPlanApply 为 true 时，plan apply 不得删除该任务，也不得把状态打回 backlog。
// 覆盖：已完成 / 已取消、阻塞中、以及任何占用 Agent 的活跃状态。
func PreserveOnPlanApply(status string) bool {
	switch status {
	case "done", "cancelled", "blocked":
		return true
	default:
		return state.IsActiveOwner(status)
	}
}

// AssertTransition 校验业务任务状态转移。矩阵与语义在 internal/engine/state；
// 本函数只负责把失败翻成带下一步提示的用户可读错误。
func AssertTransition(src, dst string) error {
	if err := state.AssertTaskTransition(src, dst); err != nil {
		return i18n.E("err.bad_transition", "next.inspect_board", src, dst)
	}
	return nil
}
