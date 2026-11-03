// VN-725 工程策略与保留设置页的验证。
//
// 覆盖 Acceptance：
//   · 自动模式／计算上限／节点容量／max_parallel 参考值四种标签不同，且默认不开启自动执行；
//   · 保存状态机（脏／保存中／已保存／失败／冲突）语义明确，冲突保留草稿；
//   · 无秘密字段入口：草稿里出现 token／password 一类键就拒绝渲染与保存。
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
  assert.ok(html.includes("Execution policy &amp; retention") || html.includes("Execution policy & retention"));
  assert.ok(html.includes("Auto mode"));
  assert.ok(html.includes("Advisory"));
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
