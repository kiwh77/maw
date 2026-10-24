// 会话轮次与来源明细视图（vnext / UI · VN-743）。
//
// 目标：对可观测的来源会话展示轮次、跨度和关联任务；
// 共享／未归属关联、完整性与已知用量分开呈现，不强制「一会话＝一任务」。
// 不支持轮次的来源给出一致的降级态；跳转时间轴的入口只在轮次有真实
// 开始时间（可核对的证据）时出现。
//
// 依据：
//   docs/contracts/vnext/observability/timeline-metering-contract-v1.md §4（用量）、§5（聚合）
//   docs/contracts/vnext/ui/WORKBENCH_UI_CONTRACT.md（token／空态／双语）
//   docs/contracts/vnext/ui-data/view-context-contract-v1.md（日期与语言上下文）
//   数据来自 VN-804 的会话归属／计量与 VN-308 的来源能力声明；本视图不重新计量。
//
// 非目标：不改公共计量口径；不把来源文本解释为可执行指令。

export const VIEW_STATUSES = Object.freeze([
  "ready", "loading", "empty", "error", "expired", "unauthenticated", "unknown",
]);

const I18N = {
  zh: {
    "sessions.title": "来源会话",
    "sessions.subtitle": "会话、轮次与任务分开看；共享与未归属如实标注，不强制一会话等于一任务。",
    "sessions.loading": "正在读取来源会话…",
    "sessions.empty": "还没有可观测的来源会话。",
    "sessions.empty.hint": "有来源运行并产生轮次后，这里会列出会话明细。",
    "sessions.error": "来源会话加载失败。",
    "sessions.error.hint": "确认本机中枢在运行，然后重试。",
    "sessions.expired": "来源会话数据已过期。",
    "sessions.expired.hint": "刷新后重新读取，不沿用上次列表。",
    "sessions.unauthenticated": "未登录，无法查看来源会话。",
    "sessions.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "sessions.unknown": "部分会话数据不完整。",
    "sessions.unknown.hint": "投影水位落后或完整性未知；已观测部分仍可查看。",
    "col.session": "会话",
    "col.source": "来源",
    "col.opened": "打开时间",
    "col.turns": "轮次",
    "col.tasks": "关联任务",
    "col.usage": "已知用量",
    "turn.count": "{n} 轮",
    "turn.list": "轮次明细",
    "turn.started": "开始",
    "turn.ended": "结束",
    "turn.no_end": "未结束／缺边界",
    "turn.no_start": "缺开始边界",
    "turn.shared": "共享轮次",
    "turn.shared_with": "共享给 {n} 个任务",
    "turn.unallocated": "未归属",
    "turn.unknown_usage": "未知用量",
    "turn.jump": "在时间轴打开这一小时",
    "turn.jump.hint": "时间轴将打开同一小时窗口（半开区间）。",
    "usage.total": "总量 {n} tok",
    "usage.input": "输入 {n}",
    "usage.output": "输出 {n}",
    "usage.unknown": "部分未知",
    "usage.none": "未观测",
    "task.none": "未归属任何任务",
    "task.count": "{n} 个任务",
    "session.selected": "会话详情",
    "session.turns_unsupported": "该来源不支持轮次明细。",
    "session.turns_unsupported.hint": "来源未提供可核对的轮次记录；这里保持降级态，不推断轮次。",
    "detail.none": "未选择会话；用 Tab 或方向键在列表上移动。",
    "cursors.completeness": "完整性",
    "kbd.hint": "键盘：↑ ↓ 移动选择，Enter 打开详情，Esc 关闭",
    "quality.observed": "实测",
    "quality.estimated": "估算",
    "quality.unknown": "未知",
  },
  en: {
    "sessions.title": "Source sessions",
    "sessions.subtitle": "Sessions, turns and tasks stay separate; shared and unattributed links are labeled as-is.",
    "sessions.loading": "Loading source sessions…",
    "sessions.empty": "No observable source sessions yet.",
    "sessions.empty.hint": "Sessions appear here once source runs produce turns.",
    "sessions.error": "Failed to load source sessions.",
    "sessions.error.hint": "Confirm the local hub is running, then retry.",
    "sessions.expired": "Source session data expired.",
    "sessions.expired.hint": "Refresh and reload; do not reuse the previous list.",
    "sessions.unauthenticated": "Sign in to view source sessions.",
    "sessions.unauthenticated.hint": "Use a local session or token, then retry.",
    "sessions.unknown": "Some session data is incomplete.",
    "sessions.unknown.hint": "Projection lags or completeness is unknown; observed parts remain visible.",
    "col.session": "Session",
    "col.source": "Source",
    "col.opened": "Opened",
    "col.turns": "Turns",
    "col.tasks": "Linked tasks",
    "col.usage": "Known usage",
    "turn.count": "{n} turns",
    "turn.list": "Turn detail",
    "turn.started": "Start",
    "turn.ended": "End",
    "turn.no_end": "Open / missing end",
    "turn.no_start": "Missing start",
    "turn.shared": "Shared turn",
    "turn.shared_with": "Shared with {n} tasks",
    "turn.unallocated": "Unattributed",
    "turn.unknown_usage": "Unknown usage",
    "turn.jump": "Open this hour in the timeline",
    "turn.jump.hint": "The timeline opens the same half-open hour window.",
    "usage.total": "total {n} tok",
    "usage.input": "in {n}",
    "usage.output": "out {n}",
    "usage.unknown": "partially unknown",
    "usage.none": "unobserved",
    "task.none": "attributed to no task",
    "task.count": "{n} tasks",
    "session.selected": "Session detail",
    "session.turns_unsupported": "This source does not provide turn detail.",
    "session.turns_unsupported.hint": "The source offers no verifiable turn records; this stays degraded, turns are not inferred.",
    "detail.none": "No session selected; use Tab or arrow keys on the list.",
    "cursors.completeness": "Completeness",
    "kbd.hint": "Keys: ↑ ↓ move, Enter opens detail, Esc closes",
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

/**
 * 规范化服务端会话：不发明轮次，不合并来源文本。
 * turns_supported 为 false 的会话保持降级态；缺边界的轮次如实保留 null。
 */
export function normalizeSessions(raw = []) {
  const list = Array.isArray(raw) ? raw : [];
  return list.map((s, i) => {
    const turnsSupported = s.turns_supported !== false;
    const turns = turnsSupported && Array.isArray(s.turns) ? s.turns.map(normalizeTurn) : [];
    return {
      session_id: String(s.session_id || ("sess_" + i)),
      source_ns: String(s.source_ns || ""),
      adapter: String(s.adapter || ""),
      opened_at: String(s.opened_at || ""),
      closed_at: s.closed_at || "",
      turns_supported: turnsSupported,
      degrade_reason: String(s.degrade_reason || ""),
      turns,
      // tasks 是关联任务实例列表；空数组 = 未归属（不得压成一个任务）。
      tasks: Array.isArray(s.tasks) ? s.tasks.map(String) : [],
      usage: normalizeUsage(s.usage),
    };
  });
}

function normalizeTurn(turn, i) {
  const usage = normalizeUsage(turn && turn.usage);
  const startedAt = turn && turn.started_at ? String(turn.started_at) : "";
  const endedAt = turn && turn.ended_at ? String(turn.ended_at) : "";
  return {
    turn_id: String((turn && turn.turn_id) || ("tu_" + i)),
    started_at: startedAt,
    ended_at: endedAt,
    startMs: startedAt ? Date.parse(startedAt) || null : null,
    endMs: endedAt ? Date.parse(endedAt) || null : null,
    shared_with: Array.isArray(turn && turn.shared_with) ? turn.shared_with.map(String) : [],
    usage,
  };
}

function normalizeUsage(usage) {
  if (!usage || typeof usage !== "object") {
    return { present: false, total_tokens: null, input_tokens: null, output_tokens: null, usage_unknown: false, quality: "unknown" };
  }
  const num = v => (Number.isFinite(Number(v)) && Number(v) >= 0 ? Number(v) : null);
  return {
    present: true,
    total_tokens: num(usage.total_tokens),
    input_tokens: num(usage.input_tokens),
    output_tokens: num(usage.output_tokens),
    usage_unknown: usage.usage_unknown === true || num(usage.total_tokens) == null,
    quality: ["observed", "estimated", "unknown"].includes(usage.quality) ? usage.quality : "unknown",
  };
}

/** 已知用量的文案：未知不填 0，部分未知必须显示出来。 */
export function usageText(usage, lang) {
  if (!usage || !usage.present) return t("usage.none", lang);
  if (usage.usage_unknown || usage.total_tokens == null) return t("usage.unknown", lang);
  return t("usage.total", lang, { n: usage.total_tokens });
}

/** 轮次徽章：共享、未归属、缺边界、未知用量——非颜色的文字提示。 */
export function turnBadges(turn, lang) {
  const badges = [];
  if (turn.shared_with.length > 0) {
    badges.push({ key: "shared", text: t("turn.shared", lang) + " · " + t("turn.shared_with", lang, { n: turn.shared_with.length }) });
  }
  if (turn.startMs == null) {
    badges.push({ key: "no-start", text: t("turn.no_start", lang) });
  } else if (turn.endMs == null) {
    badges.push({ key: "no-end", text: t("turn.no_end", lang) });
  }
  if (turn.usage.usage_unknown) {
    badges.push({ key: "unknown-usage", text: t("turn.unknown_usage", lang) });
  }
  return badges;
}

/** 任务关联文案：会话可以关联零到多个任务。 */
export function tasksText(session, lang) {
  if (!session.tasks.length) return t("task.none", lang);
  return t("task.count", lang, { n: session.tasks.length });
}

/**
 * 轮次 → 时间轴的跳转上下文：与活动视图 hourContext 同一语义（半开小时窗口）。
 * 只有轮次带真实开始时间（可核对的事件证据）时才产生入口；缺开始边界不发明。
 */
export function jumpContext(turn, timezone) {
  if (!turn || turn.startMs == null) return null;
  const tz = timezone || "Asia/Shanghai";
  const start = new Date(turn.startMs);
  start.setUTCMinutes(0, 0, 0);
  const end = new Date(start.getTime() + 3600_000);
  return {
    session_id: turn.turn_id ? undefined : undefined,
    turn_id: turn.turn_id,
    timezone: tz,
    hours: 1,
    start: start.toISOString(),
    end: end.toISOString(),
    startMs: start.getTime(),
    endMs: end.getTime(),
  };
}

function emptyState(lang, keys) {
  return '<div class="empty-state"><p class="empty-state__text">' + safeText(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + safeText(t(keys[1], lang)) + "</p></div>";
}

function usageCell(usage, lang) {
  const cls = !usage.present ? "" : (usage.usage_unknown ? " sessions__usage--unknown" : " sessions__usage--known");
  return '<span class="sessions__usage' + cls + '">' + safeText(usageText(usage, lang)) + "</span>";
}

function renderRow(session, selectedId, lang) {
  const selected = session.session_id === selectedId;
  const cols = [
    session.session_id,
    session.source_ns + (session.adapter ? " · " + session.adapter : ""),
    session.opened_at || "—",
    session.turns_supported ? t("turn.count", lang, { n: session.turns.length }) : t("session.turns_unsupported", lang),
    tasksText(session, lang),
    usageCell(session.usage, lang),
  ];
  return '<tr class="sessions__row' + (selected ? " sessions__row--selected" : "") + '"' +
    ' data-session="' + safeText(session.session_id) + '"' +
    ' role="option" aria-selected="' + (selected ? "true" : "false") + '"' +
    ' tabindex="' + (selected ? "0" : "-1") + '">' +
    cols.map(c => '<td>' + safeText(c) + "</td>").join("") + "</tr>";
}

/** 轮次行：徽章 + 用量 +（仅有真实开始时间时）时间轴跳转入口。 */
export function renderTurnRow(turn, lang) {
  const badges = turnBadges(turn, lang)
    .map(b => '<span class="sessions__badge" data-badge="' + safeText(b.key) + '">' + safeText(b.text) + "</span>")
    .join("");
  const jump = jumpContext(turn);
  const jumpHtml = jump
    ? '<button type="button" class="button sessions__jump" data-jump-start="' + safeText(jump.start) +
      '" data-jump-end="' + safeText(jump.end) + '" data-jump-tz="' + safeText(jump.timezone) + '"' +
      ' aria-label="' + safeText(t("turn.jump", lang) + " " + jump.start) + '">' +
      safeText(t("turn.jump", lang)) + "</button>"
    : "";
  const span = (turn.startMs != null
    ? safeText(turn.started_at) + " – " + (turn.endMs != null ? safeText(turn.ended_at) : safeText(t("turn.no_end", lang)))
    : safeText(t("turn.no_start", lang)));
  return '<tr class="sessions__turn" data-turn="' + safeText(turn.turn_id) + '">' +
    '<td class="id">' + safeText(turn.turn_id) + "</td>" +
    "<td>" + span + "</td>" +
    '<td class="id">' + usageCell(turn.usage, lang) + "</td>" +
    "<td>" + badges + "</td>" +
    "<td>" + jumpHtml + "</td></tr>";
}

/** 会话详情：关联任务列表 + 轮次表（或不支持来源的一致降级态）。 */
export function renderSessionDetail(session, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  if (!session) {
    return '<section class="sessions__detail" data-detail="none"><p class="field-hint">' +
      safeText(t("detail.none", lang)) + "</p></section>";
  }
  const head = "<h2>" + safeText(t("session.selected", lang)) + ': <span class="id">' +
    safeText(session.session_id) + "</span></h2>";
  const meta = '<p class="field-hint">' +
    safeText(t("col.source", lang)) + "=" + safeText(session.source_ns + (session.adapter ? " · " + session.adapter : "")) +
    " · " + safeText(t("col.tasks", lang)) + "=" + safeText(tasksText(session, lang)) +
    (session.tasks.length ? " (" + safeText(session.tasks.join(", ")) + ")" : "") +
    " · " + safeText(t("col.usage", lang)) + "=" + safeText(usageText(session.usage, lang)) +
    "</p>";

  let body;
  if (!session.turns_supported) {
    body = '<div class="empty-state" data-turns="unsupported">' +
      '<p class="empty-state__text">' + safeText(t("session.turns_unsupported", lang)) + "</p>" +
      '<p class="empty-state__text">' + safeText(t("session.turns_unsupported.hint", lang)) + "</p></div>";
  } else if (!session.turns.length) {
    body = '<div class="empty-state" data-turns="empty"><p class="empty-state__text">' +
      safeText(t("usage.none", lang)) + "</p></div>";
  } else {
    body = '<div class="table-wrap"><table class="table" data-turns="list"><thead><tr>' +
      [t("turn.list", lang), t("turn.started", lang), t("col.usage", lang), "", ""]
        .map(h => "<th>" + safeText(h) + "</th>").join("") +
      "</tr></thead><tbody>" +
      session.turns.map(turn => renderTurnRow(turn, lang)).join("") +
      "</tbody></table></div>";
  }
  return '<section class="sessions__detail" data-detail="' + safeText(session.session_id) + '">' +
    head + meta + body + "</section>";
}

export function focusModel(sessions, selectedId = null) {
  const ids = sessions.map(s => s.session_id);
  let index = ids.indexOf(selectedId);
  if (index < 0) index = ids.length ? 0 : -1;
  return {
    ids,
    index,
    selectedId: index >= 0 ? ids[index] : null,
    move(delta) {
      if (!ids.length) return focusModel([], null);
      const next = Math.max(0, Math.min(ids.length - 1, (index < 0 ? 0 : index) + delta));
      return focusModel(sessions, ids[next]);
    },
  };
}

export function handleSessionsKey(key, model) {
  switch (key) {
    case "ArrowUp":
      return { focus: model.move(-1), openDetail: false, closeDetail: false };
    case "ArrowDown":
      return { focus: model.move(1), openDetail: false, closeDetail: false };
    case "Enter":
      return { focus: model, openDetail: true, closeDetail: false };
    case "Escape":
      return { focus: model, openDetail: false, closeDetail: true };
    default:
      return { focus: model, openDetail: false, closeDetail: false };
  }
}

/**
 * 渲染会话列表视图。
 * @param {object} state { status, sessions, completeness, watermark }
 * @param {object} options { lang, selectedId, showDetail }
 */
export function renderSessions(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const status = state.status || "ready";

  if (status === "loading") {
    return '<div class="sessions" data-status="loading"><p class="nodes-loading">' +
      safeText(t("sessions.loading", lang)) + "</p></div>";
  }
  if (status === "unauthenticated") {
    return '<div class="sessions" data-status="unauthenticated">' +
      emptyState(lang, ["sessions.unauthenticated", "sessions.unauthenticated.hint"]) + "</div>";
  }
  if (status === "error") {
    return '<div class="sessions" data-status="error">' +
      emptyState(lang, ["sessions.error", "sessions.error.hint"]) + "</div>";
  }
  if (status === "expired") {
    return '<div class="sessions" data-status="expired">' +
      emptyState(lang, ["sessions.expired", "sessions.expired.hint"]) + "</div>";
  }

  const sessions = normalizeSessions(state.sessions);
  const focus = focusModel(sessions, options.selectedId);
  const selected = sessions.find(s => s.session_id === focus.selectedId) || null;
  const completeness = state.completeness || "";
  const showUnknownBanner = status === "unknown" || completeness === "partial" ||
    completeness === "unknown";

  const header = '<header class="sessions__head nodes-head"><h1>' +
    safeText(t("sessions.title", lang)) + "</h1>" +
    '<p class="field-hint">' + safeText(t("sessions.subtitle", lang)) + "</p></header>";

  const toolbar = '<div class="sessions__toolbar" role="toolbar">' +
    '<span class="sessions__kbd">' + safeText(t("kbd.hint", lang)) + "</span>" +
    (completeness ? '<span class="field-hint">' + safeText(t("cursors.completeness", lang)) + "=" +
      safeText(completeness) + "</span>" : "") +
    "</div>";

  const banner = showUnknownBanner
    ? emptyState(lang, ["sessions.unknown", "sessions.unknown.hint"])
    : "";

  if (!sessions.length && status !== "unknown") {
    return '<div class="sessions" data-status="empty">' + header + toolbar +
      emptyState(lang, ["sessions.empty", "sessions.empty.hint"]) + "</div>";
  }

  const table = '<div class="table-wrap"><table class="table sessions__table">' +
    "<thead><tr>" +
    [t("col.session", lang), t("col.source", lang), t("col.opened", lang),
     t("col.turns", lang), t("col.tasks", lang), t("col.usage", lang)]
      .map(h => "<th>" + safeText(h) + "</th>").join("") +
    "</tr></thead><tbody role=\"listbox\" aria-label=\"" + safeText(t("sessions.title", lang)) + '">' +
    sessions.map(s => renderRow(s, focus.selectedId, lang)).join("") +
    "</tbody></table></div>";

  const detail = options.showDetail === false ? "" : renderSessionDetail(selected, { lang });

  return '<div class="sessions" data-status="' +
    safeText(showUnknownBanner && status === "ready" ? "unknown" : (status || "ready")) +
    '" data-selected="' + safeText(focus.selectedId || "") + '">' +
    header + toolbar + banner + table + detail + "</div>";
}

/** 挂载辅助：把键盘事件映射到选中变化（宿主绑定）。 */
export function applyKeyToState(state, options, key) {
  const sessions = normalizeSessions(state.sessions);
  const model = focusModel(sessions, options.selectedId);
  const result = handleSessionsKey(key, model);
  return {
    selectedId: result.focus.selectedId,
    showDetail: result.closeDetail ? false : (result.openDetail ? true : options.showDetail !== false),
    focus: result.focus,
  };
}
