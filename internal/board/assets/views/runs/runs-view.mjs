// 运行详情与待核实面板（vnext / UI · VN-723）。
//
// 目标：定位「这一次执行到哪了」「我们有多确定」「还占着什么」，并且不把
// 取消受理说成已停止、不把证据缺失说成已结束。
//
// 依据：docs/contracts/vnext/execution/runtime-state-contract-v1.md（maw.runtime.state/v1）
//   §1 三类状态三套语义：tasks.status / runs.phase+reconciliation_state / 交付验收，互不推导；
//   §2 十个运行阶段封闭，终态四个不再转移；
//   §3 reconciliation_state ∈ {confirmed, unknown}，unknown 不得直接落终态（E232）；
//   §4 三种资源分别释放：node_slot 需 process_stop_confirmed、workspace 需 delivery_frozen；
//   §4.1 仍占用三条判据；§5 取消是意图，cancelled 是事实，结束先到则 already_finished（E250/E251）。
//
// 三条硬规则：
//   1. 取消受理 ≠ 已停止。只有阶段进入 cancelled/timed_out 且 process_stop_confirmed
//      才显示「已停止」；否则一律「取消中」，并保留占用。
//   2. 不知道就说不知道。reconciliation_state=unknown 时列最后事实、未知点、占用与
//      处理入口，不落终态、不提供强制释放。
//   3. 运行状态、交付状态、任务状态分三行展示，任何一层都不能替另一层下结论。

export const RUN_PHASES = Object.freeze([
  "reserved", "preparing", "launching", "running", "waiting",
  "stopping", "completed", "failed", "cancelled", "timed_out",
]);

export const TERMINAL_PHASES = Object.freeze(["completed", "failed", "cancelled", "timed_out"]);
export const RECONCILIATION_STATES = Object.freeze(["confirmed", "unknown"]);
export const QUALITIES = Object.freeze(["observed", "estimated", "unknown"]);
export const CANCEL_STATES = Object.freeze(["idle", "accepted", "stopped", "already_finished"]);

