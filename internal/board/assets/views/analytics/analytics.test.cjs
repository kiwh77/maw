// VN-744 交付与用量统计视图验证。
//
// 覆盖 Acceptance：
//   · 成果／投入分开；累计／并集／平均并发各有口径；未知不显示为零；
//   · 来源分布与时间图共用色板图例；汇总可下钻到证据与水位；
//   · 键盘访问、中英安全渲染与六种状态可核对。
//
// 运行：node --test internal/board/assets/views/analytics/analytics.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "analytics-view.mjs")).href;
const load = () => import(MODULE);

const report = () => ({
  scope: { project_id: "p1", start: "2026-10-23T00:00:00Z", end: "2026-10-24T00:00:00Z", timezone: "Asia/Shanghai" },
  generated_at: "2026-10-23T12:00:00Z",
  completeness: "partial",
  watermark: { cursor: 128, complete_before: 120 },
  timeline: {
    task_phase_seconds: 7200, cli_process_seconds: 7200, turn_seconds: 300,
    explicit_wait_seconds: 3600, engineering_seconds: 10800, avg_concurrency: 1.5,
  },
  usage: {
    turn_count: 5, shared_turns: 1, unknown_turns: 1, estimated_turns: 0,
    tokens: { total_tokens: 1730, input_tokens: 1350, output_tokens: 380 },
    usage_unknown: true,
    coverage: { observed_turns: 3, observed_denom: 5, ratio: 0.6 },
  },
  deliveries: { count: 2, superseded: 1, by_mode: { code: 1, non_code: 1 }, ids: ["dl-1", "dl-2"] },
  by_source: [
    { source_ns: "claude", turn_count: 4, usage_unknown: false, turns_supported: true },
    { source_ns: "codex", turn_count: 1, usage_unknown: true, turns_supported: false },
  ],
  by_day: [
    { day: "2026-10-23", delivery_count: 2, turn_count: 4 },
    { day: "2026-10-24", delivery_count: 0, turn_count: 1 },
  ],
  evidence: { intervals: 2, turns: 5, usage_records: 5, deliveries: 2, identities: 1 },
});

test("三类口径都有声明：累计／并集／平均并发", async () => {
  const mod = await load();
  assert.deepEqual(mod.METRIC_DEFS.map(m => m.id), ["cumulative", "union", "concurrency"]);
  const html = mod.renderAnalytics({ report: report() }, { lang: "zh" });
  assert.ok(html.includes("按身份并集"), "并集口径必须写明");
  assert.ok(html.includes("去重累计"), "累计口径必须写明");
  assert.ok(html.includes("平均并发身份"));
  assert.ok(html.includes("Σ 各身份并集秒数 ÷ 窗口秒数"), "并发口径必须写明");
});

test("未知不显示为零：缺字段渲染未知标记", async () => {
  const mod = await load();
  const rep = report();
  delete rep.usage.tokens.total_tokens;
  rep.usage.usage_unknown = true;
  rep.timeline.engineering_seconds = null;
  const html = mod.renderAnalytics({ report: rep }, { lang: "zh" });
  assert.ok(html.includes('data-unknown="true"'), "未知要有明确标记");
  assert.ok(html.includes("部分未知"));
  // 值缺失的地方不得出现整值为 0 的读数（允许 "7200 s" 这类数字尾部）。
  assert.ok(!html.includes("<dd>0 s</dd>"), "缺失时长不得渲染成 0 s");
});

test("零分母覆盖率如实显示 0/0 并注明不假装完整", async () => {
  const mod = await load();
  const rep = report();
  rep.usage.coverage = { observed_turns: 0, observed_denom: 0, ratio: 0 };
  const html = mod.renderAnalytics({ report: rep }, { lang: "zh" });
  assert.ok(html.includes("0/0"));
  assert.ok(html.includes("分母为零，覆盖率记 0"));
});

