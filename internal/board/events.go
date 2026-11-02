package board

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// eventHub shares one cross-process state watcher across all connected tabs.
// CLI workers write SQLite independently, so an in-memory publish hook alone
// would miss their commits. No database reads run when no clients are listening.
type eventHub struct {
	load     LoadFunc
	feed     FeedFunc
	log      LogFunc
	interval time.Duration
	mu       sync.Mutex
	clients  map[*hubClient]struct{}
	stop     context.CancelFunc
}

type hubClient struct {
	ch         chan hubUpdate
	logProject string
	logAgent   string
	logOffset  int64
}

type hubUpdate struct {
	boardChanged bool
	eventsJSON   []string // pre-marshaled event payloads
	logJSON      string   // optional pre-marshaled log-append payload
	// catchup 非空时表示积压超过单次 SSE 突发上限：客户端须按 since 游标补拉，禁止跳过。
	catchup *catchupHint
}

type catchupHint struct {
	Since     int64  `json:"since"`
	Watermark int64  `json:"watermark"`
	Hint      string `json:"hint"`
}

func newEventHub(load LoadFunc, feed FeedFunc, log LogFunc, interval time.Duration) *eventHub {
	return &eventHub{
		load: load, feed: feed, log: log, interval: interval,
		clients: make(map[*hubClient]struct{}),
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

func (h *eventHub) subscribe(logProject, logAgent string, logOffset int64) (*hubClient, func()) {
	c := &hubClient{
		ch:         make(chan hubUpdate, 8),
		logProject: logProject,
		logAgent:   logAgent,
		logOffset:  logOffset,
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	if h.stop == nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.stop = cancel
		go h.watch(ctx)
	}
	h.mu.Unlock()
	var once sync.Once
	return c, func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(h.clients, c)
			if len(h.clients) == 0 && h.stop != nil {
				h.stop()
				h.stop = nil
			}
		})
	}
}

func (h *eventHub) broadcastBoard(u hubUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		select {
		case client.ch <- u:
		default:
			// Drop if slow; board-changed is idempotent and feed can catch up via /api/feed.
		}
	}
}

func (h *eventHub) sendLog(c *hubClient, payload string, nextOffset int64) {
	u := hubUpdate{logJSON: payload}
	select {
	case c.ch <- u:
		c.logOffset = nextOffset
	default:
		// Slow client: drop; UI reconnects /api/log with last known offset.
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
		} else {
			fingerprint, err := snapshotFingerprint(snapshot)
			if err != nil {
				valid = false
			} else if !valid || fingerprint != last {
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
						// 一页打满且水位仍落后：发 catchup，水位只推进到已推送的最后一条，禁止跳到 EventID。
						if len(evs) >= MaxSSEEventBurst && snapshot.EventID > lastEventID {
							u.catchup = &catchupHint{
								Since:     lastEventID,
								Watermark: snapshot.EventID,
								Hint:      "backlog exceeds SSE burst; pull /api/feed or /api/v1/events without skipping",
							}
						}
					}
				}
				// 故意不把 lastEventID 跳到 snapshot.EventID：未投递的事件必须经 feed／v1 补拉。
				last = fingerprint
				valid = true
				if ctx.Err() == nil {
					h.broadcastBoard(u)
				}
			}
		}

		if h.log == nil || ctx.Err() != nil {
			return
		}
		h.mu.Lock()
		clients := make([]*hubClient, 0, len(h.clients))
		for c := range h.clients {
			if c.logProject != "" && c.logAgent != "" {
				clients = append(clients, c)
			}
		}
		h.mu.Unlock()
		for _, c := range clients {
			chunk, err := h.log(c.logProject, c.logAgent, c.logOffset, false)
			if err != nil || !chunk.OK || chunk.Missing || chunk.Chunk == "" {
				continue
			}
			payload, err := json.Marshal(map[string]any{
				"project_id":  c.logProject,
				"agent":       c.logAgent,
				"path":        chunk.Path,
				"offset":      chunk.Offset,
				"next_offset": chunk.NextOffset,
				"chunk":       chunk.Chunk,
				"truncated":  chunk.Truncated,
				"eof":         chunk.EOF,
			})
			if err != nil {
				continue
			}
			h.sendLog(c, string(payload), chunk.NextOffset)
		}
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
		q := r.URL.Query()
		logProject := q.Get("log_project")
		logAgent := q.Get("log_agent")
		var logOffset int64
		if s := q.Get("log_offset"); s != "" {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				logOffset = v
			}
		}
		client, unsubscribe := h.subscribe(logProject, logAgent, logOffset)
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
			case u := <-client.ch:
				for _, payload := range u.eventsJSON {
					if err := write("event: event\ndata: " + payload + "\n\n"); err != nil {
						return
					}
				}
				if u.catchup != nil {
					b, _ := json.Marshal(u.catchup)
					if err := write("event: catchup\ndata: " + string(b) + "\n\n"); err != nil {
						return
					}
				}
				if u.logJSON != "" {
					if err := write("event: log-append\ndata: " + u.logJSON + "\n\n"); err != nil {
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
