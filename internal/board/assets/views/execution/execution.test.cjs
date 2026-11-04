// EV-001 执行视图的验证。
//
// 覆盖 Acceptance：
//   · 选中迭代可见任务／运行／交付三类摘要；
//   · 点任务开抽屉（行上带 data-task-id / data-project-id，由宿主开抽屉）；
//   · 无迭代时有明确空态（不是空表格）。
//
// 另外钉住两条容易出错的边界：
//   · 运行／交付没有迭代字段，只能经 task_instance 归组；对不上任务的不猜迭代；
//   · 三类数据缺一类就是加载失败，不渲染成「0 次运行」。
//
// 运行：node --test internal/board/assets/views/execution/execution.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "execution-view.mjs")).href;
const load = () => import(MODULE);

function task(display, instance, iteration, status, extra = {}) {
  return {
    display_id: display,
    task_instance: instance,
    iteration_id: iteration,
    title: "task " + display,
    status,
    owner: "agent-1",
    project_id: "p-1",
    ...extra,
  };
}

function fixture() {
  const tasks = [
    task("EV-001", "ti_a", "it-1", "in_progress"),
    task("EV-002", "ti_b", "it-1", "done"),
    task("EV-003", "ti_c", "it-1", "blocked"),
    task("IT-005", "ti_d", "it-2", "ready"),
  ];
  const runs = [
    { run_id: "run_1", task_instance: "ti_a", phase: "running", occupies: true },
    { run_id: "run_2", task_instance: "ti_b", phase: "completed", occupies: false },
    // 找不到对应任务的运行：不归入任何迭代。
    { run_id: "run_x", task_instance: "ti_zzz", phase: "running", occupies: true },
  ];
  const deliveries = [
    { delivery_id: "dl_1", task_instance: "ti_b", mode: "merge", delivered_at: "2026-11-01T10:00:00Z" },
    { delivery_id: "dl_2", task_instance: "ti_b", mode: "merge", delivered_at: "2026-11-02T10:00:00Z" },
  ];
  return { status: "ok", tasks, runs, deliveries };
}

test("EV-001 按迭代分组：任务归组，运行／交付经 task_instance 归组", async () => {
  const view = await load();
  const groups = view.iterationGroupsOf(fixture());

  assert.deepEqual(groups.map(g => g.id), ["it-1", "it-2"]);
  assert.deepEqual(groups[0].tasks.map(t => t.display_id), ["EV-001", "EV-002", "EV-003"]);
  // run_x 的实例不在任务里：不猜迭代，也不新建空迭代。
  assert.deepEqual(groups[0].runs.map(r => r.run_id), ["run_1", "run_2"]);
  assert.deepEqual(groups[0].deliveries.map(d => d.delivery_id), ["dl_1", "dl_2"]);
  assert.deepEqual(groups[1].runs, []);
  assert.deepEqual(groups[1].deliveries, []);
});

test("EV-001 摘要计数分开算：任务按状态，运行按是否仍占资源", async () => {
  const view = await load();
  const group = view.iterationGroupsOf(fixture())[0];
  assert.equal(group.counts.tasks, 3);
  assert.equal(group.counts.active, 1);
  assert.equal(group.counts.blocked, 1);
  assert.equal(group.counts.done, 1);
  assert.equal(group.counts.runs, 2);
  assert.equal(group.counts.occupying, 1, "已完成的运行不再占资源");
  assert.equal(group.counts.deliveries, 2);
  assert.equal(group.counts.latestDelivery, "2026-11-02T10:00:00Z", "最近一次交付取最大值");
});

test("EV-001 选中迭代可见三类摘要，默认选第一个", async () => {
  const view = await load();
  const state = fixture();
  assert.equal(view.selectedIterationOf(state, view.iterationGroupsOf(state)), "it-1");

  const html = view.renderExecution(state, { lang: "zh" });
  assert.match(html, /data-iteration-panel="it-1"/);
  assert.ok(html.includes('data-summary="tasks"'));
  assert.ok(html.includes('data-summary="runs"'));
  assert.ok(html.includes('data-summary="deliveries"'));
  // 三个分区标题都在
  for (const title of ["任务", "运行", "交付"]) {
    assert.ok(html.includes(">" + title + "<"), "缺少分区：" + title);
  }
  // 选中态只有一个
  assert.equal((html.match(/is-active/g) || []).length, 1);
  // 第二个迭代的摘要不该混进来
  assert.ok(!html.includes('data-iteration-panel="it-2"'));
});

test("EV-001 切换迭代只换面板，不丢另一个迭代的入口", async () => {
  const view = await load();
  const state = { ...fixture(), selectedIteration: "it-2" };
  const html = view.renderExecution(state, { lang: "zh" });

  assert.match(html, /data-iteration-panel="it-2"/);
  assert.match(html, /data-iteration="it-1"/, "另一个迭代仍要能点回去");
  assert.match(html, /data-iteration="it-2"[^>]*aria-pressed="true"/);
  // 选中的迭代变了，任务行也跟着变。
  assert.ok(html.includes("IT-005"));
  assert.ok(!html.includes("EV-001"));
});

