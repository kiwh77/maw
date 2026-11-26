// BP-STA-001 日志页的验证。
//
// 输入是 sources.logs 从 GET /api/v1/events 拉回并归一到 items 的一页事实。
// 真实事件行用的是 occurred_at / task_instance / phase（见 query.EventRow），
// 旧旁路载荷才是 ts / agent / summary —— 两种形状都必须能读，且缺字段时留空，
// 不用空字符串以外的猜测填空。
//
// 运行：node --test internal/board/assets/views/logs/logs.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "logs-view.mjs")).href;
const load = () => import(MODULE);

/** /api/v1/events 的契约行（query.EventRow）。 */
const EVENT_ROWS = [
  {
    cursor: 812, event_id: "ev_812", kind: "agent.progress", project_id: "p1",
    task_instance: "ti_uad4ku2dodur", phase: "implement", quality: "observed",
    occurred_at: "2026-11-04T04:44:56Z", received_at: "2026-11-04T04:44:57Z",
  },
  {
    cursor: 811, kind: "status_changed", project_id: "p1",
    task_instance: "ti_f5nauo0wcjsu",
    occurred_at: "2026-11-04T04:42:45Z", received_at: "2026-11-04T04:42:45Z",
  },
];

/** 旧旁路 / 兼容载荷的形状。 */
const LEGACY_ROW = {
  ts: "2026-11-04T04:42:45Z", type: "status_changed", agent: "trae-goalagents-9",
  summary: "claimed → in_progress",
};

test("BP-STA-001 日志页把一页事件画成表，并带上条数", async () => {
  const view = await load();
  const html = view.renderLogs({ status: "ok", items: EVENT_ROWS }, { lang: "zh" });
  assert.equal(html.includes("2 条事件"), true);
  assert.equal(html.includes("<table"), true);
  assert.equal(html.includes("agent.progress"), true);
  // 列名必须是中文词表，不是后端字段名。
  ["时间", "类型", "主体", "摘要"].forEach(label => {
    assert.equal(html.includes(">" + label + "<"), true, "缺少列名：" + label);
  });
});

test("BP-STA-001 契约行的时间取 occurred_at，主体取任务实例", async () => {
  const view = await load();
  const html = view.renderLogs({ status: "ok", items: EVENT_ROWS }, { lang: "zh" });
  // 事件行没有 ts 字段：只认 ts 会让时间列整片空白。
  assert.equal(html.includes("2026-11-04T04:44:56Z"), true, "occurred_at 没被当时间");
  assert.equal(html.includes("ti_uad4ku2dodur"), true, "task_instance 没被当主体");
  // 摘要在没有 message 时由阶段等事实拼出，不能空着。
  assert.equal(html.includes("implement"), true, "phase 没进摘要");
});

test("BP-STA-001 旧旁路载荷的 ts／type／agent／summary 仍能读", async () => {
  const view = await load();
  const html = view.renderLogs({ status: "ok", items: [LEGACY_ROW] }, { lang: "zh" });
  assert.equal(html.includes("2026-11-04T04:42:45Z"), true, "ts 回退没生效");
  assert.equal(html.includes("status_changed"), true, "type 回退没生效");
  assert.equal(html.includes("trae-goalagents-9"), true, "agent 回退没生效");
  assert.equal(html.includes("claimed → in_progress"), true, "summary 回退没生效");
});

test("BP-STA-001 没有显式摘要时才用载荷前 120 字符，空载荷不显示 {}", async () => {
  const view = await load();
  const payload = { note: "x".repeat(200) };
  const html = view.renderLogs({
    status: "ok",
    items: [{ occurred_at: "t", kind: "k", project_id: "p1", payload }],
  }, { lang: "zh" });
  const expected = JSON.stringify(payload).slice(0, 120)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  assert.equal(html.includes(expected), true);
  // 截断之外的内容不该出现。
  assert.equal(html.includes("x".repeat(200)), false);

  const emptyPayload = view.renderLogs({ status: "ok", items: [{ kind: "k", payload: {} }] }, { lang: "zh" });
  assert.equal(emptyPayload.includes("{}"), false, "空载荷不该渲染成 {}");
});

test("BP-STA-001 空态与失败态分开：失败不画空表", async () => {
  const view = await load();
  const empty = view.renderLogs({ status: "ok", items: [] }, { lang: "zh" });
  assert.equal(empty.includes("还没有可显示的事件"), true);
  assert.equal(empty.includes("<table"), false);

  const failed = view.renderLogs({ status: "error" }, { lang: "zh" });
  assert.equal(failed.includes("日志加载失败"), true);
  assert.equal(failed.includes("<table"), false);

  assert.equal(view.renderLogs({ status: "unauthenticated" }, { lang: "en" }).includes("Failed to load logs"), true);
  assert.equal(view.renderLogs({ status: "loading" }, { lang: "zh" }).includes("正在读取日志"), true);
});

test("BP-STA-001 事件字段按文本转义", async () => {
  const view = await load();
  const html = view.renderLogs({
    status: "ok",
    items: [{ occurred_at: "<b>t</b>", kind: "<i>k</i>", task_instance: "a&b", message: "<script>alert(1)</script>" }],
  }, { lang: "zh" });
  assert.equal(html.includes("<script>"), false);
  assert.equal(html.includes("&lt;script&gt;"), true);
  assert.equal(html.includes("a&amp;b"), true);
});

test("BP-STA-001 英文界面用英文列名", async () => {
  const view = await load();
  const html = view.renderLogs({ status: "ok", items: EVENT_ROWS }, { lang: "en" });
  ["Time", "Kind", "Actor", "Summary"].forEach(label => {
    assert.equal(html.includes(">" + label + "<"), true, "缺少英文列名：" + label);
  });
  assert.equal(html.includes("2 events"), true);
  const empty = view.renderLogs({ status: "ok", items: [] }, { lang: "en" });
  assert.equal(/[\u4e00-\u9fa5]/.test(empty), false, "英文空态不该出现中文词条");
});
