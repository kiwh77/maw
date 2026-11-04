// 工程策略与保留设置（vnext / UI · VN-725）。
//
// 目标：把「自动执行、计算上限、节点容量、max_parallel 参考值」这四件经常被混为
// 一谈的东西分开展示，并让保存过程（脏、保存中、已保存、冲突、错误）可核对。
//
// 依据：
//   docs/ARCHITECTURE_VNEXT.zh-CN.md（受管执行默认关闭；上限是硬约束，参考值是建议）
//   docs/contracts/vnext/http/http-api-contract-v1.md（写请求带 revision，冲突返回 409）
//   docs/BOARD_DESIGN.md（UI-C：共用主题／布局组件，中英深浅，窄屏不溢出）
//
// 四个概念各自成立，互不推导：
//   auto     自动模式   —— 关闭 / 已开启 / 未就绪（平台不支持或配置未生效）
//   limit    计算上限   —— 同时受管执行的硬上限；开启自动模式时必须 ≥1
//   capacity 节点容量   —— 单节点执行槽，与全局上限不是同一件事
//   ref      max_parallel 参考值 —— 来自工程配置的建议值，**不是硬约束**
//
// 三条硬规则：
//   1. 自动执行默认关闭。表单永远不回填「默认开启」，未就绪时只解释原因，不给开关。
//   2. 保存必须带 revision。带旧 revision 的保存返回冲突，页面保留用户输入并要求重载，
//      不能静默覆盖别人刚改的设置。
//   3. 本页不出现秘密字段。令牌、密钥这类东西只能在 CLI／本机凭据库管理，
//      表单里出现一个 password 输入框就等于把凭据写进页面状态。
//
// 分区（EV-002 / F57）：受管执行 → 节点接入与容量 → 采集与保留 → 外观 → 系统 Agent 状态。
// 前三个是可改的字段区，后两个是只读声明区。外观与 Agent 状态**故意没有控件**：
// 主题／语言在顶栏（#theme-select / #lang-select），节点清单在 ?view=nodes，
// 复制一份到本页只会多出第二个真相。每个分区都用「入口／归属／边界」三行说清
// 这件事在哪儿改、谁说了算、本页不做什么。

export const AUTO_MODE = Object.freeze({ off: "off", on: "on", not_ready: "not_ready" });

/** 保存状态机：页面只在这些值之间迁移，不出现「成功但没保存」的中间态。 */
export const SAVE_STATUS = Object.freeze({
  idle: "idle",
  dirty: "dirty",
  saving: "saving",
  saved: "saved",
  error: "error",
  conflict: "conflict",
});

/** 校验码是封闭集合：文案在 I18N 里，判定只产出码。 */
export const ERROR_CODES = Object.freeze({
  limitInt: "limit_not_integer",
  limitMin: "limit_below_min",
  capacityInt: "capacity_not_integer",
  capacityMax: "capacity_above_max",
  retentionInt: "retention_not_integer",
  retentionRange: "retention_out_of_range",
  revisionMissing: "revision_missing",
  secretField: "secret_field",
});

/** 上限与容量共用的整数边界；写在一处，页面与测试不各记一套。 */
export const BOUNDS = Object.freeze({
  limitMin: 1,
  capacityMin: 0,
  capacityMax: 64,
  retentionDaysMin: 1,
  retentionDaysMax: 3650,
});

/** 秘密字段识别：键名命中即拒绝，值长什么样都不看。 */
const SECRET_KEY = /(token|secret|password|passwd|pat|api[-_]?key|credential)/i;

/**
 * 设置页的分区（EV-002 / F57）。
 *
 * 需求要的是「节点接入与容量、主题、语言收进设置页」，但收的不是控件：
 * 主题与语言仍由顶栏持有（`#theme-select` / `#lang-select`），节点清单仍由
 * `?view=nodes` 持有。设置页收敛的是**入口声明**——每个分区说清三件事：
 * 入口在哪、谁说了算、本页不做什么。照抄一套开关会让两处状态各走各的，
 * 比入口分散更难查。
 *
 * 顺序即阅读顺序：先放改得到的策略与字段，再放只读声明。
 */
export const SETTINGS_SECTIONS = Object.freeze([
  "managed",
  "nodes",
  "retention",
  "appearance",
  "agents",
]);

/** 每个分区固定三行说明链；缺一行就等于没说清归属。 */
const FACT_ROWS = Object.freeze(["entry", "owner", "boundary"]);

