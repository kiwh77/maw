package app

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/db"
	httpevents "github.com/kiwh77/maw/internal/httpapi/events"
	httpread "github.com/kiwh77/maw/internal/httpapi/read"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/localauth"
)

// BoardFilters 看板筛选条件（空字符串表示不限）
type BoardFilters struct {
	Project string
	Agent   string
	Status  string
}

func Board(cwd string, serve bool, addr string, filters BoardFilters) (Result, error) {
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	if err := db.Init(gdb); err != nil {
		return nil, err
	}
	// 若在某工程内且未指定 --project，仍默认看全局；可用 --project 收窄
	if filters.Project == "" {
		if pc, err := resolveProject(cwd); err == nil {
			_ = pc // 确保工程已注册进全局库
		}
	}

	snap, err := loadBoardSnapshot(gdb, filters.Project, filters.Agent, filters.Status, 4)
	if err != nil {
		return nil, err
	}

	gdir, err := home.GlobalDir()
	if err != nil {
		return nil, err
	}
	htmlPath, err := board.Write(gdir, snap)
	if err != nil {
		return nil, err
	}
	// 若当前在工程内，同步写一份到工程 .multiagent/board
	if pc, err := resolveProject(cwd); err == nil {
		_, _ = board.Write(pc.LocalHome, snap)
	}

	term := board.RenderTerminal(snap)
	res := Result{
		"ok": true, "terminal": term, "html": htmlPath, "counts": snap.Counts,
		"agents": snap.Agents, "projects": snap.Projects,
		"filters": map[string]string{
			"project": filters.Project, "agent": filters.Agent, "status": filters.Status,
		},
		"global_db": gdb,
		"snapshot":  filepath.Join(gdir, "board", "snapshot.json"),
		"next":      i18n.T("next.open_html"),
		"auto_execution": map[string]any{
			"enabled": false, "managed_execution_limit": 1, "config_version": 1,
		},
	}
	if serve {
		if addr == "" {
			addr = "127.0.0.1:7420"
		}
		hub, err := buildBoardHub(cwd, gdb, gdir, filters)
		if err != nil {
			return nil, err
		}
		url, err := board.ServeWith(addr, hub)
		if err != nil {
			return nil, i18n.EW("err.board_serve", "next.change_addr", err)
		}
		res["url"] = url
		res["auth"] = map[string]any{
			"tokens_path": hub.Auth.Tokens.Path(),
			"bootstrap":   "POST /api/session (browser) or mint CLI token under ~/.maw/auth",
		}
		res["next"] = i18n.T("board.next_url", url)
		fmt.Println(i18n.T("board.hub_started"), url)
		fmt.Println(i18n.T("board.data_source"), gdb)
		fmt.Println(i18n.T("board.ctrl_c"))
		select {}
	}
	return res, nil
}

// Serve is an alias for Board(..., serve=true) — recommended hub entrypoint.
func Serve(cwd, addr string, filters BoardFilters) (Result, error) {
	return Board(cwd, true, addr, filters)
}

// buildBoardHub 装配鉴权、旧兼容入口与 /api/v1（同一套本机令牌／会话）。
func buildBoardHub(cwd, gdb, gdir string, filters BoardFilters) (board.ServeOpts, error) {
	auth, bootstrapNote, err := ensureHubAuth(gdir)
	if err != nil {
		return board.ServeOpts{}, err
	}
	_ = bootstrapNote

	store, err := db.OpenStore(gdb)
	if err != nil {
		return board.ServeOpts{}, err
	}
	// store 随进程存活；中枢退出不杀 Runner，对账靠本机证据。
	reporter := httpevents.NewStoreReporter(store)
	eventsAPI := &httpevents.API{
		Store:   store,
		Auth:    auth,
		Reports: reporter,
	}
	readAPI := &httpread.API{Store: store, Auth: auth}
	eventsH := eventsAPI.Handler()
	readH := readAPI.Handler()
	v1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/events", "/api/v1/events/stream", "/api/v1/status-reports":
			eventsH.ServeHTTP(w, r)
		default:
			readH.ServeHTTP(w, r)
		}
	})

	runtimeView := func() map[string]any {
		view := map[string]any{
			"enabled": false, "managed_execution_limit": 1, "config_version": 1,
		}
		if pc, err := resolveProject(cwd); err == nil {
			if rt, err := NewRuntime(pc, ""); err == nil {
				mv := rt.ManagedView()
				view["enabled"] = mv["auto_enabled"]
				view["managed_execution_limit"] = mv["managed_execution_limit"]
				view["config_version"] = mv["config_version"]
				view["platform_supported"] = mv["platform_supported"]
			}
		}
		return view
	}

	return board.ServeOpts{
		Load: func(projectID, agent, status string) (board.Snapshot, error) {
			if projectID == "" {
				projectID = filters.Project
			}
			if agent == "" {
				agent = filters.Agent
			}
			if status == "" {
				status = filters.Status
			}
			return loadBoardSnapshot(gdb, projectID, agent, status, 4)
		},
		Detail: func(projectID, taskID string) (board.TaskDetail, error) {
			return loadTaskDetail(gdb, projectID, taskID)
		},
		Status: func(rep board.StatusReport) (map[string]any, error) {
			r, err := StatusReport(cwd, StatusReportInput{
				ProjectID: rep.ProjectID,
				Actor:     rep.Actor,
				TaskID:    rep.TaskID,
				Message:   rep.Message,
				Phase:     rep.Phase,
				Pct:       rep.Pct,
				Client:    rep.Client,
				Status:    rep.Status,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any(r), nil
		},
		Feed: func(since int64, projectID, agent string) ([]*db.Event, error) {
			return loadEventFeedPage(gdb, since, projectID, agent, board.MaxSSEEventBurst)
		},
		Runtime: runtimeView,
		Auth:    auth,
		V1:      v1,
	}, nil
}

// ensureHubAuth 打开（或初始化）本机令牌库与会话密钥；新旧写端共用。
func ensureHubAuth(gdir string) (*localauth.Authenticator, string, error) {
	authDir := filepath.Join(gdir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		return nil, "", err
	}
	tokPath := filepath.Join(authDir, "tokens.json")
	tokStore, err := localauth.OpenTokenStore(tokPath)
	if err != nil {
		return nil, "", err
	}
	keyPath := filepath.Join(authDir, "session.key")
	key, err := loadOrCreateSessionKey(keyPath)
	if err != nil {
		return nil, "", err
	}
	sess, err := localauth.NewSessionManager(key, 12*time.Hour)
	if err != nil {
		return nil, "", err
	}
	note := "CLI: Authorization Bearer from " + tokPath + "; browser: POST /api/session"
	return &localauth.Authenticator{Tokens: tokStore, Sessions: sess}, note, nil
}

func loadOrCreateSessionKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b[:32], nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// loadEventFeedPage 单页事件补拉；调用方按 next_since 翻页，禁止一次跳过中间水位。
func loadEventFeedPage(gdb string, since int64, projectID, agent string, limit int) ([]*db.Event, error) {
	if limit <= 0 {
		limit = 200
	}
	var out []*db.Event
	err := db.WithTx(gdb, func(tx *sql.Tx) error {
		var e error
		out, e = db.ListEventsSince(tx, since, projectID, agent, limit)
		return e
	})
	return out, err
}
