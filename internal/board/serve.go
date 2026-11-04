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
	"github.com/kiwh77/maw/internal/localauth"
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

// RuntimeViewFunc 返回受管／自动执行配置视图（自动模式默认关闭）。
type RuntimeViewFunc func() map[string]any

// ServeOpts configures the local status hub HTTP server.
type ServeOpts struct {
	Load    LoadFunc
	Detail  TaskDetailFunc
	Status  StatusFunc // nil → POST /api/status returns 501
	Feed    FeedFunc   // nil → GET /api/feed returns 501; SSE stays invalidation-only
	Log     LogFunc    // nil → GET /api/log returns 501; SSE skips log-append
	Runtime RuntimeViewFunc
	// Auth 非空时：旧写入口与 /api/v1 共用一次接入授权；匿名 POST /api/status 被拒绝。
	Auth *localauth.Authenticator
	// V1 挂载版本化 API（events／read／commands 等）；路径须自带 /api/v1 前缀。
	V1 http.Handler
}

// MaxSSEEventBurst 是单次 SSE／feed 页的事件上限；更大积压走 catchup，禁止跳过。
const MaxSSEEventBurst = 200

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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
		// 迭代筛选按「参数是否出现」判定：?iteration= 表示只保留未归属任何迭代的任务，
		// 与「不带 iteration 参数＝不按迭代筛」必须区分开。
		if q.Has("iteration") {
			snap = snap.WithIteration(strings.TrimSpace(q.Get("iteration")))
		}
		out := map[string]any{
			"updated_at":       snap.UpdatedAt,
			"generated_at":     snap.GeneratedAt,
			"max_parallel":     snap.MaxParallel,
			"total":            snap.Total,
			"done":             snap.Done,
			"active":           snap.Active,
			"blocked":          snap.Blocked,
			"percent":          snap.Percent,
			"next_hint":        snap.NextHint,
			"risks":            snap.Risks,
			"counts":           snap.Counts,
			"tasks":            snap.Tasks,
			"agents":           snap.Agents,
			"projects":         snap.Projects,
			"filter_project":   snap.FilterProject,
			"filter_agent":     snap.FilterAgent,
			"filter_status":    snap.FilterStatus,
			"filter_iteration": snap.FilterIteration,
			"global":           snap.Global,
			// 旧只读响应保持字段兼容；附带契约视图（自动模式默认关闭）。
			"auto_execution": defaultAutoExecution(opts),
			"api_compat":     "board-v1",
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("/api/runtime", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             true,
			"auto_execution": defaultAutoExecution(opts),
			// 静态页／导出 HTML 不得发起受管写操作；受管入口只走受权 CLI／v1。
			"managed_via":           "cli_or_api_v1",
			"static_export_managed": false,
		})
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
		writeJSON(w, http.StatusOK, d)
	})

	statusHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		if id, ok := localauth.IdentityFrom(r.Context()); ok {
			if rep.Actor == "" {
				rep.Actor = id.Actor
			} else if id.Via == "token" && id.Actor != "" && id.Actor != rep.Actor {
				localauth.WriteJSONError(w, http.StatusForbidden, "actor_mismatch",
					"actor 必须与令牌主体一致", nil)
				return
			}
		}
		out, err := opts.Status(rep)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	if opts.Auth != nil {
		mux.Handle("/api/status", opts.Auth.Middleware(opts.Auth.RequireScope(localauth.ScopeWrite, statusHandler)))
	} else {
		// 未注入 Auth 时拒绝写：避免匿名写通道；装配层必须显式注入。
		mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
			localauth.WriteJSONError(w, http.StatusUnauthorized, "unauthenticated",
				"旧 POST /api/status 需要本机令牌或会话；请先完成授权引导", nil)
		})
	}

	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if opts.Auth == nil || opts.Auth.Sessions == nil {
			localauth.WriteJSONError(w, http.StatusNotImplemented, "session_unavailable",
				"会话引导未装配", nil)
			return
		}
		if !isLoopback(r) || !localauth.IsLoopbackHost(r.Host) {
			localauth.WriteJSONError(w, http.StatusForbidden, "loopback_required",
				"会话只在本机下发", nil)
			return
		}
		sess, err := opts.Auth.Sessions.Issue(w, r.Host)
		if err != nil {
			localauth.WriteJSONError(w, http.StatusForbidden, "session_issue_failed", err.Error(), nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "session_id": sess.ID, "csrf": sess.CSRFToken(),
			"next": "浏览器写请求带 CSRF；CLI 写请求用 Authorization: Bearer",
		})
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
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "events": evs,
			"has_more": len(evs) >= MaxSSEEventBurst,
			"next_since": func() int64 {
				if len(evs) == 0 {
					return since
				}
				return evs[len(evs)-1].ID
			}(),
		})
	})

	mux.HandleFunc("/snapshot.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/board?"+r.URL.RawQuery, http.StatusTemporaryRedirect)
	})

	var root http.Handler = mux
	if opts.V1 != nil {
		root = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/v1/") {
				opts.V1.ServeHTTP(w, r)
				return
			}
			mux.ServeHTTP(w, r)
		})
	}
	return root
}

func defaultAutoExecution(opts ServeOpts) map[string]any {
	if opts.Runtime != nil {
		if v := opts.Runtime(); v != nil {
			return v
		}
	}
	return map[string]any{
		"enabled":                 false,
		"managed_execution_limit": 1,
		"config_version":          1,
	}
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
