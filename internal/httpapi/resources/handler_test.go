package resources_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"


	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/httpapi/resources"
	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/nodes"
)

const projectID = "0123456789abcdef"

type fakeTasks struct {
	mu    sync.Mutex
	byKey map[string]resources.TaskCreateResult
	revs  map[model.InstanceID]model.Revision
	n     int
}

func (f *fakeTasks) Create(_ context.Context, req resources.TaskCreateRequest) (resources.TaskCreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byKey == nil {
		f.byKey = map[string]resources.TaskCreateResult{}
		f.revs = map[model.InstanceID]model.Revision{}
	}
	if prev, ok := f.byKey[req.IdempotencyKey]; ok {
		prev.Replay = true
		return prev, nil
	}
	if !req.DisplayID.Valid() {
		return resources.TaskCreateResult{}, resources.ErrInvalid{Code: "invalid_display_id", Message: "display_id 非法"}
	}
	f.n++
	id := model.InstanceID("ti_created00" + string(rune('0'+f.n)))
	out := resources.TaskCreateResult{
		InstanceID: id, DisplayID: req.DisplayID, Version: 1, Revision: 1,
	}
	f.byKey[req.IdempotencyKey] = out
	f.revs[id] = 1
	return out, nil
}

func (f *fakeTasks) UpdateDefinition(_ context.Context, req resources.TaskUpdateRequest) (resources.TaskUpdateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.revs[req.InstanceID]
	if !ok {
		return resources.TaskUpdateResult{}, resources.ErrNotFound{Message: "实例不存在"}
	}
	if cur != req.ExpectedRevision {
		return resources.TaskUpdateResult{}, resources.ErrConflict{
			Code: "revision_mismatch", Message: "revision 不匹配",
			Details: map[string]any{"actual_revision": cur},
		}
	}
	f.revs[req.InstanceID] = cur + 1
	return resources.TaskUpdateResult{
		InstanceID: req.InstanceID, Version: 2, Revision: cur + 1,
	}, nil
}

type fakeNodes struct {
	mu    sync.Mutex
	nodes map[model.NodeID]nodes.Snapshot
	keys  map[string]resources.NodeWriteResult
}

func (f *fakeNodes) Register(_ context.Context, req resources.NodeWriteRequest) (resources.NodeWriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nodes == nil {
		f.nodes = map[model.NodeID]nodes.Snapshot{}
		f.keys = map[string]resources.NodeWriteResult{}
	}
	if prev, ok := f.keys[req.IdempotencyKey]; ok {
		prev.Replay = true
		return prev, nil
	}
	id := req.Registration.Config.NodeID
	if _, exists := f.nodes[id]; exists {
		return resources.NodeWriteResult{}, nodes.ErrExists
	}
	snap := nodes.Snapshot{NodeID: id, ProjectID: req.Registration.ProjectID, Config: req.Registration.Config, Revision: 1}
	f.nodes[id] = snap
	out := resources.NodeWriteResult{Snapshot: snap, Created: true}
	f.keys[req.IdempotencyKey] = out
	return out, nil
}

func (f *fakeNodes) UpdateConfig(_ context.Context, req resources.NodeWriteRequest) (resources.NodeWriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := req.Registration.Config.NodeID
	cur, ok := f.nodes[id]
	if !ok {
		return resources.NodeWriteResult{}, nodes.ErrNotFound
	}
	if cur.Revision != req.ExpectedRevision {
		return resources.NodeWriteResult{}, nodes.ErrRevisionMismatch
	}
	cur.Config = req.Registration.Config
	cur.Revision++
	f.nodes[id] = cur
	return resources.NodeWriteResult{Snapshot: cur}, nil
}

type fakeProbes struct {
	mu   sync.Mutex
	keys map[string]resources.ProbeResult
	n    int
}

func (f *fakeProbes) Accept(_ context.Context, req resources.ProbeRequest) (resources.ProbeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.keys == nil {
		f.keys = map[string]resources.ProbeResult{}
	}
	if prev, ok := f.keys[req.IdempotencyKey]; ok {
		prev.Replay = true
		return prev, nil
	}
	f.n++
	out := resources.ProbeResult{OperationID: model.OperationID("op_probe000" + string(rune('0'+f.n)))}
	f.keys[req.IdempotencyKey] = out
	return out, nil
}

func newAPI(t *testing.T) (*resources.API, string, *fakeTasks) {
	t.Helper()
	tok, err := localauth.OpenTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := tok.Mint("writer-1", localauth.ScopeWrite)
	if err != nil {
		t.Fatal(err)
	}
	tasks := &fakeTasks{}
	api := &resources.API{
		Tasks:  tasks,
		Nodes:  &fakeNodes{},
		Probes: &fakeProbes{},
		Auth:   &localauth.Authenticator{Tokens: tok},
	}
	return api, plain, tasks
}

