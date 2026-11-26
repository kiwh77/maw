package analytics

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/projection"
)

// fixture 是推进到最新迁移的临时状态库。
type fixture struct {
	store *db.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("打开状态库失败：%v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	return &fixture{store: store}
}

func (f *fixture) write(t *testing.T, fn func(*sql.Tx) error) {
	t.Helper()
	if err := f.store.Write(fn); err != nil {
		t.Fatalf("写库失败：%v", err)
	}
}

func (f *fixture) read(t *testing.T, fn func(*sql.Tx) error) {
	t.Helper()
	if err := f.store.Read(fn); err != nil {
		t.Fatalf("读库失败：%v", err)
	}
}

func mustInt(v int64) *int64 { return &v }

func fseed(t *testing.T, f *fixture) {
	t.Helper()
	f.write(t, func(tx *sql.Tx) error {
		sessions := []db.SourceSession{
			{SessionID: "sess-a1", ProjectID: projectA, SourceNS: "claude", ExternalID: "ext-1", Adapter: "claude-code", OpenedAt: "2026-10-23T00:00:00Z"},
			{SessionID: "sess-b1", ProjectID: projectB, SourceNS: "cursor", ExternalID: "ext-2", Adapter: "cursor", OpenedAt: "2026-10-23T00:00:00Z"},
			// 会话无工程归属，靠 run_source_links 挂到工程 A：归属有两条证据路径。
			{SessionID: "sess-x", ProjectID: "", SourceNS: "codex", ExternalID: "ext-3", Adapter: "codex", OpenedAt: "2026-10-23T00:00:00Z"},
		}
		for _, s := range sessions {
			if err := db.UpsertSourceSession(tx, s); err != nil {
				return err
			}
		}
		turns := []db.SourceTurn{
			{TurnID: "tu-1", SourceNS: "claude", SessionID: "sess-a1", StartedAt: "2026-10-23T10:00:00Z", EndedAt: "2026-10-23T10:05:00Z", SharedWith: []string{"ti_other"}},
			{TurnID: "tu-2", SourceNS: "claude", SessionID: "sess-a1", StartedAt: "2026-10-23T11:00:00Z"},
			// 23:30Z 在 UTC+8 已是次日：日切按查询时区。
			{TurnID: "tu-3", SourceNS: "claude", SessionID: "sess-a1", StartedAt: "2026-10-23T23:30:00Z"},
			{TurnID: "tu-5", SourceNS: "claude", SessionID: "sess-a1", StartedAt: "2026-10-23T12:00:00Z"},
			{TurnID: "tu-4", SourceNS: "cursor", SessionID: "sess-b1", StartedAt: "2026-10-23T10:30:00Z"},
			{TurnID: "tu-6", SourceNS: "codex", SessionID: "sess-x", StartedAt: "2026-10-23T13:00:00Z"},
		}
		for _, s := range turns {
			if err := db.UpsertSourceTurn(tx, s); err != nil {
				return err
			}
		}
		if err := db.InsertRunSourceLink(tx, db.RunSourceLink{
			LinkID: "link-1", ProjectID: projectA, TurnID: "tu-6", CreatedAt: "2026-10-23T13:00:00Z",
		}); err != nil {
			return err
		}
		recs := []db.UsageRecord{
			// tu-1 两个 scope 各一代：按 scope 相加。
			{UsageID: "u1", SourceNS: "claude", TurnID: "tu-1", Epoch: 1, Scope: "model", Semantic: db.UsageDelta, InputTokens: mustInt(1000), OutputTokens: mustInt(300), Quality: model.QualityObserved, RecordedAt: "2026-10-23T10:05:00Z"},
			{UsageID: "u2", SourceNS: "claude", TurnID: "tu-1", Epoch: 1, Scope: "tools", Semantic: db.UsageDelta, InputTokens: mustInt(200), OutputTokens: mustInt(50), Quality: model.QualityObserved, RecordedAt: "2026-10-23T10:05:00Z"},
			// tu-3 累计重置：epoch2 取代 epoch1，不跨代相减。
			{UsageID: "u3", SourceNS: "claude", TurnID: "tu-3", Epoch: 1, Scope: "model", Semantic: db.UsageCumulative, InputTokens: mustInt(100), OutputTokens: mustInt(20), Quality: model.QualityObserved, RecordedAt: "2026-10-23T23:35:00Z"},
			{UsageID: "u4", SourceNS: "claude", TurnID: "tu-3", Epoch: 2, Scope: "model", Semantic: db.UsageCumulative, InputTokens: mustInt(150), OutputTokens: mustInt(30), ResetObserved: true, Quality: model.QualityObserved, RecordedAt: "2026-10-23T23:40:00Z"},
			// tu-5 未知用量：token 为 null，不填 0。
			{UsageID: "u5", SourceNS: "claude", TurnID: "tu-5", Epoch: 1, Scope: "model", Semantic: db.UsageDelta, Quality: model.QualityUnknown, RecordedAt: "2026-10-23T12:01:00Z"},
			// tu-4（工程 B）同样未知：来源过滤后仍如实标注。
			{UsageID: "u6", SourceNS: "cursor", TurnID: "tu-4", Epoch: 1, Scope: "model", Semantic: db.UsageDelta, Quality: model.QualityUnknown, RecordedAt: "2026-10-23T10:35:00Z"},
		}
		for _, r := range recs {
			if err := db.PutUsageRecord(tx, r); err != nil {
				return err
			}
		}
		deliveries := []string{
			// delivery_id, mode, delivered_at, superseded_by
			`INSERT INTO deliveries(delivery_id, project_id, instance_id, version_no, run_id, mode,
				baseline_commit, delivered_at, manifest_json, fingerprint, idempotency_key, request_digest, created_at)
				VALUES ('dl-1','` + projectA + `','ti-1',1,'run-1','code','base','2026-10-23T10:10:00Z','{}','fp1','k1','dg1','2026-10-23T10:10:00Z')`,
			`INSERT INTO deliveries(delivery_id, project_id, instance_id, version_no, run_id, mode,
				baseline_commit, delivered_at, manifest_json, fingerprint, supersedes, superseded_by, idempotency_key, request_digest, created_at)
				VALUES ('dl-2','` + projectA + `','ti-1',1,'run-1','non_code','base','2026-10-23T12:00:00Z','{}','fp2','dl-1','dl-3','k2','dg2','2026-10-23T12:00:00Z')`,
			// 窗口外交付，不得计入。
			`INSERT INTO deliveries(delivery_id, project_id, instance_id, version_no, run_id, mode,
				baseline_commit, delivered_at, manifest_json, fingerprint, idempotency_key, request_digest, created_at)
				VALUES ('dl-3','` + projectA + `','ti-1',1,'run-1','code','base','2026-10-24T01:00:00Z','{}','fp3','k3','dg3','2026-10-24T01:00:00Z')`,
		}
		for _, stmt := range deliveries {
			if _, err := tx.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	})
}

var (
	projectA = "0123456789abcdef"
	projectB = "fedcba9876543210"
)

func reportQuery(w projection.Window, loc *time.Location) Query {
	return Query{ProjectID: projectA, Window: w, Loc: loc}
}

func octDay() projection.Window {
	return window("2026-10-23T00:00:00Z", "2026-10-24T00:00:00Z")
}

// 主报表：去重身份、共享轮次、累计重置、未知用量、交付数与日切全部到位。
func TestReportAggregatesDedupAndQuality(t *testing.T) {
	f := newFixture(t)
	fseed(t, f)
	svc := &Service{}
	cursors := projection.Cursors{StateCursor: 128, ProjectionCursor: 120, Completeness: projection.CompletenessPartial}
	var rep *Report
	f.read(t, func(tx *sql.Tx) error {
		var err error
		rep, err = svc.Report(tx, reportQuery(octDay(), time.UTC), []projection.Interval{
			iv("i1", "inst-a", projection.KindTaskPhase, "2026-10-23T02:00:00Z", "2026-10-23T04:00:00Z"),
			iv("i2", "inst-a", projection.KindCLIProcess, "2026-10-23T03:00:00Z", "2026-10-23T05:00:00Z"),
		}, cursors)
		return err
	})
	if rep == nil {
		t.Fatal("报表为空")
	}
	u := rep.Usage
	if u.TurnCount != 5 || u.SharedTurns != 1 {
		t.Fatalf("turn_count=%d shared=%d，要 5 / 1（tu-1 共享只记一次）", u.TurnCount, u.SharedTurns)
	}
	// tu-1: 1200+350；tu-3: 只取 epoch2 = 150/30；tu-5 未知不并入；tu-2/tu-6 从未观测。
	if u.Tokens.InputTokens == nil || *u.Tokens.InputTokens != 1350 {
		t.Fatalf("input=%v，要 1350", u.Tokens.InputTokens)
	}
	if u.Tokens.OutputTokens == nil || *u.Tokens.OutputTokens != 380 {
		t.Fatalf("output=%v，要 380", u.Tokens.OutputTokens)
	}
	if u.Tokens.TotalTokens == nil || *u.Tokens.TotalTokens != 1730 {
		t.Fatalf("total=%v，要 1730（=input+output，不含缓存）", u.Tokens.TotalTokens)
	}
	if u.Tokens.CacheReadTokens != nil {
		t.Fatalf("无缓存记账时 cache_read 应为 nil，得到 %v", *u.Tokens.CacheReadTokens)
	}
	if !u.UsageUnknown || u.UnknownTurns != 1 {
		t.Fatalf("usage_unknown=%v unknown_turns=%d，要 true / 1", u.UsageUnknown, u.UnknownTurns)
	}
	if u.Coverage.ObservedTurns != 3 || u.Coverage.ObservedDenom != 5 {
		t.Fatalf("coverage=%d/%d，要 3/5", u.Coverage.ObservedTurns, u.Coverage.ObservedDenom)
	}
	if u.Coverage.Ratio != 0.6 {
		t.Fatalf("ratio=%v，要 0.6", u.Coverage.Ratio)
	}
	// 时间轴：工程时长取 task_phase∪cli_process（02:00–05:00 = 10800），各类分别报。
	tl := rep.Timeline
	if tl.TaskPhaseSeconds != 7200 || tl.CLIProcessSeconds != 7200 || tl.EngineeringSeconds != 10800 {
		t.Fatalf("timeline=%+v，task/cli 各 7200、工程并集 10800", tl)
	}
	// 交付：窗口内 dl-1、dl-2；dl-2 已被取代。
	if rep.Deliveries.Count != 2 || rep.Deliveries.Superseded != 1 {
		t.Fatalf("deliveries=%+v，要 count=2 superseded=1", rep.Deliveries)
	}
	if rep.Deliveries.ByMode["code"] != 1 || rep.Deliveries.ByMode["non_code"] != 1 {
		t.Fatalf("by_mode=%v，要 code=1 non_code=1", rep.Deliveries.ByMode)
	}
	// 水位与完整性随报表回显，报表才可比较。
	if rep.Watermark.Cursor != 128 || rep.Watermark.CompleteBefore != 120 {
		t.Fatalf("watermark=%+v，要 cursor=128 complete_before=120", rep.Watermark)
	}
	if rep.Completeness != projection.CompletenessPartial {
		t.Fatalf("completeness=%v，要 partial", rep.Completeness)
	}
	if rep.Evidence.Turns != 5 || rep.Evidence.UsageRecords != 5 || rep.Evidence.Deliveries != 2 {
		t.Fatalf("evidence=%+v，指标必须可追到证据", rep.Evidence)
	}
	if rep.Scope.ProjectID != projectA || !rep.Scope.Start.Equal(octDay().Start) {
		t.Fatalf("scope 未回显查询范围：%+v", rep.Scope)
	}
}

// 日切与来源分组：按查询时区归日，来源之间不混算。
func TestReportByDayAndBySource(t *testing.T) {
	f := newFixture(t)
	fseed(t, f)
	svc := &Service{}
	loc := time.FixedZone("CST", 8*3600)
	var rep *Report
	f.read(t, func(tx *sql.Tx) error {
		var err error
		rep, err = svc.Report(tx, reportQuery(octDay(), loc), nil, projection.Cursors{})
		return err
	})
	if len(rep.ByDay) != 2 {
		t.Fatalf("by_day=%d 组，要 2（23 日与 24 日）", len(rep.ByDay))
	}
	day23, day24 := rep.ByDay[0], rep.ByDay[1]
	if day23.Day != "2026-10-23" || day23.TurnCount != 4 {
		t.Fatalf("23 日 = %s/%d 轮，要 2026-10-23/4", day23.Day, day23.TurnCount)
	}
	if day24.Day != "2026-10-24" || day24.TurnCount != 1 {
		t.Fatalf("24 日 = %s/%d 轮，要 2026-10-24/1（tu-3 按 UTC+8 落次日）", day24.Day, day24.TurnCount)
	}
	// 交付数按日聚合：23 日 2 笔，24 日 0 笔。
	if day23.DeliveryCount != 2 || day24.DeliveryCount != 0 {
		t.Fatalf("按日交付 = %d/%d，要 2/0", day23.DeliveryCount, day24.DeliveryCount)
	}
	if len(rep.BySource) != 2 {
		t.Fatalf("by_source=%d 组，要 2", len(rep.BySource))
	}
	if rep.BySource[0].SourceNS != "claude" || rep.BySource[0].TurnCount != 4 {
		t.Fatalf("claude 来源 = %+v，要 4 轮", rep.BySource[0])
	}
	if rep.BySource[1].SourceNS != "codex" || rep.BySource[1].TurnCount != 1 {
		t.Fatalf("codex 来源 = %+v，要 1 轮（经 run_source_links 归属）", rep.BySource[1])
	}
}

// 按来源过滤：搜索子集不与全集混算。
func TestReportSourceFilterNotMixed(t *testing.T) {
	f := newFixture(t)
	fseed(t, f)
	svc := &Service{}
	q := Query{SourceNS: "cursor", Window: octDay(), Loc: time.UTC}
	var rep *Report
	f.read(t, func(tx *sql.Tx) error {
		var err error
		rep, err = svc.Report(tx, q, nil, projection.Cursors{})
		return err
	})
	if rep.Usage.TurnCount != 1 || rep.Usage.SharedTurns != 0 {
		t.Fatalf("过滤后 turn_count=%d shared=%d，要 1 / 0", rep.Usage.TurnCount, rep.Usage.SharedTurns)
	}
	if rep.Usage.Tokens.InputTokens != nil {
		t.Fatalf("未知用量的 token 必须为 nil，不得填 0：%v", *rep.Usage.Tokens.InputTokens)
	}
	if !rep.Usage.UsageUnknown {
		t.Fatal("过滤后仍是部分未知，报表必须如实标注")
	}
	if rep.Usage.Coverage.ObservedTurns != 1 || rep.Usage.Coverage.ObservedDenom != 1 {
		t.Fatalf("coverage=%+v，要 1/1", rep.Usage.Coverage)
	}
	if rep.Scope.SourceNS != "cursor" {
		t.Fatalf("scope 未回显来源过滤：%+v", rep.Scope)
	}
}

// 零分母与空窗口：比率记 0，不假装完整；非法窗口拒绝。
func TestReportEmptyAndInvalid(t *testing.T) {
	f := newFixture(t)
	svc := &Service{}
	empty := projection.Window{Start: ts("2026-10-20T00:00:00Z"), End: ts("2026-10-21T00:00:00Z")}
	var rep *Report
	f.read(t, func(tx *sql.Tx) error {
		var err error
		rep, err = svc.Report(tx, reportQuery(empty, nil), nil, projection.Cursors{})
		return err
	})
	u := rep.Usage
	if u.TurnCount != 0 || u.Coverage.ObservedDenom != 0 || u.Coverage.Ratio != 0 {
		t.Fatalf("零分母必须给 0 比率：%+v", u.Coverage)
	}
	if u.UsageUnknown || u.Tokens.InputTokens != nil {
		t.Fatalf("无数据时报表不得编造用量：%+v", u)
	}
	if rep.Completeness != projection.CompletenessUnknown {
		t.Fatalf("无水位时 completeness 应为 unknown，得到 %v", rep.Completeness)
	}
	if rep.Timeline.AvgConcurrency != 0 {
		t.Fatalf("无区间时平均并发 = %v，要 0", rep.Timeline.AvgConcurrency)
	}
	bad := projection.Window{Start: ts("2026-10-23T10:00:00Z"), End: ts("2026-10-23T10:00:00Z")}
	err := f.store.Read(func(tx *sql.Tx) error {
		_, err := svc.Report(tx, reportQuery(bad, nil), nil, projection.Cursors{})
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "窗口") {
		t.Fatalf("非法窗口应被拒绝，得到 %v", err)
	}
}

// 小时桶之和必须等于并集总量（报表级闭环，跨日区间按本地小时分桶）。
func TestReportHoursSumMatchesUnion(t *testing.T) {
	f := newFixture(t)
	fseed(t, f)
	svc := &Service{}
	loc := time.FixedZone("CST", 8*3600)
	intervals := []projection.Interval{
		iv("i1", "inst-a", projection.KindTaskPhase, "2026-10-23T16:30:00Z", "2026-10-23T18:00:00Z"),
		iv("i2", "inst-b", projection.KindCLIProcess, "2026-10-23T17:30:00Z", "2026-10-23T18:30:00Z"),
	}
	var rep *Report
	f.read(t, func(tx *sql.Tx) error {
		var err error
		rep, err = svc.Report(tx, reportQuery(octDay(), loc), intervals, projection.Cursors{})
		return err
	})
	var sum int64
	for _, b := range rep.Timeline.Hours {
		sum += b.EngineeringSeconds
	}
	if rep.Timeline.EngineeringSeconds != 9000 {
		t.Fatalf("工程并集 = %d，要 9000（两身份重叠 30 分钟仍各算各）", rep.Timeline.EngineeringSeconds)
	}
	if sum != rep.Timeline.EngineeringSeconds {
		t.Fatalf("小时桶之和 %d ≠ 工程并集 %d", sum, rep.Timeline.EngineeringSeconds)
	}
	// 两身份在 17:30–18:00 并行：忙碌 2.5 小时·身份 / 24 小时窗口。
	if rep.Timeline.AvgConcurrency <= 0.10 || rep.Timeline.AvgConcurrency >= 0.11 {
		t.Fatalf("平均并发 = %v，要约 0.104", rep.Timeline.AvgConcurrency)
	}
}
