package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	httpevents "github.com/kiwh77/maw/internal/httpapi/events"
	httpobservability "github.com/kiwh77/maw/internal/httpapi/observability"
	httpread "github.com/kiwh77/maw/internal/httpapi/read"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/projection"
	"github.com/kiwh77/maw/internal/query"
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

	// 观测与提醒（VN-956）：查询走只读快照，SSE 只发失效信号。
	obsAPI := newObservabilityAPI(store, auth)
	obsH := obsAPI.Handler()

	v1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch v1Route(r.URL.Path) {
		case v1Events:
			eventsH.ServeHTTP(w, r)
		case v1Observability:
			obsH.ServeHTTP(w, r)
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
				// 扩展来源与能力的有效开关（按证据分级，配置只能往下压）。
				ext := rt.ExtensionsView()
				view["extensions"] = ext["extensions"]
				view["extended_enabled"] = ext["extended_enabled"]
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
				LogPath:   rep.LogPath,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any(r), nil
		},
		Feed: func(since int64, projectID, agent string) ([]*db.Event, error) {
			return loadEventFeedPage(gdb, since, projectID, agent, board.MaxSSEEventBurst)
		},
		Log:     AgentLog,
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

// changeBridge 把事件包的失效源转成观测包要的形状。
//
// 两个包各有自己的 ChangeSignal（一个是事件账本的，一个是观测查询的），
// 都不该为了省一层适配而改成共用结构：它们的字段会朝不同方向长
// （观测要标「哪类数据变了」，事件只要水位）。这里只做字段搬运，不加语义。
type changeBridge struct {
	inner *httpevents.PollingChanges
}

