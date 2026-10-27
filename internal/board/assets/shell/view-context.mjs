// Workbench ViewContext 与数据状态适配（VN-704）。
//
// 视图渲染函数是纯函数：只读 `state` 与 `options`，返回 HTML 字符串。
// 这里负责把中枢快照/接口响应收敛成视图认识的状态，并把语言、路由、
// 导航与详情动作装进一份只读上下文，供挂载层注入。
//
// 诚实性规则（验收第 2 条）：
//   - 缺失、失败、过期一律显式标成 loading/error/expired，绝不落成空成功；
//   - pending / unknown 原样透传，不在这里改写成 done / pass / completed；
//   - 映射层不做业务推断，只做字段搬运与命名对齐。

/** 视图需要的十类数据：四类执行资源 + 四类观测事实 + 工程与策略。 */
export const DATA_KINDS = Object.freeze([
  "nodes", "tasks", "runs", "deliveries",
  "timeline", "activity", "sessions", "analytics",
  "projects", "settings",
]);

/**
 * 按工程物化的观测数据。
 *
 * 这四类事实的来源是投影，而投影按工程建。没有工程时「什么都没查到」与
 * 「没有查」是两件事：这里必须让调用方能区分，不能用空列表冒充空成功。
 */
export const PROJECT_SCOPED_KINDS = Object.freeze(["timeline", "activity", "sessions", "analytics"]);

/** 缺少工程上下文时的显式状态（既不是空成功，也不是错误）。 */
export const NO_PROJECT_STATE = Object.freeze({ status: "no_project", reason: "project_required" });

const EMPTY_STATE = Object.freeze({ status: "loading" });

/** 构造一份显式失败状态；`reason` 只用于解释，不参与成功判定。 */
export function errorState(reason = "error") {
  return { status: "error", reason };
}

/** 构造一份过期/待核实状态。 */
export function expiredState(reason = "stale") {
  return { status: "expired", reason };
}

/** 把 /api/board 快照映射成任务视图状态；快照缺失即失败，不伪造空列表。 */
export function tasksStateFromSnapshot(snapshot) {
  if (!snapshot || typeof snapshot !== "object") return errorState("missing_snapshot");
  const tasks = Array.isArray(snapshot.tasks) ? snapshot.tasks : null;
  if (!tasks) return errorState("invalid_snapshot");
  return {
    status: "ok",
    tasks,
    // 依赖判断必须用全量任务集，筛选后的行不足以判定前置状态。
    allTasks: Array.isArray(snapshot.allTasks) && snapshot.allTasks.length
      ? snapshot.allTasks
      : tasks,
    inProgressTasks: tasks.filter(task => ["claimed", "in_progress", "self_check"].includes(task.status)),
  };
}

/**
 * 读列表字段：契约用 `items`，视图用资源名（nodes/runs/deliveries）。
 * 两套命名并存时优先资源名，避免把分页外壳判成 invalid_payload。
 */
function listField(payload, name) {
  if (Array.isArray(payload[name])) return payload[name];
  if (Array.isArray(payload.items)) return payload.items;
  return null;
}

/** 节点列表响应 → 节点视图状态。 */
export function nodesStateFrom(payload) {
  if (!payload || typeof payload !== "object") return errorState("missing_payload");
  if (payload.unauthenticated === true || payload.status === 401) {
    return { status: "unauthenticated", reason: "unauthenticated" };
  }
  if (payload.unavailable) return errorState("unavailable");
  const nodes = listField(payload, "nodes");
  if (!nodes) return errorState("invalid_payload");
  return { status: "ok", nodes };
}

/** 运行列表响应 → 运行视图状态；`reconciliation` 等字段原样保留。 */
export function runsStateFrom(payload) {
  if (!payload || typeof payload !== "object") return errorState("missing_payload");
  if (payload.unauthenticated === true || payload.status === 401) {
    return { status: "unauthenticated", reason: "unauthenticated" };
  }
  if (payload.unavailable) return errorState("unavailable");
  const runs = listField(payload, "runs");
  if (!runs) return errorState("invalid_payload");
  return { status: "ok", runs };
}

