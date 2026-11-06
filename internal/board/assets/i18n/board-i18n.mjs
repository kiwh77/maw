// Workbench 全局语言簿（VN-704）。
//
// 这里是统一布局内导航、页面标题、面包屑与状态提示的唯一语言源；
// 各业务视图仍保留自己的局部字典（nodes/tasks/runs/deliveries），
// 由 shell 把 `lang` 注入 ViewContext，不复制它们的词条。
//
// 约定：
//   - 语言只有 en / zh，与 legacy `#lang-select` 一致；
//   - 存储键沿用 `maw-board-lang`，刷新与 legacy 看板保持同一偏好；
//   - 未知键回退顺序：当前语言 → en → 键名本身，绝不渲染 `undefined`。

export const LANGS = Object.freeze(["en", "zh"]);
export const DEFAULT_LANG = "en";
export const LANG_STORAGE_KEY = "maw-board-lang";

export const DICTIONARY = Object.freeze({
  en: Object.freeze({
    "nav.workbench": "Workbench",
    "nav.flow": "Flow",
    "nav.control": "Control",
    "nav.archive": "Archive",
    "nav.review": "Stats",
    "nav.settings": "Settings",
    "aria.workbench": "Workbench views",
    "view.board": "Board",
    "view.topo": "Topology",
    "view.factory": "Workshop",
    "view.nodes": "Nodes",
    "view.tasks": "Tasks",
    "view.runs": "Runs",
    "view.deliveries": "Deliveries",
    "view.timeline": "Timeline",
    "view.activity": "Activity",
    "view.sessions": "Sessions",
    "view.analytics": "Analytics",
    "view.problems": "Problems",
    "view.logs": "Logs",
    "view.projects": "Projects",
    "view.settings": "Execution policy",
    "view.execution": "Execution view",
    "heading.board": "Task board",
    "heading.topo": "Dependency topology",
    "heading.factory": "Workshop",
    "heading.nodes": "Execution nodes",
    "heading.tasks": "Task queue",
    "heading.runs": "Runs",
    "heading.deliveries": "Deliveries",
    "heading.timeline": "Execution timeline",
    "heading.activity": "Activity heatmap",
    "heading.sessions": "Source sessions",
    "heading.analytics": "Delivery and usage analytics",
    "heading.problems": "Runtime problems",
    "heading.logs": "Hub event log",
    "heading.projects": "Projects",
    "heading.settings": "Execution policy",
    "heading.execution": "Iterations & execution",
    "eyebrow.board": "OPERATIONS / BOARD",
    "eyebrow.topo": "OPERATIONS / TOPOLOGY",
    "eyebrow.factory": "OPERATIONS / WORKSHOP",
    "eyebrow.nodes": "SETTINGS / NODES",
    "eyebrow.tasks": "OPERATIONS / TASKS",
    "eyebrow.runs": "OPERATIONS / RUNS",
    "eyebrow.deliveries": "OPERATIONS / DELIVERIES",
    "eyebrow.timeline": "STATS / TIMELINE",
    "eyebrow.activity": "STATS / ACTIVITY",
    "eyebrow.sessions": "STATS / SESSIONS",
    "eyebrow.analytics": "STATS / ANALYTICS",
    "eyebrow.problems": "STATS / PROBLEMS",
    "eyebrow.logs": "STATS / LOGS",
    "eyebrow.projects": "SETTINGS / PROJECTS",
    "eyebrow.settings": "SETTINGS / POLICY",
    "eyebrow.execution": "EXECUTION / ITERATIONS",
    "breadcrumb.workbench": "Workbench",
    "detail.open": "Open detail",
    "detail.back": "Back to list",
    "detail.close": "Close detail",
    "detail.missing": "This item is no longer in the current snapshot.",
    "detail.missing.hint": "Refresh the list; nothing was changed.",
    "data.loading": "Reading…",
    "data.error": "The hub did not answer this query.",
    "data.error.hint": "Data is shown as unavailable, not as success.",
    "data.unavailable": "Not provided by this hub build.",
    "data.unavailable.hint": "Nothing is marked done from missing data.",
    "lang.label": "Language",
    "lane.unknown": "Unverified",
    "status.pending": "Pending",
    "status.unknown": "Unknown",
    "review.project_required": "Select a project for stats.",
    "review.project_required.hint": "Timeline, activity, sessions and analytics are materialised per project; pick one in the filter bar and the address keeps it.",
    "review.readonly": "Stats views are read-only.",
    "review.readonly.hint": "Zooming and navigating windows never writes; nothing here changes task state.",
    "settings.readonly": "This hub build exposes no write path for policy.",
    "settings.readonly.hint": "Values below are the effective config read from the hub; change them through the CLI or /api/v1, and reload this page afterwards.",
    "settings.source.hub": "Policy source: hub config (unaffected by the project filter).",
  }),
  zh: Object.freeze({
    "nav.workbench": "工作台",
    "nav.flow": "流程",
    "nav.control": "控制",
    "nav.archive": "归档",
    "nav.review": "统计",
    "nav.settings": "设置",
    "aria.workbench": "工作台视图",
    "view.board": "看板",
    "view.topo": "依赖拓扑",
    "view.factory": "工坊",
    "view.nodes": "节点",
    "view.tasks": "任务",
    "view.runs": "运行",
    "view.deliveries": "交付",
    "view.timeline": "时间轴",
    "view.activity": "活动热图",
    "view.sessions": "来源会话",
    "view.analytics": "用量统计",
    "view.problems": "问题",
    "view.logs": "日志",
    "view.projects": "工程",
    "view.settings": "执行策略",
    "view.execution": "执行视图",
    "heading.board": "任务看板",
    "heading.topo": "依赖拓扑",
    "heading.factory": "工坊",
    "heading.nodes": "执行节点",
    "heading.tasks": "任务队列",
    "heading.runs": "运行记录",
    "heading.deliveries": "交付验收",
    "heading.timeline": "执行时间轴",
    "heading.activity": "活动热图",
    "heading.sessions": "来源会话",
    "heading.analytics": "交付与用量统计",
    "heading.problems": "运行问题",
    "heading.logs": "中枢事件日志",
    "heading.projects": "工程清单",
    "heading.settings": "执行策略",
    "heading.execution": "迭代与执行",
    "eyebrow.board": "OPERATIONS / BOARD",
    "eyebrow.topo": "OPERATIONS / TOPOLOGY",
    "eyebrow.factory": "OPERATIONS / WORKSHOP",
    "eyebrow.nodes": "SETTINGS / NODES",
    "eyebrow.tasks": "OPERATIONS / TASKS",
    "eyebrow.runs": "OPERATIONS / RUNS",
    "eyebrow.deliveries": "OPERATIONS / DELIVERIES",
    "eyebrow.timeline": "STATS / TIMELINE",
    "eyebrow.activity": "STATS / ACTIVITY",
    "eyebrow.sessions": "STATS / SESSIONS",
    "eyebrow.analytics": "STATS / ANALYTICS",
    "eyebrow.problems": "STATS / PROBLEMS",
    "eyebrow.logs": "STATS / LOGS",
    "eyebrow.projects": "SETTINGS / PROJECTS",
    "eyebrow.settings": "SETTINGS / POLICY",
    "eyebrow.execution": "EXECUTION / ITERATIONS",
    "breadcrumb.workbench": "工作台",
    "detail.open": "查看详情",
    "detail.back": "返回列表",
    "detail.close": "关闭详情",
    "detail.missing": "当前快照里已找不到这一项。",
    "detail.missing.hint": "刷新列表即可；没有做任何修改。",
    "data.loading": "正在读取…",
    "data.error": "中枢没有回应这次查询。",
    "data.error.hint": "数据标记为不可用，不会被当作成功。",
    "data.unavailable": "当前中枢版本未提供该数据。",
    "data.unavailable.hint": "缺失的数据不会被标成完成。",
    "lang.label": "语言",
    "lane.unknown": "待核实",
    "status.pending": "处理中",
    "status.unknown": "未知",
    "review.project_required": "先选择一个工程再看统计。",
    "review.project_required.hint": "时间轴、活动、会话与用量都按工程物化；在上方筛选里选定工程，地址栏会一直带着它。",
    "review.readonly": "统计视图是只读展示。",
    "review.readonly.hint": "缩放与翻窗口都不写盘，这里不会改动任何任务状态。",
    "settings.readonly": "当前中枢版本没有开放策略写入入口。",
    "settings.readonly.hint": "下面是中枢回显的生效配置；改动请走 CLI 或 /api/v1，改完刷新本页即可。",
    "settings.source.hub": "策略来源：中枢配置（不受工程筛选影响）。",
  }),
});

