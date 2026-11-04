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
    '<td class="id">' + esc(it.ts || it.created_at || "") + "</td>" +
    "<td>" + esc(it.kind || it.type || "") + "</td>" +
    "<td>" + esc(it.actor || it.agent || "") + "</td>" +
    "<td>" + esc(it.message || it.summary || it.payload_summary || JSON.stringify(it.payload || {}).slice(0, 120)) + "</td>" +
    "</tr>").join("");

  return '<p class="field-hint">' + esc(items.length + " " + t("count", lang)) + "</p>" +
    '<div class="table-wrap"><table class="table"><thead><tr>' +
    "<th>" + esc(t("colTime", lang)) + "</th>" +
    "<th>" + esc(t("colKind", lang)) + "</th>" +
    "<th>" + esc(t("colActor", lang)) + "</th>" +
    "<th>" + esc(t("colMessage", lang)) + "</th>" +
    "</tr></thead><tbody>" + rows + "</tbody></table></div>";
}
