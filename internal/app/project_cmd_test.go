package app

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

func projectIDs(t *testing.T, gdb string) []string {
	t.Helper()
	var ids []string
	if err := withGlobalRead(gdb, func(tx *sql.Tx) error {
		projects, err := db.ListProjects(tx)
		if err != nil {
			return err
		}
		for _, p := range projects {
			ids = append(ids, p.ID)
		}
		return nil
	}); err != nil {
		t.Fatalf("list projects: %v", err)
	}
	return ids
}

func hasID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// countRemovalEvents 数审计事件：移除必须留痕，且不能挂在被删工程名下。
func countRemovalEvents(t *testing.T, gdb string) int {
	t.Helper()
	var n int
	if err := withGlobalRead(gdb, func(tx *sql.Tx) error {
		return tx.QueryRow(
			`SELECT COUNT(*) FROM events WHERE type = 'project_removed' AND project_id IS NULL`,
		).Scan(&n)
	}); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func TestRemoveProjectClearsIdleProject(t *testing.T) {
	f := newFixture(t)

	res, err := RemoveProject(f.repo, f.pc.ID(), ProjectRemovalOptions{})
	if err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("结果应 ok：%+v", res)
	}
	if ids := projectIDs(t, f.pc.GlobalDB); hasID(ids, f.pc.ID()) {
		t.Fatalf("工程应已移除，仍在：%v", ids)
	}
	if n := countRemovalEvents(t, f.pc.GlobalDB); n != 1 {
		t.Fatalf("应留下 1 条 project_removed 审计事件，得到 %d", n)
	}
	if s, _ := res["summary"].(string); s == "" {
		t.Fatalf("结果应带一行人摘要：%+v", res)
	}
}

func TestRemoveProjectRefusesLiveWork(t *testing.T) {
	f := newFixture(t)
	f.seedTask("T-1", "in_progress", "cursor-demo-1", []string{"internal/**"})

	_, err := RemoveProject(f.repo, f.pc.ID(), ProjectRemovalOptions{})
	if err == nil {
		t.Fatal("有 in_progress 任务时必须拒绝移除")
	}
	if !errors.Is(err, db.ErrProjectBusy) {
		t.Fatalf("拒绝原因应是 ErrProjectBusy，得到 %v", err)
	}
	if ids := projectIDs(t, f.pc.GlobalDB); !hasID(ids, f.pc.ID()) {
		t.Fatalf("被拒绝后工程必须还在：%v", ids)
	}
}

func TestRemoveProjectForceOverridesLiveWork(t *testing.T) {
	f := newFixture(t)
	f.seedTask("T-1", "in_progress", "cursor-demo-1", []string{"internal/**"})

	if _, err := RemoveProject(f.repo, f.pc.ID(), ProjectRemovalOptions{Force: true}); err != nil {
		t.Fatalf("--force 应允许移除：%v", err)
	}
	if ids := projectIDs(t, f.pc.GlobalDB); hasID(ids, f.pc.ID()) {
		t.Fatalf("工程应已移除：%v", ids)
	}
}

func TestRemoveProjectDryRunWritesNothing(t *testing.T) {
	f := newFixture(t)
	f.seedTask("T-1", "done", "", []string{"internal/**"})
	f.seedTask("T-2", "in_progress", "cursor-demo-1", []string{"docs/**"})

	res, err := RemoveProject(f.repo, f.pc.ID(), ProjectRemovalOptions{DryRun: true})
	if err != nil {
		t.Fatalf("预览不该被现场拦住：%v", err)
	}
	if dry, _ := res["dry_run"].(bool); !dry {
		t.Fatalf("应标记为预览：%+v", res)
	}
	if blocked, _ := res["blocked"].(bool); !blocked {
		t.Fatalf("预览应说明会被现场拦住：%+v", res)
	}
	if ids := projectIDs(t, f.pc.GlobalDB); !hasID(ids, f.pc.ID()) {
		t.Fatalf("预览不得写库：%v", ids)
	}
	if n := countRemovalEvents(t, f.pc.GlobalDB); n != 0 {
		t.Fatalf("预览不得写审计事件，得到 %d", n)
	}
}

func TestRemoveProjectResolvesByName(t *testing.T) {
	f := newFixture(t)
	name := f.pc.Project.Name
	if name == "" {
		t.Fatal("fixture 工程应有名称")
	}

	if _, err := RemoveProject(f.repo, name, ProjectRemovalOptions{}); err != nil {
		t.Fatalf("按名称移除应可用：%v", err)
	}
	if ids := projectIDs(t, f.pc.GlobalDB); hasID(ids, f.pc.ID()) {
		t.Fatalf("工程应已移除：%v", ids)
	}
}

func TestRemoveProjectUnknownRef(t *testing.T) {
	f := newFixture(t)

	_, err := RemoveProject(f.repo, "no-such-project", ProjectRemovalOptions{})
	if err == nil {
		t.Fatal("未登记的工程必须报错")
	}
	// 报错要能引导到「看有哪些工程」，否则用户只能猜。
	if !strings.Contains(err.Error(), "project list") {
		t.Fatalf("错误应提示 maw project list：%v", err)
	}
}

// 预览与真删必须报同一套字段：以后新增一张关联表只改了删除没改预览，
// 用户就会按少报的规模做决定。
func TestRemoveProjectDryRunCountsMatchRealRemoval(t *testing.T) {
	f := newFixture(t)
	f.seedTask("T-1", "done", "", []string{"internal/**"})
	f.seedTask("T-2", "cancelled", "", []string{"docs/**"})

	preview, err := RemoveProject(f.repo, f.pc.ID(), ProjectRemovalOptions{DryRun: true})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	would, ok := preview["would_remove"].(map[string]int64)
	if !ok {
		t.Fatalf("预览应回 would_remove 计数：%+v", preview)
	}

	done, err := RemoveProject(f.repo, f.pc.ID(), ProjectRemovalOptions{})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	removed, ok := done["removed"].(map[string]int64)
	if !ok {
		t.Fatalf("结果应回 removed 计数：%+v", done)
	}

	if len(would) != len(removed) {
		t.Fatalf("预览与真删的字段数不同：%d vs %d", len(would), len(removed))
	}
	for key, want := range removed {
		if got := would[key]; got != want {
			t.Fatalf("%s 预览 %d ≠ 实删 %d", key, got, want)
		}
	}
}
