// VN-742 全天活动与小时下钻视图验证。
//
// 覆盖 Acceptance：
//   · 执行覆盖与事件活跃度单位、文案分开；缺测不涂成零活动；
//   · 全天点击进入与时间轴同一小时上下文；键盘访问完整；
//   · 空日、跨时区、部分覆盖、多工程筛选与状态可核对；中英安全渲染。
//
// 运行：node --test internal/board/assets/views/activity/activity.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "activity-view.mjs")).href;
const load = () => import(MODULE);

const DAY = "2026-10-23";

// 桶身份按 Asia/Shanghai 本地小时（VN-805 的 Hour.hour 为本地小时起点的 UTC ISO）。
const buckets = () => ([
  { hour: "2026-10-22T16:00:00Z", engineering_seconds: 7200, turn_seconds: 1800, task_phase_seconds: 7200, cli_process_seconds: 0, explicit_wait_seconds: 0, avg_concurrency: 2 },
  { hour: "2026-10-22T17:00:00Z", engineering_seconds: 3600, turn_seconds: 600, task_phase_seconds: 0, cli_process_seconds: 3600, explicit_wait_seconds: 1200, avg_concurrency: 1 },
  { hour: "2026-10-23T06:00:00Z", engineering_seconds: 600, turn_seconds: 0, task_phase_seconds: 600, cli_process_seconds: 0, explicit_wait_seconds: 0, avg_concurrency: 1 },
]);

test("本地日窗口跨时区换算：上海零点 = 前日 16:00Z", async () => {
  const mod = await load();
  const win = mod.dayWindow(DAY, "Asia/Shanghai");
  assert.equal(win.start, "2026-10-22T16:00:00.000Z");
  assert.equal(win.end, "2026-10-23T16:00:00.000Z");
  const utc = mod.dayWindow(DAY, "UTC");
  assert.equal(utc.start, DAY + "T00:00:00.000Z");
  // 夏令时时区也应换算到本地零点（纽约 10 月仍处 DST，UTC-4）。
  const ny = mod.dayWindow("2026-07-01", "America/New_York");
  assert.equal(ny.start, "2026-07-01T04:00:00.000Z");
});

test("hourContext 与时间轴同一小时上下文（半开、同时区）", async () => {
  const mod = await load();
  const ctx = mod.hourContext(DAY, 8, "Asia/Shanghai");
  assert.equal(ctx.start, DAY + "T00:00:00.000Z");
  assert.equal(ctx.end, DAY + "T01:00:00.000Z");
  assert.equal(ctx.hours, 1);
  assert.equal(ctx.timezone, "Asia/Shanghai");
  // 与全天窗口起点 + 8 小时严格一致。
  const win = mod.dayWindow(DAY, "Asia/Shanghai");
  assert.equal(ctx.startMs - win.startMs, 8 * 3600_000);
});

test("桶按本地小时归位；邻日桶不入这一天", async () => {
  const mod = await load();
  const { cells } = mod.normalizeBuckets([
    ...buckets(),
    { hour: "2026-10-22T15:00:00Z", engineering_seconds: 9999 }, // 上海 10-22 23 时
  ], DAY, "Asia/Shanghai");
  assert.equal(cells[0].present, true);
  assert.equal(cells[0].engineering_seconds, 7200);
  assert.equal(cells[1].present, true);
  assert.equal(cells[14].present, true, "2026-10-23T06:00Z = 上海 14 时");
  assert.equal(cells[14].engineering_seconds, 600);
  assert.equal(cells[23].present, false, "邻日 23 时的桶不得计入");
  assert.equal(cells[23].engineering_seconds, 0);
});

test("缺测是缺数据不是零活动：complete 记零，partial 记缺测", async () => {
  const mod = await load();
  const { cells } = mod.normalizeBuckets(buckets(), DAY, "Asia/Shanghai");
  assert.equal(mod.cellState(cells[5], "complete"), "zero");
  assert.equal(mod.cellState(cells[5], "partial"), "missing");
  assert.equal(mod.cellState(cells[0], "partial"), "observed");
});

