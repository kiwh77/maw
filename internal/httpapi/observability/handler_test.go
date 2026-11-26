package observability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/projection"
)

// 固定时钟：所有窗口断言都基于它，避免测试在午夜前后摇摆。
var fixedNow = time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)

// ---- 测试替身 ----

type fakeTimeline struct {
	data      TimelineData
	err       error
	lastQuery TimelineQuery
	calls     int
}

func (f *fakeTimeline) Timeline(_ context.Context, q TimelineQuery) (TimelineData, error) {
	f.lastQuery = q
	f.calls++
	return f.data, f.err
}

type fakeStats struct {
	report   *analytics.Report
	err      error
	lastQ    StatsQuery
	lastData TimelineData
}

func (f *fakeStats) Report(_ context.Context, q StatsQuery, data TimelineData) (*analytics.Report, error) {
	f.lastQ = q
	f.lastData = data
	return f.report, f.err
}

type fakeSessions struct {
	page  SessionPage
	err   error
	lastQ SessionQuery
}

func (f *fakeSessions) Sessions(_ context.Context, q SessionQuery) (SessionPage, error) {
	f.lastQ = q
	return f.page, f.err
}

type fakeReminders struct {
	page      ReminderPage
	err       error
	ack       AckResult
	ackErr    error
	lastQ     ReminderQuery
	ackIDs    []string
	ackCalled int
}

func (f *fakeReminders) Reminders(_ context.Context, q ReminderQuery) (ReminderPage, error) {
	f.lastQ = q
	return f.page, f.err
}

func (f *fakeReminders) Ack(_ context.Context, projectID, id string, _ time.Time) (AckResult, error) {
	f.ackCalled++
	f.ackIDs = append(f.ackIDs, projectID+"/"+id)
	return f.ack, f.ackErr
}

type fakeChanges struct {
	ch chan ChangeSignal
}

func (f *fakeChanges) Subscribe(context.Context) (<-chan ChangeSignal, func()) {
	return f.ch, func() {}
}

// ---- 夹具 ----

type harness struct {
	api   *API
	store *localauth.TokenStore
	read  string
	write string
	admin string
}

// newHarness 组装带鉴权的处理器。测试通过真实中间件走一遍作用域判定，
// 而不是直接调用 handler：权限检查本身就是要验的行为。
func newHarness(t *testing.T) *harness {
	t.Helper()
	store, err := localauth.OpenTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatalf("打开令牌库失败：%v", err)
	}
	readTok, _, err := store.Mint("tester", localauth.ScopeRead)
	if err != nil {
		t.Fatalf("签发只读令牌失败：%v", err)
	}
	writeTok, _, err := store.Mint("tester", localauth.ScopeWrite)
	if err != nil {
		t.Fatalf("签发写令牌失败：%v", err)
	}
	adminTok, _, err := store.Mint("tester", localauth.ScopeAdmin)
	if err != nil {
		t.Fatalf("签发管理令牌失败：%v", err)
	}
	api := &API{
		Auth: &localauth.Authenticator{Tokens: store},
		Now:  func() time.Time { return fixedNow },
	}
	return &harness{api: api, store: store, read: readTok, write: writeTok, admin: adminTok}
}

// do 发一次请求；token 为空表示不带凭据。
func (h *harness) do(t *testing.T, method, path, token string, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.api.Handler().ServeHTTP(rec, req)
	body := map[string]any{}
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	return rec, body
}

// errCode 取出契约错误码。
func errCode(body map[string]any) string {
	e, ok := body["error"].(map[string]any)
	if !ok {
		return ""
	}
	code, _ := e["code"].(string)
	return code
}

// readPaths 是五个观测读接口：窗口、来源过滤与权限检查必须一致。
func readPaths() []string {
	return []string{
		"/api/v1/projects/p1/timeline",
		"/api/v1/projects/p1/activity",
		"/api/v1/projects/p1/sessions",
		"/api/v1/projects/p1/analytics",
		"/api/v1/projects/p1/notifications",
	}
}

// supportedHarness 让五个读接口都处于「能力可用」状态。
func supportedHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.api.Timeline = &fakeTimeline{data: TimelineData{
		Cursors: projection.Cursors{
			StateCursor: 10, ProjectionCursor: 10,
			Completeness: projection.CompletenessComplete,
		},
	}}
	h.api.Stats = &fakeStats{report: &analytics.Report{
		Completeness: projection.CompletenessComplete,
		Watermark:    analytics.Watermark{Cursor: 10, CompleteBefore: 10},
	}}
	h.api.Sessions = &fakeSessions{page: SessionPage{
		Completeness: projection.CompletenessComplete,
	}}
	h.api.Reminders = &fakeReminders{page: ReminderPage{
		Completeness: projection.CompletenessComplete,
	}}
	return h
}