test("来源分布与按日分布：色阶相对最大值，0 值不进色阶", async () => {
  const mod = await load();
  const html = mod.renderAnalytics({ report: report() }, { lang: "zh" });
  assert.ok(html.includes('data-chart="sources"'));
  assert.ok(html.includes('data-chart="days"'));
  assert.ok(html.includes('data-source="claude"'));
  assert.ok(html.includes('data-day="2026-10-23"'));
  // claude=4 为最大 → l4；codex=1 → l1；23 日 2 笔交付为最大 → l4；24 日 0 笔 → level 0。
  assert.ok(html.includes('data-level="4"'));
  assert.ok(html.includes('data-level="1"'));
  assert.ok(html.includes('data-kind="deliveries" data-level="0"'), "24 日 0 交付不进色阶");
  assert.ok(html.includes('data-kind="deliveries" data-level="4"'), "23 日 2 笔为最大档");
});

test("汇总可下钻到证据与水位", async () => {
  const mod = await load();
  const html = mod.renderAnalytics({ report: report() }, { lang: "zh" });
  assert.ok(html.includes('data-evidence'));
  assert.ok(html.includes("轮次 5"));
  assert.ok(html.includes("记账 5"));
  assert.ok(html.includes("交付 2"));
  assert.ok(html.includes("状态水位=128"));
  assert.ok(html.includes("投影水位=120"));
  assert.ok(html.includes("完整性=partial"));
  assert.ok(html.includes('data-status="unknown"'));
});

test("不支持轮次的来源在分布里保持降级标注", async () => {
  const mod = await load();
  const html = mod.renderAnalytics({ report: report() }, { lang: "zh" });
  assert.ok(html.includes("来源不支持轮次"));
});

test("六种状态互不重复且带 data-status", async () => {
  const mod = await load();
  const states = {
    loading: { status: "loading" },
    empty: { status: "ready", report: {} },
    error: { status: "error" },
    expired: { status: "expired" },
    unauth: { status: "unauthenticated" },
    unknown: { status: "unknown", report: report() },
  };
  const seen = new Set();
  for (const [key, state] of Object.entries(states)) {
    const html = mod.renderAnalytics(state, { lang: "zh" });
    assert.ok(html.length > 0, key);
    assert.ok(html.includes('data-status="'), key);
    assert.ok(!seen.has(html), key + " 与其他状态重复");
    seen.add(html);
  }
});

test("英文界面不残留中文；危险字符转义", async () => {
  const mod = await load();
  const html = mod.renderAnalytics({
    report: {
      ...report(),
      by_source: [{ source_ns: "<script>x</script>", turn_count: 1, usage_unknown: false, turns_supported: true }],
    },
  }, { lang: "en" });
  // 页面标题由壳层 page-heading 提供，视图只负责卡片区。
  assert.ok(html.includes("Deliveries"));
  assert.ok(html.includes("Execution investment"));
  assert.doesNotMatch(html, /[\u4e00-\u9fa5]/);
  assert.ok(!html.includes("<script>"));
  assert.ok(html.includes("&lt;script&gt;"));
});

test("色阶函数：相对最大值分四档", async () => {
  const mod = await load();
  assert.equal(mod.levelOf(0, 100), 0);
  assert.equal(mod.levelOf(50, 100), 2);
  assert.equal(mod.levelOf(100, 100), 4);
  assert.equal(mod.levelOf(50, 0), 0);
});

// E2E-102：没选工程 ≠ 没有统计。
test("E2E-102 未选工程时指向工程下拉，而不是说没有统计结果", async () => {
  const mod = await load();
  const zh = mod.renderAnalytics({ status: "no_project" }, { lang: "zh" });
  assert.ok(zh.includes('data-status="no_project"'));
  assert.ok(zh.includes("还没有选择工程"));
  assert.ok(zh.includes("顶部选择工程"), "文案要指向工程下拉");
  assert.ok(!zh.includes("这个范围里还没有统计结果"), "未选工程不得渲染成空统计");

  const en = mod.renderAnalytics({ status: "no_project" }, { lang: "en" });
  assert.ok(en.includes("No project selected"));
  assert.ok(!/[\u4e00-\u9fa5]/.test(en), "英文界面不应残留中文");
});

// ---------- PI-203：用量归属 ----------

const ATTRIBUTION_MODULE = pathToFileURL(path.join(__dirname, "attribution.mjs")).href;
const loadAttribution = () => import(ATTRIBUTION_MODULE);

