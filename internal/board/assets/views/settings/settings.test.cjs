// VN-725 工程策略与保留设置页的验证。
//
// 覆盖 Acceptance：
//   · 自动模式／计算上限／节点容量／max_parallel 参考值四种标签不同，且默认不开启自动执行；
//   · 保存状态机（脏／保存中／已保存／失败／冲突）语义明确，冲突保留草稿；
//   · 无秘密字段入口：草稿里出现 token／password 一类键就拒绝渲染与保存；
//   · F47／F57 的六类收敛项各有分区，超时与重试、通知只声明真实入口不放控件。
// 视觉（深浅／双语／窄屏／键盘）由同目录浏览器用例负责。
//
// 运行：node --test internal/board/assets/views/settings/settings.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "settings-view.mjs")).href;
const load = () => import(MODULE);

const POLICY = {
  auto_enabled: false,
  managed_execution_limit: 1,
  node_capacity: 2,
  max_parallel_reference: 4,
  retention: { days: 30, runs: 50 },
  revision: 7,
};

function state(overrides = {}) {
  return {
    status: "ready",
    policy: null,
    save: "idle",
    ...overrides,
  };
}

test("VN-725 自动模式默认关闭，且不与上限／容量／参考值混为一个数", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  assert.equal(policy.autoEnabled, false);
  assert.equal(view.autoModeOf(policy, { platformSupported: true, configEnabled: false }), "off");

  const html = view.renderSettings(state({ policy, platformSupported: true }), { lang: "zh" });
  // 四个标签各自出现且文案不同
  for (const label of ["自动模式", "计算上限", "节点容量", "max_parallel 参考值"]) {
    assert.ok(html.includes(label), "缺少标签：" + label);
  }
  assert.ok(html.includes("仅供参考"), "参考值必须标注仅供参考");
  assert.ok(html.includes("硬上限"), "上限必须标注为硬约束");
  // 默认关闭不能渲染成已开启
  assert.ok(html.includes(">关闭<"), "自动模式应显示为关闭");
  assert.ok(!html.includes(">已开启<"), "默认不应显示已开启");
});

test("VN-725 平台未验证时自动模式是「未就绪」而不是「关闭」", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const mode = view.autoModeOf(policy, { platformSupported: false });
  assert.equal(mode, "not_ready");
  assert.equal(view.autoReasonOf({ platformSupported: false }), "auto.reason.platform");

  const html = view.renderSettings(state({ policy, platformSupported: false }), { lang: "zh" });
  assert.ok(html.includes("未就绪"));
  assert.ok(html.includes("受管执行只在 macOS 上实测过"));
});

test("VN-725 缺字段保持未知，不填默认值冒充服务端结论", async () => {
  const view = await load();
  const policy = view.normalizePolicy({ auto_enabled: false, revision: 1 });
  assert.equal(policy.limit, null);
  assert.equal(policy.capacity, null);
  assert.equal(policy.ref, null);
  assert.equal(policy.retentionDays, null);

  const html = view.renderSettings(state({ policy }), { lang: "zh" });
  assert.ok(html.includes("—"), "缺失值应显示占位符而不是 0");
});

test("VN-725 校验只产出封闭的错误码", async () => {
  const view = await load();
  const codes = draft => view.validateDraft(draft, { autoMode: "off" }).map(e => e.code);

  assert.deepEqual(codes({ limit: "2", capacity: "4", retentionDays: "30", retentionRuns: "50" }), []);
  assert.ok(codes({ limit: "0", capacity: "4", retentionDays: "30", retentionRuns: "50" }).includes("limit_below_min"));
  assert.ok(codes({ limit: "1.5", capacity: "4", retentionDays: "30", retentionRuns: "50" }).includes("limit_not_integer"));
  assert.ok(codes({ limit: "1", capacity: "0", retentionDays: "30", retentionRuns: "50" }).includes("capacity_not_integer") === false);
  assert.ok(codes({ limit: "1", capacity: "999", retentionDays: "30", retentionRuns: "50" }).includes("capacity_above_max"));
  assert.ok(codes({ limit: "1", capacity: "4", retentionDays: "0", retentionRuns: "50" }).includes("retention_out_of_range"));
  assert.ok(codes({ limit: "1", capacity: "4", retentionDays: "30", retentionRuns: "" }).includes("retention_not_integer"));
});

