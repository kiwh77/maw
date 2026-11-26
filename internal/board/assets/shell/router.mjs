// Workbench 统一路由（VN-704）。
//
// 两类入口共用一份路由状态：
//   - legacy 工程锚点 `#<project_id>`：刷新与旧链接必须仍能恢复工程筛选；
//   - vnext 视图 `?view=nodes&project=…&id=…`：用 pushState 前进后退，返回列表时保留筛选。
//
// 规则：
//   1. 请求串里没有合法 `view` 时回到默认视图（EV-003 起是执行视图，旧链接仍可直达 board）；
//   2. 视图 id 未知时不猜测，回退默认视图，避免渲染空白；
//   3. 工程锚点优先取 `?project=`，缺省时取旧 hash，两处始终序列化回 hash；
//   4. `id` 只在有详情面板的视图生效；其余视图忽略它，不改变 legacy 详情抽屉行为。

import { DEFAULT_VIEW, DEFAULT_VIEW_NO_PROJECT } from "./view-registry.mjs";

export const PARAM_VIEW = "view";
export const PARAM_PROJECT = "project";
export const PARAM_AGENT = "agent";
export const PARAM_STATUS = "status";
export const PARAM_ITERATION = "iteration";
export const PARAM_SEARCH = "q";
export const PARAM_DETAIL = "id";

/**
 * 筛选参数的「参数名 → 路由字段」对照。
 *
 * 搜索词的参数名是 `q`、字段名是 `search`：两者不同名，早先直接拿参数名当字段名
 * 取值，导致 `q` 永远序列化不出去（切页/刷新就丢搜索词）。这里显式成对登记。
 *
 * 工程不在这里登记：工程锚点走 hash（`#<project_id>`），写进查询串会与锚点重复，
 * 同一份筛选出现两个出处，解析时谁优先就成了暗规则。读的时候仍然接受旧的
 * `?project=`（旧链接形态不能破），写的时候只写 hash。
 */
const FILTER_KEYS = [
  [PARAM_AGENT, "agent"],
  [PARAM_STATUS, "status"],
  [PARAM_ITERATION, "iteration"],
  [PARAM_SEARCH, "search"],
];

/**
 * 有详情面板的视图。
 *
 * 以前用「不是默认视图就收 id」来挡：默认视图是 board，而 board 的详情在
 * legacy 抽屉里，不吃 `id` 参数。默认视图改成执行视图之后这条判据就失效了
 * （执行视图也不吃 id，反倒 board 会开始收），所以这里改成显式清单：
 * 与挂载层的 DETAIL 表同源，只有这三类会把 id 当详情打开。
 */
const DETAIL_VIEWS = Object.freeze(["nodes", "runs", "deliveries"]);

