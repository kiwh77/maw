// 分层执行时间轴视图（vnext / UI · VN-741）。
//
// 目标：按工程与运行展示任务阶段驻留、CLI 运行、可用轮次与明确等待；
// 未知／未闭合区间不补满；小时窗口可缩放；键盘聚焦与详情下钻。
//
// 依据：
//   docs/contracts/vnext/observability/timeline-metering-contract-v1.md §1–§3
//   docs/ARCHITECTURE_VNEXT.zh-CN.md §11.2
//   docs/contracts/vnext/ui/WORKBENCH_UI_CONTRACT.md（token／空态／双语）
//   docs/contracts/vnext/ui-data/view-context-contract-v1.md（日期与语言上下文）
//
// 非目标：不改路由／全局颜色；不在页面推算缺失执行时长。

export const INTERVAL_KINDS = Object.freeze([
  "task_phase",
  "cli_process",
  "turn",
  "explicit_wait",
]);

export const VIEW_STATUSES = Object.freeze([
  "ready", "loading", "empty", "error", "expired", "unauthenticated", "unknown",
]);

export const ZOOM_HOURS = Object.freeze([1, 3, 6, 12, 24]);

const I18N = {
  zh: {
    "timeline.title": "执行时间轴",
    "timeline.subtitle": "阶段驻留、CLI 运行、轮次与明确等待分开看；未知区间不补满。",
    "timeline.loading": "正在读取时间轴…",
    "timeline.empty": "这个小时窗口里没有可显示的区间。",
    "timeline.empty.hint": "换个小时或缩小缩放再试。",
    "timeline.error": "时间轴加载失败。",
    "timeline.error.hint": "确认本机中枢在运行，然后重试。",
    "timeline.expired": "时间轴数据已过期。",
    "timeline.expired.hint": "刷新后重新读取，不沿用上次区间。",
    "timeline.unauthenticated": "未登录，无法查看时间轴。",
    "timeline.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "timeline.unknown": "区间数据不完整。",
    "timeline.unknown.hint": "投影水位落后或完整性未知；已观测部分仍可查看。",
    "no.project": "还没有选择工程。",
    "no.project.hint": "时间轴按工程物化：先在顶部选择工程，再查看区间。",
    "kind.task_phase": "阶段驻留",
    "kind.cli_process": "CLI 运行",
    "kind.turn": "模型轮次",
    "kind.explicit_wait": "明确等待",
    "kind.unknown": "未知／未闭合",
    "toolbar.window": "小时窗口",
    "toolbar.zoom": "缩放",
    "toolbar.zoom_out": "缩小",
    "toolbar.zoom_in": "放大",
    "toolbar.prev": "上一窗",
    "toolbar.next": "下一窗",
    "toolbar.kbd": "键盘：← → 切换条，Enter 打开详情，Esc 关闭",
    "detail.title": "区间详情",
    "detail.kind": "类别",
    "detail.phase": "阶段",
    "detail.reason": "原因",
    "detail.start": "开始",
    "detail.end": "结束",
    "detail.open": "未结束（不计入完整时长）",
    "detail.quality": "质量",
    "detail.run": "运行",
    "detail.instance": "实例",
    "detail.none": "未选择区间。",
    "cursors.state": "状态水位",
    "cursors.projection": "投影水位",
    "cursors.completeness": "完整性",
    "quality.observed": "实测",
    "quality.estimated": "估算",
    "quality.unknown": "未知",
  },
  en: {
    "timeline.title": "Execution timeline",
    "timeline.subtitle": "Phase dwell, CLI runtime, turns, and explicit waits stay separate; unknown spans are not filled.",
    "timeline.loading": "Loading timeline…",
    "timeline.empty": "No intervals in this hour window.",
    "timeline.empty.hint": "Try another hour or zoom out.",
    "timeline.error": "Failed to load the timeline.",
    "timeline.error.hint": "Confirm the local hub is running, then retry.",
    "timeline.expired": "Timeline data expired.",
    "timeline.expired.hint": "Refresh and reload; do not reuse the previous spans.",
    "timeline.unauthenticated": "Sign in to view the timeline.",
    "timeline.unauthenticated.hint": "Use a local session or token, then retry.",
    "timeline.unknown": "Interval data is incomplete.",
    "timeline.unknown.hint": "Projection lags or completeness is unknown; observed parts remain visible.",
    "no.project": "No project selected.",
    "no.project.hint": "The timeline is materialised per project: pick one at the top first.",
    "kind.task_phase": "Phase dwell",
    "kind.cli_process": "CLI process",
    "kind.turn": "Model turn",
    "kind.explicit_wait": "Explicit wait",
    "kind.unknown": "Unknown / open",
    "toolbar.window": "Hour window",
    "toolbar.zoom": "Zoom",
    "toolbar.zoom_out": "Zoom out",
    "toolbar.zoom_in": "Zoom in",
    "toolbar.prev": "Previous",
    "toolbar.next": "Next",
    "toolbar.kbd": "Keys: ← → move bar, Enter open detail, Esc close",
    "detail.title": "Interval detail",
    "detail.kind": "Kind",
    "detail.phase": "Phase",
    "detail.reason": "Reason",
    "detail.start": "Start",
    "detail.end": "End",
    "detail.open": "Still open (not counted as full duration)",
    "detail.quality": "Quality",
    "detail.run": "Run",
    "detail.instance": "Instance",
    "detail.none": "No interval selected.",
    "cursors.state": "State cursor",
    "cursors.projection": "Projection cursor",
    "cursors.completeness": "Completeness",
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

/** 统一日期／时区上下文（只读）；视图不自行读 localStorage。 */
export function createDateContext(options = {}) {
  const timezone = options.timezone || "Asia/Shanghai";
  let hours = Number(options.hours);
  if (!ZOOM_HOURS.includes(hours)) hours = 6;
  let start = options.start ? new Date(options.start) : null;
  if (!start || Number.isNaN(start.getTime())) {
    start = new Date();
    start.setUTCMinutes(0, 0, 0);
    start = new Date(start.getTime() - (hours - 1) * 3600_000);
  }
  const end = new Date(start.getTime() + hours * 3600_000);
  return Object.freeze({
    timezone,
    hours,
    start: start.toISOString(),
    end: end.toISOString(),
    startMs: start.getTime(),
    endMs: end.getTime(),
  });
}

export function shiftWindow(dateCtx, deltaWindows = 1) {
  const ms = dateCtx.hours * 3600_000 * deltaWindows;
  return createDateContext({
    timezone: dateCtx.timezone,
    hours: dateCtx.hours,
    start: new Date(dateCtx.startMs + ms).toISOString(),
  });
}

export function zoomWindow(dateCtx, direction) {
  const idx = ZOOM_HOURS.indexOf(dateCtx.hours);
  const next = direction < 0
    ? ZOOM_HOURS[Math.max(0, idx - 1)]
    : ZOOM_HOURS[Math.min(ZOOM_HOURS.length - 1, idx + 1)];
  const mid = (dateCtx.startMs + dateCtx.endMs) / 2;
  const start = new Date(mid - (next * 3600_000) / 2);
  return createDateContext({ timezone: dateCtx.timezone, hours: next, start: start.toISOString() });
}

export function isKnownKind(kind) {
  return INTERVAL_KINDS.includes(kind);
}

/**
 * 规范化服务端区间：未知质量／缺边界不补满；非法类别降为 unknown 展示类。
 * 不推算缺失时长。
 */
export function normalizeIntervals(raw = []) {
  const list = Array.isArray(raw) ? raw : [];
  return list.map((iv, i) => {
    const kind = isKnownKind(iv.kind) ? iv.kind : null;
    const quality = ["observed", "estimated", "unknown"].includes(iv.quality) ? iv.quality : "unknown";
    const open = iv.open === true || iv.end_at == null || iv.end_at === "";
    const start = iv.start_at ? Date.parse(iv.start_at) : NaN;
    const end = open ? NaN : Date.parse(iv.end_at);
    return {
      interval_id: String(iv.interval_id || ("iv_" + i)),
      kind: kind || "unknown",
      instance_id: iv.instance_id || "",
      run_id: iv.run_id || "",
      generation: iv.generation ?? null,
      phase: iv.phase || "",
      reason: iv.reason || "",
      start_at: iv.start_at || "",
      end_at: open ? null : iv.end_at,
      startMs: Number.isFinite(start) ? start : null,
      endMs: Number.isFinite(end) ? end : null,
      quality,
      open,
      late_arrival: iv.late_arrival === true,
      evidence_ref: iv.evidence_ref || "",
      // 未知或未闭合：渲染用阴影条，绝不把缺口涂满
      fill: quality === "unknown" || open || !kind ? "hatch" : "solid",
    };
  }).filter(iv => iv.startMs != null);
}

/** 半开窗口裁剪；窗口前 carry-in；不发明 end。 */
export function clipToWindow(iv, dateCtx) {
  const w0 = dateCtx.startMs;
  const w1 = dateCtx.endMs;
  if (iv.startMs >= w1) return null;
  const endBound = iv.endMs != null ? iv.endMs : w1;
  if (endBound <= w0) return null;
  const left = Math.max(iv.startMs, w0);
  const right = Math.min(endBound, w1);
  if (right <= left) return null;
  return {
    ...iv,
    clipStartMs: left,
    clipEndMs: right,
    // 原区间无闭合时，裁切后仍标 open，时长不可信
    open: iv.open || iv.endMs == null,
  };
}

export function layoutBars(intervals, dateCtx) {
  const span = Math.max(1, dateCtx.endMs - dateCtx.startMs);
  const bars = [];
  for (const iv of intervals) {
    const clipped = clipToWindow(iv, dateCtx);
    if (!clipped) continue;
    const left = ((clipped.clipStartMs - dateCtx.startMs) / span) * 100;
    const width = ((clipped.clipEndMs - clipped.clipStartMs) / span) * 100;
    bars.push({
      ...clipped,
      leftPct: Math.max(0, Math.min(100, left)),
      widthPct: Math.max(0.15, Math.min(100 - left, width)),
    });
  }
  return bars;
}

/** 按 run（缺省 instance）分轨，便于并发对照。 */
export function groupLanes(bars) {
  const map = new Map();
  for (const bar of bars) {
    const key = bar.run_id || bar.instance_id || "ungrouped";
    if (!map.has(key)) map.set(key, []);
    map.get(key).push(bar);
  }
  return [...map.entries()].map(([id, items]) => ({ id, bars: items }));
}

export function focusModel(bars, selectedId = null) {
  const ids = bars.map(b => b.interval_id);
  let index = ids.indexOf(selectedId);
  if (index < 0) index = ids.length ? 0 : -1;
  return {
    ids,
    index,
    selectedId: index >= 0 ? ids[index] : null,
    move(delta) {
      if (!ids.length) return focusModel([], null);
      const next = Math.max(0, Math.min(ids.length - 1, (index < 0 ? 0 : index) + delta));
      return focusModel(bars, ids[next]);
    },
  };
}

/**
 * 键盘语义。
 *
 * ← / →：**平移窗口**（等价工具栏的「上一窗」「下一窗」），与 E2E-107 同一条路径。
 * ↑ / ↓：在区间之间移动选中（与会话视图一致），不改变窗口。
 * Enter 打开详情，Esc 关闭详情。
 *
 * 窗口动作只给方向与语义，具体怎么取数由宿主决定；视图不自己发请求。
 */
export function handleTimelineKey(key, model) {
  switch (key) {
    case "ArrowLeft":
      return { focus: model, window: "prev", openDetail: false, closeDetail: false };
    case "ArrowRight":
      return { focus: model, window: "next", openDetail: false, closeDetail: false };
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
 * 键盘接线：← / → 直接点工具栏对应按钮。
 *
 * 为什么点按钮而不是自己改状态：按钮与键盘必须走**同一条**宿主路径，
 * 才算「等价」——同一条路径才会带同一份取数参数（start / end）。
 * 视图自己维护第二套窗口状态必然与宿主分叉。
 */
export function installTimelineKeyboard(doc = (typeof document !== "undefined" ? document : null)) {
  if (!doc || typeof doc.addEventListener !== "function") return null;
  const onKey = (event) => {
    if (!event || event.defaultPrevented) return;
    const key = event.key;
    if (key !== "ArrowLeft" && key !== "ArrowRight") return;
    const target = event.target;
    if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" ||
        target.tagName === "SELECT" || target.isContentEditable)) return;
    const host = doc.querySelector("#vnext-view");
    const scope = host ? host.querySelector(".timeline") : null;
    if (!scope) return;
    const action = key === "ArrowLeft" ? "prev" : "next";
    const button = scope.querySelector('[data-action="' + action + '"]');
    if (!button) return;
    event.preventDefault();
    button.click();
  };
  doc.addEventListener("keydown", onKey, true);
  return () => doc.removeEventListener("keydown", onKey, true);
}

function emptyState(lang, keys) {
  return '<div class="empty-state"><p class="empty-state__text">' + safeText(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + safeText(t(keys[1], lang)) + "</p></div>";
}

function legend(lang) {
  const items = [...INTERVAL_KINDS, "unknown"].map(kind =>
    '<li class="timeline__legend-item">' +
    '<span class="timeline__swatch timeline__swatch--' + kind + '" aria-hidden="true"></span>' +
    safeText(t("kind." + kind, lang)) + "</li>"
  ).join("");
  return '<ul class="timeline__legend" aria-label="legend">' + items + "</ul>";
}

function formatAxis(iso, timezone) {
  try {
    return new Intl.DateTimeFormat("en-GB", {
      timeZone: timezone, hour: "2-digit", minute: "2-digit", hour12: false,
    }).format(new Date(iso));
  } catch {
    return iso.slice(11, 16);  }
}

/**
 * 窗口标签用的时间戳：带月日与时刻。
 *
 * 24 小时窗口若只印时分，首尾会显示成同一个时刻（例如 16:18 – 16:18），
 * 读者会以为窗口是 0 长度；日期是区分跨天窗口的必要信息。
 *
 * 格式固定为「日/月 时:分」（en-GB）：渲染层回归（no-project.test.cjs）与
 * 浏览器验收都按这个形状解析，改格式要同步改两处断言。
 */
function formatStamp(iso, timezone) {
  try {
    return new Intl.DateTimeFormat("en-GB", {
      timeZone: timezone, month: "2-digit", day: "2-digit",
      hour: "2-digit", minute: "2-digit", hour12: false,
    }).format(new Date(iso));
  } catch {
    return iso.slice(5, 16).replace("T", " ");
  }
}

function renderBar(bar, selectedId, lang) {
  const kindClass = bar.fill === "hatch" ? "unknown" : bar.kind;
  const openClass = bar.open || bar.fill === "hatch" ? " timeline__bar--open" : "";
  const selected = bar.interval_id === selectedId ? ' aria-selected="true"' : "";
  const label = t("kind." + (isKnownKind(bar.kind) ? bar.kind : "unknown"), lang);
  return '<button type="button" class="timeline__bar timeline__bar--' + kindClass + openClass + '"' +
    ' data-interval-id="' + safeText(bar.interval_id) + '"' +
    ' data-kind="' + safeText(bar.kind) + '"' +
    ' style="left:' + bar.leftPct.toFixed(3) + "%;width:" + bar.widthPct.toFixed(3) + '%"' +
    ' tabindex="' + (bar.interval_id === selectedId ? "0" : "-1") + '"' +
    selected +
    ' aria-label="' + safeText(label + " " + bar.interval_id) + '">' +
    "</button>";
}

export function renderDetail(interval, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  if (!interval) {
    return '<section class="timeline__detail" data-detail="none"><p class="field-hint">' +
      safeText(t("detail.none", lang)) + "</p></section>";
  }
  const rows = [
    ["detail.kind", t("kind." + (isKnownKind(interval.kind) ? interval.kind : "unknown"), lang)],
    ["detail.quality", t("quality." + (interval.quality || "unknown"), lang)],
    ["detail.start", interval.start_at || "—"],
    ["detail.end", interval.open ? t("detail.open", lang) : (interval.end_at || "—")],
    ["detail.run", interval.run_id || "—"],
    ["detail.instance", interval.instance_id || "—"],
  ];
  if (interval.phase) rows.push(["detail.phase", interval.phase]);
  if (interval.reason) rows.push(["detail.reason", interval.reason]);
  return '<section class="timeline__detail" data-detail="' + safeText(interval.interval_id) + '">' +
    "<h2>" + safeText(t("detail.title", lang)) + "</h2>" +
    '<div class="table-wrap"><table class="table"><tbody>' +
    rows.map(([k, v]) => "<tr><th>" + safeText(t(k, lang)) + "</th><td class=\"id\">" +
      safeText(v) + "</td></tr>").join("") +
    "</tbody></table></div></section>";
}

/**
 * 渲染时间轴主视图。
 * @param {object} state
 * @param {string} [state.status]
 * @param {array}  [state.intervals]
 * @param {object} [state.cursors] { state_cursor, projection_cursor, completeness }
 * @param {object} options { lang, dateContext, selectedId, showDetail }
 */
export function renderTimeline(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const status = state.status || "ready";

  if (status === "loading") {
    return '<div class="timeline" data-status="loading"><p class="nodes-loading">' +
      safeText(t("timeline.loading", lang)) + "</p></div>";
  }
  if (status === "unauthenticated") {
    return '<div class="timeline" data-status="unauthenticated">' +
      emptyState(lang, ["timeline.unauthenticated", "timeline.unauthenticated.hint"]) + "</div>";
  }
  if (status === "error") {
    return '<div class="timeline" data-status="error">' +
      emptyState(lang, ["timeline.error", "timeline.error.hint"]) + "</div>";
  }
  if (status === "expired") {
    return '<div class="timeline" data-status="expired">' +
      emptyState(lang, ["timeline.expired", "timeline.expired.hint"]) + "</div>";
  }
  // 没选工程不是「这段时间没干活」：必须与 empty 分开说，否则会误导读者。
  if (status === "no_project") {
    return '<div class="timeline" data-status="no_project">' +
      emptyState(lang, ["no.project", "no.project.hint"]) + "</div>";
  }

  const dateCtx = options.dateContext || createDateContext(state.window || {});
  const normalized = normalizeIntervals(state.intervals);
  const bars = layoutBars(normalized, dateCtx);
  const lanes = groupLanes(bars);
  const focus = focusModel(bars, options.selectedId);
  const selected = normalized.find(iv => iv.interval_id === focus.selectedId) || null;
  const completeness = state.cursors?.completeness || state.completeness || "";
  const showUnknownBanner = status === "unknown" || completeness === "partial" ||
    completeness === "unknown";

  const toolbar = '<div class="timeline__toolbar" role="toolbar">' +
    '<span class="field-hint">' +     safeText(t("toolbar.window", lang)) + ": " +
    safeText(formatStamp(dateCtx.start, dateCtx.timezone)) + " – " +
    safeText(formatStamp(dateCtx.end, dateCtx.timezone)) +
    " (" + dateCtx.hours + "h · " + safeText(dateCtx.timezone) + ")</span>" +
    '<button type="button" class="button" data-action="prev">' + safeText(t("toolbar.prev", lang)) + "</button>" +
    '<button type="button" class="button" data-action="next">' + safeText(t("toolbar.next", lang)) + "</button>" +
    '<button type="button" class="button" data-action="zoom-out">' + safeText(t("toolbar.zoom_out", lang)) + "</button>" +
    '<button type="button" class="button" data-action="zoom-in">' + safeText(t("toolbar.zoom_in", lang)) + "</button>" +
    '<span class="timeline__kbd">' + safeText(t("toolbar.kbd", lang)) + "</span>" +
    "</div>";

  const cursors = state.cursors
    ? '<p class="timeline__cursors">' +
      safeText(t("cursors.state", lang)) + "=" + safeText(state.cursors.state_cursor ?? "—") + " · " +
      safeText(t("cursors.projection", lang)) + "=" + safeText(state.cursors.projection_cursor ?? "—") +
      (completeness ? " · " + safeText(t("cursors.completeness", lang)) + "=" + safeText(completeness) : "") +
      "</p>"
    : "";

  const banner = showUnknownBanner
    ? emptyState(lang, ["timeline.unknown", "timeline.unknown.hint"])
    : "";

  if (!bars.length && status !== "unknown") {
    return '<div class="timeline" data-status="empty">' + toolbar + legend(lang) +
      emptyState(lang, ["timeline.empty", "timeline.empty.hint"]) + cursors + "</div>";
  }

  const lanesHtml = lanes.map(lane => {
    const focused = lane.bars.some(b => b.interval_id === focus.selectedId);
    return '<div class="timeline__lane' + (focused ? " timeline__lane--focused" : "") +
      '" data-lane="' + safeText(lane.id) + '">' +
      '<div class="timeline__lane-label">' + safeText(lane.id) + "</div>" +
      '<div class="timeline__track" role="listbox" aria-label="' + safeText(lane.id) + '">' +
      lane.bars.map(b => renderBar(b, focus.selectedId, lang)).join("") +
      "</div>" +
      '<div class="timeline__axis"><span>' + safeText(formatAxis(dateCtx.start, dateCtx.timezone)) +
      "</span><span>" + safeText(formatAxis(dateCtx.end, dateCtx.timezone)) +
      "</span></div></div>";
  }).join("");

  const detail = options.showDetail === false ? "" : renderDetail(selected, { lang });

  return '<div class="timeline" data-status="' +
    safeText(showUnknownBanner && status === "ready" ? "unknown" : (status || "ready")) +
    '" data-hours="' + dateCtx.hours + '" data-selected="' + safeText(focus.selectedId || "") + '">' +
    toolbar + legend(lang) + banner +
    '<div class="timeline__lanes">' + lanesHtml + "</div>" +
    detail + cursors + "</div>";
}

/**
 * 挂载辅助：把键盘事件映射到选中变化与窗口变化（宿主绑定）。
 *
 * 窗口动作返回**新的日期上下文**，宿主拿它去取数（start / end）；
 * 视图不自己发请求，也不替宿主决定请求参数。
 */
export function applyKeyToState(state, options, key) {
  const dateCtx = options.dateContext || createDateContext(state.window || {});
  const bars = layoutBars(normalizeIntervals(state.intervals), dateCtx);
  const model = focusModel(bars, options.selectedId);
  const result = handleTimelineKey(key, model);
  const nextContext = result.window === "prev"
    ? shiftWindow(dateCtx, -1)
    : (result.window === "next" ? shiftWindow(dateCtx, 1) : dateCtx);
  return {
    selectedId: result.focus.selectedId,
    showDetail: result.closeDetail ? false : (result.openDetail ? true : options.showDetail !== false),
    focus: result.focus,
    window: result.window || null,
    dateContext: nextContext,
  };
}

/**
 * 键盘接线在模块加载时安装。
 *
 * 装在这里而不是宿主组件里，是因为「键盘与按钮等价」要求两者走同一条取数路径：
 * 宿主只要认工具栏按钮，键盘就自动等价，不需要第二套窗口状态。
 */
installTimelineKeyboard();