func (b changeBridge) Subscribe(ctx context.Context) (<-chan httpobservability.ChangeSignal, func()) {
	if b.inner == nil {
		ch := make(chan httpobservability.ChangeSignal)
		close(ch)
		return ch, func() {}
	}
	src, cancel := b.inner.Subscribe(ctx)
	out := make(chan httpobservability.ChangeSignal, cap(src))
	go func() {
		defer close(out)
		for sig := range src {
			wm := sig.Watermark
			converted := httpobservability.ChangeSignal{
				Revision: sig.Revision,
				At:       sig.At,
				// 水位按值搬运：观测侧要知道「拉到哪」，但不需要事件账本的类型。
				Watermark: &httpobservability.Watermark{
					Cursor:     wm.Cursor,
					ReceivedAt: wm.ReceivedAt,
				},
			}
			select {
			case out <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, cancel
}

// newObservabilityAPI 装出观测与提醒 API。
//
// 抽成函数只为可测：装配错了的症状是「接口 404／403／空数据」，
// 在集成现场很难与业务空数据区分，单测里直接打一次 handler 最快。
// auth 为 nil 时表示调用方已在外层鉴权（测试或同源内嵌）。
func newObservabilityAPI(store *db.Store, auth *localauth.Authenticator) *httpobservability.API {
	return &httpobservability.API{
		Auth:      auth,
		Timeline:  newCheckpointTimeline(store),
		Stats:     &httpobservability.SQLStats{Store: store},
		Sessions:  &httpobservability.SQLSessions{Store: store},
		Reminders: &httpobservability.SQLReminders{Store: store, Writer: store},
		Changes: changeBridge{inner: &httpevents.PollingChanges{
			Interval: 2 * time.Second,
			// 失效信号只带水位与版本：没有正文，事实由查询接口按水位补拉。
			Peek: func(ctx context.Context) (query.Watermark, int64, error) {
				var wm query.Watermark
				err := query.Read(store, func(rd *query.Reader) error {
					wm = rd.Watermark()
					return nil
				})
				return wm, 0, err
			},
		}},
	}
}

// v1HandlerKind 是 /api/v1 下的三类处理者。
type v1HandlerKind int

const (
	// v1Read 是任务、运行、操作等只读列表与详情。
	v1Read v1HandlerKind = iota
	// v1Events 是事件补拉、失效流与旁路上报。
	v1Events
	// v1Observability 是时间轴、活动、会话、统计、提醒与观测能力。
	v1Observability
)

// observabilitySuffixes 是观测子树的资源名。
//
// 只按「资源段」匹配，不按前缀全等：路径里带着 {project_id}，
// 唯一稳定的部分是资源名本身。
var observabilitySuffixes = []string{
	"/timeline", "/activity", "/sessions", "/analytics", "/notifications", "/observability",
}

// v1Route 把 /api/v1 的路径分流到三类处理者。
//
// 分流必须是纯函数：根路由装配出错时，症状是「接口 404 或者被鉴权错拒」，
// 很难在集成现场分辨；把它单列出来就能用表驱动测试钉住。
func v1Route(path string) v1HandlerKind {
	switch path {
	case "/api/v1/events", "/api/v1/events/stream", "/api/v1/status-reports":
		return v1Events
	}
	if strings.HasPrefix(path, "/api/v1/projects/") {
		for _, suffix := range observabilitySuffixes {
			if strings.Contains(path, suffix) {
				return v1Observability
			}
		}
	}
	return v1Read
}

// checkpointTimeline 用库里的投影检查点回答「投影水位到哪了」。
//
// 为什么不去重建投影：投影是内存权威、可全量重建，但重建需要事实流水线
// （采集→摄取→事实），那条链路不归本卡接线。**所以这里只报水位，不编区间**：
// 缺测与「未重建」都进 Missing，绝不用 0 个区间冒充「这段时间没干活」。
type checkpointTimeline struct {
	// Load 读一次检查点；nil 表示该工程还没有检查点。
	Load func(ctx context.Context, projectID string) (*db.ProjectionState, error)
}

func newCheckpointTimeline(store query.Store) checkpointTimeline {
	return checkpointTimeline{
		Load: func(ctx context.Context, projectID string) (*db.ProjectionState, error) {
			_ = ctx
			var st *db.ProjectionState
			err := store.Read(func(tx *sql.Tx) error {
				var e error
				st, e = db.GetProjectionState(tx, projection.Name, projectID)
				return e
			})
			return st, err
		},
	}
}

// Timeline 实现 httpobservability.TimelineSource。
func (t checkpointTimeline) Timeline(ctx context.Context, q httpobservability.TimelineQuery) (httpobservability.TimelineData, error) {
	if strings.TrimSpace(q.ProjectID) == "" {
		return httpobservability.TimelineData{
			Unsupported: []httpobservability.Unsupported{{
				Capability: "timeline.project",
				Reason:     "project_required",
				Detail:     "时间轴按工程维度物化，必须指定 project_id",
			}},
		}, nil
	}
	out := httpobservability.TimelineData{}
	if t.Load == nil {
		out.Missing = append(out.Missing, httpobservability.Missing{
			Section: "timeline", Reason: "projection_source_not_configured",
			Detail: "未配置投影检查点来源，无法给出水位",
		})
		return out, nil
	}
	st, err := t.Load(ctx, q.ProjectID)
	if err != nil {
		return httpobservability.TimelineData{}, err
	}
	if st == nil {
		out.Missing = append(out.Missing, httpobservability.Missing{
			Section: "timeline", Reason: "projection_not_built",
			Detail: "该工程还没有时间轴检查点；区间与水位均未知，不能用 0 表示",
		})
		return out, nil
	}
	out.Cursors = projection.Cursors{
		StateCursor:      st.Cursor,
		ProjectionCursor: st.CompleteBefore,
		Version:          st.Version,
		Completeness:     completenessOf(st.Meta),
	}
	// 检查点存在但区间未在本进程重建：水位可信、区间不可信，两件事分开说。
	out.Missing = append(out.Missing, httpobservability.Missing{
		Section: "timeline.intervals", Reason: "projection_not_rebuilt",
		Detail: "本进程未重放事实流水线：水位来自检查点，区间集合为空不代表没有活动",
	})
	if st.DirtyFrom != nil || st.DirtyTo != nil {
		out.Missing = append(out.Missing, httpobservability.Missing{
			Section: "timeline.intervals", Reason: "projection_dirty_range",
			Detail: "检查点标记了脏范围，受影响区间需要重建",
		})
	}
	return out, nil
}

// completenessOf 读检查点里的完整度标注；缺省按 unknown，不假定 complete。
func completenessOf(meta map[string]any) projection.Completeness {
	if meta == nil {
		return projection.CompletenessUnknown
	}
	raw, _ := meta["completeness"].(string)
	switch projection.Completeness(raw) {
	case projection.CompletenessComplete:
		return projection.CompletenessComplete
	case projection.CompletenessPartial:
		return projection.CompletenessPartial
	default:
		return projection.CompletenessUnknown
	}
}
