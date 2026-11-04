// 工程清单（vnext / SETTINGS · PROJECTS）。
//
// 目标：一屏看清「本机登记了哪些工程、各自还剩多少活、最近谁在动」，并就地改名／移除。
//
// 为什么不再逐行显示「自动模式／计算上限／节点容量」：
//   /api/board 的工程快照只有登记信息（name / path / main_branch / max_parallel /
//   last_seen），按工程的执行策略写在各自 config.toml 里，快照不带，也没有批量读取
//   端点。旧版把这三个数逐行渲染，真实中枢下每一行都是「未就绪 + 同一句平台原因」，
//   外加两个永远的空值——那是把中枢级事实伪装成工程级属性，六行里五列完全一样。
//   现在策略只在页首说明入口，列表只列快照真读得到的字段；读不到的字段不占列。
//
// 三条硬规则：
//   1. 缺字段就是缺：路径／分支／参考值／最近活动拿不到时写占位符，不拿 0 或「刚刚」冒充。
//   2. 任务数由宿主按快照归组传入（state.taskCounts）；没传就写「—」并说明未知，
//      不把「没数」渲染成「0 道任务」。
//   3. 工程名与路径允许省略号截断，但必须带 title——截断不是丢掉完整值的理由。

const I18N = {
  zh: {
    "projects.meta.projects": "{n} 个工程",
    "projects.meta.with_tasks": "{n} 个有任务",
    "projects.meta.tasks": "{n} 道任务",
    "projects.meta.running": "进行中 {n}",
    "projects.meta.tasks_unknown": "任务数未知",
    "projects.meta.tasks_unknown.hint": "快照里没有任务行，这里不把「没取到」写成 0。",
    "projects.note": "自动模式／上限／容量写在各自 config.toml，本列表不回显；改名与路径用行内「编辑」，策略看「执行策略」页。",
    "projects.loading": "正在读取工程…",
    "projects.error": "工程列表加载失败。",
    "projects.error.hint": "确认本机中枢在运行，然后重试。",
    "projects.expired": "工程数据已过期。",
    "projects.expired.hint": "刷新后重新读取，不沿用上次结论。",
    "projects.unauthenticated": "未登录，无法查看工程。",
    "projects.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "projects.empty": "还没有登记任何工程。",
    "projects.empty.hint": "在工程目录里执行 maw init 后再回来。",
    "projects.unknown": "工程状态未知。",
    "projects.unknown.hint": "服务端没有返回工程快照，不能推断当前值。",
    "projects.col.name": "工程",
    "projects.col.tasks": "任务",
    "projects.col.branch": "主分支",
    "projects.col.ref": "max_parallel 参考值",
    "projects.col.ref.hint": "工程配置里的建议并行数，不是硬约束。",
    "projects.col.seen": "最近活动",
    "projects.col.actions": "操作",
    "projects.ref.unset": "未设置",
    "projects.ref.hint": "0 表示工程没有配置建议并行数。",
    "projects.tasks.running": "进行中 {n}",
    "projects.value.none": "—",
    "projects.remove": "移除",
    "projects.remove.hint": "只从本机中枢注销登记，不动工程目录",
    "projects.remove.confirm": "确认移除？",
    "projects.remove.confirm_yes": "确认移除",
    "projects.remove.cancel": "取消",
    "projects.remove.done": "已移除",
    "projects.edit": "编辑",
    "projects.edit.hint": "改名称、路径或执行策略（弹窗填写）",
    "time.just_now": "刚刚",
    "time.minutes": "{n} 分钟前",
    "time.hours": "{n} 小时前",
    "time.days": "{n} 天前",
  },
  en: {
    "projects.meta.projects": "{n} projects",
    "projects.meta.with_tasks": "{n} with tasks",
    "projects.meta.tasks": "{n} tasks",
    "projects.meta.running": "{n} running",
    "projects.meta.tasks_unknown": "task counts unknown",
    "projects.meta.tasks_unknown.hint": "The snapshot carries no task rows, so this is not rendered as 0.",
    "projects.note": "Auto mode, limit and capacity live in each project's config.toml and are not echoed here; use Edit for name and path, or the policy page for the values.",
    "projects.loading": "Loading projects…",
    "projects.error": "Failed to load projects.",
    "projects.error.hint": "Check that the local hub is running, then retry.",
    "projects.expired": "Project data is stale.",
    "projects.expired.hint": "Refresh and read again instead of reusing the old values.",
    "projects.unauthenticated": "Not signed in; projects are unavailable.",
    "projects.unauthenticated.hint": "Use a local session or token, then retry.",
    "projects.empty": "No projects registered yet.",
    "projects.empty.hint": "Run maw init in the project directory, then come back.",
    "projects.unknown": "Project state unknown.",
    "projects.unknown.hint": "The server returned no project snapshot; values cannot be inferred.",
    "projects.col.name": "Project",
    "projects.col.tasks": "Tasks",
    "projects.col.branch": "Main branch",
    "projects.col.ref": "max_parallel reference",
    "projects.col.ref.hint": "Suggested parallelism from project config; not a hard limit.",
    "projects.col.seen": "Last active",
    "projects.col.actions": "Actions",
    "projects.ref.unset": "Not set",
    "projects.ref.hint": "0 means the project sets no suggested parallelism.",
    "projects.tasks.running": "{n} running",
    "projects.value.none": "—",
    "projects.remove": "Remove",
    "projects.remove.hint": "Unregisters from the local hub only; the project directory stays untouched",
    "projects.remove.confirm": "Remove this project?",
    "projects.remove.confirm_yes": "Confirm removal",
    "projects.remove.cancel": "Cancel",
    "projects.remove.done": "Removed",
    "projects.edit": "Edit",
    "projects.edit.hint": "Change name, path or execution policy",
    "time.just_now": "just now",
    "time.minutes": "{n} min ago",
    "time.hours": "{n} h ago",
    "time.days": "{n} d ago",
  },
};