// TestReadEndpointsShareWindowSourceAndPermission 覆盖验收第 1 条：
// 时间轴、热图、会话、统计与提醒使用统一窗口、来源过滤和权限检查。
func TestReadEndpointsShareWindowSourceAndPermission(t *testing.T) {
	h := supportedHarness(t)

	for _, path := range readPaths() {
		t.Run(strings.TrimPrefix(path, "/api/v1/projects/p1/"), func(t *testing.T) {
			// 1) 无凭据：只能 401。
			rec, body := h.do(t, http.MethodGet, path, "", nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("无凭据应 401，得到 %d（%s）", rec.Code, rec.Body.String())
			}
			if code := errCode(body); code != "unauthenticated" {
				t.Fatalf("无凭据错误码应为 unauthenticated，得到 %q", code)
			}

			// 2) 窗口不成对：422 invalid_window。
			rec, body = h.do(t, http.MethodGet, path+"?start=2026-10-23T00:00:00Z", h.read, nil)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("半截窗口应 422，得到 %d（%s）", rec.Code, rec.Body.String())
			}
			if code := errCode(body); code != codeInvalidWindow {
				t.Fatalf("半截窗口错误码应为 %s，得到 %q", codeInvalidWindow, code)
			}

			// 3) 非法时区：422 invalid_timezone。
			rec, body = h.do(t, http.MethodGet, path+"?tz=Mars/Olympus", h.read, nil)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("非法时区应 422，得到 %d（%s）", rec.Code, rec.Body.String())
			}
			if code := errCode(body); code != codeInvalidTimezone {
				t.Fatalf("非法时区错误码应为 %s，得到 %q", codeInvalidTimezone, code)
			}

			// 4) offset 分页：422 invalid_pagination。
			rec, body = h.do(t, http.MethodGet, path+"?offset=20", h.read, nil)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("offset 应 422，得到 %d（%s）", rec.Code, rec.Body.String())
			}
			if code := errCode(body); code != "invalid_pagination" {
				t.Fatalf("offset 错误码应为 invalid_pagination，得到 %q", code)
			}

			// 5) 合法读：200，且回显统一窗口与来源过滤。
			rec, body = h.do(t, http.MethodGet,
				path+"?start=2026-10-23T00:00:00Z&end=2026-10-24T00:00:00Z&tz=Asia/Shanghai&source_ns=claude",
				h.read, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("合法读应 200，得到 %d（%s）", rec.Code, rec.Body.String())
			}
			window, ok := body["window"].(map[string]any)
			if !ok {
				t.Fatalf("响应必须回显 window：%s", rec.Body.String())
			}
			if window["timezone"] != "Asia/Shanghai" {
				t.Fatalf("window.timezone 应回显 Asia/Shanghai，得到 %v", window["timezone"])
			}
			if window["start"] != "2026-10-23T00:00:00Z" || window["end"] != "2026-10-24T00:00:00Z" {
				t.Fatalf("window 起止未按请求回显：%v", window)
			}
			scope, ok := body["scope"].(map[string]any)
			if !ok || scope["source_ns"] != "claude" {
				t.Fatalf("scope.source_ns 应回显 claude：%v", body["scope"])
			}
			if body["status"] == nil || body["completeness"] == nil {
				t.Fatalf("响应必须带类型化 status 与 completeness：%s", rec.Body.String())
			}
		})
	}
}

// TestWriteEndpointRequiresWriteScope 覆盖权限检查的另一半：确认提醒是写动作。
func TestWriteEndpointRequiresWriteScope(t *testing.T) {
	h := supportedHarness(t)
	path := "/api/v1/projects/p1/notifications/ntf_1/acks"
	headers := map[string]string{"Idempotency-Key": "0123456789abcdef"}

	rec, body := h.do(t, http.MethodPost, path, h.read, headers)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("只读令牌确认提醒应 403，得到 %d（%s）", rec.Code, rec.Body.String())
	}
	if code := errCode(body); code != "insufficient_scope" {
		t.Fatalf("错误码应为 insufficient_scope，得到 %q", code)
	}
	if h.api.Reminders.(*fakeReminders).ackCalled != 0 {
		t.Fatal("被拒的请求不得触达用例")
	}
}