const I18N = {
  zh: {
    "settings.title": "工程策略与保留",
    "settings.subtitle": "自动模式、计算上限、节点容量、参考值分开看；它们不是同一个数。",
    "settings.loading": "正在读取设置…",
    "settings.error": "设置加载失败。",
    "settings.error.hint": "确认本机中枢在运行，然后重试。",
    "settings.expired": "设置已过期。",
    "settings.expired.hint": "重新载入后再改，避免用旧版本覆盖别人刚保存的设置。",
    "settings.unauthenticated": "未登录，无法查看设置。",
    "settings.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "settings.unknown": "设置状态未知。",
    "settings.unknown.hint": "缺少服务端返回的策略版本，不能猜测当前值。",
    "settings.empty": "还没有可编辑的工程策略。",
    "settings.empty.hint": "先注册工程，再回来配置执行策略。",
    "field.auto": "自动模式",
    "field.auto.hint": "默认关闭。开启后才会自动选题派发；关闭时仍可人工领取。系统 Agent 的阻塞自愈是另一个开关，见下方分区。",
    "field.limit": "计算上限",
    "field.limit.hint": "同时受管执行的硬上限；开启自动模式时必须至少 1。",
    "field.capacity": "节点容量",
    "field.capacity.hint": "单台节点能同时跑几个执行；与全局上限不是同一个数。",
    "field.ref": "max_parallel 参考值",
    "field.ref.hint": "工程配置里的建议值，仅供参考，不构成硬约束。",
    "field.retention": "保留天数",
    "field.retention.hint": "观测与事件明细保留多久；到期由维护任务清理。",
    "field.retention_runs": "保留运行数",
    "field.retention_runs.hint": "每个任务最多保留多少次运行记录。",
    "auto.off": "关闭",
    "auto.on": "已开启",
    "auto.not_ready": "未就绪",
    "auto.reason.platform": "平台未验证：受管执行只在 macOS 上实测过。",
    "auto.reason.config": "配置未生效：config.toml 里的 auto_enabled 仍为 false。",
    "save.idle": "尚未修改",
    "save.dirty": "有未保存的修改",
    "save.saving": "正在保存…",
    "save.saved": "已保存",
    "save.error": "保存失败，修改仍保留在表单里。",
    "save.conflict": "设置已被其他人更新，请重新载入后再保存。",
    "action.save": "保存设置",
    "action.reload": "重新载入",
    "notice.no_secret": "本页不含令牌／密钥字段：凭据只在 CLI 与本机凭据库管理。",
    "settings.readonly": "只读",
    "settings.readonly.hint": "中枢当前没有策略写入口：这里展示服务端回显的当前值，修改请走 CLI。",
    "col.item": "项",
    "col.value": "当前值",
    "col.note": "说明",
    "col.name": "名称",
    "col.status": "状态",
    "col.heartbeat": "最近心跳",
    "fact.entry": "入口",
    "fact.owner": "归属",
    "fact.boundary": "边界",
    "section.managed": "受管执行",
    "section.managed.desc": "自动模式与并发上限：决定中枢能不能自己开跑、同时跑几道题。",
    "section.managed.entry": "本页「受管执行」分区；数值由中枢 /api/runtime 回显",
    "section.managed.owner": "中枢运行时配置（config.toml 的 auto_enabled 与受管上限）",
    "section.managed.boundary": "改的是策略值，不代替节点就绪检查：节点没就绪时自动模式也派不出去。",
    "section.nodes": "节点接入与容量",
    "section.nodes.desc": "节点是全机可调度的执行实例；Agent 是工程内的工作者身份，两者不是一回事。",
    "section.nodes.entry": "节点清单在侧栏「节点」（?view=nodes）；容量字段在本分区",
    "section.nodes.owner": "节点登记与心跳由 CLI 和接入器负责，中枢只回显结果",
    "section.nodes.boundary": "本页没有节点登记写接口，不放一个存不下去的登记表单。",
    "section.retention": "采集与保留",
    "section.retention.desc": "观测与事件明细保留多久、每个任务留几次运行记录。",
    "section.retention.entry": "本页「采集与保留」分区",
    "section.retention.owner": "中枢维护任务按天清理，不靠手工删库",
    "section.retention.boundary": "清理不可回滚：调小之前先确认历史还要不要复盘。",
    "section.appearance": "外观",
    "section.appearance.desc": "主题与语言跟随机主界面走，本页只声明入口，不再摆一套重复的开关。",
    "section.appearance.entry": "顶栏右侧：主题 #theme-select、语言 #lang-select",
    "section.appearance.owner": "浏览器本机（主题存 localStorage 的 maw-theme）",
    "section.appearance.boundary": "本页不复制控件：两处开关必然不同步，只留一处才没有第二个真相。",
    "section.agents": "系统 Agent 状态",
    "section.agents.desc": "系统 Agent 是中枢内置的执行者身份；状态由心跳上报，本页只读。",
    "section.agents.entry": "侧栏 AGENTS 栏与「节点」视图；CLI 的 maw status 可核对",
    "section.agents.owner": "接入器心跳上报",
    "section.agents.boundary": "本页只读展示，不启停、不派发。",
    "section.agents.unknown": "未收到系统 Agent 快照：状态未知不等于没有 Agent。",
    "agents.state.on": "已开启",
    "agents.state.off": "已关闭",
    "agents.state.unknown": "未知",
    "agents.state.hint.on": "阻塞自愈循环在跑：卡住的任务会被自动诊断与编排。",
    "agents.state.hint.off": "阻塞自愈循环没开：blocked 任务不会被自动处理。",
    "agents.state.hint.unknown": "服务端没回显系统 Agent 开关，这里不替你判断开还是关。",
    "agents.does.title": "它管什么",
    "agents.does.1": "管：任务 blocked 时自动入队，并编排诊断／拆题／运维。",
    "agents.does.2": "不管：自动选题派发——那是「受管执行」分区里的自动模式。",
    "agents.does.3": "两个开关互不推导：只开自愈不会派任务，只开自动模式也不会自愈。",
    "err.limit_not_integer": "计算上限必须是整数。",
    "err.limit_below_min": "计算上限至少为 1；要关闭自动执行请关自动模式，而不是填 0。",
    "err.capacity_not_integer": "节点容量必须是整数。",
    "err.capacity_above_max": "节点容量超出允许范围。",
    "err.retention_not_integer": "保留值必须是整数。",
    "err.retention_out_of_range": "保留天数超出允许范围。",
    "err.revision_missing": "缺少设置版本（revision），不能保存。",
    "err.secret_field": "本页不允许秘密字段：凭据请用 CLI 或本机凭据库。",
  },
  en: {
    "settings.title": "Execution policy & retention",
    "settings.subtitle": "Auto mode, execution limit, node capacity and the reference value are different things.",
    "settings.loading": "Loading settings…",
    "settings.error": "Failed to load settings.",
    "settings.error.hint": "Check that the local hub is running, then retry.",
    "settings.expired": "Settings are stale.",
    "settings.expired.hint": "Reload before editing so you do not overwrite someone else's save.",
    "settings.unauthenticated": "Not signed in; settings are unavailable.",
    "settings.unauthenticated.hint": "Use a local session or token, then retry.",
    "settings.unknown": "Settings state unknown.",
    "settings.unknown.hint": "The server did not return a policy revision; values cannot be guessed.",
    "settings.empty": "No editable project policy yet.",
    "settings.empty.hint": "Register a project first, then configure its execution policy.",
    "field.auto": "Auto mode",
    "field.auto.hint": "Off by default. Dispatching only happens when on; manual claiming still works when off. Blocked-task self-healing is a separate switch, see the section below.",
    "field.limit": "Execution limit",
    "field.limit.hint": "Hard cap on concurrent managed runs; at least 1 when auto mode is on.",
    "field.capacity": "Node capacity",
    "field.capacity.hint": "Concurrent runs per node; not the same number as the global limit.",
    "field.ref": "max_parallel reference",
    "field.ref.hint": "Advisory value from project config; not a hard constraint.",
    "field.retention": "Retention days",
    "field.retention.hint": "How long observability and event detail is kept before maintenance prunes it.",
    "field.retention_runs": "Retained runs",
    "field.retention_runs.hint": "Maximum run records kept per task.",
    "auto.off": "Off",
    "auto.on": "On",
    "auto.not_ready": "Not ready",
    "auto.reason.platform": "Platform not verified: managed runs are only measured on macOS.",
    "auto.reason.config": "Config not applied: auto_enabled is still false in config.toml.",
    "save.idle": "No changes",
    "save.dirty": "Unsaved changes",
    "save.saving": "Saving…",
    "save.saved": "Saved",
    "save.error": "Save failed; your changes are still in the form.",
    "save.conflict": "Settings were updated elsewhere; reload before saving again.",
    "action.save": "Save settings",
    "action.reload": "Reload",
    "notice.no_secret": "This page has no token or secret fields: credentials live in the CLI and the local credential store.",
    "settings.readonly": "Read-only",
    "settings.readonly.hint": "The hub exposes no policy write endpoint yet; these are the values the server reports. Change them through the CLI.",
    "col.item": "Item",
    "col.value": "Current",
    "col.note": "Note",
    "col.name": "Name",
    "col.status": "Status",
    "col.heartbeat": "Last heartbeat",
    "fact.entry": "Entry",
    "fact.owner": "Owner",
    "fact.boundary": "Boundary",
    "section.managed": "Managed execution",
    "section.managed.desc": "Auto mode and the concurrency cap: whether the hub may start runs on its own, and how many at once.",
    "section.managed.entry": "The managed execution section on this page; values are echoed by the hub /api/runtime",
    "section.managed.owner": "Hub runtime config (auto_enabled and the managed limit in config.toml)",
    "section.managed.boundary": "This changes policy values only; it does not replace node readiness checks.",
    "section.nodes": "Nodes & capacity",
    "section.nodes.desc": "A node is a machine-wide execution instance; an agent is a project-scoped worker identity. They are not the same thing.",
    "section.nodes.entry": "The node list lives under Nodes in the sidebar (?view=nodes); the capacity field is in this section",
    "section.nodes.owner": "Registration and heartbeats belong to the CLI and the adapter; the hub only echoes them",
    "section.nodes.boundary": "There is no node registration write endpoint here, so this page ships no form that cannot save.",
    "section.retention": "Collection & retention",
    "section.retention.desc": "How long observability and event detail is kept, and how many run records each task keeps.",
    "section.retention.entry": "The collection and retention section on this page",
    "section.retention.owner": "Hub maintenance prunes by day; nobody deletes rows by hand",
    "section.retention.boundary": "Pruning is not reversible: confirm the history is still needed before lowering it.",
    "section.appearance": "Appearance",
    "section.appearance.desc": "Theme and language follow the main shell; this page only declares where they live.",
    "section.appearance.entry": "Top bar, right side: theme #theme-select, language #lang-select",
    "section.appearance.owner": "This browser (the theme is stored in localStorage as maw-theme)",
    "section.appearance.boundary": "No duplicated controls: two switches would drift apart, so there is one source of truth.",
    "section.agents": "System agent status",
    "section.agents.desc": "System agents are the built-in executor identities; their status arrives by heartbeat and is read-only here.",
    "section.agents.entry": "The AGENTS rail in the sidebar and the Nodes view; maw status in the CLI confirms it",
    "section.agents.owner": "Heartbeats reported by adapters",
    "section.agents.boundary": "Read-only: this page does not start, stop or dispatch anything.",
    "section.agents.unknown": "No system agent snapshot received: unknown status is not the same as no agents.",
    "agents.state.on": "On",
    "agents.state.off": "Off",
    "agents.state.unknown": "Unknown",
    "agents.state.hint.on": "The self-healing loop is running: blocked tasks get diagnosed and orchestrated automatically.",
    "agents.state.hint.off": "The self-healing loop is off: blocked tasks are not processed automatically.",
    "agents.state.hint.unknown": "The server did not report the system agent switch, so this page will not guess.",
    "agents.does.title": "What it governs",
    "agents.does.1": "Governs: enqueuing blocked tasks and orchestrating diagnosis, splitting and ops.",
    "agents.does.2": "Does not govern: auto dispatch, which is the auto mode in the managed execution section.",
    "agents.does.3": "The two switches are independent: self-healing alone never dispatches, auto mode alone never heals.",
    "err.limit_not_integer": "Execution limit must be an integer.",
    "err.limit_below_min": "Execution limit is at least 1; turn auto mode off instead of entering 0.",
    "err.capacity_not_integer": "Node capacity must be an integer.",
    "err.capacity_above_max": "Node capacity exceeds the allowed range.",
    "err.retention_not_integer": "Retention values must be integers.",
    "err.retention_out_of_range": "Retention days are out of range.",
    "err.revision_missing": "Policy revision is missing; saving is not allowed.",
    "err.secret_field": "Secret fields are not allowed here: use the CLI or the local credential store.",
  },
};

