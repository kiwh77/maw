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
    "nav.review": "Review",
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
    "view.projects": "Projects",
    "view.settings": "Execution policy",
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
    "heading.projects": "Projects",
    "heading.settings": "Execution policy",
    "desc.board": "Track task flow and coordinate every Agent.",
    "desc.topo": "Dependencies left to right; missing prerequisites stay visible as stubs.",
    "desc.factory": "Watch Agents carry work between stages.",
    "desc.nodes": "Presence, auth, capability and capacity stay separate; a task is only executable when all hold.",
    "desc.tasks": "Dispatch, dependencies and write conflicts for every task.",
    "desc.runs": "Process truth, cancellation and reconciliation for every run.",
    "desc.deliveries": "Artifacts, verification evidence and acceptance are distinct stages.",
    "desc.timeline": "Per-run intervals by hour; real execution, explicit waits, blocked phases and unknown gaps stay distinct.",
    "desc.activity": "One day at a glance; a missing hour is not a quiet hour.",
    "desc.sessions": "Sessions and turns are observed evidence; a session may span several tasks and a task several sessions.",
    "desc.analytics": "Every figure carries its unit and coverage; unknown components are never counted as zero.",
    "desc.projects": "Registered projects in this hub; auto execution and capacity are shown separately.",
    "desc.settings": "Managed execution policy comes from hub config; the hub build decides whether this page can write.",
    "eyebrow.board": "OPERATIONS / BOARD",
    "eyebrow.topo": "OPERATIONS / TOPOLOGY",
    "eyebrow.factory": "OPERATIONS / WORKSHOP",
    "eyebrow.nodes": "OPERATIONS / NODES",
    "eyebrow.tasks": "OPERATIONS / TASKS",
    "eyebrow.runs": "OPERATIONS / RUNS",
    "eyebrow.deliveries": "OPERATIONS / DELIVERIES",
    "eyebrow.timeline": "REVIEW / TIMELINE",
    "eyebrow.activity": "REVIEW / ACTIVITY",
    "eyebrow.sessions": "REVIEW / SESSIONS",
    "eyebrow.analytics": "REVIEW / ANALYTICS",
    "eyebrow.projects": "SETTINGS / PROJECTS",
    "eyebrow.settings": "SETTINGS / POLICY",
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
    "review.project_required": "Select a project to review.",
    "review.project_required.hint": "Timeline, activity, sessions and analytics are materialised per project; pick one in the filter bar and the address keeps it.",
    "review.readonly": "History review is read-only.",
    "review.readonly.hint": "Zooming and navigating windows never writes; nothing here changes task state.",
    "settings.readonly": "This hub build exposes no write path for policy.",
    "settings.readonly.hint": "Values below are the effective config read from the hub; change them through the CLI or /api/v1, and reload this page afterwards.",
    "settings.source.hub": "Policy source: hub config (unaffected by the project filter).",
    "projects.policy_missing": "This snapshot carries no per-project execution policy.",
    "projects.policy_missing.hint": "Values stay blank rather than defaulting to off; open the policy page or query /api/runtime for the effective config.",
  }),
  zh: Object.freeze({
    "nav.workbench": "工作台",
    "nav.flow": "流程",
    "nav.control": "控制",
    "nav.review": "回顾",
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
    "view.projects": "工程",
    "view.settings": "执行策略",
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
    "heading.projects": "工程清单",
    "heading.settings": "执行策略",
    "desc.board": "跟踪任务流转，协调每个 Agent。",
    "desc.topo": "依赖自左向右；缺失的前置保留为占位节点。",
    "desc.factory": "看 Agent 在各阶段之间搬运任务。",
    "desc.nodes": "在线、认证、能力、容量分开看，四项都成立才接任务。",
    "desc.tasks": "逐题查看派发条件、依赖与写盘冲突。",
    "desc.runs": "进程事实、取消与对账分开呈现。",
    "desc.deliveries": "产物、验证证据与验收结论各是独立阶段。",
    "desc.timeline": "按小时排布每次执行；真实执行、明确等待、业务阻塞与未知片段分开显示。",
    "desc.activity": "一天一屏看活跃时段；缺测的小时不是安静的小时。",
    "desc.sessions": "会话与轮次是观测证据；一个会话可跨多个任务，一个任务也可跨多个会话。",
    "desc.analytics": "每个数字都带单位与覆盖范围；未知分量不会被算成零。",
    "desc.projects": "本机中枢已登记的工程；自动执行与容量分开呈现。",
    "desc.settings": "受管执行策略来自中枢配置；本页能否写入由中枢版本决定。",
    "eyebrow.board": "OPERATIONS / BOARD",
    "eyebrow.topo": "OPERATIONS / TOPOLOGY",
    "eyebrow.factory": "OPERATIONS / WORKSHOP",
    "eyebrow.nodes": "OPERATIONS / NODES",
    "eyebrow.tasks": "OPERATIONS / TASKS",
    "eyebrow.runs": "OPERATIONS / RUNS",
    "eyebrow.deliveries": "OPERATIONS / DELIVERIES",
    "eyebrow.timeline": "REVIEW / TIMELINE",
    "eyebrow.activity": "REVIEW / ACTIVITY",
    "eyebrow.sessions": "REVIEW / SESSIONS",
    "eyebrow.analytics": "REVIEW / ANALYTICS",
    "eyebrow.projects": "SETTINGS / PROJECTS",
    "eyebrow.settings": "SETTINGS / POLICY",
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
    "review.project_required": "先选择一个工程再回顾。",
    "review.project_required.hint": "时间轴、活动、会话与统计都按工程物化；在上方筛选里选定工程，地址栏会一直带着它。",
    "review.readonly": "历史回顾是只读展示。",
    "review.readonly.hint": "缩放与翻窗口都不写盘，这里不会改动任何任务状态。",
    "settings.readonly": "当前中枢版本没有开放策略写入入口。",
    "settings.readonly.hint": "下面是中枢回显的生效配置；改动请走 CLI 或 /api/v1，改完刷新本页即可。",
    "settings.source.hub": "策略来源：中枢配置（不受工程筛选影响）。",
    "projects.policy_missing": "当前快照没有带各工程的执行策略。",
    "projects.policy_missing.hint": "数值保持为空，不会默认成「关闭」；有效配置请看执行策略页或查询 /api/runtime。",
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
