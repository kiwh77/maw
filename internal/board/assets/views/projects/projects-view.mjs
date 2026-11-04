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
    "projects.col.actions": "操作",
    "projects.workspace.git": "Git",
    "projects.workspace.nongit": "非 Git",
    "projects.workspace.unknown": "未识别",
    "projects.count": "个工程",
    "projects.not_ready_hint": "自动模式未就绪的原因：",
    "projects.edit": "编辑",
    "projects.edit.hint": "改名称、路径与执行策略",
    "projects.remove": "移除",
    "projects.remove.hint": "只从本机中枢注销登记，不动工程目录",
    "projects.remove.confirm": "确认移除？",
    "projects.remove.confirm_yes": "确认移除",
    "projects.remove.cancel": "取消",
    "projects.remove.busy": "还有未完成任务或在线 Agent",
    "projects.remove.done": "已移除",
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
    "projects.col.actions": "Actions",
    "projects.workspace.git": "Git",
    "projects.workspace.nongit": "Non-Git",
    "projects.workspace.unknown": "Unknown",
    "projects.count": "projects",
    "projects.not_ready_hint": "Auto mode not ready because:",
    "projects.edit": "Edit",
    "projects.edit.hint": "Change name, path and execution policy",
    "projects.remove": "Remove",
    "projects.remove.hint": "Unregisters from the local hub only; the project directory stays untouched",
    "projects.remove.confirm": "Remove this project?",
    "projects.remove.confirm_yes": "Confirm removal",
    "projects.remove.cancel": "Cancel",
    "projects.remove.busy": "Tasks or Agents are still live",
    "projects.remove.done": "Removed",
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

/**
 * 操作列：移除是危险动作，所以走两段式（先问再确认），
 * 而且只有可写页面才渲染——静态导出页不该出现一条永远点不动的按钮。
 */
function removalCell(view, lang, options) {
  const pending = String(options.pendingRemove || "") === view.id;
  if (pending) {
    return '<td class="actions" data-field="actions">' +
      '<span class="projects-confirm">' +
      '<span class="projects-confirm__ask">' + esc(t("projects.remove.confirm", lang)) + "</span>" +
      '<button type="button" class="projects-confirm__yes" data-project-remove-confirm="' +
      esc(view.id) + '">' + esc(t("projects.remove.confirm_yes", lang)) + "</button>" +
      '<button type="button" class="projects-confirm__no" data-project-remove-cancel="' +
      esc(view.id) + '">' + esc(t("projects.remove.cancel", lang)) + "</button>" +
      "</span></td>";
  }
  return '<td class="actions" data-field="actions">' +
    '<button type="button" class="projects-edit" data-project-edit="' + esc(view.id) +
    '" title="' + esc(t("projects.edit.hint", lang)) + '">' +
    esc(t("projects.edit", lang)) + "</button> " +
    '<button type="button" class="projects-remove" data-project-remove="' + esc(view.id) +
    '" title="' + esc(t("projects.remove.hint", lang)) + '">' +
    esc(t("projects.remove", lang)) + "</button></td>";
}

/** 结果提示：成功与失败都要说清楚，失败不能静默回落到「看起来还在」。 */
function noticeRow(options, lang) {
  const notice = options.removeNotice;
  if (!notice || !notice.kind) return "";
  const cls = notice.kind === "error" ? "projects-notice projects-notice--error" : "projects-notice";
  const fallback = notice.kind === "error"
    ? t("projects.error", lang)
    : t("projects.remove.done", lang);
  return '<p class="' + cls + '" data-notice-kind="' + esc(notice.kind) + '">' +
    esc(notice.text || fallback) + "</p>";
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
  const count = '<p class="projects-count field-hint">' + projects.length + " " +
    esc(t("projects.count", lang)) + "</p>";

  if (!projects.length) return count + emptyState(lang, ["projects.empty", "projects.empty.hint"]);

  // 只有明确声明「能写」的宿主才渲染移除按钮：视图不该猜自己有没有写通道。
  const removable = options.canRemove === true;
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
      '<td class="num" data-field="ref">' + esc(p.ref ?? dash) + "</td>" +
      '<td data-field="workspace">' + esc(t("projects.workspace." + view.workspace, lang)) + "</td>" +
      (removable ? removalCell(view, lang, options) : "") +
      "</tr>";
  }).join("");

  const cols = ["name", "auto", "limit", "capacity", "ref", "workspace"];
  if (removable) cols.push("actions");

  return count + noticeRow(options, lang) +
    '<table class="table"><thead><tr>' +
    cols.map(col => "<th>" + esc(t("projects.col." + col, lang)) + "</th>").join("") +
    "</tr></thead><tbody>" + rows + "</tbody></table>";
}
