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

func TestPickNextReadyAffinityIsTieBreakOnly(t *testing.T) {
	// 亲和不再压过 priority：pri 更优的无关题应先于续链题。
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

	got := PickNextReadyFor(all, "w1")
	if got == nil || got.ID != "T-010" {
		t.Fatalf("priority must beat affinity, want T-010, got %#v", got)
	}

	// 同分（同 priority/merge_order）时亲和打破：续链赢
	sameModule.Priority, sameModule.MergeOrder = 1, 5
	depNext.Priority, depNext.MergeOrder = 1, 5
	unrelated.Status = "backlog"
	got = PickNextReadyFor(all, "w1")
	if got == nil || got.ID != "T-080" {
		t.Fatalf("tie-break affinity want T-080, got %#v", got)
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

func TestLaneStickyPick(t *testing.T) {
	import2 := &db.Task{
		ID: "N-2", Status: "done", Owner: "w1", Priority: 20, MergeOrder: 20, Lane: "IMPORT",
		WriteScope: []string{"backend/internal/requirement/importdraft/**"},
		Heartbeat:  "2026-01-02T00:00:00Z",
	}
	import4 := &db.Task{
		ID: "N-4", Status: "ready", Priority: 30, MergeOrder: 31, Lane: "IMPORT",
		Deps: []string{"N-2"}, WriteScope: []string{"backend/internal/requirement/importdraft/adapters/modao/**"},
	}
	bp8 := &db.Task{
		ID: "N-8", Status: "ready", Priority: 20, MergeOrder: 21, Lane: "BLUEPRINT",
		WriteScope: []string{"backend/internal/mawbridge/**"},
	}
	fig11 := &db.Task{
		ID: "N-11", Status: "ready", Priority: 20, MergeOrder: 22, Lane: "FIGMA",
		WriteScope: []string{"backend/internal/app.go"},
	}
	all := []*db.Task{import2, import4, bp8, fig11}

	// 粘在 IMPORT：只能领到 4，即使 8/11 priority 更好
	got := PickNextReadyInLane(all, "w1", "IMPORT", nil, true)
	if got == nil || got.ID != "N-4" {
		t.Fatalf("IMPORT lane want N-4, got %#v", got)
	}

	// 未绑定自动选队：队首最优是 BLUEPRINT 的 N-8（pri 20 mo 21）
	lane, head := SelectLane(all, nil)
	if lane != "BLUEPRINT" || head == nil || head.ID != "N-8" {
		t.Fatalf("SelectLane want BLUEPRINT/N-8, got %q %#v", lane, head)
	}

	// FIGMA 显式
	got = PickNextReadyInLane(all, "w1", "FIGMA", nil, true)
	if got == nil || got.ID != "N-11" {
		t.Fatalf("FIGMA want N-11, got %#v", got)
	}
}

func TestSelectLaneIgnoresEmptyLanes(t *testing.T) {
	onlyBlocked := &db.Task{
		ID: "A-2", Status: "ready", Priority: 1, Lane: "A",
		Deps: []string{"A-1"}, WriteScope: []string{"a/**"},
	}
	ok := &db.Task{
		ID: "B-1", Status: "ready", Priority: 50, Lane: "B",
		WriteScope: []string{"b/**"},
	}
	lane, head := SelectLane([]*db.Task{onlyBlocked, ok}, nil)
	if lane != "B" || head == nil || head.ID != "B-1" {
		t.Fatalf("want B/B-1, got %q %#v", lane, head)
	}
}
