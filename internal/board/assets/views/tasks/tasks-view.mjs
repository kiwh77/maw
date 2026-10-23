// 任务创建与指定派发视图（vnext / UI · VN-722）。
//
// 目标：把「这道题能不能现在派给某个节点」讲清楚，而不是派了再说。
//
// 依据：
//   docs/contracts/vnext/task/task-instance-contract-v1.md
//     §1 instance_id 与 display_id 是两个命名空间（E111）；
//     §5 依赖解析：legacy 零命中 E150、已退役 E151、命中多个 E153，
//        「前置已退役」是结构性死锁，必须与「还没做完」分开报；
//     §2 退役后版本变只读快照（E142），不得再出新版本；
//   docs/contracts/vnext/http/http-api-contract-v1.md §3/§5：派发是异步动作，
//     202 只算受理；写动作必须带 revision 前置。
//
// 三条硬规则：
//   1. 202／running 不是完成。派发后进入操作状态，只有 succeeded 才改结论。
//   2. 版本缺失就不能提交（revision_required），不默认「以最新为准」。
//   3. 阻止原因必须分类：前置未完成、前置已退役（死锁）、依赖歧义、范围冲突、
//      节点未就绪、节点未指定——六种不能混成一句「等前置」。
//
// 本视图只做展示与校验，不实现调度算法、不写导航、不新增公共层：
// 组件类名一律来自 assets/components（table/badge/form/empty/drawer）。

export const DEPENDENCY_REASONS = Object.freeze({
  pending: "dependency_pending",
  retired: "dependency_retired",
  ambiguous: "dependency_ambiguous",
  missing: "dependency_missing",
});

export const DISPATCH_PHASES = Object.freeze(["idle", "submitting", "pending", "succeeded", "failed"]);

