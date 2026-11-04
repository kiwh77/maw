package app

import "github.com/kiwh77/maw/internal/board"

// AgentActivityFor 是 app 侧消费看板 activity 的统一入口。
//
// 未知 Agent 保守返回 idle，不猜、不伪造「在推进」。看板快照里的
// AgentActivity 由 board.BuildSnapshot 推导；本文件只负责读，绝不反向
// 写 agents 表或触发占用释放（CQ-003 的语义不受影响）。
func AgentActivityFor(snap board.Snapshot, agentID string) string {
	if v, ok := snap.AgentActivity[agentID]; ok && v != "" {
		return v
	}
	return string(board.AgentActivityIdle)
}

// AgentActivityCounts 汇总三态数量，供状态输出与诊断用。
// 按快照里的 agents 逐条查表，而不是直接数 map——map 可能含已不在
// 本批快照里的 Agent，直接数会多算。
func AgentActivityCounts(snap board.Snapshot) map[string]int {
	out := map[string]int{
		string(board.AgentActivityWorking): 0,
		string(board.AgentActivityStalled): 0,
		string(board.AgentActivityIdle):    0,
	}
	for _, a := range snap.Agents {
		if a == nil {
			continue
		}
		k := AgentActivityFor(snap, a.ID)
		if _, ok := out[k]; ok {
			out[k]++
		}
	}
	return out
}
