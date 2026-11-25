package resources_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/httpapi/resources"
	"github.com/kiwh77/maw/internal/localauth"
)

// fakeAutoExecution 记录工作台开关端口收到的请求。
type fakeAutoExecution struct {
	getCalls int
	setCalls int
	lastSet  resources.AutoExecutionSetRequest
	state    resources.AutoExecutionState
	err      error
}

func (f *fakeAutoExecution) Get(_ context.Context, projectID string) (resources.AutoExecutionState, error) {
	f.getCalls++
	if f.err != nil {
		return resources.AutoExecutionState{}, f.err
	}
	out := f.state
	out.ProjectID = projectID
	return out, nil
}

func (f *fakeAutoExecution) Set(_ context.Context, req resources.AutoExecutionSetRequest) (resources.AutoExecutionState, error) {
	f.setCalls++
	f.lastSet = req
	if f.err != nil {
		return resources.AutoExecutionState{}, f.err
	}
	out := f.state
	out.ProjectID = req.ProjectID
	out.Enabled = req.Enabled
	return out, nil
}

func newSwitchAPI(t *testing.T, auto resources.AutoExecution) (*resources.API, string) {
	t.Helper()
	tok, err := localauth.OpenTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := tok.Mint("board-operator", localauth.ScopeWrite)
	if err != nil {
		t.Fatal(err)
	}
	return &resources.API{AutoExecution: auto, Auth: &localauth.Authenticator{Tokens: tok}}, plain
}

// TestAutoExecutionGetAndPut 覆盖读写同源契约：GET 回就绪结论，PUT 只带
// enabled 也要能写，且 enabled 缺省必须被拒绝（开关不能有「不表态」）。
func TestAutoExecutionGetAndPut(t *testing.T) {
	fake := &fakeAutoExecution{state: resources.AutoExecutionState{
		Ready: true, NodeID: "dsh-local", Limit: 1, ServeRunning: true, Applied: true,
	}}
	api, token := newSwitchAPI(t, fake)
	path := "/api/v1/projects/" + projectID + "/auto-execution"

	rec := doJSON(t, api, token, http.MethodGet, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET code=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		AutoExecution resources.AutoExecutionState `json:"auto_execution"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.AutoExecution.Ready || payload.AutoExecution.NodeID != "dsh-local" {
		t.Fatalf("GET 应回服务端判定的状态: %+v", payload.AutoExecution)
	}

	rec = doJSON(t, api, token, http.MethodPut, path,
		map[string]any{"enabled": true, "node_id": "dsh-local"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT code=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.setCalls != 1 || !fake.lastSet.Enabled || fake.lastSet.NodeID != "dsh-local" {
		t.Fatalf("端口未收到正确请求: calls=%d last=%+v", fake.setCalls, fake.lastSet)
	}

	// enabled 缺省：拒绝，避免「什么都不表态」被当成打开。
	rec = doJSON(t, api, token, http.MethodPut, path, map[string]any{"node_id": "x"}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("缺 enabled 应 422，实际 %d", rec.Code)
	}
}

// TestAutoExecutionWiringAndMethod 覆盖 501 与 405：
// 「路径已挂载但用例没接线」与「方法用错」必须分得清。
func TestAutoExecutionWiringAndMethod(t *testing.T) {
	api, token := newSwitchAPI(t, nil)
	path := "/api/v1/projects/" + projectID + "/auto-execution"
	if rec := doJSON(t, api, token, http.MethodGet, path, nil, nil); rec.Code != http.StatusNotImplemented {
		t.Fatalf("未接线应 501，实际 %d", rec.Code)
	}
	fake := &fakeAutoExecution{}
	api.AutoExecution = fake
	if rec := doJSON(t, api, token, http.MethodDelete, path, nil, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("错误方法应 405，实际 %d", rec.Code)
	}
}
