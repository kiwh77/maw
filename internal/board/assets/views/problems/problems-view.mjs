// 运行问题视图：聚合阻塞任务、异常运行与系统作业失败信号，便于优化 maw。

function esc(s) {
  return String(s ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

const I18N = {
  zh: {
    loading: "正在汇总问题…",
    error: "问题列表加载失败。",
    errorHint: "确认本机中枢在运行后重试。",
    empty: "当前没有可报告的运行问题。",
    emptyHint: "阻塞任务、对账异常与失败作业会出现在这里。",
    count: "条问题",
    colKind: "类型",
    colRef: "对象",
    colDetail: "说明",
    kindBlocked: "阻塞任务",
    kindRun: "运行异常",
    kindJob: "系统作业",
    kindAgent: "Agent",
  },
  en: {
    loading: "Collecting problems…",
    error: "Failed to load problems.",
    errorHint: "Check that the local hub is running, then retry.",
    empty: "No runtime problems to report.",
    emptyHint: "Blocked tasks, reconciliation issues and failed jobs show up here.",
    count: "problems",
    colKind: "Kind",
    colRef: "Subject",
    colDetail: "Detail",
    kindBlocked: "Blocked task",
    kindRun: "Run issue",
    kindJob: "System job",
    kindAgent: "Agent",
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
export function renderProblems(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  if (state.status === "loading") return '<p class="field-hint">' + esc(t("loading", lang)) + "</p>";
  if (state.status === "error" || state.status === "unauthenticated") {
    return emptyState(lang, t("error", lang), t("errorHint", lang));
  }
  const items = Array.isArray(state.items) ? state.items : [];
  if (!items.length) return emptyState(lang, t("empty", lang), t("emptyHint", lang));

  const rows = items.map((it) => {
    const kindKey = "kind" + String(it.kind || "Blocked").replace(/^./, (c) => c.toUpperCase());
    const kindLabel = t(kindKey, lang) !== kindKey ? t(kindKey, lang) : String(it.kind || "");
    return "<tr>" +
      "<td>" + esc(kindLabel) + "</td>" +
      '<td class="id">' + esc(it.ref || "") + "</td>" +
      "<td>" + esc(it.detail || "") + "</td>" +
      "</tr>";
  }).join("");

  return '<p class="field-hint">' + esc(items.length + " " + t("count", lang)) + "</p>" +
    '<div class="table-wrap"><table class="table"><thead><tr>' +
    "<th>" + esc(t("colKind", lang)) + "</th>" +
    "<th>" + esc(t("colRef", lang)) + "</th>" +
    "<th>" + esc(t("colDetail", lang)) + "</th>" +
    "</tr></thead><tbody>" + rows + "</tbody></table></div>";
}
