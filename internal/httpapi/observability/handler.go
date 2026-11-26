package observability

import (
	"net/http"
	"time"

	"github.com/kiwh77/maw/internal/localauth"
)

// API 是观测与提醒查询的可挂载处理器。
//
// 各数据源都是接口：真实实现见 adapters.go，测试注入内存替身。
// 任一数据源为 nil 时对应能力降级为 unsupported（类型化结果），不会 500。
type API struct {
	// Auth 非空时要求 Bearer／同源会话；读接口要 read，确认要 write。
	Auth *localauth.Authenticator
	// Timeline 提供分层区间与投影水位。
	Timeline TimelineSource
	// Stats 提供统计报表（同时是时间轴 totals 的来源）。
	Stats StatsSource
	// Sessions 提供会话与轮次明细。
	Sessions SessionSource
	// Reminders 提供提醒列表与确认。
	Reminders ReminderSource
	// SystemHealth 提供自愈与系统健康报表；nil 时该能力为 unsupported。
	SystemHealth SystemHealthSource
	// Changes 提供 SSE 失效信号；nil 时流接口报 503 而不是假装连上。
	Changes ChangeSource
	// Now 可注入时钟（测试用）；默认 time.Now。
	Now func() time.Time
}

// Handler 返回 /api/v1 观测与提醒子树；调用方负责根路由装配。
//
// 路由按资源分层，动作挂在资源下（契约 §1）：提醒的「确认」是
// notifications/{id}/acks，而不是 /doAck。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	// 全局统计路由：顶部工程为「全部工程」时，四类观测事实按全工程聚合。
	mux.HandleFunc("GET /api/v1/timeline", a.handleTimeline)
	mux.HandleFunc("GET /api/v1/activity", a.handleActivity)
	mux.HandleFunc("GET /api/v1/sessions", a.handleSessions)
	mux.HandleFunc("GET /api/v1/analytics", a.handleStats)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/timeline", a.handleTimeline)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/activity", a.handleActivity)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/sessions", a.handleSessions)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/analytics", a.handleStats)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/notifications", a.handleReminders)
	mux.HandleFunc("POST /api/v1/projects/{project_id}/notifications/{notification_id}/acks", a.handleAck)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/observability/capabilities", a.handleCapabilities)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/observability/stream", a.handleStream)
	// 系统健康：全局与工程作用域同源，读事实表，不带投影游标依赖。
	mux.HandleFunc("GET /api/v1/system/health", a.handleSystemHealth)
	mux.HandleFunc("GET /api/v1/projects/{project_id}/system/health", a.handleSystemHealth)

	var h http.Handler = mux
	if a.Auth != nil {
		h = a.Auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := localauth.IdentityFrom(r.Context())
			if !ok {
				writeErr(w, http.StatusUnauthorized, "unauthenticated",
					"需要本机令牌或同源会话", nil)
				return
			}
			// 读要 read，写要 write：确认提醒会改库，不能只凭 read 作用域。
			need := localauth.ScopeRead
			if r.Method == http.MethodPost {
				need = localauth.ScopeWrite
			}
			if !id.HasScope(need) {
				writeErr(w, http.StatusForbidden, "insufficient_scope",
					"凭据作用域不足："+string(need), nil)
				return
			}
			mux.ServeHTTP(w, r)
		}))
	}
	return h
}

// now 返回当前时间；测试可注入固定时钟。
func (a *API) now() time.Time {
	if a != nil && a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

// capabilityList 是能力发现接口的内容：哪些能力可用、不可用时为什么。
//
// 前端据此在查询前就决定「不显示这张图」，而不是发一次请求拿空数组再猜。
func (a *API) capabilityList() []Capability {
	list := []Capability{
		capabilityOf("timeline", a.Timeline != nil, reasonIf(a.Timeline == nil, "source_not_configured")),
		// 热图同时需要区间与分桶：只接一半也不能画出一张看起来完整的热图。
		capabilityOf("activity", a.Stats != nil && a.Timeline != nil,
			reasonIf(a.Stats == nil || a.Timeline == nil, "source_not_configured")),
		capabilityOf("sessions", a.Sessions != nil, reasonIf(a.Sessions == nil, "source_not_configured")),
		capabilityOf("stats", a.Stats != nil, reasonIf(a.Stats == nil, "source_not_configured")),
		capabilityOf("reminders", a.Reminders != nil, reasonIf(a.Reminders == nil, "source_not_configured")),
		capabilityOf("stream", a.Changes != nil, reasonIf(a.Changes == nil, "source_not_configured")),
	}
	return list
}

func reasonIf(cond bool, reason string) string {
	if cond {
		return reason
	}
	return ""
}

// capabilitiesResponse 是能力发现接口的响应。
type capabilitiesResponse struct {
	meta
	Capabilities []Capability `json:"capabilities"`
	// Status 沿用同一套类型化状态：全部可用才是 ready。
	Status Status `json:"status"`
}

func (a *API) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	list := a.capabilityList()
	status := StatusReady
	for _, c := range list {
		if !c.Supported {
			status = StatusPartial
			break
		}
	}
	writeJSON(w, http.StatusOK, capabilitiesResponse{
		meta: meta{OK: true}, Capabilities: list, Status: status,
	})
}
