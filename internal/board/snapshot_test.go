package board

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

// apiBoardBody 取 /api/board 的响应体，供空值契约断言共用。
func apiBoardBody(t *testing.T, load LoadFunc, query string) string {
	t.Helper()
	h := newHandler(load, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/board"+query, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

func assertEmptyArrays(t *testing.T, body string) {
	t.Helper()
	for _, want := range []string{`"tasks":[]`, `"projects":[]`, `"risks":[]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in body=%s", want, body)
		}
	}
	for _, unwanted := range []string{`"tasks":null`, `"projects":null`, `"risks":null`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("found %s in body=%s", unwanted, body)
		}
	}
}

func TestBuildSnapshotEmptyLibraryCollectionsAreArrays(t *testing.T) {
	s := BuildSnapshot(nil, nil, 0)
	if s.Tasks == nil || s.Projects == nil || s.Risks == nil {
		t.Fatalf("nil collections: tasks=%v projects=%v risks=%v", s.Tasks, s.Projects, s.Risks)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	assertEmptyArrays(t, string(raw))
}

func TestAPIBoardEmptyLibraryUsesEmptyArrays(t *testing.T) {
	// 空库：装配层拿到的工程/任务/风险都是空的，响应必须是 [] 而不是 null。
	load := func(_, _, _ string) (Snapshot, error) { return BuildSnapshot(nil, nil, 4), nil }
	assertEmptyArrays(t, apiBoardBody(t, load, ""))
}

func TestAPIBoardUnknownProjectUsesEmptyArrays(t *testing.T) {
	// 未知工程：装配层会把工程列表覆盖成 nil（查询无命中），归一化后仍是 []。
	load := func(project, _, _ string) (Snapshot, error) {
		s := BuildSnapshot(nil, nil, 4)
		s.Projects = nil
		s.Tasks = nil
		s.Agents = nil
		s.Risks = nil
		s.Normalize()
		s.FilterProject = project
		return s, nil
	}
	assertEmptyArrays(t, apiBoardBody(t, load, "?project=unknown-project"))
}

func TestAPIBoardNoRisksStillEmptyArray(t *testing.T) {
	// 有任务但无阻塞/冲突：risks 为空数组，不是 null。
	load := func(_, _, _ string) (Snapshot, error) {
		return BuildSnapshot([]*db.Task{{ID: "T-1", Status: "ready"}}, nil, 2), nil
	}
	body := apiBoardBody(t, load, "")
	if !strings.Contains(body, `"risks":[]`) || strings.Contains(body, `"risks":null`) {
		t.Fatalf("body=%s", body)
	}
	if strings.Contains(body, `"tasks":[]`) {
		t.Fatalf("risks/任务断言串味: body=%s", body)
	}
}

func TestSnapshotUpdatedAtIsDataWatermark(t *testing.T) {
	beat := time.Now().Add(-90 * time.Minute).UTC().Truncate(time.Second)
	tasks := []*db.Task{
		{ID: "T-1", Status: "ready", Heartbeat: beat.Format(time.RFC3339)},
		{ID: "T-2", Status: "ready", Heartbeat: beat.Add(-2 * time.Hour).Format(time.RFC3339)},
	}
	agents := []*db.Agent{{ID: "a-1", Status: "working", LastSeen: beat.Add(-time.Hour).Format(time.RFC3339)}}
	s := BuildSnapshot(tasks, agents, 2)
	want := beat.Local().Format(SnapshotTimeLayout)
	if s.UpdatedAt != want {
		t.Fatalf("updated_at=%q want %q (不应是渲染时刻)", s.UpdatedAt, want)
	}
	if s.GeneratedAt == "" {
		t.Fatal("generated_at 缺失")
	}
	if s.GeneratedAt == s.UpdatedAt {
		t.Fatalf("generated_at 必须独立于 updated_at: %q", s.GeneratedAt)
	}
	// 同一份数据重复构建：updated_at 不变，generated_at 允许推进。
	again := BuildSnapshot(tasks, agents, 2)
	if again.UpdatedAt != want {
		t.Fatalf("updated_at 随渲染漂移: %q", again.UpdatedAt)
	}
}

func TestSnapshotUpdatedAtEmptyWithoutData(t *testing.T) {
	s := BuildSnapshot(nil, nil, 2)
	if s.UpdatedAt != "" {
		t.Fatalf("空库不应伪造 updated_at: %q", s.UpdatedAt)
	}
	if s.GeneratedAt == "" {
		t.Fatal("generated_at 缺失")
	}
}

func TestMarkDataWatermarkOverridesGuess(t *testing.T) {
	s := BuildSnapshot([]*db.Task{{ID: "T-1", Status: "ready", Heartbeat: "2026-01-01T00:00:00Z"}}, nil, 2)
	s.MarkDataWatermark("2026-02-03T04:05:06Z")
	if want := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC).Local().Format(SnapshotTimeLayout); s.UpdatedAt != want {
		t.Fatalf("updated_at=%q want %q", s.UpdatedAt, want)
	}
	s.MarkDataWatermark("")
	if s.UpdatedAt != "" {
		t.Fatalf("空水位应为空: %q", s.UpdatedAt)
	}
}

func TestAPIBoardIncludesGeneratedAt(t *testing.T) {
	load := func(_, _, _ string) (Snapshot, error) {
		return BuildSnapshot([]*db.Task{{ID: "T-1", Status: "ready"}}, nil, 2), nil
	}
	body := apiBoardBody(t, load, "")
	if !strings.Contains(body, `"generated_at":`) {
		t.Fatalf("/api/board 手写 map 未透出 generated_at: body=%s", body)
	}
}

func TestBuildSnapshotCountsAndHints(t *testing.T) {
	tasks := []*db.Task{
		{ID: "T-1", Status: "done", Title: "a"},
		{ID: "T-2", Status: "ready", Title: "b"},
		{ID: "T-3", Status: "blocked", Title: "c", BlockedReason: "wait", ProjectName: "demo"},
		{ID: "T-4", Status: "in_progress", Title: "d"},
	}
	agents := []*db.Agent{{ID: "cursor-demo-1", Status: "working"}}
	s := BuildSnapshot(tasks, agents, 4)
	if s.Total != 4 || s.Done != 1 || s.Active != 1 || s.Blocked != 1 {
		t.Fatalf("counts total=%d done=%d active=%d blocked=%d", s.Total, s.Done, s.Active, s.Blocked)
	}
	if s.Percent != 25 {
		t.Fatalf("percent=%d", s.Percent)
	}
	if !strings.Contains(s.NextHint, "可领取") {
		t.Fatalf("hint=%s", s.NextHint)
	}
	if len(s.Risks) != 1 || !strings.Contains(s.Risks[0], "demo/T-3") {
		t.Fatalf("risks=%v", s.Risks)
	}
	if len(s.Agents) != 1 {
		t.Fatal("agents")
	}
}

func TestBuildSnapshotNilAgentsAndDone(t *testing.T) {
	s := BuildSnapshot([]*db.Task{{ID: "T-1", Status: "done"}}, nil, 2)
	if s.Agents == nil || len(s.Agents) != 0 {
		t.Fatalf("agents=%v", s.Agents)
	}
	if !strings.Contains(s.NextHint, "全部完成") {
		t.Fatalf("hint=%s", s.NextHint)
	}
}

func TestWriteSnapshotFile(t *testing.T) {
	dir := t.TempDir()
	s := BuildSnapshot([]*db.Task{{ID: "T-1", Status: "ready"}}, nil, 2)
	htmlPath, err := Write(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(htmlPath) != "index.html" {
		t.Fatalf("html=%s", htmlPath)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "board", "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Snapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestWriteSnapshotCopiesWebAssets(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, BuildSnapshot([]*db.Task{{ID: "T-1", Status: "ready"}}, nil, 2)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "board", "assets", "board-web.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "var MawBoard=") {
		t.Fatal("static export is missing the built bundle")
	}
	html, err := os.ReadFile(filepath.Join(dir, "board", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `src="assets/board-web.js"`) {
		t.Fatal("static export does not reference the copied bundle relatively")
	}
}

func TestAPIBoardHandler(t *testing.T) {
	load := func(_, _, _ string) (Snapshot, error) {
		return BuildSnapshot([]*db.Task{{ID: "T-1", Status: "ready"}}, nil, 2), nil
	}
	detail := func(_, _ string) (TaskDetail, error) {
		return TaskDetail{Task: &db.Task{ID: "T-1", Status: "ready"}}, nil
	}
	h := newHandler(load, detail)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/board", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"total":1`) {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestPageHTMLAssemblesPublishedAssets(t *testing.T) {
	html := PageHTML()
	for _, want := range []string{
		`<script id="theme-init">`,
		`const THEMES`,
		`<style>`,
		`--bg: #141618`,
		`id="app"`,
		`<script id="board-web" src="assets/board-web.js" defer></script>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	if strings.Contains(html, "{{MAW_") {
		t.Fatal("page still has unresolved asset slots")
	}
	if strings.Contains(html, `id="board-app"`) {
		t.Fatal("legacy board-app script should not be embedded")
	}
}

func TestBoardAssetsExposeBuiltBundle(t *testing.T) {
	assets, err := BoardAssets()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := fs.ReadFile(assets, "board-web.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "var MawBoard=") {
		t.Fatal("board-web.js is not the IIFE bundle")
	}
}

func TestAssetsRouteServesBundleWithoutListing(t *testing.T) {
	h := newHandler(nil, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/assets/board-web.js", nil))
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "var MawBoard=") {
		t.Fatal("served asset is not the built bundle")
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control=%q", got)
	}
	for _, p := range []string{"/assets/", "/assets/missing.js"} {
		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", p, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s code=%d", p, rr.Code)
		}
	}
}

func TestRootPageServesAssembledAssets(t *testing.T) {
	h := newHandler(nil, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 200 {
		t.Fatalf("code=%d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `id="app"`) || !strings.Contains(body, "board-web.js") || strings.Contains(body, "{{MAW_") {
		t.Fatalf("served page is not assembled")
	}
}

func TestStaticSnapshotLoadsWithoutWriteActions(t *testing.T) {
	dir := t.TempDir()
	htmlPath, err := Write(dir, BuildSnapshot([]*db.Task{{ID: "T-1", Status: "ready"}}, nil, 2))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	if !strings.Contains(html, `id="app"`) || !strings.Contains(html, "board-web.js") || strings.Contains(html, "{{MAW_") {
		t.Fatal("static snapshot is not assembled")
	}
	for _, forbidden := range []string{`method: "POST"`, `method:"POST"`, `/api/status`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("static snapshot exposes write action %q", forbidden)
		}
	}
}

// TestBuiltBundleCarriesAutoExecToggle：工作台的「自动执行」开关必须真的在
// 打进产物的 bundle 里，而不是只存在于源码。以前踩过的坑是源码改了、
// dist 忘了重建，界面看起来「没这个按钮」。
func TestBuiltBundleCarriesAutoExecToggle(t *testing.T) {
	assets, err := BoardAssets()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := fs.ReadFile(assets, "board-web.js")
	if err != nil {
		t.Fatal(err)
	}
	bundle := string(raw)
	for _, want := range []string{
		"f-auto-exec",     // 按钮 id（工具栏占位与 e2e 选择器都认它）
		"/auto-execution", // 服务端读写路径
		"自动执行",            // 中文标签
	} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("board-web.js 缺少 %q：源码改了但产物没重建？", want)
		}
	}
}