test("VN-725 秘密字段不被接受：草稿里出现凭据键即拒绝", async () => {
  const view = await load();
  assert.equal(view.containsSecret({ limit: "1" }), false);
  for (const key of ["token", "api_key", "password", "maw_pat", "clientSecret"]) {
    assert.equal(view.containsSecret({ [key]: "x" }), true, "应识别秘密键：" + key);
  }
  const errors = view.validateDraft({ limit: "1", capacity: "4", retentionDays: "30", retentionRuns: "5", token: "x" });
  assert.ok(errors.some(e => e.code === "secret_field"));

  const html = view.renderSettings(state({ policy: view.normalizePolicy(POLICY) }), { lang: "zh" });
  assert.ok(html.includes("本页不含令牌"), "页面必须声明没有秘密字段入口");
  assert.ok(!/type="password"/.test(html), "不允许出现密码输入框");
});

test("VN-725 脏判定按归一化值比较", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  assert.equal(view.isDirty(policy, { limit: "1", capacity: "2", retentionDays: "30", retentionRuns: "50" }), false);
  assert.equal(view.isDirty(policy, { limit: "2", capacity: "2", retentionDays: "30", retentionRuns: "50" }), true);
  // 字符串与数字同值不算脏
  assert.equal(view.isDirty({ ...policy, limit: 3 }, { limit: "3", capacity: "2", retentionDays: "30", retentionRuns: "50" }), false);
});

test("VN-725 保存状态机：冲突保留草稿，失败保留输入", async () => {
  const view = await load();
  assert.deepEqual(view.applySaveResult("saving"), { status: "saving", keepDraft: true });

  const conflict = view.applySaveResult("saving", { conflict: true, serverRevision: 9 });
  assert.equal(conflict.status, "conflict");
  assert.equal(conflict.keepDraft, true, "冲突时不能抹掉用户刚输入的草稿");
  assert.equal(conflict.serverRevision, 9);

  const failed = view.applySaveResult("saving", { errors: [{ field: "limit", code: "limit_below_min" }] });
  assert.equal(failed.status, "error");
  assert.equal(failed.keepDraft, true);

  const ok = view.applySaveResult("saving", { ok: true, revision: 8 });
  assert.equal(ok.status, "saved");
  assert.equal(ok.revision, 8);
});

test("VN-725 保存必须带 revision，缺失时拒绝保存", async () => {
  const view = await load();
  const draft = { limit: "1", capacity: "4", retentionDays: "30", retentionRuns: "5", revision: null };
  const errors = view.validateDraft(draft, { requireRevision: true });
  assert.ok(errors.some(e => e.code === "revision_missing"));

  // 仅编辑时允许没有 revision；保存前才拦。
  assert.equal(view.validateDraft(draft).some(e => e.code === "revision_missing"), false);
});

test("VN-725 六种页面状态各有独立分支，不互相冒充", async () => {
  const view = await load();
  const expectations = {
    loading: "正在读取设置",
    error: "设置加载失败",
    expired: "设置已过期",
    unauthenticated: "未登录",
    unknown: "设置状态未知",
    empty: "还没有可编辑的工程策略",
  };
  for (const [status, text] of Object.entries(expectations)) {
    const html = view.renderSettings(state({ status, policy: null }), { lang: "zh" });
    assert.ok(html.includes(text), status + " 状态缺少文案：" + text);
  }
  // 未知与过期不能渲染成空列表
  assert.ok(!view.renderSettings(state({ status: "unknown" }), { lang: "zh" }).includes("<table"));
});

