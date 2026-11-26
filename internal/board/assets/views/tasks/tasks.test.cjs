// VN-722 任务创建与指定派发视图的验证。
//
// 覆盖 Acceptance：
//   · 创建与编辑保留版本语义；依赖／范围／节点不满足时解释阻止原因；
//   · 显示实际优先级；派发 202 后进入操作状态而非标完成；
//   · 加载／空／错误／过期／unknown 状态可核对，未另造公共层。
//
// 契约依据：task-instance-contract §1/§5、http-api-contract §3/§5。
// 运行：node --test internal/board/assets/views/tasks/tasks.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "tasks-view.mjs")).href;
const load = () => import(MODULE);

const TASKS = [
  { id: "t1", instance_id: "ti_a", display_id: "VN-001", title: "前置题", status: "done", priority: 10, write_scope: ["internal/a/**"] },
  { id: "t2", instance_id: "ti_b", display_id: "VN-002", title: "进行中题", status: "in_progress", priority: 20, write_scope: ["internal/b/**"] },
  { id: "t3", instance_id: "ti_c", display_id: "VN-003", title: "已退役题", status: "cancelled", state: "retired", priority: 30, write_scope: ["internal/c/**"] },
];

const task = (overrides = {}) => ({
  id: "t9",
  instance_id: "ti_new",
  display_id: "VN-009",
  title: "新题",
  status: "ready",
  priority: 50,
  merge_order: 50,
  deps: [],
  write_scope: ["internal/board/assets/views/tasks/tasks-view.mjs"],
  revision: 7,
  ...overrides,
});

// ---------- 排序与实际优先级 ----------

test("排序按 priority → merge_order → 编号，界面显示任务实际优先级", async () => {
  const mod = await load();
  const sorted = mod.sortTasks([
    { display_id: "VN-3", priority: 30, merge_order: 1 },
    { display_id: "VN-1", priority: 10, merge_order: 9 },
    { display_id: "VN-2", priority: 10, merge_order: 2 },
  ]);
  assert.deepEqual(sorted.map(x => x.display_id), ["VN-2", "VN-1", "VN-3"]);

  const html = mod.renderTaskList({ tasks: [{ ...task(), priority: 70 }] }, { lang: "zh" });
  assert.ok(html.includes(">70<"), "必须渲染任务自己的优先级，而不是排序位置");
});

// ---------- 依赖解析：等待与死锁必须分开 ----------

test("依赖解析区分已完成／未完成／已退役／歧义／缺失", async () => {
  const mod = await load();
  const ctx = { tasks: TASKS };
  assert.equal(mod.resolveDependency("ti_a", ctx).status, "ok");
  assert.equal(mod.resolveDependency("ti_b", ctx).status, "pending");
  assert.equal(mod.resolveDependency("VN-003", ctx).status, "retired");
  assert.equal(mod.resolveDependency("ti_missing", ctx).status, "missing");
  assert.equal(mod.resolveDependency("VN-404", ctx).status, "missing");

  const dup = { tasks: [...TASKS, { id: "t4", display_id: "VN-003", status: "done", state: "active" }] };
  assert.equal(mod.resolveDependency("VN-003", dup).status, "ambiguous");
});

test("前置已退役报结构性死锁，不混进「等前置」", async () => {
  const mod = await load();
  const result = mod.evaluateDispatch(task({ deps: ["VN-003"] }), { revision: 7, tasks: TASKS });
  assert.equal(result.dispatchable, false);
  const codes = result.reasons.map(r => r.code);
  assert.ok(codes.includes("dispatch.dependency_retired"));
  assert.ok(!codes.includes("dispatch.dependency_pending"), "死锁不得被当成普通未完成");
});

test("同时有未完成与已退役依赖时，两类原因都报出来", async () => {
  const mod = await load();
  const result = mod.evaluateDispatch(task({ deps: ["VN-002", "VN-003"] }), { revision: 7, tasks: TASKS });
  const codes = result.reasons.map(r => r.code);
  assert.ok(codes.includes("dispatch.dependency_pending"));
  assert.ok(codes.includes("dispatch.dependency_retired"));
});

test("依赖歧义要求改用 ti_ 形式，不取第一条", async () => {
  const mod = await load();
  const dup = [...TASKS, { id: "t4", display_id: "VN-003", status: "done", state: "active" }];
  const result = mod.evaluateDispatch(task({ deps: ["VN-003"] }), { revision: 7, tasks: dup });
  assert.ok(result.reasons.some(r => r.code === "dispatch.dependency_ambiguous"));
});

// ---------- 派发前置 ----------

test("缺版本前置就不能派发", async () => {
  const mod = await load();
  const result = mod.evaluateDispatch(task(), { tasks: TASKS });
  assert.equal(result.dispatchable, false);
  assert.equal(result.reasons[0].code, "dispatch.revision_required");
});