const I18N = {
  zh: {
    "tasks.title": "任务",
    "tasks.subtitle": "新建或编辑任务，然后指定节点派发；派发只到受理，完成要看操作结果。",
    "tasks.empty": "当前筛选下没有任务。",
    "tasks.empty.hint": "换个状态看看，或新建一道题。",
    "tasks.error": "任务加载失败。",
    "tasks.error.hint": "确认本机中枢在运行，然后重试。",
    "tasks.expired": "任务数据已过期。",
    "tasks.expired.hint": "刷新后重新读取，不沿用上次结论。",
    "tasks.loading": "正在读取任务…",
    "tasks.unauthenticated": "未登录，无法读取或修改任务。",
    "tasks.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "tasks.count": "道任务",
    "tasks.dispatchable": "可派发",
    "col.title": "标题",
    "col.id": "编号",
    "col.priority": "优先级",
    "col.merge_order": "合入序位",
    "col.deps": "依赖",
    "col.scope": "写范围",
    "col.node": "节点",
    "col.state": "状态",
    "col.dispatch": "派发",
    "state.unknown": "未知",
    "state.retired": "已退役",
    "form.create": "新建任务",
    "form.edit": "编辑任务",
    "form.title": "标题",
    "form.priority": "优先级",
    "form.merge_order": "合入序位",
    "form.deps": "依赖（每行一个，ti_ 或显示编号）",
    "form.write_scope": "可写范围（每行一个路径）",
    "form.read_scope": "只读范围",
    "form.acceptance": "验收标准",
    "form.node": "指定节点",
    "form.node.none": "不指定（由调度决定）",
    "form.submit": "保存",
    "form.dispatch": "派发到该节点",
    "form.revision": "版本",
    "form.revision.missing": "缺少版本号，不能保存：需要先读取任务当前版本。",
    "form.readonly": "只读：该任务已退役，不能再改。",
    "err.title_required": "标题不能为空",
    "err.priority_invalid": "优先级必须是 0–100 的整数",
    "err.merge_order_invalid": "合入序位必须是 0–100 的整数",
    "err.dep_self": "不能依赖自己",
    "err.dep_invalid": "依赖必须形如 ti_xxx 或 VN-001",
    "err.scope_empty": "写范围不能为空",
    "err.scope_invalid": "写范围必须是仓库内相对路径，不能以 / 开头或包含 ..",
    "err.dep_cycle": "依赖成环：{cycle}",
    "dispatch.revision_required": "缺少版本前置，不能派发",
    "dispatch.dependency_pending": "前置未完成：{ids}",
    "dispatch.dependency_retired": "前置已退役，依赖永久不可满足（结构性死锁）：{ids}",
    "dispatch.dependency_ambiguous": "依赖命中多个实例，需改用 ti_ 形式消歧：{ids}",
    "dispatch.dependency_missing": "依赖找不到：{ids}",
    "dispatch.scope_conflict": "写范围与进行中任务重叠：{ids}",
    "dispatch.node_not_ready": "节点不可接任务：{reason}",
    "dispatch.node_unspecified": "指定派发需要先选节点",
    "dispatch.ok": "可以派发",
    "verdict.dispatchable": "可派发",
    "verdict.blocked": "不可派发",
    "op.idle": "尚未派发",
    "op.submitting": "正在提交派发…",
    "op.pending": "已受理，操作进行中（不等同完成）",
    "op.pending.retry": "约 {ms}ms 后重试",
    "op.succeeded": "派发成功",
    "op.failed": "派发失败：{code}",
    "op.result": "操作结果",
  },
  en: {
    "tasks.title": "Tasks",
    "tasks.subtitle": "Create or edit a task, then dispatch it to a node. Dispatch only reaches acceptance; completion needs the operation result.",
    "tasks.empty": "No tasks under current filters.",
    "tasks.empty.hint": "Try another state, or create one.",
    "tasks.error": "Failed to load tasks.",
    "tasks.error.hint": "Check that the local hub is running, then retry.",
    "tasks.expired": "Task data is out of date.",
    "tasks.expired.hint": "Refresh to re-read; previous conclusions are not reused.",
    "tasks.loading": "Loading tasks…",
    "tasks.unauthenticated": "Not signed in, cannot read or change tasks.",
    "tasks.unauthenticated.hint": "Use a local session or token and retry.",
    "tasks.count": "tasks",
    "tasks.dispatchable": "dispatchable",
    "col.title": "Title",
    "col.id": "ID",
    "col.priority": "Priority",
    "col.merge_order": "Merge order",
    "col.deps": "Deps",
    "col.scope": "Write scope",
    "col.node": "Node",
    "col.state": "State",
    "col.dispatch": "Dispatch",
    "state.unknown": "Unknown",
    "state.retired": "Retired",
    "form.create": "New task",
    "form.edit": "Edit task",
    "form.title": "Title",
    "form.priority": "Priority",
    "form.merge_order": "Merge order",
    "form.deps": "Dependencies (one per line, ti_ or display id)",
    "form.write_scope": "Write scope (one path per line)",
    "form.read_scope": "Read scope",
    "form.acceptance": "Acceptance",
    "form.node": "Assign node",
    "form.node.none": "Unassigned (scheduler decides)",
    "form.submit": "Save",
    "form.dispatch": "Dispatch to this node",
    "form.revision": "Revision",
    "form.revision.missing": "Missing revision; read the current revision before saving.",
    "form.readonly": "Read-only: this task is retired and cannot change.",
    "err.title_required": "Title is required",
    "err.priority_invalid": "Priority must be an integer 0–100",
    "err.merge_order_invalid": "Merge order must be an integer 0–100",
    "err.dep_self": "A task cannot depend on itself",
    "err.dep_invalid": "Dependency must look like ti_xxx or VN-001",
    "err.scope_empty": "Write scope is required",
    "err.scope_invalid": "Write scope must be a relative repo path (no leading / and no ..)",
    "err.dep_cycle": "Dependency cycle: {cycle}",
    "dispatch.revision_required": "Missing revision precondition; cannot dispatch",
    "dispatch.dependency_pending": "Dependencies unfinished: {ids}",
    "dispatch.dependency_retired": "Dependencies retired, permanently unsatisfiable (structural deadlock): {ids}",
    "dispatch.dependency_ambiguous": "Dependency matches several instances; use the ti_ form: {ids}",
    "dispatch.dependency_missing": "Dependencies not found: {ids}",
    "dispatch.scope_conflict": "Write scope overlaps in-progress tasks: {ids}",
    "dispatch.node_not_ready": "Node cannot take work: {reason}",
    "dispatch.node_unspecified": "Assign a node before dispatching",
    "dispatch.ok": "Ready to dispatch",
    "verdict.dispatchable": "Dispatchable",
    "verdict.blocked": "Blocked",
    "op.idle": "Not dispatched yet",
    "op.submitting": "Submitting dispatch…",
    "op.pending": "Accepted, operation still running (not completion)",
    "op.pending.retry": "Retry in about {ms}ms",
    "op.succeeded": "Dispatch succeeded",
    "op.failed": "Dispatch failed: {code}",
    "op.result": "Operation result",
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
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[ch]));
}

