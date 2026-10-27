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

  return '<div class="analytics" data-status="' +
    safeText(showUnknownBanner && status === "ready" ? "unknown" : (status || "ready")) + '">' +
    banner +
    '<div class="analytics__cards">' +
    renderDeliveriesCard(rep, lang) + renderInvestCard(rep, lang) + renderUsageCard(rep, lang) +
    "</div>" +
    '<div class="analytics__charts">' +
    renderSourceChart(rep, lang) + renderDayChart(rep, lang) +
    "</div>" +
    renderEvidence(rep, lang) + "</div>";
}
