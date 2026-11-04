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

test("VN-725 冲突态给出重新载入入口，保存中禁用提交", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);

  const conflict = view.renderSettings(state({ policy, save: "conflict" }), { lang: "zh" });
  assert.ok(conflict.includes('data-action="reload"'));
  assert.ok(conflict.includes("设置已被其他人更新"));

  const saving = view.renderSettings(state({ policy, save: "saving" }), { lang: "zh" });
  assert.ok(/type="submit" disabled/.test(saving), "保存中应禁用提交按钮");
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
  const html = view.renderSettings(state({ policy, draft: { limit: '"><script>x</script>' } }), { lang: "zh" });
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

test("E2E-101 没有写入口时表单只读并说明原因", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });

  assert.ok(html.includes('data-editable="0"'), "默认应为只读");
  assert.ok(/type="submit"[^>]*disabled/.test(html), "只读时保存按钮必须禁用");
  assert.ok(html.includes("只读"), "要有只读标记");
  assert.ok(html.includes("中枢当前没有策略写入口"), "要说明为什么不能保存");
  assert.ok(html.includes('onsubmit="return false"'), "表单不得触发原生提交（否则会整页跳走）");
  // 只读不等于不可读：值仍要出现在输入框里。
  assert.match(html, /id="settings-limit"[^>]*value="1"/);
});

test("E2E-101 显式可写时才恢复可编辑", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy, editable: true }), { lang: "zh" });
  assert.ok(html.includes('data-editable="1"'));
  assert.ok(!/type="submit"[^>]*disabled/.test(html), "可写时保存按钮应可用");
  assert.ok(!html.includes("中枢当前没有策略写入口"));
});

test("E2E-101 没有 revision 时保存按钮禁用并给出原因", async () => {
  const view = await load();
  // 服务端没回 config_version／revision：能编辑但不能保存，否则只能盲写。
  const policy = view.normalizePolicy({ auto_enabled: false, managed_execution_limit: 1 });
  assert.equal(policy.revision, null);

  const html = view.renderSettings(state({ policy, editable: true }), { lang: "zh" });
  assert.ok(html.includes('data-editable="1"'), "缺 revision 不影响编辑");
  assert.ok(/type="submit"[^>]*disabled/.test(html), "没有 revision 时保存按钮必须禁用");
  assert.ok(html.includes('data-save-blocked="revision_missing"'), "要显式说明被拦下的原因");
  assert.ok(html.includes("缺少设置版本"), "原因文案要可读");

  // 有 revision 才放开保存。
  const ready = view.renderSettings(state({ policy: view.normalizePolicy(POLICY), editable: true }), { lang: "zh" });
  assert.ok(!/type="submit"[^>]*disabled/.test(ready), "有 revision 时应可保存");
  assert.ok(!ready.includes('data-save-blocked="revision_missing"'));
});

test("E2E-101 四个输入框回填服务端当前值", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });
  // 上限=1（不是破折号）、容量=2、保留天数=30、保留运行数=50。
  assert.match(html, /id="settings-limit"[^>]*value="1"/);
  assert.match(html, /id="settings-capacity"[^>]*value="2"/);
  assert.match(html, /id="settings-retentionDays"[^>]*value="30"/);
  assert.match(html, /id="settings-retentionRuns"[^>]*value="50"/);
  // 概念表里的上限来自 /api/runtime 的 managed_execution_limit。
  assert.match(html, /data-concept="limit"[^]*?<td class="num">1<\/td>/);
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

