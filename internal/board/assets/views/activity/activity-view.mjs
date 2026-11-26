// 全天活动与小时下钻视图（vnext / UI · VN-742）。
//
// 目标：以明确口径的热图帮助定位工程活动时段——执行覆盖（分钟）与
// 事件活跃度（轮次·分钟）用不同单位和文案分开画；缺测小时标「缺测」，
// 绝不涂成零活动；点击／回车进入与时间轴同一小时上下文。
//
// 依据：
//   docs/contracts/vnext/observability/timeline-metering-contract-v1.md §3、§5
//   docs/contracts/vnext/ui/WORKBENCH_UI_CONTRACT.md（token／空态／双语）
//   docs/contracts/vnext/ui-data/view-context-contract-v1.md（日期与语言上下文）
//   数据来自 VN-805 的统计报表（internal/analytics），本视图不重新聚合来源记录。
//
// 非目标：不改 shell／公共时间格式；不在页面推算缺失执行时长。

export const VIEW_STATUSES = Object.freeze([
  "ready", "loading", "empty", "error", "expired", "unauthenticated", "unknown",
]);

// 两个热图指标：字段、换算与单位文案全部在这里声明，渲染不得另算。
export const METRICS = Object.freeze([
  {
    id: "coverage",
    field: "engineering_seconds",
    perSecond: 1 / 60,
    unit: "min",
    // 执行覆盖：task_phase ∪ cli_process 按身份并集，单位分钟。
  },
  {
    id: "activity",
    field: "turn_seconds",
    perSecond: 1 / 60,
    unit: "turn·min",
    // 事件活跃度：模型轮次时长，单位轮次·分钟——与执行覆盖不同单位。
  },
]);