export function t(key, lang = "zh", params = {}) {
  const pack = I18N[lang] || I18N.zh;
  const raw = pack[key] ?? I18N.zh[key] ?? key;
  return String(raw).replace(/\{(\w+)\}/g, (_, name) =>
    params[name] === undefined ? "" : String(params[name])
  );
}

export function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, ch => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[ch]));
}

function toInt(value) {
  if (value === undefined || value === null || value === "") return null;
  const n = Number(value);
  return Number.isFinite(n) ? Math.trunc(n) : null;
}

function parseTime(value) {
  if (value === undefined || value === null || value === "") return null;
  if (typeof value === "number") return value;
  const ms = Date.parse(String(value));
  return Number.isNaN(ms) ? null : ms;
}

/**
 * 最近活动：相对时间更好扫，但完整时间不能丢——悬停看 ISO 原文。
 * 超过 30 天不再说「N 天前」，直接给日期（UTC 截断，不做时区换算，避免同一行
 * 在两台机器上显示成不同日期）。
 */
export function relativeTime(value, lang = "zh", options = {}) {
  const ms = parseTime(value);
  if (ms === null) return null;
  const now = options.now ?? Date.now();
  const diff = Math.max(0, now - ms);
  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (diff < minute) return t("time.just_now", lang);
  if (diff < hour) return t("time.minutes", lang, { n: Math.floor(diff / minute) });
  if (diff < day) return t("time.hours", lang, { n: Math.floor(diff / hour) });
  if (diff < 30 * day) return t("time.days", lang, { n: Math.floor(diff / day) });
  return String(value).slice(0, 10);
}

/**
 * 单个工程的列表投影。
 * 只搬快照里真有的字段：缺失一律 null／空串，由渲染层写占位符。
 */