test("EV-002 分区齐全且顺序固定，每个分区都有可访问名", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy, platformSupported: true }), { lang: "zh" });

  assert.deepEqual(
    [...view.SETTINGS_SECTIONS],
    ["managed", "nodes", "retention", "retries", "notifications", "appearance", "agents"],
    "F57 的六类收敛项都要有归属：节点／容量、超时与重试、通知、采集与保留、外观、Agent 状态",
  );
  const order = view.SETTINGS_SECTIONS.map(id => html.indexOf('data-section="' + id + '"'));
  assert.ok(order.every(index => index >= 0), "分区必须全部渲染");
  assert.deepEqual(order, [...order].sort((a, b) => a - b), "渲染顺序必须与 SETTINGS_SECTIONS 一致");

  // 验收点名的四块：节点／外观／受管／系统 Agent 状态。
  for (const title of ["受管执行", "节点接入与容量", "外观", "系统 Agent 状态"]) {
    assert.ok(html.includes(title), "缺少分区标题：" + title);
  }
  for (const id of view.SETTINGS_SECTIONS) {
    assert.ok(
      html.includes('aria-labelledby="settings-section-' + id + '"'),
      id + " 分区缺少可访问名",
    );
  }
});

test("EV-002 每个分区都带入口／归属／边界三行说明链", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });

  for (const id of view.SETTINGS_SECTIONS) {
    const block = sectionHtml(html, id);
    assert.ok(block, id + " 分区未渲染");
    for (const fact of ["entry", "owner", "boundary"]) {
      assert.ok(block.includes('data-fact="' + fact + '"'), id + " 缺少说明链：" + fact);
    }
  }
  // 说明链要指向真实入口，不能是空话。
  const appearance = sectionHtml(html, "appearance");
  assert.ok(appearance.includes("#theme-select"), "外观分区要指明主题控件在顶栏");
  assert.ok(appearance.includes("#lang-select"), "外观分区要指明语言控件在顶栏");
  assert.ok(sectionHtml(html, "nodes").includes("?view=nodes"), "节点分区要指明节点清单的路由");
});

test("EV-002 外观分区只声明入口，不复制一套主题／语言开关", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });
  const block = sectionHtml(html, "appearance");

  // 复制控件就会出现两个真相：一处改了另一处不动。
  assert.ok(!/<select/.test(block), "外观分区不得自建下拉");
  assert.ok(!/<input/.test(block), "外观分区不得自建输入");
  assert.ok(!/<button/.test(block), "外观分区不得自建按钮");
  assert.ok(!/id="theme-select"/.test(html), "本页不重复定义主题控件");
  assert.ok(!/id="lang-select"/.test(html), "本页不重复定义语言控件");
});

test("EV-002 节点分区声明登记走 CLI，不放存不下去的登记表单", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const block = sectionHtml(view.renderSettings(state({ policy }), { lang: "zh" }), "nodes");

  assert.ok(block.includes("没有节点登记写接口"), "要说明本页不提供登记写接口");
  // 只承载容量字段：名称／路径／版本这类登记输入一个都不许出现。
  const names = (block.match(/<input[^>]*name="([^"]+)"/g) || [])
    .map(tag => tag.replace(/.*name="([^"]+)".*/, "$1"));
  assert.deepEqual(names, ["capacity"]);
  assert.ok(!/type="text"/.test(block), "登记表单会是文本框，本卡不提供");
  assert.ok(block.includes("节点是全机可调度的执行实例"), "节点与 Agent 要分开说清");
});

test("EV-002 系统 Agent 状态：没有快照就说未知，不编造状态", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);

  assert.equal(view.systemAgentsOf({}), null);
  assert.equal(view.systemAgentsOf({ systemAgents: null }), null);
  assert.deepEqual(view.systemAgentsOf({ systemAgents: [] }), []);

  const unknown = view.renderSettings(state({ policy }), { lang: "zh" });
  assert.ok(unknown.includes('data-agents-unknown="1"'), "缺快照时要显式说明未知");
  assert.ok(unknown.includes("状态未知不等于没有 Agent"), "未知不能写成「没有 Agent」");
  assert.ok(!unknown.includes('data-agents="1"'), "没有快照时不得渲染状态表");

  const known = view.renderSettings(
    state({ policy, systemAgents: [{ id: "runner-a", state: "idle", last_heartbeat: "2026-11-04T02:00:00Z" }] }),
    { lang: "zh" },
  );
  assert.ok(known.includes('data-agents="1"'));
  assert.ok(known.includes("runner-a") && known.includes("idle"));
  assert.ok(known.includes("2026-11-04T02:00:00Z"));
  assert.ok(!known.includes('data-agents-unknown="1"'));
});

