package observability

import (
	"context"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/engine/model"
)

// fakeSystemHealth 记录入参并返回固定报表。
type fakeSystemHealth struct {
	page    SystemHealthPage
	err     error
	lastQ   SystemHealthQuery
	lastCtx context.Context
	calls   int
}

func (f *fakeSystemHealth) SystemHealth(ctx context.Context, q SystemHealthQuery) (SystemHealthPage, error) {
	f.lastQ = q
	f.lastCtx = ctx
	f.calls++
	return f.page, f.err
}

// newHealthPage 造一份最小报表：作业总数 10、心跳分母 0（unknown）。
func newHealthPage(cursor int64) SystemHealthPage {
	total := int64(10)
	return SystemHealthPage{
		Report: &analytics.SystemHealthReport{
			GeneratedAt: fixedNow,
			Jobs: analytics.SystemJobHealth{
				Total: &total,
				SuccessRate: analytics.RatioMetric{
					Numerator: 5, Denominator: 7, Quality: model.QualityObserved,
				},
			},
			Agents: analytics.AgentHealth{
				NodeSuccessRate: analytics.RatioMetric{Quality: model.QualityUnknown},
			},
		},
		Watermark: watermarkOf(cursor, cursor, fixedNow, nil),
	}
}

func TestSystemHealthGlobalRoute(t *testing.T) {
	h := newHarness(t)
	h.api.SystemHealth = &fakeSystemHealth{page: newHealthPage(42)}

	rec, body := h.do(t, "GET",
		"/api/v1/system/health?start=2026-10-23T00:00:00Z&end=2026-10-24T00:00:00Z",
		h.read, nil)
	if rec.Code != 200 {
		t.Fatalf("状态码 = %d，期望 200：%s", rec.Code, rec.Body.String())
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("ok = %v", body["ok"])
	}
	result := body
	if cursor, _ := result["watermark"].(map[string]any)["cursor"].(float64); cursor != 42 {
		t.Fatalf("watermark.cursor = %v，期望 42", result["watermark"])
	}
	window, _ := result["window"].(map[string]any)
	if window["end"] != "2026-10-24T00:00:00Z" {
		t.Fatalf("window.end = %v", window["end"])
	}
	scope, _ := result["scope"].(map[string]any)
	if _, present := scope["project_id"]; present {
		t.Fatalf("全局作用域不应回显 project_id：%v", scope)
	}
	health, _ := body["health"].(map[string]any)
	jobs, _ := health["jobs"].(map[string]any)
	if jobs["total"] != float64(10) {
		t.Fatalf("health.jobs.total = %v，期望 10", jobs["total"])
	}
	// 节点维度缺失要在 missing 里显式说明，而不是静默丢指标。
	missing, _ := result["missing"].([]any)
	if len(missing) != 1 {
		t.Fatalf("missing = %v，期望 1 条节点归属缺失", result["missing"])
	}
}

func TestSystemHealthProjectScope(t *testing.T) {
	h := newHarness(t)
	fake := &fakeSystemHealth{page: newHealthPage(7)}
	h.api.SystemHealth = fake

	rec, body := h.do(t, "GET", "/api/v1/projects/p1/system/health", h.read, nil)
	if rec.Code != 200 {
		t.Fatalf("状态码 = %d，期望 200：%s", rec.Code, rec.Body.String())
	}
	if fake.lastQ.ProjectID != "p1" {
		t.Fatalf("下传 project_id = %q，期望 p1", fake.lastQ.ProjectID)
	}
	scope, _ := body["scope"].(map[string]any)
	if scope["project_id"] != "p1" {
		t.Fatalf("scope.project_id = %v，期望 p1", scope["project_id"])
	}
	// 未传 start/end 时必须走默认窗口，并标记 defaulted。
	window, _ := body["window"].(map[string]any)
	if window["defaulted"] != true {
		t.Fatalf("window.defaulted = %v，期望 true", window["defaulted"])
	}
}

func TestSystemHealthInvalidWindow(t *testing.T) {
	h := newHarness(t)
	h.api.SystemHealth = &fakeSystemHealth{page: newHealthPage(1)}

	rec, body := h.do(t, "GET",
		"/api/v1/system/health?start=2026-10-24T00:00:00Z&end=2026-10-23T00:00:00Z",
		h.read, nil)
	if rec.Code != 422 {
		t.Fatalf("状态码 = %d，期望 422：%s", rec.Code, rec.Body.String())
	}
	if code := errCode(body); code != codeInvalidWindow {
		t.Fatalf("错误码 = %q，期望 %q", code, codeInvalidWindow)
	}
}

func TestSystemHealthUnsupportedWhenNoSource(t *testing.T) {
	h := newHarness(t)

	rec, body := h.do(t, "GET", "/api/v1/system/health", h.read, nil)
	if rec.Code != 200 {
		t.Fatalf("状态码 = %d，期望 200：%s", rec.Code, rec.Body.String())
	}
	unsupported, _ := body["unsupported"].([]any)
	if len(unsupported) != 1 {
		t.Fatalf("unsupported = %v，期望 1 条", body["unsupported"])
	}
	if _, present := body["health"]; present && body["health"] != nil {
		t.Fatalf("无数据源时不应返回 health：%v", body["health"])
	}
}

func TestSystemHealthRequiresReadScope(t *testing.T) {
	h := newHarness(t)
	h.api.SystemHealth = &fakeSystemHealth{page: newHealthPage(1)}

	rec, _ := h.do(t, "GET", "/api/v1/system/health", "", nil)
	if rec.Code != 401 {
		t.Fatalf("无令牌状态码 = %d，期望 401", rec.Code)
	}
}

// 固定时钟不会随测试运行漂移；这里顺带断言 fake 收到的窗口是 UTC。
func TestSystemHealthWindowIsUTC(t *testing.T) {
	h := newHarness(t)
	fake := &fakeSystemHealth{page: newHealthPage(1)}
	h.api.SystemHealth = fake

	_, _ = h.do(t, "GET", "/api/v1/system/health?tz=Asia/Shanghai", h.read, nil)
	if fake.lastQ.Window.End.Location() != time.UTC {
		t.Fatalf("窗口未归一为 UTC：%v", fake.lastQ.Window.End)
	}
}
