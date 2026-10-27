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
    "field.auto.hint": "默认关闭。开启后才会自动选题派发；关闭时仍可人工领取。",
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
    "col.item": "项",
    "col.value": "当前值",
    "col.note": "说明",
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
    "field.auto.hint": "Off by default. Dispatching only happens when on; manual claiming still works when off.",
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
    "col.item": "Item",
    "col.value": "Current",
    "col.note": "Note",
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
  const platformOK = options.platformSupported !== false;
  const configOn = options.configEnabled === true || policy?.autoEnabled === true;
  if (!platformOK || options.configApplied === false) return AUTO_MODE.not_ready;
  return configOn ? AUTO_MODE.on : AUTO_MODE.off;
}

/** 自动模式未就绪的具体原因码（只有 not_ready 才有）。 */
export function autoReasonOf(options = {}) {
  if (options.platformSupported === false) return "auto.reason.platform";
  if (options.configApplied === false) return "auto.reason.config";
  return null;
}

/**
 * 归一化服务端策略。缺失字段保持 null，不填默认值冒充「服务端这么说」。
 */
export function normalizePolicy(payload) {
  const p = payload || {};
  const retention = p.retention || {};
  return {
    autoEnabled: p.auto_enabled === true ? true : p.auto_enabled === false ? false : null,
    limit: toInt(p.managed_execution_limit),
    capacity: toInt(p.node_capacity),
    ref: toInt(p.max_parallel_reference),
    retentionDays: toInt(retention.days),
    retentionRuns: toInt(retention.runs),
    revision: p.revision === undefined || p.revision === null ? null : toInt(p.revision),
  };
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
  const ro = readonly ? " readonly" : "";
  // 复用共享表单组件（.field / .field-label / .input / .field-hint），页面不再自建表单层。
  return '<div class="field">' +
    '<label class="field-label" for="' + inputId + '">' + esc(t(labelKey, lang)) + "</label>" +
    '<input class="input" id="' + inputId + '" name="' + id + '" type="number" inputmode="numeric"' +
    ' value="' + esc(value ?? "") + '"' + ro + (unit ? ' data-unit="' + esc(unit) + '"' : "") + ">" +
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

  // 标题已由壳层 page-heading 提供；这里只保留保存态。
  const saveStatus = '<p class="settings-save-status" data-save="' + esc(save) + '">' +
    badge(t("save." + save, lang), STATUS_BADGE[save] || "badge--self_check") + "</p>";

  const form = '<form class="settings-form" data-revision="' + esc(policy.revision ?? "") + '" novalidate>' +
    '<fieldset class="settings-fieldset"><legend class="field-label">' + esc(t("field.auto", lang)) + "</legend>" +
    '<p class="field-hint">' + esc(t("field.auto.hint", lang)) + "</p>" +
    (reasonKey ? '<p class="field-hint" data-auto-reason="1">' + esc(t(reasonKey, lang)) + "</p>" : "") +
    "</fieldset>" +
    fieldRow(lang, "limit", "field.limit", "field.limit.hint", state.draft?.limit ?? policy.limit) +
    fieldRow(lang, "capacity", "field.capacity", "field.capacity.hint", state.draft?.capacity ?? policy.capacity) +
    fieldRow(lang, "retentionDays", "field.retention", "field.retention.hint", state.draft?.retentionDays ?? policy.retentionDays) +
    fieldRow(lang, "retentionRuns", "field.retention_runs", "field.retention_runs.hint", state.draft?.retentionRuns ?? policy.retentionRuns) +
    '<p class="field-hint" data-no-secret="1">' + esc(t("notice.no_secret", lang)) + "</p>" +
    errorsBlock(lang, errors) +
    '<div class="settings-actions">' +
    '<button class="button button--primary" type="submit"' + (save === SAVE_STATUS.saving ? " disabled" : "") + ">" +
    esc(t("action.save", lang)) + "</button>" +
    (save === SAVE_STATUS.conflict
      ? '<button class="button" type="button" data-action="reload">' + esc(t("action.reload", lang)) + "</button>"
      : "") +
    "</div></form>";

  return saveStatus + conceptTable(lang, policy, mode, reasonKey) + form;
}
