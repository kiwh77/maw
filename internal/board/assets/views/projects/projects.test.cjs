// 工程清单的验证。
//
// 这一页回答的是「本机登记了哪些工程、各自还剩多少活、最近谁在动」，
// 所以测试重点有三条：
//   1. 列信息必须都是快照真读得到的字段（任务数、主分支、参考值、最近活动、路径）；
//      按工程的执行策略读不到，就不能占列，也不能逐行渲染成「未就绪」。
//   2. 缺字段写占位符，不拿 0 或「刚刚」冒充；任务数没取到不能显示成 0。
//   3. 工程名／路径截断必须带 title，截断不是丢掉完整值的理由。
//
// 运行：node --test internal/board/assets/views/projects/projects.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "projects-view.mjs")).href;
const load = () => import(MODULE);

const NOW = Date.parse("2026-11-04T06:00:00Z");

const PROJECT = {
  id: "p1",
  name: "goal-agents",
  path: "/Users/me/Projects/goal-agents",
  main_branch: "main",
  max_parallel: 4,
  last_seen: "2026-11-04T05:30:00Z",
};

const STATE = {
  projects: [PROJECT],
  taskCounts: { p1: { total: 130, running: 3 } },
};

function render(state = STATE, options = {}) {
  return load().then(view => view.renderProjectList(state, { lang: "zh", now: NOW, ...options }));
}

test("列信息只列快照读得到的字段，策略不再逐行渲染", async () => {
  const html = await render();
  for (const label of ["工程", "任务", "主分支", "max_parallel 参考值", "最近活动"]) {
    assert.ok(html.includes(label), "缺少列标签：" + label);
  }
  // 自动模式／计算上限／节点容量在快照里没有按工程的值：不占列，也不编造结论。
  for (const gone of ["自动模式", "计算上限", "节点容量"]) {
    assert.ok(!html.includes("<th" + ' class="projects-th--num">' + gone), "不该再出现的数字列：" + gone);
    assert.ok(!html.includes("<th>" + gone), "不该再出现的列：" + gone);
  }
  assert.ok(!html.includes("未就绪"), "不再逐行渲染「未就绪」");
  assert.ok(!html.includes("工作区"), "工作区类型快照不带，不占列");
});

test("任务数按工程归组，进行中只在有人跑时出现", async () => {
  const html = await render();
  assert.ok(html.includes('data-field="tasks"'), "缺少任务列");
  assert.ok(html.includes(">130<"), "任务总数应来自 state.taskCounts");
  assert.ok(html.includes("进行中 3"), "有进行中任务时应写明数量");

  const idle = await render({ projects: [PROJECT], taskCounts: { p1: { total: 7, running: 0 } } });
  assert.ok(idle.includes(">7<"));
  assert.ok(!idle.includes("projects-sub--running"), "没有进行中任务时不加第二行");
});

test("任务数没取到写占位符，不渲染成 0 道任务", async () => {
  const html = await render({ projects: [PROJECT] });
  assert.ok(html.includes("任务数未知"), "缺少「未知」说明");
  assert.ok(!html.includes("0 道任务"), "没取到不能写成 0");
  assert.ok(!html.includes(">0<"), "单元格不能出现 0");
  assert.ok(html.includes("—"), "单元格应写占位符");
});

test("页首一行汇总工程数、有任务数、任务量与进行中", async () => {
  const html = await render({
    projects: [PROJECT, { ...PROJECT, id: "p2", name: "idle" }],
    taskCounts: { p1: { total: 5, running: 2 } },
  });
  assert.ok(html.includes("2 个工程"));
  assert.ok(html.includes("1 个有任务"), "没任务的工程不计入「有任务」");
  assert.ok(html.includes("5 道任务"));
  assert.ok(html.includes("进行中 2"));
});

test("参考值 0 是「未设置」，缺字段才是占位符", async () => {
  const view = await load();
  assert.equal(view.projectRow({ id: "a", max_parallel: 0 }).ref, 0);
  assert.equal(view.projectRow({ id: "a" }).ref, null);
  // 快照里叫 max_parallel，视图层叫 max_parallel_reference，两者是同一个建议并行数。
  assert.equal(view.projectRow({ id: "a", max_parallel_reference: 6 }).ref, 6);

  const zero = await render({ projects: [{ ...PROJECT, max_parallel: 0 }] });
  assert.ok(zero.includes("未设置"), "0 表示没有配置建议并行数");

  const missing = await render({ projects: [{ id: "p9", name: "无字段" }] });
  assert.ok(!missing.includes("未设置"), "字段缺失时不能断言「未设置」");
  assert.ok(missing.includes('data-field="ref">—<'), "缺失写占位符");
});

