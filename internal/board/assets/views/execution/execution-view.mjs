// 执行视图（vnext / EV-001）：按迭代聚合任务、运行、交付。
//
// 目标：主视图回答「这一批题在发生什么、卡在哪、交出了什么」，而不是把任务流水
// 平铺成一屏。迭代是任务的容器，也是这里唯一的索引。
//
// 依据：
//   docs/maw_integrated_feature_list.zh-CN.md F53/F56/F58
//     （按迭代查看任务／运行／交付三类数据；执行视图为主；详情走抽屉）
//   internal/httpapi/read/iteration.go
//     （任务行带 iteration_id；runs/deliveries 用 task_instance 关联到任务）
//
// 四条硬规则：
//   1. 迭代归属只认任务行上的 iteration_id。runs/deliveries 没有 iteration 字段，
//      只能经 task_instance 归到任务所在迭代；找不到对应任务的行不猜迭代。
//   2. 三类数据任何一类缺失都不算成功：缺 runs 就把整页标成加载失败，
//      而不是渲染「0 次运行」——那会把「没取到」说成「没跑过」。
//   3. 没有迭代时给明确空态（并说明先去建迭代／跑 plan apply），不显示空表格。
//   4. 详情走抽屉：任务行只带 data-task-id / data-project-id，由宿主打开抽屉，
//      视图不自己拼详情、不改路由。
//
// 本视图只做展示与分组，不实现调度、不写库、不新增公共层：
// 类名一律来自 assets/components 与 shell.css（table/badge/empty/field-hint）。

const I18N = {
  zh: {
    "execution.loading": "正在读取执行视图…",
    "execution.error": "执行视图加载失败。",
    "execution.error.hint": "任务、运行或交付有一类没取到，就不当作空结果；确认本机中枢在运行后重试。",
    "execution.expired": "执行数据已过期。",
    "execution.expired.hint": "刷新后重新读取，不沿用上次结论。",
    "execution.unauthenticated": "未登录，无法读取执行数据。",
    "execution.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "execution.no_project": "还没有选定工程。",
    "execution.no_project.hint": "不选工程时这里汇总全部工程的迭代；只想看一个工程，就在工程下拉里选它。",
    "execution.empty": "还没有可聚合的迭代。",
    "execution.empty.hint": "用 maw plan apply 把任务带迭代归属写入，或先建迭代再加题。",
    "execution.unassigned": "未归属迭代",
    "execution.unassigned.hint": "这些任务还没归入任何迭代：它们会执行、也会合入，但不会出现在迭代收尾里。",
    "execution.iterations": "迭代",
    "execution.iteration.count": "个迭代",
    "execution.task.count": "道任务",
    "execution.selected": "当前查看",
    "execution.summary.tasks": "任务",
    "execution.summary.runs": "运行",
    "execution.summary.deliveries": "交付",
    "execution.summary.tasks.note": "进行中 {active} · 阻塞 {blocked} · 完成 {done}",
    "execution.summary.runs.note": "仍占资源 {occupying} 次",
    "execution.summary.deliveries.note": "最近一次 {latest}",
    "execution.section.tasks": "任务",
    "execution.section.runs": "运行",
    "execution.section.deliveries": "交付",
    "execution.section.empty": "该迭代下暂无{kind}记录。",
    "execution.open": "打开任务抽屉",
    "col.id": "编号",
    "col.title": "标题",
    "col.status": "状态",
    "col.owner": "负责人",
    "col.run": "运行",
    "col.task": "任务",
    "col.phase": "阶段",
    "col.occupies": "占资源",
    "col.delivery": "交付",
    "col.mode": "方式",
    "col.delivered_at": "交付时间",
    "value.none": "—",
    "value.yes": "是",
    "value.no": "否",
  },
  en: {
    "execution.loading": "Loading execution view…",
    "execution.error": "Failed to load the execution view.",
    "execution.error.hint": "If any of tasks, runs or deliveries is missing, this is not an empty result. Check the local hub and retry.",
    "execution.expired": "Execution data is out of date.",
    "execution.expired.hint": "Refresh to re-read; previous conclusions are not reused.",
    "execution.unauthenticated": "Not signed in; execution data is unavailable.",
    "execution.unauthenticated.hint": "Use a local session or token and retry.",
    "execution.no_project": "No project selected yet.",
    "execution.no_project.hint": "With no project selected this view aggregates every project; pick one in the project menu to narrow it down.",
    "execution.empty": "No iterations to aggregate yet.",
    "execution.empty.hint": "Run maw plan apply to write tasks with iteration ids, or create an iteration and add tasks.",
    "execution.unassigned": "No iteration",
    "execution.unassigned.hint": "These tasks belong to no iteration: they still run and merge, but they never appear in an iteration close-out.",
    "execution.iterations": "Iterations",
    "execution.iteration.count": "iterations",
    "execution.task.count": "tasks",
    "execution.selected": "Viewing",
    "execution.summary.tasks": "Tasks",
    "execution.summary.runs": "Runs",
    "execution.summary.deliveries": "Deliveries",
    "execution.summary.tasks.note": "active {active} · blocked {blocked} · done {done}",
    "execution.summary.runs.note": "still occupying {occupying}",
    "execution.summary.deliveries.note": "latest {latest}",
    "execution.section.tasks": "Tasks",
    "execution.section.runs": "Runs",
    "execution.section.deliveries": "Deliveries",
    "execution.section.empty": "No {kind} recorded under this iteration.",
    "execution.open": "Open task drawer",
    "col.id": "ID",
    "col.title": "Title",
    "col.status": "Status",
    "col.owner": "Owner",
    "col.run": "Run",
    "col.task": "Task",
    "col.phase": "Phase",
    "col.occupies": "Occupies",
    "col.delivery": "Delivery",
    "col.mode": "Mode",
    "col.delivered_at": "Delivered",
    "value.none": "—",
    "value.yes": "yes",
    "value.no": "no",
  },
};