function dict(lang) {
  return lang === "en" ? I18N.en : I18N.zh;
}

export function t(key, lang, vars) {
  const table = dict(lang);
  const raw = table[key] ?? I18N.zh[key] ?? key;
  if (!vars) return raw;
  return raw.replace(/\{(\w+)\}/g, (_, name) => String(vars[name] ?? ""));
}

export function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, ch => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[ch]));
}

// ---------- 判定 ----------

/** 整数解析：空串与小数都不算整数，不做「静默取整」。 */
export function toInt(value) {
  if (typeof value === "number") return Number.isInteger(value) ? value : null;
  if (typeof value !== "string") return null;
  const raw = value.trim();
  if (!/^-?\d+$/.test(raw)) return null;
  return Number(raw);
}

/**
 * 自动模式的三态。
 * `platformSupported=false` 或配置未生效时只能是 not_ready：既不是开也不是关，
 * 因为「能不能开」都还没成立，把它显示成「关闭」会让人以为手动开一下就行。
 */
export function autoModeOf(policy, options = {}) {
  // 只有服务端**明确**说支持时才认为可开：缺失＝未知，不是「支持」。
  // 把未知显示成「关闭」会让用户以为手动开一下就行，而真实情况是能不能开都还没成立。
  const platformOK = options.platformSupported === true;
  const configOn = options.configEnabled === true || policy?.autoEnabled === true;
  if (!platformOK || options.configApplied === false) return AUTO_MODE.not_ready;
  return configOn ? AUTO_MODE.on : AUTO_MODE.off;
}