/** 合法的 legacy 工程锚点：非空且不含 `=`（我们的路由参数只出现在 query 里）。 */
export function isLegacyProjectAnchor(hash = "") {
  const raw = String(hash || "").replace(/^#/, "").trim();
  return raw !== "" && raw.indexOf("=") < 0;
}

/** 读取锚点里的工程 ID（解码失败时返回原文）。 */
export function projectFromHash(hash = "") {
  const raw = String(hash || "").replace(/^#/, "").trim();
  if (!isLegacyProjectAnchor(raw)) return "";
  try {
    return decodeURIComponent(raw);
  } catch (_) {
    return raw;
  }
}

/**
 * 解析路由。`isKnownView` 由注册表提供，避免路由器猜测视图清单。
 * @returns {{view:string, project:string, agent:string, status:string, search:string, detail:string, fromHash:boolean}}
 */
export function parseRoute(search = "", hash = "", isKnownView = null) {
  let params;
  try {
    params = new URLSearchParams(String(search || "").replace(/^\?/, ""));
  } catch (_) {
    params = new URLSearchParams("");
  }
  const known = typeof isKnownView === "function" ? isKnownView : () => true;
  const requested = params.get(PARAM_VIEW) || "";
  const anchor = projectFromHash(hash);
  const project = params.get(PARAM_PROJECT) || anchor || "";
  const fallback = project ? DEFAULT_VIEW : DEFAULT_VIEW_NO_PROJECT;
  const view = requested && known(requested) ? requested : fallback;
  return {
    view,
    project,
    agent: params.get(PARAM_AGENT) || "",
    status: params.get(PARAM_STATUS) || "",
    // iteration：参数缺席 = 不筛（null）；`?iteration=` = 筛未归属（""）。
    iteration: params.has(PARAM_ITERATION) ? (params.get(PARAM_ITERATION) || "") : null,
    search: params.get(PARAM_SEARCH) || "",
    detail: DETAIL_VIEWS.indexOf(view) >= 0 ? (params.get(PARAM_DETAIL) || "") : "",
    fromHash: Boolean(anchor) && !params.get(PARAM_PROJECT),
  };
}

/**
 * 序列化筛选条件为查询串。
 *
 * board 是默认视图，不带 `view` 参数以保持旧链接形态；但**筛选与搜索必须写出去**：
 * 以前这里对 board 直接返回空串，结果刷新一次筛选就没了、链接也没法分享。
 * 只看「有没有非默认内容」，而不是「是不是默认视图」。
 */
export function serializeQuery(route = {}) {
  const params = new URLSearchParams();
  const view = route.view || DEFAULT_VIEW;
  if (view !== DEFAULT_VIEW) params.set(PARAM_VIEW, view);
  FILTER_KEYS.forEach(([param, field]) => {
    if (field === "iteration") {
      // null/undefined：不写；""：写空值（未归属）；其它：写原文。
      if (route.iteration === null || route.iteration === undefined) return;
      params.set(param, route.iteration);
      return;
    }
    const value = route[field] || "";
    if (value) params.set(param, value);
  });
  if (route.detail) params.set(PARAM_DETAIL, route.detail);
  const query = params.toString();
  return query ? "?" + query : "";
}

/** 工程锚点序列化：有工程时写 `#<id>`，与 legacy 的读写规则一致。 */
export function serializeHash(route = {}) {
  const project = route.project || "";
  return project ? "#" + encodeURIComponent(project) : "";
}

/** 生成完整相对 URL；path 默认为 `/`。 */
export function serializeRoute(route = {}, pathname = "/") {
  const base = pathname || "/";
  return base + serializeQuery(route) + serializeHash(route);
}

/** 路由等价判断：任一筛选、视图或详情不同即不相等。 */
function normalizeIteration(value) {
  return value === undefined ? null : value;
}

export function sameRoute(a = {}, b = {}) {
  return (a.view || DEFAULT_VIEW) === (b.view || DEFAULT_VIEW) &&
    (a.project || "") === (b.project || "") &&
    (a.agent || "") === (b.agent || "") &&
    (a.status || "") === (b.status || "") &&
    normalizeIteration(a.iteration) === normalizeIteration(b.iteration) &&
    (a.search || "") === (b.search || "") &&
    (a.detail || "") === (b.detail || "");
}

/**
 * 写入路由。replace=false 时压入历史，供返回键回到上一视图/列表。
 * 写入失败（沙箱或只读 history）时静默降级为原地渲染。
 */
export function writeRoute(route, { history = null, location = null, replace = false } = {}) {
  const pathname = (location && location.pathname) || "/";
  const url = serializeRoute(route, pathname);
  try {
    if (history && typeof history[replace ? "replaceState" : "pushState"] === "function") {
      history[replace ? "replaceState" : "pushState"](null, "", url);
      return url;
    }
  } catch (_) {
    // 继续尝试直接改地址。
  }
  try {
    if (location) location.hash = route.project ? "#" + encodeURIComponent(route.project) : "";
  } catch (_) {
    // 无法写地址时保留内存路由。
  }
  return url;
}

/** 以当前路由为底，覆盖视图/筛选/详情等字段。 */
export function withRoute(route, patch = {}) {
  return { ...parseDefaults(route), ...patch };
}

function parseDefaults(route = {}) {
  return {
    view: route.view || DEFAULT_VIEW,
    project: route.project || "",
    agent: route.agent || "",
    status: route.status || "",
    iteration: route.iteration === undefined ? null : route.iteration,
    search: route.search || "",
    detail: route.detail || "",
  };
}
