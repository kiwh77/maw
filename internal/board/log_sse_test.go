package board

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventStreamLogAppend(t *testing.T) {
	var calls int
	hub := newEventHub(
		func(_, _, _ string) (Snapshot, error) { return Snapshot{Total: 1}, nil },
		nil,
		func(project, agent string, offset int64, fromTail bool) (LogChunk, error) {
			calls++
			if project != "p1" || agent != "a1" {
				t.Fatalf("bad args %s %s", project, agent)
			}
			if offset > 0 {
				return LogChunk{OK: true, Path: "/home/u/x.log", Offset: offset, NextOffset: offset, EOF: true}, nil
			}
			return LogChunk{
				OK: true, Path: "/home/u/x.log", Offset: 0, NextOffset: 5,
				Chunk: "hello", EOF: false,
			}, nil
		},
		5*time.Millisecond,
	)
	handler := hub.handler(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events?log_project=p1&log_agent=a1&log_offset=0", nil).WithContext(ctx)
	w := &streamRecorder{ResponseRecorder: httptest.NewRecorder(), frames: make(chan string, 16)}
	done := make(chan struct{})
	go func() { handler.ServeHTTP(w, req); close(done) }()

	deadline := time.After(2 * time.Second)
	sawLog := false
	for !sawLog {
		select {
		case frame := <-w.frames:
			if strings.Contains(frame, "event: log-append") {
				sawLog = true
				var payload map[string]any
				dataLine := ""
				for _, line := range strings.Split(frame, "\n") {
					if strings.HasPrefix(line, "data: ") {
						dataLine = strings.TrimPrefix(line, "data: ")
					}
				}
				if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
					t.Fatalf("bad payload %q: %v", dataLine, err)
				}
				if payload["chunk"] != "hello" || payload["agent"] != "a1" {
					t.Fatalf("%v", payload)
				}
			}
		case <-deadline:
			t.Fatalf("no log-append; calls=%d", calls)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream leak")
	}
}