/** 自动模式未就绪的具体原因码（只有 not_ready 才有）。 */
export function autoReasonOf(options = {}) {
  // 只有明确 true 才算「平台支持」；缺失与 false 都归到平台原因，
  // 否则未就绪会不给理由，用户看不出是平台没验证还是配置没生效。
  if (options.platformSupported !== true) return "auto.reason.platform";
  if (options.configApplied === false) return "auto.reason.config";
  return null;
}

/**
 * 归一化服务端策略。缺失字段保持 null，不填默认值冒充「服务端这么说」。
 *
 * `systemAgentEnabled` 来自 /api/runtime 的 `system_agent_enabled`（或
 * `system_agent.enabled`）：它与 auto_enabled 是两条不同循环的开关，
 * 缺失时保持 null，页面写「未知」而不是「关闭」。
 */
export function normalizePolicy(payload) {
  const p = payload || {};
  const retention = p.retention || {};
  const rawAgent = p.system_agent && typeof p.system_agent === "object" ? p.system_agent : null;
  const agentFlag = p.system_agent_enabled !== undefined
    ? p.system_agent_enabled
    : rawAgent?.enabled;
  return {
    autoEnabled: p.auto_enabled === true ? true : p.auto_enabled === false ? false : null,
    limit: toInt(p.managed_execution_limit),
    capacity: toInt(p.node_capacity),
    ref: toInt(p.max_parallel_reference),
    retentionDays: toInt(retention.days),
    retentionRuns: toInt(retention.runs),
    systemAgentEnabled: agentFlag === true ? true : agentFlag === false ? false : null,
    revision: p.revision === undefined || p.revision === null ? null : toInt(p.revision),
  };
}

