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
 * @property {string} [navKey] 侧栏菜单名键；缺省回退 titleKey
 * @property {string} headingKey 页面主标题键（vnext 用）
 * @property {string} descKey  页面说明键（vnext 用）
 * @property {string} eyebrowKey 页面眉标键（vnext 用）
 * @property {string} symbol   导航图标字符
 * @property {string} dataKind 需要加载的数据种类（legacy 为空）
 * @property {boolean} requiresProject 是否必须先选定工程
 */

/**
 * 冷启动的默认视图（EV-003）。
 *
 * 中枢首页先回答「这一批题在发生什么」：执行视图按迭代聚合任务、运行与交付，
 * 旧看板（`board`）保留为次级入口——它在侧栏置顶，也可以直接用 `?view=board` 深链进入。
 * 默认视图不参与查询串序列化，所以执行视图的地址就是不带 `view` 参数的 `/`。
 */
export const DEFAULT_VIEW = "execution";

/**
 * 无工程时的默认视图。
 *
 * 与 `DEFAULT_VIEW` 同值：执行视图在「全部工程」下按工程逐个取数再合并，
 * 没有工程也是有内容的正常状态（见 view-context 的 PROJECT_SCOPED_KINDS）。
 */
export const DEFAULT_VIEW_NO_PROJECT = "execution";

/** 侧栏不展示的分组：flow 只留置顶的工作台；archive 保留深链但不进菜单。 */
export const SIDEBAR_HIDDEN_GROUPS = Object.freeze(["flow", "archive"]);

/**
 * 侧栏不展示的视图 id（深链 `?view=` 仍可渲染）。
 * tasks/runs/deliveries 已并入执行视图；不再占控制组菜单。
 */
export const SIDEBAR_HIDDEN_IDS = Object.freeze(["tasks", "runs", "deliveries"]);

/**
 * 侧栏置顶项：不归属任何分组，直接排在导航最前，不带分组标题。
 *
 * 整个 flow 组在侧栏被隐藏（入口在工具栏的视图切换器里），看板如果没有置顶项，
 * 一旦切到别的视图就回不去。这里把它单独提成置顶菜单，菜单名用 `nav.workbench`
 * （工作台），指向 board 视图本身。EV-003 之后 board 不再是默认视图，
 * 所以它的地址会显式带上 `?view=board`——这正是「深链旧看板」要走的那条路。
 */
export const SIDEBAR_PINNED_IDS = Object.freeze(["board"]);

/**
 * 导航分组。
 *
 * 分组顺序即导航顺序：流程 → 统计 → 设置。
 * 旧「控制」组已拆除：节点迁入设置；任务/运行/交付进 archive（深链保留）。
 * language 键统一为 `nav.<group>`。
 */
export const VIEW_GROUPS = Object.freeze([
  Object.freeze({ id: "flow", labelKey: "nav.flow" }),
  Object.freeze({ id: "archive", labelKey: "nav.archive" }),
  Object.freeze({ id: "review", labelKey: "nav.review" }),
  Object.freeze({ id: "settings", labelKey: "nav.settings" }),
]);