func doJSON(t *testing.T, api *resources.API, token, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:8030"+path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	return rec
}

func TestCreateTask201AndIdempotentReplay(t *testing.T) {
	api, token, _ := newAPI(t)
	body := map[string]any{
		"display_id": "VN-001", "title": "hello",
		"write_scope": []string{"a.go"}, "acceptance": []string{"ok"},
	}
	hdr := map[string]string{"Idempotency-Key": "create-task-key-001"}
	rec := doJSON(t, api, token, http.MethodPost, "/api/v1/projects/"+projectID+"/tasks", body, hdr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var first map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &first)
	if first["ok"] != true || first["idempotent_replay"] != false {
		t.Fatalf("first=%v", first)
	}

	rec = doJSON(t, api, token, http.MethodPost, "/api/v1/projects/"+projectID+"/tasks", body, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay status=%d", rec.Code)
	}
	var second map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &second)
	if second["idempotent_replay"] != true {
		t.Fatalf("replay flag missing: %v", second)
	}
	if first["task_instance"] != second["task_instance"] {
		t.Fatal("幂等重放必须返回同一实例")
	}
}

func TestUpdateTaskRevisionMismatch(t *testing.T) {
	api, token, tasks := newAPI(t)
	hdr := map[string]string{"Idempotency-Key": "create-task-key-002"}
	rec := doJSON(t, api, token, http.MethodPost, "/api/v1/projects/"+projectID+"/tasks",
		map[string]any{"display_id": "VN-002", "title": "t"}, hdr)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	inst, _ := created["task_instance"].(string)

	// 占用正确 revision 一次
	_ = tasks
	rec = doJSON(t, api, token, http.MethodPatch, "/api/v1/tasks/"+inst,
		map[string]any{"title": "t2"},
		map[string]string{"Idempotency-Key": "upd-task-key-00001", "If-Match": `rev-1`})
	if rec.Code != http.StatusOK {
		t.Fatalf("update=%d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, api, token, http.MethodPatch, "/api/v1/tasks/"+inst,
		map[string]any{"title": "t3"},
		map[string]string{"Idempotency-Key": "upd-task-key-00002", "If-Match": `rev-1`})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "revision_mismatch") {
		t.Fatalf("期望 409 revision_mismatch，得到 %d %s", rec.Code, rec.Body.String())
	}
}

func TestNodeConfigRejectsCredentials(t *testing.T) {
	api, token, _ := newAPI(t)
	body := map[string]any{
		"config": map[string]any{
			"node_id": "node-a", "name": "A", "adapter_kind": "codebuddy_cli",
			"entry_path": "/usr/bin/cb", "profile_ref": "default", "mode": "managed_cli",
			"platform": "darwin", "slot_limit": 1, "accept_new": true,
			"password": "super-secret",
		},
	}
	rec := doJSON(t, api, token, http.MethodPost, "/api/v1/projects/"+projectID+"/nodes", body,
		map[string]string{"Idempotency-Key": "node-create-key-001"})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "credentials_not_allowed") {
		t.Fatalf("凭据应被拒绝：%d %s", rec.Code, rec.Body.String())
	}
}

func TestNodeRegisterAndProbeAccepted(t *testing.T) {
	api, token, _ := newAPI(t)
	body := map[string]any{
		"config": map[string]any{
			"node_id": "node-b", "name": "B", "adapter_kind": "codebuddy_cli",
			"entry_path": "/usr/bin/cb", "profile_ref": "default", "mode": "managed_cli",
			"platform": "darwin", "slot_limit": 2, "accept_new": true,
		},
	}
	rec := doJSON(t, api, token, http.MethodPost, "/api/v1/projects/"+projectID+"/nodes", body,
		map[string]string{"Idempotency-Key": "node-create-key-002"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register=%d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, api, token, http.MethodPost, "/api/v1/nodes/node-b/probes", map[string]any{},
		map[string]string{"Idempotency-Key": "probe-key-00000001", "If-Match": `rev-1`})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("probe=%d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") == "" {
		t.Fatal("202 必须带 Location")
	}
	if !strings.Contains(rec.Body.String(), "operation_id") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestMissingIdempotencyAndRevision(t *testing.T) {
	api, token, _ := newAPI(t)
	rec := doJSON(t, api, token, http.MethodPost, "/api/v1/projects/"+projectID+"/tasks",
		map[string]any{"display_id": "VN-009", "title": "x"}, nil)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "invalid_idempotency_key") {
		t.Fatalf("缺幂等键：%d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, api, token, http.MethodPatch, "/api/v1/tasks/ti_abcdef012345",
		map[string]any{"title": "x"},
		map[string]string{"Idempotency-Key": "upd-missing-rev-0001"})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "revision_required") {
		t.Fatalf("缺 If-Match：%d %s", rec.Code, rec.Body.String())
	}
}
