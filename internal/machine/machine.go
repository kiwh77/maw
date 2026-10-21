package machine

import "github.com/wuhao/goal-agents/internal/errx"

var transitions = map[string]map[string]bool{
	"backlog":     {"ready": true, "cancelled": true, "blocked": true},
	"ready":       {"claimed": true, "in_progress": true, "cancelled": true, "blocked": true},
	"claimed":     {"in_progress": true, "ready": true, "cancelled": true, "blocked": true},
	"in_progress": {"self_check": true, "in_review": true, "blocked": true, "cancelled": true, "ready": true},
	"self_check":  {"in_review": true, "in_progress": true, "blocked": true, "cancelled": true},
	"in_review":   {"merging": true, "in_progress": true, "blocked": true, "cancelled": true},
	"merging":     {"done": true, "in_progress": true, "conflict": true, "blocked": true, "cancelled": true},
	"conflict":    {"in_progress": true, "ready": true, "cancelled": true, "blocked": true},
}

var ActiveOwner = map[string]bool{
	"claimed": true, "in_progress": true, "self_check": true,
	"in_review": true, "merging": true, "conflict": true,
}

func AssertTransition(src, dst string) error {
	if src == dst || src == "blocked" {
		return nil
	}
	if m := transitions[src]; m != nil && m[dst] {
		return nil
	}
	return errx.New("illegal status transition "+src+" -> "+dst, "run `maw board` to inspect task status")
}
