package board

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

func TestStatusAndFeedAPI(t *testing.T) {
	var reports []StatusReport
	var sinceSeen int64
	h := newHandlerOpts(ServeOpts{
		Load: func(_, _, _ string) (Snapshot, error) {
			return Snapshot{Total: 1, EventID: 3}, nil
		},
		Status: func(r StatusReport) (map[string]any, error) {
			reports = append(reports, r)
			return map[string]any{"ok": true, "agent": r.Actor}, nil
		},
		Feed: func(since int64, project, agent string) ([]*db.Event, error) {
			sinceSeen = since
			return []*db.Event{{ID: since + 1, Actor: agent, Type: "external_progress", Data: map[string]any{"message": "hi"}}}, nil
		},
	})

	body, _ := json.Marshal(StatusReport{Actor: "cursor-demo-1", Message: "hi", ProjectID: "p1"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/status", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("status code=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(reports) != 1 || reports[0].Actor != "cursor-demo-1" {
		t.Fatalf("%v", reports)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/feed?since=2&agent=cursor-demo-1", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || sinceSeen != 2 {
		t.Fatalf("feed code=%d since=%d body=%s", rr.Code, sinceSeen, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/status", bytes.NewReader(body))
	req.RemoteAddr = "8.8.8.8:99"
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden got %d", rr.Code)
	}
}
