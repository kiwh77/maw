package board

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// eventHub shares one cross-process state watcher across all connected tabs.
// CLI workers write SQLite independently, so an in-memory publish hook alone
// would miss their commits. No database reads run when no clients are listening.
type eventHub struct {
	load     LoadFunc
	feed     FeedFunc
	interval time.Duration
	mu       sync.Mutex
	clients  map[chan hubUpdate]struct{}
	stop     context.CancelFunc
}

type hubUpdate struct {
	boardChanged bool
	eventsJSON   []string // pre-marshaled event payloads
}

func newEventHub(load LoadFunc, feed FeedFunc, interval time.Duration) *eventHub {
	return &eventHub{
		load: load, feed: feed, interval: interval,
		clients: make(map[chan hubUpdate]struct{}),
	}
}

func snapshotFingerprint(s Snapshot) ([32]byte, error) {
	s.UpdatedAt = "" // A generated timestamp is not a persisted state change.
	payload, err := json.Marshal(struct {
		Snapshot
		EventID int64 `json:"event_id"`
	}{Snapshot: s, EventID: s.EventID})
	return sha256.Sum256(payload), err
}

func (h *eventHub) subscribe() (<-chan hubUpdate, func()) {
	updates := make(chan hubUpdate, 8)
	h.mu.Lock()
	h.clients[updates] = struct{}{}
	if h.stop == nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.stop = cancel
		go h.watch(ctx)
	}
	h.mu.Unlock()
	var once sync.Once
	return updates, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(h.clients, updates)
			if len(h.clients) == 0 && h.stop != nil {
				h.stop()
				h.stop = nil
			}
		})
	}
}

func (h *eventHub) broadcast(u hubUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		select {
		case client <- u:
		default:
			// Drop if slow; board-changed is idempotent and feed can catch up via /api/feed.
		}
	}
}

func (h *eventHub) watch(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	var last [32]byte
	var lastEventID int64
	valid := false
	check := func() {
		snapshot, err := h.load("", "", "")
		if err != nil {
			valid = false
			return
		}
		fingerprint, err := snapshotFingerprint(snapshot)
		if err != nil {
			valid = false
			return
		}
		if valid && fingerprint == last {
			return
		}
		u := hubUpdate{boardChanged: true}
		if h.feed != nil && snapshot.EventID > lastEventID {
			if evs, err := h.feed(lastEventID, "", ""); err == nil {
				for _, e := range evs {
					b, err := json.Marshal(e)
					if err != nil {
						continue
					}
					u.eventsJSON = append(u.eventsJSON, string(b))
					if e.ID > lastEventID {
						lastEventID = e.ID
					}
				}
			}
		}
		if snapshot.EventID > lastEventID {
			lastEventID = snapshot.EventID
		}
		last = fingerprint
		valid = true
		if ctx.Err() != nil {
			return
		}
		h.broadcast(u)
	}
	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}

func (h *eventHub) handler(heartbeat time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := w.(http.Flusher); !ok {
			http.Error(w, "streaming unavailable", http.StatusNotImplemented)
			return
		}
		updates, unsubscribe := h.subscribe()
		defer unsubscribe()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		controller := http.NewResponseController(w)
		write := func(frame string) error {
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := fmt.Fprint(w, frame); err != nil {
				return err
			}
			return controller.Flush()
		}
		if err := write("retry: 3000\nevent: ready\ndata: {}\n\n"); err != nil {
			return
		}
		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case u := <-updates:
				for _, payload := range u.eventsJSON {
					if err := write("event: event\ndata: " + payload + "\n\n"); err != nil {
						return
					}
				}
				if u.boardChanged {
					if err := write("event: board-changed\ndata: {}\n\n"); err != nil {
						return
					}
				}
			case <-ticker.C:
				if err := write(": keepalive\n\n"); err != nil {
					return
				}
			}
		}
	})
}
