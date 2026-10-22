package machine

import "github.com/wuhao/maw/internal/errx"

var transitions = map[string]map[string]bool{
	"backlog":     {"ready": true, "cancelled": true, "blocked": true},
	"ready":       {"claimed": true, "in_progress": true, "cancelled": true, "blocked": true, "backlog": true},
	"claimed":     {"in_progress": true, "ready": true, "cancelled": true, "blocked": true},
	"in_progress": {"self_check": true, "in_review": true, "blocked": true, "cancelled": true, "ready": true},
	"self_check":  {"in_review": true, "in_progress": true, "blocked": true, "cancelled": true},
	"in_review":   {"merging": true, "in_progress": true, "blocked": true, "cancelled": true},
	"merging":     {"done": true, "in_progress": true, "conflict": true, "blocked": true, "cancelled": true},
	"conflict":    {"in_progress": true, "in_review": true, "ready": true, "cancelled": true, "blocked": true},
}

var ActiveOwner = map[string]bool{
	"claimed": true, "in_progress": true, "self_check": true,
	"in_review": true, "merging": true, "conflict": true,
}

// PreserveOnPlanApply 为 true 时，plan apply 不得删除该任务，也不得把状态打回 backlog。
// 覆盖：已完成 / 已取消、阻塞中、以及任何占用 Agent 的活跃状态。
func PreserveOnPlanApply(status string) bool {
	switch status {
	case "done", "cancelled", "blocked":
		return true
	default:
		return ActiveOwner[status]
	}
}

func AssertTransition(src, dst string) error {
	// blocked 可跳到任意态（unblock 恢复 PrevStatus）；同态转移一律拒绝（含 done→done）。
	if src == "blocked" {
		return nil
	}
	if src == dst {
		return errx.New("illegal status transition "+src+" -> "+dst, "run `maw board` to inspect task status")
	}
	if m := transitions[src]; m != nil && m[dst] {
		return nil
	}
	return errx.New("illegal status transition "+src+" -> "+dst, "run `maw board` to inspect task status")
}
