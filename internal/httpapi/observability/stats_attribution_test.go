package observability

import (
	"context"
	"net/http"
	"testing"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/projection"
)

// fakeAttributionStats 同时实现 Report 与 Attribution：归属查询是可选的第二能力。
type fakeAttributionStats struct {
	fakeStats
	attribution       *analytics.AttributionReport
	lastDimension     analytics.Dimension
	attributionCalled int
}

func (f *fakeAttributionStats) Attribution(_ context.Context, q AttributionQuery,
	_ TimelineData) (*analytics.AttributionReport, error) {
	f.attributionCalled++
	f.lastDimension = q.Dimension
	if f.attribution != nil {
		return f.attribution, nil
	}
	return &analytics.AttributionReport{Dimension: q.Dimension}, nil
}

// attributionSample 造一份最小归属报表：分母为 1/2，成本未知但有依据。
func attributionSample() *analytics.AttributionReport {
	ratio := 0.5
	return &analytics.AttributionReport{
		Dimension: analytics.DimensionNode,
		Totals: analytics.AttributionTotals{
			Calls:        analytics.Denominator{Numerator: 1, Denominator: 2, Ratio: &ratio},
			UsageUnknown: false,
			Cost:         analytics.CostEstimate{Basis: "未配置价目表"},
		},
		Rollups: []analytics.AttributionRollup{{
			Dimension: analytics.DimensionNode,
			Value:     "node_pi",
			Turns:     2,
			UsageObserved: analytics.Denominator{
				Numerator: 1, Denominator: 2, Ratio: &ratio,
			},
		}},
	}
}

// TestStatsHandlerServesAttributionByDimension 说明 API 可按维度查归集：
// 给了 dimension 才返回归集，没给就不改既有报表形状。
func TestStatsHandlerServesAttributionByDimension(t *testing.T) {
	h := newHarness(t)
	stats := &fakeAttributionStats{fakeStats: fakeStats{report: &analytics.Report{
		Completeness: "complete",
	}}}
	stats.attribution = attributionSample()
	h.api.Stats = stats
	h.api.Timeline = &fakeTimeline{data: TimelineData{
		Cursors: projection.Cursors{StateCursor: 10, ProjectionCursor: 10,
			Completeness: projection.CompletenessComplete},
	}}

	rec, body := h.do(t, http.MethodGet, "/api/v1/analytics?range=7d&dimension=node", h.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d body=%v", rec.Code, body)
	}
	attribution, ok := body["attribution"].(map[string]any)
	if !ok {
		t.Fatalf("应返回 attribution: %v", body)
	}
	if attribution["dimension"] != "node" {
		t.Fatalf("维度回显=%v", attribution["dimension"])
	}
	if stats.lastDimension != analytics.DimensionNode {
		t.Fatalf("适配器收到的维度=%q", stats.lastDimension)
	}
	rollups, ok := attribution["rollups"].([]any)
	if !ok || len(rollups) != 1 {
		t.Fatalf("归集条目=%v", attribution["rollups"])
	}

	// 不指定维度：既有报表形状不变，不多出 attribution 字段。
	rec, body = h.do(t, http.MethodGet, "/api/v1/analytics?range=7d", h.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d body=%v", rec.Code, body)
	}
	if _, ok := body["attribution"]; ok {
		t.Fatal("没有维度时不该返回归集")
	}
}

// TestStatsHandlerRejectsUnknownDimension 说明维度是封闭集合：乱传要明确拒绝。
func TestStatsHandlerRejectsUnknownDimension(t *testing.T) {
	h := newHarness(t)
	stats := &fakeAttributionStats{fakeStats: fakeStats{report: &analytics.Report{}}}
	h.api.Stats = stats
	rec, body := h.do(t, http.MethodGet, "/api/v1/analytics?range=7d&dimension=team", h.read, nil)
	if rec.Code == http.StatusOK {
		t.Fatalf("非法维度应被拒绝，得到 200：%v", body)
	}
	if stats.attributionCalled != 0 {
		t.Fatalf("非法维度不该下发到统计适配器，调用 %d 次", stats.attributionCalled)
	}
}
