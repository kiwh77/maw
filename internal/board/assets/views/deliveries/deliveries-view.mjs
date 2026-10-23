// 交付与验收详情页（vnext / UI · VN-724）。
//
// 目标：把「这次交出来的东西算不算完成」讲成可核对的证据链，而不是把
// 「进程退出 0」当成验收通过。
//
// 依据：
//   docs/contracts/vnext/delivery/delivery-manifest-contract-v1.md（maw.delivery.manifest/v1）
//     §1 三条硬规则：退出码 0 只能 pending（E333）；产物查不到实体不算数（E320/E341）；
//        旧提交上的验证不能给新提交背书（E331/E334）；
//     §2 Manifest 必含 task_instance / task_version / run_id / baseline_commit / mode / completion；
//     §3 产物只是候选：exists 与 in_write_scope 由 maw 侧核实（E320/E321）；
//     §4 验证结论分等级：pass / manual / fail / pending / stale，只有结果类证据算通过（E332/E333）；
//     §5 代码与非代码两套完成条件（E340–E343、E350/E351、E361）；
//     §6 重复交付：被取代的交付不得保留 done（E370），supersedes 不得自指（E371）。
//   docs/contracts/vnext/ui/WORKBENCH_UI_CONTRACT.md
//     §2–§3 token 与字级、§6 表格、§8 空态、§10 键盘焦点、§12 中英双语、§13 窄屏。
//   docs/contracts/vnext/ui-data/view-context-contract-v1.md §3：视图自带局部字典，
//     键必须命名空间化为 view.<view_id>.<key>，中英成对。
//
// 三条硬规则：
//   1. 运行结束 ≠ 验收通过。退出码只能当候选证据，四类阶段分开显示。
//   2. 不知道就说不知道。绑定字段缺失时不给 done/pending 结论，只列缺失字段与处理入口。
//   3. 旧证据不背书新交付。版本、提交、产物哈希三者任一不符，验证即为失效。
//
// 本视图只做展示与推导，不实现请求、不写导航、不新增公共层：
// 复用 assets/components 的 table / badge / form / empty 类名，样式来自 tokens.css。
// 接线（页面挂载与数据读取）由后续任务完成，本模块保持纯函数 + 字符串渲染，便于注入测试。

export const CONTRACT = "maw.delivery.manifest/v1";
export const MODES = Object.freeze(["code", "non_code"]);
export const VERIFICATION_RESULTS = Object.freeze(["pass", "manual", "fail", "pending", "stale"]);
export const DECISIONS = Object.freeze(["done", "pending", "rejected"]);

/** 交付的四个阶段（F28）：运行结束、待验收、验收通过、任务完成互不推导。 */
export const STAGES = Object.freeze(["run_finished", "delivery_pending", "accepted", "task_done"]);

