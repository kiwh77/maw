package board

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

func archiveFixture() []*db.Task {
	return []*db.Task{
		{ID: "T-active", Status: "ready"},
		{ID: "T-done", Status: "done"},
		{
			ID: "T-arch", Status: "done",
			Extra: map[string]any{
				db.TaskArchivedKey:   true,
				db.TaskArchivedAtKey: "2026-11-04T00:00:00Z",
				db.TaskArchivedByKey: "lead",
			},
		},
	}
}

// 默认视图只留活动态，且聚合计数仍描述全量数据、原快照不被改动。
func TestArchiveDefaultViewHidesDoneAndArchived(t *testing.T) {
	snap := BuildSnapshot(archiveFixture(), nil, 1)
	if len(snap.Tasks) != 3 {
		t.Fatalf("全量快照应有 3 条，实际 %d", len(snap.Tasks))
	}
	view := snap.WithTaskView(TaskView{})
	if len(view.Tasks) != 1 || view.Tasks[0].ID != "T-active" {
		t.Fatalf("默认视图应只剩活动题，实际 %+v", view.Tasks)
	}
	if view.Total != snap.Total || view.Done != snap.Done {
		t.Fatalf("隐藏列表不应改动全量聚合：view=%d/%d snap=%d/%d",
			view.Done, view.Total, snap.Done, snap.Total)
	}
	if len(snap.Tasks) != 3 {
		t.Fatal("WithTaskView 不得修改原快照")
	}
}

// 活动题依赖的终态上游必须留在列表里，chips／拓扑才能解析标题与状态。
func TestActiveViewKeepsDirectUpstreamDeps(t *testing.T) {
	tasks := []*db.Task{
		{ProjectID: "p1", ID: "T-done", Title: "上游已完成", Status: "done"},
		{ProjectID: "p1", ID: "T-arch", Title: "上游已归档", Status: "done",
			Extra: map[string]any{db.TaskArchivedKey: true}},
		{ProjectID: "p1", ID: "T-orphan-done", Title: "无人引用的完成题", Status: "done"},
		{ProjectID: "p1", ID: "T-active", Title: "活动题", Status: "ready",
			Deps: []string{"T-done", "T-arch"}},
		{ProjectID: "p2", ID: "T-done", Title: "别的工程同号", Status: "done"},
	}
	snap := BuildSnapshot(tasks, nil, 1)
	view := snap.WithTaskView(TaskView{})
	got := map[string]string{}
	for _, tsk := range view.Tasks {
		got[tsk.ProjectID+"/"+tsk.ID] = tsk.Title
	}
	if got["p1/T-active"] != "活动题" {
		t.Fatalf("活动题应保留：%+v", got)
	}
	if got["p1/T-done"] != "上游已完成" {
		t.Fatalf("直接依赖的 done 上游应保留标题：%+v", got)
	}
	if got["p1/T-arch"] != "上游已归档" {
		t.Fatalf("直接依赖的 archived 上游应保留标题：%+v", got)
	}
	if _, ok := got["p1/T-orphan-done"]; ok {
		t.Fatalf("无人引用的完成题不应回流：%+v", got)
	}
	if _, ok := got["p2/T-done"]; ok {
		t.Fatalf("不得跨工程补同号上游：%+v", got)
	}
	if view.Total != snap.Total || view.Done != snap.Done {
		t.Fatalf("补上游不得改动全量聚合")
	}
}

// 显式 include 才能看到被隐藏的题；未知值忽略而不是猜。
func TestArchiveIncludeShowsHidden(t *testing.T) {
	snap := BuildSnapshot(archiveFixture(), nil, 1)
	for _, q := range []string{"done,archived", "all"} {
		if got := len(snap.WithTaskView(ParseTaskView(q)).Tasks); got != 3 {
			t.Fatalf("include=%q 应可见 3 条，实际 %d", q, got)
		}
	}
	if v := ParseTaskView("done"); !v.IncludeDone || v.IncludeArchived {
		t.Fatalf("include=done 只应放行 done: %+v", v)
	}
	if v := ParseTaskView("bogus"); v.IncludeDone || v.IncludeArchived {
		t.Fatalf("未知 include 不应放行任何东西: %+v", v)
	}
	if got := len(snap.WithTaskView(ParseTaskView("done")).Tasks); got != 2 {
		t.Fatalf("include=done 应可见 active+done 共 2 条，实际 %d", got)
	}
}