test("EV-001 不存在的选中值退回第一个迭代，不渲染空面板", async () => {
  const view = await load();
  const state = { ...fixture(), selectedIteration: "it-404" };
  assert.equal(view.selectedIterationOf(state, view.iterationGroupsOf(state)), "it-1");
  assert.match(view.renderExecution(state, { lang: "zh" }), /data-iteration-panel="it-1"/);
});

test("EV-001 没选迭代时默认第一组，别把空串当成「未归属」", async () => {
  const view = await load();
  const state = {
    status: "ok",
    tasks: [
      task("EV-001", "ti_a", "it-1", "in_progress"),
      task("CQ-001", "ti_b", "", "done"),
    ],
    runs: [],
    deliveries: [],
  };
  const groups = view.iterationGroupsOf(state);
  // 空串这一组是「未归属迭代」，它排在最后，不是默认选中项。
  assert.deepEqual(groups.map(g => g.id), ["it-1", ""]);
  assert.equal(view.selectedIterationOf(state, groups), "it-1", "没给选中值时不能落到未归属组");
  assert.match(view.renderExecution(state, { lang: "zh" }), /data-iteration-panel="it-1"/);

  // 显式选「未归属」仍然可点：空串是合法 id，不是「没选」。
  const unassigned = { ...state, selectedIteration: "" };
  assert.equal(view.selectedIterationOf(unassigned, groups), "");
  const html = view.renderExecution(unassigned, { lang: "zh" });
  assert.match(html, /data-iteration-panel=""/);
  assert.match(html, /data-unassigned="1"/);
  assert.ok(!html.includes("EV-001"), "未归属面板里不该出现别的迭代的任务");
});

test("EV-001 任务行带抽屉钩子：data-task-id 用展示编号，不暴露 instance", async () => {
  const view = await load();
  const html = view.renderExecution(fixture(), { lang: "zh" });
  assert.match(html, /<tr class="row-clickable"[^>]*data-task-id="EV-001"/);
  assert.match(html, /data-task-id="EV-002"[^>]*data-project-id="p-1"|data-project-id="p-1"[^>]*data-task-id="EV-002"/);
  // 抽屉按展示编号查任务；把 ti_ 当成 id 会查不到。
  assert.ok(!/data-task-id="ti_/.test(html), "抽屉钩子不能用 instance_id");
  assert.ok(html.includes('tabindex="0"'), "行要能键盘聚焦");
});

test("EV-001 没有迭代时给明确空态，不渲染空表格", async () => {
  const view = await load();
  const html = view.renderExecution({ status: "ok", tasks: [], runs: [], deliveries: [] }, { lang: "zh" });

  assert.ok(html.includes('data-state="empty"'));
  assert.ok(html.includes("这个工程还没有迭代"));
  assert.ok(html.includes("maw plan apply"), "空态要指出下一步");
  assert.ok(!html.includes("<table"), "没有迭代时不渲染空表格");
});

test("EV-001 未归属迭代的任务单独成组并说明后果", async () => {
  const view = await load();
  const state = { status: "ok", tasks: [task("CQ-001", "ti_x", "", "ready")], runs: [], deliveries: [] };
  const groups = view.iterationGroupsOf(state);
  assert.deepEqual(groups.map(g => g.id), [""]);

  const html = view.renderExecution(state, { lang: "zh" });
  assert.ok(html.includes("未归属迭代"));
  assert.ok(html.includes('data-unassigned="1"'));
  assert.ok(html.includes("不会出现在迭代收尾里"), "要说清后果，而不是只给个空标签");
});

test("EV-001 各类加载态各有独立分支，缺数据不冒充空结果", async () => {
  const view = await load();
  const cases = {
    loading: "正在读取执行视图",
    error: "执行视图加载失败",
    expired: "执行数据已过期",
    unauthenticated: "未登录",
    no_project: "先选一个工程",
  };
  for (const [status, text] of Object.entries(cases)) {
    const html = view.renderExecution({ status }, { lang: "zh" });
    assert.ok(html.includes(text), status + " 缺少文案：" + text);
    assert.ok(!html.includes("<table"), status + " 不应渲染表格");
  }
  // 缺 runs 却报 ok：属于上游出错，这里不能补 0 次运行。
  const missing = view.renderExecution({ status: "ok", tasks: [task("A-1", "ti_a", "it-1", "ready")] }, { lang: "zh" });
  assert.ok(missing.includes('data-summary="runs"'));
  assert.ok(missing.includes("该迭代下暂无运行记录"), "缺数据只说明暂无，不编造次数");
});

test("EV-001 英文界面不残留中文且三类摘要齐全", async () => {
  const view = await load();
  const html = view.renderExecution(fixture(), { lang: "en" });
  assert.ok(!/[\u4e00-\u9fa5]/.test(html), "英文界面不得出现中文");
  assert.ok(html.includes("Tasks") && html.includes("Runs") && html.includes("Deliveries"));
  assert.ok(html.includes("still occupying"));
});

test("EV-001 标题与编号经过转义，不把数据当 HTML", async () => {
  const view = await load();
  const state = {
    status: "ok",
    tasks: [task("EV-001", "ti_a", "it-1", "ready", { title: '<script>alert(1)</script>' })],
    runs: [],
    deliveries: [],
  };
  const html = view.renderExecution(state, { lang: "zh" });
  assert.ok(!html.includes("<script>alert(1)</script>"));
  assert.ok(html.includes("&lt;script&gt;alert(1)&lt;/script&gt;"));
});