test("VN-725 冲突态给出重新载入入口，保存中仍有明确状态", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);

  const conflict = view.renderSettings(state({ policy, save: "conflict" }), { lang: "zh" });
  assert.ok(conflict.includes('data-action="reload"'));
  assert.ok(conflict.includes("设置已被其他人更新"));

  // 页面不再有重复表单：保存态只体现在顶部状态与（可写时的）按钮上。
  const saving = view.renderSettings(state({ policy, save: "saving" }), { lang: "zh" });
  assert.ok(saving.includes('data-save="saving"'));
  assert.ok(!/<form/.test(saving), "系统策略页不再渲染第二套表单");
});

test("VN-725 英文界面不残留中文文案", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "en" });
  // 页面标题由壳层 page-heading 提供（浏览器用例 D-04 断言 #page-h1），视图只管字段区。
  assert.ok(html.includes("Auto mode"));
  assert.ok(html.includes("Advisory"));
  assert.ok(html.includes("Execution limit"));
  assert.ok(!/[\u4e00-\u9fa5]/.test(html), "英文界面不应出现中文");
});

test("VN-725 输入值经过转义，不把字段当成 HTML", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(
    state({ policy, systemAgents: [{ name: '"><script>x</script>', state: "idle" }] }),
    { lang: "zh" },
  );
  assert.ok(!html.includes("<script>x</script>"));
  assert.ok(html.includes("&quot;&gt;&lt;script&gt;"));
});

// ---------------------------------------------------------------------------
// E2E-101：平台支持缺失、只读表单、服务端值回填。
// 这三条原先都会失败：缺失被当成支持、表单可编辑但存不下去、上限显示破折号。
// ---------------------------------------------------------------------------

test("E2E-101 平台支持未声明时是未就绪，不是关闭", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  // undefined 表示服务端没说，不能猜成「支持」。
  assert.equal(view.autoModeOf(policy, {}), "not_ready");
  assert.equal(view.autoModeOf(policy, { platformSupported: undefined }), "not_ready");
  assert.equal(view.autoModeOf({ ...policy, autoEnabled: true }, {}), "not_ready");

  const html = view.renderSettings(state({ policy }), { lang: "zh" });
  assert.ok(html.includes("未就绪"), "缺失平台支持时应显示未就绪");
  assert.ok(html.includes("受管执行只在 macOS 上实测过"), "未就绪要给原因");
});

test("E2E-101 系统策略页只读展示，不再渲染第二套表单", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });

  assert.ok(html.includes('data-scope="system"'), "执行策略是系统级资源");
  assert.ok(html.includes("只读"), "要有只读标记");
  assert.ok(html.includes("中枢当前没有策略写入口"), "要说明为什么不能保存");
  assert.ok(!/<form/.test(html), "不得再渲染一套存不下去的表单");
  assert.ok(!/<input/.test(html), "只读页不应出现输入框");
  // 只读不等于不可读：值仍在策略表里。
  assert.match(html, /data-concept="limit"[^]*?<td class="num">1<\/td>/);
});

test("E2E-101 显式可写时才出现保存入口", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy, editable: true }), { lang: "zh" });
  assert.ok(!html.includes("settings.readonly.hint") && !html.includes("中枢当前没有策略写入口"));
  assert.ok(html.includes("保存"), "可写时要有保存入口");
});

test("E2E-101 没有 revision 时不给保存入口并说明原因", async () => {
  const view = await load();
  // 服务端没回 config_version／revision：能展示但不能保存，否则只能盲写。
  const policy = view.normalizePolicy({ auto_enabled: false, managed_execution_limit: 1 });
  assert.equal(policy.revision, null);

  const html = view.renderSettings(state({ policy, editable: true }), { lang: "zh" });
  assert.ok(html.includes('data-save-blocked="revision_missing"'), "要显式说明被拦下的原因");
  assert.ok(html.includes("缺少设置版本"), "原因文案要可读");
  assert.ok(!/settings-actions/.test(html), "没有 revision 时不渲染保存按钮");

  // 有 revision 才放开保存。
  const ready = view.renderSettings(state({ policy: view.normalizePolicy(POLICY), editable: true }), { lang: "zh" });
  assert.ok(ready.includes("settings-actions"), "有 revision 时应可保存");
  assert.ok(!ready.includes('data-save-blocked="revision_missing"'));
});