// TestAckRequiresIdempotencyKeyAndReportsReplay 覆盖确认提醒的幂等与状态归属。
func TestAckRequiresIdempotencyKeyAndReportsReplay(t *testing.T) {
	h := supportedHarness(t)
	fake := h.api.Reminders.(*fakeReminders)
	fake.ack = AckResult{
		Notification: &ReminderItem{NotificationID: "ntf_1", Status: "acked"},
		Changed:      true,
	}
	path := "/api/v1/projects/p1/notifications/ntf_1/acks"

	// 缺幂等键：422（契约 §4：所有 POST 必带）。
	rec, body := h.do(t, http.MethodPost, path, h.write, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("缺 Idempotency-Key 应 422，得到 %d（%s）", rec.Code, rec.Body.String())
	}
	if code := errCode(body); code != "invalid_idempotency_key" {
		t.Fatalf("错误码应为 invalid_idempotency_key，得到 %q", code)
	}

	// 第一次确认：changed=true，且明确声明没改任务状态。
	rec, body = h.do(t, http.MethodPost, path, h.write,
		map[string]string{"Idempotency-Key": "0123456789abcdef"})
	if rec.Code != http.StatusOK {
		t.Fatalf("确认应 200，得到 %d（%s）", rec.Code, rec.Body.String())
	}
	if body["changed"] != true || body["changed_task_status"] != false {
		t.Fatalf("首次确认应 changed=true／changed_task_status=false：%s", rec.Body.String())
	}

	// 第二次确认：幂等重放，不是失败。
	fake.ack = AckResult{Notification: &ReminderItem{NotificationID: "ntf_1", Status: "acked"}}
	rec, body = h.do(t, http.MethodPost, path, h.write,
		map[string]string{"Idempotency-Key": "fedcba9876543210"})
	if rec.Code != http.StatusOK {
		t.Fatalf("重复确认应 200，得到 %d（%s）", rec.Code, rec.Body.String())
	}
	if body["changed"] != false || body["idempotent_replay"] != true {
		t.Fatalf("重复确认应 changed=false／idempotent_replay=true：%s", rec.Body.String())
	}
	if len(fake.ackIDs) != 2 || fake.ackIDs[0] != "p1/ntf_1" {
		t.Fatalf("确认必须带上工程与提醒身份：%v", fake.ackIDs)
	}
}