// /api/board 的 include 参数：生产入口默认隐藏，显式取回；
// 旧 compat wrapper 不启用默认视图，但显式 include 仍生效（不破坏既有调用方）。
func TestArchiveAPIBoardIncludeQuery(t *testing.T) {
	load := func(_, _, _ string) (Snapshot, error) {
		return BuildSnapshot(archiveFixture(), nil, 1), nil
	}
	h := newHandlerOpts(ServeOpts{Load: load, DefaultActiveView: true})
	cases := []struct {
		query string
		want  int
	}{
		{"", 1},
		{"?include=done", 2},
		{"?include=archived", 2},
		{"?include=all", 3},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/board"+tc.query, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: HTTP %d", tc.query, w.Code)
		}
		var body struct {
			Tasks           []map[string]any `json:"tasks"`
			IncludeDone     bool             `json:"include_done"`
			IncludeArchived bool             `json:"include_archived"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Tasks) != tc.want {
			t.Fatalf("%s: tasks=%d，期望 %d", tc.query, len(body.Tasks), tc.want)
		}
		if tc.query == "?include=all" && (!body.IncludeDone || !body.IncludeArchived) {
			t.Fatal("include=all 应在响应里回显两个开关")
		}
	}
}

// 旧只读兼容包装不改默认响应形状，但显式 include 仍按新规则过滤。
func TestArchiveCompatWrapperKeepsLegacyDefault(t *testing.T) {
	load := func(_, _, _ string) (Snapshot, error) {
		return BuildSnapshot(archiveFixture(), nil, 1), nil
	}
	h := newHandler(load, nil)
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 3},
		{"?include=done", 2},
		{"?include=archived", 2},
		{"?include=all", 3},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/board"+tc.query, nil))
		var body struct {
			Tasks []map[string]any `json:"tasks"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Tasks) != tc.want {
			t.Fatalf("%s: tasks=%d，期望 %d", tc.query, len(body.Tasks), tc.want)
		}
	}
}

// archive 后任务行、状态与审计事件仍可查（不删历史）。
func TestArchiveAuditStillReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	if err := db.Init(path); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.WithTx(path, func(tx *sql.Tx) error {
		if err := db.UpsertTask(tx, &db.Task{
			ProjectID: "p1", ID: "T-1", Title: "完成题", Status: "done",
		}); err != nil {
			return err
		}
		return db.ArchiveTask(tx, "p1", "T-1", "lead", now)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(path, func(tx *sql.Tx) error {
		got, err := db.GetTask(tx, "p1", "T-1")
		if err != nil {
			return err
		}
		if got == nil {
			t.Fatal("归档后任务行不应消失")
		}
		if got.Status != "done" {
			t.Fatalf("归档不得改变业务状态: %s", got.Status)
		}
		if !db.IsArchived(got) {
			t.Fatal("归档标记未持久化")
		}
		at, by := db.ArchivedInfo(got)
		if at == "" || by != "lead" {
			t.Fatalf("审计信息不可读: at=%q by=%q", at, by)
		}
		events, err := db.ListEventsByTask(tx, "p1", "T-1", 20)
		if err != nil {
			return err
		}
		found := false
		for _, e := range events {
			if e != nil && e.Type == "task_archived" {
				found = true
			}
		}
		if !found {
			t.Fatal("归档审计事件不可查")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 幂等：重复归档不应再写一条审计事件。
	if err := db.WithTx(path, func(tx *sql.Tx) error {
		return db.ArchiveTask(tx, "p1", "T-1", "lead", now.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(path, func(tx *sql.Tx) error {
		events, _ := db.ListEventsByTask(tx, "p1", "T-1", 20)
		n := 0
		for _, e := range events {
			if e != nil && e.Type == "task_archived" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("重复归档应幂等，审计事件数=%d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
