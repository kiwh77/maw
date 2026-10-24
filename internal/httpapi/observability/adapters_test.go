package observability

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/projection"
)

// 本文件验证「薄适配」真的接通了上游：用真实状态库跑会话、统计、提醒与时间轴，
// 而不是只靠 HTTP 层替身。替身证明传输语义，真实库证明数据形状。

const (
	testProject = "p1"
	testWindow  = "2026-10-23"
)

// adapterFixture 是推进到最新迁移的临时状态库。
type adapterFixture struct {
	store *db.Store
}

func newAdapterFixture(t *testing.T) *adapterFixture {
	t.Helper()
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("打开状态库失败：%v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	return &adapterFixture{store: store}
}

func (f *adapterFixture) write(t *testing.T, fn func(*sql.Tx) error) {
	t.Helper()
	if err := f.store.Write(fn); err != nil {
		t.Fatalf("写库失败：%v", err)
	}
}

// window 返回测试用的半开窗口：2026-10-23 全天 UTC。
func testWindowRange(t *testing.T) projection.Window {
	t.Helper()
	start, err := time.Parse(time.RFC3339, "2026-10-23T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return projection.Window{Start: start, End: start.Add(24 * time.Hour)}
}

func i64(v int64) *int64 { return &v }

// seed 写入会话／轮次／关联／用量／提醒／一条事件水位。
func (f *adapterFixture) seed(t *testing.T) {
	t.Helper()
	f.write(t, func(tx *sql.Tx) error {
		sessions := []db.SourceSession{
			{SessionID: "sess-a", ProjectID: testProject, SourceNS: "claude",
				ExternalID: "ext-a", Adapter: "claude-code",
				OpenedAt: "2026-10-23T01:00:00Z", ClosedAt: "2026-10-23T03:00:00Z"},
			// 会话自身没有工程归属：靠 Run 关联命中工程过滤。
			{SessionID: "sess-x", SourceNS: "codex", ExternalID: "ext-x", Adapter: "codex",
				OpenedAt: "2026-10-23T04:00:00Z"},
		}
		for _, s := range sessions {
			if err := db.UpsertSourceSession(tx, s); err != nil {
				return err
			}
		}
		turns := []db.SourceTurn{
			{TurnID: "turn-1", SourceNS: "claude", SessionID: "sess-a",
				StartedAt: "2026-10-23T01:10:00Z", EndedAt: "2026-10-23T01:20:00Z"},
			{TurnID: "turn-2", SourceNS: "codex", SessionID: "sess-x",
				StartedAt: "2026-10-23T04:10:00Z"},
			{TurnID: "turn-3", SourceNS: "claude", SessionID: "sess-a",
				StartedAt: "2026-10-23T02:00:00Z", EndedAt: "2026-10-23T02:05:00Z"},
		}
		for _, tr := range turns {
			if err := db.UpsertSourceTurn(tx, tr); err != nil {
				return err
			}
		}
		links := []db.RunSourceLink{
			{LinkID: "link-1", ProjectID: testProject, RunID: "run-1", InstanceID: "ti_a",
				SessionID: "sess-a", TurnID: "turn-1", Attributed: true},
			{LinkID: "link-2", ProjectID: testProject, RunID: "run-2", InstanceID: "ti_b",
				SessionID: "sess-x", TurnID: "turn-2", Attributed: true},
			{LinkID: "link-3", ProjectID: testProject, RunID: "run-3", InstanceID: "ti_a",
				SessionID: "sess-a", TurnID: "turn-3", Attributed: true},
		}
		for _, l := range links {
			if err := db.InsertRunSourceLink(tx, l); err != nil {
				return err
			}
		}
		// turn-1 有两条同 scope 记账：epoch 2 取代 epoch 1，不得相加。
		usage := []db.UsageRecord{
			{UsageID: "u1", SourceNS: "claude", TurnID: "turn-1", Epoch: 1, Scope: "model",
				Semantic: db.UsageDelta, InputTokens: i64(100), OutputTokens: i64(20),
				TotalTokens: i64(120), Quality: model.QualityObserved},
			{UsageID: "u2", SourceNS: "claude", TurnID: "turn-1", Epoch: 2, Scope: "model",
				Semantic: db.UsageDelta, InputTokens: i64(150), OutputTokens: i64(30),
				TotalTokens: i64(180), Quality: model.QualityObserved},
		}
		for _, u := range usage {
			if err := db.PutUsageRecord(tx, u); err != nil {
				return err
			}
		}
		notes := []db.NotificationDelivery{
			{NotificationID: "ntf_1", ProjectID: testProject, EventID: "evt_1",
				Channel: "in_app", Status: "pending", CreatedAt: "2026-10-23T01:00:00Z",
				Meta: map[string]any{"kind": "waiting", "subject": "ti_a", "state": "waiting"}},
			{NotificationID: "ntf_2", ProjectID: testProject, EventID: "evt_2",
				Channel: "desktop", Status: "acked", CreatedAt: "2026-10-23T02:00:00Z",
				AckedAt: "2026-10-23T02:10:00Z",
				Meta:    map[string]any{"kind": "delivery", "subject": "ti_b", "state": "pending"}},
			// 别的工程：工程过滤不得把它算进来。
			{NotificationID: "ntf_3", ProjectID: "p2", EventID: "evt_3",
				Channel: "in_app", Status: "pending", CreatedAt: "2026-10-23T03:00:00Z",
				Meta: map[string]any{"kind": "failed", "subject": "ti_z", "state": "failed"}},
		}
		for _, n := range notes {
			if _, err := db.PutNotificationDelivery(tx, n); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO run_events(event_id, project_id, run_id, instance_id, kind,
  phase, lease_id, schema_version, quality, occurred_at, received_at, payload_json)
VALUES ('evt_1', ?, 'run-1', 'ti_a', 'task.claimed', '', '', 1, 'observed',
  '2026-10-23T01:00:00Z', '2026-10-23T01:00:01Z', '{}')`, testProject)
		return err
	})
}

// TestSQLSessionsListsTurnsWithDedupedUsage 验证会话下钻：
// 轮次用量按 (turn_id, epoch) 去重，未关闭的会话／轮次不算完整。
func TestSQLSessionsListsTurnsWithDedupedUsage(t *testing.T) {
	f := newAdapterFixture(t)
	f.seed(t)
	src := &SQLSessions{Store: f.store}

	page, err := src.Sessions(context.Background(), SessionQuery{
		ProjectID: testProject, Window: testWindowRange(t), Limit: 10,
	})
	if err != nil {
		t.Fatalf("查询会话失败：%v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("应返回 2 个会话（含靠 Run 关联命中的），得到 %d", len(page.Items))
	}
	if page.Completeness != projection.CompletenessPartial {
		t.Fatalf("有未关闭会话／轮次时应为 partial，得到 %q", page.Completeness)
	}
	if page.Watermark == nil || page.Watermark.Cursor == 0 {
		t.Fatalf("会话是事实直读，水位应带上事件游标：%+v", page.Watermark)
	}

	byID := map[string]SessionItem{}
	for _, it := range page.Items {
		byID[it.SessionID] = it
	}
	a, ok := byID["sess-a"]
	if !ok {
		t.Fatal("缺少 sess-a")
	}
	if a.Unattributed {
		t.Fatal("sess-a 自身有工程归属，不应标 unattributed")
	}
	if a.SpanSeconds != 7200 {
		t.Fatalf("sess-a 跨度应为 2 小时，得到 %d", a.SpanSeconds)
	}
	if a.TurnCount != 2 || len(a.Turns) != 2 {
		t.Fatalf("sess-a 应有 2 轮，得到 %d", a.TurnCount)
	}
	// 同 scope 只取最大 epoch：150 + 30，不是 (100+20)+(150+30)。
	if a.Usage.InputTokens == nil || *a.Usage.InputTokens != 150 {
		t.Fatalf("sess-a input 应按轮去重为 150，得到 %v", a.Usage.InputTokens)
	}
	if a.Usage.OutputTokens == nil || *a.Usage.OutputTokens != 30 {
		t.Fatalf("sess-a output 应为 30，得到 %v", a.Usage.OutputTokens)
	}
	if a.Usage.TotalTokens == nil || *a.Usage.TotalTokens != 180 {
		t.Fatalf("sess-a total 应为 180，得到 %v", a.Usage.TotalTokens)
	}
	if len(a.Turns[0].InstanceIDs) == 0 || a.Turns[0].InstanceIDs[0] != "ti_a" {
		t.Fatalf("轮次应带任务实例：%+v", a.Turns[0])
	}

	x, ok := byID["sess-x"]
	if !ok {
		t.Fatal("缺少 sess-x")
	}
	if !x.Unattributed {
		t.Fatal("sess-x 没有工程归属，应标 unattributed")
	}
	if !x.Open {
		t.Fatal("sess-x 没有关闭边界，应标 open")
	}
	if x.Usage.UnknownTurns != 1 || x.Usage.Quality != model.QualityUnknown {
		t.Fatalf("sess-x 用量应记为未知，不得填 0：%+v", x.Usage)
	}
	if x.Usage.TotalTokens != nil {
		t.Fatalf("未知用量不得给出数字：%v", x.Usage.TotalTokens)
	}
}

// TestSQLSessionsSourceFilterAndPagination 验证来源过滤与游标分页。
func TestSQLSessionsSourceFilterAndPagination(t *testing.T) {
	f := newAdapterFixture(t)
	f.seed(t)
	src := &SQLSessions{Store: f.store}

	filtered, err := src.Sessions(context.Background(), SessionQuery{
		ProjectID: testProject, SourceNS: "claude", Window: testWindowRange(t), Limit: 10,
	})
	if err != nil {
		t.Fatalf("按来源查询失败：%v", err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].SessionID != "sess-a" {
		t.Fatalf("source_ns=claude 只应返回 sess-a：%+v", filtered.Items)
	}

	first, err := src.Sessions(context.Background(), SessionQuery{
		ProjectID: testProject, Window: testWindowRange(t), Limit: 1,
	})
	if err != nil {
		t.Fatalf("首页失败：%v", err)
	}
	if len(first.Items) != 1 || first.Next == "" {
		t.Fatalf("limit=1 时应有下一页游标：%+v", first)
	}
	second, err := src.Sessions(context.Background(), SessionQuery{
		ProjectID: testProject, Window: testWindowRange(t), Limit: 1, Cursor: first.Next,
	})
	if err != nil {
		t.Fatalf("第二页失败：%v", err)
	}
	if len(second.Items) != 1 {
		t.Fatalf("第二页应有 1 条，得到 %d", len(second.Items))
	}
	if second.Items[0].SessionID == first.Items[0].SessionID {
		t.Fatal("翻页不得重复同一条会话")
	}
}

// TestSQLSessionsTruncatesTurnDetail 验证轮次上限不是静默截断。
func TestSQLSessionsTruncatesTurnDetail(t *testing.T) {
	f := newAdapterFixture(t)
	f.seed(t)
	src := &SQLSessions{Store: f.store, MaxTurnsPerSession: 1}

	page, err := src.Sessions(context.Background(), SessionQuery{
		ProjectID: testProject, SourceNS: "claude", Window: testWindowRange(t), Limit: 10,
	})
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	it := page.Items[0]
	if !it.TurnsTruncated {
		t.Fatal("超出上限时必须标 turns_truncated")
	}
	if it.TurnCount != 2 {
		t.Fatalf("turn_count 应反映真实轮次数 2，得到 %d", it.TurnCount)
	}
	if len(it.Turns) != 1 {
		t.Fatalf("明细应被截到 1 条，得到 %d", len(it.Turns))
	}
	if len(page.Missing) == 0 {
		t.Fatal("截断必须计入缺测")
	}
	if page.Completeness != projection.CompletenessPartial {
		t.Fatalf("截断后完整性应为 partial，得到 %q", page.Completeness)
	}
}

// TestSQLRemindersListAndAck 验证提醒列表、角标与幂等确认。
func TestSQLRemindersListAndAck(t *testing.T) {
	f := newAdapterFixture(t)
	f.seed(t)
	src := &SQLReminders{Store: f.store, Writer: f.store}

	page, err := src.Reminders(context.Background(), ReminderQuery{
		ProjectID: testProject, Window: testWindowRange(t), Limit: 10,
	})
	if err != nil {
		t.Fatalf("查询提醒失败：%v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("工程内应有 2 条提醒，得到 %d", len(page.Items))
	}
	if page.Unhandled != 1 {
		t.Fatalf("未确认应为 1 条，得到 %d", page.Unhandled)
	}
	if page.Counts["pending"] != 1 || page.Counts["acked"] != 1 {
		t.Fatalf("状态分组不符：%v", page.Counts)
	}
	if page.Items[0].Kind != "waiting" || page.Items[0].Subject != "ti_a" {
		t.Fatalf("meta 投影不正确：%+v", page.Items[0])
	}

	// 按状态过滤。
	acked, err := src.Reminders(context.Background(), ReminderQuery{
		ProjectID: testProject, Window: testWindowRange(t), Status: "acked", Limit: 10,
	})
	if err != nil {
		t.Fatalf("按状态过滤失败：%v", err)
	}
	if len(acked.Items) != 1 || acked.Items[0].NotificationID != "ntf_2" {
		t.Fatalf("status=acked 应只返回 ntf_2：%+v", acked.Items)
	}

	// 第一次确认：changed=true。
	res, err := src.Ack(context.Background(), testProject, "ntf_1", fixedNow)
	if err != nil {
		t.Fatalf("确认失败：%v", err)
	}
	if !res.Changed || res.Notification == nil || res.Notification.Status != "acked" {
		t.Fatalf("确认后应为 acked 且 changed=true：%+v", res)
	}
	if res.Notification.AckedAt == "" {
		t.Fatal("确认必须写 acked_at")
	}
	if res.ChangedTaskStatus {
		t.Fatal("确认提醒不得改任务状态")
	}

	// 第二次确认：幂等重放。
	res, err = src.Ack(context.Background(), testProject, "ntf_1", fixedNow)
	if err != nil {
		t.Fatalf("重复确认不应报错：%v", err)
	}
	if res.Changed || !res.IdempotentReplay {
		t.Fatalf("重复确认应 changed=false／replay=true：%+v", res)
	}

	// 跨工程确认：按 404 处理，不泄露存在性。
	if _, err := src.Ack(context.Background(), testProject, "ntf_3", fixedNow); err == nil {
		t.Fatal("确认别的工程的提醒应失败")
	}
	var nf *ErrNotFound
	if _, err := src.Ack(context.Background(), testProject, "ntf_missing", fixedNow); err == nil {
		t.Fatal("确认不存在的提醒应失败")
	} else if !asNotFound(err, &nf) {
		t.Fatalf("应映射成 not_found，得到 %v", err)
	}
}

// asNotFound 判断错误是否为 not_found。
func asNotFound(err error, target **ErrNotFound) bool {
	for err != nil {
		if e, ok := err.(*ErrNotFound); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// TestSQLStatsReportKeepsTurnDedupeAndCoverage 验证统计报表吃到了同一份用量口径。
func TestSQLStatsReportKeepsTurnDedupeAndCoverage(t *testing.T) {
	f := newAdapterFixture(t)
	f.seed(t)
	stats := &SQLStats{Store: f.store}

	report, err := stats.Report(context.Background(), StatsQuery{
		ProjectID: testProject, Window: testWindowRange(t),
	}, TimelineData{Cursors: projection.Cursors{
		StateCursor: 1, ProjectionCursor: 1, Completeness: projection.CompletenessComplete,
	}})
	if err != nil {
		t.Fatalf("统计失败：%v", err)
	}
	if report.Usage.TurnCount != 3 {
		t.Fatalf("窗口内应有 3 轮，得到 %d", report.Usage.TurnCount)
	}
	if report.Usage.Tokens.TotalTokens == nil || *report.Usage.Tokens.TotalTokens != 180 {
		t.Fatalf("token 应按轮去重为 180，得到 %v", report.Usage.Tokens.TotalTokens)
	}
	// 从未观测的轮次不进 usage_unknown，而由覆盖率如实呈现（契约 §4.3／§5）：
	// 3 轮里只有 1 轮有记账，覆盖率必须说 1/3，不能假装完整。
	if report.Usage.UsageUnknown {
		t.Fatal("从未观测的轮次不应混进 usage_unknown，应由覆盖率表达")
	}
	cov := report.Usage.Coverage
	if cov.ObservedTurns != 1 || cov.ObservedDenom != 3 {
		t.Fatalf("覆盖率应为 1/3，得到 %+v", cov)
	}
	if cov.Ratio < 0.33 || cov.Ratio > 0.34 {
		t.Fatalf("覆盖率比率应为 1/3，得到 %v", cov.Ratio)
	}
	if report.Watermark.Cursor != 1 || report.Watermark.CompleteBefore != 1 {
		t.Fatalf("报表必须回显投影水位：%+v", report.Watermark)
	}
	if report.Completeness != projection.CompletenessComplete {
		t.Fatalf("完整性应沿用投影：%q", report.Completeness)
	}
}

// TestProjectorTimelineSourceFilter 验证时间轴的来源过滤：
// 没有来源归属的区间被排除并计入缺测，而不是默认算进被过滤的来源。
func TestProjectorTimelineSourceFilter(t *testing.T) {
	f := newAdapterFixture(t)
	f.seed(t)

	p := projection.New(testProject)
	open := func(cursor int64, id, instance string, at time.Time) projection.Fact {
		return projection.Fact{
			Cursor: cursor, OccurredAt: at, ReceivedAt: at,
			Kind: projection.KindTaskPhase, Action: projection.ActionOpen,
			IntervalID: id, InstanceID: instance, RunID: "run-" + id,
			Generation: 1, Phase: "in_progress", Quality: model.QualityObserved,
		}
	}
	closeF := func(cursor int64, id string, at time.Time) projection.Fact {
		return projection.Fact{
			Cursor: cursor, OccurredAt: at, ReceivedAt: at,
			Kind: projection.KindTaskPhase, Action: projection.ActionClose,
			IntervalID: id, InstanceID: "", RunID: "", Generation: 0,
			Quality: model.QualityObserved,
		}
	}
	base := time.Date(2026, 10, 23, 1, 0, 0, 0, time.UTC)
	facts := []projection.Fact{
		open(1, "iv_a", "ti_a", base),
		closeF(2, "iv_a", base.Add(30*time.Minute)),
		open(3, "iv_b", "ti_b", base.Add(time.Hour)),
		closeF(4, "iv_b", base.Add(90*time.Minute)),
		open(5, "iv_orphan", "ti_orphan", base.Add(2*time.Hour)),
		closeF(6, "iv_orphan", base.Add(150*time.Minute)),
	}
	if err := p.Apply(facts...); err != nil {
		t.Fatalf("投影失败：%v", err)
	}

	src := &ProjectorTimeline{Projector: p, Sources: &SQLSourceResolver{Store: f.store}}
	data, err := src.Timeline(context.Background(), TimelineQuery{
		ProjectID: testProject, Window: testWindowRange(t), SourceNS: "claude",
	})
	if err != nil {
		t.Fatalf("时间轴查询失败：%v", err)
	}
	if len(data.Unsupported) != 0 {
		t.Fatalf("配置了解析器就不该报不支持：%+v", data.Unsupported)
	}
	if len(data.Intervals) != 1 || data.Intervals[0].IntervalID != "iv_a" {
		t.Fatalf("source_ns=claude 只应剩 iv_a：%+v", data.Intervals)
	}
	foundUnattributed := false
	for _, m := range data.Missing {
		if m.Reason == "source_unattributed" {
			foundUnattributed = true
		}
	}
	if !foundUnattributed {
		t.Fatalf("无来源归属的区间必须计入缺测：%+v", data.Missing)
	}
	if data.Cursors.StateCursor != 6 || data.Cursors.ProjectionCursor != 6 {
		t.Fatalf("双水位应透传：%+v", data.Cursors)
	}

	// 没有解析器时，过滤条件无法满足，必须报不支持而不是给全量。
	bare := &ProjectorTimeline{Projector: p}
	data, err = bare.Timeline(context.Background(), TimelineQuery{
		ProjectID: testProject, Window: testWindowRange(t), SourceNS: "claude",
	})
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if len(data.Unsupported) == 0 {
		t.Fatal("缺解析器时应给出 unsupported")
	}
	if len(data.Intervals) != 0 {
		t.Fatal("不支持过滤时不得返回未过滤数据")
	}
}