/** 一份带「逐次调用 + 旧 CLI 运行级 + 未声明维度值」的归属报表。 */
const attributionReport = (overrides = {}) => ({
  dimension: "source",
  scope: { project_id: "p1", start: "2026-10-23T00:00:00Z", end: "2026-10-24T00:00:00Z", timezone: "Asia/Shanghai" },
  completeness: "partial",
  watermark: { cursor: 42, complete_before: 40 },
  gaps: [{ field: "runs_by_dimension", reason: "Run 结果只按节点归集" }],
  rollups: [
    {
      dimension: "source", value: "pi", scopes: ["call"], turns: 3,
      tokens: { input_tokens: 900, output_tokens: 300, total_tokens: 1200, cache_read_tokens: 200, cache_write_tokens: null },
      usage_unknown: false, estimated_turns: 1, cache_normalized: 2, cache_unverified: 0,
      usage_observed: { numerator: 3, denominator: 3, ratio: 1 },
      evidence: ["dimension:source", "scope:call"],
    },
    {
      dimension: "source", value: "codebuddy", scopes: ["run"], turns: 2,
      tokens: { input_tokens: null, output_tokens: null, total_tokens: null },
      usage_unknown: true, estimated_turns: 0, cache_normalized: 0, cache_unverified: 0,
      usage_observed: { numerator: 0, denominator: 2, ratio: 0 },
      evidence: ["dimension:source", "scope:run"],
    },
    {
      dimension: "source", value: "", scopes: [], turns: 1,
      tokens: { input_tokens: null, output_tokens: null, total_tokens: null },
      usage_unknown: true,
      usage_observed: { numerator: 0, denominator: 1, ratio: 0 },
    },
  ],
  totals: {
    tokens: { total_tokens: 1200, input_tokens: 900, output_tokens: 300 },
    calls_with_usage: { numerator: 3, denominator: 6, ratio: 0.5 },
    turns_with_usage: { numerator: 3, denominator: 6, ratio: 0.5 },
    run_success: { numerator: 0, denominator: 0, ratio: null },
    delivery_pass: { numerator: 0, denominator: 2, ratio: 0 },
    usage_unknown: true,
    estimated_cost: { amount_usd: null, basis: "未配置价目表" },
  },
  ...overrides,
});

/** 归一化之后的 ready 归属状态（视图只认归一模型）。 */
async function readyAttribution(overrides = {}) {
  const mod = await loadAttribution();
  return {
    status: "ready",
    dimension: "source",
    dimensions: ["source", "node", "model", "purpose"],
    reports: { source: mod.normalizeAttribution(attributionReport(overrides)) },
    errors: {},
  };
}

test("归属报表归一：缺 token 保持 null，零分母比率保持 null", async () => {
  const mod = await loadAttribution();
  const rep = mod.normalizeAttribution(attributionReport({
    dimension: "purpose",
    rollups: [{ dimension: "purpose", value: "diagnose", scopes: ["call"], turns: 1, tokens: {}, usage_observed: { numerator: 0, denominator: 0 } }],
  }));
  assert.equal(rep.dimension, "purpose");
  assert.equal(rep.rollups[0].tokens.total, null, "缺 token 不得变成 0");
  assert.deepEqual(rep.totals.runSuccess, { numerator: 0, denominator: 0, ratio: null });
  assert.equal(rep.totals.cost.amountUSD, null);
  assert.equal(rep.scope.projectId, "p1");
});

test("归属面板：四个维度可切换，汇总带分子分母，Run 成功零分母写无分母", async () => {
  const mod = await load();
  const html = mod.renderAttributionPanel(await readyAttribution(), "zh");
  assert.ok(html.includes('data-attribution'));
  assert.ok(html.includes('data-dimension="source"'));
  for (const dim of ["source", "node", "model", "purpose"]) {
    assert.ok(html.includes(`data-attr-dimension="${dim}"`), dim + " 维度缺一不可");
  }
  for (const label of ["来源", "节点", "模型", "用途"]) {
    assert.ok(html.includes(">" + label + "</button>"), "维度标签要翻译成中文：" + label);
  }
  assert.ok(!html.includes("attr.dimension."), "维度标签不得漏翻成 i18n key");
  assert.ok(html.includes("调用覆盖（有用量/调用）"));
  assert.ok(html.includes("3/6 (50%)"), "比率必须写出分子/分母");
  assert.ok(html.includes("Run 成功"));
  assert.ok(html.includes("0/0"), "零分母照实写 0/0");
  assert.ok(html.includes("无分母"), "零分母不得写成 0%");
  assert.ok(!html.includes("0/0 (0%)"), "零分母不得伪造百分比");
});

