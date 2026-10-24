package observability

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/notifications"
	"github.com/kiwh77/maw/internal/projection"
)

// remindersResponse 是提醒列表（F33）。
type remindersResponse struct {
	meta
	Result
	Items []ReminderItem `json:"items"`
	// NextCursor 为 null 表示最后一页。
	NextCursor any `json:"next_cursor"`
	Count      int `json:"count"`
	// Unhandled 是范围内未确认条数（不受分页影响）。
	Unhandled int `json:"unhandled"`
	// Counts 是按状态分组的条数。
	Counts map[string]int `json:"counts,omitempty"`
}

// ackResponse 是确认提醒的结果。
type ackResponse struct {
	meta
	Notification *ReminderItem `json:"notification"`
	// Changed 为 false 表示这条提醒此前已确认（幂等重放），不是失败。
	Changed bool `json:"changed"`
	// IdempotentReplay 与 Changed 互补，单独给出来方便客户端区分「第一次点」与「又点一次」。
	IdempotentReplay bool `json:"idempotent_replay"`
	// ChangedTaskStatus 恒为 false：确认提醒不改业务任务状态（HTTP 契约 §7.4）。
	ChangedTaskStatus bool `json:"changed_task_status"`
}

// handleReminders 返回提醒列表。
//
// 窗口、来源过滤、权限检查与其他读接口一致；但提醒没有来源维度（它由业务事件
// 派生，不挂在来源会话上），因此带 source_ns 时返回类型化的不支持，而不是
// 悄悄忽略过滤条件给出全量。
func (a *API) handleReminders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := rejectLegacyPagination(q); err != nil {
		mapErr(w, err)
		return
	}
	projectID := strings.TrimSpace(r.PathValue("project_id"))
	if projectID == "" {
		mapErr(w, &ErrNotFound{Resource: "project", Message: "缺少工程身份"})
		return
	}
	limit, err := parseLimit(q)
	if err != nil {
		mapErr(w, err)
		return
	}
	spec, err := parseWindow(q, a.now(), 30*24*time.Hour)
	if err != nil {
		mapErr(w, err)
		return
	}
	sourceNS, err := parseSourceNS(q)
	if err != nil {
		mapErr(w, err)
		return
	}
	status, err := parseReminderStatus(q.Get("status"))
	if err != nil {
		mapErr(w, err)
		return
	}
	kind, err := parseReminderKind(q.Get("kind"))
	if err != nil {
		mapErr(w, err)
		return
	}
	channel, err := parseReminderChannel(q.Get("channel"))
	if err != nil {
		mapErr(w, err)
		return
	}

	scope := Scope{ProjectID: projectID, SourceNS: sourceNS}
	window := windowOf(spec)

	if a.Reminders == nil {
		res := unsupportedResult(capabilityOf("reminders", false, "source_not_configured"),
			scope, window, []Unsupported{{
				Capability: "reminders", Reason: "source_not_configured",
				Detail: "未注入提醒数据源",
			}})
		writeJSON(w, http.StatusOK, remindersResponse{meta: meta{OK: true}, Result: res,
			Items: []ReminderItem{}})
		return
	}
	if sourceNS != "" {
		res := unsupportedResult(capabilityOf("reminders", true, ""), scope, window,
			[]Unsupported{{
				Capability: "reminders.source_filter", Reason: "source_dimension_absent",
				Detail: "提醒由业务事件派生，没有来源命名空间维度",
			}})
		writeJSON(w, http.StatusOK, remindersResponse{meta: meta{OK: true}, Result: res,
			Items: []ReminderItem{}})
		return
	}

	page, err := a.Reminders.Reminders(r.Context(), ReminderQuery{
		ProjectID: projectID,
		Window:    spec.Window,
		Status:    status,
		Kind:      kind,
		Channel:   channel,
		Limit:     limit,
		Cursor:    strings.TrimSpace(q.Get("cursor")),
	})
	if err != nil {
		mapErr(w, err)
		return
	}

	completeness := page.Completeness
	if completeness == "" {
		completeness = projection.CompletenessUnknown
	}
	items := page.Items
	if items == nil {
		items = []ReminderItem{}
	}
	res := newResult(capabilityOf("reminders", true, ""), scope, window, page.Watermark,
		completeness, nil, nil, nil)
	writeJSON(w, http.StatusOK, remindersResponse{
		meta:       meta{OK: true},
		Result:     res,
		Items:      items,
		NextCursor: cursorOrNil(page.Next),
		Count:      len(items),
		Unhandled:  page.Unhandled,
		Counts:     page.Counts,
	})
}

// handleAck 记录用户确认（F33 的「按身份确认已处理」）。
//
// 它是一层薄适配：去重、尝试收尾、重复确认的幂等性都由 internal/notifications
// 负责；HTTP 层只做幂等键校验、归属校验与结果投影。
func (a *API) handleAck(w http.ResponseWriter, r *http.Request) {
	if _, err := requireIdempotencyKey(r); err != nil {
		mapErr(w, err)
		return
	}
	projectID := strings.TrimSpace(r.PathValue("project_id"))
	notificationID := strings.TrimSpace(r.PathValue("notification_id"))
	if projectID == "" || notificationID == "" {
		mapErr(w, &ErrNotFound{Resource: "notification", Message: "缺少提醒身份"})
		return
	}
	if a.Reminders == nil {
		mapErr(w, &ErrForbidden{Code: codeCapabilityOff, Message: "提醒能力未配置"})
		return
	}

	result, err := a.Reminders.Ack(r.Context(), projectID, notificationID, a.now())
	if err != nil {
		mapErr(w, err)
		return
	}
	// 「没改动」等价于幂等重放：确认接口没有其它能解释「没改动」的分支，
	// 所以 replay 直接由 changed 反推，避免数据源漏填时两个字段互相矛盾。
	replay := result.IdempotentReplay || !result.Changed
	writeJSON(w, http.StatusOK, ackResponse{
		meta:              meta{OK: true},
		Notification:      result.Notification,
		Changed:           result.Changed,
		IdempotentReplay:  replay,
		ChangedTaskStatus: false,
	})
}

// parseReminderStatus 校验提醒状态过滤；取值必须落在 notification_deliveries 的
// CHECK 集合里——多一个值就是 schema 违约，不是本层能扩的枚举。
func parseReminderStatus(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !notifications.Status(raw).Valid() {
		return "", invalidField(codeUnknownEnum, "status", "未知提醒状态 "+strconv.Quote(raw))
	}
	return raw, nil
}

// parseReminderKind 校验提醒类型过滤。
func parseReminderKind(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !notifications.Kind(raw).Valid() {
		return "", invalidField(codeUnknownEnum, "kind", "未知提醒类型 "+strconv.Quote(raw))
	}
	return raw, nil
}

// parseReminderChannel 校验渠道过滤。
func parseReminderChannel(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !notifications.Channel(raw).Valid() {
		return "", invalidField(codeUnknownEnum, "channel", "未知提醒渠道 "+strconv.Quote(raw))
	}
	return raw, nil
}
