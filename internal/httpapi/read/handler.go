package read

import (
	"database/sql"
	"net/http"

	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/query"
)

// API 是版本化只读资源集合。Store 必须是只读友好的 query.Store（如 *db.Store）。
// Auth 非空时要求 Bearer／会话且具备 read 作用域；测试可注入 nil 跳过鉴权。
type API struct {
	Store query.Store
	Auth  *localauth.Authenticator
}

// Handler 返回可挂到任意 ServeMux 的 /api/v1 只读树；调用方负责根路由装配。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", a.handleProjects)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/tasks", a.handleProjectTasks)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/nodes", a.handleProjectNodes)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/runs", a.handleProjectRuns)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/operations", a.handleProjectOperations)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/deliveries", a.handleProjectDeliveries)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/instances", a.handleProjectInstances)
	mux.HandleFunc("GET /api/v1/tasks/{instance_id}", a.handleTaskByInstance)
	mux.HandleFunc("GET /api/v1/operations/{operation_id}", a.handleOperationByID)
	mux.HandleFunc("GET /api/v1/runs/{run_id}", a.handleRunByID)
	mux.HandleFunc("GET /api/v1/board", a.handleBoard)

	var h http.Handler = mux
	if a.Auth != nil {
		h = a.Auth.Middleware(a.Auth.RequireScope(localauth.ScopeRead, mux))
	}
	return h
}

// txStore 把已打开的只读事务伪装成 query.Store，保证列表与水位同一快照。
type txStore struct{ tx *sql.Tx }

func (t *txStore) Read(fn func(*sql.Tx) error) error { return fn(t.tx) }

// withReader 在 Store 的一个只读快照里执行 fn。
func (a *API) withReader(fn func(*query.Reader, *sql.Tx) error) error {
	return a.Store.Read(func(tx *sql.Tx) error {
		return query.Read(&txStore{tx: tx}, func(rd *query.Reader) error {
			return fn(rd, tx)
		})
	})
}

type errResponseSent struct{}

func (errResponseSent) Error() string { return "response sent" }

func isSent(err error) bool {
	_, ok := err.(errResponseSent)
	return ok
}