const I18N = {
  zh: {
    "view.deliveries.title": "交付与验收",
    "view.deliveries.subtitle": "产物、验证对象与结论逐条对上；运行结束不等于验收通过。",
    "view.deliveries.count": "次交付",
    "view.deliveries.empty": "没有可显示的交付记录。",
    "view.deliveries.empty.hint": "换个任务或工程看看。",
    "view.deliveries.error": "交付记录加载失败。",
    "view.deliveries.error.hint": "确认本机中枢在运行，然后重试。",
    "view.deliveries.expired": "交付数据已过期。",
    "view.deliveries.expired.hint": "刷新后重新读取，不沿用上次结论。",
    "view.deliveries.loading": "正在读取交付记录…",
    "view.deliveries.unauthenticated": "未登录，无法查看交付记录。",
    "view.deliveries.unauthenticated.hint": "使用本机会话或令牌后重试。",
    "view.deliveries.reload": "重新读取",
    "view.deliveries.reload.note": "重新读取只刷新证据，不改写已有结论。",
    "view.deliveries.unknown.title": "无法判定",
    "view.deliveries.unknown.hint": "绑定字段缺失时不给结论：先补齐身份，再判定完成。",
    "view.deliveries.unknown.missing": "缺失字段",
    "view.deliveries.unknown.action.recheck": "重新读取",
    "view.deliveries.unknown.action.note": "缺失绑定属于交付本身的问题，不提供「强制完成」入口。",
    "view.deliveries.section.decision": "完成判定",
    "view.deliveries.section.stages": "阶段",
    "view.deliveries.section.run": "运行结果",
    "view.deliveries.section.binding": "绑定",
    "view.deliveries.section.artifacts": "产物",
    "view.deliveries.section.verifications": "验证",
    "view.deliveries.section.review": "评审与合入",
    "view.deliveries.section.trace": "追溯",
    "view.deliveries.decision.done": "已完成",
    "view.deliveries.decision.pending": "待验收",
    "view.deliveries.decision.rejected": "已拒绝",
    "view.deliveries.decision.unknown": "无法判定",
    "view.deliveries.decision.declared": "声明的结论",
    "view.deliveries.decision.shortfall": "声明为「已完成」但证据不足：按待验收处理，不继承旧结论。",
    "view.deliveries.decision.all_met": "完成条件全部满足。",
    "view.deliveries.decision.blocked": "未满足的条件",
    "view.deliveries.stage.run_finished": "运行结束",
    "view.deliveries.stage.delivery_pending": "交付待验收",
    "view.deliveries.stage.accepted": "验收通过",
    "view.deliveries.stage.task_done": "任务完成",
    "view.deliveries.stage.on": "是",
    "view.deliveries.stage.off": "否",
    "view.deliveries.stage.unknown": "未知",
    "view.deliveries.stage.col.stage": "阶段",
    "view.deliveries.stage.col.state": "结论",
    "view.deliveries.stage.col.note": "说明",
    "view.deliveries.bind.field": "字段",
    "view.deliveries.bind.value": "值",
    "view.deliveries.stage.run_finished.note": "运行阶段已进入终态；只说明这次执行结束。",
    "view.deliveries.stage.delivery_pending.note": "交付已有记录，但完成条件未全部满足。",
    "view.deliveries.stage.accepted.note": "产物、验证、评审与合入同时成立。",
    "view.deliveries.stage.task_done.note": "任务状态为完成且交付已验收；任一层缺失都不算完成。",
    "view.deliveries.stage.unknown.note": "没有任务状态，不推断完成。",
    "view.deliveries.run.title": "运行结果",
    "view.deliveries.run.exit_code": "退出码",
    "view.deliveries.run.success": "进程退出成功（仅候选证据）",
    "view.deliveries.run.failed": "进程退出失败",
    "view.deliveries.run.unknown": "没有运行结果",
    "view.deliveries.run.note": "运行结束不是验收通过：退出码只能作为候选证据，完成仍需产物、验证、评审与合入。",
    "view.deliveries.bind.instance": "任务实例",
    "view.deliveries.bind.version": "定义版本",
    "view.deliveries.bind.run": "运行",
    "view.deliveries.bind.mode": "模式",
    "view.deliveries.bind.baseline": "基线提交",
    "view.deliveries.bind.candidate": "候选提交",
    "view.deliveries.bind.delivered_at": "交付时间",
    "view.deliveries.bind.fingerprint": "产物指纹",
    "view.deliveries.bind.supersedes": "取代的交付",
    "view.deliveries.bind.superseded_by": "已被取代为",
    "view.deliveries.bind.none": "无",
    "view.deliveries.mode.code": "代码",
    "view.deliveries.mode.non_code": "非代码",
    "view.deliveries.artifacts.empty": "没有声明产物。",
    "view.deliveries.artifacts.id": "产物",
    "view.deliveries.artifacts.kind": "类型",
    "view.deliveries.artifacts.ref": "引用",
    "view.deliveries.artifacts.declared_by": "声明来源",
    "view.deliveries.artifacts.exists": "实体存在",
    "view.deliveries.artifacts.scope": "在写盘范围",
    "view.deliveries.artifacts.sha": "内容哈希",
    "view.deliveries.artifacts.counted": "计入交付",
    "view.deliveries.artifacts.counted.yes": "计入",
    "view.deliveries.artifacts.counted.no": "不计入",
    "view.deliveries.artifacts.by.cli": "工具声明（候选）",
    "view.deliveries.artifacts.by.manual": "人工确认",
    "view.deliveries.artifacts.kind.commit": "提交",
    "view.deliveries.artifacts.kind.file": "文件",
    "view.deliveries.verifications.empty": "没有验证记录。",
    "view.deliveries.verifications.command": "命令",
    "view.deliveries.verifications.target_version": "目标版本",
    "view.deliveries.verifications.target_commit": "目标提交",
    "view.deliveries.verifications.target_artifact": "目标产物",
    "view.deliveries.verifications.result": "结论",
    "view.deliveries.verifications.evidence": "证据",
    "view.deliveries.verifications.ran_at": "执行时间",
    "view.deliveries.verifications.supports": "可支撑完成",
    "view.deliveries.verifications.supports.yes": "可支撑",
    "view.deliveries.verifications.supports.no": "不支撑",
    "view.deliveries.verifications.evidence.none": "没有证据",
    "view.deliveries.result.pass": "通过",
    "view.deliveries.result.manual": "人工验收",
    "view.deliveries.result.fail": "失败",
    "view.deliveries.result.pending": "未定",
    "view.deliveries.result.stale": "已失效",
    "view.deliveries.review.required": "需要评审",
    "view.deliveries.review.verdict": "评审结论",
    "view.deliveries.review.reviewer": "评审人",
    "view.deliveries.review.reviewed_at": "评审时间",
    "view.deliveries.review.approved": "通过",
    "view.deliveries.review.rejected": "未通过",
    "view.deliveries.review.not_required": "不需要评审",
    "view.deliveries.review.none": "未记录",
    "view.deliveries.merge.required": "需要合入",
    "view.deliveries.merge.merged": "已合入",
    "view.deliveries.merge.merged_commit": "合入提交",
    "view.deliveries.merge.merged_at": "合入时间",
    "view.deliveries.merge.not_merged": "未合入",
    "view.deliveries.merge.not_applicable": "非代码交付不记合入",
    "view.deliveries.completion.decided_at": "判定时间",
    "view.deliveries.completion.reason": "原因",
    "view.deliveries.trace.time": "时间",
    "view.deliveries.trace.kind": "事件",
    "view.deliveries.trace.actor": "操作人",
    "view.deliveries.trace.detail": "内容",
    "view.deliveries.trace.empty": "没有可追溯的记录。",
    "view.deliveries.trace.delivery": "交付",
    "view.deliveries.trace.verification": "验证",
    "view.deliveries.trace.review": "评审",
    "view.deliveries.trace.merge": "合入",
    "view.deliveries.trace.completion": "完成判定",
    "view.deliveries.trace.supersede": "取代",
    "view.deliveries.col.delivery": "交付",
    "view.deliveries.col.instance": "任务实例",
    "view.deliveries.col.version": "版本",
    "view.deliveries.col.mode": "模式",
    "view.deliveries.col.artifacts": "产物",
    "view.deliveries.col.verification": "验证",
    "view.deliveries.col.merge": "合入",
    "view.deliveries.col.decision": "判定",
    "view.deliveries.list.detail": "打开详情后判定",
    "view.deliveries.code.E300": "必填字段缺失或类型不对。",
    "view.deliveries.code.E310": "未绑定任务实例，这份交付没有身份。",
    "view.deliveries.code.E311": "未绑定任务定义版本。",
    "view.deliveries.code.E312": "未绑定产生它的运行。",
    "view.deliveries.code.E313": "基线与候选提交必须是 7–40 位十六进制哈希。",
    "view.deliveries.code.E320": "产物查不到实体，不计入交付。",
    "view.deliveries.code.E321": "产物越出本卡写盘范围。",
    "view.deliveries.code.E330": "验证的目标版本不是本交付的版本。",
    "view.deliveries.code.E331": "验证针对旧提交，不能给新提交背书。",
    "view.deliveries.code.E332": "判定通过必须带结果类证据。",
    "view.deliveries.code.E333": "只有退出码不算通过。",
    "view.deliveries.code.E334": "产物已变更，旧验证对当前产物失效。",
    "view.deliveries.code.E340": "缺少一条有效通过的验证。",
    "view.deliveries.code.E341": "缺少计入交付的产物。",
    "view.deliveries.code.E342": "代码交付未合入，不能算完成。",
    "view.deliveries.code.E343": "需要评审且评审未通过。",
    "view.deliveries.code.E350": "非代码交付缺少人工或机器验收结论。",
    "view.deliveries.code.E351": "非代码交付不得记录合入。",
    "view.deliveries.code.E360": "拒绝必须写明原因。",
    "view.deliveries.code.E361": "已完成时不允许存在失败验证。",
    "view.deliveries.code.E370": "已被取代的交付不得保留完成结论。",
    "view.deliveries.code.E371": "取代必须指向另一份交付，不能自指或指向不存在。",
  },
  en: {
    "view.deliveries.title": "Deliveries",
    "view.deliveries.subtitle": "Artifacts, verification targets and conclusions line up row by row. A finished run is not an accepted delivery.",
    "view.deliveries.count": "deliveries",
    "view.deliveries.empty": "No delivery records to show.",
    "view.deliveries.empty.hint": "Try another task or project.",
    "view.deliveries.error": "Failed to load delivery records.",
    "view.deliveries.error.hint": "Check that the local hub is running, then retry.",
    "view.deliveries.expired": "Delivery data is out of date.",
    "view.deliveries.expired.hint": "Refresh to re-read; previous conclusions are not reused.",
    "view.deliveries.loading": "Loading delivery records…",
    "view.deliveries.unauthenticated": "Not signed in, cannot read delivery records.",
    "view.deliveries.unauthenticated.hint": "Use a local session or token and retry.",
    "view.deliveries.reload": "Reload",
    "view.deliveries.reload.note": "Reloading only refreshes evidence; it never rewrites an existing conclusion.",
    "view.deliveries.unknown.title": "Cannot decide",
    "view.deliveries.unknown.hint": "Without bindings there is no conclusion: restore the identity first, then decide.",
    "view.deliveries.unknown.missing": "Missing fields",
    "view.deliveries.unknown.action.recheck": "Reload",
    "view.deliveries.unknown.action.note": "Missing bindings are a defect in the delivery itself; there is no force-complete action.",
    "view.deliveries.section.decision": "Completion",
    "view.deliveries.section.stages": "Stages",
    "view.deliveries.section.run": "Run outcome",
    "view.deliveries.section.binding": "Bindings",
    "view.deliveries.section.artifacts": "Artifacts",
    "view.deliveries.section.verifications": "Verification",
    "view.deliveries.section.review": "Review and merge",
    "view.deliveries.section.trace": "Trace",
    "view.deliveries.decision.done": "Done",
    "view.deliveries.decision.pending": "Pending acceptance",
    "view.deliveries.decision.rejected": "Rejected",
    "view.deliveries.decision.unknown": "Cannot decide",
    "view.deliveries.decision.declared": "Declared conclusion",
    "view.deliveries.decision.shortfall": "Declared done but evidence is insufficient: treated as pending, old conclusions are not inherited.",
    "view.deliveries.decision.all_met": "All completion conditions are met.",
    "view.deliveries.decision.blocked": "Unmet conditions",
    "view.deliveries.stage.run_finished": "Run finished",
    "view.deliveries.stage.delivery_pending": "Delivery pending",
    "view.deliveries.stage.accepted": "Accepted",
    "view.deliveries.stage.task_done": "Task done",
    "view.deliveries.stage.on": "Yes",
    "view.deliveries.stage.off": "No",
    "view.deliveries.stage.unknown": "Unknown",
    "view.deliveries.stage.col.stage": "Stage",
    "view.deliveries.stage.col.state": "State",
    "view.deliveries.stage.col.note": "Note",
    "view.deliveries.bind.field": "Field",
    "view.deliveries.bind.value": "Value",
    "view.deliveries.stage.run_finished.note": "The run reached a terminal phase; it only says this execution ended.",
    "view.deliveries.stage.delivery_pending.note": "A delivery exists, but not all completion conditions hold.",
    "view.deliveries.stage.accepted.note": "Artifacts, verification, review and merge all hold.",
    "view.deliveries.stage.task_done.note": "Task state is done and the delivery is accepted; missing either one is not done.",
    "view.deliveries.stage.unknown.note": "No task state available; completion is not inferred.",
    "view.deliveries.run.title": "Run outcome",
    "view.deliveries.run.exit_code": "Exit code",
    "view.deliveries.run.success": "Process exited 0 (candidate evidence only)",
    "view.deliveries.run.failed": "Process exited non-zero",
    "view.deliveries.run.unknown": "No run outcome",
    "view.deliveries.run.note": "A finished run is not an accepted delivery: an exit code is candidate evidence only. Completion still needs artifacts, verification, review and merge.",
    "view.deliveries.bind.instance": "Task instance",
    "view.deliveries.bind.version": "Definition version",
    "view.deliveries.bind.run": "Run",
    "view.deliveries.bind.mode": "Mode",
    "view.deliveries.bind.baseline": "Baseline commit",
    "view.deliveries.bind.candidate": "Candidate commit",
    "view.deliveries.bind.delivered_at": "Delivered at",
    "view.deliveries.bind.fingerprint": "Artifact fingerprint",
    "view.deliveries.bind.supersedes": "Supersedes",
    "view.deliveries.bind.superseded_by": "Superseded by",
    "view.deliveries.bind.none": "None",
    "view.deliveries.mode.code": "Code",
    "view.deliveries.mode.non_code": "Non-code",
    "view.deliveries.artifacts.empty": "No artifacts declared.",
    "view.deliveries.artifacts.id": "Artifact",
    "view.deliveries.artifacts.kind": "Kind",
    "view.deliveries.artifacts.ref": "Ref",
    "view.deliveries.artifacts.declared_by": "Declared by",
    "view.deliveries.artifacts.exists": "Exists",
    "view.deliveries.artifacts.scope": "In write scope",
    "view.deliveries.artifacts.sha": "SHA-256",
    "view.deliveries.artifacts.counted": "Counted",
    "view.deliveries.artifacts.counted.yes": "Counted",
    "view.deliveries.artifacts.counted.no": "Not counted",
    "view.deliveries.artifacts.by.cli": "Declared by tool (candidate)",
    "view.deliveries.artifacts.by.manual": "Confirmed manually",
    "view.deliveries.artifacts.kind.commit": "Commit",
    "view.deliveries.artifacts.kind.file": "File",
    "view.deliveries.verifications.empty": "No verification records.",
    "view.deliveries.verifications.command": "Command",
    "view.deliveries.verifications.target_version": "Target version",
    "view.deliveries.verifications.target_commit": "Target commit",
    "view.deliveries.verifications.target_artifact": "Target artifact",
    "view.deliveries.verifications.result": "Result",
    "view.deliveries.verifications.evidence": "Evidence",
    "view.deliveries.verifications.ran_at": "Ran at",
    "view.deliveries.verifications.supports": "Supports completion",
    "view.deliveries.verifications.supports.yes": "Supports",
    "view.deliveries.verifications.supports.no": "Does not support",
    "view.deliveries.verifications.evidence.none": "No evidence",
    "view.deliveries.result.pass": "Passed",
    "view.deliveries.result.manual": "Manual acceptance",
    "view.deliveries.result.fail": "Failed",
    "view.deliveries.result.pending": "Undetermined",
    "view.deliveries.result.stale": "Stale",
    "view.deliveries.review.required": "Review required",
    "view.deliveries.review.verdict": "Verdict",
    "view.deliveries.review.reviewer": "Reviewer",
    "view.deliveries.review.reviewed_at": "Reviewed at",
    "view.deliveries.review.approved": "Approved",
    "view.deliveries.review.rejected": "Rejected",
    "view.deliveries.review.not_required": "Not required",
    "view.deliveries.review.none": "Not recorded",
    "view.deliveries.merge.required": "Merge required",
    "view.deliveries.merge.merged": "Merged",
    "view.deliveries.merge.merged_commit": "Merged commit",
    "view.deliveries.merge.merged_at": "Merged at",
    "view.deliveries.merge.not_merged": "Not merged",
    "view.deliveries.merge.not_applicable": "Non-code deliveries do not record a merge",
    "view.deliveries.completion.decided_at": "Decided at",
    "view.deliveries.completion.reason": "Reason",
    "view.deliveries.trace.time": "Time",
    "view.deliveries.trace.kind": "Event",
    "view.deliveries.trace.actor": "Actor",
    "view.deliveries.trace.detail": "Detail",
    "view.deliveries.trace.empty": "No traceable records.",
    "view.deliveries.trace.delivery": "Delivery",
    "view.deliveries.trace.verification": "Verification",
    "view.deliveries.trace.review": "Review",
    "view.deliveries.trace.merge": "Merge",
    "view.deliveries.trace.completion": "Completion decision",
    "view.deliveries.trace.supersede": "Supersession",
    "view.deliveries.col.delivery": "Delivery",
    "view.deliveries.col.instance": "Task instance",
    "view.deliveries.col.version": "Version",
    "view.deliveries.col.mode": "Mode",
    "view.deliveries.col.artifacts": "Artifacts",
    "view.deliveries.col.verification": "Verification",
    "view.deliveries.col.merge": "Merge",
    "view.deliveries.col.decision": "Decision",
    "view.deliveries.list.detail": "Open detail to decide",
    "view.deliveries.code.E300": "A required field is missing or mistyped.",
    "view.deliveries.code.E310": "No task instance bound; this delivery has no identity.",
    "view.deliveries.code.E311": "No task definition version bound.",
    "view.deliveries.code.E312": "No producing run bound.",
    "view.deliveries.code.E313": "Baseline and candidate must be 7–40 hex commit hashes.",
    "view.deliveries.code.E320": "Artifact entity not found; it is not counted.",
    "view.deliveries.code.E321": "Artifact is outside this card's write scope.",
    "view.deliveries.code.E330": "Verification targets a different task version.",
    "view.deliveries.code.E331": "Verification targets an old commit and cannot vouch for a new one.",
    "view.deliveries.code.E332": "A pass must carry result-class evidence.",
    "view.deliveries.code.E333": "An exit code alone is not a pass.",
    "view.deliveries.code.E334": "The artifact changed; the old verification is stale.",
    "view.deliveries.code.E340": "No valid passing verification.",
    "view.deliveries.code.E341": "No counted artifact.",
    "view.deliveries.code.E342": "A code delivery is not merged and cannot be done.",
    "view.deliveries.code.E343": "Review is required and was not approved.",
    "view.deliveries.code.E350": "A non-code delivery lacks a manual or machine acceptance conclusion.",
    "view.deliveries.code.E351": "A non-code delivery must not record a merge.",
    "view.deliveries.code.E360": "A rejection must state a reason.",
    "view.deliveries.code.E361": "A done delivery must not carry failed verification.",
    "view.deliveries.code.E370": "A superseded delivery must not keep a done conclusion.",
    "view.deliveries.code.E371": "Supersession must point to another delivery, not to itself or a missing one.",
  },
};

