// Package schedule 决定哪些任务可同时变为 ready / 被领取。
// 规则：
//  1. 显式 deps 未完成 → 不可激活
//  2. write_scope 与「进行中」任务重叠 → 不可激活
//  3. write_scope 与「尚未完成且排序更靠前」的任务重叠 → 不可激活（用 priority→merge_order→id 串行）
//  4. 领取硬边界是 lane（上下文子队列）：Agent 粘在一条 lane 上，只从该队选题
//  5. 队内选题：priority→merge_order 优先；同分才用上下文亲和（deps 续链 / 写盘重叠 / 同模块前缀）打破
// 这样蓝图可一次压入同目录串行题，不必因 overlap 拆多波 apply；并行靠多 lane × 多 Agent。
package schedule

import (
	"strings"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/machine"
	"github.com/kiwh77/maw/internal/scope"
)

// Incomplete 尚未终态完成（仍占写盘序位）。
func Incomplete(t *db.Task) bool {
	if t == nil {
		return false
	}
	switch t.Status {
	case "done", "cancelled":
		return false
	default:
		return true
	}
}

// LaneOf 规范化任务子队列 id（去空白；未标则为空串默认队）。
func LaneOf(t *db.Task) string {
	if t == nil {
		return ""
	}
	return strings.TrimSpace(t.Lane)
}

// Before 表示 a 应排在 b 之前（数字越小越优先）。
func Before(a, b *db.Task) bool {
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if a.MergeOrder != b.MergeOrder {
		return a.MergeOrder < b.MergeOrder
	}
	return a.ID < b.ID
}

func depsSatisfied(t *db.Task, done map[string]bool) bool {
	for _, d := range t.Deps {
		if !done[d] {
			return false
		}
	}
	return true
}

// CanActivate 任务是否可处于 ready（或被 next 领取）。
// shared 为蓝图声明的共享文件；任一共享路径同时只能被一个 ActiveOwner 任务触及。
func CanActivate(t *db.Task, all []*db.Task) bool {
	return CanActivateShared(t, all, nil)
}

// CanActivateShared 同 CanActivate，并按 shared_files 互斥。
func CanActivateShared(t *db.Task, all []*db.Task, shared []db.SharedFileRow) bool {
	if t == nil {
		return false
	}
	done := map[string]bool{}
	for _, o := range all {
		if o.Status == "done" {
			done[o.ID] = true
		}
	}
	if !depsSatisfied(t, done) {
		return false
	}
	for _, o := range all {
		if o.ID == t.ID {
			continue
		}
		if len(scope.ScopesOverlap(t.WriteScope, o.WriteScope)) == 0 {
			continue
		}
		if machine.ActiveOwner[o.Status] {
			return false
		}
		if !Incomplete(o) {
			continue
		}
		// 重叠且对方未完成：仅当本任务排序更靠前才可激活
		if Before(o, t) {
			return false
		}
	}
	if len(shared) > 0 && !sharedFilesFree(t, all, shared) {
		return false
	}
	return true
}

// sharedFilesFree：若本任务 write_scope 覆盖某共享路径，则不允许另一 ActiveOwner 任务也覆盖该路径。
func sharedFilesFree(t *db.Task, all []*db.Task, shared []db.SharedFileRow) bool {
	myShared := overlappingSharedPaths(t.WriteScope, shared)
	if len(myShared) == 0 {
		return true
	}
	for _, o := range all {
		if o == nil || o.ID == t.ID || !machine.ActiveOwner[o.Status] {
			continue
		}
		their := overlappingSharedPaths(o.WriteScope, shared)
		for p := range myShared {
			if their[p] {
				return false
			}
		}
	}
	return true
}

func overlappingSharedPaths(writeScope []string, shared []db.SharedFileRow) map[string]bool {
	out := map[string]bool{}
	for _, sf := range shared {
		if sf.Path == "" {
			continue
		}
		if scope.PathInScope(sf.Path, writeScope) || len(scope.ScopesOverlap(writeScope, []string{sf.Path})) > 0 {
			out[sf.Path] = true
		}
	}
	return out
}

// LastContextTask 取 Agent 最近一次可作上下文锚点的题（仍属该 Owner 的 done，
// 或尚在合入管道中的 in_review/merging）。无历史则返回 nil。
func LastContextTask(all []*db.Task, agent string) *db.Task {
	if agent == "" {
		return nil
	}
	var best *db.Task
	for _, t := range all {
		if t == nil || t.Owner != agent {
			continue
		}
		switch t.Status {
		case "done", "in_review", "merging":
		default:
			continue
		}
		if best == nil || contextNewer(t, best) {
			best = t
		}
	}
	return best
}

// contextNewer 判断 a 是否比 b 更新（心跳时间戳优先，其次 revision）。
func contextNewer(a, b *db.Task) bool {
	if a.Heartbeat != b.Heartbeat {
		return a.Heartbeat > b.Heartbeat
	}
	if a.Revision != b.Revision {
		return a.Revision > b.Revision
	}
	return a.ID > b.ID
}

