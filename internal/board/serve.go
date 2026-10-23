package board

import (
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

// StatusReport is the write payload for POST /api/status.
type StatusReport struct {
	ProjectID string `json:"project_id"`
	Actor     string `json:"actor"`
	TaskID    string `json:"task_id"`
	Message   string `json:"message"`
	Phase     string `json:"phase"`
	Pct       *int   `json:"pct"`
	Client    string `json:"client"`
	Status    string `json:"status"`
	LogPath   string `json:"log_path"`
}

// StatusFunc applies an injected status report. Returns a JSON-serializable result map.
type StatusFunc func(StatusReport) (map[string]any, error)

// FeedFunc returns events with id > since (optional project/agent filters).
type FeedFunc func(since int64, projectID, agent string) ([]*db.Event, error)

// ServeOpts configures the local status hub HTTP server.
type ServeOpts struct {
	Load   LoadFunc
	Detail TaskDetailFunc
	Status StatusFunc // nil → POST /api/status returns 501
	Feed   FeedFunc   // nil → GET /api/feed returns 501; SSE stays invalidation-only
	Log    LogFunc    // nil → GET /api/log returns 501; SSE skips log-append
}

// assetHandler 提供嵌入的前端构建产物（assets/dist）。
// 只暴露真实文件：不做目录列举，且与页面一致使用 no-store 避免旧 bundle 残留。
func assetHandler() http.Handler {
	assets, err := BoardAssets()
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "assets unavailable", http.StatusNotImplemented)
		})
	}
	files := http.FileServerFS(assets)
	return http.StripPrefix("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			http.NotFound(w, r)
			return
		}
		info, statErr := fs.Stat(assets, name)
		if statErr != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	}))
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newHandlerOpts(opts ServeOpts) http.Handler {
	mux := http.NewServeMux()
	events := newEventHub(opts.Load, opts.Feed, opts.Log, 500*time.Millisecond)
	mux.Handle("/api/events", events.handler(15*time.Second))
	mux.Handle("/assets/", assetHandler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(PageHTML()))
	})
	mux.HandleFunc("/api/board", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		snap, err := opts.Load(q.Get("project"), q.Get("agent"), q.Get("status"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(snap)
	})
	mux.HandleFunc("/api/task", func(w http.ResponseWriter, r *http.Request) {
		if opts.Detail == nil {
			http.Error(w, "task detail unavailable", http.StatusNotImplemented)
			return
		}
		q := r.URL.Query()
		taskID := q.Get("id")
		if taskID == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		d, err := opts.Detail(q.Get("project"), taskID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if d.Task == nil {
			http.Error(w, "task not found", http.StatusNotFound)
			return
		}
		d.OK = true
		if d.Events == nil {
			d.Events = []*db.Event{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(d)
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isLoopback(r) {
			http.Error(w, "status inject only allowed from loopback", http.StatusForbidden)
			return
		}
		if opts.Status == nil {
			http.Error(w, "status inject unavailable", http.StatusNotImplemented)
			return
		}
		var rep StatusReport
		if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		rep.Actor = strings.TrimSpace(rep.Actor)
		rep.ProjectID = strings.TrimSpace(rep.ProjectID)
		out, err := opts.Status(rep)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/api/log", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if opts.Log == nil {
			http.Error(w, "log unavailable", http.StatusNotImplemented)
			return
		}
		q := r.URL.Query()
		projectID := strings.TrimSpace(q.Get("project"))
		agent := strings.TrimSpace(q.Get("agent"))
		if projectID == "" || agent == "" {
			http.Error(w, "missing project or agent", http.StatusBadRequest)
			return
		}
		offset, _ := strconv.ParseInt(q.Get("offset"), 10, 64)
		fromTail := q.Get("tail") == "1" || q.Get("tail") == "true"
		chunk, err := opts.Log(projectID, agent, offset, fromTail)
		if err != nil && !chunk.OK {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(chunk)
	})
	mux.HandleFunc("/api/feed", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if opts.Feed == nil {
			http.Error(w, "feed unavailable", http.StatusNotImplemented)
			return
		}
		q := r.URL.Query()
		since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
		evs, err := opts.Feed(since, q.Get("project"), q.Get("agent"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if evs == nil {
			evs = []*db.Event{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "events": evs})
	})
	mux.HandleFunc("/snapshot.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/board?"+r.URL.RawQuery, http.StatusTemporaryRedirect)
	})
	return mux
}

// Serve starts the local hub. Prefer ServeWith for status inject + feed.
func Serve(addr string, load LoadFunc, detail TaskDetailFunc) (string, error) {
	return ServeWith(addr, ServeOpts{Load: load, Detail: detail})
}

// ServeWith starts the local hub with optional write/feed endpoints.
func ServeWith(addr string, opts ServeOpts) (string, error) {
	if opts.Load == nil {
		return "", errString("load func required")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	url := "http://" + ln.Addr().String() + "/"
	go func() {
		_ = http.Serve(ln, newHandlerOpts(opts))
	}()
	return url, nil
}

type errString string

func (e errString) Error() string { return string(e) }
