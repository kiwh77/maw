// 交付与用量统计视图（vnext / UI · VN-744）。
//
// 目标：把 VN-805 统计报表画出来——成果（交付）与投入（时长·并发）分开，
// 累计／并集／平均并发各写明口径；未知不显示为零；来源分布与时间图共用
// token 色板与图例；每个汇总都带可下钻的证据数量与水位。
//
// 依据：
//   docs/contracts/vnext/observability/timeline-metering-contract-v1.md §4–§6
//   docs/contracts/vnext/ui/WORKBENCH_UI_CONTRACT.md（token／空态／双语）
//   docs/contracts/vnext/ui-data/view-context-contract-v1.md（日期与语言上下文）
//
// 非目标：不在页面重新聚合原始记录；不改公共计量口径。

import {
  ATTRIBUTION_DIMENSIONS,
  dimensionLabelKey,
  granularityOf,
  runDrillHref,
} from "./attribution.mjs";

export const VIEW_STATUSES = Object.freeze([
  "ready", "loading", "empty", "error", "expired", "unauthenticated", "unknown",
]);

// 三类口径的文案：累计（按轮去重）、并集（按身份并集）、平均并发（Σ身份并集/窗口）。
export const METRIC_DEFS = Object.freeze([
  { id: "cumulative", basis: "dedup_by_turn" },
  { id: "union", basis: "identity_union" },
  { id: "concurrency", basis: "union_over_window" },
]);

