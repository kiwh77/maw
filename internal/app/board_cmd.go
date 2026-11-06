package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/claim"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/home"
	httpcommands "github.com/kiwh77/maw/internal/httpapi/commands"
	httpevents "github.com/kiwh77/maw/internal/httpapi/events"
	httpobservability "github.com/kiwh77/maw/internal/httpapi/observability"
	httpread "github.com/kiwh77/maw/internal/httpapi/read"
	httpresources "github.com/kiwh77/maw/internal/httpapi/resources"
	httpsystemjobs "github.com/kiwh77/maw/internal/httpapi/systemjobs"
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
		// 与 system watch 共用同一把 coordinator.lock：auto 派发与系统 Agent
		// 只由一个 serve 进程推进；第二个 serve 抢不到锁就安分退出。
		startServeLoops(cwd)
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

	// 工程登记写资源：看板「工程」页的移除走这一条，与 CLI 同一套用例。
	// 节点写口接到本机 nodes 表（通用 local_cli），结束 501。
	resAPI := &httpresources.API{
		Projects:   projectRemovalPort{cwd: cwd},
		Nodes:      hubDBNodes{gdb: gdb},
		NodeChecks: hubNodeChecks{gdb: gdb},
		Auth:       auth,
	}
	resH := resAPI.Handler()

	// 命令写入口（派发／领取／取消／交付／评审／合入）：派发接到统一领取用例，
	// 其余端口未接线时回 501，让「路径已挂载」与「用例已接线」分得清。
	cmdAPI := &httpcommands.API{
		Dispatcher: hubCommandPorts{cwd: cwd, gdb: gdb, store: store},
		Auth:       auth,
	}
	cmdH := cmdAPI.Handler()

	sysAPI := &httpsystemjobs.API{
		Store: store,
		Auth:  auth,
		Config: func(projectID string) (config.SystemAgent, error) {
			_ = projectID
			if pc, err := resolveProject(cwd); err == nil && pc.Config != nil {
				return pc.Config.SystemAgent, nil
			}
			return config.SystemAgent{}, nil
		},
	}
	sysH := sysAPI.Handler()

	// 模型目录只读端点：只列「已登记实测枚举」，不写状态、不探测本机 CLI。
	// flavor 是发行包维度（cn / intl），不是节点的 cli_flavor（claude / codex）。
	modelsH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method_not_allowed", http.StatusMethodNotAllowed)
			return
		}
		out, err := ListNodeModels(r.URL.Query().Get("flavor"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	})

	v1 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/nodes/models" {
			modelsH.ServeHTTP(w, r)
			return
		}
		kind := v1Route(r.URL.Path)
		// 写方法落在没有专属写处理者的路径上时分给写树：
		// 命令资源（dispatches／claims／cancellations…）归 commands，
		// 其余（任务定义、节点登记、工程移除）归 resources。这样
		// /api/v1/projects/{id}/dispatches 才有 202 + Location，而不是只读树的纯文本 405。
		// 事件上报、系统作业、观测确认这些自带写处理者的路径仍按路径先分流。
		if kind == v1Read && isV1WriteMethod(r.Method) {
			if v1CommandPath(r.URL.Path) {
				cmdH.ServeHTTP(w, r)
				return
			}
			resH.ServeHTTP(w, r)
			return
		}
		switch kind {
		case v1Events:
			eventsH.ServeHTTP(w, r)
		case v1Observability:
			obsH.ServeHTTP(w, r)
		case v1Resources:
			resH.ServeHTTP(w, r)
		case v1System:
			sysH.ServeHTTP(w, r)
		default:
			readH.ServeHTTP(w, r)
		}
	})

	runtimeView := func() map[string]any {
		view := map[string]any{
			"scope":   "system",
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
				Kind:      rep.Kind,
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
		V1:      v1JSONErrorNormalizer{next: v1},
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

// hubDispatchNode 是中枢派发用的节点标识。
//
// 与人工领取的 manual-compat 同一思路：中枢受理派发时只借一个槽位载体，
// 不冒充真实 Worker 节点；真实节点由 Worker 自己登记。
const hubDispatchNode = "hub-dispatch"

// hubCommandPorts 把 /api/v1 命令写入口接到既有用例。
//
// 只接线「派发」——它与 maw next 共用同一个领取用例（engine/claim），
// 受理后留下 pending 的 dispatch 操作，由协调者（或 CLI 领取）继续推进；
// 其余命令端点没有现成用例可复用，保持未接线（501），不在 Hub 里另造语义。
type hubCommandPorts struct {
	cwd   string
	gdb   string
	store *db.Store
}

// Dispatch 按指定任务／节点派发，返回待执行操作（202 + Location 由传输层补齐）。
func (p hubCommandPorts) Dispatch(ctx context.Context, cmd httpcommands.DispatchCommand) (httpcommands.Receipt, error) {
	pc, err := hubProjectContext(p.cwd, p.gdb, string(cmd.ProjectID))
	if err != nil {
		return httpcommands.Receipt{}, err
	}
	nodeID := cmd.NodeID
	if nodeID == "" {
		nodeID = model.NodeID(hubDispatchNode)
		if err := ensureHubDispatchNode(p.store, pc.ID()); err != nil {
			return httpcommands.Receipt{}, err
		}
	}
	workspace, err := hubDispatchWorkspace(pc, cmd.IdempotencyKey)
	if err != nil {
		return httpcommands.Receipt{}, err
	}
	// actor 来自令牌／浏览器会话（如 session:…），不一定符合 {应用}-{工程}-{序号}：
	// 与 CLI 自领一样保留原样，否则中枢派发会被命名规范挡在门外。
	legacyActor := !model.AgentID(cmd.Actor).Valid()
	out, err := claim.New(p.store, nil).Claim(ctx, claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID:        model.ProjectID(pc.ID()),
			AgentID:          model.AgentID(cmd.Actor),
			RequestedTask:    cmd.RequestedTask,
			ExpectedRevision: &cmd.ExpectedRevision,
			Capacity:         cmd.Capacity,
			IdempotencyKey:   cmd.IdempotencyKey,
		},
		NodeID:        nodeID,
		Workspace:     workspace,
		LegacyAgentID: legacyActor,
	})
	if err != nil {
		return httpcommands.Receipt{}, err
	}
	rev := out.Revision
	return httpcommands.Receipt{
		OperationID: out.OperationID,
		Status:      hubOperationStatus(p.store, out.OperationID, out.Replay),
		Action:      "dispatch",
		Resource:    "/api/v1/projects/" + pc.ID() + "/dispatches",
		Revision:    &rev,
		Replay:      out.Replay,
	}, nil
}