test("E2E-101 策略表回填系统当前值", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });
  // 上限=1（不是破折号）、容量=2、保留天数=30、保留运行数=50。
  assert.match(html, /data-concept="limit"[^]*?<td class="num">1<\/td>/);
  assert.match(html, /data-concept="capacity"[^]*?<td class="num">2<\/td>/);
  assert.match(html, /data-concept="retention_days"[^]*?<td class="num">30<\/td>/);
  assert.match(html, /data-concept="retention_runs"[^]*?<td class="num">50<\/td>/);
});

// ---------------------------------------------------------------------------
// EV-002：设置页收敛入口声明。
//
// 需求要「节点与容量、主题、语言收进设置页」，收的是入口声明而不是控件：
// 主题／语言仍归顶栏，节点清单仍归 ?view=nodes。所以这里验的是三件事——
// 分区齐全、每个分区都说清入口／归属／边界、只读声明区不复制控件。
// ---------------------------------------------------------------------------

/** 按 data-section 取出单个分区的 HTML，避免跨分区串断言。 */
function sectionHtml(html, id) {
  const at = html.indexOf('data-section="' + id + '"');
  if (at < 0) return "";
  const from = html.lastIndexOf("<section", at);
  const to = html.indexOf("</section>", at);
  return from < 0 || to < 0 ? "" : html.slice(from, to + "</section>".length);
}

test("EV-002 分区固定为策略与入口两块，且都有可访问名", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy, platformSupported: true }), { lang: "zh" });

  assert.deepEqual([...view.SETTINGS_SECTIONS], ["policy", "entries"], "执行策略是系统级页面，只保留策略与入口");
  const order = view.SETTINGS_SECTIONS.map(id => html.indexOf('data-section="' + id + '"'));
  assert.ok(order.every(index => index >= 0), "分区必须全部渲染");
  assert.deepEqual(order, [...order].sort((a, b) => a - b), "渲染顺序必须与 SETTINGS_SECTIONS 一致");

  for (const title of ["系统执行策略", "相关入口"]) {
    assert.ok(html.includes(title), "缺少分区标题：" + title);
  }
  for (const id of view.SETTINGS_SECTIONS) {
    assert.ok(html.includes('aria-labelledby="settings-section-' + id + '"'), id + " 分区缺少可访问名");
  }
});

test("EV-002 相关入口指明真实位置，不复制控件", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });
  const block = sectionHtml(html, "entries");

  assert.ok(block.includes("?view=nodes"), "节点入口要指向节点页");
  assert.ok(block.includes("config.toml"), "工程级参数要指向工程配置");
  assert.ok(block.includes("顶栏"), "主题与语言要指向顶栏");
  assert.ok(block.includes("超时与重试") && block.includes("通知"), "超时与通知要有归属");
  // 只声明入口，不复制一套控件。
  assert.ok(!/<input|<select|<button|<a\s/.test(block), "入口分区不得含可聚焦控件");
});

test("EV-002 系统 Agent 快照：缺失不渲染，已知才成段", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);

  assert.equal(view.systemAgentsOf({}), null);
  assert.equal(view.systemAgentsOf({ systemAgents: null }), null);
  assert.deepEqual(view.systemAgentsOf({ systemAgents: [] }), []);

  const unknown = view.renderSettings(state({ policy }), { lang: "zh" });
  assert.ok(!unknown.includes('data-section="agents"'), "没有快照时整段省略，不编造状态");

  const empty = view.renderSettings(state({ policy, systemAgents: [] }), { lang: "zh" });
  assert.ok(empty.includes('data-agents-empty="1"'), "空列表要说明没有 Agent，而不是空白");

  const known = view.renderSettings(
    state({ policy, systemAgents: [{ id: "runner-a", state: "idle", last_heartbeat: "2026-11-04T02:00:00Z" }] }),
    { lang: "zh" },
  );
  assert.ok(known.includes('data-agents="1"'));
  assert.ok(known.includes("runner-a") && known.includes("idle"));
  assert.ok(known.includes("2026-11-04T02:00:00Z"));
});