// TestTimelineTypedResults 覆盖验收第 2 条：缺测、投影落后、不支持能力都是类型化结果。
func TestTimelineTypedResults(t *testing.T) {
	base := func() *harness { return supportedHarness(t) }
	path := "/api/v1/projects/p1/timeline?start=2026-10-23T00:00:00Z&end=2026-10-24T00:00:00Z"

	t.Run("投影落后", func(t *testing.T) {
		h := base()
		h.api.Timeline.(*fakeTimeline).data = TimelineData{
			Cursors: projection.Cursors{
				StateCursor: 30, ProjectionCursor: 20,
				Completeness: projection.CompletenessComplete,
			},
		}
		rec, body := h.do(t, http.MethodGet, path, h.read, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("落后不是错误，应 200，得到 %d", rec.Code)
		}
		if body["status"] != string(StatusStale) {
			t.Fatalf("status 应为 stale，得到 %v", body["status"])
		}
		lag, ok := body["projection_lag"].(map[string]any)
		if !ok || lag["pending"] != float64(10) {
			t.Fatalf("projection_lag.pending 应为 10：%v", body["projection_lag"])
		}
	})

	t.Run("采集缺口", func(t *testing.T) {
		h := base()
		h.api.Timeline.(*fakeTimeline).data = TimelineData{
			Cursors: projection.Cursors{
				StateCursor: 10, ProjectionCursor: 10,
				Completeness: projection.CompletenessComplete,
			},
			Gaps: []Gap{{From: 4, To: 6, Reason: "collector_offline"}},
		}
		_, body := h.do(t, http.MethodGet, path, h.read, nil)
		// 有缺口就不得报 complete：完整性必须降为 partial。
		if body["completeness"] != string(projection.CompletenessPartial) {
			t.Fatalf("有缺口时 completeness 应为 partial，得到 %v", body["completeness"])
		}
		if body["status"] != string(StatusPartial) {
			t.Fatalf("status 应为 partial，得到 %v", body["status"])
		}
		missing, _ := body["missing"].([]any)
		if len(missing) == 0 {
			t.Fatal("缺口必须计入 missing，不能悄悄从分母剔除")
		}
		gaps, _ := body["gaps"].([]any)
		if len(gaps) != 1 {
			t.Fatalf("gaps 应回显一条，得到 %v", body["gaps"])
		}
	})

	t.Run("完整性未知", func(t *testing.T) {
		h := base()
		h.api.Timeline.(*fakeTimeline).data = TimelineData{
			Cursors: projection.Cursors{Completeness: projection.CompletenessUnknown},
		}
		_, body := h.do(t, http.MethodGet, path, h.read, nil)
		if body["status"] != string(StatusUnknown) {
			t.Fatalf("status 应为 unknown，得到 %v", body["status"])
		}
	})

	t.Run("能力未接线", func(t *testing.T) {
		h := base()
		h.api.Timeline = nil
		rec, body := h.do(t, http.MethodGet, path, h.read, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("能力缺失应是类型化结果而不是 5xx，得到 %d", rec.Code)
		}
		if body["status"] != string(StatusUnsupported) {
			t.Fatalf("status 应为 unsupported，得到 %v", body["status"])
		}
		capability, _ := body["capability"].(map[string]any)
		if capability["supported"] != false {
			t.Fatalf("capability.supported 应为 false：%v", body["capability"])
		}
		items, _ := body["items"].([]any)
		if len(items) != 0 {
			t.Fatal("能力缺失时不得返回数据")
		}
	})

	t.Run("来源过滤不支持", func(t *testing.T) {
		h := base()
		h.api.Timeline.(*fakeTimeline).data = TimelineData{
			Unsupported: []Unsupported{{
				Capability: "timeline.source_filter", Reason: "source_filter_not_configured",
			}},
		}
		_, body := h.do(t, http.MethodGet, path+"&source_ns=claude", h.read, nil)
		if body["status"] != string(StatusUnsupported) {
			t.Fatalf("过滤条件无法满足时应报 unsupported，得到 %v", body["status"])
		}
		unsupported, _ := body["unsupported"].([]any)
		if len(unsupported) == 0 {
			t.Fatal("必须给出 unsupported 原因")
		}
		first, _ := unsupported[0].(map[string]any)
		if first["capability"] != "timeline.source_filter" {
			t.Fatalf("unsupported.capability 不符：%v", first)
		}
	})

	t.Run("统计缺测", func(t *testing.T) {
		h := base()
		h.api.Stats = nil
		_, body := h.do(t, http.MethodGet, path, h.read, nil)
		missing, _ := body["missing"].([]any)
		found := false
		for _, m := range missing {
			entry, _ := m.(map[string]any)
			if entry["section"] == "totals" {
				found = true
			}
		}
		if !found {
			t.Fatalf("缺统计源时必须显式说明 totals 缺测：%v", body["missing"])
		}
	})
}

// TestTimelinePaginationIsStable 覆盖游标分页：不重不漏，排序键含身份。
func TestTimelinePaginationIsStable(t *testing.T) {
	h := supportedHarness(t)
	at := time.Date(2026, 10, 23, 1, 0, 0, 0, time.UTC)
	end := at.Add(time.Hour)
	mk := func(id string, start time.Time) projection.Interval {
		e := start.Add(10 * time.Minute)
		return projection.Interval{
			IntervalID: id, Kind: projection.KindTaskPhase, InstanceID: "ti_a",
			StartAt: start, EndAt: &e, Quality: model.QualityObserved,
		}
	}
	h.api.Timeline.(*fakeTimeline).data = TimelineData{
		Cursors: projection.Cursors{StateCursor: 3, ProjectionCursor: 3,
			Completeness: projection.CompletenessComplete},
		Intervals: []projection.Interval{
			mk("iv_c", at.Add(40*time.Minute)),
			mk("iv_a", at),
			mk("iv_b", at.Add(20*time.Minute)),
		},
	}
	base := "/api/v1/projects/p1/timeline?start=" + at.Format(time.RFC3339) +
		"&end=" + end.Format(time.RFC3339) + "&limit=2"

	rec, body := h.do(t, http.MethodGet, base, h.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("首页应 200，得到 %d（%s）", rec.Code, rec.Body.String())
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("首页应有 2 条，得到 %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["interval_id"] != "iv_a" {
		t.Fatalf("排序应按 start_at 升序，首条应为 iv_a：%v", first["interval_id"])
	}
	next, _ := body["next_cursor"].(string)
	if next == "" {
		t.Fatal("还有下一页时 next_cursor 必须非空")
	}

	_, body = h.do(t, http.MethodGet, base+"&cursor="+next, h.read, nil)
	items, _ = body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("第二页应有 1 条，得到 %d", len(items))
	}
	last, _ := items[0].(map[string]any)
	if last["interval_id"] != "iv_c" {
		t.Fatalf("第二页应为 iv_c，得到 %v", last["interval_id"])
	}
	if body["next_cursor"] != nil {
		t.Fatalf("最后一页 next_cursor 必须为 null，得到 %v", body["next_cursor"])
	}
}

