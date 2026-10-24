// VN-741 分层执行时间轴视图验证。
//
// 覆盖 Acceptance：
//   · 四类图例分开，未知／未闭合区间不补满；
//   · 小时窗口、缩放、键盘聚焦与详情下钻；统一日期上下文；
//   · 加载／空／错误／过期／unknown 状态可核对；中英安全渲染。
//
// 运行：node --test internal/board/assets/views/timeline/timeline.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "timeline-view.mjs")).href;
const load = () => import(MODULE);

const sample = () => ([
  {
    interval_id: "iv_phase",
    kind: "task_phase",
    phase: "in_progress",
    instance_id: "ti_a",
    run_id: "run_1",
    start_at: "2026-10-23T01:00:00Z",
    end_at: "2026-10-23T03:00:00Z",
    quality: "observed",
  },
  {
    interval_id: "iv_cli",
    kind: "cli_process",
    instance_id: "ti_a",
    run_id: "run_1",
    start_at: "2026-10-23T01:30:00Z",
    end_at: "2026-10-23T02:00:00Z",
    quality: "observed",
  },
  {
    interval_id: "iv_turn",
    kind: "turn",
    instance_id: "ti_a",
    run_id: "run_1",
    start_at: "2026-10-23T01:40:00Z",
    end_at: "2026-10-23T01:42:00Z",
    quality: "observed",
  },
  {
    interval_id: "iv_wait",
    kind: "explicit_wait",
    reason: "external_dependency",
    instance_id: "ti_a",
    run_id: "run_1",
    start_at: "2026-10-23T02:10:00Z",
    end_at: "2026-10-23T02:40:00Z",
    quality: "observed",
  },
  {
    interval_id: "iv_open",
    kind: "cli_process",
    instance_id: "ti_a",
    run_id: "run_2",
    start_at: "2026-10-23T02:50:00Z",
    end_at: null,
    open: true,
    quality: "unknown",
  },
]);

test("四类区间封闭，未知类别不冒充执行", async () => {
  const mod = await load();
  assert.deepEqual(mod.INTERVAL_KINDS, [
    "task_phase", "cli_process", "turn", "explicit_wait",
  ]);
  const norm = mod.normalizeIntervals([
    { interval_id: "x", kind: "bogus", start_at: "2026-10-23T01:00:00Z", end_at: "2026-10-23T02:00:00Z" },
  ]);
  assert.equal(norm[0].kind, "unknown");
  assert.equal(norm[0].fill, "hatch");
});

test("未闭合与 unknown 质量不补满（hatch，不 solid）", async () => {
  const mod = await load();
  const norm = mod.normalizeIntervals(sample());
  const open = norm.find(iv => iv.interval_id === "iv_open");
  assert.equal(open.open, true);
  assert.equal(open.fill, "hatch");
  assert.equal(open.endMs, null);
  const phase = norm.find(iv => iv.interval_id === "iv_phase");
  assert.equal(phase.fill, "solid");
});

test("半开窗口 carry-in 与终点排除", async () => {
  const mod = await load();
  const ctx = mod.createDateContext({
    start: "2026-10-23T01:30:00Z",
    hours: 1,
    timezone: "UTC",
  });
  assert.equal(new Date(ctx.end).toISOString(), "2026-10-23T02:30:00.000Z");
  const bars = mod.layoutBars(mod.normalizeIntervals(sample()), ctx);
  const ids = bars.map(b => b.interval_id);
  assert.ok(ids.includes("iv_phase"), "窗口前开始的阶段应 carry-in");
  assert.ok(ids.includes("iv_cli"));
  // 恰好从窗口终点开始的区间不入窗
  const edge = mod.layoutBars(mod.normalizeIntervals([{
    interval_id: "iv_edge",
    kind: "turn",
    start_at: "2026-10-23T02:30:00Z",
    end_at: "2026-10-23T03:00:00Z",
    quality: "observed",
  }]), ctx);
  assert.equal(edge.length, 0);
});

