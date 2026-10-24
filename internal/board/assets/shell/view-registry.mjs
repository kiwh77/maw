// Workbench 视图注册表（VN-704 / VN-705）。
//
// 一个统一布局里同时存在两类视图：
//   - legacy（board / topo / factory）：仍由内嵌 board-app.js 渲染，shell 只做导航与显隐；
//   - vnext（其余全部）：由 assets/views 下的纯渲染函数产出 HTML，
//     shell 注入 ViewContext 后挂载到 #vnext-view。
//
// 注册表是新旧视图的唯一清单：导航、路由回退、标题与语言键都从这里取，
// 不再在页面里各写一份 `if (view === ...)`。
//
// VN-705 把回顾、统计与工程设置也登记进来。这些视图与执行视图**共用同一套
// 导航、筛选与路由**：切页不丢工程筛选，地址栏能原样恢复现场。
// 其中观测类视图（回顾／统计）按工程物化，没有工程就没有可查的事实——
// 这一点用 `requiresProject` 声明，由挂载层给出「先选工程」而不是空成功。

/**
 * @typedef {Object} ViewDef
 * @property {string} id       路由与导航使用的稳定 id
 * @property {string} group    导航分组（见 VIEW_GROUPS）
 * @property {boolean} legacy  是否由 legacy 应用渲染
 * @property {string} titleKey 全局语言簿中的标题键
 * @property {string} headingKey 页面主标题键（vnext 用）
 * @property {string} descKey  页面说明键（vnext 用）
 * @property {string} eyebrowKey 页面眉标键（vnext 用）
 * @property {string} symbol   导航图标字符
 * @property {string} dataKind 需要加载的数据种类（legacy 为空）
 * @property {boolean} requiresProject 是否必须先选定工程
 */

/** 首屏默认视图：保持旧直达链接与旧看板行为。 */
export const DEFAULT_VIEW = "board";

/**
 * 导航分组。
 *
 * 分组顺序即导航顺序：先看流程（谁在干什么），再看控制（节点／任务／运行／交付），
 * 然后是回顾统计，最后是设置。language 键统一为 `nav.<group>`，不再在 navModel 里
 * 用三元表达式逐个猜——新增一组就必须在语言簿里补齐中英两版。
 */
export const VIEW_GROUPS = Object.freeze([
  Object.freeze({ id: "flow", labelKey: "nav.flow" }),
  Object.freeze({ id: "control", labelKey: "nav.control" }),
  Object.freeze({ id: "review", labelKey: "nav.review" }),
  Object.freeze({ id: "settings", labelKey: "nav.settings" }),
]);

/** @type {ReadonlyArray<ViewDef>} */
export const VIEW_DEFS = Object.freeze([
  Object.freeze({ id: "board", group: "flow", legacy: true, titleKey: "view.board", headingKey: "heading.board", descKey: "desc.board", eyebrowKey: "eyebrow.board", symbol: "▦", dataKind: "", requiresProject: false }),
  Object.freeze({ id: "factory", group: "flow", legacy: true, titleKey: "view.factory", headingKey: "heading.factory", descKey: "desc.factory", eyebrowKey: "eyebrow.factory", symbol: "⚒", dataKind: "", requiresProject: false }),
  Object.freeze({ id: "topo", group: "flow", legacy: true, titleKey: "view.topo", headingKey: "heading.topo", descKey: "desc.topo", eyebrowKey: "eyebrow.topo", symbol: "⤳", dataKind: "", requiresProject: true }),
  Object.freeze({ id: "nodes", group: "control", legacy: false, titleKey: "view.nodes", headingKey: "heading.nodes", descKey: "desc.nodes", eyebrowKey: "eyebrow.nodes", symbol: "▤", dataKind: "nodes", requiresProject: false }),
  Object.freeze({ id: "tasks", group: "control", legacy: false, titleKey: "view.tasks", headingKey: "heading.tasks", descKey: "desc.tasks", eyebrowKey: "eyebrow.tasks", symbol: "☰", dataKind: "tasks", requiresProject: false }),
  Object.freeze({ id: "runs", group: "control", legacy: false, titleKey: "view.runs", headingKey: "heading.runs", descKey: "desc.runs", eyebrowKey: "eyebrow.runs", symbol: "⌁", dataKind: "runs", requiresProject: false }),
  Object.freeze({ id: "deliveries", group: "control", legacy: false, titleKey: "view.deliveries", headingKey: "heading.deliveries", descKey: "desc.deliveries", eyebrowKey: "eyebrow.deliveries", symbol: "✓", dataKind: "deliveries", requiresProject: false }),
  // 回顾：时间轴、活动热图、会话轮次（F34–F38）。全部只读，按工程物化。
  Object.freeze({ id: "timeline", group: "review", legacy: false, titleKey: "view.timeline", headingKey: "heading.timeline", descKey: "desc.timeline", eyebrowKey: "eyebrow.timeline", symbol: "⌚", dataKind: "timeline", requiresProject: true }),
  Object.freeze({ id: "activity", group: "review", legacy: false, titleKey: "view.activity", headingKey: "heading.activity", descKey: "desc.activity", eyebrowKey: "eyebrow.activity", symbol: "▩", dataKind: "activity", requiresProject: true }),
  Object.freeze({ id: "sessions", group: "review", legacy: false, titleKey: "view.sessions", headingKey: "heading.sessions", descKey: "desc.sessions", eyebrowKey: "eyebrow.sessions", symbol: "↳", dataKind: "sessions", requiresProject: true }),
  // 统计：工程与工具用量（F39–F43），单位与覆盖范围以响应回显为准。
  Object.freeze({ id: "analytics", group: "review", legacy: false, titleKey: "view.analytics", headingKey: "heading.analytics", descKey: "desc.analytics", eyebrowKey: "eyebrow.analytics", symbol: "◫", dataKind: "analytics", requiresProject: true }),
  // 工程设置：工程清单 + 受管执行策略。不是筛选目标，而是配置入口。
  Object.freeze({ id: "projects", group: "settings", legacy: false, titleKey: "view.projects", headingKey: "heading.projects", descKey: "desc.projects", eyebrowKey: "eyebrow.projects", symbol: "❏", dataKind: "projects", requiresProject: false }),
  Object.freeze({ id: "settings", group: "settings", legacy: false, titleKey: "view.settings", headingKey: "heading.settings", descKey: "desc.settings", eyebrowKey: "eyebrow.settings", symbol: "⚙", dataKind: "settings", requiresProject: false }),
]);

/** 旧视图容器 id，切到 vnext 视图时整体隐藏。 */
export const LEGACY_CONTAINERS = Object.freeze(["columns", "topology", "factory"]);

/** 旧视图的切换按钮 id，回切时通过点击保留 legacy 内部状态。 */
export const LEGACY_TOGGLES = Object.freeze({ board: "view-board", topo: "view-topo", factory: "view-factory" });

/** 分组语言键；分组未知时回退到空串（渲染层据此跳过标题）。 */
export function groupLabelKey(groupId) {
  const found = VIEW_GROUPS.find(group => group.id === groupId);
  return found ? found.labelKey : "";
}

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
    /** 该视图是否必须先有工程上下文。 */
    requiresProject(id) {
      const def = byId.get(id);
      return Boolean(def && def.requiresProject);
    },
    /** 导航模型：按分组返回标题键与条目，保持定义顺序。 */
    navModel() {
      const groups = [];
      list.forEach(def => {
        let group = groups.find(item => item.id === def.group);
        if (!group) {
          group = { id: def.group, labelKey: groupLabelKey(def.group), items: [] };
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