test("两个指标单位不同：覆盖=分钟，活跃度=轮次·分钟", async () => {
  const mod = await load();
  assert.equal(mod.METRICS.length, 2);
  assert.notEqual(mod.METRICS[0].unit, mod.METRICS[1].unit);
  const { cells } = mod.normalizeBuckets(buckets(), DAY, "Asia/Shanghai");
  assert.equal(mod.metricMinutes(cells[0], mod.METRICS[0]), 120); // 7200s → 120 min
  assert.equal(mod.metricMinutes(cells[0], mod.METRICS[1]), 30); // 1800s → 30 turn·min
  const zh = mod.t("unit.min", "zh");
  const zhTurn = mod.t("unit.turn·min", "zh");
  assert.equal(zh, "分钟");
  assert.equal(zhTurn, "轮次·分钟");
  assert.ok(mod.formatMetric(cells[0], mod.METRICS[1], "zh").includes("轮次·分钟"));
});

test("色阶相对全天最大值，0 值不进色阶", async () => {
  const mod = await load();
  assert.equal(mod.levelOf(0, 120), 0);
  assert.equal(mod.levelOf(60, 120), 2);
  assert.equal(mod.levelOf(120, 120), 4);
  assert.equal(mod.levelOf(60, 0), 0, "无最大值时不发明档位");
});

test("键盘方向键与 Home／End 在 24 格上移动", async () => {
  const mod = await load();
  assert.equal(mod.handleActivityKey("ArrowRight", null), 0);
  assert.equal(mod.handleActivityKey("ArrowLeft", 0), null);
  assert.equal(mod.handleActivityKey("ArrowRight", 5), 6);
  assert.equal(mod.handleActivityKey("ArrowUp", 5), 4);
  assert.equal(mod.handleActivityKey("Home", 5), 0);
  assert.equal(mod.handleActivityKey("End", 5), 23);
  assert.equal(mod.handleActivityKey("Escape", 5), 5, "无关键位不移动");
});

const renderState = (over = {}) => ({
  status: "ready",
  day: DAY,
  timezone: "Asia/Shanghai",
  hours: buckets(),
  completeness: "complete",
  watermark: { cursor: 40, complete_before: 40 },
  projects: [{ id: "p1", label: "工程一" }, { id: "p2", label: "<b>注入</b>" }],
  project_id: "p1",
  ...over,
});

test("热图渲染：覆盖与活跃度两张图、单位写在图题里", async () => {
  const mod = await load();
  const html = mod.renderActivity(renderState(), { lang: "zh", selectedHour: 0 });
  assert.ok(html.includes('data-metric="coverage"'));
  assert.ok(html.includes('data-metric="activity"'));
  assert.ok(html.includes("执行覆盖"));
  assert.ok(html.includes("事件活跃度"));
  assert.ok(html.includes("分钟"));
  assert.ok(html.includes("轮次·分钟"));
  // 覆盖图 0 时 = 120 分，活跃度图 0 时 = 30 轮次·分（同一格，两图数值不同）。
  const coverageChart = html.split('data-metric="coverage"')[1].split('data-metric="activity"')[0];
  assert.ok(coverageChart.includes('data-hour="0"'));
  assert.ok(coverageChart.includes(">120<"));
  const activityChart = html.split('data-metric="activity"')[1];
  assert.ok(activityChart.includes(">30<"));
});

test("缺测小时标缺测并带 missing 状态，绝不涂成零活动", async () => {
  const mod = await load();
  const html = mod.renderActivity(renderState({ completeness: "partial" }), { lang: "zh" });
  assert.ok(html.includes('data-state="missing"'));
  assert.ok(html.includes("缺测"));
  assert.ok(html.includes('data-status="unknown"'));
  assert.ok(html.includes("部分小时缺测"));
  const zeroState = mod.renderActivity(renderState({ completeness: "complete", hours: [] }), { lang: "zh" });
  assert.ok(zeroState.includes('data-state="zero"'));
  assert.ok(zeroState.includes("无活动"));
  // 空日 + 完整数据 → 空态。
  assert.ok(zeroState.includes('data-status="empty"'));
});

