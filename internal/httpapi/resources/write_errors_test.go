package resources_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/httpapi/resources"
	"github.com/kiwh77/maw/internal/localauth"
)

// errorCode 取出契约 §6 错误体里的 code，顺便断言响应确实是 JSON。
//
// 写树的错误一律走 JSON 错误码：中枢声明 managed_via=cli_or_api_v1，
// 调用方只能靠 code 分支，纯文本 Method Not Allowed 会让前端把它当 500。
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("错误响应缺 Content-Type：%d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("错误响应不是 JSON：%d %s", rec.Code, rec.Body.String())
	}
	errObj, _ := body["error"].(map[string]any)
	if body["ok"] != false || errObj == nil {
		t.Fatalf("错误体形态不符：%s", rec.Body.String())
	}
	c, _ := errObj["code"].(string)
	if c == "" {
		t.Fatalf("错误体缺 code：%s", rec.Body.String())
	}
	return c
}

func newBareAPI(t *testing.T) (*resources.API, string) {
	t.Helper()
	tok, err := localauth.OpenTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := tok.Mint("writer-2", localauth.ScopeWrite)
	if err != nil {
		t.Fatal(err)
	}
	// 只注入鉴权，不注入任何用例：模拟中枢「写树已挂载、写资源未接线」。
	return &resources.API{Auth: &localauth.Authenticator{Tokens: tok}}, plain
}

func TestWriteMethodMismatchReturnsJSON405(t *testing.T) {
	api, token, _ := newAPI(t)

	// 建任务入口只接受 POST：读方法不能落到纯文本 405。
	rec := doJSON(t, api, token, http.MethodGet, "/api/v1/projects/"+projectID+"/tasks", nil, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("期望 405，得到 %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow 必须显式写出：%q", got)
	}
	if code := errorCode(t, rec); code != "method_not_allowed" {
		t.Fatalf("错误码应为 method_not_allowed，得到 %q", code)
	}

	// 更新入口只接受 PATCH。
	rec = doJSON(t, api, token, http.MethodPost, "/api/v1/tasks/ti_abcdef012345", map[string]any{}, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("期望 405，得到 %d %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "method_not_allowed" {
		t.Fatalf("错误码应为 method_not_allowed，得到 %q", code)
	}

	// 工程移除只接受 DELETE。
	rec = doJSON(t, api, token, http.MethodGet, "/api/v1/projects/"+projectID, nil, nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("期望 405，得到 %d %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "method_not_allowed" {
		t.Fatalf("错误码应为 method_not_allowed，得到 %q", code)
	}
}

func TestUnknownWritePathReturnsJSON404(t *testing.T) {
	api, token, _ := newAPI(t)

	rec := doJSON(t, api, token, http.MethodPost, "/api/v1/projects/"+projectID+"/no-such-resource",
		map[string]any{}, map[string]string{"Idempotency-Key": "unknown-path-key-0001"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("期望 404，得到 %d %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "not_found" {
		t.Fatalf("错误码应为 not_found，得到 %q", code)
	}
}

func TestUnwiredWriteResourcesReturnJSON501(t *testing.T) {
	api, token := newBareAPI(t)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		header map[string]string
	}{
		{
			name:   "建任务",
			method: http.MethodPost,
			path:   "/api/v1/projects/" + projectID + "/tasks",
			body:   map[string]any{"display_id": "VN-001", "title": "t"},
			header: map[string]string{"Idempotency-Key": "unwired-task-key-0001"},
		},
		{
			name:   "登记节点",
			method: http.MethodPost,
			path:   "/api/v1/projects/" + projectID + "/nodes",
			body:   map[string]any{"config": map[string]any{"node_id": "node-a"}},
			header: map[string]string{"Idempotency-Key": "unwired-node-key-0001"},
		},
		{
			name:   "节点探测",
			method: http.MethodPost,
			path:   "/api/v1/nodes/node-a/probes",
			body:   map[string]any{},
			header: map[string]string{"Idempotency-Key": "unwired-probe-key-001", "If-Match": `rev-1`},
		},
	}
	for _, tc := range cases {
		rec := doJSON(t, api, token, tc.method, tc.path, tc.body, tc.header)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s：期望 501，得到 %d %s", tc.name, rec.Code, rec.Body.String())
		}
		if code := errorCode(t, rec); code != "unsupported" {
			t.Fatalf("%s：错误码应为 unsupported，得到 %q", tc.name, code)
		}
	}
}
