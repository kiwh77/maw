// Workbench 统一路由（VN-704）。
//
// 两类入口共用一份路由状态：
//   - legacy 工程锚点 `#<project_id>`：刷新与旧链接必须仍能恢复工程筛选；
//   - vnext 视图 `?view=nodes&project=…&id=…`：用 pushState 前进后退，返回列表时保留筛选。
//
// 规则：
//   1. 请求串里没有合法 `view` 时回到 board（与旧直达链接一致）；
//   2. 视图 id 未知时不猜测，回退 board，避免渲染空白；
//   3. 工程锚点优先取 `?project=`，缺省时取旧 hash，两处始终序列化回 hash；
//   4. `id` 只在 vnext 视图生效；board 视图忽略它，不改变 legacy 详情抽屉行为。

import { DEFAULT_VIEW } from "./view-registry.mjs";

export const PARAM_VIEW = "view";
export const PARAM_PROJECT = "project";
export const PARAM_AGENT = "agent";
export const PARAM_STATUS = "status";
export const PARAM_SEARCH = "q";
export const PARAM_DETAIL = "id";

/**
 * 筛选参数的「参数名 → 路由字段」对照。
 *
 * 搜索词的参数名是 `q`、字段名是 `search`：两者不同名，早先直接拿参数名当字段名
 * 取值，导致 `q` 永远序列化不出去（切页/刷新就丢搜索词）。这里显式成对登记。
 */
const FILTER_KEYS = [
  [PARAM_PROJECT, "project"],
  [PARAM_AGENT, "agent"],
  [PARAM_STATUS, "status"],
  [PARAM_SEARCH, "search"],
];

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
  const view = requested && known(requested) ? requested : DEFAULT_VIEW;
  const anchor = projectFromHash(hash);
  const project = params.get(PARAM_PROJECT) || anchor;
  return {
    view,
    project: project || "",
    agent: params.get(PARAM_AGENT) || "",
    status: params.get(PARAM_STATUS) || "",
    search: params.get(PARAM_SEARCH) || "",
    detail: view === DEFAULT_VIEW ? "" : (params.get(PARAM_DETAIL) || ""),
    fromHash: Boolean(anchor) && !params.get(PARAM_PROJECT),
  };
}

/** 序列化筛选条件为查询串；board 视图省略全部参数，保持旧链接形态。 */
export function serializeQuery(route = {}) {
  const params = new URLSearchParams();
  const view = route.view || DEFAULT_VIEW;
  if (view === DEFAULT_VIEW) return "";
  params.set(PARAM_VIEW, view);
  FILTER_KEYS.forEach(([param, field]) => {
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
export function sameRoute(a = {}, b = {}) {
  return (a.view || DEFAULT_VIEW) === (b.view || DEFAULT_VIEW) &&
    (a.project || "") === (b.project || "") &&
    (a.agent || "") === (b.agent || "") &&
    (a.status || "") === (b.status || "") &&
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
    search: route.search || "",
    detail: route.detail || "",
  };
}