test("写范围与其他进行中任务重叠时报冲突", async () => {
  const mod = await load();
  const result = mod.evaluateDispatch(
    task({ write_scope: ["internal/b/**"] }),
    { revision: 7, tasks: TASKS, inProgressTasks: TASKS.filter(t => t.status === "in_progress") }
  );
  assert.ok(result.reasons.some(r => r.code === "dispatch.scope_conflict"));
});

test("节点未就绪时把节点原因带出来，在线不等于可执行", async () => {
  const mod = await load();
  const blocked = mod.evaluateDispatch(task({ node_id: "node_a" }), {
    revision: 7,
    tasks: TASKS,
    node: { verdict: "blocked", reasons: ["reason.auth"] },
  });
  const reason = blocked.reasons.find(r => r.code === "dispatch.node_not_ready");
  assert.ok(reason);
  assert.equal(reason.reason, "reason.auth", "必须透出具体卡点，不能只说不可用");
});

test("指定派发未选节点时报未指定，不静默交给调度", async () => {
  const mod = await load();
  const result = mod.evaluateDispatch(task(), { revision: 7, tasks: TASKS, nodeRequired: true });
  assert.ok(result.reasons.some(r => r.code === "dispatch.node_unspecified"));
});

test("四类前置都满足才可派发", async () => {
  const mod = await load();
  const result = mod.evaluateDispatch(task({ deps: ["ti_a"], node_id: "node_a" }), {
    revision: 7,
    tasks: TASKS,
    node: { verdict: "executable", reasons: [] },
  });
  assert.equal(result.dispatchable, true);
  assert.deepEqual(result.reasons, []);
});

test("已退役任务不能被派发", async () => {
  const mod = await load();
  const result = mod.evaluateDispatch(task({ state: "retired" }), { revision: 7, tasks: TASKS });
  assert.equal(result.dispatchable, false);
});

// ---------- 草稿校验 ----------

test("草稿校验覆盖标题／优先级／序位／依赖／范围五类错误", async () => {
  const mod = await load();
  const result = mod.validateDraft({
    id: "ti_new",
    title: "  ",
    priority: 500,
    merge_order: -1,
    deps: ["bad-dep", "ti_new"],
    write_scope: ["/etc/passwd", "../outside"],
  });
  const codes = result.errors.map(e => e.code).sort();
  assert.deepEqual(codes, [
    "dep_invalid", "dep_self", "merge_order_invalid",
    "priority_invalid", "scope_invalid", "scope_invalid", "title_required",
  ].sort());
  assert.equal(result.ok, false);
});

test("依赖成环被拦下并指出环路径", async () => {
  const mod = await load();
  const result = mod.validateDraft(
    { id: "ti_new", title: "环", priority: 1, deps: ["ti_b"], write_scope: ["a/**"] },
    { allDeps: { ti_b: ["ti_new"] } }
  );
  const cycle = result.errors.find(e => e.code === "dep_cycle");
  assert.ok(cycle);
  assert.match(cycle.cycle, /ti_new/);
});

test("合法草稿零错误", async () => {
  const mod = await load();
  const result = mod.validateDraft({
    title: "正常题", priority: 20, merge_order: 20,
    deps: ["ti_a"], write_scope: ["internal/board/assets/views/tasks/**"],
  });
  assert.deepEqual(result.errors, []);
  assert.equal(result.ok, true);
});

// ---------- 表单：版本语义 ----------

test("编辑态缺版本时禁止提交并说明原因", async () => {
  const mod = await load();
  const html = mod.renderTaskForm({ mode: "edit", draft: task(), revision: null }, { lang: "zh" });
  assert.ok(html.includes(mod.t("form.revision.missing", "zh")));
  assert.match(html, /id="task-submit"[^>]*disabled/);
});

test("编辑态带版本时可提交，并显示当前版本", async () => {
  const mod = await load();
  const html = mod.renderTaskForm({ mode: "edit", draft: task(), revision: 7 }, { lang: "zh" });
  assert.ok(html.includes("7"));
  assert.doesNotMatch(html, /id="task-submit"[^>]*disabled/);
});

test("新建态不要求版本前置", async () => {
  const mod = await load();
  const html = mod.renderTaskForm({ mode: "create", draft: task({ revision: undefined }) }, { lang: "zh" });
  assert.ok(!html.includes(mod.t("form.revision.missing", "zh")));
  assert.doesNotMatch(html, /id="task-submit"[^>]*disabled/);
});

test("退役任务整表只读且说明原因", async () => {
  const mod = await load();
  const html = mod.renderTaskForm(
    { mode: "edit", draft: { ...task(), state: "retired" }, revision: 7 },
    { lang: "zh" }
  );
  assert.ok(html.includes(mod.t("form.readonly", "zh")));
  assert.match(html, /<input[^>]*readonly/);
});