// TestActivityMissingHours 覆盖热图的缺测语义：没有桶且不完整 ≠ 全天零活动。
func TestActivityMissingHours(t *testing.T) {
	h := supportedHarness(t)
	h.api.Timeline.(*fakeTimeline).data = TimelineData{
		Cursors: projection.Cursors{StateCursor: 5, ProjectionCursor: 5,
			Completeness: projection.CompletenessPartial},
	}
	h.api.Stats.(*fakeStats).report = &analytics.Report{
		Completeness: projection.CompletenessPartial,
		Timeline:     analytics.TimelineSummary{},
	}
	rec, body := h.do(t, http.MethodGet,
		"/api/v1/projects/p1/activity?day=2026-10-23&tz=Asia/Shanghai", h.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，得到 %d（%s）", rec.Code, rec.Body.String())
	}
	if body["day"] != "2026-10-23" {
		t.Fatalf("day 应回显 2026-10-23，得到 %v", body["day"])
	}
	if body["present_hours"] != float64(0) {
		t.Fatalf("present_hours 应为 0，得到 %v", body["present_hours"])
	}
	missing, _ := body["missing"].([]any)
	found := false
	for _, m := range missing {
		entry, _ := m.(map[string]any)
		if entry["section"] == "hours" {
			found = true
		}
	}
	if !found {
		t.Fatalf("没有小时桶且不完整时必须标 hours 缺测：%v", body["missing"])
	}

	// 热图只接受单日：超过 24 小时直接 422。
	rec, _ = h.do(t, http.MethodGet,
		"/api/v1/projects/p1/activity?start=2026-10-20T00:00:00Z&end=2026-10-24T00:00:00Z", h.read, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("多日热图应 422，得到 %d", rec.Code)
	}
}

// TestSessionsPassesUnifiedWindowAndSource 验证会话接口吃到的是同一套解析结果。
func TestSessionsPassesUnifiedWindowAndSource(t *testing.T) {
	h := supportedHarness(t)
	rec, body := h.do(t, http.MethodGet,
		"/api/v1/projects/p1/sessions?day=2026-10-23&tz=Asia/Shanghai&source_ns=cursor&limit=10",
		h.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，得到 %d（%s）", rec.Code, rec.Body.String())
	}
	q := h.api.Sessions.(*fakeSessions).lastQ
	if q.ProjectID != "p1" || q.SourceNS != "cursor" || q.Limit != 10 {
		t.Fatalf("会话查询未带上工程／来源／分页：%+v", q)
	}
	wantStart := time.Date(2026, 10, 22, 16, 0, 0, 0, time.UTC) // 本地零点
	if !q.Window.Start.Equal(wantStart) {
		t.Fatalf("day 应按 Asia/Shanghai 切日，start=%v", q.Window.Start)
	}
	if q.Window.End.Sub(q.Window.Start) != 24*time.Hour {
		t.Fatalf("day 窗口应为 24 小时，得到 %v", q.Window.End.Sub(q.Window.Start))
	}
	if body["next_cursor"] != nil {
		t.Fatalf("空页 next_cursor 应为 null：%v", body["next_cursor"])
	}
}

// TestRemindersSourceFilterIsTypedUnsupported 验证「提醒没有来源维度」不是静默忽略。
func TestRemindersSourceFilterIsTypedUnsupported(t *testing.T) {
	h := supportedHarness(t)
	rec, body := h.do(t, http.MethodGet,
		"/api/v1/projects/p1/notifications?source_ns=claude", h.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，得到 %d（%s）", rec.Code, rec.Body.String())
	}
	if body["status"] != string(StatusUnsupported) {
		t.Fatalf("status 应为 unsupported，得到 %v", body["status"])
	}
	if h.api.Reminders.(*fakeReminders).lastQ.ProjectID != "" {
		t.Fatal("不支持过滤时不得回落到全量查询")
	}
	// 未知状态词必须是 422 unknown_enum，不能当成零匹配。
	rec, body = h.do(t, http.MethodGet,
		"/api/v1/projects/p1/notifications?status=bogus", h.read, nil)
	if rec.Code != http.StatusUnprocessableEntity || errCode(body) != codeUnknownEnum {
		t.Fatalf("未知状态应 422 unknown_enum，得到 %d %q", rec.Code, errCode(body))
	}
}

