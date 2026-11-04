// BP-STA-001 问题页的验证。
//
// 这一页的输入是 sources.problems 提炼好的信号条目（blocked / run / job / agent），
// 测试只盯渲染契约与「缺数据不等于没有」这两件事：
//   - 四种状态（loading / error / 空 / 有数据）各自可辨，失败态不画空表；
//   - 类型词走本地词表，未知类型原样呈现而不是吞成空串；
//   - 条目字段按文本转义，快照里的标题与原因不能被拼进 HTML。
//
// 运行：node --test internal/board/assets/views/problems/problems.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "problems-view.mjs")).href;
const load = () => import(MODULE);

const ITEMS = [
  { kind: "blocked", ref: "BP-STA-001", detail: "write_scope 覆盖不到验收项" },
  { kind: "run", ref: "run_u98m2o2pfwez", detail: "failed / unconfirmed" },
  { kind: "agent", ref: "codebuddy-goalagents-14", detail: "offline while holding BP-STA-001" },
  { kind: "job", ref: "job_reconcile", detail: "retry exhausted" },
];

test("BP-STA-001 问题页把四类信号都画成行，并带上条数", async () => {
  const view = await load();
  const html = view.renderProblems({ status: "ok", items: ITEMS }, { lang: "zh" });
  assert.equal(html.includes("4 条问题"), true);
  // 类型词来自本地词表，不是后端英文原样透传。
  ["阻塞任务", "运行异常", "Agent", "系统作业"].forEach(label => {
    assert.equal(html.includes(label), true, "缺少类型词：" + label);
  });
  ITEMS.forEach(item => {
    assert.equal(html.includes(item.ref), true, "缺少对象：" + item.ref);
    assert.equal(html.includes(item.detail), true, "缺少说明：" + item.detail);
  });
  assert.equal(html.includes("<table"), true);
});

test("BP-STA-001 未知类型原样呈现，不吞成空单元格", async () => {
  const view = await load();
  const html = view.renderProblems({ status: "ok", items: [{ kind: "quarantine", ref: "x", detail: "y" }] }, { lang: "zh" });
  assert.equal(html.includes("quarantine"), true);
});

test("BP-STA-001 空态、失败态与登录态各自可辨", async () => {
  const view = await load();
  const empty = view.renderProblems({ status: "ok", items: [] }, { lang: "zh" });
  assert.equal(empty.includes("当前没有可报告的运行问题"), true);
  assert.equal(empty.includes("<table"), false);

  // 失败与未登录共用一个提示，但都必须显式报错，不能渲染成空表。
  const failed = view.renderProblems({ status: "error" }, { lang: "zh" });
  assert.equal(failed.includes("问题列表加载失败"), true);
  assert.equal(failed.includes("<table"), false);

  const noAuth = view.renderProblems({ status: "unauthenticated" }, { lang: "en" });
  assert.equal(noAuth.includes("Failed to load problems"), true);

  const loading = view.renderProblems({ status: "loading" }, { lang: "zh" });
  assert.equal(loading.includes("正在汇总问题"), true);
  assert.equal(loading.includes("<table"), false);
});

test("BP-STA-001 条目字段按文本转义", async () => {
  const view = await load();
  const html = view.renderProblems({
    status: "ok",
    items: [{ kind: "blocked", ref: "<i>t</i>", detail: '<script>alert(1)</script>' }],
  }, { lang: "zh" });
  assert.equal(html.includes("<script>"), false);
  assert.equal(html.includes("&lt;script&gt;"), true);
  assert.equal(html.includes("&lt;i&gt;t&lt;/i&gt;"), true);
});

test("BP-STA-001 英文界面用英文列名与类型词", async () => {
  const view = await load();
  const html = view.renderProblems({ status: "ok", items: ITEMS }, { lang: "en" });
  ["Kind", "Subject", "Detail"].forEach(label => {
    assert.equal(html.includes(">" + label + "<"), true, "缺少英文列名：" + label);
  });
  assert.equal(html.includes("Blocked task"), true);
  assert.equal(html.includes("4 problems"), true);
  // 空态没有业务数据，整段必须是英文——词表漏译在这里最容易露出来。
  const empty = view.renderProblems({ status: "ok", items: [] }, { lang: "en" });
  assert.equal(/[\u4e00-\u9fa5]/.test(empty), false, "英文空态不该出现中文词条");
});
