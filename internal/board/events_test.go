package board

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

func TestSnapshotFingerprintIgnoresClockButDetectsState(t *testing.T) {
	s := BuildSnapshot([]*db.Task{{ID: "T-1", Status: "ready"}}, nil, 4)
	before, err := snapshotFingerprint(s)
	if err != nil {
		t.Fatal(err)
	}
	s.UpdatedAt = "another clock tick"
	after, _ := snapshotFingerprint(s)
	if before != after {
		t.Fatal("clock-only changes must not trigger notifications")
	}
	s.Tasks[0].Status = "in_progress"
	after, _ = snapshotFingerprint(s)
	if before == after {
		t.Fatal("task changes must trigger notifications")
	}
	s.EventID++
	eventOnly, _ := snapshotFingerprint(s)
	if after == eventOnly {
		t.Fatal("new events must trigger notifications even without task changes")
	}
}

func TestEventHubBroadcastsAndStopsWhenLastClientLeaves(t *testing.T) {
	var calls atomic.Int32
	var version atomic.Int32
	hub := newEventHub(func(_, _, _ string) (Snapshot, error) {
		calls.Add(1)
		return Snapshot{Total: int(version.Load())}, nil
	}, 5*time.Millisecond)
	one, leaveOne := hub.subscribe()
	two, leaveTwo := hub.subscribe()
	defer leaveOne()
	defer leaveTwo()
	for _, updates := range []<-chan struct{}{one, two} {
		select {
		case <-updates:
		case <-time.After(time.Second):
			t.Fatal("no initial baseline")
		}
	}
	version.Store(1)
	for _, updates := range []<-chan struct{}{one, two} {
		select {
		case <-updates:
		case <-time.After(time.Second):
			t.Fatal("subscriber missed change")
		}
	}
	leaveOne()
	version.Store(2)
	select {
	case <-two:
	case <-time.After(time.Second):
		t.Fatal("remaining subscriber missed change")
	}
	leaveTwo()
	hub.mu.Lock()
	stopped := hub.stop == nil && len(hub.clients) == 0
	hub.mu.Unlock()
	if !stopped {
		t.Fatal("watcher must stop when no clients remain")
	}
}

func TestEventHubRecoversFromLoadFailure(t *testing.T) {
	failed := make(chan struct{}, 1)
	var fail atomic.Bool
	fail.Store(true)
	hub := newEventHub(func(_, _, _ string) (Snapshot, error) {
		if fail.Load() {
			select {
			case failed <- struct{}{}:
			default:
			}
			return Snapshot{}, errors.New("temporary DB error")
		}
		return Snapshot{}, nil
	}, time.Millisecond)
	updates, leave := hub.subscribe()
	defer leave()
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("load failure was not observed")
	}
	fail.Store(false)
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("recovery must invalidate client data")
	}
}

// Capture a real streaming response without binding a network port.
type streamRecorder struct {
	*httptest.ResponseRecorder
	mu     sync.Mutex
	frames chan string
}

func (w *streamRecorder) Flush() {
	w.mu.Lock()
	frame := w.Body.String()
	w.Body.Reset()
	w.mu.Unlock()
	w.frames <- frame
}

func TestEventStreamReadyHeartbeatAndCancellation(t *testing.T) {
	hub := newEventHub(func(_, _, _ string) (Snapshot, error) { return Snapshot{}, nil }, time.Hour)
	handler := hub.handler(5 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	w := &streamRecorder{ResponseRecorder: httptest.NewRecorder(), frames: make(chan string, 8)}
	done := make(chan struct{})
	go func() { handler.ServeHTTP(w, req); close(done) }()
	select {
	case frame := <-w.frames:
		if !strings.Contains(frame, "event: ready\ndata: {}\n\n") || !strings.Contains(frame, "retry: 3000") {
			t.Fatalf("bad initial frame: %q", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("no ready frame")
	}
	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatal("wrong content type")
	}
	if w.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatal("proxy buffering must be disabled")
	}
	heartbeatTimeout := time.After(time.Second)
heartbeatLoop:
	for {
		select {
		case frame := <-w.frames:
			if frame == ": keepalive\n\n" {
				break heartbeatLoop
			}
			if frame != "event: board-changed\ndata: {}\n\n" {
				t.Fatalf("bad frame: %q", frame)
			}
		case <-heartbeatTimeout:
			t.Fatal("no heartbeat")
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disconnected stream leaked")
	}
}

func TestEventStreamRejectsOtherMethods(t *testing.T) {
	hub := newEventHub(nil, time.Second)
	w := httptest.NewRecorder()
	hub.handler(time.Second).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/events", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d", w.Code)
	}
}

func TestEventHubDoesNotNotifyForClockOnlyChanges(t *testing.T) {
	hub := newEventHub(func(_, _, _ string) (Snapshot, error) { return Snapshot{UpdatedAt: time.Now().String()}, nil }, time.Millisecond)
	updates, leave := hub.subscribe()
	defer leave()
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("no baseline")
	}
	select {
	case <-updates:
		t.Fatal("timestamp triggered an invalidation")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestFingerprintRejectsUnserializableData(t *testing.T) {
	_, err := snapshotFingerprint(Snapshot{Tasks: []*db.Task{{Extra: map[string]any{"invalid": make(chan int)}}}})
	if err == nil {
		t.Fatal("invalid data should fail fingerprinting")
	}
}

func TestBoardRoutesKeepFiltersAndExposeEvents(t *testing.T) {
	var got [3]string
	handler := newHandler(func(project, agent, status string) (Snapshot, error) {
		got = [3]string{project, agent, status}
		return Snapshot{Total: 4}, nil
	}, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/board?project=p&agent=w1&status=ready", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":4`) || got != [3]string{"p", "w1", "ready"} {
		t.Fatalf("snapshot filters lost: %v %s", got, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "prefers-color-scheme: light") || !strings.Contains(w.Body.String(), `new EventSource("/api/events")`) {
		t.Fatal("page did not include themes and realtime client")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/events", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("events route not registered: %d", w.Code)
	}
}
