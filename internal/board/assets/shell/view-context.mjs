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

/** 视图需要的四类数据。 */
export const DATA_KINDS = Object.freeze(["nodes", "tasks", "runs", "deliveries"]);

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

/** 节点列表响应 → 节点视图状态。 */
export function nodesStateFrom(payload) {
  if (!payload || typeof payload !== "object") return errorState("missing_payload");
  if (payload.unavailable) return errorState("unavailable");
  const nodes = Array.isArray(payload.nodes) ? payload.nodes : null;
  if (!nodes) return errorState("invalid_payload");
  return { status: "ok", nodes };
}

/** 运行列表响应 → 运行视图状态；`reconciliation` 等字段原样保留。 */
export function runsStateFrom(payload) {
  if (!payload || typeof payload !== "object") return errorState("missing_payload");
  if (payload.unavailable) return errorState("unavailable");
  const runs = Array.isArray(payload.runs) ? payload.runs : null;
  if (!runs) return errorState("invalid_payload");
  return { status: "ok", runs };
}

/** 交付列表响应 → 交付视图状态；`pending` 与 `pass` 不在此合并。 */
export function deliveriesStateFrom(payload) {
  if (!payload || typeof payload !== "object") return errorState("missing_payload");
  if (payload.unavailable) return errorState("unavailable");
  const deliveries = Array.isArray(payload.deliveries) ? payload.deliveries : null;
  if (!deliveries) return errorState("invalid_payload");
  return { status: "ok", deliveries };
}

/** 统一入口：视图 id → 对应状态；任务视图需要快照，其余需要列表载荷。 */
export function stateForKind(kind, payload) {
  switch (kind) {
    case "tasks": return tasksStateFromSnapshot(payload);
    case "nodes": return nodesStateFrom(payload);
    case "runs": return runsStateFrom(payload);
    case "deliveries": return deliveriesStateFrom(payload);
    default: return errorState("unknown_kind");
  }
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
  });
}

/** 空状态常量，便于 shell 首帧渲染。 */
export const LOADING_STATE = EMPTY_STATE;