test("EV-002 只读策略与保留展示完整", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });

  assert.ok(html.includes('data-scope="system"'), "执行策略要显式声明系统级");
  assert.ok(html.includes("只读"));
  assert.ok(html.includes("中枢当前没有策略写入口"));
  for (const concept of ["auto", "limit", "capacity", "ref", "retention_days", "retention_runs"]) {
    assert.ok(html.includes('data-concept="' + concept + '"'), "策略表缺行：" + concept);
  }
  assert.ok(!/<form/.test(html) && !/<input/.test(html), "只读页不得出现第二套表单");
});

test("EV-002 系统级页面有中英两版", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const en = view.renderSettings(state({ policy }), { lang: "en" });

  assert.ok(!/[\u4e00-\u9fa5]/.test(en), "英文界面不得残留中文");
  assert.ok(en.includes("System execution policy"));
  assert.ok(en.includes("Related entry points"));
  assert.ok(en.includes("Execution nodes & capacity") || en.includes("Execution nodes &amp; capacity"));
  assert.ok(en.includes("System scope"));
});

// ---------------------------------------------------------------------------
// 系统级页面：策略表之外的内容只保留入口声明，避免重复控件。
// ---------------------------------------------------------------------------

test("只读是系统策略页的一等状态，不靠禁用表单冒充", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });

  assert.ok(html.includes('data-readonly-reason="1"'), "只读页要说明为什么不能保存");
  assert.ok(html.includes("只作展示，不提交"), "只读说明要讲清不会提交");
  assert.ok(html.includes("只读快照"), "策略表要标明自己是当前生效值快照");

  const editable = view.renderSettings(state({ policy, editable: true }), { lang: "zh" });
  assert.ok(!editable.includes('data-readonly-reason="1"'), "可写时不得带只读说明");
  assert.ok(editable.includes(".settings-actions") || editable.includes("settings-actions"), "可写时要给出保存入口");
});

// ---------------------------------------------------------------------------
// SA-004：auto_enabled 与 system_agent.enabled 是两个开关。
//
// 页面收敛为系统策略表后，不再单独渲染系统 Agent 分区；但归一化仍要认出
// 两种服务端形状，避免版本变化时把「未知」静默当成「关闭」。
// ---------------------------------------------------------------------------

test("SA-004 系统 Agent 开关三态：开／关／未知，缺失不冒充关闭", async () => {
  const view = await load();

  // 顶层字段与嵌套对象两种形状都要认（/api/runtime 会随版本变化）。
  assert.equal(view.normalizePolicy({ system_agent_enabled: true }).systemAgentEnabled, true);
  assert.equal(view.normalizePolicy({ system_agent_enabled: false }).systemAgentEnabled, false);
  assert.equal(view.normalizePolicy({ system_agent: { enabled: true } }).systemAgentEnabled, true);
  assert.equal(view.normalizePolicy({}).systemAgentEnabled, null);
  // 顶层优先于嵌套，避免两个形状打架时静默取错。
  assert.equal(
    view.normalizePolicy({ system_agent_enabled: false, system_agent: { enabled: true } }).systemAgentEnabled,
    false,
  );

  assert.equal(view.systemAgentStateOf({ systemAgentEnabled: true }), "on");
  assert.equal(view.systemAgentStateOf({ systemAgentEnabled: false }), "off");
  assert.equal(view.systemAgentStateOf({}), "unknown");

  const policy = view.normalizePolicy(POLICY);
  const unknown = view.renderSettings(state({ policy }), { lang: "zh" });
  assert.ok(!/data-system-agent="off"/.test(unknown), "未知不得渲染成关闭");
});