test("最近活动是相对时间，悬停保留原始时间戳", async () => {
  const view = await load();
  assert.equal(view.relativeTime("2026-11-04T05:59:30Z", "zh", { now: NOW }), "刚刚");
  assert.equal(view.relativeTime("2026-11-04T05:30:00Z", "zh", { now: NOW }), "30 分钟前");
  assert.equal(view.relativeTime("2026-11-04T03:00:00Z", "zh", { now: NOW }), "3 小时前");
  assert.equal(view.relativeTime("2026-11-02T06:00:00Z", "zh", { now: NOW }), "2 天前");
  assert.equal(view.relativeTime("2026-09-01T06:00:00Z", "zh", { now: NOW }), "2026-09-01");
  assert.equal(view.relativeTime("", "zh", { now: NOW }), null);

  const html = await render();
  assert.ok(html.includes("30 分钟前"));
  assert.ok(html.includes('title="2026-11-04T05:30:00Z"'), "完整时间不能丢");

  const noSeen = await render({ projects: [{ id: "p9", name: "无 last_seen" }] });
  assert.ok(noSeen.includes('data-field="seen">—<'));
});

test("工程名与路径允许截断，但必须带 title", async () => {
  const html = await render();
  assert.ok(html.includes('title="goal-agents"'), "工程名要能悬停看全值");
  assert.ok(html.includes('title="/Users/me/Projects/goal-agents"'), "路径要能悬停看全值");
  assert.ok(html.includes('class="projects-path"'), "路径是名称下方的第二行");
});

test("主分支缺字段写占位符，不写「未知分支」这类结论", async () => {
  const html = await render({ projects: [{ id: "p9", name: "无分支" }] });
  assert.ok(html.includes('data-field="branch">—<'));
});

test("列表状态分支齐全，未知不渲染成空表", async () => {
  const view = await load();
  const expectations = {
    loading: "正在读取工程",
    error: "工程列表加载失败",
    expired: "工程数据已过期",
    unauthenticated: "未登录",
    unknown: "工程状态未知",
    empty: "还没有登记任何工程",
  };
  for (const [status, text] of Object.entries(expectations)) {
    const html = view.renderProjectList({ status, projects: [] }, { lang: "zh", now: NOW });
    assert.ok(html.includes(text), status + " 状态缺少文案：" + text);
    assert.ok(!html.includes("<table"), status + " 状态不该渲染表格");
  }
});

test("英文界面不残留中文", async () => {
  const view = await load();
  const html = view.renderProjectList(STATE, { lang: "en", now: NOW });
  assert.ok(html.includes("Main branch"));
  assert.ok(html.includes("Last active"));
  assert.ok(!/[\u4e00-\u9fa5]/.test(html), "英文界面不应出现中文");
});

test("工程名与路径均转义", async () => {
  const html = await render({
    projects: [{ id: "p1", name: '<img src=x onerror="alert(1)">', path: "<script>x</script>" }],
    taskCounts: { p1: { total: 1, running: 0 } },
  });
  assert.ok(!html.includes("<img src=x"));
  assert.ok(!html.includes("<script>"));
  assert.ok(html.includes("&lt;img"));
});

test("操作列只在可写宿主出现，移除仍是两段式", async () => {
  const view = await load();
  const readonly = view.renderProjectList(STATE, { lang: "zh", now: NOW });
  assert.ok(!readonly.includes("data-project-remove"), "只读宿主不该渲染移除按钮");
  assert.ok(!readonly.includes(">操作<"), "只读宿主不该多出操作列");

  const writable = view.renderProjectList(STATE, { lang: "zh", now: NOW, canRemove: true });
  assert.ok(writable.includes('data-project-remove="p1"'));
  assert.ok(writable.includes('data-project-edit="p1"'));
  assert.ok(writable.includes(">操作<"));

  const pending = view.renderProjectList(STATE, { lang: "zh", now: NOW, canRemove: true, pendingRemove: "p1" });
  assert.ok(pending.includes("确认移除？"));
  assert.ok(pending.includes('data-project-remove-confirm="p1"'));
  assert.ok(pending.includes('data-project-remove-cancel="p1"'));
  // 同一行不能再保留一键删除入口，否则二次确认就是摆设。
  assert.ok(!pending.includes('data-project-remove="p1"'));
});

test("移除结果提示不静默：成功与失败都写明", async () => {
  const view = await load();
  const failed = view.renderProjectList(STATE, {
    lang: "zh", now: NOW, canRemove: true,
    removeNotice: { kind: "error", text: "工程还有未完成的任务或在线 Agent" },
  });
  assert.ok(failed.includes('data-notice-kind="error"'));
  assert.ok(failed.includes("工程还有未完成的任务或在线 Agent"));

  const done = view.renderProjectList(STATE, {
    lang: "zh", now: NOW, canRemove: true,
    removeNotice: { kind: "ok", text: "已移除工程登记" },
  });
  assert.ok(done.includes('data-notice-kind="ok"'));
  assert.ok(!done.includes('data-notice-kind="error"'));
});

test("页首说明策略归属，不把策略说成列表里的数", async () => {
  const html = await render();
  assert.ok(html.includes("config.toml"), "要说明策略写在各工程 config.toml");
  assert.ok(html.includes("不回显"), "要说明本列表不回显策略");
});

test("操作列用页面私有类名，避开 legacy 的 .actions flex 底栏", async () => {
  const view = await load();
  const html = view.renderProjectList(STATE, { lang: "zh", now: NOW, canRemove: true });
  assert.ok(html.includes('class="projects-actions"'));
  assert.ok(!html.includes('<td class="actions"'), "legacy .actions 会把 td 变成 flex，列会错位");
});
