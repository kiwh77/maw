// 节点列表与能力详情（vnext / UI · VN-721）。
//
// 目标：让人一眼看出「这台机器能不能接这道题」，以及卡在哪个条件上。
//
// 依据：
//   docs/ARCHITECTURE_VNEXT.zh-CN.md 第 140 行（认证与模型：ready / needs-login / needs-model / unknown，
//   探测有时效，不缓存成永久可用）、第 144 行（能力同时保存声明值、验证值、CLI 版本与最近验证时间；
//   版本变化使相关验证失效）、第 486 行「在线、认证、能力与容量不混淆」。
//
// 四维独立，互不推导：
//   presence   在线    —— 有近期心跳／探测证据
//   auth       认证    —— ready / needs-login / needs-model / unknown
//   capability 能力    —— 声明值 ≠ 验证值；版本变化即失效
//   capacity   容量    —— 执行槽是否还有空位
// 另有接收策略 intake（open / paused）：暂停不代表节点不可用，只代表现在不接新任务。
//
// 三条硬规则：
//   1. 在线 ≠ 可执行。在线但认证或能力未就绪一律判 blocked；
//      只有缺探测证据（在线或认证无法判定）才判 unknown，两者都不得渲染成「可执行」。
//   2. 能力未知不是能力具备。声明过但未验证的能力一律按未验证展示。
//   3. 版本失效要显式说「需要重新确认」，不自动继承旧版本的验证结论。

export const PRESENCE = Object.freeze({ online: "online", stale: "stale", offline: "offline" });
export const AUTH_STATES = Object.freeze(["ready", "needs-login", "needs-model", "unknown"]);
export const VERDICTS = Object.freeze(["executable", "blocked", "unknown"]);
export const INTAKE = Object.freeze({ open: "open", paused: "paused" });

/** 判为「在线」的心跳时效上限；超过只降级为 stale，不直接判离线。 */
export const PRESENCE_TTL_MS = 90_000;

const I18N = {
  zh: {
    "nodes.title": "执行节点",
    "nodes.subtitle": "在线、认证、能力、容量分开看，四项都成立才接任务。",
    "nodes.empty": "还没有登记任何节点。",
    "nodes.empty.hint": "先在节点所在机器上完成注册，再回来查看就绪度。",
    "nodes.error": "节点列表加载失败。",
    "nodes.error.hint": "确认本机中枢在运行，然后重试。",
    "nodes.expired": "数据已过期。",
    "nodes.expired.hint": "刷新后会重新探测，不沿用上次结论。",
    "nodes.loading": "正在读取节点…",
    "nodes.unauthenticated": "未登录，无法查看节点。",
    "nodes.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "nodes.count": "个节点",
    "nodes.executable": "个可接任务",
    "dim.presence": "在线",
    "dim.auth": "认证",
    "dim.capability": "能力",
    "dim.capacity": "容量",
    "dim.intake": "接收",
    "presence.online": "在线",
    "presence.stale": "心跳过期",
    "presence.offline": "离线",
    "auth.ready": "已就绪",
    "auth.needs-login": "需要登录",
    "auth.needs-model": "需要选模型",
    "auth.unknown": "未探测",
    "cap.verified": "已验 {n} 项",
    "cap.unverified": "未验证 {n} 项",
    "cap.version_changed": "版本变化，需重新确认",
    "cap.none": "未声明能力",
    "cap.declared": "声明",
    "cap.verified_list": "已验证",
    "capacity.slots": "{busy}/{total} 槽占用",
    "capacity.full": "槽已满",
    "intake.open": "接任务",
    "intake.paused": "暂停接收",
    "verdict.executable": "可接任务",
    "verdict.blocked": "不可接任务",
    "verdict.unknown": "状态未知",
    "reason.not_online": "节点不在线",
    "reason.auth": "认证未就绪：{state}",
    "reason.capability_unknown": "能力未验证",
    "reason.capability_version": "能力验证已随版本失效",
    "reason.capacity": "执行槽已满",
    "reason.intake_paused": "已暂停接收任务",
    "reason.unknown": "缺少探测证据",
    "detail.title": "能力详情",
    "detail.version": "CLI 版本",
    "detail.verified_at": "最近验证",
    "detail.verified_version": "验证版本",
    "detail.slots": "执行槽",
    "detail.paused_reason": "暂停原因",
    "detail.back": "返回列表",
    "detail.unauth_title": "需要登录后才能查看能力详情",
  },
  en: {
    "nodes.title": "Execution nodes",
    "nodes.subtitle": "Presence, auth, capability and capacity are independent; a task needs all four.",
    "nodes.empty": "No nodes registered yet.",
    "nodes.empty.hint": "Register on the node's machine, then come back for readiness.",
    "nodes.error": "Failed to load nodes.",
    "nodes.error.hint": "Check that the local hub is running, then retry.",
    "nodes.expired": "Data is out of date.",
    "nodes.expired.hint": "Refresh to re-probe; previous conclusions are not reused.",
    "nodes.loading": "Loading nodes…",
    "nodes.unauthenticated": "Not signed in, cannot read nodes.",
    "nodes.unauthenticated.hint": "Use a local session or token and retry.",
    "nodes.count": "nodes",
    "nodes.executable": "can take work",
    "dim.presence": "Presence",
    "dim.auth": "Auth",
    "dim.capability": "Capability",
    "dim.capacity": "Capacity",
    "dim.intake": "Intake",
    "presence.online": "Online",
    "presence.stale": "Heartbeat stale",
    "presence.offline": "Offline",
    "auth.ready": "Ready",
    "auth.needs-login": "Needs login",
    "auth.needs-model": "Needs model",
    "auth.unknown": "Not probed",
    "cap.verified": "{n} verified",
    "cap.unverified": "{n} unverified",
    "cap.version_changed": "Version changed, re-verify",
    "cap.none": "No capability declared",
    "cap.declared": "Declared",
    "cap.verified_list": "Verified",
    "capacity.slots": "{busy}/{total} slots busy",
    "capacity.full": "Slots full",
    "intake.open": "Taking work",
    "intake.paused": "Paused",
    "verdict.executable": "Can take work",
    "verdict.blocked": "Cannot take work",
    "verdict.unknown": "Unknown",
    "reason.not_online": "Node is not online",
    "reason.auth": "Auth not ready: {state}",
    "reason.capability_unknown": "Capability not verified",
    "reason.capability_version": "Capability verification invalidated by version change",
    "reason.capacity": "All execution slots busy",
    "reason.intake_paused": "Intake is paused",
    "reason.unknown": "No probe evidence",
    "detail.title": "Capability detail",
    "detail.version": "CLI version",
    "detail.verified_at": "Last verified",
    "detail.verified_version": "Verified version",
    "detail.slots": "Execution slots",
    "detail.paused_reason": "Pause reason",
    "detail.back": "Back to list",
    "detail.unauth_title": "Sign in to read capability detail",
  },
};

