package schedule

import (
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

func TestPickNextReadyByPriorityAndScope(t *testing.T) {
	a := &db.Task{ID: "T-061", Status: "ready", Priority: 1, MergeOrder: 10, WriteScope: []string{"desktop-tauri/**"}}
	b := &db.Task{ID: "T-073", Status: "ready", Priority: 3, MergeOrder: 30, WriteScope: []string{"desktop-tauri/**"}}
	c := &db.Task{ID: "T-074", Status: "ready", Priority: 1, MergeOrder: 20, WriteScope: []string{"backend/internal/nbagent/**"}}

	// 同盘重叠：只应激活排序更前的 T-061；T-074 不同盘可并行
	if !CanActivate(a, []*db.Task{a, b, c}) {
		t.Fatal("T-061 should activate")
	}
	if CanActivate(b, []*db.Task{a, b, c}) {
		t.Fatal("T-073 must wait for T-061 (same write_scope, worse priority)")
	}
	if !CanActivate(c, []*db.Task{a, b, c}) {
		t.Fatal("T-074 different scope should activate")
	}

	got := PickNextReady([]*db.Task{b, c, a})
	if got == nil || got.ID != "T-061" {
		t.Fatalf("want T-061 first, got %#v", got)
	}

	// T-061 进行中时，T-074 仍可领；T-073 仍不可
	a.Status = "in_progress"
	if CanActivate(b, []*db.Task{a, b, c}) {
		t.Fatal("T-073 blocked while T-061 active")
	}
	if !CanActivate(c, []*db.Task{a, b, c}) {
		t.Fatal("T-074 still ok")
	}
	got = PickNextReady([]*db.Task{a, b, c})
	if got == nil || got.ID != "T-074" {
		t.Fatalf("want T-074, got %#v", got)
	}

	// T-061 done 后 T-073 可激活
	a.Status = "done"
	if !CanActivate(b, []*db.Task{a, b, c}) {
		t.Fatal("T-073 should activate after T-061 done")
	}
}

func TestDepsGate(t *testing.T) {
	a := &db.Task{ID: "T-074", Status: "ready", Priority: 1, WriteScope: []string{"nbagent/**"}}
	b := &db.Task{ID: "T-080", Status: "backlog", Priority: 2, Deps: []string{"T-074"}, WriteScope: []string{"bugflow/**"}}
	if CanActivate(b, []*db.Task{a, b}) {
		t.Fatal("deps not done")
	}
	a.Status = "done"
	if !CanActivate(b, []*db.Task{a, b}) {
		t.Fatal("deps done should allow")
	}
}

func TestSerializedByDeps(t *testing.T) {
	g := map[string][]string{
		"T-061": nil,
		"T-062": {"T-061"},
		"T-073": {"T-062"},
		"T-074": nil,
	}
	if !SerializedByDeps(g, "T-061", "T-073") {
		t.Fatal("expected serialized")
	}
	if SerializedByDeps(g, "T-061", "T-074") {
		t.Fatal("independent")
	}
}

func TestPickNextReadyForAffinity(t *testing.T) {
	// w1 刚做完 nbagent；ready 里有更优先的无关题 + 同模块续题 + 依赖续链
	done := &db.Task{
		ID: "T-074", Status: "done", Owner: "w1", Priority: 1, MergeOrder: 10,
		WriteScope: []string{"backend/internal/nbagent/**"}, Heartbeat: "2026-01-02T00:00:00Z",
	}
	unrelated := &db.Task{
		ID: "T-010", Status: "ready", Priority: 1, MergeOrder: 5,
		WriteScope: []string{"frontend/src/views/**"},
	}
	sameModule := &db.Task{
		ID: "T-075", Status: "ready", Priority: 5, MergeOrder: 50,
		WriteScope: []string{"backend/internal/nbagent/service/**"},
	}
	depNext := &db.Task{
		ID: "T-080", Status: "ready", Priority: 8, MergeOrder: 80,
		Deps: []string{"T-074"}, WriteScope: []string{"backend/internal/bugflow/**"},
	}

	all := []*db.Task{done, unrelated, sameModule, depNext}

	// 无 agent：仍按全局序位 → T-010
	got := PickNextReady(all)
	if got == nil || got.ID != "T-010" {
		t.Fatalf("no-agent want T-010, got %#v", got)
	}

	// w1：依赖续链分最高 → T-080
	got = PickNextReadyFor(all, "w1")
	if got == nil || got.ID != "T-080" {
		t.Fatalf("w1 affinity want T-080 (deps), got %#v", got)
	}

	// 去掉续链后：同模块亲和优先于更优先的前端题
	depNext.Status = "backlog"
	got = PickNextReadyFor(all, "w1")
	if got == nil || got.ID != "T-075" {
		t.Fatalf("w1 affinity want T-075 (same module), got %#v", got)
	}

	// 陌生 agent：无锚点，回退全局序位
	got = PickNextReadyFor(all, "w9")
	if got == nil || got.ID != "T-010" {
		t.Fatalf("w9 want T-010, got %#v", got)
	}
}

func TestAffinityScore(t *testing.T) {
	hint := &db.Task{ID: "T-001", WriteScope: []string{"backend/internal/foo/**"}}
	dep := &db.Task{ID: "T-002", Deps: []string{"T-001"}, WriteScope: []string{"backend/internal/bar/**"}}
	overlap := &db.Task{ID: "T-003", WriteScope: []string{"backend/internal/foo/x.go"}}
	far := &db.Task{ID: "T-004", WriteScope: []string{"docs/**"}}

	if AffinityScore(dep, hint) < AffinityScore(overlap, hint) {
		t.Fatal("deps continuation should beat mere overlap/module")
	}
	if AffinityScore(overlap, hint) <= AffinityScore(far, hint) {
		t.Fatal("overlap should beat unrelated")
	}
	if AffinityScore(far, hint) != 0 {
		t.Fatal("docs vs foo should be zero affinity")
	}
}
