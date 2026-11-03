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

test("键盘左右平移窗口，上下移动选中，Enter 开详情，Esc 关", async () => {
  const mod = await load();
  const ctx = mod.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" });
  const state = { intervals: sample() };

  // ← / →：窗口平移（等价工具栏「上一窗」「下一窗」）
  const prev = mod.applyKeyToState(state, { dateContext: ctx, selectedId: "iv_phase" }, "ArrowLeft");
  assert.equal(prev.window, "prev");
  assert.equal(prev.dateContext.startMs, ctx.startMs - ctx.hours * 3600_000);
  const next = mod.applyKeyToState(state, { dateContext: ctx, selectedId: "iv_phase" }, "ArrowRight");
  assert.equal(next.window, "next");
  assert.equal(next.dateContext.startMs, ctx.startMs + ctx.hours * 3600_000);
  // 窗口平移不改选中
  assert.equal(next.selectedId, "iv_phase");

  // ↑ / ↓：在区间之间移动选中，不动窗口
  const down = mod.applyKeyToState(state, { dateContext: ctx, selectedId: "iv_phase" }, "ArrowDown");
  assert.ok(down.selectedId);
  assert.notEqual(down.selectedId, "iv_phase");
  assert.equal(down.window, null);
  assert.equal(down.dateContext.startMs, ctx.startMs);

  let sel = down;
  sel = mod.applyKeyToState(state, { dateContext: ctx, selectedId: sel.selectedId, showDetail: false }, "Enter");
  assert.equal(sel.showDetail, true);
  sel = mod.applyKeyToState(state, { dateContext: ctx, selectedId: sel.selectedId, showDetail: true }, "Escape");
  assert.equal(sel.showDetail, false);
});

// E2E-107：键盘左右键与工具栏按钮走同一条路径（键盘=点按钮）。
test("E2E-107 键盘左右键点对应按钮，与工具栏同一条取数路径", async () => {
  const mod = await load();
  const clicks = [];
  const button = { click: () => clicks.push("prev") };
  const scope = { querySelector: (sel) => (sel.includes('"prev"') ? button : null) };
  const host = { querySelector: (sel) => (sel === ".timeline" ? scope : null) };
  const handlers = [];
  const fakeDoc = {
    addEventListener: (type, fn) => handlers.push(fn),
    removeEventListener: () => {},
    querySelector: (sel) => (sel === "#vnext-view" ? host : null),
  };
  const dispose = mod.installTimelineKeyboard(fakeDoc);
  assert.equal(handlers.length, 1, "键盘监听应注册一次");

  const event = { key: "ArrowLeft", target: { tagName: "BODY" }, defaultPrevented: false, preventDefault() {} };
  handlers[0](event);
  assert.deepEqual(clicks, ["prev"], "左方向键应点「上一窗」按钮");

  // 没有时间轴视图时不劫持按键
  host.querySelector = () => null;
  handlers[0]({ key: "ArrowLeft", target: { tagName: "BODY" }, defaultPrevented: false, preventDefault() {} });
  assert.equal(clicks.length, 1, "非时间轴视图不该响应方向键");

  // 输入框里打字不劫持
  host.querySelector = (sel) => (sel === ".timeline" ? scope : null);
  handlers[0]({ key: "ArrowLeft", target: { tagName: "INPUT" }, defaultPrevented: false, preventDefault() {} });
  assert.equal(clicks.length, 1, "输入框内的方向键不该被劫持");

  assert.equal(typeof dispose, "function");
  dispose();
});

// E2E-107：窗口标签带日期与时间，跨天窗口首尾不再显示成同一时刻。
test("E2E-107 窗口标签含日期，跨天首尾不同时刻", async () => {
  const mod = await load();
  const ctx = mod.createDateContext({ start: "2026-10-23T16:18:00Z", hours: 24, timezone: "UTC" });
  const html = mod.renderTimeline({ intervals: sample() }, { lang: "zh", dateContext: ctx });
  const label = (html.match(/timeline__toolbar[\s\S]*?<\/div>/) || [""])[0];
  const stamps = label.match(/\d{1,2}\/\d{1,2},? \d{2}:\d{2}/g) || [];
  assert.equal(stamps.length, 2, "窗口标签应含起止两个时间戳：" + label);
  assert.notEqual(stamps[0], stamps[1], "24h 窗口首尾不得显示成同一时刻：" + label);
});

// E2E-107：改窗口后未知态与缺测说明保持，不用空列表冒充没有活动。
test("E2E-107 改窗口后仍解释未知态，不退化成空列表", async () => {
  const mod = await load();
  const ctx = mod.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" });
  const moved = mod.shiftWindow(ctx, 1);
  const html = mod.renderTimeline(
    { status: "unknown", intervals: [], cursors: { completeness: "partial" } },
    { lang: "zh", dateContext: moved }
  );
  assert.ok(html.includes('data-status="unknown"'), "改窗口后未知态应保持");
  assert.ok(html.includes("不完整"), "应继续解释数据不完整");
  assert.ok(!html.includes("没有可显示的区间"), "未知态不得退化成空列表文案");
  assert.ok(html.includes("状态水位"), "水位说明应保留");
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
  // 页面标题由壳层 page-heading 提供，视图只负责工具栏与图例。
  assert.ok(html.includes("Hour window"));
  assert.ok(html.includes("CLI process"));
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

// E2E-102：没选工程 ≠ 这段时间没有区间。
test("E2E-102 未选工程时指向工程下拉，而不是说这段时间没有数据", async () => {
  const mod = await load();
  const zh = mod.renderTimeline({ status: "no_project" }, { lang: "zh" });
  assert.ok(zh.includes('data-status="no_project"'), "根节点应标 no_project");
  assert.ok(zh.includes("还没有选择工程"));
  assert.ok(zh.includes("顶部选择工程"), "文案要指向工程下拉");
  assert.ok(!zh.includes("没有可显示的区间"), "未选工程不得渲染成空区间");

  const en = mod.renderTimeline({ status: "no_project" }, { lang: "en" });
  assert.ok(en.includes("No project selected"));
  assert.ok(!/[\u4e00-\u9fa5]/.test(en), "英文界面不应残留中文");

  // 有工程时 unknown 分支不回退：仍然解释「不完整」。
  const unknown = mod.renderTimeline({ status: "unknown", intervals: [] }, { lang: "zh" });
  assert.ok(unknown.includes("不完整"));
  assert.ok(!unknown.includes("还没有选择工程"));
});
