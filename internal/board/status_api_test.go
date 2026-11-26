package board

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/localauth"
)

func testAuth(t *testing.T) (*localauth.Authenticator, string) {
	t.Helper()
	tok, err := localauth.OpenTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := tok.Mint("cursor-demo-1", localauth.ScopeWrite, localauth.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte("k"), 32)
	sess, err := localauth.NewSessionManager(key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &localauth.Authenticator{Tokens: tok, Sessions: sess}, plain
}

func TestStatusRequiresAuthNoAnonymousWrite(t *testing.T) {
	auth, token := testAuth(t)
	var reports []StatusReport
	var logCalls int
	h := newHandlerOpts(ServeOpts{
		Load: func(_, _, _ string) (Snapshot, error) {
			return Snapshot{Total: 1, EventID: 3}, nil
		},
		Status: func(r StatusReport) (map[string]any, error) {
			reports = append(reports, r)
			return map[string]any{"ok": true, "agent": r.Actor}, nil
		},
		Feed: func(since int64, project, agent string) ([]*db.Event, error) {
			return []*db.Event{{ID: since + 1, Actor: agent, Type: "external_progress"}}, nil
		},
		Log: func(project, agent string, offset int64, fromTail bool) (LogChunk, error) {
			logCalls++
			if project != "p1" || agent != "cursor-demo-1" {
				t.Fatalf("unexpected log args %s %s", project, agent)
			}
			return LogChunk{OK: true, Path: "/home/u/log.jsonl", Offset: offset, NextOffset: offset + 4, Chunk: "abcd", EOF: false}, nil
		},
		Auth: auth,
	})

	body, _ := json.Marshal(StatusReport{Actor: "cursor-demo-1", Message: "hi", ProjectID: "p1", LogPath: "/tmp/x"})

	// 匿名写必须拒绝
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/status", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status code=%d body=%s", rr.Code, rr.Body.String())
	}

	// Bearer 写成功
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/status", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("authed status code=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(reports) != 1 || reports[0].Actor != "cursor-demo-1" || reports[0].LogPath != "/tmp/x" {
		t.Fatalf("%v", reports)
	}

	// 旧只读仍兼容（无令牌可读 board）
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/board", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"auto_execution"`) {
		t.Fatalf("board read code=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"enabled":false`) {
		t.Fatalf("auto mode must default off: %s", rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/feed?since=2&agent=cursor-demo-1", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("feed code=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/log?project=p1&agent=cursor-demo-1&offset=0&tail=1", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"chunk":"abcd"`) || logCalls != 1 {
		t.Fatalf("log code=%d body=%s calls=%d", rr.Code, rr.Body.String(), logCalls)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/status", bytes.NewReader(body))
	req.RemoteAddr = "8.8.8.8:99"
	req.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden got %d", rr.Code)
	}
}

func TestStatusWithoutAuthAssemblyRejected(t *testing.T) {
	h := newHandlerOpts(ServeOpts{
		Load: func(_, _, _ string) (Snapshot, error) { return Snapshot{}, nil },
		Status: func(r StatusReport) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		},
	})
	body, _ := json.Marshal(StatusReport{Actor: "x", Message: "y"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/status", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unwired auth must reject writes, got %d", rr.Code)
	}
}