test("归属面板：逐次调用与旧 CLI 运行级按原粒度标注，不合并", async () => {
  const mod = await load();
  const html = mod.renderAttributionPanel(await readyAttribution(), "zh");
  assert.ok(html.includes('data-granularity="call"'));
  assert.ok(html.includes("逐次调用"));
  assert.ok(html.includes('data-granularity="run"'));
  assert.ok(html.includes("运行级（旧 CLI 汇总）"), "旧 CLI 数据按原粒度显示");
  assert.ok(html.includes('data-dimension-value="codebuddy"'));
});

test("归属面板：未知用量、估算与未归一缓存都显式标注", async () => {
  const mod = await load();
  const html = mod.renderAttributionPanel(await readyAttribution(), "zh");
  assert.ok(html.includes('data-unknown="true"'), "未知用量要有明确标记");
  assert.ok(html.includes("估算 1 轮"), "估算轮次必须写明");
  assert.ok(html.includes("缓存已归一 2"));
  assert.ok(html.includes('data-attr-partial="true"'), "部分未知必须有横幅");
  assert.ok(html.includes("部分未知"));
  // 未知 token 不得渲染成 0。
  assert.ok(!html.includes("<td>0</td>"));
});

test("归属面板：空维度值写「未声明」，不写空白", async () => {
  const mod = await load();
  const html = mod.renderAttributionPanel(await readyAttribution(), "zh");
  assert.ok(html.includes('data-undeclared="true"'));
  assert.ok(html.includes("未声明"));
});

test("归属面板：可从汇总追到 Run，并保留水位与缺测说明", async () => {
  const mod = await load();
  const html = mod.renderAttributionPanel(await readyAttribution(), "zh");
  assert.ok(html.includes('data-drill="runs"'), "汇总必须能下钻到 Run 列表");
  assert.ok(html.includes("view=runs"), "下钻地址要带 run 视图");
  assert.ok(html.includes("project=p1"), "下钻地址要带工程作用域");
  assert.ok(html.includes("去 Run 列表核对"));
  assert.ok(html.includes("归属水位=42"));
  assert.ok(html.includes("<code>runs_by_dimension</code>"), "缺测字段要原样可查");
});

test("归属面板：没有可归集用量时给空态而不是全零表", async () => {
  const mod = await load();
  const attribution = await readyAttribution({ rollups: [], totals: {} });
  const html = mod.renderAttributionPanel(attribution, "zh");
  assert.ok(html.includes('data-attr-empty'));
  assert.ok(html.includes("没有可归集的用量"));
  assert.ok(!html.includes('data-attr-table'), "没有行就不要渲染表头");
});

test("归属面板：某维度失败不影响其它维度，错误可重试", async () => {
  const mod = await load();
  const attribution = await readyAttribution();
  attribution.dimension = "node";
  attribution.errors = { node: "http_500" };
  const html = mod.renderAttributionPanel(attribution, "zh");
  assert.ok(html.includes("归属统计加载失败"));
  assert.ok(html.includes('data-action="retry-attribution"'));
  assert.ok(html.includes('data-attr-dimension="source"'), "其它维度仍可切换");
  assert.ok(html.includes('data-dimension-error="http_500"'));
});

test("归属面板：loading／未登录／不支持／整体失败各有状态", async () => {
  const mod = await load();
  const cases = {
    loading: { status: "loading" },
    unauthenticated: { status: "unauthenticated" },
    unsupported: { status: "unsupported" },
    error: { status: "error" },
  };
  const seen = new Set();
  for (const [key, state] of Object.entries(cases)) {
    const html = mod.renderAttributionPanel(state, "zh");
    assert.ok(html.includes(`data-status="${key}"`), key);
    assert.ok(html.length > 0, key);
    assert.ok(!seen.has(html), key + " 与其他状态重复");
    seen.add(html);
  }
});

