package systemjobs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

func TestProjectJobsAcceptsDispatchReady(t *testing.T) {
	dir := t.TempDir()
	store, err := db.OpenStore(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer((&API{Store: store}).Handler())
	defer srv.Close()

	post := func(body string) *http.Response {
		t.Helper()
		resp, err := http.Post(
			srv.URL+"/api/v1/projects/0123456789abcdef/system-jobs",
			"application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	resp := post(`{"kind":"dispatch_ready","reason":"有 ready 题待派发"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch_ready 应入队，got %d", resp.StatusCode)
	}
	var out struct {
		OK    bool   `json:"ok"`
		JobID string `json:"job_id"`
		Kind  string `json:"kind"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.JobID == "" {
		t.Fatalf("unexpected body: %+v", out)
	}

	bad := post(`{"kind":"not_a_kind"}`)
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法 kind 应拒绝，got %d", bad.StatusCode)
	}
}
