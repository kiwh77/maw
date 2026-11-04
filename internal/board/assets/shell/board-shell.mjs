// Workbench 挂载层（VN-704）。
//
// 职责边界：
//   - 统一注册、导航与路由：legacy（board/topo/factory）与 vnext（nodes/tasks/runs/deliveries）
//     共用一份视图清单与地址规则；
//   - ViewContext 注入：语言、筛选、详情与导航动作只以只读上下文交给视图；
//   - 数据状态适配：失败/缺失/过期显式呈现，pending/unknown 原样透传；
//   - 根布局与全局语言收口：导航标签、页面标题、面包屑、`<html lang>` 由此层设置，
//     legacy 仍负责它自己的看板列与详情抽屉。
//
// 兼容策略：bundle 缺失时页面保持 legacy 原样；shell 只在 `#vnext-nav` /
// `#vnext-view` 存在时接管，不重写 legacy 的渲染函数。

import {
  translate,
  normalizeLang,
  readLangPreference,
  persistLang,
  applyDocumentLang,
  createTranslator,
} from "../i18n/board-i18n.mjs";
import {
  createRegistry,
  resolveView,
  groupLabelKey,
  navLabelKey,
  SIDEBAR_PINNED_IDS,
  DEFAULT_VIEW,
  LEGACY_CONTAINERS,
  LEGACY_TOGGLES,
} from "./view-registry.mjs";
import {
  parseRoute,
  writeRoute,
  serializeRoute,
  sameRoute,
  withRoute,
} from "./router.mjs";
import {
  stateForKind,
  findDetail,
  createViewContext,
  needsProject,
  LOADING_STATE,
  NO_PROJECT_STATE,
  errorState,
  DATA_KINDS,
} from "./view-context.mjs";
import { renderNodeList, renderNodeDetail } from "../views/nodes/nodes-view.mjs";
import { renderTaskList } from "../views/tasks/tasks-view.mjs";
import { renderRunList, renderRunDetail } from "../views/runs/runs-view.mjs";
import { renderDeliveryList, renderDeliveryDetail } from "../views/deliveries/deliveries-view.mjs";
import { renderTimeline } from "../views/timeline/timeline-view.mjs";
import { renderActivity } from "../views/activity/activity-view.mjs";
import { renderSessions } from "../views/sessions/sessions-view.mjs";
import { renderAnalytics } from "../views/analytics/analytics-view.mjs";
import { renderProjectList } from "../views/projects/projects-view.mjs";
import { renderSettings, normalizePolicy } from "../views/settings/settings-view.mjs";
import { renderExecution } from "../views/execution/execution-view.mjs";

/** vnext 视图 id → 列表渲染函数。 */
const LIST_RENDERERS = Object.freeze({
  nodes: renderNodeList,
  tasks: renderTaskList,
  runs: renderRunList,
  deliveries: renderDeliveryList,
  timeline: renderTimeline,
  activity: renderActivity,
  sessions: renderSessions,
  analytics: renderAnalytics,
  projects: renderProjectList,
  settings: renderSettings,
  // 执行视图是默认落地页：这条不登记，legacy 挂载层打开首页就会拿到 undefined。
  execution: renderExecution,
});

/** vnext 视图 id → 详情渲染函数；tasks 走 legacy 抽屉，故不在此表。 */
const DETAIL_RENDERERS = Object.freeze({
  nodes: renderNodeDetail,
  runs: renderRunDetail,
  deliveries: renderDeliveryDetail,
});

const TASKS_SOURCE = "/api/board";

/** 控制类资源：服务端只有 `/api/v1/projects/{id}/…`，没有全局列表。 */
const CONTROL_RESOURCES = Object.freeze(["nodes", "runs", "deliveries"]);

/**
 * 按工程物化的观测资源名。
 *
 * 服务端路由是 `/api/v1/projects/{project_id}/<资源>`；这里只登记资源名，
 * 不为四个视图各写一份拼地址的代码——拼错一处就是一次静默 404。
 */
const PROJECT_RESOURCES = Object.freeze({
  timeline: "timeline",
  activity: "activity",
  sessions: "sessions",
  analytics: "analytics",
});

/**
 * 观测视图 → 能力发现接口里的能力名。
 *
 * 统计接口叫 analytics，能力名却叫 stats（契约 §4 沿用了服务端术语）；
 * 这张表就是两套命名之间唯一的转换点。
 */
const CAPABILITY_OF_KIND = Object.freeze({
  timeline: "timeline",
  activity: "activity",
  sessions: "sessions",
  analytics: "stats",
});

/** 构造按工程取观测资源的地址。 */
export function projectResourcePath(kind, projectId) {
  const resource = PROJECT_RESOURCES[kind];
  if (!resource) return "";
  const id = String(projectId || "").trim();
  if (!id) return "";
  return "/api/v1/projects/" + encodeURIComponent(id) + "/" + resource;
}

/** 构造控制类（节点／运行／交付）按工程地址。 */
export function controlResourcePath(kind, projectId) {
  if (CONTROL_RESOURCES.indexOf(kind) < 0) return "";
  const id = String(projectId || "").trim();
  if (!id) return "";
  return "/api/v1/projects/" + encodeURIComponent(id) + "/" + kind;
}

