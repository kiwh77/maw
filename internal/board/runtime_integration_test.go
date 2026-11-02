package board

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

func TestAssetsEmbedStillMounted(t *testing.T) {
	h := newHandlerOpts(ServeOpts{
		Load: func(_, _, _ string) (Snapshot, error) { return Snapshot{}, nil },
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/", nil)
	h.ServeHTTP(rr, req)
	// 目录列举关闭 → 404；若 handler 未挂载会落到 / 的 NotFound 以外路径。关键是路由存在且非 501「assets unavailable」 unless embed missing。
	if rr.Code == http.StatusNotImplemented && strings.Contains(rr.Body.String(), "assets unavailable") {
		t.Skip("embed assets not built in this checkout")
	}
	if rr.Code != http.StatusNotFound && rr.Code != http.StatusOK {
		t.Fatalf("assets route unexpected code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRuntimeViewAutoOffAndStaticExportNoManaged(t *testing.T) {
	h := newHandlerOpts(ServeOpts{
		Load: func(_, _, _ string) (Snapshot, error) { return Snapshot{Total: 1}, nil },
		Runtime: func() map[string]any {
			return map[string]any{"enabled": false, "managed_execution_limit": 1, "config_version": 1}
		},
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	ae, _ := body["auto_execution"].(map[string]any)
	if ae["enabled"] != false {
		t.Fatalf("auto must be off: %+v", ae)
	}
	if body["static_export_managed"] != false {
		t.Fatal("static export must not initiate managed ops")
	}
}

func TestSSECatchupDoesNotSkipPastBurst(t *testing.T) {
	var sinceSeen int64 = -1
	hub := newEventHub(func(_, _, _ string) (Snapshot, error) {
		return Snapshot{EventID: 500, Total: 1}, nil
	}, func(since int64, _, _ string) ([]*db.Event, error) {
		sinceSeen = since
		out := make([]*db.Event, 0, MaxSSEEventBurst)
		for i := 0; i < MaxSSEEventBurst; i++ {
			out = append(out, &db.Event{ID: since + int64(i) + 1, Type: "progress"})
		}
		return out, nil
	}, nil, 5*time.Millisecond)

	client, leave := hub.subscribe("", "", 0)
	defer leave()

	deadline := time.After(2 * time.Second)
	sawCatchup := false
	for !sawCatchup {
		select {
		case u := <-client.ch:
			if u.catchup != nil {
				sawCatchup = true
				if u.catchup.Watermark != 500 {
					t.Fatalf("watermark=%d", u.catchup.Watermark)
				}
				if len(u.eventsJSON) != MaxSSEEventBurst {
					t.Fatalf("should deliver one burst page, got %d", len(u.eventsJSON))
				}
				// 水位只推进到已投递末条，不得跳到 500
				if u.catchup.Since >= 500 {
					t.Fatalf("since jumped past undelivered: %d", u.catchup.Since)
				}
			}
		case <-deadline:
			t.Fatalf("no catchup; sinceSeen=%d", sinceSeen)
		}
	}
}

func TestV1MountReceivesPrefixedPaths(t *testing.T) {
	called := false
	h := newHandlerOpts(ServeOpts{
		Load: func(_, _, _ string) (Snapshot, error) { return Snapshot{}, nil },
		V1: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if r.URL.Path != "/api/v1/events" {
				t.Fatalf("path=%s", r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		}),
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	h.ServeHTTP(rr, req)
	if !called || rr.Code != http.StatusNoContent {
		t.Fatalf("v1 not mounted: called=%v code=%d", called, rr.Code)
	}
}