const I18N = {
  zh: {
    "runs.title": "运行详情",
    "runs.subtitle": "运行阶段、交付状态与任务状态分开看；取消只到受理，停止要证据。",
    "runs.empty": "没有可显示的执行记录。",
    "runs.empty.hint": "换个任务或时间范围看看。",
    "runs.error": "运行记录加载失败。",
    "runs.error.hint": "确认本机中枢在运行，然后重试。",
    "runs.expired": "运行数据已过期。",
    "runs.expired.hint": "刷新后重新读取，不沿用上次结论。",
    "runs.loading": "正在读取运行记录…",
    "runs.unauthenticated": "未登录，无法查看运行记录。",
    "runs.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "oc.id": "运行",
    "runs.list": "运行记录",
    "runs.count": "次执行",
    "layer.run": "运行",
    "layer.delivery": "交付",
    "layer.task": "任务",
    "layer.run.note": "这一次执行到哪了、有多确定",
    "layer.delivery.note": "结果够不够格算完成",
    "layer.task.note": "这道题对计划意味着什么",
    "phase.reserved": "已预留",
    "phase.preparing": "准备中",
    "phase.launching": "启动中",
    "phase.running": "执行中",
    "phase.waiting": "等待中",
    "phase.stopping": "停止中",
    "phase.completed": "协议完成",
    "phase.failed": "失败",
    "phase.cancelled": "已取消",
    "phase.timed_out": "已超时",
    "phase.unknown": "阶段未知",
    "rec.confirmed": "已核实",
    "rec.unknown": "待核实",
    "rec.unknown.hint": "观测缺失不是停止证据，占用继续保留。",
    "delivery.frozen": "产物已冻结",
    "delivery.not_frozen": "产物未冻结",
    "delivery.pending": "待验收",
    "delivery.merged": "已合入",
    "delivery.unknown": "交付状态未知",
    "task.label": "任务状态",
    "task.unknown": "未知",
    "occ.title": "占用资源",
    "occ.node_slot": "节点槽",
    "occ.workspace": "工作目录",
    "occ.merge": "合入",
    "occ.held": "仍占用",
    "occ.released": "已释放",
    "occ.held_for": "占用原因：{reason}",
    "occ.reason.non_terminal": "阶段未结束",
    "occ.reason.unknown": "核实状态未知",
    "occ.reason.not_confirmed": "进程停止未确认",
    "release.node_slot.blocked": "进程未确认停止，槽不能释放",
    "release.workspace.blocked": "产物未冻结，目录不能重分配",
    "release.ready": "满足释放条件",
    "unknown.title": "待核实",
    "unknown.last_facts": "最后事实",
    "unknown.points": "未知点",
    "unknown.no_facts": "没有可信事实：这段时间内没有任何已核实证据。",
    "unknown.point.no_heartbeat": "心跳中断，无法判断进程是否仍在运行",
    "unknown.point.no_evidence": "缺少停止证据，不能判定已结束",
    "unknown.point.unattributed": "有来源记录尚未归属到本次运行",
    "unknown.action.recheck": "重新核实",
    "unknown.action.report": "上报人工处置",
    "unknown.action.note": "证据不足时不提供强制释放：先核实，再释放。",
    "fact.phase": "阶段 {phase}",
    "fact.quality": "证据质量 {quality}",
    "fact.at": "时间 {at}",
    "fact.none": "—",
    "cancel.title": "取消与停止",
    "cancel.idle": "未请求取消",
    "cancel.accepted": "取消已受理，正在停止（尚未确认停止）",
    "cancel.stopped": "已停止，进程停止已确认",
    "cancel.already_finished": "运行已先结束，取消未生效；保留原结果：{result}",
    "cancel.request": "请求停止本次运行",
    "cancel.note": "只停止本次运行，不改任务状态，也不删除已有结果。",
    "log.title": "运行日志",
    "log.empty": "暂无日志。",
    "quality.observed": "实测",
    "quality.estimated": "推算",
    "quality.unknown": "未知",
    "gen.label": "代际",
    "gen.stale": "旧代际写入已失效，不按最新覆盖。",
  },
  en: {
    "runs.title": "Run detail",
    "runs.subtitle": "Run phase, delivery and task state are separate. Cancel only reaches acceptance; stopping needs evidence.",
    "runs.empty": "No run records to show.",
    "runs.empty.hint": "Try another task or time range.",
    "runs.error": "Failed to load run records.",
    "runs.error.hint": "Check that the local hub is running, then retry.",
    "runs.expired": "Run data is out of date.",
    "runs.expired.hint": "Refresh to re-read; previous conclusions are not reused.",
    "runs.loading": "Loading run records…",
    "runs.unauthenticated": "Not signed in, cannot read run records.",
    "runs.unauthenticated.hint": "Use a local session or token and retry.",
    "oc.id": "Run",
    "runs.list": "Runs",
    "runs.count": "runs",
    "layer.run": "Run",
    "layer.delivery": "Delivery",
    "layer.task": "Task",
    "layer.run.note": "Where this execution is and how sure we are",
    "layer.delivery.note": "Whether the result qualifies as done",
    "layer.task.note": "What this task means for the plan",
    "phase.reserved": "Reserved",
    "phase.preparing": "Preparing",
    "phase.launching": "Launching",
    "phase.running": "Running",
    "phase.waiting": "Waiting",
    "phase.stopping": "Stopping",
    "phase.completed": "Protocol completed",
    "phase.failed": "Failed",
    "phase.cancelled": "Cancelled",
    "phase.timed_out": "Timed out",
    "phase.unknown": "Unknown phase",
    "rec.confirmed": "Confirmed",
    "rec.unknown": "Needs verification",
    "rec.unknown.hint": "Missing observations are not stop evidence; occupancy stays.",
    "delivery.frozen": "Artifacts frozen",
    "delivery.not_frozen": "Artifacts not frozen",
    "delivery.pending": "Pending review",
    "delivery.merged": "Merged",
    "delivery.unknown": "Delivery state unknown",
    "task.label": "Task state",
    "task.unknown": "Unknown",
    "occ.title": "Held resources",
    "occ.node_slot": "Node slot",
    "occ.workspace": "Workspace",
    "occ.merge": "Merge",
    "occ.held": "Held",
    "occ.released": "Released",
    "occ.held_for": "Held because: {reason}",
    "occ.reason.non_terminal": "Phase is not terminal",
    "occ.reason.unknown": "Reconciliation unknown",
    "occ.reason.not_confirmed": "Process stop not confirmed",
    "release.node_slot.blocked": "Process not confirmed stopped; slot cannot be released",
    "release.workspace.blocked": "Artifacts not frozen; workspace cannot be reassigned",
    "release.ready": "Release conditions met",
    "unknown.title": "Needs verification",
    "unknown.last_facts": "Last facts",
    "unknown.points": "Unknowns",
    "unknown.no_facts": "No trustworthy fact: no confirmed evidence in this window.",
    "unknown.point.no_heartbeat": "Heartbeat lost; cannot tell whether the process still runs",
    "unknown.point.no_evidence": "No stop evidence; cannot conclude it finished",
    "unknown.point.unattributed": "Some source records are not attributed to this run",
    "unknown.action.recheck": "Re-verify",
    "unknown.action.report": "Report for manual handling",
    "unknown.action.note": "Without evidence there is no forced release: verify first, then release.",
    "fact.phase": "Phase {phase}",
    "fact.quality": "Evidence quality {quality}",
    "fact.at": "At {at}",
    "fact.none": "—",
    "cancel.title": "Cancel / stop",
    "cancel.idle": "Cancel not requested",
    "cancel.accepted": "Cancel accepted, stopping (stop not confirmed yet)",
    "cancel.stopped": "Stopped; process stop confirmed",
    "cancel.already_finished": "Run already finished; cancel had no effect. Original result kept: {result}",
    "cancel.request": "Stop this run",
    "cancel.note": "Stops this run only; does not change task state or delete results.",
    "log.title": "Run log",
    "log.empty": "No log yet.",
    "quality.observed": "Observed",
    "quality.estimated": "Estimated",
    "quality.unknown": "Unknown",
    "gen.label": "Generation",
    "gen.stale": "Writes from an old generation are void; never overwrite with latest.",
  },
};