/** 构造能力发现地址。 */
export function capabilitiesPath(projectId) {
  const id = String(projectId || "").trim();
  if (!id) return "";
  return "/api/v1/projects/" + encodeURIComponent(id) + "/observability/capabilities";
}

/** 静态导出（file:// 打开）没有同源中枢：一切写入口都必须关闭。 */
export function isStaticExport(win) {
  const protocol = win && win.location ? String(win.location.protocol || "") : "";
  return protocol === "file:";
}

/** 转义任意文本，避免数据进入 HTML 时破坏结构。 */
function esc(value) {
  return String(value == null ? "" : value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function byId(doc, id) {
  return doc && typeof doc.getElementById === "function" ? doc.getElementById(id) : null;
}

/** 读取 localStorage；沙箱/无来源页面会抛 SecurityError，这里一律降级为 null。 */
function safeStorage(win) {
  try {
    return win && win.localStorage ? win.localStorage : null;
  } catch (_) {
    return null;
  }
}

/** 构造默认数据源：网络不可用或端点不存在时返回显式失败载荷。 */
export function createDefaultSources(win, fetchImpl = null) {
  const doFetch = fetchImpl || (win && typeof win.fetch === "function" ? win.fetch.bind(win) : null);
  let sessionPromise = null;

  async function ensureSession() {
    if (!doFetch) return false;
    if (!sessionPromise) {
      sessionPromise = (async () => {
        try {
          const res = await doFetch("/api/session", {
            method: "POST",
            headers: { Accept: "application/json" },
            credentials: "same-origin",
          });
          return Boolean(res && res.ok);
        } catch (_) {
          return false;
        }
      })();
    }
    const ok = await sessionPromise;
    if (!ok) sessionPromise = null;
    return ok;
  }

  async function getJSON(url, opts = {}) {
    if (!doFetch) return { unavailable: true, status: 0 };
    if (opts.auth) await ensureSession();
    try {
      let res = await doFetch(url, {
        headers: { Accept: "application/json" },
        credentials: "same-origin",
      });
      if (res && res.status === 401 && opts.auth) {
        sessionPromise = null;
        if (await ensureSession()) {
          res = await doFetch(url, {
            headers: { Accept: "application/json" },
            credentials: "same-origin",
          });
        }
      }
      if (!res || !res.ok) {
        const fail = { unavailable: true, status: res ? res.status : 0 };
        if (res && res.status === 401) fail.unauthenticated = true;
        return fail;
      }
      return await res.json();
    } catch (_) {
      return { unavailable: true, status: 0 };
    }
  }

  function withNamedList(kind, payload) {
    if (!payload || payload.unavailable === true) return payload;
    if (!Array.isArray(payload[kind]) && Array.isArray(payload.items)) {
      return { ...payload, [kind]: payload.items };
    }
    return payload;
  }

  async function fetchControl(kind, projectId) {
    const url = controlResourcePath(kind, projectId);
    if (!url) return { unavailable: true, status: 0 };
    return withNamedList(kind, await getJSON(url, { auth: true }));
  }

  async function fetchControlAll(kind) {
    const board = await getJSON(TASKS_SOURCE);
    if (!board || board.unavailable === true) return board || { unavailable: true, status: 0 };
    const projects = Array.isArray(board.projects) ? board.projects : [];
    const ids = projects
      .map(p => String((p && (p.id || p.project_id)) || "").trim())
      .filter(Boolean);
    if (!ids.length) return { [kind]: [], items: [] };
    const parts = await Promise.all(ids.map(id => fetchControl(kind, id)));
    if (parts.every(p => p && p.unauthenticated === true)) {
      return { unavailable: true, status: 401, unauthenticated: true };
    }
    if (parts.every(p => p && p.unavailable === true)) {
      return { unavailable: true, status: (parts[0] && parts[0].status) || 0 };
    }
    const items = parts.flatMap(p => {
      if (!p || p.unavailable === true) return [];
      if (Array.isArray(p[kind])) return p[kind];
      if (Array.isArray(p.items)) return p.items;
      return [];
    });
    return { [kind]: items, items };
  }

  /** 观测资源：地址由工程决定，缺工程不拼地址、直接报不可用。 */
  function projectSource(kind) {
    return async route => {
      const url = projectResourcePath(kind, route && route.project);
      if (!url) return { unavailable: true, status: 0 };
      return getJSON(url, { auth: true });
    };
  }

  function controlSource(kind) {
    return async route => {
      const project = String((route && route.project) || "").trim();
      if (project) return fetchControl(kind, project);
      return fetchControlAll(kind);
    };
  }

  /** 看板快照（带当前筛选）。执行视图与任务视图共用同一个来源。 */
  async function fetchTasks(route) {
    const params = new URLSearchParams();
    if (route.project) params.set("project", route.project);
    if (route.agent) params.set("agent", route.agent);
    if (route.status) params.set("status", route.status);
    const query = params.toString();
    return getJSON(TASKS_SOURCE + (query ? "?" + query : ""));
  }

  /** 从列表载荷里取数组：契约用资源名，分页外壳用 `items`。 */
  function listOf(payload, name) {
    if (!payload || payload.unavailable === true) return null;
    if (Array.isArray(payload[name])) return payload[name];
    if (Array.isArray(payload.items)) return payload.items;
    return null;
  }

  return {
    tasks: fetchTasks,
    /**
     * 执行视图（EV-001 / EV-003）：任务、运行、交付三类一次取齐。
     *
     * 与 web/src/lib/sources.ts 同一口径：缺一类就整份判失败，不返回
     * 「有任务、零运行」的半成品；没有工程时按登记工程逐个取再合并。
     */
    async execution(route) {
      const project = String((route && route.project) || "").trim();
      const [taskPayload, runsPayload, deliveriesPayload] = await Promise.all([
        fetchTasks(route || {}),
        project ? fetchControl("runs", project) : fetchControlAll("runs"),
        project ? fetchControl("deliveries", project) : fetchControlAll("deliveries"),
      ]);
      const parts = [taskPayload, runsPayload, deliveriesPayload];
      if (parts.some(p => p && p.unauthenticated === true)) {
        return { unavailable: true, status: 401, unauthenticated: true };
      }
      const failed = parts.find(p => !p || p.unavailable === true);
      if (failed) return { unavailable: true, status: failed.status || 0 };
      const tasks = listOf(taskPayload, "tasks");
      const runs = listOf(runsPayload, "runs");
      const deliveries = listOf(deliveriesPayload, "deliveries");
      if (!tasks || !runs || !deliveries) return { unavailable: true, status: 0 };
      return { tasks, runs, deliveries };
    },
    nodes: controlSource("nodes"),
    runs: controlSource("runs"),
    deliveries: controlSource("deliveries"),
    timeline: projectSource("timeline"),
    activity: projectSource("activity"),
    sessions: projectSource("sessions"),
    analytics: projectSource("analytics"),
    // 工程清单复用看板快照：工程是中枢级资源，不受工程筛选影响。
    async projects() { return getJSON(TASKS_SOURCE); },
    // 策略读的是中枢生效配置；没有写入端点，因此不提供 saveSettings。
    async settings() {
      const payload = await getJSON("/api/runtime");
      if (!payload || payload.unavailable === true) return payload || { unavailable: true };
      return { ...payload, policy: payload.auto_execution || null };
    },
    /** 能力发现：返回 null 表示「查不到」，调用方据此按未知能力继续请求。 */
    async capabilities(projectId) {
      const url = capabilitiesPath(projectId);
      if (!url) return null;
      const payload = await getJSON(url, { auth: true });
      if (!payload || payload.unavailable === true) return null;
      const list = Array.isArray(payload.capabilities) ? payload.capabilities : null;
      if (!list) return null;
      const map = {};
      list.forEach(item => {
        if (!item || typeof item.name !== "string") return;
        map[item.name] = { supported: item.supported !== false, reason: item.reason || "" };
      });
      return map;
    },
  };
}

/** 客户端搜索：只在任务视图内做，不改快照本身。 */
function matchSearch(task, term) {
  if (!term) return true;
  const needle = String(term).trim().toLowerCase();
  if (!needle) return true;
  return ["display_id", "instance_id", "id", "title", "owner"].some(key =>
    String(task && task[key] != null ? task[key] : "").toLowerCase().indexOf(needle) >= 0
  );
}

function filterTasks(payload, route) {
  if (!payload || !Array.isArray(payload.tasks) || !route.search) return payload;
  const allTasks = Array.isArray(payload.tasks) ? payload.tasks : [];
  return {
    ...payload,
    allTasks,
    tasks: allTasks.filter(task => matchSearch(task, route.search)),
  };
}

/** 状态说明条：只解释失败/缺失，不参与成功判定。 */
function statusHint(state, t) {
  if (!state) return "";
  if (state.status === "loading") {
    return '<p class="vnext-status" data-state="loading">' + esc(t("data.loading")) + "</p>";
  }
  if (state.reason === "unavailable") {
    return '<p class="vnext-status" data-state="error">' + esc(t("data.unavailable")) +
      " " + esc(t("data.unavailable.hint")) + "</p>";
  }
  if (state.status === "error") {
    return '<p class="vnext-status" data-state="error">' + esc(t("data.error")) +
      " " + esc(t("data.error.hint")) + "</p>";
  }
  if (state.status === "expired") {
    return '<p class="vnext-status" data-state="expired">' + esc(t("lane.unknown")) + "</p>";
  }
  return "";
}

/**
 * 回顾类视图的只读提示（F38：所有历史回顾均为只读展示）。
 *
 * 这不是「降级」，而是这些页面的性质：能缩放、能翻窗口，但任何操作都不写盘。
 * 写在这里而不是各视图自己写，是为了四个回顾页说法一致。
 */
const REVIEW_KINDS = Object.freeze(["timeline", "activity", "sessions", "analytics"]);

/** 一条带状态的说明块；`attrs` 只放数据属性，便于断言与去重。 */
function noticeHtml(state, titleKey, hintKey, t, attrs = "") {
  return '<div class="vnext-notice" data-state="' + esc(state) + '"' + (attrs ? " " + attrs : "") + ">" +
    '<p class="vnext-notice__title">' + esc(t(titleKey)) + "</p>" +
    '<p class="field-hint">' + esc(t(hintKey)) + "</p></div>";
}

/**
 * 创建挂载层。返回句柄供 bootstrap、测试与集成方调用。
 * 无 DOM 环境时不抛错，返回未挂载句柄。
 */
export function createShell(options = {}) {
  const doc = options.document || (typeof document !== "undefined" ? document : null);
  const win = options.window || (doc && doc.defaultView) || (typeof window !== "undefined" ? window : null);
  const registry = options.registry || createRegistry();
  const sources = options.sources || createDefaultSources(win, options.fetchImpl);
  const fixtures = options.fixtures || {};
  const styles = options.styles || "";
  const routeKnown = id => Boolean(registry.get(id));
  // 只读由调用方或地址决定：file:// 打开的静态导出没有同源中枢，写入口必须关掉。
  const readOnly = options.readOnly === true ||
    (options.readOnly !== false && isStaticExport(win));

  const state = {
    route: options.route || (win && win.location
      ? parseRoute(win.location.search, win.location.hash, routeKnown)
      : parseRoute("", "", routeKnown)),
    lang: normalizeLang(options.lang || readLangPreference({
      search: win && win.location ? win.location.search : "",
      storage: safeStorage(win),
      navigatorLanguage: win && win.navigator ? win.navigator.language : "",
    })),
    states: {},
    payloads: {},
    loadedFor: {},
    // 能力发现按工程缓存：同一工程切页不该重复问一次「你有时间轴吗」。
    capabilities: {},
    token: 0,
    lastLegacyDetail: "",
    lastDetailFocus: null,
    listeners: [],
  };

  const handle = {
    registry,
    route: () => ({ ...state.route }),
    lang: () => state.lang,
    readOnly: () => readOnly,
    navigate: () => handle,
    setLang: () => handle,
    openDetail: () => handle,
    closeDetail: () => handle,
    render: () => handle,
    mount: () => handle,
    destroy: () => handle,
    serialize: () => serializeRoute(state.route, win && win.location ? win.location.pathname : "/"),
  };

  if (!doc || !win) return handle;

  const root = byId(doc, "vnext-view");
  const navRoot = byId(doc, "vnext-nav");
  const detailRoot = byId(doc, "vnext-detail");
  const t = (key, params) => translate(key, state.lang, params);

  function on(target, type, handler, opts) {
    if (!target || typeof target.addEventListener !== "function") return;
    target.addEventListener(type, handler, opts);
    state.listeners.push(() => target.removeEventListener(type, handler, opts));
  }

  function injectStyles() {
    if (!styles || byId(doc, "maw-vnext-styles")) return;
    const style = doc.createElement("style");
    style.id = "maw-vnext-styles";
    style.textContent = styles;
    (doc.head || doc.documentElement).appendChild(style);
  }

  function ensureNav() {
    if (navRoot || !byId(doc, "app-shell-aside")) return navRoot;
    const nav = doc.createElement("nav");
    nav.id = "vnext-nav";
    nav.className = "vnext-nav";
    nav.setAttribute("aria-label", t("aria.workbench"));
    byId(doc, "app-shell-aside").appendChild(nav);
    return nav;
  }

  function ensureDetail() {
    const existing = byId(doc, "vnext-detail");
    if (existing) return existing;
    const panel = doc.createElement("section");
    panel.id = "vnext-detail";
    panel.className = "vnext-detail";
    panel.hidden = true;
    panel.setAttribute("role", "region");
    panel.innerHTML = '<div class="vnext-detail__head">' +
      '<button type="button" class="vnext-detail__back" id="vnext-detail-back"></button>' +
      '<h2 class="vnext-detail__title" id="vnext-detail-title"></h2>' +
      '<button type="button" class="vnext-detail__close" id="vnext-detail-close">×</button>' +
      "</div>" +
      '<div class="vnext-detail__body" id="vnext-detail-body"></div>';
    // 挂到内容区：与列表并列，详情时替换列表而非浮层盖住筛选头。
    const host = (root && root.parentNode) || byId(doc, "content") ||
      (doc.querySelector && doc.querySelector(".content")) ||
      doc.body || doc.documentElement;
    if (root && root.parentNode === host) host.insertBefore(panel, root.nextSibling);
    else host.appendChild(panel);
    return panel;
  }

  /** 单个导航按钮；置顶项多一个类名，仍复用同一套点击协议（data-view）。 */
  function navButton(def) {
    const active = def.id === state.route.view;
    const pinned = SIDEBAR_PINNED_IDS.indexOf(def.id) >= 0;
    return '<button type="button" class="nav-item' + (pinned ? " nav-item--pinned" : "") +
      (active ? " active" : "") + '"' +
      ' data-view="' + esc(def.id) + '"' +
      (active ? ' aria-current="page"' : "") + ">" +
      '<span class="symbol" aria-hidden="true">' + esc(def.symbol) + "</span>" +
      "<span>" + esc(t(navLabelKey(def))) + "</span></button>";
  }

  function renderNav() {
    const nav = ensureNav();
    if (!nav) return;
    const label = byId(doc, "workbench-label");
    if (label) label.textContent = t("nav.workbench");
    nav.classList.add("vnext-nav");
    nav.setAttribute("aria-label", t("aria.workbench"));
    // 置顶项先渲染、不带分组标题：工作台=无 view 参数的默认页，任何时候都能一键回去。
    nav.innerHTML = registry.sidebarPinned().map(navButton).join("") +
      registry.sidebarNavModel().map(group =>
        '<div class="section-label">' + esc(t(group.labelKey)) + "</div>" +
        group.items.map(navButton).join("")
      ).join("");
  }

  function updateChrome(def) {
    const eyebrow = doc.querySelector(".page-heading .eyebrow");
    if (eyebrow && def.eyebrowKey) eyebrow.textContent = t(def.eyebrowKey);
    const h1 = byId(doc, "page-h1");
    if (h1 && def.headingKey) h1.textContent = t(def.headingKey);
    const desc = byId(doc, "page-desc");
    if (desc && def.descKey) desc.textContent = t(def.descKey);
    const crumb = doc.querySelector(".breadcrumb .dim");
    if (crumb) crumb.textContent = t("nav.workbench");
    // 面包屑：`工作台 / <分组> / <工程或视图名>`。
    //
    // 分组由 shell 独占（legacy 不认这一格），工程名写在 `#workspace-name`——
    // 旧看板本来就用它显示选中工程名，两边口径一致，不会互相覆盖出两个不同的名字。
    const scope = byId(doc, "crumb-scope");
    const scopeSlash = byId(doc, "crumb-scope-slash");
    const labelKey = groupLabelKey(def.group);
    if (scope) {
      scope.textContent = labelKey ? t(labelKey) : "";
      scope.hidden = !labelKey;
      if (scopeSlash) scopeSlash.hidden = !labelKey;
    }
    const project = String(state.route.project || "").trim();
    const name = byId(doc, "workspace-name");
    if (name) {
      name.dataset.locked = "1";
      // 选了工程就显示工程（这就是当前上下文），没选才退回视图名。
      name.textContent = project || t(def.titleKey);
    }
    const title = doc.querySelector("h1#page-h1");
    if (title) title.setAttribute("tabindex", "-1");
  }

  function hideLegacyContainers() {
    LEGACY_CONTAINERS.forEach(id => {
      const el = byId(doc, id);
      if (el) el.hidden = true;
    });
    const hints = byId(doc, "hint-center");
    if (hints) hints.hidden = true;
  }

  /**
   * 按视图门控看板 chrome，避免 vnext 菜单页仍挂着筛选条 / Board 切换。
   *
   *   - legacy：stats + filters + view-toggle + 搜索全显
   *   - control/review/settings：整条看板 chrome 隐藏（工程筛选仅服务流程视图）
   */
  function setBoardChrome(def) {
    const isLegacy = Boolean(def && def.legacy);
    const showStats = isLegacy;
    const showFilters = isLegacy;
    const showToggle = isLegacy;
    const showSearch = isLegacy;

    const stats = byId(doc, "stats");
    if (stats) stats.hidden = !showStats;

    const filters = byId(doc, "filters");
    if (filters) filters.hidden = !showFilters;

    const toggle = byId(doc, "view-toggle");
    if (toggle) {
      toggle.hidden = !showToggle;
      const buttons = toggle.querySelectorAll
        ? toggle.querySelectorAll("[data-view]")
        : [];
      Array.prototype.forEach.call(buttons, btn => {
        // vnext 清掉 legacy 高亮；回 legacy 时按当前视图恢复，不依赖 win.setView。
        const active = showToggle && def && btn.dataset.view === def.id;
        btn.classList.toggle("active", active);
        btn.setAttribute("aria-pressed", active ? "true" : "false");
      });
    }

    const count = byId(doc, "board-count");
    if (count) count.hidden = !showToggle;

    const search = byId(doc, "task-search");
    if (search) {
      const box = search.closest ? search.closest(".search-box") : null;
      if (box) box.hidden = !showSearch;
      else search.hidden = !showSearch;
    }

    const toolbar = doc.querySelector ? doc.querySelector(".toolbar") : null;
    if (toolbar) toolbar.hidden = !showFilters && !showToggle && !showSearch;
  }

  function showLegacy(def) {
    if (root) root.hidden = true;
    if (detailRoot) detailRoot.hidden = true;
    const hints = byId(doc, "hint-center");
    if (hints) hints.hidden = false;
    setBoardChrome(def);
    if (typeof win.setView === "function") {
      win.setView(def.id);
      return;
    }
    const target = LEGACY_TOGGLES[def.id] === "view-board" ? "columns"
      : LEGACY_TOGGLES[def.id] === "view-topo" ? "topology" : "factory";
    LEGACY_CONTAINERS.forEach(id => {
      const el = byId(doc, id);
      if (el) el.hidden = id !== target;
    });
  }

  function currentPayload(kind) {
    if (Object.prototype.hasOwnProperty.call(fixtures, kind)) return fixtures[kind];
    return state.payloads[kind];
  }

  function currentState(kind) {
    if (Object.prototype.hasOwnProperty.call(fixtures, kind)) {
      return stateForKind(kind, fixtures[kind], { readOnly, normalizePolicy });
    }
    return state.states[kind] || LOADING_STATE;
  }

  function context(def) {
    return createViewContext({
      view: def,
      lang: state.lang,
      t: createTranslator(state.lang),
      route: state.route,
      navigate: handle.navigate,
      openDetail: handle.openDetail,
      closeDetail: handle.closeDetail,
      data: Object.freeze({ ...state.states }),
      registry,
      readOnly,
    });
  }

  /**
   * 只读收口。
   *
   * 设置页自带保存按钮，而写入入口是否存在由中枢版本决定：视图自己不知道这件事，
   * 也不该由它猜。所以由挂载层在这里把按钮禁用并追加说明——视图仍按原样渲染，
   * 不会因为静态导出就少显示一段内容。
   */
  function applyViewChrome(def, viewState) {
    if (!root) return;
    const kind = def.id;
    // 快照没带策略时先说明「数值未知」，否则每一行都会被读成「自动模式关闭」。
    if (kind === "projects" && viewState && viewState.policyKnown === false &&
      typeof root.insertAdjacentHTML === "function" &&
      typeof root.querySelector === "function" && !root.querySelector("[data-policy-missing]")) {
      root.insertAdjacentHTML("afterbegin", noticeHtml("policy_missing",
        "projects.policy_missing", "projects.policy_missing.hint", t, 'data-policy-missing="1"'));
    }
    const wantNotice = REVIEW_KINDS.indexOf(kind) >= 0 || (kind === "settings" && readOnly);
    if (wantNotice && typeof root.insertAdjacentHTML === "function" &&
      typeof root.querySelector === "function" && !root.querySelector("[data-readonly-notice]")) {
      const html = kind === "settings"
        ? noticeHtml("readonly", "settings.readonly", "settings.readonly.hint", t,
          'data-readonly-notice="' + esc(kind) + '"')
        : noticeHtml("readonly", "review.readonly", "review.readonly.hint", t,
          'data-readonly-notice="' + esc(kind) + '"');
      root.insertAdjacentHTML("beforeend", html);
    }
    if (kind !== "settings" || !readOnly) return;
    if (typeof root.querySelectorAll !== "function") return;
    const forms = root.querySelectorAll("form");
    Array.prototype.forEach.call(forms, form => {
      if (form.dataset) form.dataset.readonly = "1";
      form.setAttribute && form.setAttribute("data-readonly", "1");
      const buttons = form.querySelectorAll ? form.querySelectorAll("button") : [];
      Array.prototype.forEach.call(buttons, button => {
        button.disabled = true;
        button.setAttribute && button.setAttribute("aria-disabled", "true");
      });
    });
  }

  /** 该视图此刻是否被「必须先选工程」挡住。 */
  function projectBlocked(def) {
    return needsProject(def.id) && !String(state.route.project || "").trim();
  }

  /** 能力发现结果；`null` 表示查不到，按未知能力继续请求。 */
  async function capabilitiesFor(projectId) {
    const key = String(projectId || "");
    if (!key) return null;
    if (Object.prototype.hasOwnProperty.call(state.capabilities, key)) return state.capabilities[key];
    let map = null;
    try {
      map = typeof sources.capabilities === "function" ? await sources.capabilities(key) : null;
    } catch (_) {
      map = null;
    }
    state.capabilities[key] = map;
    return map;
  }

  /** 把一份状态画进容器；工程缺失时画说明而不是空视图。 */
  function paint(def, viewState) {
    if (!root) return;
    if (projectBlocked(def)) {
      root.innerHTML = noticeHtml("no_project", "review.project_required", "review.project_required.hint", t);
      return;
    }
    root.innerHTML = statusHint(viewState, t) +
      LIST_RENDERERS[def.id](viewState, { lang: state.lang });
    if (rowsAreClickable(def.id)) decorateRows();
    applyViewChrome(def, viewState);
  }

  /**
   * 行是否可点开详情：只有登记了详情渲染函数的视图（以及走旧抽屉的任务视图）才装饰。
   * 回顾／统计／设置没有详情面，把它们的行做成可点会得到一个「找不到这一项」的空面板。
   */
  function rowsAreClickable(kind) {
    return kind === "tasks" || Boolean(DETAIL_RENDERERS[kind]);
  }

  function decorateRows() {
    if (!root || typeof root.querySelectorAll !== "function") return;
    root.querySelectorAll("tbody tr").forEach(tr => {
      const cell = tr.querySelector("td.id");
      if (!cell) return;
      const id = String(cell.textContent || "").trim();
      if (!id || id === "—") return;
      tr.classList.add("row-clickable");
      tr.setAttribute("tabindex", "0");
      tr.setAttribute("aria-label", t("detail.open"));
      tr.dataset.detailId = id;
    });
  }

  function renderDetail(def) {
    const panel = ensureDetail();
    if (!panel) return;
    const id = state.route.detail;
    const hideList = () => { if (root) root.hidden = true; };
    const showList = () => { if (root && !def.legacy) root.hidden = false; };
    if (def.legacy || !id) {
      panel.hidden = true;
      showList();
      if (def.id !== "tasks" && state.lastLegacyDetail && typeof win.closeDrawer === "function") {
        win.closeDrawer();
      }
      state.lastLegacyDetail = "";
      return;
    }
    if (def.id === "tasks") {
      panel.hidden = true;
      showList();
      if (state.lastLegacyDetail !== id) {
        state.lastLegacyDetail = id;
        if (typeof win.openDrawer === "function") win.openDrawer(id, state.route.project || "");
      }
      return;
    }
    const renderDetailFn = DETAIL_RENDERERS[def.id];
    if (!renderDetailFn) {
      // 没有详情面的视图（回顾／统计／设置）：不做「找不到这一项」的假提示。
      panel.hidden = true;
      showList();
      return;
    }
    const item = findDetail(def.id, currentPayload(def.id), id);
    const title = byId(doc, "vnext-detail-title");
    const body = byId(doc, "vnext-detail-body");
    if (title) title.textContent = item
      ? String(item.name || item.title || item.run_id || item.delivery_id || id)
      : id;
    if (body) {
      body.innerHTML = item && renderDetailFn
        ? renderDetailFn(item, { lang: state.lang })
        : '<p class="field-hint">' + esc(t("detail.missing")) + " " + esc(t("detail.missing.hint")) + "</p>";
    }
    panel.hidden = false;
    hideList();
    const back = byId(doc, "vnext-detail-back");
    const close = byId(doc, "vnext-detail-close");
    if (back) back.textContent = t("detail.back");
    if (close) close.setAttribute("aria-label", t("detail.close"));
    const focusTarget = back || close;
    if (focusTarget && typeof focusTarget.focus === "function") focusTarget.focus();
  }

  async function load(kind, route) {
    const key = kind + "|" + [route.project, route.agent, route.status, route.search].join("|");
    if (state.loadedFor[kind] === key) return;
    state.loadedFor[kind] = key;
    const def = registry.get(kind);
    // 工程缺失：不发请求、不构造空列表，显式记成「未选上下文」。
    if (def && projectBlocked(def)) {
      state.states[kind] = NO_PROJECT_STATE;
      state.payloads[kind] = null;
      paint(def, NO_PROJECT_STATE);
      return Promise.resolve();
    }
    state.states[kind] = LOADING_STATE;
    const token = ++state.token;
    if (root && state.route.view === kind && !(def && def.legacy)) {
      paint(def, LOADING_STATE);
    }
    // 能力预检：能力发现说「不支持」时不做无望请求，直接给降级说明。
    const capName = CAPABILITY_OF_KIND[kind];
    if (capName) {
      const caps = await capabilitiesFor(route.project);
      const entry = caps ? caps[capName] : null;
      if (entry && entry.supported === false) {
        if (token !== state.token || state.route.view !== kind) return Promise.resolve();
        state.states[kind] = {
          status: "error",
          reason: "unavailable",
          capability: entry.reason || "source_not_configured",
        };
        state.payloads[kind] = null;
        paint(resolveView(registry, kind), state.states[kind]);
        return Promise.resolve();
      }
    }
    let payload = null;
    try {
      if (Object.prototype.hasOwnProperty.call(fixtures, kind)) {
        payload = fixtures[kind];
      } else if (sources[kind]) {
        payload = await sources[kind](route);
      } else {
        payload = { unavailable: true };
      }
    } catch (_) {
      payload = { unavailable: true };
    }
    if (token !== state.token || state.route.view !== kind) return Promise.resolve();
    if (kind === "tasks") payload = filterTasks(payload, route);
    state.payloads[kind] = payload;
    state.states[kind] = stateForKind(kind, payload, { readOnly, normalizePolicy });
    paint(resolveView(registry, kind), state.states[kind]);
    renderDetail(resolveView(registry, state.route.view));
    return Promise.resolve();
  }

  function render() {
    const def = resolveView(registry, state.route.view);
    applyDocumentLang(doc, state.lang);
    const langSelect = byId(doc, "lang-select");
    if (langSelect) langSelect.value = state.lang;
    renderNav();
    renderDetail(def);
    if (def.legacy) {
      updateChrome(def);
      showLegacy(def);
      return handle;
    }
    hideLegacyContainers();
    setBoardChrome(def);
    updateChrome(def);
    // 有详情面时列表已由 renderDetail 隐藏，勿再强制显示。
    const detailOpen = Boolean(state.route.detail && DETAIL_RENDERERS[def.id]);
    if (root && !detailOpen) root.hidden = false;
    paint(def, currentState(def.id));
    void load(def.id, state.route);
    return handle;
  }

  function applyLang(lang, { notifyLegacy = true } = {}) {
    state.lang = normalizeLang(lang);
    persistLang(state.lang, safeStorage(win));
    applyDocumentLang(doc, state.lang);
    if (notifyLegacy && typeof win.setLang === "function") {
      try { win.setLang(state.lang); } catch (_) { /* legacy i18n 失败不影响 shell */ }
    }
    render();
    return handle;
  }

  function readFilterPatch() {
    const project = byId(doc, "f-project");
    const agent = byId(doc, "f-agent");
    const status = byId(doc, "f-status");
    const search = byId(doc, "task-search");
    return {
      project: project ? project.value || "" : state.route.project,
      agent: agent ? agent.value || "" : state.route.agent,
      status: status ? status.value || "" : state.route.status,
      search: search ? search.value || "" : state.route.search,
    };
  }

  /** 把地址里的筛选回填到工具栏，保证刷新/直达后 UI 与路由一致。 */
  function pushRouteToFilters() {
    const apply = (id, value) => {
      const el = byId(doc, id);
      if (!el || !value) return;
      const options = el.options ? Array.prototype.slice.call(el.options) : [];
      if (options.some(option => option.value === value)) el.value = value;
    };
    apply("f-project", state.route.project);
    apply("f-agent", state.route.agent);
    apply("f-status", state.route.status);
    const search = byId(doc, "task-search");
    if (search && state.route.search) search.value = state.route.search;
  }

  /** vnext 视图下，工具栏改动同步进路由；legacy 视图仍由 board-app 自己处理。 */
  function syncFiltersToRoute() {
    const def = resolveView(registry, state.route.view);
    if (def.legacy) return;
    const next = withRoute(state.route, readFilterPatch());
    if (sameRoute(next, state.route)) return;
    writeRoute(next, { history: win.history, location: win.location, replace: true });
    state.route = next;
    render();
  }

  handle.mount = () => {
    injectStyles();
    pushRouteToFilters();
    render();
    const nav = ensureNav();
    on(nav, "click", event => {
      const button = event.target && event.target.closest ? event.target.closest("[data-view]") : null;
      if (!button) return;
      handle.navigate(button.dataset.view);
    });
    const langSelect = byId(doc, "lang-select");
    on(langSelect, "change", event => applyLang(event.target.value));
    on(byId(doc, "filters"), "change", () => syncFiltersToRoute());
    on(byId(doc, "f-reset"), "click", () => win.setTimeout(syncFiltersToRoute, 0));
    let searchTimer = null;
    on(byId(doc, "task-search"), "input", () => {
      if (searchTimer) win.clearTimeout(searchTimer);
      searchTimer = win.setTimeout(syncFiltersToRoute, 180);
    });
    // vnext 下拦截 legacy toggle：改走 shell.navigate，避免 #columns 与 #vnext-view 叠层。
    on(byId(doc, "view-toggle"), "click", event => {
      const current = resolveView(registry, state.route.view);
      if (current.legacy) return;
      const button = event.target && event.target.closest
        ? event.target.closest("[data-view]")
        : null;
      if (!button || !button.dataset.view) return;
      if (typeof event.preventDefault === "function") event.preventDefault();
      if (typeof event.stopPropagation === "function") event.stopPropagation();
      handle.navigate(button.dataset.view);
    }, true);
    // legacy 用 replaceState 改工程锚点，不触发 hashchange，这里补一次同步。
    on(byId(doc, "project-nav"), "click", () => {
      win.setTimeout(() => {
        const next = parseRoute(win.location.search, win.location.hash, routeKnown);
        if (sameRoute(next, state.route)) return;
        state.route = next;
        render();
      }, 0);
    });
    on(win, "popstate", () => {
      state.route = parseRoute(win.location.search, win.location.hash, routeKnown);
      render();
    });
    on(win, "hashchange", () => {
      const next = parseRoute(win.location.search, win.location.hash, routeKnown);
      if (sameRoute(next, state.route)) return;
      state.route = next;
      render();
    });
    on(win, "keydown", event => {
      if (event.key === "Escape" && state.route.detail) {
        event.preventDefault();
        handle.closeDetail();
      }
    });
    if (root) {
      // 只读模式下把提交拦在最前：即使有人绕过 disabled，也不会发出写请求。
      on(root, "submit", event => {
        if (!readOnly) return;
        if (event && typeof event.preventDefault === "function") event.preventDefault();
      }, true);
      on(root, "click", event => {
        if (event.target && event.target.closest && event.target.closest("button,a,input,select,textarea")) return;
        const row = event.target && event.target.closest ? event.target.closest("tr[data-detail-id]") : null;
        if (row) handle.openDetail(row.dataset.detailId);
      });
      on(root, "keydown", event => {
        if (event.key !== "Enter" && event.key !== " ") return;
        const row = event.target && event.target.closest ? event.target.closest("tr[data-detail-id]") : null;
        if (!row) return;
        event.preventDefault();
        handle.openDetail(row.dataset.detailId);
      });
    }
    const panel = ensureDetail();
    on(byId(doc, "vnext-detail-close"), "click", () => handle.closeDetail());
    on(byId(doc, "vnext-detail-back"), "click", () => handle.closeDetail());
    if (panel) {
      panel.setAttribute("aria-label", t("detail.close"));
    }
    return handle;
  };

  handle.navigate = (view, patch = {}, { replace = false } = {}) => {
    const def = resolveView(registry, view);
    const next = withRoute(state.route, { view: def.id, detail: "", ...patch });
    if (!sameRoute(next, state.route)) {
      writeRoute(next, { history: win.history, location: win.location, replace });
      state.route = next;
    }
    render();
    return handle;
  };

  handle.setLang = lang => applyLang(lang);

  handle.openDetail = id => {
    state.lastDetailFocus = doc.activeElement || null;
    const next = withRoute(state.route, { detail: String(id || "") });
    writeRoute(next, { history: win.history, location: win.location });
    state.route = next;
    render();
    return handle;
  };

  handle.closeDetail = () => {
    if (!state.route.detail && (!detailRoot || detailRoot.hidden)) return handle;
    const next = withRoute(state.route, { detail: "" });
    writeRoute(next, { history: win.history, location: win.location, replace: true });
    state.route = next;
    if (typeof win.closeDrawer === "function") win.closeDrawer();
    state.lastLegacyDetail = "";
    render();
    if (state.lastDetailFocus && typeof state.lastDetailFocus.focus === "function") {
      state.lastDetailFocus.focus();
    }
    return handle;
  };

  handle.render = () => render();

  handle.destroy = () => {
    state.listeners.forEach(off => {
      try { off(); } catch (_) { /* 监听器已移除 */ }
    });
    state.listeners.length = 0;
    const style = byId(doc, "maw-vnext-styles");
    if (style && style.parentNode) style.parentNode.removeChild(style);
    if (root) root.hidden = true;
    if (detailRoot) detailRoot.hidden = true;
    return handle;
  };

  return handle;
}

/** 便捷入口：创建并立即挂载。 */
export function mountShell(options = {}) {
  return createShell(options).mount();
}

export { DATA_KINDS, errorState, needsProject, NO_PROJECT_STATE };