export function projectRow(project = {}) {
  const id = String(project.project_id || project.id || "");
  const ref = toInt(project.max_parallel_reference ?? project.max_parallel);
  return {
    id,
    name: String(project.name || id),
    path: typeof project.path === "string" ? project.path : "",
    branch: typeof project.main_branch === "string" ? project.main_branch : "",
    ref,
    lastSeen: project.last_seen || "",
  };
}

/** 任务计数只认宿主给的归组结果；没给就是未知，不补 0。 */
export function taskCountOf(state, id) {
  if (!state || !state.taskCounts || typeof state.taskCounts !== "object") return null;
  const bucket = state.taskCounts[id];
  if (!bucket || typeof bucket !== "object") return { total: 0, running: 0 };
  return {
    total: toInt(bucket.total) ?? 0,
    running: toInt(bucket.running) ?? 0,
  };
}

function emptyState(lang, keys) {
  return '<div class="empty-state"><p class="empty-state__text">' + esc(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + esc(t(keys[1], lang)) + "</p></div>";
}

/**
 * 操作列：移除是危险动作，所以走两段式（先问再确认），
 * 而且只有可写页面才渲染——静态导出页不该出现一条永远点不动的按钮。
 */
function removalCell(view, lang, options) {
  const pending = String(options.pendingRemove || "") === view.id;
  if (pending) {
    return '<td class="projects-actions" data-field="actions">' +
      '<span class="projects-confirm">' +
      '<span class="projects-confirm__ask">' + esc(t("projects.remove.confirm", lang)) + "</span>" +
      '<button type="button" class="projects-confirm__yes" data-project-remove-confirm="' +
      esc(view.id) + '">' + esc(t("projects.remove.confirm_yes", lang)) + "</button>" +
      '<button type="button" class="projects-confirm__no" data-project-remove-cancel="' +
      esc(view.id) + '">' + esc(t("projects.remove.cancel", lang)) + "</button>" +
      "</span></td>";
  }
  return '<td class="projects-actions" data-field="actions">' +
    '<button type="button" class="projects-edit" data-project-edit="' + esc(view.id) +
    '" title="' + esc(t("projects.edit.hint", lang)) + '">' +
    esc(t("projects.edit", lang)) + "</button>" +
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

/** 页首一行：工程数、有任务的工程数、任务总量与进行中——都是快照能直接算出来的。 */
function metaLine(state, projects, lang) {
  const parts = [t("projects.meta.projects", lang, { n: projects.length })];
  const known = Boolean(state.taskCounts && typeof state.taskCounts === "object");
  if (known) {
    let withTasks = 0;
    let total = 0;
    let running = 0;
    for (const project of projects) {
      const count = taskCountOf(state, projectRow(project).id);
      if (!count) continue;
      if (count.total > 0) withTasks += 1;
      total += count.total;
      running += count.running;
    }
    parts.push(t("projects.meta.with_tasks", lang, { n: withTasks }));
    parts.push(t("projects.meta.tasks", lang, { n: total }));
    parts.push(t("projects.meta.running", lang, { n: running }));
  } else {
    parts.push(t("projects.meta.tasks_unknown", lang));
  }
  const title = known ? "" : ' title="' + esc(t("projects.meta.tasks_unknown.hint", lang)) + '"';
  return '<p class="projects-meta field-hint"' + title + ">" + esc(parts.join(" · ")) + "</p>";
}

function nameCell(view, lang) {
  const dash = t("projects.value.none", lang);
  const path = view.path
    ? '<span class="projects-path" title="' + esc(view.path) + '">' + esc(view.path) + "</span>"
    : '<span class="projects-path projects-path--missing">' + esc(dash) + "</span>";
  // title 挂在真正会被截断的元素上：悬停哪一行就看哪一行的全值。
  return '<td class="name">' +
    '<span class="projects-name" title="' + esc(view.name) + '">' + esc(view.name) + "</span>" + path + "</td>";
}

function taskCell(count, lang) {
  const dash = t("projects.value.none", lang);
  if (!count) return '<td class="num" data-field="tasks">' + esc(dash) + "</td>";
  // 第二行只在真的有人跑时才出现：没有进行中的任务就只留总数，
  // 否则每一行都会多一句「无任务」，把列撑成两行却没多给信息。
  const note = count.running > 0
    ? '<span class="projects-sub projects-sub--running">' +
      esc(t("projects.tasks.running", lang, { n: count.running })) + "</span>"
    : "";
  return '<td class="num" data-field="tasks">' +
    '<span class="projects-num">' + esc(count.total) + "</span>" + note + "</td>";
}

function refCell(ref, lang) {
  const dash = t("projects.value.none", lang);
  if (ref === null) return '<td class="num" data-field="ref">' + esc(dash) + "</td>";
  if (ref === 0) {
    return '<td class="num" data-field="ref" title="' + esc(t("projects.ref.hint", lang)) + '">' +
      '<span class="projects-muted">' + esc(t("projects.ref.unset", lang)) + "</span></td>";
  }
  return '<td class="num" data-field="ref">' + esc(ref) + "</td>";
}

function seenCell(view, lang, options) {
  const dash = t("projects.value.none", lang);
  const relative = relativeTime(view.lastSeen, lang, { now: options.now ?? Date.now() });
  if (!relative) return '<td class="time" data-field="seen">' + esc(dash) + "</td>";
  return '<td class="time" data-field="seen" title="' + esc(view.lastSeen) + '">' +
    esc(relative) + "</td>";
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
  const meta = metaLine(state, projects, lang);

  if (!projects.length) {
    return meta + emptyState(lang, ["projects.empty", "projects.empty.hint"]);
  }

  // 只有明确声明「能写」的宿主才渲染操作列：视图不该猜自己有没有写通道。
  const removable = options.canRemove === true;
  const rows = projects.map(project => {
    const view = projectRow(project);
    const dash = t("projects.value.none", lang);
    return '<tr data-project="' + esc(view.id) + '">' +
      nameCell(view, lang) +
      taskCell(taskCountOf(state, view.id), lang) +
      '<td class="projects-cell-branch" data-field="branch">' +
      (view.branch
        ? '<span class="projects-branch" title="' + esc(view.branch) + '">' + esc(view.branch) + "</span>"
        : esc(dash)) + "</td>" +
      refCell(view.ref, lang) +
      seenCell(view, lang, options) +
      (removable ? removalCell(view, lang, options) : "") +
      "</tr>";
  }).join("");

  const cols = [
    { key: "name", cls: "projects-col--name" },
    { key: "tasks", cls: "projects-col--tasks", align: "num" },
    { key: "branch", cls: "projects-col--branch" },
    { key: "ref", cls: "projects-col--ref", align: "num" },
    { key: "seen", cls: "projects-col--seen", align: "num" },
  ];
  if (removable) cols.push({ key: "actions", cls: "projects-col--actions", align: "num" });

  // 列宽写在 colgroup 里：固定布局下省略号才有确定的宽度可截，
  // 否则「工程」列会被相邻的长文本挤成四五个字符。
  const colgroup = "<colgroup>" + cols.map(col =>
    '<col class="' + col.cls + '">').join("") + "</colgroup>";

  return meta +
    '<p class="projects-note field-hint">' + esc(t("projects.note", lang)) + "</p>" +
    noticeRow(options, lang) +
    '<div class="table-wrap"><table class="table projects-table">' + colgroup +
    "<thead><tr>" +
    cols.map(col => {
      const label = t("projects.col." + col.key, lang);
      const hint = col.key === "ref" ? t("projects.col.ref.hint", lang) : "";
      const cls = col.align === "num" ? ' class="projects-th--num"' : "";
      return "<th" + cls + (hint ? ' title="' + esc(hint) + '"' : "") + ">" + esc(label) + "</th>";
    }).join("") +
    "</tr></thead><tbody>" + rows + "</tbody></table></div>";
}
