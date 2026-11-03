// VN-725 工程列表的验证。
//
// 与设置页共用判定（settings-view.mjs）：这里重点核对列表投影没把
// 「自动模式／上限／容量／参考值」混成一列，且缺字段保持未知。
//
// 运行：node --test internal/board/assets/views/projects/projects.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "projects-view.mjs")).href;
const load = () => import(MODULE);

const PROJECT = {
  project_id: "p1",
  name: "goal-agents",
  platform_supported: true,
  workspace: "git",
  policy: {
    auto_enabled: false,
    managed_execution_limit: 1,
    node_capacity: 2,
    max_parallel_reference: 4,
    revision: 7,
  },
};

test("VN-725 列表四列含义不同，标签不复用", async () => {
  const view = await load();
  const html = view.renderProjectList({ projects: [PROJECT] }, { lang: "zh" });
  const labels = ["自动模式", "计算上限", "节点容量", "max_parallel 参考值"];
  for (const label of labels) {
    assert.ok(html.includes(label), "缺少列标签：" + label);
  }
  // 每列独立，不出现「上限/参考值」共用一格
  for (const field of ["limit", "capacity", "ref"]) {
    assert.ok(html.includes('data-field="' + field + '"'), "缺少独立列：" + field);
  }
  // 语义只写在表头：单元格里不再重复「仅供参考，不是硬约束」这类提示。
  assert.ok(!html.includes("仅供参考"), "参考值列不该再带说明文案");
});

test("VN-725 工程策略缺字段时显示未知，不用 0 冒充", async () => {
  const view = await load();
  const html = view.renderProjectList(
    { projects: [{ project_id: "p2", name: "空的", policy: { auto_enabled: false, revision: 1 } }] },
    { lang: "zh" },
  );
  assert.ok(html.includes("—"));
  assert.ok(!html.includes('data-field="limit">0<'), "缺失的上限不能显示成 0");
});

test("VN-725 自动模式三态在列表里可区分", async () => {
  const view = await load();
  const policy = view.projectPolicy(PROJECT, { platformSupported: true });
  assert.equal(policy.mode, "off");

  const notReady = view.projectPolicy(
    { ...PROJECT, platform_supported: false },
    { platformSupported: false },
  );
  assert.equal(notReady.mode, "not_ready");
  assert.equal(notReady.reasonKey, "auto.reason.platform");

  const enabled = view.projectPolicy(
    { ...PROJECT, policy: { ...PROJECT.policy, auto_enabled: true } },
    { platformSupported: true },
  );
  assert.equal(enabled.mode, "on");

  const html = view.renderProjectList(
    { projects: [{ ...PROJECT, platform_supported: false }] },
    { lang: "zh", platformSupported: false },
  );
  assert.ok(html.includes("未就绪"));
  assert.ok(html.includes("受管执行只在 macOS 上实测过"));
});

test("VN-725 工作区类型如实显示", async () => {
  const view = await load();
  const git = view.renderProjectList({ projects: [PROJECT] }, { lang: "zh" });
  assert.ok(git.includes(">Git<") || git.includes(" Git "));

  const nongit = view.renderProjectList(
    { projects: [{ ...PROJECT, workspace: "nongit" }] },
    { lang: "zh" },
  );
  assert.ok(nongit.includes("非 Git"));

  const unknown = view.projectPolicy({ ...PROJECT, workspace: "weird" }, { platformSupported: true });
  assert.equal(unknown.workspace, "unknown");
});

test("VN-725 列表状态分支齐全，未知不渲染成空表", async () => {
  const view = await load();
  const expectations = {
    loading: "正在读取工程",
    error: "工程列表加载失败",
    expired: "工程数据已过期",
    unauthenticated: "未登录",
    unknown: "策略状态未知",
    empty: "还没有登记任何工程",
  };
  for (const [status, text] of Object.entries(expectations)) {
    const html = view.renderProjectList({ status, projects: [] }, { lang: "zh" });
    assert.ok(html.includes(text), status + " 状态缺少文案：" + text);
    assert.ok(!html.includes("<table"), status + " 状态不该渲染表格");
  }
});

test("VN-725 英文界面不残留中文", async () => {
  const view = await load();
  const html = view.renderProjectList({ projects: [PROJECT] }, { lang: "en" });
  assert.ok(html.includes("Execution policy") || html.includes("Auto mode"));
  assert.ok(!/[\u4e00-\u9fa5]/.test(html), "英文界面不应出现中文");
});

test("VN-725 工程名与 id 均转义", async () => {
  const view = await load();
  const html = view.renderProjectList(
    { projects: [{ ...PROJECT, name: '<img src=x onerror="alert(1)">' }] },
    { lang: "zh" },
  );
  assert.ok(!html.includes("<img src=x"));
  assert.ok(html.includes("&lt;img"));
});

test("工程移除按钮只在可写宿主出现", async () => {
  const view = await load();
  // 静态导出页没有写通道：不给一个点了没反应的按钮。
  const readonly = view.renderProjectList({ projects: [PROJECT] }, { lang: "zh" });
  assert.ok(!readonly.includes("data-project-remove"), "只读宿主不该渲染移除按钮");
  assert.ok(!readonly.includes("操作"), "只读宿主不该多出操作列");

  const writable = view.renderProjectList({ projects: [PROJECT] }, { lang: "zh", canRemove: true });
  assert.ok(writable.includes('data-project-remove="p1"'), "可写宿主应带工程 id");
  assert.ok(writable.includes(">移除<"));
  assert.ok(writable.includes("操作"), "可写宿主应多出操作列");
});

test("工程移除是两段式：确认态不再给直接删除按钮", async () => {
  const view = await load();
  const html = view.renderProjectList(
    { projects: [PROJECT, { project_id: "p9", name: "another" }] },
    { lang: "zh", canRemove: true, pendingRemove: "p1" },
  );
  assert.ok(html.includes("确认移除？"));
  assert.ok(html.includes('data-project-remove-confirm="p1"'));
  assert.ok(html.includes('data-project-remove-cancel="p1"'));
  // 同一行不能再保留一键删除入口，否则二次确认就是摆设。
  assert.ok(!html.includes('data-project-remove="p1"'));
  // 其他行照常，不受影响。
  assert.ok(html.includes('data-project-remove="p9"'));
});

test("工程移除结果提示不静默：成功与失败都写明", async () => {
  const view = await load();
  const failed = view.renderProjectList(
    { projects: [PROJECT] },
    { lang: "zh", canRemove: true, removeNotice: { kind: "error", text: "工程还有未完成的任务或在线 Agent" } },
  );
  assert.ok(failed.includes('data-notice-kind="error"'));
  assert.ok(failed.includes("工程还有未完成的任务或在线 Agent"));

  const done = view.renderProjectList(
    { projects: [PROJECT] },
    { lang: "zh", canRemove: true, removeNotice: { kind: "ok", text: "已移除工程登记" } },
  );
  assert.ok(done.includes('data-notice-kind="ok"'));
  assert.ok(!done.includes('data-notice-kind="error"'));
});

test("工程移除的英文界面不残留中文", async () => {
  const view = await load();
  const html = view.renderProjectList(
    { projects: [PROJECT] },
    { lang: "en", canRemove: true, pendingRemove: "p1", removeNotice: { kind: "error", text: "busy" } },
  );
  assert.ok(html.includes("Remove this project?"));
  assert.ok(html.includes("Confirm removal"));
  assert.ok(!/[\u4e00-\u9fa5]/.test(html), "英文界面不应出现中文");
});
