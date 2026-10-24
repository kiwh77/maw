// 工程列表与执行策略概览（vnext / UI · VN-725）。
//
// 目标：在一屏里看出「这个工程眼下怎么跑」，并且不把四个数混成一个：
// 自动模式（状态）／计算上限（硬约束）／节点容量（单节点）／max_parallel 参考值（建议）。
//
// 与设置页共用判定（`../settings/settings-view.mjs`）：标签、三态、校验、
// 秘密字段规则都只有一份实现，页面之间不各写一套（UI-C：不另造公共层，
// 也不复制规则）。这里只做列表投影。

import {
  AUTO_MODE,
  autoModeOf,
  autoReasonOf,
  esc,
  normalizePolicy,
  t as tSettings,
} from "../settings/settings-view.mjs";

const I18N = {
  zh: {
    "projects.title": "工程",
    "projects.subtitle": "每个工程的执行策略；四个数值含义不同，按标签读。",
    "projects.loading": "正在读取工程…",
    "projects.error": "工程列表加载失败。",
    "projects.error.hint": "确认本机中枢在运行，然后重试。",
    "projects.expired": "工程数据已过期。",
    "projects.expired.hint": "刷新后重新读取，不沿用上次策略。",
    "projects.unauthenticated": "未登录，无法查看工程。",
    "projects.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "projects.empty": "还没有登记任何工程。",
    "projects.empty.hint": "在工程目录里执行 maw init 后再回来。",
    "projects.unknown": "策略状态未知。",
    "projects.unknown.hint": "服务端没有返回策略版本，不能推断当前值。",
    "projects.col.name": "工程",
    "projects.col.auto": "自动模式",
    "projects.col.limit": "计算上限",
    "projects.col.capacity": "节点容量",
    "projects.col.ref": "max_parallel 参考值",
    "projects.col.workspace": "工作区",
    "projects.workspace.git": "Git",
    "projects.workspace.nongit": "非 Git",
    "projects.workspace.unknown": "未识别",
    "projects.ref.note": "仅供参考，不是硬约束",
    "projects.count": "个工程",
    "projects.not_ready_hint": "自动模式未就绪的原因：",
  },
  en: {
    "projects.title": "Projects",
    "projects.subtitle": "Execution policy per project; the four numbers mean different things.",
    "projects.loading": "Loading projects…",
    "projects.error": "Failed to load projects.",
    "projects.error.hint": "Check that the local hub is running, then retry.",
    "projects.expired": "Project data is stale.",
    "projects.expired.hint": "Refresh and read the policy again instead of reusing the old one.",
    "projects.unauthenticated": "Not signed in; projects are unavailable.",
    "projects.unauthenticated.hint": "Use a local session or token, then retry.",
    "projects.empty": "No projects registered yet.",
    "projects.empty.hint": "Run maw init in the project directory, then come back.",
    "projects.unknown": "Policy state unknown.",
    "projects.unknown.hint": "The server returned no policy revision; values cannot be inferred.",
    "projects.col.name": "Project",
    "projects.col.auto": "Auto mode",
    "projects.col.limit": "Execution limit",
    "projects.col.capacity": "Node capacity",
    "projects.col.ref": "max_parallel reference",
    "projects.col.workspace": "Workspace",
    "projects.workspace.git": "Git",
    "projects.workspace.nongit": "Non-Git",
    "projects.workspace.unknown": "Unknown",
    "projects.ref.note": "Advisory only, not a hard constraint",
    "projects.count": "projects",
    "projects.not_ready_hint": "Auto mode not ready because:",
  },
};

function t(key, lang) {
  const table = lang === "en" ? I18N.en : I18N.zh;
  return table[key] ?? tSettings(key, lang);
}

const AUTO_BADGE = {
  [AUTO_MODE.on]: "badge--done",
  [AUTO_MODE.off]: "badge--self_check",
  [AUTO_MODE.not_ready]: "badge--blocked",
};

function badge(text, cls) {
  return '<span class="badge ' + cls + '">' + esc(text) + "</span>";
}

function emptyState(lang, keys) {
  return '<div class="empty-state"><p class="empty-state__text">' + esc(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + esc(t(keys[1], lang)) + "</p></div>";
}

/**
 * 单个工程的策略投影。
 * 缺字段一律 `null` → 渲染成「—」，不拿 0 或默认值冒充服务端结论。
 */
export function projectPolicy(project = {}, options = {}) {
  const policy = normalizePolicy(project.policy || project);
  const mode = autoModeOf(policy, {
    platformSupported: options.platformSupported ?? project.platform_supported,
    configApplied: project.config_applied,
    configEnabled: policy.autoEnabled === true,
  });
  return {
    id: project.project_id || project.id || "",
    name: project.name || project.project_id || project.id || "",
    policy,
    mode,
    reasonKey: mode === AUTO_MODE.not_ready
      ? autoReasonOf({
        platformSupported: options.platformSupported ?? project.platform_supported,
        configApplied: project.config_applied,
      })
      : null,
    workspace: project.workspace === "git" || project.workspace === "nongit" ? project.workspace : "unknown",
    revision: policy.revision,
  };
}

/** 列表页：加载／空／错误／过期／未认证／unknown 各走一条明确分支。 */
export function renderProjectList(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const status = state.status || "ready";

  if (status === "loading") return '<p class="projects-loading">' + esc(t("projects.loading", lang)) + "</p>";
  if (status === "unauthenticated") return emptyState(lang, ["projects.unauthenticated", "projects.unauthenticated.hint"]);
  if (status === "error") return emptyState(lang, ["projects.error", "projects.error.hint"]);
  if (status === "expired") return emptyState(lang, ["projects.expired", "projects.expired.hint"]);
  if (status === "unknown") return emptyState(lang, ["projects.unknown", "projects.unknown.hint"]);

  const projects = Array.isArray(state.projects) ? state.projects : [];
  const header = '<header class="projects-head"><h1>' + esc(t("projects.title", lang)) + "</h1>" +
    '<p class="field-hint">' + esc(t("projects.subtitle", lang)) + "</p>" +
    '<p class="projects-count">' + projects.length + " " + esc(t("projects.count", lang)) + "</p></header>";

  if (!projects.length) return header + emptyState(lang, ["projects.empty", "projects.empty.hint"]);

  const rows = projects.map(project => {
    const view = projectPolicy(project, options);
    const p = view.policy;
    const dash = "—";
    return '<tr data-project="' + esc(view.id) + '">' +
      '<td class="name">' + esc(view.name) + "</td>" +
      "<td>" + badge(t("auto." + view.mode, lang), AUTO_BADGE[view.mode]) +
      (view.reasonKey ? '<p class="field-hint">' + esc(t("projects.not_ready_hint", lang)) + esc(t(view.reasonKey, lang)) + "</p>" : "") +
      "</td>" +
      '<td class="num" data-field="limit">' + esc(p.limit ?? dash) + "</td>" +
      '<td class="num" data-field="capacity">' + esc(p.capacity ?? dash) + "</td>" +
      '<td class="num" data-field="ref">' + esc(p.ref ?? dash) +
      '<p class="field-hint">' + esc(t("projects.ref.note", lang)) + "</p></td>" +
      '<td data-field="workspace">' + esc(t("projects.workspace." + view.workspace, lang)) + "</td>" +
      "</tr>";
  }).join("");

  return header +
    '<table class="table"><thead><tr>' +
    ["name", "auto", "limit", "capacity", "ref", "workspace"]
      .map(col => "<th>" + esc(t("projects.col." + col, lang)) + "</th>").join("") +
    "</tr></thead><tbody>" + rows + "</tbody></table>";
}