const I18N = {
  zh: {
    "analytics.title": "交付与用量统计",
    "analytics.subtitle": "成果与投入分开；累计、并集、平均并发各有口径；未知不显示为零。",
    "analytics.loading": "正在读取统计…",
    "analytics.empty": "这个范围里还没有统计结果。",
    "analytics.empty.hint": "有交付或轮次进入统计投影后，这里会给出汇总。",
    "analytics.error": "统计加载失败。",
    "analytics.error.hint": "确认本机中枢在运行，然后重试。",
    "analytics.expired": "统计数据已过期。",
    "analytics.expired.hint": "刷新后重新读取，不沿用上次汇总。",
    "analytics.unauthenticated": "未登录，无法查看统计。",
    "analytics.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "analytics.unknown": "统计不完整。",
    "analytics.unknown.hint": "投影水位落后或完整性未知；已观测部分仍可查看。",
    "no.project": "还没有选择工程。",
    "no.project.hint": "统计按工程物化：先在顶部选择工程，再看交付与投入。",
    "card.deliveries": "交付成果",
    "card.deliveries.basis": "口径：窗口内去重交付数（按交付身份）",
    "card.invest": "执行投入",
    "card.invest.basis": "口径：task_phase ∪ cli_process 按身份并集（秒）",
    "card.usage": "轮次用量",
    "card.usage.basis": "口径：token 按 (turn, scope) 最新代际去重累计",
    "deliveries.count": "{n} 笔交付",
    "deliveries.superseded": "其中 {n} 笔已被取代",
    "deliveries.mode.code": "代码",
    "deliveries.mode.non_code": "非代码",
    "invest.engineering": "工程时长（并集）",
    "invest.phase": "阶段驻留",
    "invest.cli": "CLI 运行",
    "invest.wait": "明确等待（另计）",
    "invest.concurrency": "平均并发身份",
    "concurrency.basis": "口径：Σ 各身份并集秒数 ÷ 窗口秒数",
    "usage.turns": "{n} 轮",
    "usage.shared": "共享轮次 {n}",
    "usage.tokens": "累计 token {n}",
    "usage.unknown": "部分未知",
    "usage.unknown_turns": "未知用量 {n} 轮",
    "usage.estimated_turns": "估算用量 {n} 轮",
    "usage.coverage": "观测覆盖",
    "coverage.zero": "分母为零，覆盖率记 0，不假装完整",
    "source.dist": "来源分布",
    "source.dist.basis": "口径：按来源命名空间去重轮次",
    "day.dist": "按日分布",
    "day.dist.basis": "口径：按查询时区归日的轮次与交付数",
    "legend.levels": "色阶：相对本图最大值分四档，0 值不进色阶",
    "evidence.title": "证据",
    "evidence.intervals": "区间 {n}",
    "evidence.turns": "轮次 {n}",
    "evidence.usage_records": "记账 {n}",
    "evidence.deliveries": "交付 {n}",
    "evidence.identities": "身份 {n}",
    "watermark.state": "状态水位",
    "watermark.projection": "投影水位",
    "cursors.completeness": "完整性",
    "source.unsupported": "来源不支持轮次",
    "kbd.hint": "键盘：Tab 进入图卡，方向键在读数间移动，Enter 展开证据",
    "quality.observed": "实测",
    "quality.estimated": "估算",
    "quality.unknown": "未知",
    "attr.title": "用量归属",
    "attr.subtitle": "按来源、节点、模型与用途查看用量与采集完整度；未知与估算如实标注。",
    "attr.basis": "口径：按用量行维度归集；每个比率都带分子/分母，分母为 0 时记「无分母」，不写 0%。",
    "attr.tab.hint": "切换维度只换归集键，窗口与去重口径不变。",
    "attr.loading": "正在读取归属统计…",
    "attr.error": "归属统计加载失败。",
    "attr.error.hint": "该维度暂时不可用；其余维度仍可查看。",
    "attr.retry": "重试",
    "attr.empty": "这个窗口里没有可归集的用量。",
    "attr.empty.hint": "没有记账不等于用量为零：先跑起执行或诊断，再回来看。",
    "attr.unsupported": "该维度暂不支持归集。",
    "attr.unauthenticated": "未登录，无法读取归属统计。",
    "attr.table.value": "维度值",
    "attr.table.turns": "轮次",
    "attr.table.tokens": "token（总量 / 输入+输出）",
    "attr.table.coverage": "观测覆盖",
    "attr.table.quality": "质量",
    "attr.table.granularity": "粒度",
    "attr.table.evidence": "证据",
    "attr.value.undeclared": "未声明",
    "attr.coverage.none": "无分母",
    "attr.granularity.call": "逐次调用",
    "attr.granularity.run": "运行级（旧 CLI 汇总）",
    "attr.granularity.mixed": "逐次 + 运行级",
    "attr.granularity.unknown": "粒度未知",
    "attr.quality.estimated_turns": "估算 {n} 轮",
    "attr.cache.verified": "缓存已归一 {n}",
    "attr.cache.unverified": "缓存未归一 {n}",
    "attr.totals.calls": "调用覆盖（有用量/调用）",
    "attr.totals.turns": "轮次覆盖（有用量/轮次）",
    "attr.totals.run_success": "Run 成功",
    "attr.totals.delivery_pass": "交付通过",
    "attr.totals.tokens": "累计 token",
    "attr.totals.cost": "估算成本",
    "attr.totals.cost.basis": "未配置价目表：只报 token 与质量，不编单价",
    "attr.partial": "部分未知：合计不含未知用量的轮次，覆盖率按已记账分母给出。",
    "attr.gaps": "缺测与口径说明",
    "attr.watermark": "归属水位",
    "attr.run.drill": "去 Run 列表核对",
    "attr.run.drill.missing_project": "选择工程后可跳到 Run 列表",
    "attr.run.grain": "Run 结果只按节点归集：其它维度没有归属证据，分维度留空只报合计。",
    "attr.gap.tool_seconds": "工具调用只有 journal 证据，没有独立区间来源：不用 Run 时间去顶替。",
    "attr.dimension.source": "来源",
    "attr.dimension.node": "节点",
    "attr.dimension.model": "模型",
    "attr.dimension.purpose": "用途",
    "attr.purpose.worker": "Worker（执行）",
    "attr.purpose.lead": "Lead（规划）",
    "attr.purpose.diagnose": "诊断",
  },
  en: {
    "analytics.title": "Delivery & usage statistics",
    "analytics.subtitle": "Outcomes and investment stay separate; cumulative, union and concurrency each state their basis; unknown is never shown as zero.",
    "analytics.loading": "Loading statistics…",
    "analytics.empty": "No statistics in this scope yet.",
    "analytics.empty.hint": "Summaries appear once deliveries or turns enter the projection.",
    "analytics.error": "Failed to load statistics.",
    "analytics.error.hint": "Confirm the local hub is running, then retry.",
    "analytics.expired": "Statistics data expired.",
    "analytics.expired.hint": "Refresh and reload; do not reuse the previous summary.",
    "analytics.unauthenticated": "Sign in to view statistics.",
    "analytics.unauthenticated.hint": "Use a local session or token, then retry.",
    "analytics.unknown": "Statistics are incomplete.",
    "analytics.unknown.hint": "Projection lags or completeness is unknown; observed parts remain visible.",
    "no.project": "No project selected.",
    "no.project.hint": "Statistics are materialised per project: pick one at the top first.",
    "card.deliveries": "Deliveries",
    "card.deliveries.basis": "Basis: distinct deliveries in window (by delivery id)",
    "card.invest": "Execution investment",
    "card.invest.basis": "Basis: task_phase ∪ cli_process union by identity (seconds)",
    "card.usage": "Turn usage",
    "card.usage.basis": "Basis: tokens deduped by latest (turn, scope) epoch, cumulative",
    "deliveries.count": "{n} deliveries",
    "deliveries.superseded": "{n} superseded",
    "deliveries.mode.code": "code",
    "deliveries.mode.non_code": "non-code",
    "invest.engineering": "Engineering time (union)",
    "invest.phase": "Phase dwell",
    "invest.cli": "CLI process",
    "invest.wait": "Explicit wait (separate)",
    "invest.concurrency": "Avg concurrent identities",
    "concurrency.basis": "Basis: Σ per-identity union seconds ÷ window seconds",
    "usage.turns": "{n} turns",
    "usage.shared": "{n} shared turns",
    "usage.tokens": "cumulative tokens {n}",
    "usage.unknown": "partially unknown",
    "usage.unknown_turns": "{n} turns with unknown usage",
    "usage.estimated_turns": "{n} estimated turns",
    "usage.coverage": "observed coverage",
    "coverage.zero": "zero denominator: ratio stays 0, never faked as complete",
    "source.dist": "Source distribution",
    "source.dist.basis": "Basis: turns deduped per source namespace",
    "day.dist": "Daily distribution",
    "day.dist.basis": "Basis: turns and deliveries grouped by query-timezone day",
    "legend.levels": "Scale: four levels relative to the chart max; zero never enters the scale",
    "evidence.title": "Evidence",
    "evidence.intervals": "intervals {n}",
    "evidence.turns": "turns {n}",
    "evidence.usage_records": "records {n}",
    "evidence.deliveries": "deliveries {n}",
    "evidence.identities": "identities {n}",
    "watermark.state": "State cursor",
    "watermark.projection": "Projection cursor",
    "cursors.completeness": "Completeness",
    "source.unsupported": "source provides no turns",
    "kbd.hint": "Keys: Tab into cards, arrows between readings, Enter expands evidence",
    "quality.observed": "Observed",
    "quality.estimated": "Estimated",
    "quality.unknown": "Unknown",
    "attr.title": "Usage attribution",
    "attr.subtitle": "Usage and collection completeness by source, node, model and purpose; unknown and estimated stay labelled.",
    "attr.basis": "Basis: grouped by the usage row dimension; every ratio states numerator/denominator, and a zero denominator reads \"no denominator\", never 0%.",
    "attr.tab.hint": "Switching dimension changes the grouping key only; window and dedupe stay the same.",
    "attr.loading": "Loading attribution…",
    "attr.error": "Failed to load attribution.",
    "attr.error.hint": "This dimension is unavailable for now; the others remain readable.",
    "attr.retry": "Retry",
    "attr.empty": "No usage to attribute in this window.",
    "attr.empty.hint": "No records is not zero usage: run an execution or diagnosis first, then come back.",
    "attr.unsupported": "Attribution is not supported for this dimension.",
    "attr.unauthenticated": "Sign in to read attribution.",
    "attr.table.value": "Value",
    "attr.table.turns": "Turns",
    "attr.table.tokens": "Tokens (total / input+output)",
    "attr.table.coverage": "Observed coverage",
    "attr.table.quality": "Quality",
    "attr.table.granularity": "Granularity",
    "attr.table.evidence": "Evidence",
    "attr.value.undeclared": "undeclared",
    "attr.coverage.none": "no denominator",
    "attr.granularity.call": "Per-call",
    "attr.granularity.run": "Run-level (legacy CLI summary)",
    "attr.granularity.mixed": "Per-call + run-level",
    "attr.granularity.unknown": "Granularity unknown",
    "attr.quality.estimated_turns": "{n} estimated turns",
    "attr.cache.verified": "cache normalised {n}",
    "attr.cache.unverified": "cache unverified {n}",
    "attr.totals.calls": "Call coverage (with usage / calls)",
    "attr.totals.turns": "Turn coverage (with usage / turns)",
    "attr.totals.run_success": "Run success",
    "attr.totals.delivery_pass": "Delivery pass",
    "attr.totals.tokens": "Cumulative tokens",
    "attr.totals.cost": "Estimated cost",
    "attr.totals.cost.basis": "No price table configured: tokens and quality only, unit price never invented",
    "attr.partial": "Partially unknown: totals exclude turns without usage; coverage uses the recorded denominator.",
    "attr.gaps": "Gaps and basis notes",
    "attr.watermark": "Attribution watermark",
    "attr.run.drill": "Verify in run list",
    "attr.run.drill.missing_project": "Pick a project to drill into runs",
    "attr.run.grain": "Run outcomes group by node only: other dimensions carry no attribution evidence, so per-dimension run results stay empty and only totals are reported.",
    "attr.gap.tool_seconds": "Tool calls have journal evidence only and no independent interval source; run time is never substituted.",
    "attr.dimension.source": "Source",
    "attr.dimension.node": "Node",
    "attr.dimension.model": "Model",
    "attr.dimension.purpose": "Purpose",
    "attr.purpose.worker": "Worker (execution)",
    "attr.purpose.lead": "Lead (planning)",
    "attr.purpose.diagnose": "Diagnose",
  },
};