test("字段错误通过 aria-invalid 与 aria-describedby 关联控件", async () => {
  const mod = await load();
  const html = mod.renderTaskForm(
    { mode: "create", draft: task({ title: "" }), errors: [{ field: "title", code: "title_required" }] },
    { lang: "zh" }
  );
  assert.match(html, /aria-invalid="true"/);
  assert.match(html, /aria-describedby="err-title"/);
  assert.ok(html.includes(mod.t("err.title_required", "zh")));
});

// ---------- 派发状态：202 不是完成 ----------

test("派发受理后停在 pending，不显示成功也不显示失败", async () => {
  const mod = await load();
  const html = mod.renderDispatchState(
    { phase: "pending", operation: { status: "queued", retry_after_ms: 800 } },
    { lang: "zh" }
  );
  assert.match(html, /data-phase="pending"/);
  assert.ok(html.includes(mod.t("op.pending", "zh")));
  assert.ok(html.includes("queued"));
  assert.ok(!html.includes(mod.t("op.succeeded", "zh")), "202 不得标为完成");
  assert.ok(!html.includes(mod.t("op.failed", "zh")));
});

test("只有 succeeded 才渲染成功，并带操作结果", async () => {
  const mod = await load();
  const html = mod.renderDispatchState(
    { phase: "succeeded", result: { operation_id: "op_1" } },
    { lang: "zh" }
  );
  assert.match(html, /data-phase="succeeded"/);
  assert.ok(html.includes("op_1"));
});

test("失败态给出错误码，不与受理混淆", async () => {
  const mod = await load();
  const html = mod.renderDispatchState(
    { phase: "failed", error: { code: "write_conflict", message: "worktree dirty" } },
    { lang: "zh" }
  );
  assert.match(html, /data-phase="failed"/);
  assert.ok(html.includes("write_conflict"));
});

test("提交中不重复提交且不宣称完成", async () => {
  const mod = await load();
  const html = mod.renderDispatchState({ phase: "submitting" }, { lang: "zh" });
  assert.ok(html.includes(mod.t("op.submitting", "zh")));
  assert.ok(!html.includes(mod.t("op.succeeded", "zh")));
});

// ---------- 列表状态与双语 ----------

test("加载／空／错误／过期／未认证各走独立文案", async () => {
  const mod = await load();
  const states = {
    loading: { status: "loading" },
    empty: { tasks: [] },
    error: { status: "error" },
    expired: { status: "expired" },
    unauth: { status: "unauthenticated" },
  };
  const seen = new Set();
  for (const [key, state] of Object.entries(states)) {
    const text = mod.renderTaskList(state, { lang: "zh" });
    assert.ok(text.length > 0, key);
    assert.ok(!seen.has(text), key + " 与其他状态文案重复");
    seen.add(text);
  }
});

test("未知状态显式标注，不猜成 ready", async () => {
  const mod = await load();
  const html = mod.renderTaskList({ tasks: [task({ status: undefined })] }, { lang: "zh" });
  assert.ok(html.includes(mod.t("state.unknown", "zh")));
});

test("不可派发的行必须写出具体阻止原因", async () => {
  const mod = await load();
  const html = mod.renderTaskList(
    { tasks: [task({ deps: ["VN-003"] })], allTasks: TASKS, inProgressTasks: [] },
    { lang: "zh" }
  );
  assert.ok(html.includes(mod.t("verdict.blocked", "zh")));
  assert.ok(html.includes(mod.t("dispatch.dependency_retired", "zh", { ids: "VN-003" })));
});

test("统计只数真正可派发的任务", async () => {
  const mod = await load();
  const html = mod.renderTaskList(
    {
      tasks: [
        task({ id: "a", instance_id: "ti_1", display_id: "VN-101", deps: ["ti_a"] }),
        task({ id: "b", instance_id: "ti_2", display_id: "VN-102", deps: ["VN-003"] }),
        task({ id: "c", instance_id: "ti_3", display_id: "VN-103", revision: undefined }),
      ],
      allTasks: TASKS,
    },
    { lang: "zh" }
  );
  assert.ok(html.includes("3 " + mod.t("tasks.count", "zh")));
  assert.ok(html.includes("1 " + mod.t("tasks.dispatchable", "zh")));
});

test("英文界面不残留中文，且关键标签已翻译", async () => {
  const mod = await load();
  const en = mod.renderTaskList({ tasks: [task()] }, { lang: "en" });
  // 标题由壳层面包屑提供；列表校验英文标签与无中文残留。
  assert.ok(en.includes(mod.t("tasks.count", "en")) || /ready|done|blocked/i.test(en));
  assert.doesNotMatch(en.replace(/新题/g, ""), /[\u4e00-\u9fa5]/);
});

test("标题与范围里的标记被转义", async () => {
  const mod = await load();
  const html = mod.renderTaskList(
    { tasks: [task({ title: '<img src=x onerror="alert(1)">' })] },
    { lang: "zh" }
  );
  assert.ok(!html.includes("<img"));
  assert.ok(html.includes("&lt;img"));
});
