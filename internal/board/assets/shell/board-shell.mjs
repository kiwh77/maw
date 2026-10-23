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
  LOADING_STATE,
  errorState,
  DATA_KINDS,
} from "./view-context.mjs";
import { renderNodeList, renderNodeDetail } from "../views/nodes/nodes-view.mjs";
import { renderTaskList } from "../views/tasks/tasks-view.mjs";
import { renderRunList, renderRunDetail } from "../views/runs/runs-view.mjs";
import { renderDeliveryList, renderDeliveryDetail } from "../views/deliveries/deliveries-view.mjs";

/** vnext 视图 id → 列表渲染函数。 */
const LIST_RENDERERS = Object.freeze({
  nodes: renderNodeList,
  tasks: renderTaskList,
  runs: renderRunList,
  deliveries: renderDeliveryList,
});

/** vnext 视图 id → 详情渲染函数；tasks 走 legacy 抽屉，故不在此表。 */
const DETAIL_RENDERERS = Object.freeze({
  nodes: renderNodeDetail,
  runs: renderRunDetail,
  deliveries: renderDeliveryDetail,
});

const TASKS_SOURCE = "/api/board";
const KIND_ENDPOINTS = Object.freeze({
  nodes: "/api/v1/nodes",
  runs: "/api/v1/runs",
  deliveries: "/api/v1/deliveries",
});

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
  async function getJSON(url) {
    if (!doFetch) return { unavailable: true };
    try {
      const res = await doFetch(url, { headers: { Accept: "application/json" } });
      if (!res || !res.ok) return { unavailable: true, status: res ? res.status : 0 };
      return await res.json();
    } catch (_) {
      return { unavailable: true, status: 0 };
    }
  }
  return {
    async tasks(route) {
      const params = new URLSearchParams();
      if (route.project) params.set("project", route.project);
      if (route.agent) params.set("agent", route.agent);
      if (route.status) params.set("status", route.status);
      const query = params.toString();
      return getJSON(TASKS_SOURCE + (query ? "?" + query : ""));
    },
    async nodes() { return getJSON(KIND_ENDPOINTS.nodes); },
    async runs() { return getJSON(KIND_ENDPOINTS.runs); },
    async deliveries() { return getJSON(KIND_ENDPOINTS.deliveries); },
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
    token: 0,
    lastLegacyDetail: "",
    lastDetailFocus: null,
    listeners: [],
  };

  const handle = {
    registry,
    route: () => ({ ...state.route }),
    lang: () => state.lang,
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
    if (detailRoot) return detailRoot;
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
    (doc.body || doc.documentElement).appendChild(panel);
    return panel;
  }

  function renderNav() {
    const nav = ensureNav();
    if (!nav) return;
    const label = byId(doc, "workbench-label");
    if (label) label.textContent = t("nav.workbench");
    nav.classList.add("vnext-nav");
    nav.setAttribute("aria-label", t("aria.workbench"));
    nav.innerHTML = registry.navModel().map(group =>
      '<div class="section-label">' + esc(t(group.labelKey)) + "</div>" +
      group.items.map(def => {
        const active = def.id === state.route.view;
        return '<button type="button" class="nav-item' + (active ? " active" : "") + '"' +
          ' data-view="' + esc(def.id) + '"' +
          (active ? ' aria-current="page"' : "") + ">" +
          '<span class="symbol" aria-hidden="true">' + esc(def.symbol) + "</span>" +
          "<span>" + esc(t(def.titleKey)) + "</span></button>";
      }).join("")
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
    const name = byId(doc, "workspace-name");
    if (name) {
      name.dataset.locked = "1";
      name.textContent = t(def.titleKey);
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

  function showLegacy(def) {
    if (root) root.hidden = true;
    if (detailRoot) detailRoot.hidden = true;
    const hints = byId(doc, "hint-center");
    if (hints) hints.hidden = false;
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
      return stateForKind(kind, fixtures[kind]);
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
    });
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
    if (def.legacy || !id) {
      panel.hidden = true;
      if (def.id !== "tasks" && state.lastLegacyDetail && typeof win.closeDrawer === "function") {
        win.closeDrawer();
      }
      state.lastLegacyDetail = "";
      return;
    }
    if (def.id === "tasks") {
      panel.hidden = true;
      if (state.lastLegacyDetail !== id) {
        state.lastLegacyDetail = id;
        if (typeof win.openDrawer === "function") win.openDrawer(id, state.route.project || "");
      }
      return;
    }
    const renderDetailFn = DETAIL_RENDERERS[def.id];
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
    state.states[kind] = LOADING_STATE;
    const token = ++state.token;
    if (root && state.route.view === kind && !registry.get(kind)?.legacy) {
      root.innerHTML = statusHint(LOADING_STATE, t) + LIST_RENDERERS[kind](LOADING_STATE, { lang: state.lang });
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
    state.states[kind] = stateForKind(kind, payload);
    if (root) {
      root.innerHTML = statusHint(state.states[kind], t) +
        LIST_RENDERERS[kind](state.states[kind], { lang: state.lang });
      decorateRows();
    }
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
    updateChrome(def);
    if (root) root.hidden = false;
    const viewState = currentState(def.id);
    if (root) {
      root.innerHTML = statusHint(viewState, t) +
        LIST_RENDERERS[def.id](viewState, { lang: state.lang });
      decorateRows();
    }
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

export { DATA_KINDS, errorState };
