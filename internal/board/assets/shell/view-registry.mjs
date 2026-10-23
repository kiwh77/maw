// Workbench 视图注册表（VN-704）。
//
// 一个统一布局里同时存在两类视图：
//   - legacy（board / topo / factory）：仍由内嵌 board-app.js 渲染，shell 只做导航与显隐；
//   - vnext（nodes / tasks / runs / deliveries）：由 assets/views 下的纯渲染函数产出 HTML，
//     shell 注入 ViewContext 后挂载到 #vnext-view。
//
// 注册表是新旧视图的唯一清单：导航、路由回退、标题与语言键都从这里取，
// 不再在页面里各写一份 `if (view === ...)`。

/**
 * @typedef {Object} ViewDef
 * @property {string} id       路由与导航使用的稳定 id
 * @property {"flow"|"control"} group 导航分组
 * @property {boolean} legacy  是否由 legacy 应用渲染
 * @property {string} titleKey 全局语言簿中的标题键
 * @property {string} headingKey 页面主标题键（vnext 用）
 * @property {string} descKey  页面说明键（vnext 用）
 * @property {string} eyebrowKey 页面眉标键（vnext 用）
 * @property {string} symbol   导航图标字符
 */

/** 首屏默认视图：保持旧直达链接与旧看板行为。 */
export const DEFAULT_VIEW = "board";

/** @type {ReadonlyArray<ViewDef>} */
export const VIEW_DEFS = Object.freeze([
  Object.freeze({ id: "board", group: "flow", legacy: true, titleKey: "view.board", headingKey: "heading.board", descKey: "desc.board", eyebrowKey: "eyebrow.board", symbol: "▦" }),
  Object.freeze({ id: "factory", group: "flow", legacy: true, titleKey: "view.factory", headingKey: "heading.factory", descKey: "desc.factory", eyebrowKey: "eyebrow.factory", symbol: "⚒" }),
  Object.freeze({ id: "topo", group: "flow", legacy: true, titleKey: "view.topo", headingKey: "heading.topo", descKey: "desc.topo", eyebrowKey: "eyebrow.topo", symbol: "⤳" }),
  Object.freeze({ id: "nodes", group: "control", legacy: false, titleKey: "view.nodes", headingKey: "heading.nodes", descKey: "desc.nodes", eyebrowKey: "eyebrow.nodes", symbol: "▤" }),
  Object.freeze({ id: "tasks", group: "control", legacy: false, titleKey: "view.tasks", headingKey: "heading.tasks", descKey: "desc.tasks", eyebrowKey: "eyebrow.tasks", symbol: "☰" }),
  Object.freeze({ id: "runs", group: "control", legacy: false, titleKey: "view.runs", headingKey: "heading.runs", descKey: "desc.runs", eyebrowKey: "eyebrow.runs", symbol: "⌁" }),
  Object.freeze({ id: "deliveries", group: "control", legacy: false, titleKey: "view.deliveries", headingKey: "heading.deliveries", descKey: "desc.deliveries", eyebrowKey: "eyebrow.deliveries", symbol: "✓" }),
]);

/** 旧视图容器 id，切到 vnext 视图时整体隐藏。 */
export const LEGACY_CONTAINERS = Object.freeze(["columns", "topology", "factory"]);

/** 旧视图的切换按钮 id，回切时通过点击保留 legacy 内部状态。 */
export const LEGACY_TOGGLES = Object.freeze({ board: "view-board", topo: "view-topo", factory: "view-factory" });

/** 构造注册表；允许测试传入裁剪后的定义集。 */
export function createRegistry(defs = VIEW_DEFS) {
  const list = defs.map(def => Object.freeze({ ...def }));
  const byId = new Map();
  list.forEach(def => {
    if (byId.has(def.id)) throw new Error("duplicate view id: " + def.id);
    byId.set(def.id, def);
  });
  return {
    all: list,
    ids: list.map(def => def.id),
    get(id) {
      return byId.get(id) || null;
    },
    legacyIds: list.filter(def => def.legacy).map(def => def.id),
    vnextIds: list.filter(def => !def.legacy).map(def => def.id),
    /** 导航模型：按分组返回标题键与条目，保持定义顺序。 */
    navModel() {
      const groups = [];
      list.forEach(def => {
        let group = groups.find(item => item.id === def.group);
        if (!group) {
          group = { id: def.group, labelKey: def.group === "flow" ? "nav.flow" : "nav.control", items: [] };
          groups.push(group);
        }
        group.items.push(def);
      });
      return groups;
    },
  };
}

/** 解析视图 id；未知或缺失一律回退默认视图。优先 vnext 注册表。 */
export function resolveView(registry, id) {
  const def = registry.get(id);
  if (def) return def;
  return registry.get(DEFAULT_VIEW) || registry.all[0] || null;
}

/** 视图是否由 legacy 应用渲染。 */
export function isLegacyView(registry, id) {
  const def = registry.get(id);
  return Boolean(def && def.legacy);
}

/** 供 ViewContext 使用的导航条目（只含 id 与标题键，不含 DOM）。 */
export function navItems(registry, lang) {
  return registry.all.map(def => ({
    id: def.id,
    group: def.group,
    legacy: def.legacy,
    symbol: def.symbol,
    label: def.titleKey,
    lang,
  }));
}