// AffinityScore 候选题相对上下文锚点的亲和分（越高越应优先领取）。
// 仅作队内 priority/merge_order 同分时的打破；不再跨队压过序位键。
func AffinityScore(cand, hint *db.Task) int {
	if cand == nil || hint == nil {
		return 0
	}
	score := 0
	for _, d := range cand.Deps {
		if d == hint.ID {
			score += 100
			break
		}
	}
	if len(scope.ScopesOverlap(cand.WriteScope, hint.WriteScope)) > 0 {
		score += 50
	}
	score += sharedModuleBonus(cand.WriteScope, hint.WriteScope)
	return score
}

// sharedModuleBonus：写盘路径共享模块前缀时加分（至少 2 段，如 backend/internal）。
func sharedModuleBonus(a, b []string) int {
	best := 0
	for _, x := range a {
		xp := pathSegments(prefixOfScope(x))
		for _, y := range b {
			yp := pathSegments(prefixOfScope(y))
			n := commonPrefixLen(xp, yp)
			if n > best {
				best = n
			}
		}
	}
	if best < 2 {
		return 0
	}
	// 每多共享一段 +10，封顶 40
	bonus := (best - 1) * 10
	if bonus > 40 {
		bonus = 40
	}
	return bonus
}

func prefixOfScope(pat string) string {
	pat = strings.ReplaceAll(pat, "\\", "/")
	pat = strings.TrimPrefix(pat, "./")
	if strings.HasSuffix(pat, "/**") {
		return strings.TrimSuffix(pat, "/**")
	}
	pat = strings.TrimRight(pat, "*")
	return strings.TrimRight(pat, "/")
}

func pathSegments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}

func commonPrefixLen(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// betterPick 队内选题：先 priority/merge_order，同分再用亲和，再比 id。
func betterPick(a, b *db.Task, hint *db.Task) bool {
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if a.MergeOrder != b.MergeOrder {
		return a.MergeOrder < b.MergeOrder
	}
	sa, sb := AffinityScore(a, hint), AffinityScore(b, hint)
	if sa != sb {
		return sa > sb
	}
	return a.ID < b.ID
}

// PickNextReady 按 priority/merge_order/id 选取可领取的 ready 任务（无 Agent / 无 lane 过滤）。
func PickNextReady(all []*db.Task) *db.Task {
	return PickNextReadyFor(all, "")
}

// PickNextReadyFor 为指定 Agent 选取下一题（不限 lane；兼容旧调用）。
func PickNextReadyFor(all []*db.Task, agent string) *db.Task {
	return PickNextReadyForShared(all, agent, nil)
}

// PickNextReadyForShared 同 PickNextReadyFor，并尊重 shared_files 互斥。
func PickNextReadyForShared(all []*db.Task, agent string, shared []db.SharedFileRow) *db.Task {
	return PickNextReadyInLane(all, agent, "", shared, false)
}

// PickNextReadyInLane 在指定子队列内选题。
// restrictLane=false 且 lane="" 表示不限队（仅用于兼容旧入口）；
// restrictLane=true 时即使 lane 为空串也只选默认空队列。
func PickNextReadyInLane(all []*db.Task, agent, lane string, shared []db.SharedFileRow, restrictLane bool) *db.Task {
	hint := LastContextTask(all, agent)
	var best *db.Task
	for _, t := range all {
		if t.Status != "ready" {
			continue
		}
		if restrictLane && LaneOf(t) != lane {
			continue
		}
		if !CanActivateShared(t, all, shared) {
			continue
		}
		if best == nil || betterPick(t, best, hint) {
			best = t
		}
	}
	return best
}

// SelectLane 为未绑定 Agent 挑选一条有可激活 ready 的子队列：
// 以各队队首（可激活 ready 中序位最前）比较，取全局序位最优者所在 lane。
func SelectLane(all []*db.Task, shared []db.SharedFileRow) (lane string, head *db.Task) {
	heads := map[string]*db.Task{}
	for _, t := range all {
		if t == nil || t.Status != "ready" {
			continue
		}
		if !CanActivateShared(t, all, shared) {
			continue
		}
		l := LaneOf(t)
		cur := heads[l]
		if cur == nil || Before(t, cur) {
			heads[l] = t
		}
	}
	for l, h := range heads {
		if head == nil || Before(h, head) {
			head = h
			lane = l
		}
	}
	return lane, head
}

// LaneHasActivatable 某子队列是否存在可激活 ready。
func LaneHasActivatable(all []*db.Task, lane string, shared []db.SharedFileRow) bool {
	return PickNextReadyInLane(all, "", lane, shared, true) != nil
}

// DependsOn 判断 descendant 是否（直接或间接）依赖 ancestor。
func DependsOn(graph map[string][]string, descendant, ancestor string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(n string) bool {
		if n == ancestor {
			return true
		}
		if seen[n] {
			return false
		}
		seen[n] = true
		for _, d := range graph[n] {
			if walk(d) {
				return true
			}
		}
		return false
	}
	return walk(descendant)
}

// SerializedByDeps 二者是否由依赖链强制串行。
func SerializedByDeps(graph map[string][]string, a, b string) bool {
	return DependsOn(graph, a, b) || DependsOn(graph, b, a)
}