/**
 * 系统 Agent 开关的三态：开 / 关 / 未知。
 *
 * 缺失不等于关闭：把「服务端没说」显示成「关闭」，用户会去翻一个本来没错的配置。
 */
export function systemAgentStateOf(policy) {
  const flag = policy?.systemAgentEnabled;
  if (flag === true) return "on";
  if (flag === false) return "off";
  return "unknown";
}

/**
 * 校验草稿。返回 `[{field, code}]`；空数组表示可保存。
 *
 * 校验只看形状与边界，不猜服务端策略：例如「自动模式开启时上限必须 ≥1」
 * 在 `autoMode === not_ready` 时不成立（还没开启）。
 */
export function validateDraft(draft, options = {}) {
  const out = [];
  const mode = options.autoMode ?? AUTO_MODE.off;

  const limit = toInt(draft?.limit);
  if (limit === null) {
    out.push({ field: "limit", code: ERROR_CODES.limitInt });
  } else if (limit < BOUNDS.limitMin) {
    out.push({ field: "limit", code: ERROR_CODES.limitMin });
  }

  const capacity = toInt(draft?.capacity);
  if (capacity === null) {
    out.push({ field: "capacity", code: ERROR_CODES.capacityInt });
  } else if (capacity < BOUNDS.capacityMin || capacity > BOUNDS.capacityMax) {
    out.push({ field: "capacity", code: ERROR_CODES.capacityMax });
  }

  const days = toInt(draft?.retentionDays);
  if (days === null) {
    out.push({ field: "retentionDays", code: ERROR_CODES.retentionInt });
  } else if (days < BOUNDS.retentionDaysMin || days > BOUNDS.retentionDaysMax) {
    out.push({ field: "retentionDays", code: ERROR_CODES.retentionRange });
  }

  const runs = toInt(draft?.retentionRuns);
  if (runs === null) {
    out.push({ field: "retentionRuns", code: ERROR_CODES.retentionInt });
  } else if (runs < BOUNDS.limitMin) {
    out.push({ field: "retentionRuns", code: ERROR_CODES.retentionRange });
  }

  if (containsSecret(draft)) {
    out.push({ field: "secret", code: ERROR_CODES.secretField });
  }

  // 只有真要保存时才要求 revision：编辑器允许在「没拿到版本」时先改，保存时再报。
  if (options.requireRevision && (draft?.revision ?? null) === null) {
    out.push({ field: "revision", code: ERROR_CODES.revisionMissing });
  }
  if (mode === AUTO_MODE.on && limit !== null && limit < BOUNDS.limitMin) {
    // 已在上面报过 limit_below_min；这里只保证「开着自动却 0 上限」不会漏。
    if (!out.some(e => e.field === "limit")) {
      out.push({ field: "limit", code: ERROR_CODES.limitMin });
    }
  }
  return out;
}

/** 草稿里出现秘密字段就拒绝：这类值不该进页面状态。 */
export function containsSecret(draft) {
  if (!draft || typeof draft !== "object") return false;
  return Object.keys(draft).some(key => SECRET_KEY.test(key));
}

