package board

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

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
		`<script id="board-app">`,
		`const I18N`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	if strings.Contains(html, "{{MAW_") {
		t.Fatal("page still has unresolved asset slots")
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
	if !strings.Contains(body, "const I18N") || strings.Contains(body, "{{MAW_") {
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
	if !strings.Contains(html, "const I18N") || strings.Contains(html, "{{MAW_") {
		t.Fatal("static snapshot is not assembled")
	}
	for _, forbidden := range []string{`method: "POST"`, `method:"POST"`, `/api/status`} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("static snapshot exposes write action %q", forbidden)
		}
	}
}