export function t(key, lang = "zh", params = {}) {
  const pack = I18N[lang] || I18N.zh;
  const raw = pack[key] ?? I18N.zh[key] ?? key;
  return String(raw).replace(/\{(\w+)\}/g, (_, name) =>
    params[name] === undefined ? "" : String(params[name])
  );
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

/**
 * 判定节点四维与接收策略。
 * @returns {{presence:string, auth:string, capability:string, capacity:string, intake:string,
 *            verdict:string, reasons:string[], executable:boolean, unknown:boolean}}
 */
export function evaluateReadiness(node, options = {}) {
  const now = options.now ?? Date.now();
  const ttl = options.presenceTtlMs ?? PRESENCE_TTL_MS;

  const presence = evaluatePresence(node?.presence, now, ttl);
  const auth = AUTH_STATES.includes(node?.auth?.state) ? node.auth.state : "unknown";
  const capability = evaluateCapability(node?.capability);
  const capacity = evaluateCapacity(node?.slots);
  const intake = node?.intake === INTAKE.paused ? INTAKE.paused : INTAKE.open;

  const reasons = [];
  // 未知优先：证据不足时既不判可执行，也不编造具体原因。
  if (presence === "unknown" || auth === "unknown") {
    reasons.push("reason.unknown");
    return finish("unknown", reasons, { presence, auth, capability, capacity, intake });
  }
  if (presence !== PRESENCE.online) reasons.push("reason.not_online");
  if (auth !== "ready") reasons.push("reason.auth");
  // 声明≠验证：unknown / partial 都按「能力未验证」处理，不因为声明过就放行。
  if (capability === "unknown" || capability === "partial" || capability === "none") {
    reasons.push("reason.capability_unknown");
  }
  if (capability === "version_changed") reasons.push("reason.capability_version");
  if (capacity === "full") reasons.push("reason.capacity");
  if (intake === INTAKE.paused) reasons.push("reason.intake_paused");

  return finish(reasons.length ? "blocked" : "executable", reasons, {
    presence, auth, capability, capacity, intake,
  });
}

function finish(verdict, reasons, dims) {
  return {
    ...dims,
    verdict,
    reasons,
    executable: verdict === "executable",
    unknown: verdict === "unknown",
  };
}

function evaluatePresence(presence, now, ttl) {
  if (!presence) return "unknown";
  if (presence.online === false) return PRESENCE.offline;
  const seenAt = parseTime(presence.last_seen);
  if (seenAt === null) return presence.online === true ? "unknown" : "unknown";
  if (now - seenAt <= ttl) return PRESENCE.online;
  return PRESENCE.stale;
}

function parseTime(value) {
  if (value === undefined || value === null || value === "") return null;
  if (typeof value === "number") return value;
  const ms = Date.parse(String(value));
  return Number.isNaN(ms) ? null : ms;
}

/**
 * 能力：声明值与验证值分开，版本不一致即失效。
 * 返回 none / unknown / version_changed / partial / verified。
 */
function evaluateCapability(capability) {
  if (!capability) return "unknown";
  const declared = Array.isArray(capability.declared) ? capability.declared : [];
  const verified = Array.isArray(capability.verified) ? capability.verified : [];
  if (!declared.length) return "none";
  if (!verified.length) return "unknown";

  const cliVersion = capability.cli_version ?? null;
  const verifiedVersion = capability.verified_version ?? null;
  // 版本变化使旧验证结论失效，不自动继承（架构 §144）。
  if (cliVersion && verifiedVersion && cliVersion !== verifiedVersion) return "version_changed";

  return verified.length >= declared.length ? "verified" : "partial";
}

function evaluateCapacity(slots) {
  if (!slots || typeof slots.total !== "number") return "unknown";
  const busy = typeof slots.busy === "number" ? slots.busy : 0;
  if (busy >= slots.total) return "full";
  return "available";
}

// ---------- 渲染 ----------

const VERDICT_BADGE = {
  executable: "badge--done",
  blocked: "badge--blocked",
  unknown: "badge--self_check",
};

function badge(text, cls) {
  return '<span class="badge ' + cls + '">' + esc(text) + "</span>";
}

function emptyState(lang, keys) {
  return '<div class="empty-state"><p class="empty-state__text">' + esc(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + esc(t(keys[1], lang)) + "</p></div>";
}

/** 列表页：加载／空／错误／过期／未认证各走一种明确状态，不互相冒充。 */
export function renderNodeList(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";

  if (state.status === "loading") return '<p class="nodes-loading">' + esc(t("nodes.loading", lang)) + "</p>";
  if (state.status === "unauthenticated") {
    return emptyState(lang, ["nodes.unauthenticated", "nodes.unauthenticated.hint"]);
  }
  if (state.status === "error") return emptyState(lang, ["nodes.error", "nodes.error.hint"]);
  if (state.status === "expired") return emptyState(lang, ["nodes.expired", "nodes.expired.hint"]);

  const nodes = Array.isArray(state.nodes) ? state.nodes : [];

  if (!nodes.length) {
    return emptyState(lang, ["nodes.empty", "nodes.empty.hint"]);
  }

  const now = options.now ?? Date.now();
  const rows = nodes.map(node => {
    const ready = evaluateReadiness(node, { ...options, now });
    const reason = ready.reasons.length
      ? t(ready.reasons[0], lang, { state: t("auth." + ready.auth, lang) })
      : "";
    return "<tr>" +
      '<td class="name">' + esc(node.name || node.id) + "</td>" +
      '<td class="id">' + esc(node.id) + "</td>" +
      '<td>' + badge(t("presence." + ready.presence, lang), presenceBadge(ready.presence)) + "</td>" +
      '<td>' + badge(t("auth." + ready.auth, lang), authBadge(ready.auth)) + "</td>" +
      '<td>' + esc(capabilityText(node.capability, lang)) + "</td>" +
      '<td class="num">' + esc(capacityText(node.slots, lang)) + "</td>" +
      "<td>" + badge(t("intake." + ready.intake, lang), ready.intake === "paused" ? "badge--warn" : "badge--in_review") + "</td>" +
      "<td>" + badge(t("verdict." + ready.verdict, lang), VERDICT_BADGE[ready.verdict]) +
        (reason ? '<span class="field-hint"> ' + esc(reason) + "</span>" : "") + "</td>" +
      "</tr>";
  }).join("");

  const executableCount = nodes.filter(node =>
    evaluateReadiness(node, { ...options, now }).executable
  ).length;

  return '<p class="field-hint">' + esc(nodes.length + " " + t("nodes.count", lang)) + " · " +
    esc(executableCount + " " + t("nodes.executable", lang)) + "</p>" +
    '<div class="table-wrap"><table class="table"><thead><tr>' +
    "<th>" + esc(lang === "en" ? "Node" : "节点") + "</th>" +
    "<th>" + esc(lang === "en" ? "ID" : "编号") + "</th>" +
    "<th>" + esc(t("dim.presence", lang)) + "</th>" +
    "<th>" + esc(t("dim.auth", lang)) + "</th>" +
    "<th>" + esc(t("dim.capability", lang)) + "</th>" +
    "<th>" + esc(t("dim.capacity", lang)) + "</th>" +
    "<th>" + esc(t("dim.intake", lang)) + "</th>" +
    "<th>" + esc(lang === "en" ? "Verdict" : "结论") + "</th>" +
    "</tr></thead><tbody>" + rows + "</tbody></table></div>";
}

function presenceBadge(presence) {
  if (presence === "online") return "badge--done";
  if (presence === "stale") return "badge--warn";
  return "badge--self_check";
}

function authBadge(auth) {
  if (auth === "ready") return "badge--done";
  if (auth === "unknown") return "badge--self_check";
  return "badge--warn";
}

function capabilityText(capability, lang) {
  if (!capability) return t("cap.none", lang);
  const declared = Array.isArray(capability.declared) ? capability.declared : [];
  const verified = Array.isArray(capability.verified) ? capability.verified : [];
  const state = evaluateCapability(capability);
  if (state === "version_changed") return t("cap.version_changed", lang);
  if (state === "none") return t("cap.none", lang);
  return t("cap.verified", lang, { n: verified.length }) + " · " +
    t("cap.unverified", lang, { n: Math.max(0, declared.length - verified.length) });
}

function capacityText(slots, lang) {
  if (!slots || typeof slots.total !== "number") return "—";
  const busy = typeof slots.busy === "number" ? slots.busy : 0;
  if (busy >= slots.total) return t("capacity.full", lang);
  return t("capacity.slots", lang, { busy, total: slots.total });
}

/** 详情页：能力按「声明／已验证」两列列出，版本失效时整块标注需重新确认。 */
export function renderNodeDetail(node, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  if (options.unauthenticated) {
    return emptyState(lang, ["detail.unauth_title", "nodes.unauthenticated.hint"]);
  }
  if (!node) return emptyState(lang, ["nodes.empty", "nodes.empty.hint"]);

  const ready = evaluateReadiness(node, options);
  const capability = node.capability || {};
  const declared = Array.isArray(capability.declared) ? capability.declared : [];
  const verified = new Set(Array.isArray(capability.verified) ? capability.verified : []);
  const capState = evaluateCapability(capability);

  const capRows = declared.length
    ? declared.map(name =>
        "<tr><td class=\"name\">" + esc(name) + "</td><td>" +
        (verified.has(name)
          ? badge(t("cap.verified_list", lang, { n: 1 }), "badge--done")
          : badge(t("cap.unverified", lang, { n: 1 }), "badge--self_check")) +
        "</td></tr>"
      ).join("")
    : '<tr><td colspan="2">' + esc(t("cap.none", lang)) + "</td></tr>";

  const versionNote = capState === "version_changed"
    ? '<p class="field-hint" style="color:var(--warn)">' + esc(t("cap.version_changed", lang)) + "</p>"
    : "";

  const reasons = ready.reasons.length
    ? '<ul class="field-hint">' + ready.reasons.map(key =>
        "<li>" + esc(t(key, lang, { state: t("auth." + ready.auth, lang) })) + "</li>"
      ).join("") + "</ul>"
    : "";

  return '<article class="node-detail">' +
    '<p class="field-hint">' + esc(node.name || node.id) + " · " + esc(node.id) +
    " · " + esc(node.adapter || "—") + " " +
    badge(t("verdict." + ready.verdict, lang), VERDICT_BADGE[ready.verdict]) +
    badge(t("intake." + ready.intake, lang), ready.intake === "paused" ? "badge--warn" : "badge--in_review") +
    "</p>" +
    reasons +
    '<section class="drawer__section"><h3>' + esc(t("detail.title", lang)) + "</h3>" + versionNote +
    '<div class="table-wrap"><table class="table"><thead><tr>' +
    "<th>" + esc(t("cap.declared", lang)) + "</th>" +
    "<th>" + esc(t("cap.verified_list", lang)) + "</th>" +
    "</tr></thead><tbody>" + capRows + "</tbody></table></div></section>" +
    '<section class="drawer__section"><h3>' + esc(t("detail.slots", lang)) + "</h3>" +
    '<dl class="kv">' +
    "<dt>" + esc(t("detail.version", lang)) + "</dt><dd>" + esc(capability.cli_version || "—") + "</dd>" +
    "<dt>" + esc(t("detail.verified_version", lang)) + "</dt><dd>" + esc(capability.verified_version || "—") + "</dd>" +
    "<dt>" + esc(t("detail.verified_at", lang)) + "</dt><dd>" + esc(capability.verified_at || "—") + "</dd>" +
    "<dt>" + esc(t("detail.slots", lang)) + "</dt><dd>" + esc(capacityText(node.slots, lang)) + "</dd>" +
    (node.intake === INTAKE.paused
      ? "<dt>" + esc(t("detail.paused_reason", lang)) + "</dt><dd>" + esc(node.paused_reason || "—") + "</dd>"
      : "") +
    "</dl></section></article>";
}