const INSTANCE_RE = /^ti_[A-Za-z0-9._:-]+$/;
const DISPLAY_RE = /^VN-\d{3,}$/;

export function isInstanceId(value) {
  return INSTANCE_RE.test(String(value || ""));
}

export function isDisplayId(value) {
  return DISPLAY_RE.test(String(value || ""));
}

/** 排序：priority → merge_order → 编号。界面显示的优先级取任务实际值，不取排序位置。 */
export function sortTasks(tasks = []) {
  return [...tasks].sort((a, b) => {
    const pa = Number.isFinite(a?.priority) ? a.priority : Number.MAX_SAFE_INTEGER;
    const pb = Number.isFinite(b?.priority) ? b.priority : Number.MAX_SAFE_INTEGER;
    if (pa !== pb) return pa - pb;
    const ma = Number.isFinite(a?.merge_order) ? a.merge_order : Number.MAX_SAFE_INTEGER;
    const mb = Number.isFinite(b?.merge_order) ? b.merge_order : Number.MAX_SAFE_INTEGER;
    if (ma !== mb) return ma - mb;
    return String(a?.display_id || a?.id || "").localeCompare(String(b?.display_id || b?.id || ""));
  });
}

/**
 * 解析单个依赖。返回 { status: 'ok' | pending | retired | ambiguous | missing, ... }。
 * 契约 §5：退役是对「结构性死锁」的判定，不能混进 pending。
 */
export function resolveDependency(dep, context = {}) {
  const tasks = Array.isArray(context.tasks) ? context.tasks : [];
  const raw = String(dep || "").trim();
  if (!raw) return { status: "missing", dep: raw };

  if (isInstanceId(raw)) {
    const hit = tasks.find(task => task.instance_id === raw || task.id === raw);
    if (!hit) return { status: "missing", dep: raw };
    if (hit.state === "retired") return { status: "retired", dep: raw, id: hit.display_id };
    if (hit.status !== "done") return { status: "pending", dep: raw, id: hit.display_id, task_status: hit.status };
    return { status: "ok", dep: raw, id: hit.display_id };
  }

  if (isDisplayId(raw)) {
    const hits = tasks.filter(task => task.display_id === raw);
    if (hits.length === 0) return { status: "missing", dep: raw };
    if (hits.length > 1) return { status: "ambiguous", dep: raw, count: hits.length };
    const hit = hits[0];
    if (hit.state === "retired") return { status: "retired", dep: raw, id: raw };
    if (hit.status !== "done") return { status: "pending", dep: raw, id: raw, task_status: hit.status };
    return { status: "ok", dep: raw, id: raw };
  }

  return { status: "missing", dep: raw, malformed: true };
}

/** 依赖成环检测（只在草稿内部做，跨任务成环由服务端判定）。 */
export function findCycle(draftId, deps = [], allDeps = {}) {
  const seen = new Set();
  const stack = [...deps.map(String)];
  while (stack.length) {
    const next = stack.pop();
    if (next === draftId) return [draftId, ...deps.map(String), draftId];
    if (seen.has(next)) continue;
    seen.add(next);
    for (const child of allDeps[next] || []) stack.push(String(child));
  }
  return null;
}

/**
 * 草稿校验。返回 { ok, errors: [{field, code}] }，错误码同时可作 i18n key（err.*）。
 */
export function validateDraft(draft = {}, context = {}) {
  const errors = [];
  const push = (field, code, extra = {}) => errors.push({ field, code, ...extra });

  const title = String(draft.title || "").trim();
  if (!title) push("title", "title_required");

  if (!Number.isInteger(draft.priority) || draft.priority < 0 || draft.priority > 100) {
    push("priority", "priority_invalid");
  }
  if (draft.merge_order !== undefined && draft.merge_order !== null &&
      (!Number.isInteger(draft.merge_order) || draft.merge_order < 0 || draft.merge_order > 100)) {
    push("merge_order", "merge_order_invalid");
  }

  const selfId = draft.instance_id || draft.id || null;
  const deps = Array.isArray(draft.deps) ? draft.deps : [];
  for (const dep of deps) {
    const raw = String(dep || "").trim();
    if (!raw) continue;
    if (selfId && (raw === selfId || raw === draft.display_id)) {
      push("deps", "dep_self", { dep: raw });
      continue;
    }
    if (!isInstanceId(raw) && !isDisplayId(raw)) {
      push("deps", "dep_invalid", { dep: raw });
    }
  }
  // 自依赖已由 dep_self 报出，成环检测跳过自身，避免同一问题两个码。
  const cycleDeps = deps.filter(dep => String(dep || "").trim() !== selfId &&
    String(dep || "").trim() !== draft.display_id);
  const cycle = findCycle(selfId, cycleDeps, context.allDeps || {});
  if (cycle) push("deps", "dep_cycle", { cycle: cycle.join(" → ") });

  const scope = Array.isArray(draft.write_scope) ? draft.write_scope : [];
  const cleaned = scope.map(s => String(s || "").trim()).filter(Boolean);
  if (!cleaned.length) {
    push("write_scope", "scope_empty");
  } else {
    for (const path of cleaned) {
      if (path.startsWith("/") || path.split("/").includes("..")) {
        push("write_scope", "scope_invalid", { path });
      }
    }
  }

  return { ok: errors.length === 0, errors };
}