const COMMIT_RE = /^[0-9a-f]{7,40}$/i;

/** 局部字典：view.<view_id>.<key> 命名空间化，中英成对（ui-data 契约 V130/V131）。 */
export function dictionary(lang) {
  return { ...(I18N[lang] || I18N.zh) };
}

/** 解析顺序：当前语言 → 中文 → 原样 key，不返回空字符串。 */
export function t(key, lang = "zh", params = {}) {
  const pack = I18N[lang] || I18N.zh;
  const raw = pack[key] ?? I18N.zh[key] ?? key;
  return String(raw).replace(/\{(\w+)\}/g, (_, name) =>
    params[name] === undefined ? "" : String(params[name])
  );
}

/** 文本安全渲染：一律转义，不解析任何 HTML。 */
export function safeText(value) {
  return String(value ?? "").replace(/[&<>"']/g, ch => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[ch]));
}

export const esc = safeText;

const isObj = v => v !== null && typeof v === "object" && !Array.isArray(v);
const asArray = v => (Array.isArray(v) ? v : []);
const commitOk = v => typeof v === "string" && COMMIT_RE.test(v);
const blocker = (code, field) => ({ code, key: "view.deliveries.code." + code, field: field || null });

// ---------- 产物：候选不是事实 ----------

/**
 * 结果类证据：至少一条不是 exit_code: 前缀的记录。
 * 只有退出码一律视为无证据（契约 §4，E333）。
 */
export function hasResultEvidence(evidence) {
  return asArray(evidence).some(item => {
    const text = String(item ?? "").trim();
    return text !== "" && !text.startsWith("exit_code:");
  });
}

/** 产物是否计入交付：实体存在 + 未越界（E320／E321）。 */
export function artifactVerdict(artifact = {}) {
  if (artifact.exists !== true) return { accepted: false, code: "E320" };
  if (artifact.in_write_scope !== true) return { accepted: false, code: "E321" };
  return { accepted: true, code: null };
}

/** 拆出计入与不计入的产物，不计入的带原因码。 */
export function resolveArtifacts(delivery = {}) {
  const accepted = [];
  const rejected = [];
  for (const artifact of asArray(delivery.artifacts)) {
    const verdict = artifactVerdict(artifact);
    if (verdict.accepted) accepted.push(artifact);
    else rejected.push({ artifact, code: verdict.code });
  }
  return { accepted, rejected };
}

// ---------- 验证：旧证据不背书新交付 ----------

/**
 * 单条验证的结论（契约 §4／§6）。
 * - 版本不符 → E330；通过但提交不是候选提交 → E331；
 * - 目标产物哈希与当前产物不符 → E334；
 * - pass 缺少结果类证据 → E332（只有退出码时是 E333）。
 */
export function verificationVerdict(verification = {}, delivery = {}) {
  const result = verification.result;
  const targetVersion = Number(verification.target_task_version);
  const deliveryVersion = Number(delivery.task_version);

  if (Number.isFinite(targetVersion) && Number.isFinite(deliveryVersion) && targetVersion !== deliveryVersion) {
    return { result, supportsDone: false, code: "E330", link: "stale" };
  }

  if (delivery.mode === "code" && result === "pass" &&
      String(verification.target_commit || "") !== String(delivery.candidate_commit || "")) {
    return { result, supportsDone: false, code: "E331", link: "stale" };
  }

  if (verification.target_artifact_id) {
    const artifact = asArray(delivery.artifacts)
      .find(item => item && item.artifact_id === verification.target_artifact_id);
    const current = artifact ? String(artifact.sha256 || "") : "";
    if (!artifact || current !== String(verification.target_artifact_sha256 || "")) {
      return { result, supportsDone: false, code: "E334", link: "stale" };
    }
  }

  if (result === "stale") return { result, supportsDone: false, code: null, link: "stale" };
  if (result === "fail" || result === "pending") return { result, supportsDone: false, code: null, link: "invalid" };
  if (result === "manual") {
    // 人工结论只对非代码任务有效（E350 路径）。
    return { result, supportsDone: delivery.mode === "non_code", code: delivery.mode === "non_code" ? null : "E350", link: "manual" };
  }

  const evidence = asArray(verification.evidence);
  if (!hasResultEvidence(evidence)) {
    const onlyExit = evidence.length > 0;
    return { result, supportsDone: false, code: onlyExit ? "E333" : "E332", link: "noevidence" };
  }
  return { result, supportsDone: true, code: null, link: "valid" };
}

/** 逐条列出验证，带能否支撑完成的结论。 */
export function describeVerifications(delivery = {}) {
  return asArray(delivery.verification).map(verification => ({
    verification,
    verdict: verificationVerdict(verification, delivery),
  }));
}

// ---------- 绑定与阶段 ----------

/** 绑定缺口：缺任一必含字段都不构成可判定交付（E310–E313、E300）。 */
export function bindingGaps(delivery = {}) {
  const gaps = [];
  if (!delivery.task_instance) gaps.push(blocker("E310", "view.deliveries.bind.instance"));
  if (!(Number.isFinite(Number(delivery.task_version)) && Number(delivery.task_version) > 0)) {
    gaps.push(blocker("E311", "view.deliveries.bind.version"));
  }
  if (!delivery.run_id) gaps.push(blocker("E312", "view.deliveries.bind.run"));
  if (!MODES.includes(delivery.mode)) gaps.push(blocker("E300", "view.deliveries.bind.mode"));
  if (!isObj(delivery.completion)) gaps.push(blocker("E300", "view.deliveries.completion.reason"));
  if (!commitOk(delivery.baseline_commit)) gaps.push(blocker("E313", "view.deliveries.bind.baseline"));
  if (delivery.mode === "code" && !commitOk(delivery.candidate_commit)) {
    gaps.push(blocker("E313", "view.deliveries.bind.candidate"));
  }
  return gaps;
}

/** 运行结果永远只是候选证据：退出码 0 不产生验收通过。 */
export function describeRunOutcome(run = null) {
  const exitCode = run && Number.isFinite(Number(run.exit_code)) ? Number(run.exit_code) : null;
  if (exitCode === null) {
    return { exitCode: null, success: null, supportsDone: false, key: "view.deliveries.run.unknown" };
  }
  return {
    exitCode,
    success: exitCode === 0,
    supportsDone: false,
    key: exitCode === 0 ? "view.deliveries.run.success" : "view.deliveries.run.failed",
  };
}

// ---------- 完成判定 ----------

/**
 * 推导完成判定（契约 §5）。返回的是「证据能支撑的结论」，
 * 与交付自己声明的 completion.decision 分开，用于标出「声明 done 但证据不足」。
 *
 * @param {object} delivery
 * @param {{siblings?: object[]}} [options] 同任务实例的其它交付，用于 E371 校验
 */
export function evaluateCompletion(delivery = {}, options = {}) {
  const gaps = bindingGaps(delivery);
  if (gaps.length) {
    return {
      state: "unknown",
      decision: "unknown",
      canBeDone: false,
      blockers: gaps,
      declared: isObj(delivery.completion) ? delivery.completion.decision || null : null,
      shortfall: false,
      acceptedArtifacts: [],
      rejectedArtifacts: [],
      verifications: [],
    };
  }

  const artifacts = resolveArtifacts(delivery);
  const verifications = describeVerifications(delivery);
  const blockers = [];

  for (const item of artifacts.rejected) blockers.push(blocker(item.code));
  if (artifacts.accepted.length === 0) blockers.push(blocker("E341"));

  const supporting = verifications.filter(item => item.verdict.supportsDone);
  if (delivery.mode === "code") {
    if (!supporting.length) blockers.push(blocker("E340"));
  } else {
    const manualOk = supporting.some(item => item.verification.result === "manual");
    if (!manualOk) blockers.push(blocker("E350"));
  }
  // 没凑出有效通过时，把每条验证自己的失效原因也摊开（E330/E331/E332/E333/E334）：
  // 「缺少有效验证」是结论，具体为什么不算数要能让用户核对。
  if (!supporting.length) {
    for (const item of verifications) {
      if (item.verdict.code) blockers.push(blocker(item.verdict.code));
    }
  }

  if (verifications.some(item => item.verification.result === "fail")) blockers.push(blocker("E361"));

  const review = isObj(delivery.review) ? delivery.review : {};
  if (delivery.mode === "code" && review.required !== false && review.verdict !== "approved") {
    blockers.push(blocker("E343"));
  }
  if (delivery.mode === "non_code" && review.required !== false && review.verdict !== "approved") {
    blockers.push(blocker("E343"));
  }

  const merge = isObj(delivery.merge) ? delivery.merge : {};
  if (delivery.mode === "code" && merge.merged !== true) blockers.push(blocker("E342"));
  if (delivery.mode === "non_code" && merge.merged === true) blockers.push(blocker("E351"));

  if (delivery.superseded_by) blockers.push(blocker("E370"));

  const supersedes = String(delivery.supersedes || "");
  if (supersedes) {
    const siblings = asArray(options.siblings);
    const self = delivery.delivery_id;
    const found = siblings.some(item => item && item.delivery_id === supersedes);
    if (supersedes === self || (siblings.length > 0 && !found)) blockers.push(blocker("E371"));
  }

  const completion = delivery.completion;
  const declared = completion.decision || null;
  const uniqueReasons = dedupe(blockers);

  if (declared === "rejected") {
    // 拒绝本身就把交付挡在 done 之外：未满足的完成条件照旧列出，但不推翻「拒绝」这一结论；
    // 只有「拒绝却没写原因」让这条结论不成立（E360）。
    const hasReason = String(completion.reason || "").trim() !== "";
    if (!hasReason) uniqueReasons.push(blocker("E360"));
    return {
      state: hasReason ? "rejected" : "pending",
      decision: "rejected",
      canBeDone: false,
      blockers: uniqueReasons,
      declared,
      shortfall: false,
      acceptedArtifacts: artifacts.accepted,
      rejectedArtifacts: artifacts.rejected,
      verifications,
    };
  }

  const canBeDone = uniqueReasons.length === 0;
  return {
    state: canBeDone ? "done" : "pending",
    decision: canBeDone ? "done" : "pending",
    canBeDone,
    blockers: uniqueReasons,
    declared,
    shortfall: declared === "done" && !canBeDone,
    acceptedArtifacts: artifacts.accepted,
    rejectedArtifacts: artifacts.rejected,
    verifications,
  };
}

function dedupe(items) {
  const seen = new Set();
  return items.filter(item => {
    if (seen.has(item.code)) return false;
    seen.add(item.code);
    return true;
  });
}

/** 四阶段分开：运行结束／待验收／验收通过／任务完成（F28）。 */
export function describeStages(delivery = {}, options = {}) {
  const decision = options.decision || evaluateCompletion(delivery, options);
  const run = options.run || null;
  const runFinished = Boolean(run) && ["completed", "failed", "cancelled", "timed_out"].includes(run.phase);
  const taskStatus = options.task_status || delivery.task_status || "";

  return [
    { id: "run_finished", on: runFinished, known: Boolean(run), noteKey: "view.deliveries.stage.run_finished.note" },
    {
      id: "delivery_pending",
      on: decision.state === "pending" || decision.state === "unknown",
      known: decision.state !== "unknown",
      noteKey: "view.deliveries.stage.delivery_pending.note",
    },
    {
      id: "accepted",
      on: decision.state === "done",
      known: decision.state !== "unknown",
      noteKey: "view.deliveries.stage.accepted.note",
    },
    {
      id: "task_done",
      on: taskStatus === "done" && decision.state === "done",
      known: taskStatus !== "",
      noteKey: taskStatus === "" ? "view.deliveries.stage.unknown.note" : "view.deliveries.stage.task_done.note",
    },
  ];
}

// ---------- 渲染 ----------

function badge(text, cls) {
  return '<span class="badge ' + cls + '">' + safeText(text) + "</span>";
}

function decisionClass(state) {
  if (state === "done") return "badge--done";
  if (state === "rejected") return "badge--blocked";
  if (state === "pending") return "badge--claimed";
  return "badge--in_review";
}

function emptyState(lang, keys, error = false) {
  return '<div class="empty-state' + (error ? " empty-state--error" : "") + '">' +
    '<p class="empty-state__text">' + safeText(t(keys[0], lang)) + "</p>" +
    '<p class="empty-state__text">' + safeText(t(keys[1], lang)) + "</p></div>";
}

function yesNo(value, lang, yesKey = "view.deliveries.stage.on", noKey = "view.deliveries.stage.off") {
  return value ? safeText(t(yesKey, lang)) : safeText(t(noKey, lang));
}

function tableWrap(headers, rows, lang) {
  return '<div class="table-wrap"><table class="table"><thead><tr>' +
    headers.map(key => "<th>" + safeText(t(key, lang)) + "</th>").join("") +
    "</tr></thead><tbody>" + rows + "</tbody></table></div>";
}

function kvRows(pairs, lang) {
  return pairs.map(([labelKey, value, mono]) =>
    "<tr><th>" + safeText(t(labelKey, lang)) + "</th>" +
    '<td class="' + (mono ? "id" : "name") + '">' + safeText(value) + "</td></tr>"
  ).join("");
}

function shortSha(value) {
  const text = String(value || "");
  return text.length > 12 ? text.slice(0, 12) + "…" : text;
}

function modeLabel(mode, lang) {
  return MODES.includes(mode) ? t("view.deliveries.mode." + mode, lang) : String(mode || "—");
}

/** 交付详情：判定、阶段、运行、绑定、产物、验证、评审与合入、追溯。 */
export function renderDeliveryDetail(delivery = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  if (!isObj(delivery) || (!delivery.delivery_id && !delivery.task_instance)) {
    return emptyState(lang, ["view.deliveries.empty", "view.deliveries.empty.hint"]);
  }

  const decision = evaluateCompletion(delivery, options);
  if (decision.state === "unknown") return renderUnknownPanel(delivery, options);

  const stages = describeStages(delivery, { ...options, decision });
  const run = describeRunOutcome(options.run || null);

  const header = '<header><h1>' + safeText(t("view.deliveries.title", lang)) + " · " +
    safeText(delivery.delivery_id || "—") + "</h1>" +
    '<p class="field-hint">' + safeText(t("view.deliveries.bind.instance", lang)) + " " +
    safeText(delivery.task_instance) + " · " + safeText(t("view.deliveries.bind.version", lang)) + " " +
    safeText(delivery.task_version) + " · " + safeText(modeLabel(delivery.mode, lang)) + "</p>" +
    '<div class="field"><button type="button" class="button" data-action="recheck">' +
    safeText(t("view.deliveries.reload", lang)) + "</button>" +
    '<span class="field-hint"> ' + safeText(t("view.deliveries.reload.note", lang)) + "</span></div>" +
    "</header>";

  const decisionSection = "<section><h2>" + safeText(t("view.deliveries.section.decision", lang)) + "</h2>" +
    "<p>" + badge(t("view.deliveries.decision." + decision.decision, lang), decisionClass(decision.state)) +
    '<span class="field-hint"> ' + safeText(t("view.deliveries.decision.declared", lang)) + " " +
    safeText(decision.declared || t("view.deliveries.bind.none", lang)) + "</span></p>" +
    (decision.shortfall
      ? '<p class="field-hint">' + safeText(t("view.deliveries.decision.shortfall", lang)) + "</p>"
      : "") +
    (decision.blockers.length
      ? "<h3>" + safeText(t("view.deliveries.decision.blocked", lang)) + "</h3><ul>" +
        decision.blockers.map(item => "<li>" +
          badge(item.code, "badge--blocked") + " " +
          safeText(t(item.key, lang)) +
          (item.field ? '<span class="field-hint"> ' + safeText(t(item.field, lang)) + "</span>" : "") +
          "</li>").join("") + "</ul>"
      : '<p class="field-hint">' + safeText(t("view.deliveries.decision.all_met", lang)) + "</p>") +
    "</section>";

  const stageSection = "<section><h2>" + safeText(t("view.deliveries.section.stages", lang)) + "</h2>" +
    tableWrap(
      ["view.deliveries.stage.col.stage", "view.deliveries.stage.col.state", "view.deliveries.stage.col.note"],
      stages.map(stage => "<tr><td class=\"name\">" + safeText(t("view.deliveries.stage." + stage.id, lang)) +
        "</td><td>" +
        badge(
          stage.known === false
            ? t("view.deliveries.stage.unknown", lang)
            : t(stage.on ? "view.deliveries.stage.on" : "view.deliveries.stage.off", lang),
          stage.on ? "badge--done" : stage.known === false ? "badge--in_review" : "badge--self_check"
        ) +
        '</td><td class="name">' + safeText(t(stage.noteKey, lang)) + "</td></tr>").join(""),
      lang
    ) + "</section>";

  const runSection = "<section><h2>" + safeText(t("view.deliveries.section.run", lang)) + "</h2>" +
    tableWrap(
      ["view.deliveries.run.exit_code", "view.deliveries.stage.col.state", "view.deliveries.stage.col.note"],
      "<tr><td class=\"id\">" + safeText(run.exitCode === null ? "—" : run.exitCode) + "</td><td>" +
      badge(t(run.key, lang), run.success === true ? "badge--claimed" : "badge--in_review") +
      '</td><td class="name">' + safeText(t("view.deliveries.run.note", lang)) + "</td></tr>",
      lang
    ) + "</section>";

  const bindingSection = "<section><h2>" + safeText(t("view.deliveries.section.binding", lang)) + "</h2>" +
    tableWrap(
      ["view.deliveries.bind.field", "view.deliveries.bind.value"],
      kvRows([
        ["view.deliveries.bind.instance", delivery.task_instance || "—", true],
        ["view.deliveries.bind.version", delivery.task_version ?? "—", true],
        ["view.deliveries.bind.run", delivery.run_id || "—", true],
        ["view.deliveries.bind.mode", modeLabel(delivery.mode, lang), false],
        ["view.deliveries.bind.baseline", delivery.baseline_commit || "—", true],
        ["view.deliveries.bind.candidate", delivery.candidate_commit || t("view.deliveries.bind.none", lang), true],
        ["view.deliveries.bind.delivered_at", delivery.delivered_at || "—", true],
        ["view.deliveries.bind.fingerprint", delivery.fingerprint || "—", true],
        ["view.deliveries.bind.supersedes", delivery.supersedes || t("view.deliveries.bind.none", lang), true],
        ["view.deliveries.bind.superseded_by", delivery.superseded_by || t("view.deliveries.bind.none", lang), true],
      ], lang),
      lang
    ) + "</section>";

  const artifactRows = decision.acceptedArtifacts.length + decision.rejectedArtifacts.length
    ? [
        ...decision.acceptedArtifacts.map(artifact => artifactRow(artifact, true, lang)),
        ...decision.rejectedArtifacts.map(item => artifactRow(item.artifact, false, lang)),
      ].join("")
    : "";
  const artifactsSection = "<section><h2>" + safeText(t("view.deliveries.section.artifacts", lang)) + "</h2>" +
    (artifactRows
      ? tableWrap([
          "view.deliveries.artifacts.id",
          "view.deliveries.artifacts.kind",
          "view.deliveries.artifacts.ref",
          "view.deliveries.artifacts.declared_by",
          "view.deliveries.artifacts.exists",
          "view.deliveries.artifacts.scope",
          "view.deliveries.artifacts.sha",
          "view.deliveries.artifacts.counted",
        ], artifactRows, lang)
      : '<p class="field-hint">' + safeText(t("view.deliveries.artifacts.empty", lang)) + "</p>") +
    "</section>";

  const verificationRows = decision.verifications.map(({ verification, verdict }) =>
    "<tr>" +
    '<td class="path">' + safeText(verification.command || "—") + "</td>" +
    '<td class="num">' + safeText(verification.target_task_version ?? "—") + "</td>" +
    '<td class="id">' + safeText(shortSha(verification.target_commit) || "—") + "</td>" +
    '<td class="id">' + safeText(verification.target_artifact_id
      ? verification.target_artifact_id + "@" + shortSha(verification.target_artifact_sha256)
      : "—") + "</td>" +
    "<td>" + badge(t("view.deliveries.result." + (VERIFICATION_RESULTS.includes(verification.result) ? verification.result : "pending"), lang),
      verdict.supportsDone ? "badge--done" : verdict.code === "E333" || verdict.code === "E331" || verdict.code === "E334"
        ? "badge--claimed" : "badge--self_check") + "</td>" +
    '<td class="path">' + safeText(asArray(verification.evidence).map(String).join(" · ") ||
      t("view.deliveries.verifications.evidence.none", lang)) + "</td>" +
    '<td class="time">' + safeText(verification.ran_at || "—") + "</td>" +
    '<td class="name">' + yesNo(verdict.supportsDone, lang,
      "view.deliveries.verifications.supports.yes", "view.deliveries.verifications.supports.no") +
    (verdict.code
      ? '<span class="field-hint"> ' + safeText(verdict.code) + " " +
        safeText(t("view.deliveries.code." + verdict.code, lang)) + "</span>"
      : "") + "</td>" +
    "</tr>").join("");

  const verificationsSection = "<section><h2>" + safeText(t("view.deliveries.section.verifications", lang)) + "</h2>" +
    (verificationRows
      ? tableWrap([
          "view.deliveries.verifications.command",
          "view.deliveries.verifications.target_version",
          "view.deliveries.verifications.target_commit",
          "view.deliveries.verifications.target_artifact",
          "view.deliveries.verifications.result",
          "view.deliveries.verifications.evidence",
          "view.deliveries.verifications.ran_at",
          "view.deliveries.verifications.supports",
        ], verificationRows, lang)
      : '<p class="field-hint">' + safeText(t("view.deliveries.verifications.empty", lang)) + "</p>") +
    "</section>";

  const review = isObj(delivery.review) ? delivery.review : {};
  const merge = isObj(delivery.merge) ? delivery.merge : {};
  const completion = isObj(delivery.completion) ? delivery.completion : {};

  const reviewSection = "<section><h2>" + safeText(t("view.deliveries.section.review", lang)) + "</h2>" +
    tableWrap(
      ["view.deliveries.bind.field", "view.deliveries.bind.value"],
      kvRows([
        ["view.deliveries.review.required", review.required === false
          ? t("view.deliveries.review.not_required", lang)
          : t("view.deliveries.stage.on", lang), false],
        ["view.deliveries.review.verdict", review.verdict
          ? t("view.deliveries.review." + review.verdict, lang)
          : t("view.deliveries.review.none", lang), false],
        ["view.deliveries.review.reviewer", review.reviewer || "—", true],
        ["view.deliveries.review.reviewed_at", review.reviewed_at || "—", true],
        ["view.deliveries.merge.required", merge.required === false
          ? t("view.deliveries.stage.off", lang)
          : t("view.deliveries.stage.on", lang), false],
        ["view.deliveries.merge.merged", delivery.mode === "non_code"
          ? t("view.deliveries.merge.not_applicable", lang)
          : merge.merged === true
            ? t("view.deliveries.merge.merged", lang)
            : t("view.deliveries.merge.not_merged", lang), false],
        ["view.deliveries.merge.merged_commit", merge.merged_commit || "—", true],
        ["view.deliveries.merge.merged_at", merge.merged_at || "—", true],
        ["view.deliveries.completion.decided_at", completion.decided_at || "—", true],
        ["view.deliveries.completion.reason", completion.reason || "—", false],
      ], lang),
      lang
    ) + "</section>";

  const traceRowsData = traceOf(delivery);
  const traceSection = "<section><h2>" + safeText(t("view.deliveries.section.trace", lang)) + "</h2>" +
    (traceRowsData.length
      ? tableWrap(
          ["view.deliveries.trace.time", "view.deliveries.trace.kind", "view.deliveries.trace.actor", "view.deliveries.trace.detail"],
          traceRowsData.map(row => "<tr>" +
            '<td class="time">' + safeText(row.at || "—") + "</td>" +
            '<td class="name">' + safeText(t(row.kindKey, lang)) + "</td>" +
            '<td class="id">' + safeText(row.actor || "—") + "</td>" +
            '<td class="name">' + safeText(row.detail || "—") + "</td></tr>").join(""),
          lang
        )
      : '<p class="field-hint">' + safeText(t("view.deliveries.trace.empty", lang)) + "</p>") +
    "</section>";

  return '<article data-delivery-id="' + safeText(delivery.delivery_id || "") + '">' +
    header + decisionSection + stageSection + runSection + bindingSection +
    artifactsSection + verificationsSection + reviewSection + traceSection + "</article>";
}

function artifactRow(artifact = {}, counted, lang) {
  const verdict = artifactVerdict(artifact);
  const countedBadge = badge(
    counted ? t("view.deliveries.artifacts.counted.yes", lang) : t("view.deliveries.artifacts.counted.no", lang),
    counted ? "badge--done" : "badge--blocked"
  );
  return "<tr>" +
    '<td class="id">' + safeText(artifact.artifact_id || "—") + "</td>" +
    '<td class="name">' + safeText(artifact.kind
      ? t("view.deliveries.artifacts.kind." + artifact.kind, lang)
      : "—") + "</td>" +
    '<td class="path">' + safeText(artifact.ref || "—") + "</td>" +
    '<td class="name">' + safeText(artifact.declared_by
      ? t("view.deliveries.artifacts.by." + artifact.declared_by, lang)
      : "—") + "</td>" +
    "<td>" + yesNo(artifact.exists === true, lang) + "</td>" +
    "<td>" + yesNo(artifact.in_write_scope === true, lang) + "</td>" +
    '<td class="id">' + safeText(artifact.sha256 ? shortSha(artifact.sha256) : "—") + "</td>" +
    "<td>" + countedBadge +
    (!counted && verdict.code ? '<span class="field-hint"> ' + safeText(verdict.code) + "</span>" : "") +
    "</td></tr>";
}

/** 追溯：交付、验证、评审、合入、完成判定的时间线，供人工核对。 */
export function traceOf(delivery = {}) {
  const rows = [];
  const review = isObj(delivery.review) ? delivery.review : {};
  const merge = isObj(delivery.merge) ? delivery.merge : {};
  const completion = isObj(delivery.completion) ? delivery.completion : {};

  if (delivery.delivered_at) {
    rows.push({
      at: delivery.delivered_at,
      kindKey: "view.deliveries.trace.delivery",
      actor: delivery.declared_by || "",
      detail: String(delivery.candidate_commit || delivery.baseline_commit || ""),
    });
  }
  for (const item of asArray(delivery.verification)) {
    if (!item) continue;
    rows.push({
      at: item.ran_at || "",
      kindKey: "view.deliveries.trace.verification",
      actor: item.target_commit ? shortSha(item.target_commit) : "",
      detail: (item.command || "") + " · " + String(item.result || ""),
    });
  }
  if (review.reviewed_at || review.verdict) {
    rows.push({
      at: review.reviewed_at || "",
      kindKey: "view.deliveries.trace.review",
      actor: review.reviewer || "",
      detail: String(review.verdict || ""),
    });
  }
  if (merge.merged_at || merge.merged === true) {
    rows.push({
      at: merge.merged_at || "",
      kindKey: "view.deliveries.trace.merge",
      actor: merge.merged_commit ? shortSha(merge.merged_commit) : "",
      detail: merge.merged === true ? "merged" : "not_merged",
    });
  }
  if (completion.decided_at || completion.decision) {
    rows.push({
      at: completion.decided_at || "",
      kindKey: "view.deliveries.trace.completion",
      actor: "",
      detail: String(completion.decision || "") + (completion.reason ? " · " + completion.reason : ""),
    });
  }
  if (delivery.supersedes || delivery.superseded_by) {
    rows.push({
      at: "",
      kindKey: "view.deliveries.trace.supersede",
      actor: "",
      detail: [delivery.supersedes ? "supersedes " + delivery.supersedes : "",
        delivery.superseded_by ? "superseded_by " + delivery.superseded_by : ""].filter(Boolean).join(" / "),
    });
  }

  // 时间缺失的排在最后：不猜时间，也不隐藏记录。
  const keyed = rows.map((row, index) => ({ row, index }));
  keyed.sort((a, b) => {
    const at = (item) => (item.row.at ? item.row.at : "\uffff");
    if (at(a) === at(b)) return a.index - b.index;
    return at(a) < at(b) ? -1 : 1;
  });
  return keyed.map(item => item.row);
}

/** 绑定缺失：不给结论，只给缺失字段与处理入口。 */
export function renderUnknownPanel(delivery = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";
  const gaps = bindingGaps(delivery);

  const list = gaps.length
    ? "<ul>" + gaps.map(item => "<li>" + badge(item.code, "badge--blocked") + " " +
        safeText(t(item.key, lang)) + "</li>").join("") + "</ul>"
    : '<p class="field-hint">' + safeText(t("view.deliveries.unknown.hint", lang)) + "</p>";

  return '<section class="field" data-decision="unknown">' +
    "<h2>" + safeText(t("view.deliveries.unknown.title", lang)) + "</h2>" +
    '<p class="field-hint">' + safeText(t("view.deliveries.unknown.hint", lang)) + "</p>" +
    "<h3>" + safeText(t("view.deliveries.unknown.missing", lang)) + "</h3>" + list +
    '<div class="field"><button type="button" class="button" data-action="recheck">' +
    safeText(t("view.deliveries.unknown.action.recheck", lang)) + "</button></div>" +
    '<p class="field-hint">' + safeText(t("view.deliveries.unknown.action.note", lang)) + "</p>" +
    "</section>";
}

/** 交付列表：状态位互不重复；行内不给结论，判定留给详情。 */
export function renderDeliveryList(state = {}, options = {}) {
  const lang = options.lang === "en" ? "en" : "zh";

  if (state.status === "loading") {
    return '<p class="field-hint">' + safeText(t("view.deliveries.loading", lang)) + "</p>";
  }
  if (state.status === "unauthenticated") {
    return emptyState(lang, ["view.deliveries.unauthenticated", "view.deliveries.unauthenticated.hint"]);
  }
  if (state.status === "error") {
    return emptyState(lang, ["view.deliveries.error", "view.deliveries.error.hint"], true);
  }
  if (state.status === "expired") {
    return emptyState(lang, ["view.deliveries.expired", "view.deliveries.expired.hint"]);
  }

  const deliveries = asArray(state.deliveries);
  const header = "<header><h1>" + safeText(t("view.deliveries.title", lang)) + "</h1>" +
    '<p class="field-hint">' + safeText(t("view.deliveries.subtitle", lang)) + "</p></header>";
  if (!deliveries.length) {
    return header + emptyState(lang, ["view.deliveries.empty", "view.deliveries.empty.hint"]);
  }

  const rows = deliveries.map(delivery => {
    const hasDetail = Array.isArray(delivery.artifacts) && Array.isArray(delivery.verification);
    const decision = hasDetail ? evaluateCompletion(delivery, options) : null;
    const artifacts = hasDetail ? decision.acceptedArtifacts.length + "/" +
      (decision.acceptedArtifacts.length + decision.rejectedArtifacts.length) : "—";
    const merged = delivery.mode === "non_code"
      ? t("view.deliveries.merge.not_applicable", lang)
      : isObj(delivery.merge) && delivery.merge.merged === true
        ? t("view.deliveries.merge.merged", lang)
        : t("view.deliveries.merge.not_merged", lang);

    return "<tr>" +
      '<td class="id">' + safeText(delivery.delivery_id || "—") + "</td>" +
      '<td class="id">' + safeText(delivery.task_instance || "—") + "</td>" +
      '<td class="num">' + safeText(delivery.task_version ?? "—") + "</td>" +
      '<td class="name">' + safeText(modeLabel(delivery.mode, lang)) + "</td>" +
      '<td class="num">' + safeText(artifacts) + "</td>" +
      "<td>" + badge(
        hasDetail
          ? t("view.deliveries.decision." + decision.decision, lang)
          : t("view.deliveries.list.detail", lang),
        hasDetail ? decisionClass(decision.state) : "badge--in_review"
      ) + "</td>" +
      '<td class="name">' + safeText(merged) + "</td>" +
      '<td class="name">' + safeText(delivery.superseded_by
        ? t("view.deliveries.trace.supersede", lang)
        : "—") + "</td>" +
      "</tr>";
  }).join("");

  return header +
    '<p class="field-hint">' + safeText(deliveries.length + " " + t("view.deliveries.count", lang)) + "</p>" +
    tableWrap([
      "view.deliveries.col.delivery",
      "view.deliveries.col.instance",
      "view.deliveries.col.version",
      "view.deliveries.col.mode",
      "view.deliveries.col.artifacts",
      "view.deliveries.col.decision",
      "view.deliveries.col.merge",
      "view.deliveries.trace.kind",
    ], rows, lang);
}