// hubProjectContext 按 project_id 找到工程上下文；本仓工程优先走已解析上下文。
func hubProjectContext(cwd, gdb, projectID string) (*projCtx, error) {
	if pc, err := resolveProject(cwd); err == nil && pc.ID() == projectID {
		return pc, nil
	}
	var proj *db.Project
	if err := db.WithTx(gdb, func(tx *sql.Tx) error {
		var e error
		proj, e = db.GetProject(tx, projectID)
		return e
	}); err != nil {
		return nil, err
	}
	if proj == nil {
		return nil, httpcommands.ErrNotFound{Message: "工程 " + projectID + " 未登记"}
	}
	local, err := home.RequireHome(proj.Path)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(home.ConfigPath(local))
	if err != nil {
		return nil, err
	}
	return &projCtx{Repo: proj.Path, LocalHome: local, GlobalDB: gdb, Project: proj, Config: cfg}, nil
}

// ensureHubDispatchNode 保证中枢派发节点已登记（同工程复用，不重复建）。
func ensureHubDispatchNode(store *db.Store, projectID string) error {
	return store.Write(func(tx *sql.Tx) error {
		n, err := db.GetNode(tx, hubDispatchNode)
		if err != nil {
			return err
		}
		if n != nil && n.ProjectID == projectID {
			return nil
		}
		return db.UpsertNode(tx, &db.Node{
			ID: hubDispatchNode, ProjectID: projectID,
			Label: hubDispatchNode, SlotLimit: 64,
		})
	})
}

// hubDispatchWorkspace 按幂等键派生稳定的独占目录：重放命中同一目录，避免双开。
func hubDispatchWorkspace(pc *projCtx, idempotencyKey string) (string, error) {
	rt, err := NewRuntime(pc, "")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(idempotencyKey))
	dir := filepath.Join(rt.StateDir(), "workspaces", "api-"+hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Abs(dir)
}

// hubOperationStatus 把操作结果映射成契约里的四值状态。
//
// 新受理一定是 pending → queued；只有幂等重放才按已落库结果回报终止态，
// 这样 202 永远不宣称成功，重放也不会把一个已应用的操作说成还在排队。
func hubOperationStatus(store *db.Store, id model.OperationID, replay bool) httpcommands.OperationStatus {
	if !replay {
		return httpcommands.StatusQueued
	}
	result := model.ResultPending
	if err := store.Read(func(tx *sql.Tx) error {
		op, err := db.GetOperation(tx, string(id))
		if err != nil || op == nil {
			return err
		}
		result = op.Result
		return nil
	}); err != nil {
		return httpcommands.StatusQueued
	}
	switch result {
	case model.ResultApplied, model.ResultAlreadyFinished:
		return httpcommands.StatusSucceeded
	case model.ResultRejected:
		return httpcommands.StatusFailed
	}
	return httpcommands.StatusQueued
}

