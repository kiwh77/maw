// VN-743 会话轮次与来源明细视图验证。
//
// 覆盖 Acceptance：
//   · 共享／未归属关联、完整性与已知用量；不强制一会话等于一任务；
//   · 不支持轮次的来源一致降级；跳转入口只在有真实开始时间时出现；
//   · 键盘访问、中英安全渲染与六种状态可核对。
//
// 运行：node --test internal/board/assets/views/sessions/sessions.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "sessions-view.mjs")).href;
const load = () => import(MODULE);

const sessions = () => ([
  {
    session_id: "sess-1",
    source_ns: "codebuddy",
    adapter: "codebuddy-cli",
    opened_at: "2026-10-23T02:00:00Z",
    closed_at: "2026-10-23T02:10:00Z",
    tasks: ["ti_a", "ti_b"],
    usage: { present: true, total_tokens: 23968, input_tokens: 23945, output_tokens: 23, usage_unknown: false, quality: "observed" },
    turns: [
      { turn_id: "tu-1", started_at: "2026-10-23T02:01:00Z", ended_at: "2026-10-23T02:05:00Z", shared_with: ["ti_b"], usage: { present: true, total_tokens: 23968, usage_unknown: false, quality: "observed" } },
      { turn_id: "tu-2", started_at: "2026-10-23T02:06:00Z", ended_at: null, shared_with: [], usage: { present: true, usage_unknown: true, quality: "unknown" } },
    ],
  },
  {
    session_id: "sess-2",
    source_ns: "codex",
    adapter: "codex-cli",
    opened_at: "2026-10-23T03:00:00Z",
    tasks: [],
    turns_supported: false,
    degrade_reason: "C24/C25 未验证",
  },
  {
    session_id: "sess-3",
    source_ns: "zcode",
    adapter: "zcode",
    opened_at: "2026-10-23T04:00:00Z",
    tasks: [],
    turns: [
      { turn_id: "tu-9", started_at: "2026-10-23T04:01:00Z", ended_at: "2026-10-23T04:02:00Z", shared_with: ["ti_x"], usage: { present: true, total_tokens: 500, usage_unknown: false, quality: "observed" } },
    ],
  },
]);

test("规范化：缺边界保留 null，未知用量不填 0", async () => {
  const mod = await load();
  const norm = mod.normalizeSessions(sessions());
  const openTurn = norm[0].turns.find(x => x.turn_id === "tu-2");
  assert.equal(openTurn.endMs, null);
  assert.equal(openTurn.usage.usage_unknown, true);
  assert.equal(openTurn.usage.total_tokens, null);
  // 不支持的来源不解析轮次。
  assert.equal(norm[1].turns_supported, false);
  assert.deepEqual(norm[1].turns, []);
});

test("共享与未归属分开标注；不强制一会话等于一任务", async () => {
  const mod = await load();
  const norm = mod.normalizeSessions(sessions());
  // sess-1 关联两个任务：不得压成单个任务。
  assert.equal(norm[0].tasks.length, 2);
  assert.equal(mod.tasksText(norm[0], "zh"), "2 个任务");
  // sess-3 一轮共享给另一个任务：徽章带共享文案。
  const badges = mod.turnBadges(norm[2].turns[0], "zh");
  assert.ok(badges.some(b => b.key === "shared" && b.text.includes("共享轮次")));
  // sess-2 / sess-3 零关联 → 未归属。
  assert.equal(mod.tasksText(norm[1], "zh"), "未归属任何任务");
  assert.equal(mod.tasksText(norm[2], "zh"), "未归属任何任务");
});

test("已知用量：未知显示部分未知，不显示 0", async () => {
  const mod = await load();
  const norm = mod.normalizeSessions(sessions());
  assert.equal(mod.usageText(norm[0].usage, "zh"), "总量 23968 tok");
  assert.equal(mod.usageText({ present: true, total_tokens: null, usage_unknown: true }, "zh"), "部分未知");
  assert.equal(mod.usageText({ present: false }, "zh"), "未观测");
});