// TestStreamCarriesOnlyInvalidation 覆盖验收第 2 条后半：
// SSE 只做失效通知，查询可恢复事实。
func TestStreamCarriesOnlyInvalidation(t *testing.T) {
	h := supportedHarness(t)
	// 通道先装一条信号再关闭：handler 写出这一帧后因通道关闭而退出，
	// 测试不必依赖超时或上下文取消来收尾。
	ch := make(chan ChangeSignal, 1)
	ch <- ChangeSignal{
		Watermark:    &Watermark{Cursor: 42, CompleteBefore: 40, Gaps: []Gap{}},
		Revision:     7,
		At:           fixedNow,
		Capabilities: []string{"timeline", "stats"},
	}
	close(ch)
	h.api.Changes = &fakeChanges{ch: ch}

	rec, _ := h.do(t, http.MethodGet, "/api/v1/projects/p1/observability/stream", h.read, nil)
	body := rec.Body.String()

	if !strings.Contains(body, "event: ready") {
		t.Fatalf("连接就绪帧缺失：%s", body)
	}
	if !strings.Contains(body, "event: invalidate") {
		t.Fatalf("失效帧缺失：%s", body)
	}
	if !strings.Contains(body, `"cursor":42`) {
		t.Fatalf("失效帧必须带水位：%s", body)
	}
	// 只允许失效信号：事实正文一律不得出现在帧里。
	for _, forbidden := range []string{"interval_id", "notification_id", "start_at", "input_tokens"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("SSE 不得承载事实字段 %q：%s", forbidden, body)
		}
	}
}

// TestStreamUnavailableIsNotSilent 验证没有失效源时不会假装连上。
func TestStreamUnavailableIsNotSilent(t *testing.T) {
	h := supportedHarness(t)
	rec, body := h.do(t, http.MethodGet,
		"/api/v1/projects/p1/observability/stream", h.read, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("无失效源应 503，得到 %d", rec.Code)
	}
	if errCode(body) != "stream_unavailable" {
		t.Fatalf("错误码应为 stream_unavailable，得到 %q", errCode(body))
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("503 必须带 Retry-After，客户端据此退回轮询")
	}
}

// TestCapabilitiesReflectConfiguredSources 验证能力发现接口与装配一致。
func TestCapabilitiesReflectConfiguredSources(t *testing.T) {
	h := supportedHarness(t)
	h.api.Changes = &fakeChanges{ch: make(chan ChangeSignal)}
	rec, body := h.do(t, http.MethodGet,
		"/api/v1/projects/p1/observability/capabilities", h.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，得到 %d", rec.Code)
	}
	list, _ := body["capabilities"].([]any)
	if len(list) == 0 {
		t.Fatal("能力清单不得为空")
	}
	for _, entry := range list {
		c, _ := entry.(map[string]any)
		if c["supported"] != true {
			t.Fatalf("全部数据源已注入，能力应可用：%v", c)
		}
	}

	h.api.Timeline = nil
	_, body = h.do(t, http.MethodGet,
		"/api/v1/projects/p1/observability/capabilities", h.read, nil)
	list, _ = body["capabilities"].([]any)
	for _, entry := range list {
		c, _ := entry.(map[string]any)
		if c["name"] == "timeline" && c["supported"] != false {
			t.Fatalf("时间轴源已移除，capability 应为 false：%v", c)
		}
	}
}

// TestMapErrDoesNotLeakInternals 验证兜底错误不回显内部细节。
func TestMapErrDoesNotLeakInternals(t *testing.T) {
	h := supportedHarness(t)
	h.api.Sessions = &fakeSessions{err: errors.New("sql: SELECT * FROM secret_table")}
	rec, body := h.do(t, http.MethodGet, "/api/v1/projects/p1/sessions", h.read, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("内部错误应 500，得到 %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret_table") {
		t.Fatalf("错误体不得回显内部细节：%s", rec.Body.String())
	}
	if errCode(body) != "internal_error" {
		t.Fatalf("错误码应为 internal_error，得到 %q", errCode(body))
	}
}