/** 有实质变化才算脏；字符串与数字按归一化后的值比较。 */
export function isDirty(current, draft) {
  if (!current || !draft) return false;
  const fields = ["limit", "capacity", "retentionDays", "retentionRuns"];
  return fields.some(field => toInt(draft[field]) !== toInt(current[field]));
}

/**
 * 保存结果 → 页面状态。
 *
 * `current` 是点保存时的状态；`result` 为空表示「请求刚发出、还没有结果」。
 * 冲突保留草稿：用户刚输入的值不能被「重新载入」悄悄抹掉。
 */
export function applySaveResult(current, result = {}) {
  if (result.conflict) {
    return {
      status: SAVE_STATUS.conflict,
      keepDraft: true,
      serverRevision: toInt(result.serverRevision),
    };
  }
  if (result.ok) {
    return { status: SAVE_STATUS.saved, keepDraft: false, revision: toInt(result.revision) };
  }
  if (result.errors && result.errors.length) {
    return { status: SAVE_STATUS.error, keepDraft: true, errors: result.errors };
  }
  if (current === SAVE_STATUS.error) return { status: SAVE_STATUS.error, keepDraft: true };
  // 没有结果 = 正在保存：草稿当然留着。
  return { status: SAVE_STATUS.saving, keepDraft: true };
}

// ---------- 渲染 ----------

const STATUS_BADGE = {
  [SAVE_STATUS.idle]: "badge--self_check",
  [SAVE_STATUS.dirty]: "badge--claimed",
  [SAVE_STATUS.saving]: "badge--in_progress",
  [SAVE_STATUS.saved]: "badge--done",
  [SAVE_STATUS.error]: "badge--blocked",
  [SAVE_STATUS.conflict]: "badge--conflict",
};

const AUTO_BADGE = {
  [AUTO_MODE.on]: "badge--done",
  [AUTO_MODE.off]: "badge--self_check",
  [AUTO_MODE.not_ready]: "badge--blocked",
};

function badge(text, cls) {
  return '<span class="badge ' + cls + '">' + esc(text) + "</span>";
}