/**
 * 派发前置判定。六种阻止原因分开报，不合并成「等前置」。
 * @param {object} task 目标任务（含 instance_id / display_id / write_scope / status / state）
 * @param {object} context { revision, tasks, inProgressTasks, node: {verdict, reasons}, nodeRequired }
 */
export function evaluateDispatch(task, context = {}) {
  const reasons = [];
  const push = (code, detail = {}) => reasons.push({ code, ...detail });

  if (context.revision === undefined || context.revision === null) {
    push("dispatch.revision_required");
  }
  if (task?.state === "retired") {
    push("dispatch.dependency_retired", { ids: task.display_id || task.instance_id });
  }

  const byStatus = { pending: [], retired: [], ambiguous: [], missing: [] };
  for (const dep of task?.deps || []) {
    const resolved = resolveDependency(dep, { tasks: context.tasks || [] });
    if (resolved.status === "ok") continue;
    if (resolved.status === "pending") byStatus.pending.push(resolved.id || resolved.dep);
    else if (resolved.status === "retired") byStatus.retired.push(resolved.id || resolved.dep);
    else if (resolved.status === "ambiguous") byStatus.ambiguous.push(resolved.dep);
    else byStatus.missing.push(resolved.dep);
  }
  if (byStatus.pending.length) push("dispatch.dependency_pending", { ids: byStatus.pending.join(", ") });
  if (byStatus.retired.length) push("dispatch.dependency_retired", { ids: byStatus.retired.join(", ") });
  if (byStatus.ambiguous.length) push("dispatch.dependency_ambiguous", { ids: byStatus.ambiguous.join(", ") });
  if (byStatus.missing.length) push("dispatch.dependency_missing", { ids: byStatus.missing.join(", ") });

  const conflicts = findScopeConflicts(task, context.inProgressTasks || []);
  if (conflicts.length) push("dispatch.scope_conflict", { ids: conflicts.join(", ") });

  if (context.nodeRequired && !context.node) {
    push("dispatch.node_unspecified");
  } else if (context.node && context.node.verdict !== "executable") {
    const reason = Array.isArray(context.node.reasons) && context.node.reasons.length
      ? context.node.reasons[0]
      : "unknown";
    push("dispatch.node_not_ready", { reason });
  }

  return { dispatchable: reasons.length === 0, reasons };
}

/** 写范围与其他「进行中」任务重叠（本视图只报冲突，不改成败）。 */
function findScopeConflicts(task, inProgress) {
  const mine = new Set((task?.write_scope || []).map(String));
  const hits = [];
  for (const other of inProgress) {
    if (!other || other.id === task?.id || other.instance_id === task?.instance_id) continue;
    const overlap = (other.write_scope || []).some(path => mine.has(String(path)));
    if (overlap) hits.push(other.display_id || other.instance_id || other.id);
  }
  return hits;
}

// ---------- 渲染 ----------

function badge(text, cls) {
  return '<span class="badge ' + cls + '">' + esc(text) + "</span>";
}

