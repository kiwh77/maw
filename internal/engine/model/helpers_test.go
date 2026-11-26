package model_test

import (
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

const testProject = model.ProjectID("716c30878ba1b95f")

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad fixture time %q: %v", s, err)
	}
	return v
}

func ptr[T any](v T) *T { return &v }

// codes 把违反列表压成「码集合」，便于断言「包含某码」而不受条数与顺序影响。
func codes(vs []*model.Error) map[model.Code]bool {
	out := make(map[model.Code]bool, len(vs))
	for _, v := range vs {
		out[v.Code] = true
	}
	return out
}

func assertClean(t *testing.T, what string, vs []*model.Error) {
	t.Helper()
	if len(vs) != 0 {
		for _, v := range vs {
			t.Errorf("%s: 期望无违反，得到 %v", what, v)
		}
	}
}

func assertHas(t *testing.T, what string, vs []*model.Error, want model.Code) {
	t.Helper()
	if !model.HasCode(vs, want) {
		t.Errorf("%s: 期望触发 %s，实际 %v", what, want, vs)
	}
}

func assertNotHas(t *testing.T, what string, vs []*model.Error, unwanted model.Code) {
	t.Helper()
	if model.HasCode(vs, unwanted) {
		t.Errorf("%s: 不应触发 %s，实际 %v", what, unwanted, vs)
	}
}