/** 把任意值收敛到受支持的语言；非法值一律回退默认语言。 */
export function normalizeLang(value) {
  return value === "zh" ? "zh" : "en";
}

/** 读取语言偏好：`?lang=` → 存储 → 浏览器语言 → 默认。 */
export function readLangPreference({ search = "", storage = null, navigatorLanguage = "" } = {}) {
  try {
    const query = new URLSearchParams(search).get("lang");
    if (LANGS.includes(query)) return query;
  } catch (_) {
    // 非法 query 串按未设置处理。
  }
  try {
    const saved = storage && storage.getItem(LANG_STORAGE_KEY);
    if (LANGS.includes(saved)) return saved;
  } catch (_) {
    // 存储被禁用时继续按浏览器语言判断。
  }
  if (String(navigatorLanguage || "").toLowerCase().startsWith("zh")) return "zh";
  return DEFAULT_LANG;
}

/** 写入语言偏好；存储不可用时静默失败。 */
export function persistLang(lang, storage = null) {
  const value = normalizeLang(lang);
  try {
    if (storage) storage.setItem(LANG_STORAGE_KEY, value);
  } catch (_) {
    // 偏好无法持久化不影响本次切换。
  }
  return value;
}

/** 词条插值：`{n}` 之类的占位符替换为参数值。 */
function interpolate(text, params) {
  if (!params || typeof params !== "object") return text;
  return text.replace(/\{(\w+)\}/g, (match, name) => (
    Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : match
  ));
}

/** 翻译词条，回退顺序为当前语言 → en → 键名。 */
export function translate(key, lang = DEFAULT_LANG, params = {}) {
  const language = normalizeLang(lang);
  const pack = DICTIONARY[language] || DICTIONARY[DEFAULT_LANG];
  const base = DICTIONARY[DEFAULT_LANG];
  const text = pack[key] != null ? pack[key] : (base[key] != null ? base[key] : key);
  return interpolate(text, params);
}

/** 生成绑定语言的翻译函数，便于注入 ViewContext。 */
export function createTranslator(lang = DEFAULT_LANG) {
  const language = normalizeLang(lang);
  return (key, params) => translate(key, language, params);
}

/** `<html lang>` 的规范取值。 */
export function documentLang(lang) {
  return normalizeLang(lang) === "zh" ? "zh-CN" : "en";
}

/** 把语言写到文档根元素；文档不可用时返回规范值。 */
export function applyDocumentLang(documentRef, lang) {
  const value = documentLang(lang);
  try {
    if (documentRef && documentRef.documentElement) {
      documentRef.documentElement.lang = value;
    }
  } catch (_) {
    // 只读文档根元素时忽略。
  }
  return value;
}