function emptyState(lang, keys) {
  return '<div class="empty-state"><p class="empty-state__text">' + esc(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + esc(t(keys[1], lang)) + "</p></div>";
}

export function renderTaskList(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";

  if (state.status === "loading") return '<p class="nodes-loading">' + esc(t("tasks.loading", lang)) + "</p>";
  if (state.status === "unauthenticated") return emptyState(lang, ["tasks.unauthenticated", "tasks.unauthenticated.hint"]);
  if (state.status === "error") return emptyState(lang, ["tasks.error", "tasks.error.hint"]);
  if (state.status === "expired") return emptyState(lang, ["tasks.expired", "tasks.expired.hint"]);

  const tasks = sortTasks(Array.isArray(state.tasks) ? state.tasks : []);
  const header = '<header class="nodes-head"><h1>' + esc(t("tasks.title", lang)) + "</h1>" +
    '<p class="field-hint">' + esc(t("tasks.subtitle", lang)) + "</p></header>";

  if (!tasks.length) return header + emptyState(lang, ["tasks.empty", "tasks.empty.hint"]);

  // 依赖解析必须用全量任务集：筛选后的可见行不足以判断前置是否已完成／已退役。
  const universe = Array.isArray(state.allTasks) && state.allTasks.length ? state.allTasks : tasks;

  const rows = tasks.map(task => {
    const verdict = evaluateDispatch(task, {
      revision: task.revision,
      tasks: universe,
      inProgressTasks: state.inProgressTasks || [],
      node: state.nodeFor ? state.nodeFor(task) : undefined,
      nodeRequired: task.node_id ? true : false,
    });
    const reason = verdict.dispatchable
      ? t("dispatch.ok", lang)
      : t(verdict.reasons[0].code, lang, verdict.reasons[0]);

    return "<tr>" +
      '<td class="id">' + esc(task.display_id || task.instance_id || task.id) + "</td>" +
      '<td class="name">' + esc(task.title || "—") + "</td>" +
      '<td class="num">' + esc(task.priority ?? "—") + "</td>" +
      '<td class="num">' + esc(task.merge_order ?? "—") + "</td>" +
      "<td>" + esc((task.deps || []).join(", ") || "—") + "</td>" +
      '<td class="path">' + esc((task.write_scope || []).join(", ") || "—") + "</td>" +
      "<td>" + esc(task.node_id || "—") + "</td>" +
      "<td>" + badge(stateLabel(task, lang), task.state === "retired" ? "badge--self_check" : "badge--in_progress") + "</td>" +
      "<td>" + badge(
        verdict.dispatchable ? t("verdict.dispatchable", lang) : t("verdict.blocked", lang),
        verdict.dispatchable ? "badge--done" : "badge--blocked"
      ) + '<span class="field-hint"> ' + esc(reason) + "</span></td>" +
      "</tr>";
  }).join("");

  const dispatchableCount = tasks.filter(task =>
    evaluateDispatch(task, {
      revision: task.revision,
      tasks: universe,
      inProgressTasks: state.inProgressTasks || [],
      node: state.nodeFor ? state.nodeFor(task) : undefined,
      nodeRequired: task.node_id ? true : false,
    }).dispatchable
  ).length;

  const cols = ["id", "title", "priority", "merge_order", "deps", "scope", "node", "state", "dispatch"];

  return header +
    '<p class="field-hint">' + esc(tasks.length + " " + t("tasks.count", lang)) + " · " +
    esc(dispatchableCount + " " + t("tasks.dispatchable", lang)) + "</p>" +
    '<div class="table-wrap"><table class="table"><thead><tr>' +
    cols.map(col => "<th>" + esc(t("col." + col, lang)) + "</th>").join("") +
    "</tr></thead><tbody>" + rows + "</tbody></table></div>";
}

function stateLabel(task, lang) {
  if (task?.state === "retired") return t("state.retired", lang);
  const status = task?.status;
  if (!status) return t("state.unknown", lang);
  return status;
}

/**
 * 表单。编辑态必须带 revision；退役任务整表只读。
 * @param {object} state { mode, draft, errors, revision, tasks, nodes }
 */
export function renderTaskForm(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const mode = state.mode === "edit" ? "edit" : "create";
  const draft = state.draft || {};
  const errors = Array.isArray(state.errors) ? state.errors : [];
  const readonly = draft.state === "retired";
  const revisionMissing = mode === "edit" && (state.revision === undefined || state.revision === null);

  const field = (name, labelKey, control, extra = "") => {
    const err = errors.find(e => e.field === name);
    const errId = "err-" + name;
    return '<div class="field">' +
      '<label for="f-' + name + '">' + esc(t(labelKey, lang)) + "</label>" +
      control.replace("{{aria}}", err ? ' aria-invalid="true" aria-describedby="' + errId + '"' : "") +
      (err ? '<p class="field-error" id="' + errId + '">' + esc(t("err." + err.code, lang, err)) + "</p>" : "") +
      extra + "</div>";
  };

  const text = (name, value) =>
    '<input class="input" id="f-' + name + '" name="' + name + '" value="' + esc(value ?? "") + '"{{aria}}' +
    (readonly ? " readonly" : "") + ">";

  const num = (name, value) =>
    '<input class="input" id="f-' + name + '" name="' + name + '" type="number" min="0" max="100" value="' +
    esc(value ?? "") + '"{{aria}}' + (readonly ? " readonly" : "") + ">";

  const area = (name, values) =>
    '<textarea class="textarea" id="f-' + name + '" name="' + name + '" rows="3"{{aria}}' +
    (readonly ? " readonly" : "") + ">" + esc((values || []).join("\n")) + "</textarea>";

  const nodeOptions = (state.nodes || []).map(node =>
    '<option value="' + esc(node.id) + '"' + (draft.node_id === node.id ? " selected" : "") + ">" +
    esc(node.name || node.id) + "</option>"
  ).join("");

  const dispatchBlocked = revisionMissing || readonly;
  const submitDisabled = dispatchBlocked || state.pending === true;

  return '<form class="task-form" id="task-form" novalidate>' +
    "<h2>" + esc(t(mode === "edit" ? "form.edit" : "form.create", lang)) + "</h2>" +
    (revisionMissing ? '<p class="field-error">' + esc(t("form.revision.missing", lang)) + "</p>" : "") +
    (readonly ? '<p class="field-hint">' + esc(t("form.readonly", lang)) + "</p>" : "") +
    (mode === "edit"
      ? '<p class="field-hint">' + esc(t("form.revision", lang)) + "：" +
        esc(state.revision ?? "—") + "</p>"
      : "") +
    field("title", "form.title", text("title", draft.title)) +
    field("priority", "form.priority", num("priority", draft.priority)) +
    field("merge_order", "form.merge_order", num("merge_order", draft.merge_order)) +
    field("deps", "form.deps", area("deps", draft.deps)) +
    field("write_scope", "form.write_scope", area("write_scope", draft.write_scope)) +
    field("read_scope", "form.read_scope", area("read_scope", draft.read_scope)) +
    field("acceptance", "form.acceptance", area("acceptance", draft.acceptance)) +
    '<div class="field"><label for="f-node">' + esc(t("form.node", lang)) + "</label>" +
    '<select class="select" id="f-node" name="node_id"' + (readonly ? " disabled" : "") + ">" +
    '<option value="">' + esc(t("form.node.none", lang)) + "</option>" + nodeOptions + "</select></div>" +
    '<div class="field">' +
    '<button type="submit" class="button button--primary" id="task-submit"' +
    (submitDisabled ? " disabled" : "") + ">" + esc(t("form.submit", lang)) + "</button>" +
    (draft.node_id
      ? '<button type="button" class="button" id="task-dispatch"' + (dispatchBlocked ? " disabled" : "") + ">" +
        esc(t("form.dispatch", lang)) + "</button>"
      : "") +
    "</div></form>";
}

/**
 * 派发状态。202 与 running 都停在 pending，只有 succeeded 才是完成。
 * @param {object} state { phase, operation, error, result }
 */
export function renderDispatchState(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const phase = DISPATCH_PHASES.includes(state.phase) ? state.phase : "idle";
  const operation = state.operation || null;

  if (phase === "idle") return '<p class="field-hint">' + esc(t("op.idle", lang)) + "</p>";
  if (phase === "submitting") return '<p class="field-hint">' + esc(t("op.submitting", lang)) + "</p>";

  if (phase === "pending") {
    const retry = operation?.retry_after_ms;
    return '<div class="field-hint" data-phase="pending">' +
      badge(t("op.pending", lang), "badge--warn") +
      '<span class="source-mark"> ' + esc(operation?.status || "queued") + "</span>" +
      (retry ? '<span class="field-hint"> · ' + esc(t("op.pending.retry", lang, { ms: retry })) + "</span>" : "") +
      "</div>";
  }

  if (phase === "failed") {
    const code = state.error?.code || "unknown_error";
    return '<div data-phase="failed">' + badge(t("op.failed", lang, { code }), "badge--blocked") +
      (state.error?.message ? '<p class="field-hint">' + esc(state.error.message) + "</p>" : "") + "</div>";
  }

  return '<div data-phase="succeeded">' + badge(t("op.succeeded", lang), "badge--done") +
    (state.result
      ? '<p class="field-hint">' + esc(t("op.result", lang)) + "：" +
        '<code>' + esc(JSON.stringify(state.result)) + "</code></p>"
      : "") + "</div>";
}