test("归属面板：英文界面无中文，值经转义", async () => {
  const mod = await load();
  const attribution = await readyAttribution({
    rollups: [{
      dimension: "source", value: "<script>x</script>", scopes: ["call"], turns: 1,
      tokens: { total_tokens: 10 },
      usage_observed: { numerator: 1, denominator: 1, ratio: 1 },
      evidence: ["scope:call"],
    }],
    gaps: [],
    completeness: "complete",
    totals: { tokens: { total_tokens: 10 }, usage_unknown: false },
  });
  const html = mod.renderAttributionPanel(attribution, "en");
  assert.ok(html.includes("Usage attribution"));
  assert.ok(html.includes("Per-call"));
  assert.doesNotMatch(html, /[\u4e00-\u9fa5]/);
  assert.ok(!html.includes("<script>"));
  assert.ok(html.includes("&lt;script&gt;"));
});

test("统计视图接线：有归属数据才多出面板，没有时保持原样", async () => {
  const mod = await load();
  const withAttr = mod.renderAnalytics({ report: report(), attribution: await readyAttribution() }, { lang: "zh" });
  assert.ok(withAttr.includes("data-attribution"));
  assert.ok(withAttr.includes("data-card=\"usage\""), "原有卡片仍要在");
  const without = mod.renderAnalytics({ report: report() }, { lang: "zh" });
  assert.ok(!without.includes("data-attribution"));
});

test("归属取数：每个维度独立成败，全失败才整体失败", async () => {
  const mod = await loadAttribution();
  const payload = { attribution: attributionReport(), completeness: "partial" };
  const urls = [];
  const fetchImpl = async url => {
    urls.push(url);
    if (url.includes("dimension=node")) return { ok: false, status: 500 };
    if (url.includes("dimension=model")) return { ok: false, status: 401 };
    return { ok: true, status: 200, json: async () => payload };
  };
  const out = await mod.fetchAttribution("p1", { fetchImpl });
  assert.equal(out.status, "ready");
  assert.ok(out.reports.source && out.reports.purpose);
  assert.equal(out.errors.node, "http_500");
  assert.equal(out.errors.model, "unauthenticated");
  assert.equal(urls.length, 4);
  assert.ok(urls.every(u => u.startsWith("/api/v1/projects/p1/analytics?dimension=")));

  const allFail = await mod.fetchAttribution("p1", { fetchImpl: async () => ({ ok: false, status: 500 }) });
  assert.equal(allFail.status, "error");

  const allUnauth = await mod.fetchAttribution("p1", { fetchImpl: async () => ({ ok: false, status: 401 }) });
  assert.equal(allUnauth.status, "unauthenticated");

  const missing = await mod.fetchAttribution("p1", { fetchImpl: async () => ({ ok: true, status: 200, json: async () => ({}) }) });
  assert.equal(missing.status, "unsupported", "没有 attribution 字段 = 这次查询不支持归集");
});

test("归属取数：无工程时走全局聚合路由", async () => {
  const mod = await loadAttribution();
  const urls = [];
  await mod.fetchAttribution("", {
    fetchImpl: async url => {
      urls.push(url);
      return { ok: true, status: 200, json: async () => ({ attribution: attributionReport() }) };
    },
  });
  assert.ok(urls.every(u => u.startsWith("/api/v1/analytics?dimension=")), urls.join(","));
});

test("归属面板：用途维度翻成可读标签，原始值仍可核对", async () => {
  const mod = await load();
  const attributionMod = await loadAttribution();
  const attribution = {
    status: "ready",
    dimension: "purpose",
    dimensions: ["source", "node", "model", "purpose"],
    reports: {
      purpose: attributionMod.normalizeAttribution({
        dimension: "purpose",
        rollups: [
          { dimension: "purpose", value: "worker", scopes: ["call"], turns: 1, tokens: { total_tokens: 5 } },
          { dimension: "purpose", value: "lead", scopes: ["call"], turns: 1, tokens: { total_tokens: 5 } },
          { dimension: "purpose", value: "diagnose", scopes: ["call"], turns: 1, tokens: { total_tokens: 5 } },
        ],
        totals: { usage_unknown: false },
        completeness: "complete",
      }),
    },
    errors: {},
  };
  const html = mod.renderAttributionPanel(attribution, "zh");
  assert.ok(html.includes("Worker（执行）"));
  assert.ok(html.includes("Lead（规划）"));
  assert.ok(html.includes("诊断"));
  assert.ok(html.includes('data-dimension-value="diagnose"'), "原始值要留作核对");
});