test("缩放与平移保持时区上下文", async () => {
  const mod = await load();
  let ctx = mod.createDateContext({ start: "2026-10-23T00:00:00Z", hours: 6, timezone: "Asia/Shanghai" });
  ctx = mod.zoomWindow(ctx, 1);
  assert.ok(mod.ZOOM_HOURS.includes(ctx.hours));
  assert.equal(ctx.timezone, "Asia/Shanghai");
  const next = mod.shiftWindow(ctx, 1);
  assert.equal(next.startMs - ctx.startMs, ctx.hours * 3600_000);
});

test("键盘左右移动焦点，Enter 开详情，Esc 关", async () => {
  const mod = await load();
  const ctx = mod.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" });
  const state = { intervals: sample() };
  let sel = mod.applyKeyToState(state, { dateContext: ctx, selectedId: "iv_phase" }, "ArrowRight");
  assert.ok(sel.selectedId);
  assert.notEqual(sel.selectedId, "iv_phase");
  sel = mod.applyKeyToState(state, { dateContext: ctx, selectedId: sel.selectedId, showDetail: false }, "Enter");
  assert.equal(sel.showDetail, true);
  sel = mod.applyKeyToState(state, { dateContext: ctx, selectedId: sel.selectedId, showDetail: true }, "Escape");
  assert.equal(sel.showDetail, false);
});

test("图例与详情下钻渲染四类标签", async () => {
  const mod = await load();
  const ctx = mod.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" });
  const html = mod.renderTimeline(
    { intervals: sample(), cursors: { state_cursor: 20, projection_cursor: 18, completeness: "partial" } },
    { lang: "zh", dateContext: ctx, selectedId: "iv_wait", showDetail: true }
  );
  assert.ok(html.includes(mod.t("kind.task_phase", "zh")));
  assert.ok(html.includes(mod.t("kind.cli_process", "zh")));
  assert.ok(html.includes(mod.t("kind.turn", "zh")));
  assert.ok(html.includes(mod.t("kind.explicit_wait", "zh")));
  assert.ok(html.includes('data-detail="iv_wait"'));
  assert.ok(html.includes("external_dependency"));
  assert.ok(html.includes("timeline__bar--open") || html.includes("timeline__bar--unknown"));
  assert.ok(html.includes(mod.t("cursors.state", "zh")));
  assert.ok(html.includes(mod.t("cursors.projection", "zh")));
});

test("加载／空／错误／过期／未认证／unknown 文案互不重复", async () => {
  const mod = await load();
  const states = {
    loading: { status: "loading" },
    empty: { status: "ready", intervals: [] },
    error: { status: "error" },
    expired: { status: "expired" },
    unauth: { status: "unauthenticated" },
    unknown: { status: "unknown", intervals: sample() },
  };
  const seen = new Set();
  for (const [key, state] of Object.entries(states)) {
    const html = mod.renderTimeline(state, {
      lang: "zh",
      dateContext: mod.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3 }),
    });
    assert.ok(html.length > 0, key);
    assert.ok(html.includes('data-status="'), key);
    assert.ok(!seen.has(html), key + " 与其他状态重复");
    seen.add(html);
  }
});

test("英文界面不残留中文；危险字符转义", async () => {
  const mod = await load();
  const html = mod.renderTimeline({
    intervals: [{
      interval_id: '<img src=x onerror="alert(1)">',
      kind: "task_phase",
      phase: "<b>x</b>",
      start_at: "2026-10-23T01:00:00Z",
      end_at: "2026-10-23T02:00:00Z",
      quality: "observed",
      run_id: "run_1",
    }],
  }, {
    lang: "en",
    dateContext: mod.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" }),
    selectedId: '<img src=x onerror="alert(1)">',
    showDetail: true,
  });
  assert.ok(html.includes("Execution timeline"));
  assert.doesNotMatch(html, /[\u4e00-\u9fa5]/);
  assert.ok(!html.includes("<img"));
  assert.ok(!html.includes("<b>x</b>"));
});

test("并发 run 分轨，不把缺口涂成执行", async () => {
  const mod = await load();
  const ctx = mod.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" });
  const bars = mod.layoutBars(mod.normalizeIntervals(sample()), ctx);
  const lanes = mod.groupLanes(bars);
  assert.ok(lanes.length >= 2, "run_1 与 run_2 应分轨");
  const openBar = bars.find(b => b.interval_id === "iv_open");
  assert.ok(openBar);
  assert.ok(openBar.widthPct < 100, "未闭合条不得铺满整轨");
});