/** 交付列表响应 → 交付视图状态；`pending` 与 `pass` 不在此合并。 */
export function deliveriesStateFrom(payload) {
  if (!payload || typeof payload !== "object") return errorState("missing_payload");
  if (payload.unauthenticated === true || payload.status === 401) {
    return { status: "unauthenticated", reason: "unauthenticated" };
  }
  if (payload.unavailable) return errorState("unavailable");
  const deliveries = listField(payload, "deliveries");
  if (!deliveries) return errorState("invalid_payload");
  return { status: "ok", deliveries };
}

/** 统一入口：视图 id → 对应状态；任务视图需要快照，其余需要列表载荷。 */
export function stateForKind(kind, payload, options = {}) {
  switch (kind) {
    case "tasks": return tasksStateFromSnapshot(payload);
    case "nodes": return nodesStateFrom(payload);
    case "runs": return runsStateFrom(payload);
    case "deliveries": return deliveriesStateFrom(payload);
    case "timeline": return timelineStateFrom(payload);
    case "activity": return activityStateFrom(payload);
    case "sessions": return sessionsStateFrom(payload);
    case "analytics": return analyticsStateFrom(payload);
    case "projects": return projectsStateFrom(payload);
    case "settings": return settingsStateFrom(payload, options);
    default: return errorState("unknown_kind");
  }
}

// ---------- 观测类适配（VN-705） ----------
//
// 五个观测读接口共用同一层响应外壳：`status` 说数据能不能直接用，
// `capability` 说这次查询依赖的能力有没有，`completeness` 说覆盖到哪。
// 因此适配也共用一层判定，只有取值部分各不相同。

/**
 * 观测响应的公共判定。
 *
 * 三种情况都**不能**落成空成功：
 *   1. 载荷缺失／端点为不可用 → error(unavailable)；
 *   2. `capability.supported === false` → 能力缺失，文案与空态同源；
 *   3. `status === "unsupported"` 或带 `unsupported` 条目 → 本次请求本身无法服务。
 */
function observabilityBase(payload) {
  if (!payload || typeof payload !== "object") return { error: errorState("missing_payload") };
  if (payload.unauthenticated === true || payload.status === 401) {
    return { error: { status: "unauthenticated", reason: "unauthenticated" } };
  }
  if (payload.unavailable === true) return { error: errorState("unavailable") };
  const cap = payload.capability;
  if (cap && cap.supported === false) {
    return { error: { status: "error", reason: "unavailable", capability: cap.reason || "capability_missing" } };
  }
  if (payload.status === "unsupported") return { error: errorState("unavailable") };
  if (Array.isArray(payload.unsupported) && payload.unsupported.length) {
    return { error: { status: "error", reason: "unavailable", unsupported: payload.unsupported } };
  }
  return {
    // ready 之外（partial／stale／unknown）一律交视图自己打「部分未知」横幅：
    // 已观测部分仍然可信，不能因为覆盖不全就把整页判成失败。
    status: payload.status === "unknown" ? "unknown" : "ready",
    completeness: typeof payload.completeness === "string" ? payload.completeness : "",
  };
}

/** 回显窗口；缺字段保持 null，不用「现在」冒充服务端窗口。 */
function windowOf(raw) {
  if (!raw || typeof raw !== "object") return null;
  const out = {};
  ["start", "end", "timezone"].forEach(key => {
    if (typeof raw[key] === "string" && raw[key]) out[key] = raw[key];
  });
  const hours = Number(raw.hours);
  if (Number.isFinite(hours) && hours > 0) out.hours = hours;
  return Object.keys(out).length ? out : null;
}

/** 时间轴游标：只搬运水位里的真值，缺测留 null 让视图写「—」。 */
function timelineCursors(payload, completeness) {
  const wm = payload.watermark;
  if (!wm || typeof wm !== "object") return null;
  const cursor = Number.isFinite(Number(wm.cursor)) ? Number(wm.cursor) : null;
  const completeBefore = Number.isFinite(Number(wm.complete_before)) ? Number(wm.complete_before) : null;
  return {
    state_cursor: cursor,
    projection_cursor: completeBefore,
    completeness: completeness || "",
    received_at: typeof wm.received_at === "string" ? wm.received_at : "",
    gap_count: Array.isArray(wm.gaps) ? wm.gaps.length : 0,
  };
}