test("小时下钻给出与时间轴一致的上下文（data-hour-start/end）", async () => {
  const mod = await load();
  const state = renderState();
  const { cells } = mod.normalizeBuckets(state.hours, state.day, state.timezone);
  const html = mod.renderHourDetail(cells[8], state, { lang: "zh" });
  const ctx = mod.hourContext(DAY, 8, "Asia/Shanghai");
  assert.ok(html.includes('data-detail="8"'));
  assert.ok(html.includes('data-hour-start="' + ctx.start + '"'));
  assert.ok(html.includes('data-hour-end="' + ctx.end + '"'));
  assert.ok(html.includes("平均并发身份"));
  const none = mod.renderHourDetail(null, state, { lang: "zh" });
  assert.ok(none.includes('data-detail="none"'));
});

test("多工程筛选回显当前工程并转义标签", async () => {
  const mod = await load();
  const html = mod.renderActivity(renderState(), { lang: "zh" });
  assert.ok(html.includes('data-action="project"'));
  assert.ok(html.includes('<option value="p1" selected>'));
  assert.ok(html.includes("&lt;b&gt;注入&lt;/b&gt;"));
  assert.ok(!html.includes("<b>注入</b>"));
  const noFilter = mod.renderActivity({ ...renderState(), projects: [] }, { lang: "zh" });
  assert.ok(!noFilter.includes('data-action="project"'));
});

test("六种状态互不重复且带 data-status", async () => {
  const mod = await load();
  const states = {
    loading: { status: "loading" },
    empty: { status: "ready", day: DAY, timezone: "UTC", hours: [], completeness: "complete" },
    error: { status: "error" },
    expired: { status: "expired" },
    unauth: { status: "unauthenticated" },
    unknown: { status: "unknown", day: DAY, timezone: "UTC", hours: buckets(), completeness: "partial" },
  };
  const seen = new Set();
  for (const [key, state] of Object.entries(states)) {
    const html = mod.renderActivity(state, { lang: "zh" });
    assert.ok(html.length > 0, key);
    assert.ok(html.includes('data-status="'), key);
    assert.ok(!seen.has(html), key + " 与其他状态重复");
    seen.add(html);
  }
});

test("英文界面不残留中文；危险字符转义；非法日期不崩溃", async () => {
  const mod = await load();
  const html = mod.renderActivity(renderState({
    project_id: "p2",
    projects: [{ id: "p1", label: "Project One" }, { id: "p2", label: "<b>inject</b>" }],
  }), { lang: "en", selectedHour: 0 });
  // 页面标题由壳层 page-heading 提供，视图只负责热图与图例。
  assert.ok(html.includes("Execution coverage"));
  assert.ok(html.includes("turn·minutes"));
  assert.doesNotMatch(html, /[\u4e00-\u9fa5]/);
  assert.ok(!html.includes("<b>inject</b>"));
  assert.ok(html.includes("&lt;b&gt;inject&lt;/b&gt;"));
  // 非法日期退回今天，渲染仍安全（不抛 Invalid time value）。
  const bad = mod.renderActivity(renderState({ day: "<script>x</script>" }), { lang: "en" });
  assert.ok(bad.includes("Execution coverage"));
  assert.ok(!bad.includes("<script>"));
});

// E2E-102：没选工程 ≠ 这一天没有活动。
test("E2E-102 未选工程时指向工程下拉，而不是说这一天没有活动", async () => {
  const mod = await load();
  const zh = mod.renderActivity({ status: "no_project" }, { lang: "zh" });
  assert.ok(zh.includes('data-status="no_project"'));
  assert.ok(zh.includes("还没有选择工程"));
  assert.ok(zh.includes("顶部选择工程"), "文案要指向工程下拉");
  assert.ok(!zh.includes("这一天没有观测到活动"), "未选工程不得渲染成空活动");

  const en = mod.renderActivity({ status: "no_project" }, { lang: "en" });
  assert.ok(en.includes("No project selected"));
  assert.ok(!/[\u4e00-\u9fa5]/.test(en), "英文界面不应残留中文");

  // partial 分支不回退：缺测仍按 missing 记，不因新增 no_project 而变成零。
  const cells = mod.normalizeBuckets([{ hour: "01", count: 0, quality: "unknown" }], "2026-10-23", "UTC").cells;
  assert.equal(mod.cellState(cells[0], "partial"), "missing");
});