test("EV-002 受管只读策略展示不因分区重构而丢失", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });

  // 原有展示一条不少：只读标记、四概念对照表、只读原因、禁用保存。
  assert.ok(html.includes('data-editable="0"'));
  assert.ok(html.includes("只读"));
  assert.ok(html.includes("中枢当前没有策略写入口"));
  assert.ok(/type="submit"[^>]*disabled/.test(html));
  for (const concept of ["auto", "limit", "capacity", "ref"]) {
    assert.ok(html.includes('data-concept="' + concept + '"'), "概念表缺行：" + concept);
  }

  // 四个输入仍在同一个表单内，且 DOM 顺序未变——键盘顺序靠它保证。
  const form = html.slice(html.indexOf('<form class="settings-form"'), html.indexOf("</form>"));
  const ids = (form.match(/id="settings-(\w+)"/g) || []).map(tag => tag.replace(/id="settings-(\w+)"/, "$1"));
  assert.deepEqual(ids, ["limit", "capacity", "retentionDays", "retentionRuns"]);
  // 只读声明区不产生可聚焦元素：否则 Tab 会先撞上它们，输入框就不再是第一个焦点。
  for (const id of ["appearance", "agents"]) {
    assert.ok(!/<input|<select|<button|<a\s/.test(sectionHtml(html, id)), id + " 分区不得含可聚焦元素");
  }
});

test("EV-002 新增分区同样有中英两版，说明链也翻译", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const en = view.renderSettings(state({ policy }), { lang: "en" });

  assert.ok(!/[\u4e00-\u9fa5]/.test(en), "英文界面不得残留中文");
  assert.ok(en.includes("Nodes &amp; capacity") || en.includes("Nodes & capacity"));
  assert.ok(en.includes("Appearance"));
  assert.ok(en.includes("System agent status"));
  assert.ok(en.includes("Entry") && en.includes("Owner") && en.includes("Boundary"));
});

// ---------------------------------------------------------------------------
// F47 / F57：单一设置页要收敛「超时与重试」「通知」。
//
// 这两类此前只写在需求里，页面上没有分区，入口仍然散在 config.toml 和服务端。
// 补分区时不复制控件，只声明入口——但入口必须是真实位置，不能写「在别处配置」。
// ---------------------------------------------------------------------------

test("F57 超时与重试、通知两个分区已收敛到设置页", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });

  const retries = sectionHtml(html, "retries");
  const notifications = sectionHtml(html, "notifications");
  assert.ok(retries, "缺少「超时与重试」分区");
  assert.ok(notifications, "缺少「通知与提醒」分区");

  // 入口要能落地：超时/心跳在 config.toml，重试口径在 reconcile，提醒在 v1 读接口。
  assert.ok(retries.includes("timeout_sec"), "超时要指向 config.toml [gates] timeout_sec");
  assert.ok(retries.includes("heartbeat_ttl_sec"), "心跳要指向 config.toml [locks] heartbeat_ttl_sec");
  assert.ok(retries.includes("DefaultRetryPolicy"), "重试要指向实际实现，不写空话");
  assert.ok(notifications.includes("/api/v1/projects/{id}/notifications"), "通知要指向真实读接口");
  assert.ok(notifications.includes("/acks"), "确认要指向真实写路径");

  // 只声明入口，不放控件：阈值、渠道、确认动作都不在浏览器里。
  for (const [id, block] of [["retries", retries], ["notifications", notifications]]) {
    assert.ok(!/<input|<select|<button|<a\s/.test(block), id + " 分区不得含可聚焦元素");
  }
});

test("F57 两个新分区同样带入口／归属／边界，且英文版不残留中文", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const zh = view.renderSettings(state({ policy }), { lang: "zh" });

  for (const id of ["retries", "notifications"]) {
    const block = sectionHtml(zh, id);
    for (const fact of ["entry", "owner", "boundary"]) {
      assert.ok(block.includes('data-fact="' + fact + '"'), id + " 缺少说明链：" + fact);
    }
  }

  const en = view.renderSettings(state({ policy }), { lang: "en" });
  assert.ok(en.includes("Timeouts &amp; retries") || en.includes("Timeouts & retries"));
  assert.ok(en.includes("Notifications &amp; reminders") || en.includes("Notifications & reminders"));
  assert.ok(!/[\u4e00-\u9fa5]/.test(en), "英文界面不得残留中文");
});

