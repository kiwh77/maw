package events

import (
	"database/sql"
	"net/http"

	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/query"
)

// API 是事件补拉／SSE／旁路上报的可挂载处理器。
type API struct {
	Store   Store
	Auth    *localauth.Authenticator
	Changes ChangeSource
	Reports Reporter
}

// Handler 返回 /api/v1 事件相关树；调用方负责根路由装配。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/events", a.handleEventsPull)
	mux.HandleFunc("GET /api/v1/events/stream", a.handleEventsStream)
	mux.HandleFunc("POST /api/v1/status-reports", a.handleStatusReports)

	var h http.Handler = mux
	if a.Auth != nil {
		// 读与写共用中间件；各 handler 内再按需检查作用域（stream／pull 需 read，上报需 write）。
		h = a.Auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := localauth.IdentityFrom(r.Context())
			if !ok {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "需要本机令牌或会话", nil)
				return
			}
			need := localauth.ScopeRead
			if r.Method == http.MethodPost {
				need = localauth.ScopeWrite
			}
			if !id.HasScope(need) {
				writeErr(w, http.StatusForbidden, "forbidden", "作用域不足", nil)
				return
			}
			mux.ServeHTTP(w, r)
		}))
	}
	return h
}

type txStore struct{ tx *sql.Tx }

func (t *txStore) Read(fn func(*sql.Tx) error) error { return fn(t.tx) }

func (a *API) withReader(fn func(*query.Reader) error) error {
	if a.Store == nil {
		return &query.Error{Code: "internal_error", Msg: "Store 未注入"}
	}
	return a.Store.Read(func(tx *sql.Tx) error {
		return query.Read(&txStore{tx: tx}, fn)
	})
}