/** @type {ReadonlyArray<ViewDef>} */
export const VIEW_DEFS = Object.freeze([
  Object.freeze({ id: "board", group: "flow", legacy: true, titleKey: "view.board", navKey: "nav.workbench", headingKey: "heading.board", descKey: "desc.board", eyebrowKey: "eyebrow.board", symbol: "▦", dataKind: "", requiresProject: false }),
  Object.freeze({ id: "factory", group: "flow", legacy: true, titleKey: "view.factory", headingKey: "heading.factory", descKey: "desc.factory", eyebrowKey: "eyebrow.factory", symbol: "⚒", dataKind: "", requiresProject: false }),
  Object.freeze({ id: "topo", group: "flow", legacy: true, titleKey: "view.topo", headingKey: "heading.topo", descKey: "desc.topo", eyebrowKey: "eyebrow.topo", symbol: "⤳", dataKind: "", requiresProject: true }),
  // 深链保留：侧栏隐藏（SIDEBAR_HIDDEN_IDS / archive 组）。
  Object.freeze({ id: "tasks", group: "archive", legacy: false, titleKey: "view.tasks", headingKey: "heading.tasks", descKey: "desc.tasks", eyebrowKey: "eyebrow.tasks", symbol: "☰", dataKind: "tasks", requiresProject: false }),
  Object.freeze({ id: "runs", group: "archive", legacy: false, titleKey: "view.runs", headingKey: "heading.runs", descKey: "desc.runs", eyebrowKey: "eyebrow.runs", symbol: "⌁", dataKind: "runs", requiresProject: false }),
  Object.freeze({ id: "deliveries", group: "archive", legacy: false, titleKey: "view.deliveries", headingKey: "heading.deliveries", descKey: "desc.deliveries", eyebrowKey: "eyebrow.deliveries", symbol: "✓", dataKind: "deliveries", requiresProject: false }),
  // 统计：时间轴／热图／会话／用量（保留）+ 问题／日志（新增）。全部只读，按工程物化。
  Object.freeze({ id: "timeline", group: "review", legacy: false, titleKey: "view.timeline", headingKey: "heading.timeline", descKey: "desc.timeline", eyebrowKey: "eyebrow.timeline", symbol: "⌚", dataKind: "timeline", requiresProject: true }),
  Object.freeze({ id: "activity", group: "review", legacy: false, titleKey: "view.activity", headingKey: "heading.activity", descKey: "desc.activity", eyebrowKey: "eyebrow.activity", symbol: "▩", dataKind: "activity", requiresProject: true }),
  Object.freeze({ id: "sessions", group: "review", legacy: false, titleKey: "view.sessions", headingKey: "heading.sessions", descKey: "desc.sessions", eyebrowKey: "eyebrow.sessions", symbol: "↳", dataKind: "sessions", requiresProject: true }),
  Object.freeze({ id: "analytics", group: "review", legacy: false, titleKey: "view.analytics", headingKey: "heading.analytics", descKey: "desc.analytics", eyebrowKey: "eyebrow.analytics", symbol: "◫", dataKind: "analytics", requiresProject: true }),
  Object.freeze({ id: "problems", group: "review", legacy: false, titleKey: "view.problems", headingKey: "heading.problems", descKey: "desc.problems", eyebrowKey: "eyebrow.problems", symbol: "!", dataKind: "problems", requiresProject: false }),
  Object.freeze({ id: "logs", group: "review", legacy: false, titleKey: "view.logs", headingKey: "heading.logs", descKey: "desc.logs", eyebrowKey: "eyebrow.logs", symbol: "≡", dataKind: "logs", requiresProject: false }),
  // 设置：工程清单 + 执行节点 + 受管执行策略。
  Object.freeze({ id: "projects", group: "settings", legacy: false, titleKey: "view.projects", headingKey: "heading.projects", descKey: "desc.projects", eyebrowKey: "eyebrow.projects", symbol: "❏", dataKind: "projects", requiresProject: false }),
  Object.freeze({ id: "nodes", group: "settings", legacy: false, titleKey: "view.nodes", headingKey: "heading.nodes", descKey: "desc.nodes", eyebrowKey: "eyebrow.nodes", symbol: "▤", dataKind: "nodes", requiresProject: false }),
  Object.freeze({ id: "settings", group: "settings", legacy: false, titleKey: "view.settings", headingKey: "heading.settings", descKey: "desc.settings", eyebrowKey: "eyebrow.settings", symbol: "⚙", dataKind: "settings", requiresProject: false }),
  // 执行视图（EV-001）：按迭代聚合任务／运行／交付，是主视图而不是筛选目标。
  // 归入 flow 组：该组不进侧栏（侧栏只留置顶的工作台），所以它不占新的导航入口，
  // 用 `?view=execution` 进入；EV-003 会把默认落地页切到它。
  Object.freeze({ id: "execution", group: "flow", legacy: false, titleKey: "view.execution", headingKey: "heading.execution", descKey: "desc.execution", eyebrowKey: "eyebrow.execution", symbol: "◈", dataKind: "execution", requiresProject: false }),
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

/**
 * 导航标签键：置顶项可以另起一个菜单名（如「工作台」），否则用视图标题。
 * 导航、语言切换与 `navItems` 都走这里，避免三处各写一遍 `navKey || titleKey`。
 */
export function navLabelKey(def) {
  if (!def) return "";
  return def.navKey || def.titleKey;
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
    /** 侧栏置顶项：按定义顺序，渲染时不套分组标题。 */
    sidebarPinned() {
      return list.filter(def => SIDEBAR_PINNED_IDS.indexOf(def.id) >= 0);
    },
    /** 侧栏导航：只留可见分组，且把置顶项与隐藏 id 从分组里摘掉，避免重复入口。 */
    sidebarNavModel() {
      return this.navModel()
        .filter(group => SIDEBAR_HIDDEN_GROUPS.indexOf(group.id) < 0)
        .map(group => ({
          ...group,
          items: group.items.filter(def =>
            SIDEBAR_PINNED_IDS.indexOf(def.id) < 0 &&
            SIDEBAR_HIDDEN_IDS.indexOf(def.id) < 0
          ),
        }))
        .filter(group => group.items.length > 0);
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
    label: navLabelKey(def),
    lang,
  }));
}
