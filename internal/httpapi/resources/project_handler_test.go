package resources_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/httpapi/resources"
	"github.com/kiwh77/maw/internal/localauth"
)

// fakeProjects 记录端口收到的请求，并按预设返回结果／错误。
type fakeProjects struct {
	calls  int
	last   resources.ProjectRemovalRequest
	result resources.ProjectRemovalResult
	err    error

	updateCalls  int
	lastUpdate   resources.ProjectUpdateRequest
	updateResult resources.ProjectUpdateResult
	updateErr    error
}

func (f *fakeProjects) Remove(_ context.Context, req resources.ProjectRemovalRequest) (resources.ProjectRemovalResult, error) {
	f.calls++
	f.last = req
	if f.err != nil {
		return resources.ProjectRemovalResult{}, f.err
	}
	return f.result, nil
}

func (f *fakeProjects) Update(_ context.Context, req resources.ProjectUpdateRequest) (resources.ProjectUpdateResult, error) {
	f.updateCalls++
	f.lastUpdate = req
	if f.updateErr != nil {
		return resources.ProjectUpdateResult{}, f.updateErr
	}
	return f.updateResult, nil
}

func newProjectAPI(t *testing.T, projects resources.Projects) (*resources.API, string) {
	t.Helper()
	tok, err := localauth.OpenTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := tok.Mint("board-operator", localauth.ScopeWrite)
	if err != nil {
		t.Fatal(err)
	}
	return &resources.API{Projects: projects, Auth: &localauth.Authenticator{Tokens: tok}}, plain
}

func TestRemoveProjectReturnsRemovalCounts(t *testing.T) {
	fake := &fakeProjects{result: resources.ProjectRemovalResult{
		ProjectID: projectID, ProjectName: "demo", ProjectPath: "/tmp/demo",
		Summary:  "移除 demo：任务 2",
		Removed:  map[string]int64{"tasks": 2, "projects": 1},
		Retained: map[string]int64{"events": 7},
	}}
	api, token := newProjectAPI(t, fake)

	rec := doJSON(t, api, token, http.MethodDelete, "/api/v1/projects/"+projectID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("body=%v", body)
	}
	removed, _ := body["removed"].(map[string]any)
	if removed["tasks"] != float64(2) {
		t.Fatalf("界面要能读到删了多少行：%v", body["removed"])
	}
	retained, _ := body["retained"].(map[string]any)
	if retained["events"] != float64(7) {
		t.Fatalf("保留台账也要回给界面：%v", body["retained"])
	}
	if fake.last.ProjectID != projectID || fake.last.Force {
		t.Fatalf("默认不该 force：%+v", fake.last)
	}
}

func TestRemoveProjectForceFlagReachesUseCase(t *testing.T) {
	fake := &fakeProjects{result: resources.ProjectRemovalResult{ProjectID: projectID}}
	api, token := newProjectAPI(t, fake)

	rec := doJSON(t, api, token, http.MethodDelete,
		"/api/v1/projects/"+projectID+"?force=1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !fake.last.Force {
		t.Fatal("force=1 应原样传给用例，由用例决定怎么处理现场")
	}
}

