package analytics

import (
	"database/sql"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/projection"
)

// attrWindow 是归属统计用的固定窗口（UTC），与种子数据对齐。
func attrWindow() projection.Window {
	return projection.Window{
		Start: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC),
	}
}

// seedAttribution 种一批带归属的用量：两次调用（一个未知用量）、一次被取代的 run 级汇总。
func seedAttribution(t *testing.T, f *fixture) {
	t.Helper()
	f.write(t, func(tx *sql.Tx) error {
		if err := db.UpsertProject(tx, &db.Project{
			ID: "p1", Name: "proj", Path: t.TempDir(), MainBranch: "main",
		}); err != nil {
			return err
		}
		for _, session := range []string{"pi:s1"} {
			if err := db.UpsertSourceSession(tx, db.SourceSession{
				SessionID: session, ProjectID: "p1", SourceNS: "pi", ExternalID: session,
			}); err != nil {
				return err
			}
		}
		turns := []struct{ id, call, purpose string }{
			{"pi:s1:call:pc_1", "pc_1", "worker"},
			{"pi:s1:call:pc_2", "pc_2", "worker"},
		}
		for _, tr := range turns {
			if err := db.UpsertSourceTurn(tx, db.SourceTurn{
				TurnID: tr.id, SourceNS: "pi", SessionID: "pi:s1",
				StartedAt: "2026-11-18T02:00:00Z", ExternalID: tr.call,
			}); err != nil {
				return err
			}
			if err := db.EnsureRunSourceLink(tx, db.RunSourceLink{
				LinkID: "link_" + tr.call, ProjectID: "p1", RunID: "run_a",
				TurnID: tr.id, CallID: tr.call, NodeID: "node_pi", Generation: 1, Attributed: true,
			}); err != nil {
				return err
			}
		}
		in, out := int64(100), int64(20)
		if err := db.PutUsageRecord(tx, db.UsageRecord{
			UsageID: "u1", SourceNS: "pi", TurnID: "pi:s1:call:pc_1", Epoch: 1,
			Scope: db.UsageScopeCall, Semantic: db.UsageDelta,
			InputTokens: &in, OutputTokens: &out, Quality: model.QualityObserved,
			ProjectID: "p1", NodeID: "node_pi", RunID: "run_a", SessionID: "pi:s1",
			CallID: "pc_1", Purpose: "worker", Model: "claude-x", Provider: "anthropic",
			RecordedAt: "2026-11-18T02:10:00Z",
		}); err != nil {
			return err
		}
		// 第二次调用：来源没给 token（未知用量，不填 0）。
		if err := db.PutUsageRecord(tx, db.UsageRecord{
			UsageID: "u2", SourceNS: "pi", TurnID: "pi:s1:call:pc_2", Epoch: 1,
			Scope: db.UsageScopeCall, Semantic: db.UsageDelta, Quality: model.QualityUnknown,
			ProjectID: "p1", NodeID: "node_pi", RunID: "run_a", SessionID: "pi:s1",
			CallID: "pc_2", Purpose: "worker", Model: "claude-x", Provider: "anthropic",
			RecordedAt: "2026-11-18T02:20:00Z",
		}); err != nil {
			return err
		}
		// 运行级汇总：已被 call 级取代，不参与汇总。
		runIn, runOut := int64(999), int64(888)
		if err := db.PutUsageRecord(tx, db.UsageRecord{
			UsageID: "u_run", SourceNS: "pi", TurnID: "pi:s1:run:run_a", Epoch: 1,
			Scope: db.UsageScopeRun, Semantic: db.UsageDelta,
			InputTokens: &runIn, OutputTokens: &runOut, Quality: model.QualityObserved,
			ProjectID: "p1", NodeID: "node_pi", RunID: "run_a", Purpose: "worker",
			Model: "claude-x", Superseded: true, Baseline: true,
			RecordedAt: "2026-11-18T02:30:00Z",
		}); err != nil {
			return err
		}
		// 一个已完成的 Run 与一次通过的交付。
		if _, err := tx.Exec(`INSERT INTO runs(run_id, project_id, instance_id, version_no, generation,
  phase, reconciliation_state, created_at, updated_at, started_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`, "run_a", "p1", "ti_1", 1, 1, "completed", "confirmed",
			"2026-11-18T01:59:00Z", "2026-11-18T02:40:00Z", "2026-11-18T02:00:00Z"); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO deliveries(delivery_id, project_id, instance_id, version_no,
  run_id, mode, baseline_commit, candidate_commit, delivered_at, manifest_json, fingerprint,
  idempotency_key, request_digest, created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "d_1", "p1", "ti_1", 1, "run_a", "code",
			"base", "cand", "2026-11-18T02:45:00Z", "{}", "fp", "ik", "rd", "2026-11-18T02:45:00Z"); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO delivery_verifications(verification_id, delivery_id, result,
  target_version, fingerprint, ran_at, verification_json)
VALUES (?,?,?,?,?,?,?)`, "v_1", "d_1", "pass", 1, "fp", "2026-11-18T02:46:00Z", "{}")
		return err
	})
}

// TestAttributionRollupsUseExplicitDenominators 说明归集把分子分母都写出来：
// 两次调用里只有一次拿到 token，覆盖率就是 1/2，不是「看起来完整」的 1。
func TestAttributionRollupsUseExplicitDenominators(t *testing.T) {
	f := newFixture(t)
	seedAttribution(t, f)
	svc := &Service{}
	f.read(t, func(tx *sql.Tx) error {
		report, err := svc.Attribution(tx, AttributionQuery{
			Query:     Query{ProjectID: "p1", Window: attrWindow()},
			Dimension: DimensionNode,
		}, nil, projection.Cursors{})
		if err != nil {
			return err
		}
		if len(report.Rollups) != 1 {
			t.Fatalf("按节点应只有 1 组，得到 %d", len(report.Rollups))
		}
		r := report.Rollups[0]
		if r.Value != "node_pi" {
			t.Fatalf("节点取值=%q", r.Value)
		}
		if r.Turns != 2 {
			t.Fatalf("两次调用应记 2 轮，得到 %d", r.Turns)
		}
		if r.Tokens.InputTokens == nil || *r.Tokens.InputTokens != 100 {
			t.Fatalf("被取代的 run 级不应计入，input=%v", r.Tokens.InputTokens)
		}
		if r.UsageObserved.Denominator != 2 || r.UsageObserved.Numerator != 1 {
			t.Fatalf("覆盖率应为 1/2，得到 %+v", r.UsageObserved)
		}
		if r.UsageUnknown {
			t.Fatal("还有一次调用拿到 token，不该整组标未知")
		}
		if report.Totals.Calls.Numerator != 2 || report.Totals.Calls.Denominator != 2 {
			t.Fatalf("调用分母应来自关联：%+v", report.Totals.Calls)
		}
		if report.Totals.RunSuccess.Numerator != 1 || report.Totals.RunSuccess.Denominator != 1 {
			t.Fatalf("Run 成功率应为 1/1：%+v", report.Totals.RunSuccess)
		}
		if report.Totals.DeliveryPass.Numerator != 1 || report.Totals.DeliveryPass.Denominator != 1 {
			t.Fatalf("交付通过应为 1/1：%+v", report.Totals.DeliveryPass)
		}
		if report.Totals.Cost.AmountUSD != nil {
			t.Fatal("没有价目表就不该给成本数字")
		}
		if report.Totals.Cost.Basis == "" {
			t.Fatal("成本未知必须写明依据")
		}
		if len(report.Gaps) == 0 {
			t.Fatal("工具时间与分维度 Run 结果都应记为缺口")
		}
		return nil
	})
}

// TestAttributionDimensionsAndFilters 说明维度可切换，过滤条件生效且不串味。
func TestAttributionDimensionsAndFilters(t *testing.T) {
	f := newFixture(t)
	seedAttribution(t, f)
	svc := &Service{}
	f.read(t, func(tx *sql.Tx) error {
		for _, dim := range []Dimension{DimensionModel, DimensionSource, DimensionPurpose} {
			report, err := svc.Attribution(tx, AttributionQuery{
				Query:     Query{ProjectID: "p1", Window: attrWindow()},
				Dimension: dim,
			}, nil, projection.Cursors{})
			if err != nil {
				return err
			}
			if len(report.Rollups) != 1 {
				t.Fatalf("维度 %s 应有 1 组，得到 %d", dim, len(report.Rollups))
			}
			if report.Dimension != dim {
				t.Fatalf("报表应回显维度 %s，得到 %s", dim, report.Dimension)
			}
		}
		// 按用途过滤：lead 没有数据，归集应为空但报表仍然成立。
		report, err := svc.Attribution(tx, AttributionQuery{
			Query:     Query{ProjectID: "p1", Window: attrWindow()},
			Dimension: DimensionNode,
			Purpose:   "lead",
		}, nil, projection.Cursors{})
		if err != nil {
			return err
		}
		if len(report.Rollups) != 0 {
			t.Fatalf("lead 用途不该有归集，得到 %d 组", len(report.Rollups))
		}
		if report.Totals.Calls.Denominator != 0 || report.Totals.Calls.Ratio != nil {
			t.Fatalf("零分母比率必须是 null，得到 %+v", report.Totals.Calls)
		}
		return nil
	})
}

// TestAttributionTimeSplitKeepsFourKindsApart 说明四类时间分开算：
// Run 时间取 task_phase 与 cli_process 的并集，等待单独报，工具时间缺测就是 null。
func TestAttributionTimeSplitKeepsFourKindsApart(t *testing.T) {
	f := newFixture(t)
	seedAttribution(t, f)
	w := attrWindow()
	intervals := []projection.Interval{
		{IntervalID: "iv_p", Kind: projection.KindTaskPhase, InstanceID: "ti_1", RunID: "run_a",
			Generation: 1, StartAt: attrTime("2026-11-18T02:00:00Z"), EndAt: attrTimePtr("2026-11-18T02:30:00Z")},
		{IntervalID: "iv_c", Kind: projection.KindCLIProcess, InstanceID: "ti_1", RunID: "run_a",
			Generation: 1, StartAt: attrTime("2026-11-18T02:05:00Z"), EndAt: attrTimePtr("2026-11-18T02:25:00Z")},
		{IntervalID: "iv_t", Kind: projection.KindTurn, InstanceID: "ti_1", RunID: "run_a",
			Generation: 1, StartAt: attrTime("2026-11-18T02:10:00Z"), EndAt: attrTimePtr("2026-11-18T02:12:00Z")},
		{IntervalID: "iv_w", Kind: projection.KindExplicitWait, InstanceID: "ti_1", RunID: "run_a",
			Generation: 1, StartAt: attrTime("2026-11-18T02:20:00Z"), EndAt: attrTimePtr("2026-11-18T02:40:00Z")},
	}
	svc := &Service{}
	f.read(t, func(tx *sql.Tx) error {
		report, err := svc.Attribution(tx, AttributionQuery{
			Query:     Query{ProjectID: "p1", Window: w},
			Dimension: DimensionSource,
		}, intervals, projection.Cursors{})
		if err != nil {
			return err
		}
		time := report.Time
		if time.RunSeconds == nil || *time.RunSeconds != 1800 {
			t.Fatalf("Run 时间应是 task_phase 与 cli_process 的并集 1800 秒，得到 %v", time.RunSeconds)
		}
		if time.ModelSeconds == nil || *time.ModelSeconds != 120 {
			t.Fatalf("模型时间应是 turn 区间并集 120 秒，得到 %v", time.ModelSeconds)
		}
		if time.WaitSeconds == nil || *time.WaitSeconds != 1200 {
			t.Fatalf("等待时间应单独报 1200 秒，得到 %v", time.WaitSeconds)
		}
		if time.ToolSeconds != nil {
			t.Fatalf("工具时间没有独立来源，必须是 null（不拿 Run 时间顶替），得到 %d", *time.ToolSeconds)
		}
		found := false
		for _, gap := range time.Gaps {
			if gap.Field == "tool_seconds" && gap.Reason != "" {
				found = true
			}
		}
		if !found {
			t.Fatalf("工具时间缺测必须记缺口并写明原因，得到 %+v", time.Gaps)
		}
		return nil
	})
}

// attrTime 解析区间时刻（UTC）。
func attrTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		panic(err)
	}
	return t
}

// attrTimePtr 造一个区间结束时刻指针。
func attrTimePtr(v string) *time.Time {
	t := attrTime(v)
	return &t
}