function emptyState(lang, keys) {
  return '<div class="empty-state"><p class="empty-state__text">' + esc(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + esc(t(keys[1], lang)) + "</p></div>";
}

function fieldRow(lang, id, labelKey, hintKey, value, { readonly = false, unit = "" } = {}) {
  const inputId = "settings-" + id;
  const ro = readonly ? " readonly disabled" : "";
  // 服务端没给值时不编默认值：留空并保持可读，比填一个看起来像配置的 0 更诚实。
  const shown = value === null || value === undefined ? "" : value;
  // 复用共享表单组件（.field / .field-label / .input / .field-hint），页面不再自建表单层。
  return '<div class="field">' +
    '<label class="field-label" for="' + inputId + '">' + esc(t(labelKey, lang)) + "</label>" +
    '<input class="input" id="' + inputId + '" name="' + id + '" type="number" inputmode="numeric"' +
    ' value="' + esc(shown) + '"' + ro + (unit ? ' data-unit="' + esc(unit) + '"' : "") + ">" +
    '<p class="field-hint" id="' + inputId + '-hint">' + esc(t(hintKey, lang)) + "</p>" +
    "</div>";
}

/**
 * 四件常被混淆的东西单独成行，标签与说明都不复用：自动模式是状态，
 * 上限是硬约束，容量是单节点，参考值只供参考。
 */
function conceptTable(lang, policy, mode, reasonKey) {
  const rows = [
    {
      id: "auto", label: "field.auto", value: badge(t("auto." + mode, lang), AUTO_BADGE[mode]),
      note: reasonKey ? t("field.auto.hint", lang) + " " + t(reasonKey, lang) : t("field.auto.hint", lang),
    },
    { id: "limit", label: "field.limit", value: esc(policy.limit ?? "—"), note: t("field.limit.hint", lang) },
    { id: "capacity", label: "field.capacity", value: esc(policy.capacity ?? "—"), note: t("field.capacity.hint", lang) },
    { id: "ref", label: "field.ref", value: esc(policy.ref ?? "—"), note: t("field.ref.hint", lang) },
  ];
  const body = rows.map(row =>
    '<tr data-concept="' + row.id + '">' +
    '<th scope="row">' + esc(t(row.label, lang)) + "</th>" +
    '<td class="num">' + row.value + "</td>" +
    '<td class="note">' + esc(row.note) + "</td>" +
    "</tr>").join("");
  return '<table class="table"><caption class="field-hint">' + esc(t("settings.subtitle", lang)) + "</caption>" +
    "<thead><tr><th>" + esc(t("col.item", lang)) + "</th><th>" + esc(t("col.value", lang)) +
    "</th><th>" + esc(t("col.note", lang)) + "</th></tr></thead><tbody>" + body + "</tbody></table>";
}

function errorsBlock(lang, errors) {
  if (!errors || !errors.length) return "";
  const items = errors.map(e =>
    '<li class="field-error" data-error="' + esc(e.code) + '">' + esc(t("err." + e.code, lang)) + "</li>").join("");
  return '<ul class="settings-errors" role="alert" aria-live="polite">' + items + "</ul>";
}

/**
 * 说明链：入口／归属／边界。
 *
 * 三行缺一不可——只说「入口在这」而不说归属，用户会以为改这里就等于改成了；
 * 只说归属而不说边界，就会有人来找一个本页不打算提供的按钮。
 */
function factsBlock(lang, id) {
  const rows = FACT_ROWS.map(row =>
    '<div class="settings-fact" data-fact="' + row + '">' +
    "<dt>" + esc(t("fact." + row, lang)) + "</dt>" +
    "<dd>" + esc(t("section." + id + "." + row, lang)) + "</dd>" +
    "</div>").join("");
  return '<dl class="settings-facts">' + rows + "</dl>";
}

/** 分区外壳：标题与说明取自同一组键，分区 id 同时是锚点与测试钩子。 */
function section(lang, id, body) {
  const headId = "settings-section-" + id;
  return '<section class="settings-section" data-section="' + esc(id) + '" aria-labelledby="' + headId + '">' +
    '<h2 class="settings-section__title" id="' + headId + '">' + esc(t("section." + id, lang)) + "</h2>" +
    '<p class="field-hint settings-section__desc">' + esc(t("section." + id + ".desc", lang)) + "</p>" +
    (body || "") + factsBlock(lang, id) + "</section>";
}

/**
 * 系统 Agent 快照归一化。
 *
 * 没给就返回 null：视图据此写「状态未知」，不拿空数组冒充「没有 Agent」——
 * 那两件事对排障的意义完全相反。
 */
export function systemAgentsOf(state) {
  if (!state || !Array.isArray(state.systemAgents)) return null;
  return state.systemAgents.map(agent => ({
    name: String(agent?.name || agent?.id || "agent"),
    status: String(agent?.status || agent?.state || ""),
    heartbeat: String(agent?.heartbeat || agent?.last_heartbeat || ""),
  }));
}

function agentsTable(lang, agents) {
  const body = agents.map(agent =>
    '<tr data-agent="' + esc(agent.name) + '">' +
    '<th scope="row">' + esc(agent.name) + "</th>" +
    '<td class="num">' + esc(agent.status || "—") + "</td>" +
    '<td class="note">' + esc(agent.heartbeat || "—") + "</td>" +
    "</tr>").join("");
  return '<table class="table settings-agents" data-agents="1"><thead><tr>' +
    "<th>" + esc(t("col.name", lang)) + "</th>" +
    "<th>" + esc(t("col.status", lang)) + "</th>" +
    "<th>" + esc(t("col.heartbeat", lang)) + "</th>" +
    "</tr></thead><tbody>" + body + "</tbody></table>";
}

const AGENT_BADGE = { on: "badge--done", off: "badge--self_check", unknown: "badge--blocked" };

/**
 * 系统 Agent 开关状态块（SA-004）。
 *
 * 这里必须同时说三件事，否则读者会把它和「自动模式」混成一个开关：
 * 现在开没开、它管什么、它**不管**什么。未知时给「未知」，不给「关闭」。
 */
function systemAgentBlock(lang, policy) {
  const state = systemAgentStateOf(policy);
  const does = ["agents.does.1", "agents.does.2", "agents.does.3"]
    .map(key => "<li>" + esc(t(key, lang)) + "</li>").join("");
  return '<p class="settings-agents__state" data-system-agent="' + esc(state) + '">' +
    badge(t("agents.state." + state, lang), AGENT_BADGE[state] || "badge--self_check") +
    '<span class="field-hint">' + esc(t("agents.state.hint." + state, lang)) + "</span></p>" +
    '<div class="settings-agents__does"><p class="field-hint">' + esc(t("agents.does.title", lang)) + "</p>" +
    '<ul class="settings-agents__list">' + does + "</ul></div>";
}

/**
 * 渲染设置页。
 * state: {status, policy, draft, save, errors, platformSupported, configApplied}
 */
export function renderSettings(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const status = state.status || "ready";

  if (status === "loading") return '<p class="settings-loading">' + esc(t("settings.loading", lang)) + "</p>";
  if (status === "unauthenticated") return emptyState(lang, ["settings.unauthenticated", "settings.unauthenticated.hint"]);
  if (status === "error") return emptyState(lang, ["settings.error", "settings.error.hint"]);
  if (status === "expired") return emptyState(lang, ["settings.expired", "settings.expired.hint"]);
  if (status === "unknown") return emptyState(lang, ["settings.unknown", "settings.unknown.hint"]);
  if (!state.policy) return emptyState(lang, ["settings.empty", "settings.empty.hint"]);

  const policy = state.policy;
  const mode = autoModeOf(policy, {
    platformSupported: state.platformSupported,
    configApplied: state.configApplied,
    configEnabled: policy.autoEnabled === true,
  });
  const reasonKey = mode === AUTO_MODE.not_ready
    ? autoReasonOf({ platformSupported: state.platformSupported, configApplied: state.configApplied })
    : null;
  const save = state.save || SAVE_STATUS.idle;
  const errors = state.errors || [];
  // 中枢没有策略写入口时按只读渲染：可编辑却存不下去，比明说只读更糟。
  const editable = state.editable === true;
  // 保存必须带 revision：没有版本号就分不清「这是不是服务端当前那份」，
  // 点了保存只能盲写，所以按钮先禁用并说明原因，而不是让它静默失败。
  const revisionMissing = policy.revision === null || policy.revision === undefined;

  // 标题已由壳层 page-heading 提供；这里只保留保存态与只读标记。
  const saveStatus = '<p class="settings-save-status" data-save="' + esc(save) + '">' +
    badge(t("save." + save, lang), STATUS_BADGE[save] || "badge--self_check") +
    (editable ? "" : " " + badge(t("settings.readonly", lang), "badge--self_check")) + "</p>";

  // 自动模式不是一个输入框，而是一段状态：开关在服务端，页面只能解释当前状态。
  const autoFieldset = '<fieldset class="settings-fieldset"><legend class="field-label">' +
    esc(t("field.auto", lang)) + "</legend>" +
    '<p class="field-hint">' + esc(t("field.auto.hint", lang)) + "</p>" +
    (reasonKey ? '<p class="field-hint" data-auto-reason="1">' + esc(t(reasonKey, lang)) + "</p>" : "") +
    "</fieldset>";

  // 分区顺序即 Tab 顺序：上限 → 容量 → 保留天数 → 保留运行数 → 保存。
  // 只读声明区（外观／系统 Agent）不产生可聚焦元素，排在其后不影响键盘路径。
  const managedSection = section(lang, "managed", autoFieldset +
    fieldRow(lang, "limit", "field.limit", "field.limit.hint", state.draft?.limit ?? policy.limit, { readonly: !editable }));
  const nodesSection = section(lang, "nodes",
    fieldRow(lang, "capacity", "field.capacity", "field.capacity.hint", state.draft?.capacity ?? policy.capacity, { readonly: !editable }));
  const retentionSection = section(lang, "retention",
    fieldRow(lang, "retentionDays", "field.retention", "field.retention.hint", state.draft?.retentionDays ?? policy.retentionDays, { readonly: !editable }) +
    fieldRow(lang, "retentionRuns", "field.retention_runs", "field.retention_runs.hint", state.draft?.retentionRuns ?? policy.retentionRuns, { readonly: !editable }));

  const form = '<form class="settings-form" data-revision="' + esc(policy.revision ?? "") +
    '" data-editable="' + (editable ? "1" : "0") + '" onsubmit="return false" novalidate>' +
    managedSection + nodesSection + retentionSection +
    '<p class="field-hint" data-no-secret="1">' + esc(t("notice.no_secret", lang)) + "</p>" +
    (editable ? "" : '<p class="field-hint" data-readonly-reason="1">' + esc(t("settings.readonly.hint", lang)) + "</p>") +
    errorsBlock(lang, errors) +
    (revisionMissing
      ? '<p class="field-hint" data-save-blocked="revision_missing">' + esc(t("err.revision_missing", lang)) + "</p>"
      : "") +
    '<div class="settings-actions">' +
    '<button class="button button--primary" type="submit"' +
    (editable && !revisionMissing && save !== SAVE_STATUS.saving ? "" : " disabled") + ">" +
    esc(t("action.save", lang)) + "</button>" +
    (save === SAVE_STATUS.conflict
      ? '<button class="button" type="button" data-action="reload">' + esc(t("action.reload", lang)) + "</button>"
      : "") +
    "</div></form>";

  // 外观与系统 Agent 只声明入口，不放控件：主题／语言在顶栏，Agent 状态由心跳上报。
  const agents = systemAgentsOf(state);
  const agentsBody = systemAgentBlock(lang, policy) + (agents
    ? agentsTable(lang, agents)
    : '<p class="field-hint" data-agents-unknown="1">' + esc(t("section.agents.unknown", lang)) + "</p>");

  return saveStatus + conceptTable(lang, policy, mode, reasonKey) + form +
    section(lang, "appearance", "") + section(lang, "agents", agentsBody);
}
