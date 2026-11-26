package app

import "github.com/kiwh77/maw/internal/engine/model"

// runPhasePath 用转移表做最短路：不硬编码「应该经过哪几跳」，只走契约允许的边。
func runPhasePath(from, to model.RunPhase) ([]model.RunPhase, bool) {
	if from == to {
		return nil, true
	}
	queue := [][]model.RunPhase{{from}}
	seen := map[model.RunPhase]bool{from: true}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		cur := path[len(path)-1]
		for _, next := range model.AllRunPhases() {
			if seen[next] || !model.CanTransition(cur, next) {
				continue
			}
			nextPath := append(append([]model.RunPhase{}, path...), next)
			if next == to {
				return nextPath[1:], true
			}
			seen[next] = true
			queue = append(queue, nextPath)
		}
	}
	return nil, false
}