// commandSuffixes 是命令子树的资源名（按动作而非对象区分）。
var commandSuffixes = []string{
	"/dispatches", "/claims", "/cancellations", "/retries",
	"/deliveries", "/verifications", "/reviews", "/merges",
}

// v1CommandPath 判断写方法是否落在命令子树上。
//
// 只看资源段后缀，且仅用于写方法分流：GET 的同名路径（如
// /api/v1/projects/{id}/deliveries 列表）仍归只读树，不会被命令树抢走。
func v1CommandPath(p string) bool {
	for _, s := range commandSuffixes {
		if strings.HasSuffix(p, s) {
			return true
		}
	}
	return false
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

// v1JSONErrorNormalizer 把 /api/v1 子树里仍由 net/http mux 兜底产生的纯文本 404/405
// 改写成契约 §6 的 JSON 错误体。
//
// 各子 API 内部仍用按方法注册的 mux；方法没匹配上时 mux 只会回 text/plain 的
// 「Method Not Allowed」／「404 page not found」。中枢既然声明 managed_via=cli_or_api_v1，
// 调用方就只能按 error.code 分支，纯文本会被当成服务端异常，所以在装配层统一改写。
// 只改写这两类纯文本错误：正常响应与 SSE 流原样透传（不缓冲、不改头）。
type v1JSONErrorNormalizer struct {
	next http.Handler
}

func (n v1JSONErrorNormalizer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ew := &v1ErrorWriter{ResponseWriter: w, status: http.StatusOK}
	n.next.ServeHTTP(ew, r)
	ew.finish()
}

// v1ErrorWriter 延迟写出状态头：先看清状态与 Content-Type 再决定是透传还是改写。
type v1ErrorWriter struct {
	http.ResponseWriter
	status  int
	started bool
	swallow bool
}

func (w *v1ErrorWriter) WriteHeader(status int) {
	if w.started {
		return
	}
	w.status = status
}

func (w *v1ErrorWriter) Write(b []byte) (int, error) {
	w.start()
	if w.swallow {
		// 已改写成 JSON 错误体：丢掉原先的纯文本正文。
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

func (w *v1ErrorWriter) Flush() {
	w.start()
	if f, ok := w.ResponseWriter.(http.Flusher); ok && !w.swallow {
		f.Flush()
	}
}

func (w *v1ErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// start 只执行一次：纯文本 404/405 改写为 JSON，其余响应原样放行。
func (w *v1ErrorWriter) start() {
	if w.started {
		return
	}
	w.started = true
	if w.plainError() {
		w.swallow = true
		localauth.WriteJSONError(w.ResponseWriter, w.status,
			v1ErrorCode(w.status), v1ErrorMessage(w.status), nil)
		return
	}
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *v1ErrorWriter) finish() {
	if !w.started {
		w.start()
	}
}

func (w *v1ErrorWriter) plainError() bool {
	switch w.status {
	case http.StatusNotFound, http.StatusMethodNotAllowed:
	default:
		return false
	}
	ct := w.Header().Get("Content-Type")
	return ct == "" || strings.HasPrefix(ct, "text/plain")
}

func v1ErrorCode(status int) string {
	if status == http.StatusMethodNotAllowed {
		return "method_not_allowed"
	}
	return "not_found"
}

func v1ErrorMessage(status int) string {
	if status == http.StatusMethodNotAllowed {
		return "该 /api/v1 路径不接受此方法"
	}
	return "未知的 /api/v1 路径"
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
	// v1Resources 是写资源（工程登记移除、任务定义、节点登记等）；
	// 只读路径上的写方法也归这里（见 v1 装配处的 isV1WriteMethod 分流）。
	v1Resources
	// v1System 是系统 Agent 作业队列。
	v1System
)

// isV1WriteMethod 判断是否是需要落到写树的方法。
//
// GET/HEAD/OPTIONS 归只读树：中枢的 /api/v1 查询接口按方法注册，
// 写树只承载会改状态的 POST/PATCH/PUT/DELETE。
func isV1WriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

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
	case "/api/v1/system-jobs":
		return v1System
	case "/api/v1/timeline", "/api/v1/activity", "/api/v1/sessions", "/api/v1/analytics":
		// 「全部工程」的全局统计路由：没有工程段，不能落到只读任务树。
		return v1Observability
	}
	if strings.HasPrefix(path, "/api/v1/projects/") {
		rest := strings.TrimPrefix(path, "/api/v1/projects/")
		// 「工程本身」只有一段：{project_id}。子资源（tasks／nodes／timeline…）
		// 都带第二段，继续按原规则分流。
		if rest != "" && !strings.Contains(rest, "/") {
			return v1Resources
		}
		if strings.Contains(path, "/system-jobs") {
			return v1System
		}
		for _, suffix := range observabilitySuffixes {
			if strings.Contains(path, suffix) {
				return v1Observability
			}
		}
	}
	return v1Read
}

// checkpointAggregate 是全部工程检查点的合并视图。
//
// 合并口径：游标取最大（全局事实水位），complete_before 取最小（所有工程都
// 追平的最远位置），完整度只在全部工程都 complete 时才是 complete。
type checkpointAggregate struct {
	Cursor         int64
	CompleteBefore int64
	Completeness   projection.Completeness
	Dirty          bool
}

// checkpointTimeline 用库里的投影检查点回答「投影水位到哪了」。
//
// 为什么不去重建投影：投影是内存权威、可全量重建，但重建需要事实流水线
// （采集→摄取→事实），那条链路不归本卡接线。**所以这里只报水位，不编区间**：
// 缺测与「未重建」都进 Missing，绝不用 0 个区间冒充「这段时间没干活」。
type checkpointTimeline struct {
	// Load 读一次检查点；nil 表示该工程还没有检查点。
	Load func(ctx context.Context, projectID string) (*db.ProjectionState, error)
	// LoadAll 聚合全部工程的检查点；工程为空（「全部工程」）时使用。
	LoadAll func(ctx context.Context) (*checkpointAggregate, error)
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
		LoadAll: func(ctx context.Context) (*checkpointAggregate, error) {
			_ = ctx
			var (
				rows           int
				cursor         int64
				completeBefore int64
				completeRows   int64
				partialRows    int64
				dirtyRows      int64
			)
			err := store.Read(func(tx *sql.Tx) error {
				return tx.QueryRow(`SELECT COUNT(*),
  COALESCE(MAX(cursor), 0), COALESCE(MIN(complete_before), 0),
  COALESCE(SUM(CASE WHEN json_extract(meta_json, '$.completeness') = 'complete' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN json_extract(meta_json, '$.completeness') = 'partial' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN dirty_from IS NOT NULL OR dirty_to IS NOT NULL THEN 1 ELSE 0 END), 0)
FROM projection_state WHERE projection = ?`, projection.Name).Scan(
					&rows, &cursor, &completeBefore, &completeRows, &partialRows, &dirtyRows)
			})
			if err != nil {
				return nil, err
			}
			if rows == 0 {
				return nil, nil
			}
			completeness := projection.CompletenessUnknown
			switch {
			case completeRows == int64(rows):
				completeness = projection.CompletenessComplete
			case completeRows+partialRows > 0:
				completeness = projection.CompletenessPartial
			}
			return &checkpointAggregate{
				Cursor:         cursor,
				CompleteBefore: completeBefore,
				Completeness:   completeness,
				Dirty:          dirtyRows > 0,
			}, nil
		},
	}
}

// Timeline 实现 httpobservability.TimelineSource。
func (t checkpointTimeline) Timeline(ctx context.Context, q httpobservability.TimelineQuery) (httpobservability.TimelineData, error) {
	out := httpobservability.TimelineData{}
	if strings.TrimSpace(q.ProjectID) == "" {
		// 「全部工程」：合并各工程检查点，水位取最保守的公共边界。
		if t.LoadAll == nil {
			out.Missing = append(out.Missing, httpobservability.Missing{
				Section: "timeline", Reason: "projection_source_not_configured",
				Detail: "未配置全局投影检查点来源，无法给出全部工程的水位",
			})
			return out, nil
		}
		agg, err := t.LoadAll(ctx)
		if err != nil {
			return httpobservability.TimelineData{}, err
		}
		if agg == nil {
			out.Missing = append(out.Missing, httpobservability.Missing{
				Section: "timeline", Reason: "projection_not_built",
				Detail: "全部工程都还没有时间轴检查点；区间与水位均未知，不能用 0 表示",
			})
			return out, nil
		}
		out.Cursors = projection.Cursors{
			StateCursor:      agg.Cursor,
			ProjectionCursor: agg.CompleteBefore,
			Completeness:     agg.Completeness,
		}
		out.Missing = append(out.Missing, httpobservability.Missing{
			Section: "timeline.intervals", Reason: "projection_not_rebuilt",
			Detail: "本进程未重放事实流水线：水位来自检查点，区间集合为空不代表没有活动",
		})
		if agg.Dirty {
			out.Missing = append(out.Missing, httpobservability.Missing{
				Section: "timeline.intervals", Reason: "projection_dirty_range",
				Detail: "有工程标记了脏范围，受影响区间需要重建",
			})
		}
		return out, nil
	}
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