export function t(key, lang = "zh", params = {}) {
  const pack = I18N[lang] || I18N.zh;
  const raw = pack[key] ?? I18N.zh[key] ?? key;
  return String(raw).replace(/\{(\w+)\}/g, (_, name) =>
    params[name] === undefined ? "" : String(params[name])
  );
}

/** 文本安全渲染：一律转义，再按纯文本换行展示，不解析任何 HTML。 */
export function safeText(value) {
  return String(value ?? "").replace(/[&<>"']/g, ch => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[ch]));
}

export const esc = safeText;

export function isTerminal(phase) {
  return TERMINAL_PHASES.includes(phase);
}

/**
 * 是否仍计入占用（契约 §4.1 三条判据，命中任一即仍占用）。
 * @returns {{occupying:boolean, reasons:string[]}}
 */
export function stillOccupying(run = {}) {
  const reasons = [];
  if (!isTerminal(run.phase)) reasons.push("occ.reason.non_terminal");
  if (run.reconciliation_state === "unknown") reasons.push("occ.reason.unknown");
  if ((run.phase === "failed" || run.phase === "cancelled") && run.process_stop_confirmed !== true) {
    reasons.push("occ.reason.not_confirmed");
  }
  return { occupying: reasons.length > 0, reasons };
}

/** 资源释放资格：两种资源两套前置，不能一起放（契约 §4）。 */
export function releaseEligibility(run = {}) {
  const slotBlocked = run.process_stop_confirmed !== true;
  const workspaceBlocked = (run.delivery_frozen ?? run.delivery?.frozen) !== true;
  return {
    node_slot: {
      releasable: !slotBlocked,
      blockedBy: slotBlocked ? "release.node_slot.blocked" : null,
    },
    workspace: {
      releasable: !workspaceBlocked,
      blockedBy: workspaceBlocked ? "release.workspace.blocked" : null,
    },
  };
}

/**
 * 取消状态（契约 §5）：受理只到 accepted；cancelled 是事实，需要停止证据。
 * 结束事实先到时返回 already_finished，且不得改写原阶段。
 */
export function describeCancel(run = {}, operation = null) {
  const phase = run.phase;
  const wanted = run.desired_action === "stop" || operation?.action === "stop" || operation?.status !== undefined;

  if (operation?.result?.code === "already_finished" || operation?.code === "already_finished" ||
      (operation?.status === "failed" && operation?.error?.code === "already_finished")) {
    return {
      state: "already_finished",
      phase,
      original: operation?.result?.result ?? operation?.result ?? null,
      // 结束优先：不把 completed/failed/timed_out 追改成 cancelled。
      mutatedPhase: false,
    };
  }

  if (phase === "cancelled" || phase === "timed_out") {
    if (run.process_stop_confirmed === true) {
      return { state: "stopped", phase, stopConfirmed: true };
    }
    // 阶段已是 cancelled 但停止未确认：只能说取消生效，停止仍待证据。
    return { state: "accepted", phase, stopConfirmed: false };
  }

  if (isTerminal(phase) && (phase === "completed" || phase === "failed")) {
    return { state: "already_finished", phase, original: run.result ?? null, mutatedPhase: false };
  }

  if (wanted || run.desired_action === "stop") {
    return { state: "accepted", phase, stopConfirmed: false };
  }
  return { state: "idle", phase, stopConfirmed: null };
}

/**
 * 三层状态分别返回，禁止互相推导（契约 §1）。
 */
export function describeLayers(run = {}) {
  const phase = RUN_PHASES.includes(run.phase) ? run.phase : "unknown";
  const delivery = run.delivery || {};
  const deliveryState = delivery.merged
    ? "merged"
    : delivery.frozen === true || run.delivery_frozen === true
      ? "pending"
      : run.delivery_frozen === false || delivery.frozen === false
        ? "not_frozen"
        : "unknown";

  return {
    run: {
      phase,
      reconciliation: RECONCILIATION_STATES.includes(run.reconciliation_state)
        ? run.reconciliation_state
        : "unknown",
      terminal: isTerminal(phase),
    },
    delivery: { state: deliveryState, frozen: deliveryState !== "not_frozen" && deliveryState !== "unknown" },
    task: {
      status: run.task_status || run.task?.status || null,
      // completed 只说明协议走完，不代表验收通过（§1）。
      completedImpliesDone: false,
    },
  };
}

/**
 * 待核实面板的四块内容：最后事实、未知点、占用、处理入口。
 * 不给强制释放入口——证据不足时先核实。
 */
export function resolveUnknown(run = {}) {
  const facts = [];
  const phase = RUN_PHASES.includes(run.phase) ? run.phase : null;
  const lastEvent = Array.isArray(run.events) && run.events.length
    ? run.events[run.events.length - 1]
    : null;

  if (phase) {
    facts.push({ kind: "phase", phase, at: lastEvent?.received_at || run.updated_at || null,
      quality: QUALITIES.includes(lastEvent?.quality) ? lastEvent.quality : "unknown" });
  }
  if (run.last_heartbeat_at) {
    facts.push({ kind: "heartbeat", at: run.last_heartbeat_at, quality: "observed" });
  }

  const points = [];
  if (!run.last_heartbeat_at) points.push("unknown.point.no_heartbeat");
  if (run.process_stop_confirmed !== true) points.push("unknown.point.no_evidence");
  if (run.unattributed_records > 0) points.push("unknown.point.unattributed");
  if (!points.length) points.push("unknown.point.no_evidence");

  const occupancy = stillOccupying(run);
  const eligibility = releaseEligibility(run);

  return {
    facts,
    points,
    occupancy,
    releases: eligibility,
    actions: [
      { id: "recheck", labelKey: "unknown.action.recheck" },
      { id: "report", labelKey: "unknown.action.report" },
    ],
    forcedRelease: false,
  };
}

// ---------- 渲染 ----------

function badge(text, cls) {
  return '<span class="badge ' + cls + '">' + safeText(text) + "</span>";
}

function emptyState(lang, keys) {
  return '<div class="empty-state"><p class="empty-state__text">' + safeText(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + safeText(t(keys[1], lang)) + "</p></div>";
}

function phaseBadgeClass(phase) {
  if (phase === "completed") return "badge--done";
  if (phase === "failed" || phase === "timed_out") return "badge--blocked";
  if (phase === "cancelled") return "badge--self_check";
  if (phase === "stopping" || phase === "waiting") return "badge--claimed";
  return "badge--in_progress";
}

function layerRow(labelKey, value, noteKey, lang, cls = "") {
  return "<tr><th>" + safeText(t(labelKey, lang)) + "</th>" +
    "<td>" + badge(value, cls || "badge--in_review") +
    '<span class="field-hint"> ' + safeText(t(noteKey, lang)) + "</span></td></tr>";
}

export function renderRunList(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";

  if (state.status === "loading") return '<p class="nodes-loading">' + safeText(t("runs.loading", lang)) + "</p>";
  if (state.status === "unauthenticated") return emptyState(lang, ["runs.unauthenticated", "runs.unauthenticated.hint"]);
  if (state.status === "error") return emptyState(lang, ["runs.error", "runs.error.hint"]);
  if (state.status === "expired") return emptyState(lang, ["runs.expired", "runs.expired.hint"]);

  const runs = Array.isArray(state.runs) ? state.runs : [];
  const header = '<header class="nodes-head"><h1>' + safeText(t("runs.list", lang)) + "</h1>" +
    '<p class="field-hint">' + safeText(t("runs.subtitle", lang)) + "</p></header>";
  if (!runs.length) return header + emptyState(lang, ["runs.empty", "runs.empty.hint"]);

  const rows = runs.map(run => {
    const layers = describeLayers(run);
    const occ = stillOccupying(run);
    return "<tr>" +
      '<td class="id">' + safeText(run.run_id || run.id || "—") + "</td>" +
      "<td>" + badge(t("phase." + layers.run.phase, lang), phaseBadgeClass(layers.run.phase)) + "</td>" +
      "<td>" + badge(t("rec." + layers.run.reconciliation, lang),
        layers.run.reconciliation === "confirmed" ? "badge--done" : "badge--claimed") + "</td>" +
      "<td>" + safeText(t("delivery." + layers.delivery.state, lang)) + "</td>" +
      '<td class="name">' + safeText(layers.task.status || t("task.unknown", lang)) + "</td>" +
      "<td>" + badge(occ.occupying ? t("occ.held", lang) : t("occ.released", lang),
        occ.occupying ? "badge--claimed" : "badge--self_check") + "</td>" +
      "</tr>";
  }).join("");

  return header +
    '<p class="field-hint">' + safeText(runs.length + " " + t("runs.count", lang)) + "</p>" +
    '<div class="table-wrap"><table class="table"><thead><tr>' +
    ["id", "layer.run", "rec.confirmed", "layer.delivery", "layer.task", "occ.title"].map(key =>
      "<th>" + safeText(t(key === "id" ? "oc.id" : key, lang)) + "</th>"
    ).join("") +
    "</tr></thead><tbody>" + rows + "</tbody></table></div>";
}

/** 运行详情：三层分开 + 取消状态 + 占用 + 日志（纯文本）。 */
export function renderRunDetail(run = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  if (!run || !run.run_id && !run.id) return emptyState(lang, ["runs.empty", "runs.empty.hint"]);

  const layers = describeLayers(run);
  const cancel = describeCancel(run, options.operation || null);
  const occ = stillOccupying(run);
  const eligibility = releaseEligibility(run);

  const cancelClass = cancel.state === "stopped"
    ? "badge--self_check"
    : cancel.state === "already_finished"
      ? "badge--in_review"
      : cancel.state === "accepted"
        ? "badge--claimed"
        : "badge--self_check";

  const occupancyRows = ["node_slot", "workspace"].map(res => {
    const item = eligibility[res];
    return "<li>" + safeText(t("occ." + res, lang)) + " · " +
      badge(item.releasable ? t("occ.released", lang) : t("occ.held", lang),
        item.releasable ? "badge--self_check" : "badge--claimed") +
      (item.blockedBy ? '<span class="field-hint"> ' + safeText(t(item.blockedBy, lang)) + "</span>" : "") +
      "</li>";
  }).join("");

  const logs = Array.isArray(run.events) && run.events.length
    ? '<pre class="run-log">' + run.events.map(ev =>
        safeText("[" + (ev.received_at || ev.occurred_at || "?") + "] " +
          (ev.type || "") + " " + (ev.message || ""))
      ).join("\n") + "</pre>"
    : '<p class="field-hint">' + safeText(t("log.empty", lang)) + "</p>";

  return '<article class="run-detail">' +
    '<header class="nodes-head">' +
    "<h1>" + safeText(run.run_id || run.id) + "</h1>" +
    '<p class="field-hint">' + safeText(t("gen.label", lang)) + " " + safeText(run.generation ?? "—") + "</p>" +
    "</header>" +
    '<div class="table-wrap"><table class="table">' +
    layerRow("layer.run", t("phase." + layers.run.phase, lang) + " · " + t("rec." + layers.run.reconciliation, lang),
      "layer.run.note", lang, phaseBadgeClass(layers.run.phase)) +
    layerRow("layer.delivery", t("delivery." + layers.delivery.state, lang), "layer.delivery.note", lang) +
    layerRow("layer.task", layers.task.status || t("task.unknown", lang), "layer.task.note", lang) +
    "</table></div>" +
    '<section class="drawer__section"><h3>' + safeText(t("cancel.title", lang)) + "</h3>" +
    '<p>' + badge(t("cancel." + cancel.state, lang, { result: JSON.stringify(cancel.original ?? null) }), cancelClass) + "</p>" +
    '<p class="field-hint">' + safeText(t("cancel.note", lang)) + "</p></section>" +
    '<section class="drawer__section"><h3>' + safeText(t("occ.title", lang)) + "</h3>" +
    '<ul class="field-hint">' + occupancyRows +
    (occ.reasons.length
      ? '<li>' + safeText(t("occ.held_for", lang, { reason: occ.reasons.map(r => t(r, lang)).join(" / ") })) + "</li>"
      : "") +
    "</ul></section>" +
    '<section class="drawer__section"><h3>' + safeText(t("log.title", lang)) + "</h3>" + logs + "</section>" +
    "</article>";
}

/** 待核实面板：最后事实 / 未知点 / 占用 / 处理入口，且不提供强制释放。 */
export function renderUnknownPanel(run = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const info = resolveUnknown(run);

  const facts = info.facts.length
    ? "<ul>" + info.facts.map(fact => "<li>" +
        safeText(t("fact." + fact.kind, lang, {
          phase: fact.phase ? t("phase." + fact.phase, lang) : "—",
          at: fact.at || "—",
          quality: t("quality." + fact.quality, lang),
        })) + "</li>").join("") + "</ul>"
    : '<p class="field-hint">' + safeText(t("unknown.no_facts", lang)) + "</p>";

  const points = "<ul>" + info.points.map(key =>
    "<li>" + safeText(t(key, lang)) + "</li>").join("") + "</ul>";

  const occupancy = "<ul>" + ["node_slot", "workspace"].map(res => {
    const item = info.releases[res];
    return "<li>" + safeText(t("occ." + res, lang)) + " · " +
      badge(item.releasable ? t("release.ready", lang) : t("occ.held", lang),
        item.releasable ? "badge--self_check" : "badge--claimed") +
      (item.blockedBy ? '<span class="field-hint"> ' + safeText(t(item.blockedBy, lang)) + "</span>" : "") +
      "</li>";
  }).join("") + "</ul>";

  const actions = '<div class="field">' +
    info.actions.map(action =>
      '<button type="button" class="button" data-action="' + safeText(action.id) + '">' +
      safeText(t(action.labelKey, lang)) + "</button>"
    ).join("") + "</div>";

  return '<section class="unknown-panel" data-reconciliation="unknown">' +
    "<h2>" + safeText(t("unknown.title", lang)) + "</h2>" +
    '<p class="field-hint">' + safeText(t("rec.unknown.hint", lang)) + "</p>" +
    '<div class="drawer__section"><h3>' + safeText(t("unknown.last_facts", lang)) + "</h3>" + facts + "</div>" +
    '<div class="drawer__section"><h3>' + safeText(t("unknown.points", lang)) + "</h3>" + points + "</div>" +
    '<div class="drawer__section"><h3>' + safeText(t("occ.title", lang)) + "</h3>" + occupancy + "</div>" +
    '<div class="drawer__section"><h3>' + safeText(lang === "en" ? "Handling" : "处理入口") + "</h3>" +
    actions + '<p class="field-hint">' + safeText(t("unknown.action.note", lang)) + "</p></div>" +
    "</section>";
}