/** 时间轴响应 → 时间轴视图状态。 */
export function timelineStateFrom(payload) {
  const base = observabilityBase(payload);
  if (base.error) return base.error;
  const intervals = Array.isArray(payload.items) ? payload.items : null;
  if (!intervals) return errorState("invalid_payload");
  return {
    status: base.status,
    completeness: base.completeness,
    intervals,
    window: windowOf(payload.window),
    cursors: timelineCursors(payload, base.completeness),
  };
}

/** 活动热图响应 → 活动视图状态。 */
export function activityStateFrom(payload) {
  const base = observabilityBase(payload);
  if (base.error) return base.error;
  const hours = Array.isArray(payload.hours) ? payload.hours : null;
  if (!hours) return errorState("invalid_payload");
  const win = windowOf(payload.window);
  return {
    status: base.status,
    completeness: base.completeness,
    hours,
    day: typeof payload.day === "string" ? payload.day : "",
    timezone: win && win.timezone ? win.timezone : "",
    presentHours: Number.isFinite(Number(payload.present_hours)) ? Number(payload.present_hours) : null,
  };
}

/** 会话响应 → 会话视图状态；未归属会话原样保留，不在此强行归类。 */
export function sessionsStateFrom(payload) {
  const base = observabilityBase(payload);
  if (base.error) return base.error;
  const sessions = Array.isArray(payload.items) ? payload.items : null;
  if (!sessions) return errorState("invalid_payload");
  return { status: base.status, completeness: base.completeness, sessions };
}

/** 统计响应 → 统计视图状态；报表缺失即失败，不构造全零报表。 */
export function analyticsStateFrom(payload) {
  const base = observabilityBase(payload);
  if (base.error) return base.error;
  const report = payload.report;
  if (!report || typeof report !== "object") return errorState("invalid_payload");
  return { status: base.status, completeness: base.completeness, report };
}

/**
 * /api/board 快照 → 工程清单状态。
 *
 * 对齐两处命名：快照里叫 `max_parallel`，策略里叫 `max_parallel_reference`，
 * 指的是同一个「建议并行数」。除此之外只搬字段，不补默认值。
 *
 * `policyKnown` 记录快照到底有没有带策略：看板快照本身不含策略字段，
 * 这时页面必须说明「数值未知」，而不是让每一行都显示成「关闭」。
 */
export function projectsStateFrom(payload) {
  if (!payload || typeof payload !== "object") return errorState("missing_snapshot");
  if (payload.unavailable === true) return errorState("unavailable");
  const raw = Array.isArray(payload.projects) ? payload.projects : null;
  if (!raw) return errorState("invalid_snapshot");
  const projects = raw.map(project => {
    if (!project || typeof project !== "object") return project;
    const out = { ...project };
    if (out.max_parallel_reference === undefined && out.max_parallel !== undefined) {
      out.max_parallel_reference = out.max_parallel;
    }
    return out;
  });
  // 「策略是否已知」只认自动执行相关字段：`max_parallel` 是建议并行数，
  // 它出现不代表中枢说过自动模式开还是关。把参考值当策略，页面就会
  // 一边显示参考值、一边把自动模式读成「关闭」。
  const policyKnown = projects.some(project => project && typeof project === "object" && (
    project.policy != null ||
    project.auto_enabled !== undefined ||
    project.managed_execution_limit !== undefined
  ));
  return { status: "ok", projects, policyKnown };
}

/**
 * /api/runtime → 工程设置状态。
 *
 * 策略字段只认服务端回显：缺字段保持 null（视图写「—」），不在这里补默认值——
 * 补了就等于替服务端宣布「上限是 1」，而真实配置可能是别的。
 *
 * `normalizePolicy` 由挂载层注入（来自设置视图自身）：归一化规则只有一份，
 * 这一层只负责把中枢响应搬到它认识的名字上（`enabled` → `auto_enabled`，
 * `config_version` → `revision`），不重新判断任何一个值。
 */
