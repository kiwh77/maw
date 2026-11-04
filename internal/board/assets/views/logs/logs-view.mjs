// 中枢事件日志：只读尾部，用于诊断与调优。

function esc(s) {
  return String(s ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

const I18N = {
  zh: {
    loading: "正在读取日志…",
    error: "日志加载失败。",
    errorHint: "确认本机中枢在运行且已登录后重试。",
    empty: "还没有可显示的事件。",
    emptyHint: "领取、进度、派发与系统作业事件会出现在这里。",
    count: "条事件",
    colTime: "时间",
    colKind: "类型",
    colActor: "主体",
    colMessage: "摘要",
  },
  en: {
    loading: "Reading logs…",
    error: "Failed to load logs.",
    errorHint: "Check that the hub is running and you are signed in.",
    empty: "No events to show yet.",
    emptyHint: "Claim, progress, dispatch and system-job events appear here.",
    count: "events",
    colTime: "Time",
    colKind: "Kind",
    colActor: "Actor",
    colMessage: "Summary",
  },
};

function t(key, lang) {
  return (lang === "en" ? I18N.en : I18N.zh)[key] || key;
}

function emptyState(lang, title, hint) {
  return '<div class="empty-state"><p class="empty-state__text">' + esc(title) + "</p>" +
    '<p class="empty-state__text">' + esc(hint) + "</p></div>";
}

/**
 * 时间列：/api/v1/events 的行用 `occurred_at`（事实发生时刻）与 `received_at`（入库时刻），
 * 旧旁路载荷用 `ts`／`created_at`。先取事实时刻，取不到再退回入库时刻，
 * 都不写就不编时间——空单元格比一个假时间诚实。
 */
function timeOf(it) {
  return String(it.occurred_at || it.received_at || it.ts || it.created_at || "");
}

/**
 * 主体列：事件行没有 actor 字段，真正能定位「是谁」的是任务实例、运行与工程，
 * 依次退回，全部缺失就留空。
 */
function subjectOf(it) {
  return String(it.task_instance || it.instance_id || it.actor || it.agent || it.project_id || "");
}

/**
 * 摘要列：优先取显式文案；事件行没有 message，就用「阶段 · 质量 · 运行」拼一句
 * 可核对的事实，实在没有才退回载荷前 120 字符（没有载荷就留空，不显示 `{}`）。
 */
function summaryOf(it) {
  const explicit = it.message || it.summary || it.payload_summary;
  if (explicit) return String(explicit);
  const facts = [it.phase, it.quality, it.run_id].filter(Boolean).map(String);
  if (facts.length) return facts.join(" · ");
  if (it.payload && typeof it.payload === "object" && Object.keys(it.payload).length) {
    return JSON.stringify(it.payload).slice(0, 120);
  }
  return "";
}

/** @param {{status?:string, items?:Array}} state */
export function renderLogs(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  if (state.status === "loading") return '<p class="field-hint">' + esc(t("loading", lang)) + "</p>";
  if (state.status === "error" || state.status === "unauthenticated") {
    return emptyState(lang, t("error", lang), t("errorHint", lang));
  }
  const items = Array.isArray(state.items) ? state.items : [];
  if (!items.length) return emptyState(lang, t("empty", lang), t("emptyHint", lang));

  const rows = items.map((it) => "<tr>" +
    '<td class="id">' + esc(timeOf(it)) + "</td>" +
    "<td>" + esc(it.kind || it.type || "") + "</td>" +
    "<td>" + esc(subjectOf(it)) + "</td>" +
    "<td>" + esc(summaryOf(it)) + "</td>" +
    "</tr>").join("");

  return '<p class="field-hint">' + esc(items.length + " " + t("count", lang)) + "</p>" +
    '<div class="table-wrap"><table class="table"><thead><tr>' +
    "<th>" + esc(t("colTime", lang)) + "</th>" +
    "<th>" + esc(t("colKind", lang)) + "</th>" +
    "<th>" + esc(t("colActor", lang)) + "</th>" +
    "<th>" + esc(t("colMessage", lang)) + "</th>" +
    "</tr></thead><tbody>" + rows + "</tbody></table></div>";
}