test("跳转入口只出现在有真实开始时间的轮次上", async () => {
  const mod = await load();
  const norm = mod.normalizeSessions(sessions());
  const good = mod.renderTurnRow(norm[0].turns[0], "zh");
  assert.ok(good.includes("data-jump-start="));
  assert.ok(good.includes("在时间轴打开这一小时"));
  const html = mod.renderSessions({ sessions: sessions(), completeness: "complete" }, { lang: "zh", selectedId: "sess-1" });
  // tu-1 有入口；tu-2 有开始时间也有入口；缺开始边界的轮次不发明入口。
  const unsupported = mod.renderSessionDetail(norm[1], { lang: "zh" });
  assert.ok(unsupported.includes("data-turns=\"unsupported\""));
  assert.ok(unsupported.includes("该来源不支持轮次明细"));
  assert.ok(!unsupported.includes("sessions__jump"));
});

test("跳转上下文与时间轴同一小时窗口（半开、对齐整点）", async () => {
  const mod = await load();
  const norm = mod.normalizeSessions(sessions());
  const ctx = mod.jumpContext(norm[0].turns[0], "Asia/Shanghai");
  assert.equal(ctx.start, "2026-10-23T02:00:00.000Z");
  assert.equal(ctx.end, "2026-10-23T03:00:00.000Z");
  assert.equal(ctx.hours, 1);
  // 缺开始边界：没有上下文，也不发明。
  assert.equal(mod.jumpContext({ turn_id: "x", startMs: null }, "UTC"), null);
});

test("键盘 ↑ ↓ 移动选择，Enter 开详情，Esc 关", async () => {
  const mod = await load();
  let sel = mod.applyKeyToState({ sessions: sessions() }, { selectedId: "sess-1", showDetail: false }, "ArrowDown");
  assert.equal(sel.selectedId, "sess-2");
  sel = mod.applyKeyToState({ sessions: sessions() }, { selectedId: sel.selectedId, showDetail: false }, "Enter");
  assert.equal(sel.showDetail, true);
  sel = mod.applyKeyToState({ sessions: sessions() }, { selectedId: sel.selectedId, showDetail: true }, "Escape");
  assert.equal(sel.showDetail, false);
  // 头部回绕保护：第一行向上不动。
  sel = mod.applyKeyToState({ sessions: sessions() }, { selectedId: "sess-1" }, "ArrowUp");
  assert.equal(sel.selectedId, "sess-1");
});

test("六种状态互不重复且带 data-status", async () => {
  const mod = await load();
  const states = {
    loading: { status: "loading" },
    empty: { status: "ready", sessions: [] },
    error: { status: "error" },
    expired: { status: "expired" },
    unauth: { status: "unauthenticated" },
    unknown: { status: "unknown", sessions: sessions(), completeness: "partial" },
  };
  const seen = new Set();
  for (const [key, state] of Object.entries(states)) {
    const html = mod.renderSessions(state, { lang: "zh" });
    assert.ok(html.length > 0, key);
    assert.ok(html.includes('data-status="'), key);
    assert.ok(!seen.has(html), key + " 与其他状态重复");
    seen.add(html);
  }
});

test("英文界面不残留中文；危险字符转义", async () => {
  const mod = await load();
  const html = mod.renderSessions({
    sessions: [{
      session_id: '<img src=x onerror="alert(1)">',
      source_ns: "codebuddy",
      tasks: [],
      turns: [],
      usage: { present: false },
    }],
    completeness: "complete",
  }, { lang: "en", selectedId: '<img src=x onerror="alert(1)">' });
  assert.ok(html.includes("Source sessions"));
  assert.doesNotMatch(html, /[\u4e00-\u9fa5]/);
  assert.ok(!html.includes("<img"));
  // 完整性随报表回显。
  const withCursor = mod.renderSessions({ sessions: sessions(), completeness: "partial" }, { lang: "zh" });
  assert.ok(withCursor.includes("完整性"));
  assert.ok(withCursor.includes('data-status="unknown"'));
});