func TestRemoveProjectBusyIsConflict(t *testing.T) {
	fake := &fakeProjects{err: resources.ErrConflict{
		Code:    "project_busy",
		Message: "工程仍有未完成的任务或在线 Agent，移除被拒绝",
		Details: map[string]any{"tasks": []map[string]any{{"id": "T-1", "status": "in_progress"}}},
	}}
	api, token := newProjectAPI(t, fake)

	rec := doJSON(t, api, token, http.MethodDelete, "/api/v1/projects/"+projectID, nil, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("期望 409，得到 %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "project_busy") {
		t.Fatalf("应带 project_busy 码：%s", rec.Body.String())
	}
}

func TestRemoveProjectNotFound(t *testing.T) {
	fake := &fakeProjects{err: resources.ErrNotFound{Message: "工程未登记"}}
	api, token := newProjectAPI(t, fake)

	rec := doJSON(t, api, token, http.MethodDelete, "/api/v1/projects/"+projectID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("期望 404，得到 %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoveProjectRejectsMalformedID(t *testing.T) {
	fake := &fakeProjects{}
	api, token := newProjectAPI(t, fake)

	rec := doJSON(t, api, token, http.MethodDelete, "/api/v1/projects/not-a-project-id", nil, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("期望 422，得到 %d %s", rec.Code, rec.Body.String())
	}
	if fake.calls != 0 {
		t.Fatal("形态非法的 id 不该进用例")
	}
}

func TestRemoveProjectWithoutUseCaseIs501(t *testing.T) {
	api, token := newProjectAPI(t, nil)

	rec := doJSON(t, api, token, http.MethodDelete, "/api/v1/projects/"+projectID, nil, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("期望 501（本中枢未接线该写入口），得到 %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unsupported") {
		t.Fatalf("未接线要回 unsupported 错误码：%s", rec.Body.String())
	}
}

func TestUpdateProjectReturnsEffectiveFields(t *testing.T) {
	fake := &fakeProjects{updateResult: resources.ProjectUpdateResult{
		ProjectID: projectID, ProjectName: "renamed", ProjectPath: "/tmp/demo2",
		MainBranch: "main", MaxParallel: 3,
		ConfigPath: "/tmp/demo2/.multiagent/config.toml", ConfigWriten: true,
	}}
	api, token := newProjectAPI(t, fake)

	body := map[string]any{
		"name": "renamed", "path": "/tmp/demo2", "main_branch": "main",
		"max_parallel": 3, "auto_enabled": true, "limit": 2,
		"allowed_nodes": []string{"node-a", "node-b"},
		"worker_node":   "node-a", "llm_node": "node-b",
	}
	rec := doJSON(t, api, token, http.MethodPatch, "/api/v1/projects/"+projectID, body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH 应回 200，得到 %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["project_name"] != "renamed" || out["config_written"] != true {
		t.Fatalf("字段未生效：%v", out)
	}
	if out["max_parallel"] != float64(3) {
		t.Fatalf("max_parallel 要回数值：%v", out["max_parallel"])
	}

	got := fake.lastUpdate
	if got.ProjectID != projectID {
		t.Fatalf("project_id=%q", got.ProjectID)
	}
	if got.Name == nil || *got.Name != "renamed" || got.Path == nil || *got.Path != "/tmp/demo2" {
		t.Fatalf("名称/路径没传到用例：%+v", got)
	}
	if got.AutoEnabled == nil || !*got.AutoEnabled || got.Limit == nil || *got.Limit != 2 {
		t.Fatalf("策略字段没传到用例：%+v", got)
	}
	if got.AllowedNodes == nil || len(*got.AllowedNodes) != 2 {
		t.Fatalf("allowed_nodes 没传到用例：%+v", got.AllowedNodes)
	}
}

func TestUpdateProjectRejectsMalformedID(t *testing.T) {
	fake := &fakeProjects{}
	api, token := newProjectAPI(t, fake)

	rec := doJSON(t, api, token, http.MethodPatch,
		"/api/v1/projects/not-a-project-id", map[string]any{"name": "x"}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("期望 422，得到 %d %s", rec.Code, rec.Body.String())
	}
	if fake.updateCalls != 0 {
		t.Fatal("形态非法的 id 不该进用例")
	}
}

func TestUpdateProjectWithoutUseCaseIs501(t *testing.T) {
	api, token := newProjectAPI(t, nil)

	rec := doJSON(t, api, token, http.MethodPatch,
		"/api/v1/projects/"+projectID, map[string]any{"name": "x"}, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("期望 501，得到 %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoveProjectRequiresWriteToken(t *testing.T) {
	fake := &fakeProjects{result: resources.ProjectRemovalResult{ProjectID: projectID}}
	api, _ := newProjectAPI(t, fake)

	// 无凭据：中间件必须先拒，不能走到删除。
	req := httptest.NewRequest(http.MethodDelete, "http://127.0.0.1:8030/api/v1/projects/"+projectID, nil)
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，得到 %d %s", rec.Code, rec.Body.String())
	}
	if fake.calls != 0 {
		t.Fatal("未认证请求不该进用例")
	}
}
