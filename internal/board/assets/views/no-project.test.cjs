// E2E-102 / E2E-107 的渲染层回归。
//
// 这两件事在浏览器里也能看到，但把它们钉在渲染层更快、更稳：
//   1. 没选工程（no_project）必须与「这段时间没有数据」分开说；
//   2. 时间轴窗口标签必须带日期，否则 24 小时窗口会显示成 16:18 – 16:18。
//
// 运行：node --test internal/board/assets/views/no-project.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const load = rel => import(pathToFileURL(path.join(__dirname, rel)).href);

test("E2E-102 时间轴：未选工程不等于没有区间", async () => {
  const view = await load("timeline/timeline-view.mjs");
  const zh = view.renderTimeline({ status: "no_project" }, { lang: "zh" });
  assert.ok(zh.includes('data-status="no_project"'), "根节点应标 no_project");
  assert.ok(zh.includes("还没有选择工程"), "应提示先选工程");
  assert.ok(!zh.includes("没有可显示的区间"), "不能把未选工程说成这段时间没数据");

  const en = view.renderTimeline({ status: "no_project" }, { lang: "en" });
  assert.ok(en.includes("No project selected"));
});

test("E2E-102 热图：未选工程不等于这一天没活动", async () => {
  const view = await load("activity/activity-view.mjs");
  const zh = view.renderActivity({ status: "no_project" }, { lang: "zh" });
  assert.ok(zh.includes('data-status="no_project"'));
  assert.ok(zh.includes("还没有选择工程"));
  assert.ok(!zh.includes("这一天没有观测到活动"), "不能把未选工程说成没有活动");
});

test("E2E-102 会话：未选工程不等于没有会话", async () => {
  const view = await load("sessions/sessions-view.mjs");
  const zh = view.renderSessions({ status: "no_project" }, { lang: "zh" });
  assert.ok(zh.includes('data-status="no_project"'));
  assert.ok(zh.includes("还没有选择工程"));
  assert.ok(!zh.includes("还没有可观测的来源会话"), "不能把未选工程说成没有会话");
});

test("E2E-102 统计：未选工程不等于没有统计", async () => {
  const view = await load("analytics/analytics-view.mjs");
  const zh = view.renderAnalytics({ status: "no_project" }, { lang: "zh" });
  assert.ok(zh.includes('data-status="no_project"'));
  assert.ok(zh.includes("还没有选择工程"));
  assert.ok(!zh.includes("这个范围里还没有统计结果"), "不能把未选工程说成没有统计");
});

test("E2E-102 有工程时 unknown 分支不受影响", async () => {
  const view = await load("timeline/timeline-view.mjs");
  const html = view.renderTimeline({ status: "unknown", intervals: [] }, { lang: "zh" });
  assert.ok(html.includes("不完整"), "未知态仍要解释原因");
  assert.ok(!html.includes("还没有选择工程"));
});

test("E2E-107 时间轴窗口标签带日期", async () => {
  const view = await load("timeline/timeline-view.mjs");
  const ctx = view.createDateContext({ hours: 24, start: "2026-11-01T00:00:00Z" });
  const html = view.renderTimeline({ status: "unknown", intervals: [] }, { lang: "zh", dateContext: ctx });
  const stamps = html.match(/\d{1,2}\/\d{1,2},? \d{2}:\d{2}/g) || [];
  assert.ok(stamps.length >= 2, "窗口两端都应带月日：" + stamps.join(" | "));
});