const I18N = {
  zh: {
    "activity.title": "全天活动",
    "activity.subtitle": "执行覆盖与事件活跃度分开画；缺测小时标斜线，不涂成零活动。",
    "activity.loading": "正在读取全天活动…",
    "activity.empty": "这一天没有观测到活动。",
    "activity.empty.hint": "换一天或扩大工程筛选范围再试。",
    "activity.error": "全天活动加载失败。",
    "activity.error.hint": "确认本机中枢在运行，然后重试。",
    "activity.expired": "全天活动数据已过期。",
    "activity.expired.hint": "刷新后重新读取，不沿用上次热图。",
    "activity.unauthenticated": "未登录，无法查看全天活动。",
    "activity.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "activity.unknown": "部分小时缺测。",
    "activity.unknown.hint": "投影水位落后或完整性未知；斜线格是缺测，不是零活动。",
    "no.project": "还没有选择工程。",
    "no.project.hint": "全天活动按工程物化：先在顶部选择工程，再看这一天的热图。",
    "metric.coverage": "执行覆盖",
    "metric.activity": "事件活跃度",
    "unit.min": "分钟",
    "unit.turn·min": "轮次·分钟",
    "metric.coverage.hint": "task_phase ∪ cli_process 按身份并集（分钟）",
    "metric.activity.hint": "模型轮次时长（轮次·分钟），与执行覆盖不同单位",
    "cell.missing": "缺测",
    "cell.zero": "无活动",
    "hour.label": "时",
    "hour.selected": "已选小时",
    "hour.context": "小时上下文",
    "hour.context.hint": "时间轴将打开同一小时窗口（半开区间）。",
    "hour.avg_concurrency": "平均并发身份",
    "hour.task_phase": "阶段驻留",
    "hour.cli_process": "CLI 运行",
    "hour.explicit_wait": "明确等待",
    "detail.none": "未选择小时；用 Tab 或方向键在热图上移动。",
    "project.filter": "工程筛选",
    "project.all": "全部工程",
    "cursors.completeness": "完整性",
    "kbd.hint": "键盘：方向键移动，Home／End 到头尾，Enter 选中小时",
    "quality.observed": "实测",
    "quality.estimated": "估算",
    "quality.unknown": "未知",
  },
  en: {
    "activity.title": "Day activity",
    "activity.subtitle": "Execution coverage and event activity stay separate; missing hours are hatched, never painted as zero.",
    "activity.loading": "Loading day activity…",
    "activity.empty": "No observed activity on this day.",
    "activity.empty.hint": "Try another day or widen the project filter.",
    "activity.error": "Failed to load day activity.",
    "activity.error.hint": "Confirm the local hub is running, then retry.",
    "activity.expired": "Day activity data expired.",
    "activity.expired.hint": "Refresh and reload; do not reuse the previous heatmap.",
    "activity.unauthenticated": "Sign in to view day activity.",
    "activity.unauthenticated.hint": "Use a local session or token, then retry.",
    "activity.unknown": "Some hours are unmeasured.",
    "activity.unknown.hint": "Projection lags or completeness is unknown; hatched cells are missing, not zero.",
    "no.project": "No project selected.",
    "no.project.hint": "Day activity is materialised per project: pick one at the top first.",
    "metric.coverage": "Execution coverage",
    "metric.activity": "Event activity",
    "unit.min": "minutes",
    "unit.turn·min": "turn·minutes",
    "metric.coverage.hint": "task_phase ∪ cli_process union by identity (minutes)",
    "metric.activity.hint": "Model turn duration (turn·minutes); a different unit from coverage",
    "cell.missing": "missing",
    "cell.zero": "no activity",
    "hour.label": ":00",
    "hour.selected": "Selected hour",
    "hour.context": "Hour context",
    "hour.context.hint": "The timeline opens the same half-open hour window.",
    "hour.avg_concurrency": "Avg concurrent identities",
    "hour.task_phase": "Phase dwell",
    "hour.cli_process": "CLI process",
    "hour.explicit_wait": "Explicit wait",
    "detail.none": "No hour selected; use Tab or arrow keys on the heatmap.",
    "project.filter": "Project filter",
    "project.all": "All projects",
    "cursors.completeness": "Completeness",
    "kbd.hint": "Keys: arrows move, Home／End jump, Enter selects the hour",
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

/** 某时区下某时刻的偏移量（毫秒）——用于把本地日换算成 UTC 半开窗口。 */
export function tzOffsetMs(date, timezone) {
  const dtf = new Intl.DateTimeFormat("en-US", {
    timeZone: timezone, hour12: false,
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit",
  });
  const parts = {};
  for (const p of dtf.formatToParts(date)) {
    if (p.type !== "literal") parts[p.type] = Number(p.value);
  }
  const asUTC = Date.UTC(parts.year, parts.month - 1, parts.day,
    parts.hour % 24, parts.minute, parts.second);
  return asUTC - date.getTime();
}

/** 本地日的 UTC 半开窗口 [本地零点, 次日零点)；跨时区与夏令时各收敛一次。
 *  day 非法时退回今天，渲染层仍然只输出转义后的文本。 */
export function dayWindow(day, timezone) {
  const guess = new Date(day + "T00:00:00Z");
  if (Number.isNaN(guess.getTime())) {
    return dayWindow(new Date().toISOString().slice(0, 10), timezone);
  }
  let start = new Date(guess.getTime() - tzOffsetMs(guess, timezone));
  const refined = new Date(start.getTime() - (tzOffsetMs(start, timezone) - tzOffsetMs(guess, timezone)));
  start = refined;
  return {
    startMs: start.getTime(),
    endMs: start.getTime() + 24 * 3600_000,
    start: start.toISOString(),
    end: new Date(start.getTime() + 24 * 3600_000).toISOString(),
    timezone,
    hours: 1,
    day,
  };
}

/** 小时下钻：与时间轴 createDateContext 同一上下文（同半开窗口、同时区）。 */
export function hourContext(day, hour, timezone) {
  const base = dayWindow(day, timezone);
  const startMs = base.startMs + hour * 3600_000;
  const start = new Date(startMs);
  return {
    day,
    hour,
    timezone,
    hours: 1,
    startMs,
    endMs: startMs + 3600_000,
    start: start.toISOString(),
    end: new Date(startMs + 3600_000).toISOString(),
  };
}

/**
 * 规范化服务端小时桶：bucket.hour 为本地小时起点（UTC ISO）。
 * 不重新聚合：缺桶即缺数据；是否算「真实零活动」由 completeness 决定。
 */
export function normalizeBuckets(raw = [], day, timezone) {
  const win = dayWindow(day, timezone);
  const cells = [];
  for (let h = 0; h < 24; h++) {
    const startMs = win.startMs + h * 3600_000;
    cells.push({
      hour: h,
      startMs,
      endMs: startMs + 3600_000,
      start: new Date(startMs).toISOString(),
      end: new Date(startMs + 3600_000).toISOString(),
      present: false,
      engineering_seconds: 0,
      turn_seconds: 0,
      task_phase_seconds: 0,
      cli_process_seconds: 0,
      explicit_wait_seconds: 0,
      avg_concurrency: 0,
    });
  }
  for (const bucket of Array.isArray(raw) ? raw : []) {
    const ms = bucket.hour ? Date.parse(bucket.hour) : NaN;
    if (!Number.isFinite(ms)) continue;
    const idx = Math.round((ms - win.startMs) / 3600_000);
    if (idx < 0 || idx > 23) continue; // 邻日的桶不属于这一天
    const cell = cells[idx];
    cell.present = true;
    for (const f of ["engineering_seconds", "turn_seconds", "task_phase_seconds",
      "cli_process_seconds", "explicit_wait_seconds"]) {
      const v = Number(bucket[f]);
      if (Number.isFinite(v) && v >= 0) cell[f] = v;
    }
    const c = Number(bucket.avg_concurrency);
    if (Number.isFinite(c) && c >= 0) cell.avg_concurrency = c;
  }
  return { win, cells };
}

/** 缺测判定：桶不存在且完整性不是 complete——是缺数据，不是零活动。 */
export function cellState(cell, completeness) {
  if (cell.present) return "observed";
  return completeness === "complete" ? "zero" : "missing";
}

export function metricMinutes(cell, metric) {
  const m = METRICS.find(x => x === metric || x.id === metric) || METRICS[0];
  const v = Number(cell[m.field]) || 0;
  return Math.max(0, v * m.perSecond);
}

/** 相对全天最大值的 0–4 档；0 值与缺测都不进色阶。 */
export function levelOf(minutes, max) {
  if (!(max > 0) || !(minutes > 0)) return 0;
  return Math.min(4, Math.max(1, Math.ceil((minutes / max) * 4)));
}

export function formatMinutes(min, lang) {
  const v = Math.round(min * 10) / 10;
  const unit = t("unit.min", lang);
  return v + " " + unit;
}

/** 按指标自身的单位格式化（执行覆盖=分钟，事件活跃度=轮次·分钟）。 */
export function formatMetric(cell, metric, lang) {
  const v = Math.round(metricMinutes(cell, metric) * 10) / 10;
  return v + " " + t("unit." + metric.unit, lang);
}

function cellLabel(cell, state, lang) {
  const st = cellState(cell, state.completeness);
  if (st === "missing") return t("cell.missing", lang);
  if (st === "zero") return t("cell.zero", lang);
  const sep = lang === "en" ? ", " : "，";
  return METRICS.map(m => t("metric." + m.id, lang) + " " + formatMetric(cell, m, lang)).join(sep);
}

function renderCell(cell, metric, state, lang, selectedHour, maxMinutes) {
  const st = cellState(cell, state.completeness);
  const selected = cell.hour === selectedHour;
  const level = st === "observed" ? levelOf(metricMinutes(cell, metric), maxMinutes) : 0;
  const classes = ["activity__cell"];
  if (st === "missing") classes.push("activity__cell--missing");
  else classes.push("activity__cell--l" + level);
  if (selected) classes.push("activity__cell--selected");
  const value = st === "observed"
    ? String(Math.round(metricMinutes(cell, metric)))
    : t("cell." + (st === "missing" ? "missing" : "zero"), lang);
  return '<button type="button" class="' + classes.join(" ") + '"' +
    ' data-hour="' + cell.hour + '"' +
    ' data-state="' + st + '"' +
    ' data-level="' + (st === "observed" ? level : "none") + '"' +
    ' aria-label="' + safeText(cell.hour + t("hour.label", lang) + " " + cellLabel(cell, state, lang)) + '"' +
    ' aria-selected="' + (selected ? "true" : "false") + '"' +
    ' tabindex="' + (selected ? "0" : "-1") + '">' +
    '<span class="activity__cell-hour" aria-hidden="true">' + cell.hour + "</span>" +
    '<span class="activity__cell-value" aria-hidden="true">' + safeText(value) + "</span>" +
    "</button>";
}

function heatGrid(state, lang, selectedHour, metric) {
  const { cells } = normalizeBuckets(state.hours || [], state.day, state.timezone);
  const maxMinutes = Math.max(...cells.map(c => metricMinutes(c, metric)), 0);
  const cellsHtml = cells.map(c => renderCell(c, metric, state, lang, selectedHour, maxMinutes)).join("");
  return '<div class="activity__grid" role="listbox" aria-label="' +
    safeText(t("metric." + metric.id, lang)) + '">' + cellsHtml + "</div>";
}

/** 两个指标各一张图，单位与口径写在图题里（F41）。 */
function metricCharts(state, lang, selectedHour) {
  return METRICS.map(m => {
    return '<section class="activity__metric" data-metric="' + m.id + '">' +
      "<h2>" + safeText(t("metric." + m.id, lang)) +
      ' <span class="activity__unit">(' + safeText(t("unit." + m.unit, lang)) + ")</span></h2>" +
      '<p class="field-hint">' + safeText(t("metric." + m.id + ".hint", lang)) + "</p>" +
      heatGrid(state, lang, selectedHour, m) +
      "</section>";
  }).join("");
}

function emptyState(lang, keys) {
  return '<div class="empty-state"><p class="empty-state__text">' + safeText(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + safeText(t(keys[1], lang)) + "</p></div>";
}

/** 选中小时的下钻面板：单位分开报，并给出与时间轴同一的小时上下文。 */
export function renderHourDetail(cell, state, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  if (!cell) {
    return '<section class="activity__detail" data-detail="none"><p class="field-hint">' +
      safeText(t("detail.none", lang)) + "</p></section>";
  }
  const ctx = hourContext(state.day, cell.hour, state.timezone);
  const st = cellState(cell, state.completeness);
  const rows = [
    ["hour.context", ctx.start + " – " + ctx.end + " (" + ctx.timezone + ")"],
    ["metric.coverage", st === "observed" ? formatMetric(cell, METRICS[0], lang) : t("cell." + st, lang)],
    ["metric.activity", st === "observed" ? formatMetric(cell, METRICS[1], lang) : t("cell." + st, lang)],
    ["hour.avg_concurrency", st === "observed" ? String(cell.avg_concurrency) : t("cell." + st, lang)],
    ["hour.task_phase", st === "observed" ? formatMinutes(cell.task_phase_seconds / 60, lang) : "—"],
    ["hour.cli_process", st === "observed" ? formatMinutes(cell.cli_process_seconds / 60, lang) : "—"],
    ["hour.explicit_wait", st === "observed" ? formatMinutes(cell.explicit_wait_seconds / 60, lang) : "—"],
  ];
  return '<section class="activity__detail" data-detail="' + cell.hour + '">' +
    "<h2>" + safeText(t("hour.selected", lang)) + ": " + cell.hour + t("hour.label", lang) + "</h2>" +
    '<div class="table-wrap"><table class="table"><tbody>' +
    rows.map(([k, v]) => "<tr><th>" + safeText(t(k, lang)) + "</th><td class=\"id\">" +
      safeText(v) + "</td></tr>").join("") +
    "</tbody></table></div>" +
    '<p class="field-hint" data-hour-start="' + safeText(ctx.start) + '" data-hour-end="' + safeText(ctx.end) + '">' +
    safeText(t("hour.context.hint", lang)) + "</p></section>";
}

function projectFilter(state, lang) {
  const projects = Array.isArray(state.projects) ? state.projects : [];
  if (!projects.length) return "";
  const options = projects.map(p =>
    '<option value="' + safeText(p.id) + '"' +
    (p.id === state.project_id ? " selected" : "") + ">" +
    safeText(p.label || p.id) + "</option>").join("");
  return '<label class="activity__filter">' + safeText(t("project.filter", lang)) +
    ' <select data-action="project">' + options + "</select></label>";
}

/** 键盘导航：方向键在 24 格上移动，Home／End 到头尾。 */
export function focusModel(selectedHour = null) {
  const at = (h) => (h >= 0 && h <= 23) ? h : null;
  return {
    hour: at(selectedHour),
    move(delta) {
      const cur = this.hour == null ? (delta > 0 ? -1 : 24) : this.hour;
      return focusModel(at(cur + delta));
    },
    home() { return focusModel(0); },
    end() { return focusModel(23); },
  };
}

export function handleActivityKey(key, selectedHour) {
  const model = focusModel(selectedHour);
  switch (key) {
    case "ArrowRight": return model.move(1).hour;
    case "ArrowLeft": return model.move(-1).hour;
    case "ArrowDown": return model.move(1).hour;
    case "ArrowUp": return model.move(-1).hour;
    case "Home": return model.home().hour;
    case "End": return model.end().hour;
    default: return selectedHour;
  }
}

/**
 * 渲染全天活动视图。
 * @param {object} state { status, day, timezone, hours, completeness, watermark, projects, project_id }
 * @param {object} options { lang, selectedHour }
 */
export function renderActivity(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const status = state.status || "ready";
  const timezone = state.timezone || "Asia/Shanghai";

  if (status === "loading") {
    return '<div class="activity" data-status="loading"><p class="nodes-loading">' +
      safeText(t("activity.loading", lang)) + "</p></div>";
  }
  if (status === "unauthenticated") {
    return '<div class="activity" data-status="unauthenticated">' +
      emptyState(lang, ["activity.unauthenticated", "activity.unauthenticated.hint"]) + "</div>";
  }
  if (status === "error") {
    return '<div class="activity" data-status="error">' +
      emptyState(lang, ["activity.error", "activity.error.hint"]) + "</div>";
  }
  if (status === "expired") {
    return '<div class="activity" data-status="expired">' +
      emptyState(lang, ["activity.expired", "activity.expired.hint"]) + "</div>";
  }
  // 未选工程不是「这一天没活动」：热图按工程物化，必须先选工程。
  if (status === "no_project") {
    return '<div class="activity" data-status="no_project">' +
      emptyState(lang, ["no.project", "no.project.hint"]) + "</div>";
  }

  const day = state.day || new Date().toISOString().slice(0, 10);
  state = { ...state, day, timezone };
  const { win, cells } = normalizeBuckets(state.hours || [], day, timezone);
  const completeness = state.completeness || "";
  const showMissingBanner = status === "unknown" || completeness === "partial" ||
    completeness === "unknown";
  const selectedHour = options.selectedHour == null ? null : options.selectedHour;
  const selected = selectedHour == null ? null : cells[selectedHour];

  const toolbar = '<div class="activity__toolbar" role="toolbar">' +
    '<span class="field-hint">' + safeText(day) + " · " + safeText(timezone) + "</span>" +
    projectFilter(state, lang) +
    '<span class="activity__kbd">' + safeText(t("kbd.hint", lang)) + "</span>" +
    "</div>";

  const banner = showMissingBanner
    ? '<p class="activity__missing-banner">' + safeText(t("activity.unknown", lang)) +
      " " + safeText(t("activity.unknown.hint", lang)) + "</p>"
    : "";

  const hasAny = cells.some(c => cellState(c, completeness) === "observed");
  const allZeroOrMissing = !hasAny;

  if (allZeroOrMissing && !showMissingBanner) {
    return '<div class="activity" data-status="empty">' + toolbar +
      metricCharts(state, lang, selectedHour) +
      emptyState(lang, ["activity.empty", "activity.empty.hint"]) + "</div>";
  }

  const detail = renderHourDetail(selected, { ...state, day, timezone }, { lang });

  return '<div class="activity" data-status="' +
    safeText(showMissingBanner && status === "ready" ? "unknown" : (status || "ready")) +
    '" data-day="' + safeText(day) + '" data-tz="' + safeText(timezone) + '"' +
    ' data-selected-hour="' + (selectedHour == null ? "" : selectedHour) + '">' +
    toolbar + banner +
    metricCharts(state, lang, selectedHour) +
    detail + "</div>";
}

/** 挂载辅助：把键盘事件映射到选中小时变化（宿主绑定）。 */
export function applyKeyToState(selectedHour, key) {
  return handleActivityKey(key, selectedHour);
}