test("E2E-101 只读是表单的一等状态，不靠禁用外观冒充", async () => {
  const view = await load();
  const policy = view.normalizePolicy(POLICY);
  const html = view.renderSettings(state({ policy }), { lang: "zh" });

  assert.ok(html.includes('data-readonly="1"'), "只读时表单要声明 data-readonly");
  assert.ok(html.includes('data-form-caption="1"'), "字段区要说明它在展示什么");
  assert.ok(html.includes("只作展示，不提交"), "只读说明要讲清不会提交");
  assert.ok(html.includes("只读快照"), "概念表要标明自己是当前生效值快照");

  const editable = view.renderSettings(state({ policy, editable: true }), { lang: "zh" });
  assert.ok(!editable.includes('data-readonly="1"'), "可写时不得带只读标记");
  assert.ok(editable.includes("请求会带上当前 revision"), "可写说明要讲清保存语义");
});

// ---------------------------------------------------------------------------
// SA-004：auto_enabled 与 system_agent.enabled 是两个开关。
//
// 设置页只说「自动模式」，读者就会把「自动模式关着」读成「什么都不自动跑」，
// 而系统 Agent 的阻塞自愈可能正在工作。所以页面必须同时给出两个开关的状态，
// 并且说清各自管什么、互不推导；服务端没回显时写「未知」，不写「关闭」。
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
  assert.ok(unknown.includes('data-system-agent="unknown"'), "缺开关时必须显式标未知");
  assert.ok(unknown.includes("服务端没回显系统 Agent 开关"), "未知要给原因");
  assert.ok(!/data-system-agent="off"/.test(unknown), "未知不得渲染成关闭");
});

test("SA-004 两个开关各说各话：自愈管阻塞处理，自动模式管派发", async () => {
  const view = await load();
  const on = view.renderSettings(
    state({ policy: { ...view.normalizePolicy(POLICY), systemAgentEnabled: true } }),
    { lang: "zh" },
  );
  assert.ok(on.includes('data-system-agent="on"'));
  assert.ok(on.includes(">已开启<"));
  assert.ok(on.includes("阻塞自愈循环在跑"));

  const off = view.renderSettings(
    state({ policy: { ...view.normalizePolicy(POLICY), systemAgentEnabled: false } }),
    { lang: "zh" },
  );
  assert.ok(off.includes('data-system-agent="off"'));
  assert.ok(off.includes("阻塞自愈循环没开"));

  // 分工写在分区里，而不是靠读者从两个开关名猜。
  const agents = sectionHtml(off, "agents");
  assert.ok(agents.includes("不管：自动选题派发"), "要明说系统 Agent 不负责派发");
  assert.ok(agents.includes("两个开关互不推导"), "要明说两个开关互不推导");

  const managed = sectionHtml(off, "managed");
  assert.ok(managed.includes("系统 Agent 的阻塞自愈是另一个开关"), "自动模式要指向另一个开关");
  // 两个开关都不是输入项：本卡不做可写保存 API。
  assert.ok(!/<input/.test(agents), "系统 Agent 分区不得出现输入框");
  assert.ok(!/data-system-agent="on"/.test(off), "关闭态不得同时显示已开启");
});

test("SA-004 英文界面同样区分两个开关", async () => {
  const view = await load();
  const policy = { ...view.normalizePolicy(POLICY), systemAgentEnabled: true };
  const en = view.renderSettings(state({ policy }), { lang: "en" });

  assert.ok(!/[\u4e00-\u9fa5]/.test(en), "英文界面不得残留中文");
  assert.ok(en.includes("What it governs"));
  assert.ok(en.includes("auto dispatch"), "英文也要说清它不管派发");
  assert.ok(en.includes("independent"));
});
