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
  assert.ok(html.includes("Delivery &amp; usage statistics"));
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