export function settingsStateFrom(payload, options = {}) {
  if (!payload || typeof payload !== "object") return errorState("missing_payload");
  if (payload.unavailable === true) return errorState("unavailable");
  const raw = payload.policy || payload.auto_execution || null;
  if (!raw || typeof raw !== "object") return errorState("invalid_payload");
  const aligned = { ...raw };
  if (aligned.auto_enabled === undefined && aligned.enabled !== undefined) {
    aligned.auto_enabled = aligned.enabled;
  }
  if (aligned.revision === undefined && aligned.config_version !== undefined) {
    aligned.revision = aligned.config_version;
  }
  const normalize = typeof options.normalizePolicy === "function"
    ? options.normalizePolicy
    : value => value;
  const state = {
    status: "ready",
    policy: normalize(aligned),
    readOnly: options.readOnly === true,
    // 平台是否支持受管执行由服务端说；没说不猜成「支持」。
    platformSupported: typeof aligned.platform_supported === "boolean" ? aligned.platform_supported : undefined,
    configApplied: aligned.config_applied === false ? false : undefined,
  };
  if (options.readOnly === true) state.readOnlyReason = options.readOnlyReason || "static_export";
  return state;
}

/** 视图 id → 是否需要先选定工程。 */
export function needsProject(kind) {
  return PROJECT_SCOPED_KINDS.indexOf(kind) >= 0;
}

/** 详情候选字段：不同资源的稳定 id 字段不同。 */
const DETAIL_KEYS = Object.freeze({
  nodes: ["id", "node_id"],
  tasks: ["instance_id", "id"],
  runs: ["run_id", "id"],
  deliveries: ["delivery_id", "id"],
});

/** 从列表载荷里按 id 找详情；找不到返回 null（由调用方显示缺失，不编造）。 */
export function findDetail(kind, payload, id) {
  if (!payload || !id) return null;
  const list = Array.isArray(payload.nodes) ? payload.nodes
    : Array.isArray(payload.runs) ? payload.runs
      : Array.isArray(payload.deliveries) ? payload.deliveries
        : Array.isArray(payload.tasks) ? payload.tasks
          : Array.isArray(payload.allTasks) ? payload.allTasks
            : Array.isArray(payload.items) ? payload.items
              : [];
  const keys = DETAIL_KEYS[kind] || ["id"];
  const wanted = String(id);
  for (const item of list) {
    if (!item) continue;
    if (keys.some(key => item[key] != null && String(item[key]) === wanted)) return item;
  }
  return null;
}

/**
 * 构造 ViewContext：视图只拿这些字段，不直接摸 window / 路由。
 * @returns {Readonly<Object>}
 */
export function createViewContext({
  view,
  lang = "en",
  t,
  route = {},
  navigate = () => {},
  openDetail = () => {},
  closeDetail = () => {},
  data = {},
  registry = null,
  readOnly = false,
} = {}) {
  return Object.freeze({
    view: view && view.id ? view.id : "",
    legacy: Boolean(view && view.legacy),
    lang,
    t: typeof t === "function" ? t : (key => key),
    route: Object.freeze({ ...route }),
    filters: Object.freeze({
      project: route.project || "",
      agent: route.agent || "",
      status: route.status || "",
      search: route.search || "",
    }),
    detailId: route.detail || "",
    data,
    navigate,
    openDetail,
    closeDetail,
    views: registry ? registry.ids.slice() : [],
    // 只读上下文（静态导出／无写入口）：视图不该据此隐藏数据，只能据此禁写。
    readOnly: readOnly === true,
    // 该视图是否要求先选定工程，供视图选择「先选工程」而不是空列表。
    requiresProject: Boolean(view && view.requiresProject),
  });
}

/** 空状态常量，便于 shell 首帧渲染。 */
export const LOADING_STATE = EMPTY_STATE;