const ACTIVE_STATUS = Object.freeze(["claimed", "in_progress", "self_check"]);

function dict(lang) {
  return lang === "en" ? I18N.en : I18N.zh;
}

export function t(key, lang, vars) {
  const table = dict(lang);
  const raw = table[key] ?? I18N.zh[key] ?? key;
  if (!vars) return raw;
  return raw.replace(/\{(\w+)\}/g, (_, name) => String(vars[name] ?? ""));
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

function badge(text, cls) {
  return '<span class="badge ' + cls + '">' + esc(text) + "</span>";
}

function emptyState(lang, keys, attrs = "") {
  return '<div class="empty-state"' + attrs + '><p class="empty-state__text">' +
    esc(t(keys[0], lang)) + '</p><p class="empty-state__text">' + esc(t(keys[1], lang)) + "</p></div>";
}

// ---------- 分组 ----------

/** 任务实例 id：契约字段叫 task_instance，快照里叫 instance_id，两处都要认。 */
export function taskInstanceOf(task) {
  return String(task?.task_instance || task?.instance_id || "").trim();
}

/** 任务展示编号：详情抽屉与 CLI 都用它，不用 instance_id。 */
export function taskDisplayOf(task) {
  return String(task?.display_id || task?.id || taskInstanceOf(task) || "").trim();
}

function iterationOf(task) {
  return String(task?.iteration_id ?? "").trim();
}

function projectOf(task) {
  return String(task?.project_id ?? "").trim();
}

/** 分组键的分隔符：迭代编号只在一个工程内唯一，跨工程必须带上工程。 */
const KEY_SEP = "::";

/**
 * 分组键 = 工程 + 迭代。
 *
 * 不选工程时（中枢首页默认）这里会同时拿到多个工程的迭代，
 * 而 `IT-001` 这种编号在每个工程里都可能有——只按迭代编号分组会把
 * 两个工程的题合成一个面板，摘要数字跟着错。工程名不参与键：
 * 名字可改，id 不会。
 */
export function groupKeyOf(task) {
  return projectOf(task) + KEY_SEP + iterationOf(task);
}

function summarize(group) {
  const tasks = group.tasks;
  return {
    tasks: tasks.length,
    active: tasks.filter(task => ACTIVE_STATUS.includes(String(task?.status || ""))).length,
    blocked: tasks.filter(task => String(task?.status || "") === "blocked").length,
    done: tasks.filter(task => String(task?.status || "") === "done").length,
    runs: group.runs.length,
    occupying: group.runs.filter(run => run?.occupies !== false).length,
    deliveries: group.deliveries.length,
    latestDelivery: group.deliveries
      .map(item => String(item?.delivered_at || ""))
      .filter(Boolean)
      .sort()
      .pop() || "",
  };
}

/**
 * 按迭代分组。
 *
 * 分组键是「工程 + 迭代」（见 groupKeyOf）：不选工程时首页会同时拿到多个工程的
 * 迭代，只按迭代编号分组会把两个工程同名迭代的题合成一个面板。
 *
 * 迭代顺序取任务在列表里首次出现的顺序（列表已按合入序位排过），
 * 不按 id 排序：id 是机器编号，排序结果对人没有意义。
 * 未归属迭代的任务在各自工程内单独成一组（迭代为空串），并显式标注——
 * 把它们混进某个迭代会让收尾清单漏项。
 */
export function iterationGroupsOf(state = {}) {
  const tasks = Array.isArray(state.tasks) ? state.tasks : [];
  const runs = Array.isArray(state.runs) ? state.runs : [];
  const deliveries = Array.isArray(state.deliveries) ? state.deliveries : [];

  const groups = new Map();
  const ensure = task => {
    const id = groupKeyOf(task);
    if (!groups.has(id)) {
      groups.set(id, {
        id,
        projectId: projectOf(task),
        projectName: String(task?.project_name ?? "").trim(),
        iterationId: iterationOf(task),
        tasks: [],
        runs: [],
        deliveries: [],
      });
    }
    return groups.get(id);
  };
  tasks.forEach(task => ensure(task).tasks.push(task));

  // 运行与交付没有迭代字段：只能经 task_instance 归到任务所在迭代。
  const groupOfInstance = new Map();
  for (const group of groups.values()) {
    for (const task of group.tasks) {
      const instance = taskInstanceOf(task);
      if (instance) groupOfInstance.set(instance, group.id);
    }
  }
  for (const run of runs) {
    const id = groupOfInstance.get(String(run?.task_instance || "").trim());
    if (id === undefined) continue;
    groups.get(id).runs.push(run);
  }
  for (const item of deliveries) {
    const id = groupOfInstance.get(String(item?.task_instance || "").trim());
    if (id === undefined) continue;
    groups.get(id).deliveries.push(item);
  }

  return [...groups.values()].map(group => ({ ...group, counts: summarize(group) }));
}

/**
 * 当前查看的迭代。
 *
 * 空串是「未归属迭代」这一组的合法 id，所以不能把「没选」和「选了未归属」混为一谈：
 * 只有调用方显式给过 selectedIteration 才按它走，否则退回第一个分组。
 * 选中值指向已不存在的迭代时同样退回第一个，不记忆失效的选择。
 */
export function selectedIterationOf(state = {}, groups = []) {
  const raw = state?.selectedIteration;
  const wanted = raw === undefined || raw === null ? null : String(raw).trim();
  if (wanted !== null && groups.some(group => group.id === wanted)) return wanted;
  return groups.length ? groups[0].id : "";
}

/**
 * 迭代标签。跨工程看时补上工程名：两个工程都有 IT-001，
 * 只写编号会让读者以为是一个迭代。
 */
function iterationLabel(lang, group, multiProject) {
  const base = group.iterationId ? group.iterationId : t("execution.unassigned", lang);
  if (!multiProject) return base;
  const name = group.projectName || group.projectId || "";
  return name ? base + " · " + name : base;
}

// ---------- 渲染 ----------

function iterationBar(lang, groups, selected, multiProject) {
  const items = groups.map(group => {
    const active = group.id === selected;
    const label = iterationLabel(lang, group, multiProject);
    return '<button type="button" class="execution-iteration' + (active ? " is-active" : "") +
      '" data-iteration="' + esc(group.id) + '" aria-pressed="' + (active ? "true" : "false") + '">' +
      '<span class="execution-iteration__id">' + esc(label) + "</span>" +
      '<span class="execution-iteration__count">' + esc(group.counts.tasks) + "</span></button>";
  }).join("");
  return '<div class="execution-iterations" role="group" aria-label="' + esc(t("execution.iterations", lang)) + '">' +
    items + "</div>";
}

function summaryCards(lang, group) {
  const c = group.counts;
  const card = (kind, value, note) =>
    '<div class="execution-card" data-summary="' + kind + '">' +
    '<span class="execution-card__label">' + esc(t("execution.summary." + kind, lang)) + "</span>" +
    '<span class="execution-card__value">' + esc(value) + "</span>" +
    '<span class="field-hint">' + esc(note) + "</span></div>";
  return '<div class="execution-summary">' +
    card("tasks", c.tasks, t("execution.summary.tasks.note", lang, { active: c.active, blocked: c.blocked, done: c.done })) +
    card("runs", c.runs, t("execution.summary.runs.note", lang, { occupying: c.occupying })) +
    card("deliveries", c.deliveries, t("execution.summary.deliveries.note", lang, { latest: c.latestDelivery || t("value.none", lang) })) +
    "</div>";
}

function table(lang, cols, rows, emptyText) {
  if (!rows.length) {
    return '<p class="field-hint" data-empty="1">' + esc(emptyText) + "</p>";
  }
  return '<div class="table-wrap"><table class="table"><thead><tr>' +
    cols.map(col => "<th>" + esc(t("col." + col, lang)) + "</th>").join("") +
    "</tr></thead><tbody>" + rows.join("") + "</tbody></table></div>";
}

/** 某一类的空文案：带上具体类别，避免三条「暂无记录」长得一模一样。 */
function emptyFor(lang, kindKey) {
  return t("execution.section.empty", lang, { kind: t(kindKey, lang) });
}

function tasksTable(lang, group) {
  const rows = group.tasks.map(task => {
    const display = taskDisplayOf(task);
    const status = String(task?.status || "") || t("value.none", lang);
    const cls = status === "blocked" ? "badge--blocked"
      : status === "done" ? "badge--done"
        : ACTIVE_STATUS.includes(status) ? "badge--in_progress" : "badge--self_check";
    return '<tr class="row-clickable" tabindex="0" data-task-id="' + esc(display) +
      '" data-project-id="' + esc(task?.project_id || "") +
      '" title="' + esc(t("execution.open", lang)) + '">' +
      '<td class="id">' + esc(display || t("value.none", lang)) + "</td>" +
      '<td class="name">' + esc(task?.title || t("value.none", lang)) + "</td>" +
      "<td>" + badge(status, cls) + "</td>" +
      "<td>" + esc(task?.owner || t("value.none", lang)) + "</td>" +
      "</tr>";
  });
  return table(lang, ["id", "title", "status", "owner"], rows, emptyFor(lang, "execution.section.tasks"));
}

function runsTable(lang, group) {
  const rows = group.runs.map(run => {
    const occupies = run?.occupies === false ? t("value.no", lang) : t("value.yes", lang);
    return "<tr>" +
      '<td class="id">' + esc(run?.run_id || t("value.none", lang)) + "</td>" +
      "<td>" + esc(run?.task_instance || t("value.none", lang)) + "</td>" +
      "<td>" + esc(run?.phase || t("value.none", lang)) + "</td>" +
      "<td>" + esc(occupies) + "</td>" +
      "</tr>";
  });
  return table(lang, ["run", "task", "phase", "occupies"], rows, emptyFor(lang, "execution.section.runs"));
}

function deliveriesTable(lang, group) {
  const rows = group.deliveries.map(item => "<tr>" +
    '<td class="id">' + esc(item?.delivery_id || t("value.none", lang)) + "</td>" +
    "<td>" + esc(item?.task_instance || t("value.none", lang)) + "</td>" +
    "<td>" + esc(item?.mode || t("value.none", lang)) + "</td>" +
    '<td class="note">' + esc(item?.delivered_at || t("value.none", lang)) + "</td>" +
    "</tr>");
  return table(lang, ["delivery", "task", "mode", "delivered_at"], rows, emptyFor(lang, "execution.section.deliveries"));
}

/**
 * 渲染执行视图。
 * state: {status, tasks, runs, deliveries, selectedIteration}
 * options: {lang}
 */
export function renderExecution(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const status = state.status || "loading";

  if (status === "loading") return '<p class="vnext-status">' + esc(t("execution.loading", lang)) + "</p>";
  if (status === "unauthenticated") return emptyState(lang, ["execution.unauthenticated", "execution.unauthenticated.hint"]);
  if (status === "expired") return emptyState(lang, ["execution.expired", "execution.expired.hint"]);
  if (status === "no_project") return emptyState(lang, ["execution.no_project", "execution.no_project.hint"], ' data-state="no_project"');
  if (status === "error") return emptyState(lang, ["execution.error", "execution.error.hint"], ' data-state="error"');

  const groups = iterationGroupsOf(state);
  if (!groups.length) {
    // 没有迭代是「还没建」，不是「没有任务」：空态要说下一步做什么。
    return emptyState(lang, ["execution.empty", "execution.empty.hint"], ' data-state="empty"');
  }

  const selected = selectedIterationOf(state, groups);
  const group = groups.find(item => item.id === selected) || groups[0];
  const unassigned = group.iterationId === "";
  // 跨工程看时才在标签上补工程名：单工程里那个后缀是噪声。
  const multiProject = new Set(groups.map(item => item.projectId)).size > 1;
  const heading = '<div class="execution-head">' +
    '<p class="field-hint" data-iteration-total="' + esc(groups.length) + '">' +
    esc(groups.length + " " + t("execution.iteration.count", lang) + " · " +
      (Array.isArray(state.tasks) ? state.tasks.length : 0) + " " + t("execution.task.count", lang)) + "</p></div>";

  const panel = '<section class="execution-panel" data-iteration-panel="' + esc(group.id) + '">' +
    '<h2 class="execution-panel__title">' + esc(iterationLabel(lang, group, multiProject)) + "</h2>" +
    (unassigned ? '<p class="field-hint" data-unassigned="1">' + esc(t("execution.unassigned.hint", lang)) + "</p>" : "") +
    summaryCards(lang, group) +
    '<h3 class="execution-panel__section">' + esc(t("execution.section.tasks", lang)) + "</h3>" + tasksTable(lang, group) +
    '<h3 class="execution-panel__section">' + esc(t("execution.section.runs", lang)) + "</h3>" + runsTable(lang, group) +
    '<h3 class="execution-panel__section">' + esc(t("execution.section.deliveries", lang)) + "</h3>" + deliveriesTable(lang, group) +
    "</section>";

  return heading + iterationBar(lang, groups, selected, multiProject) + panel;
}