export function t(key, lang = "zh", params = {}) {
  const table = I18N[lang === "en" ? "en" : "zh"] || I18N.zh;
  let s = table[key] || I18N.zh[key] || key;
  for (const [k, v] of Object.entries(params || {})) {
    s = s.replaceAll("{" + k + "}", String(v));
  }
  return s;
}

export function safeText(value) {
  return String(value ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

export const esc = safeText;

/** 规范化报表：缺字段一律视为「未观测」，不填 0。 */
export function normalizeReport(raw = {}) {
  const usage = raw.usage || {};
  const tokens = usage.tokens || {};
  const timeline = raw.timeline || {};
  const deliveries = raw.deliveries || {};
  return {
    scope: raw.scope || {},
    generated_at: raw.generated_at || "",
    completeness: raw.completeness || "",
    watermark: raw.watermark || null,
    timeline: {
      task_phase_seconds: numOrNull(timeline.task_phase_seconds),
      cli_process_seconds: numOrNull(timeline.cli_process_seconds),
      turn_seconds: numOrNull(timeline.turn_seconds),
      explicit_wait_seconds: numOrNull(timeline.explicit_wait_seconds),
      engineering_seconds: numOrNull(timeline.engineering_seconds),
      avg_concurrency: typeof timeline.avg_concurrency === "number" ? timeline.avg_concurrency : null,
    },
    usage: {
      turn_count: numOrNull(usage.turn_count),
      shared_turns: numOrNull(usage.shared_turns),
      unknown_turns: numOrNull(usage.unknown_turns),
      estimated_turns: numOrNull(usage.estimated_turns),
      tokens: {
        total_tokens: numOrNull(tokens.total_tokens),
        input_tokens: numOrNull(tokens.input_tokens),
        output_tokens: numOrNull(tokens.output_tokens),
      },
      usage_unknown: usage.usage_unknown === true,
      coverage: {
        observed_turns: numOrNull((usage.coverage || {}).observed_turns),
        observed_denom: numOrNull((usage.coverage || {}).observed_denom),
        ratio: typeof (usage.coverage || {}).ratio === "number" ? usage.coverage.ratio : 0,
      },
    },
    deliveries: {
      count: numOrNull(deliveries.count),
      superseded: numOrNull(deliveries.superseded),
      by_mode: deliveries.by_mode && typeof deliveries.by_mode === "object" ? deliveries.by_mode : {},
      ids: Array.isArray(deliveries.ids) ? deliveries.ids.map(String) : [],
    },
    by_source: Array.isArray(raw.by_source) ? raw.by_source.map(s => ({
      source_ns: String(s.source_ns || ""),
      turn_count: numOrNull(s.turn_count),
      usage_unknown: s.usage_unknown === true,
      turns_supported: s.turns_supported !== false,
    })) : [],
    by_day: Array.isArray(raw.by_day) ? raw.by_day.map(d => ({
      day: String(d.day || ""),
      delivery_count: numOrNull(d.delivery_count),
      turn_count: numOrNull(d.turn_count),
    })) : [],
    evidence: raw.evidence || null,
  };
}

function numOrNull(v) {
  // null／undefined 是「未观测」，不是 0——「未知不显示为零」的落点。
  if (v == null) return null;
  return Number.isFinite(Number(v)) && Number(v) >= 0 ? Number(v) : null;
}

/** 相对最大值的 0–4 档；0 值与缺测不进色阶（与活动视图同规）。 */
export function levelOf(value, max) {
  if (!(max > 0) || !(value > 0)) return 0;
  return Math.min(4, Math.max(1, Math.ceil((value / max) * 4)));
}

function emptyState(lang, keys) {
  return '<div class="empty-state"><p class="empty-state__text">' + safeText(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + safeText(t(keys[1], lang)) + "</p></div>";
}

function unknownValue(lang) {
  return '<span class="analytics__unknown" data-unknown="true">' + safeText(t("quality.unknown", lang)) + "</span>";
}

function numText(v, lang) {
  return v == null ? unknownValue(lang) : safeText(String(v));
}

function renderDeliveriesCard(rep, lang) {
  const d = rep.deliveries;
  const rows = [
    [t("deliveries.count", lang, { n: d.count == null ? t("quality.unknown", lang) : d.count }), "count"],
    [t("deliveries.superseded", lang, { n: d.superseded == null ? t("quality.unknown", lang) : d.superseded }), "superseded"],
    [t("deliveries.mode.code", lang), numText(d.by_mode.code ?? null, lang)],
    [t("deliveries.mode.non_code", lang), numText(d.by_mode.non_code ?? null, lang)],
  ];
  return '<section class="analytics__card" data-card="deliveries" tabindex="0">' +
    "<h2>" + safeText(t("card.deliveries", lang)) + "</h2>" +
    '<p class="field-hint">' + safeText(t("card.deliveries.basis", lang)) + "</p>" +
    '<dl class="analytics__reads">' + rows.map(([k, v]) =>
      "<div><dt>" + safeText(k) + "</dt><dd>" + v + "</dd></div>").join("") + "</dl></section>";
}

function renderInvestCard(rep, lang) {
  const tl = rep.timeline;
  const rows = [
    [t("invest.engineering", lang), numText(tl.engineering_seconds, lang) + (tl.engineering_seconds == null ? "" : " s")],
    [t("invest.phase", lang), numText(tl.task_phase_seconds, lang) + (tl.task_phase_seconds == null ? "" : " s")],
    [t("invest.cli", lang), numText(tl.cli_process_seconds, lang) + (tl.cli_process_seconds == null ? "" : " s")],
    [t("invest.wait", lang), numText(tl.explicit_wait_seconds, lang) + (tl.explicit_wait_seconds == null ? "" : " s")],
    [t("invest.concurrency", lang), tl.avg_concurrency == null ? unknownValue(lang) : safeText(tl.avg_concurrency.toFixed(3))],
  ];
  return '<section class="analytics__card" data-card="invest" tabindex="0">' +
    "<h2>" + safeText(t("card.invest", lang)) + "</h2>" +
    '<p class="field-hint">' + safeText(t("card.invest.basis", lang)) + "</p>" +
    '<p class="field-hint">' + safeText(t("concurrency.basis", lang)) + "</p>" +
    '<dl class="analytics__reads">' + rows.map(([k, v]) =>
      "<div><dt>" + safeText(k) + "</dt><dd>" + v + "</dd></div>").join("") + "</dl></section>";
}

function renderUsageCard(rep, lang) {
  const u = rep.usage;
  const tokens = u.tokens.total_tokens == null ? unknownValue(lang) : safeText(String(u.tokens.total_tokens));
  const denom = u.coverage.observed_denom;
  const coverage = denom == null || denom === 0
    ? safeText("0/0") + ' <span class="field-hint">' + safeText(t("coverage.zero", lang)) + "</span>"
    : safeText(u.coverage.observed_turns + "/" + denom);
  const rows = [
    [t("usage.turns", lang), u.turn_count == null ? unknownValue(lang) : safeText(String(u.turn_count))],
    [t("usage.shared", lang), u.shared_turns == null ? unknownValue(lang) : safeText(String(u.shared_turns))],
    [t("usage.tokens", lang), tokens],
    [t("usage.unknown_turns", lang), u.unknown_turns == null ? unknownValue(lang) : safeText(String(u.unknown_turns))],
    [t("usage.estimated_turns", lang), u.estimated_turns == null ? unknownValue(lang) : safeText(String(u.estimated_turns))],
    [t("usage.coverage", lang), coverage],
  ];
  return '<section class="analytics__card" data-card="usage" tabindex="0">' +
    "<h2>" + safeText(t("card.usage", lang)) + "</h2>" +
    '<p class="field-hint">' + safeText(t("card.usage.basis", lang)) + "</p>" +
    '<dl class="analytics__reads">' + rows.map(([k, v]) =>
      "<div data-read><dt>" + safeText(k) + "</dt><dd>" + v + "</dd></div>").join("") + "</dl>" +
    (u.usage_unknown ? '<p class="analytics__unknown-banner">' + safeText(t("usage.unknown", lang)) + "</p>" : "") +
    "</section>";
}

/** 来源分布：共享色阶条 + 数字读数，每行可追到轮次去重数量。 */
function renderSourceChart(rep, lang) {
  const rows = rep.by_source;
  if (!rows.length) return "";
  const max = Math.max(...rows.map(r => r.turn_count ?? 0), 0);
  const bars = rows.map(r => {
    const v = r.turn_count ?? 0;
    const label = r.turns_supported ? String(v) : t("source.unsupported", lang);
    return '<div class="analytics__bar-row" data-source="' + safeText(r.source_ns) + '">' +
      '<span class="analytics__bar-label">' + safeText(r.source_ns) +
      (r.usage_unknown ? ' <span class="analytics__unknown" data-unknown="true">' + safeText(t("usage.unknown", lang)) + "</span>" : "") +
      "</span>" +
      '<span class="analytics__bar analytics__bar--l' + levelOf(v, max) + '" data-level="' + levelOf(v, max) + '" aria-hidden="true"></span>' +
      '<span class="analytics__bar-value">' + safeText(label) + "</span></div>";
  }).join("");
  return '<section class="analytics__chart" data-chart="sources" tabindex="0">' +
    "<h2>" + safeText(t("source.dist", lang)) + "</h2>" +
    '<p class="field-hint">' + safeText(t("source.dist.basis", lang)) + "</p>" +
    bars +
    '<p class="field-hint analytics__legend" data-legend="levels">' + safeText(t("legend.levels", lang)) + "</p>" +
    "</section>";
}

/** 按日分布：交付数与轮次数共用同一色板。 */
function renderDayChart(rep, lang) {
  const rows = rep.by_day;
  if (!rows.length) return "";
  const maxTurns = Math.max(...rows.map(r => r.turn_count ?? 0), 0);
  const maxDeliveries = Math.max(...rows.map(r => r.delivery_count ?? 0), 0);
  const bars = rows.map(r => {
    const turns = r.turn_count ?? 0;
    const dels = r.delivery_count ?? 0;
    return '<div class="analytics__bar-row" data-day="' + safeText(r.day) + '">' +
      '<span class="analytics__bar-label">' + safeText(r.day) + "</span>" +
      '<span class="analytics__bar analytics__bar--l' + levelOf(turns, maxTurns) + '" data-kind="turns" data-level="' + levelOf(turns, maxTurns) + '" aria-hidden="true"></span>' +
      '<span class="analytics__bar analytics__bar--l' + levelOf(dels, maxDeliveries) + ' analytics__bar--half" data-kind="deliveries" data-level="' + levelOf(dels, maxDeliveries) + '" aria-hidden="true"></span>' +
      '<span class="analytics__bar-value">' + safeText(t("usage.turns", lang, { n: turns })) +
      " · " + safeText(t("deliveries.count", lang, { n: dels })) + "</span></div>";
  }).join("");
  return '<section class="analytics__chart" data-chart="days" tabindex="0">' +
    "<h2>" + safeText(t("day.dist", lang)) + "</h2>" +
    '<p class="field-hint">' + safeText(t("day.dist.basis", lang)) + "</p>" +
    bars +
    '<p class="field-hint analytics__legend" data-legend="levels">' + safeText(t("legend.levels", lang)) + "</p>" +
    "</section>";
}

function renderEvidence(rep, lang) {
  const ev = rep.evidence;
  const wm = rep.watermark;
  let html = '<section class="analytics__evidence" data-evidence tabindex="0"><h2>' +
    safeText(t("evidence.title", lang)) + "</h2>";
  if (ev) {
    html += '<dl class="analytics__reads">' + [
      ["evidence.intervals", ev.intervals],
      ["evidence.turns", ev.turns],
      ["evidence.usage_records", ev.usage_records],
      ["evidence.deliveries", ev.deliveries],
      ["evidence.identities", ev.identities],
    ].map(([k, v]) => "<div><dt>" + safeText(t(k, lang, { n: v == null ? "—" : v })) + "</dt><dd>" +
      (v == null ? unknownValue(lang) : safeText(String(v))) + "</dd></div>").join("") + "</dl>";
  }
  if (wm) {
    html += '<p class="analytics__cursors">' +
      safeText(t("watermark.state", lang)) + "=" + safeText(wm.cursor ?? "—") + " · " +
      safeText(t("watermark.projection", lang)) + "=" + safeText(wm.complete_before ?? "—") +
      (rep.completeness ? " · " + safeText(t("cursors.completeness", lang)) + "=" + safeText(rep.completeness) : "") +
      "</p>";
  }
  return html + "</section>";
}

// ---------- 用量归属（PI-203） ----------

/** 比率：分子/分母逐字给出；分母为 0 时比率是 null，写「无分母」而不是 0%。 */
function ratioText(den, lang) {
  if (!den) return unknownValue(lang);
  const base = den.numerator + "/" + den.denominator;
  if (den.ratio == null) {
    return safeText(base) + ' <span class="field-hint">' + safeText(t("attr.coverage.none", lang)) + "</span>";
  }
  return safeText(base + " (" + Math.round(den.ratio * 100) + "%)");
}

function granularityBadge(grain, lang) {
  const cls = grain === "call" ? "badge--done" : grain === "run" ? "badge--claimed" : "badge--in_review";
  return '<span class="badge ' + cls + '" data-granularity="' + safeText(grain) + '">' +
    safeText(t("attr.granularity." + grain, lang)) + "</span>";
}

/** 质量标记：未知、估算与缓存归一分开说，不合并成一个「大概能用」。 */
function attributionQuality(row, lang) {
  const out = [];
  if (row.usageUnknown) {
    out.push('<span class="analytics__unknown" data-unknown="true">' +
      safeText(t("quality.unknown", lang)) + "</span>");
  }
  if (row.estimatedTurns != null && row.estimatedTurns > 0) {
    out.push('<span class="badge badge--claimed" data-quality="estimated">' +
      safeText(t("attr.quality.estimated_turns", lang, { n: row.estimatedTurns })) + "</span>");
  }
  if (row.cacheUnverified != null && row.cacheUnverified > 0) {
    out.push('<span class="analytics__unknown" data-cache="unverified">' +
      safeText(t("attr.cache.unverified", lang, { n: row.cacheUnverified })) + "</span>");
  }
  if (row.cacheNormalized != null && row.cacheNormalized > 0) {
    out.push('<span class="field-hint" data-cache="verified">' +
      safeText(t("attr.cache.verified", lang, { n: row.cacheNormalized })) + "</span>");
  }
  if (!out.length) out.push('<span class="field-hint">' + safeText(t("quality.observed", lang)) + "</span>");
  return out.join(" ");
}

/** token：合计为 null 就写未知；输入/输出缺一项时用 ? 标出，不补 0。 */
function attributionTokens(row, lang) {
  const tk = row.tokens;
  if (tk.total == null) return unknownValue(lang);
  const parts = [];
  if (tk.input != null || tk.output != null) {
    parts.push((tk.input == null ? "?" : tk.input) + " + " + (tk.output == null ? "?" : tk.output));
  }
  return safeText(String(tk.total)) +
    (parts.length ? ' <span class="field-hint">(' + safeText(parts[0]) + ")</span>" : "");
}

/** 用途是封闭集合：翻成可读标签，原始值仍留在 data 属性里可核对。 */
const PURPOSE_LABEL_KEYS = Object.freeze({
  worker: "attr.purpose.worker",
  lead: "attr.purpose.lead",
  diagnose: "attr.purpose.diagnose",
});

function attributionValue(row, lang) {
  if (row.value === "") {
    return '<span class="analytics__undeclared" data-undeclared="true">' +
      safeText(t("attr.value.undeclared", lang)) + "</span>";
  }
  const key = row.dimension === "purpose" ? PURPOSE_LABEL_KEYS[row.value] : "";
  return key ? safeText(t(key, lang)) : safeText(row.value);
}

function renderAttributionRow(row, lang) {
  const value = attributionValue(row, lang);
  const grain = granularityOf(row);
  return '<tr data-attr-row="1" data-dimension-value="' + safeText(row.value) +
    '" data-granularity="' + safeText(grain) + '">' +
    '<td class="analytics__attr-value">' + value + "</td>" +
    "<td>" + (row.turns == null ? unknownValue(lang) : safeText(String(row.turns))) + "</td>" +
    "<td>" + attributionTokens(row, lang) + "</td>" +
    "<td>" + ratioText(row.observed, lang) + "</td>" +
    '<td class="analytics__attr-quality">' + attributionQuality(row, lang) + "</td>" +
    "<td>" + granularityBadge(grain, lang) + "</td>" +
    '<td class="analytics__attr-evidence">' + safeText(row.evidence.join(" · ")) + "</td></tr>";
}

function renderAttributionTotals(rep, lang) {
  const tot = rep.totals;
  const rows = [
    [t("attr.totals.calls", lang), ratioText(tot.calls, lang)],
    [t("attr.totals.turns", lang), ratioText(tot.turns, lang)],
    [t("attr.totals.run_success", lang), ratioText(tot.runSuccess, lang)],
    [t("attr.totals.delivery_pass", lang), ratioText(tot.deliveryPass, lang)],
    [t("attr.totals.tokens", lang), tot.tokens.total == null ? unknownValue(lang) : safeText(String(tot.tokens.total))],
    [t("attr.totals.cost", lang), tot.cost.amountUSD == null
      ? '<span class="field-hint">' + safeText(t("attr.totals.cost.basis", lang)) + "</span>"
      : safeText("$" + tot.cost.amountUSD)],
  ];
  return '<dl class="analytics__reads analytics__attr-totals" data-attr-totals>' +
    rows.map(([k, v]) => "<div><dt>" + safeText(k) + "</dt><dd>" + v + "</dd></div>").join("") +
    "</dl>";
}

function renderAttributionTabs(dimensions, active, errors, lang) {
  const buttons = dimensions.map(dim => {
    const err = errors[dim];
    return '<button type="button" class="analytics__attr-tab' +
      (dim === active ? " is-active" : "") + '" role="tab"' +
      ' aria-selected="' + (dim === active ? "true" : "false") + '"' +
      ' data-attr-dimension="' + safeText(dim) + '"' +
      (err ? ' data-dimension-error="' + safeText(err) + '"' : "") + ">" +
      safeText(t(dimensionLabelKey(dim), lang)) + "</button>";
  }).join("");
  return '<div class="analytics__attr-tabs" role="tablist" data-attr-tabs>' + buttons + "</div>";
}

/** 缺测说明：已知字段翻成本地文案，未知字段原样保留（不猜语义）。 */
function renderAttributionGaps(rep, lang) {
  if (!rep.gaps.length) return "";
  const known = {
    runs_by_dimension: "attr.run.grain",
    tool_seconds: "attr.gap.tool_seconds",
  };
  const items = rep.gaps.map(gap => {
    const key = known[gap.field];
    const text = key ? t(key, lang) : gap.reason;
    return "<li><code>" + safeText(gap.field) + "</code> " + safeText(text) + "</li>";
  }).join("");
  return '<div class="analytics__attr-gaps" data-attr-gaps><h3>' +
    safeText(t("attr.gaps", lang)) + "</h3><ul>" + items + "</ul></div>";
}

function renderAttributionFooter(rep, project, lang) {
  const wm = rep.watermark;
  const drill = project
    ? '<a class="analytics__attr-drill-link" href="' + safeText(runDrillHref(project)) +
      '" data-drill="runs">' + safeText(t("attr.run.drill", lang)) + "</a>"
    : '<span class="field-hint">' + safeText(t("attr.run.drill.missing_project", lang)) + "</span>";
  return '<div class="analytics__attr-footer" data-attr-footer>' +
    '<p class="analytics__attr-drill">' + drill + ' <span class="field-hint">' +
    safeText(t("attr.run.grain", lang)) + "</span></p>" +
    (wm
      ? '<p class="analytics__cursors">' + safeText(t("attr.watermark", lang)) + "=" +
        safeText(wm.cursor ?? "—") + " · " + safeText(wm.complete_before ?? "—") + "</p>"
      : "") +
    "</div>";
}

/**
 * 用量归属面板。
 *
 * @param {object} attribution { status, dimension, dimensions, reports, errors }
 * @param {string} lang
 * @param {object} options { project }
 */
export function renderAttributionPanel(attribution, lang = "zh", options = {}) {
  const state = attribution && typeof attribution === "object" ? attribution : null;
  if (!state) return "";
  const status = state.status || "loading";
  const head = '<div class="analytics__attr-head"><h2>' + safeText(t("attr.title", lang)) + "</h2>" +
    '<p class="field-hint">' + safeText(t("attr.basis", lang)) + "</p></div>";
  const open = extra => '<section class="analytics__attribution" data-attribution data-status="' +
    safeText(status) + '"' + (extra || "") + ">";

  if (status === "loading") {
    return open() + head + '<p class="nodes-loading">' + safeText(t("attr.loading", lang)) + "</p></section>";
  }
  if (status === "unauthenticated") {
    return open() + head +
      emptyState(lang, ["attr.unauthenticated", "attr.error.hint"]) + "</section>";
  }
  if (status === "unsupported") {
    return open() + head + emptyState(lang, ["attr.unsupported", "attr.error.hint"]) + "</section>";
  }
  if (status === "error") {
    return open() + head +
      '<div class="empty-state" data-attr-error><p class="empty-state__text">' +
      safeText(t("attr.error", lang)) + '</p><p class="empty-state__text">' +
      safeText(t("attr.error.hint", lang)) + '</p><button type="button" class="analytics__attr-retry"' +
      ' data-action="retry-attribution">' + safeText(t("attr.retry", lang)) + "</button></div></section>";
  }

  const dimensions = Array.isArray(state.dimensions) && state.dimensions.length
    ? state.dimensions.filter(dim => ATTRIBUTION_DIMENSIONS.includes(dim))
    : [...ATTRIBUTION_DIMENSIONS];
  const active = dimensions.includes(state.dimension) ? state.dimension : dimensions[0];
  const reports = state.reports && typeof state.reports === "object" ? state.reports : {};
  const errors = state.errors && typeof state.errors === "object" ? state.errors : {};
  const rep = reports[active];
  const project = String(options.project || (rep && rep.scope && rep.scope.projectId) || "").trim();

  let body = "";
  if (!rep) {
    body = '<div class="empty-state" data-attr-error><p class="empty-state__text">' +
      safeText(t("attr.error", lang)) + '</p><p class="empty-state__text">' +
      safeText(errors[active] ? String(errors[active]) : t("attr.error.hint", lang)) +
      '</p><button type="button" class="analytics__attr-retry" data-action="retry-attribution">' +
      safeText(t("attr.retry", lang)) + "</button></div>";
  } else {
    const partial = rep.completeness !== "complete" || rep.totals.usageUnknown;
    const rows = rep.rollups.map(row => renderAttributionRow(row, lang)).join("");
    const headers = [
      ["attr.table.value", "left"],
      ["attr.table.turns", "num"],
      ["attr.table.tokens", "num"],
      ["attr.table.coverage", "num"],
      ["attr.table.quality", "left"],
      ["attr.table.granularity", "left"],
      ["attr.table.evidence", "left"],
    ].map(([key, kind]) => '<th scope="col"' + (kind === "num" ? ' class="analytics__num"' : "") + ">" +
      safeText(t(key, lang)) + "</th>").join("");
    body =
      (partial
        ? '<p class="analytics__missing-banner" data-attr-partial="true">' +
          safeText(t("attr.partial", lang)) + "</p>"
        : "") +
      renderAttributionTotals(rep, lang) +
      (rows
        ? '<div class="table-wrap"><table class="table analytics__attr-table" data-attr-table>' +
          "<thead><tr>" + headers + "</tr></thead><tbody>" + rows + "</tbody></table></div>"
        : '<p class="analytics__attr-empty" data-attr-empty>' + safeText(t("attr.empty", lang)) +
          '</p><p class="field-hint">' + safeText(t("attr.empty.hint", lang)) + "</p>") +
      renderAttributionGaps(rep, lang) +
      renderAttributionFooter(rep, project, lang);
  }

  return open(' data-dimension="' + safeText(active) + '"') + head +
    renderAttributionTabs(dimensions, active, errors, lang) +
    '<p class="field-hint analytics__attr-tab-hint">' + safeText(t("attr.tab.hint", lang)) + "</p>" +
    body + "</section>";
}

/**
 * 渲染统计视图。
 * @param {object} state { status, report }
 * @param {object} options { lang }
 */
export function renderAnalytics(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const status = state.status || "ready";

  if (status === "loading") {
    return '<div class="analytics" data-status="loading"><p class="nodes-loading">' +
      safeText(t("analytics.loading", lang)) + "</p></div>";
  }
  if (status === "unauthenticated") {
    return '<div class="analytics" data-status="unauthenticated">' +
      emptyState(lang, ["analytics.unauthenticated", "analytics.unauthenticated.hint"]) + "</div>";
  }
  if (status === "error") {
    return '<div class="analytics" data-status="error">' +
      emptyState(lang, ["analytics.error", "analytics.error.hint"]) + "</div>";
  }
  if (status === "expired") {
    return '<div class="analytics" data-status="expired">' +
      emptyState(lang, ["analytics.expired", "analytics.expired.hint"]) + "</div>";
  }
  // 未选工程不是「这段时间没有交付」：统计按工程物化，要先选工程。
  if (status === "no_project") {
    return '<div class="analytics" data-status="no_project">' +
      emptyState(lang, ["no.project", "no.project.hint"]) + "</div>";
  }

  const rep = normalizeReport(state.report || {});
  const completeness = rep.completeness;
  const showUnknownBanner = status === "unknown" || completeness === "partial" ||
    completeness === "unknown";

  const hasData = rep.deliveries.count != null || rep.usage.turn_count != null ||
    rep.timeline.engineering_seconds != null;

  if (!hasData && !showUnknownBanner) {
    return '<div class="analytics" data-status="empty">' +
      emptyState(lang, ["analytics.empty", "analytics.empty.hint"]) + "</div>";
  }

  const banner = showUnknownBanner
    ? '<p class="analytics__missing-banner">' + safeText(t("analytics.unknown", lang)) +
      " " + safeText(t("analytics.unknown.hint", lang)) + "</p>"
    : "";

  const project = String(options.project || (rep.scope && rep.scope.project_id) || "").trim();
  const attribution = renderAttributionPanel(state.attribution, lang, { project });

  return '<div class="analytics" data-status="' +
    safeText(showUnknownBanner && status === "ready" ? "unknown" : (status || "ready")) + '">' +
    banner +
    '<div class="analytics__cards">' +
    renderDeliveriesCard(rep, lang) + renderInvestCard(rep, lang) + renderUsageCard(rep, lang) +
    "</div>" +
    attribution +
    '<div class="analytics__charts">' +
    renderSourceChart(rep, lang) + renderDayChart(rep, lang) +
    "</div>" +
    renderEvidence(rep, lang) + "</div>";
}
