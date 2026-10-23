const I18N = {
  en: {
    "title": "maw global task board",
    "aria.sidebar": "Projects & Agents",
    "label.workspace": "Workspace",
    "aria.project_nav": "Project navigation",
    "aria.agents": "Working Agents",
    "loading": "Loading…",
    "connecting": "Connecting…",
    "theme": "Theme",
    "theme.title": "Theme: follow system by default",
    "theme.system": "System",
    "theme.light": "Light",
    "theme.dark": "Dark",
    "lang": "Language",
    "all_projects": "All projects",
    "heading.board": "Task board",
    "heading.desc": "Track task flow and coordinate every Agent.",
    "copy.join": "Copy project join prompt",
    "aria.stats": "Task stats for current filters",
    "filter.project": "Project",
    "filter.agent": "Agent",
    "filter.status": "Status",
    "all_agents": "All Agents",
    "all_statuses": "All statuses",
    "reset": "Reset",
    "reset.title": "Reset all filters",
    "aria.views": "View switch",
    "view.board": "Board",
    "view.topo": "Dependency topology",
    "view.factory": "Workshop",
    "loading_tasks": "Loading tasks…",
    "search": "Search tasks",
    "search.ph": "Search tasks, ids, owners…",
    "aria.flow": "Task flow",
    "aria.topo": "Task dependency topology",
    "next.reading": "Reading schedule hints…",
    "close_detail": "Close detail",
    "status.backlog": "Backlog",
    "status.ready": "Ready",
    "status.claimed": "Claimed",
    "status.in_progress": "In progress",
    "status.self_check": "Self-check",
    "status.in_review": "In review",
    "status.merging": "Merging",
    "status.conflict": "Conflict",
    "status.blocked": "Blocked",
    "status.done": "Done",
    "status.cancelled": "Cancelled",
    "lane.queued": "Queued",
    "lane.queued.empty": "Waiting for new tasks",
    "lane.running": "Running",
    "lane.running.empty": "No tasks running",
    "lane.integrating": "Review & merge",
    "lane.integrating.empty": "Waiting for review",
    "lane.attention": "Needs attention",
    "lane.attention.empty": "No blocks or conflicts",
    "lane.finished": "Finished",
    "lane.finished.empty": "Completed tasks appear here",
    "ev.task_created": "Task created",
    "ev.task_claimed": "Task claimed",
    "ev.status_changed": "Status changed",
    "ev.progress": "Progress milestone",
    "ev.agent_registered": "Agent registered",
    "ev.agent_unregistered": "Agent unregistered",
    "ev.review_auto": "Auto-approved review",
    "ev.review_result": "Review result",
    "ev.gate_failed": "Gate failed",
    "ev.merged": "Merged",
    "ev.plan_applied": "Blueprint applied",
    "none": "none",
    "files_n": " files",
    "file_n": "file ",
    "unknown_project": "Unknown project",
    "unknown_path": "(unknown path; confirm in board Project filter)",
    "copied": "Copied",
    "copy_failed": "Copy failed",
    "detail_unavailable": "Detail unavailable",
    "task_missing": "Task missing or filtered out",
    "task_not_found": "No task matches current filters",
    "no_title": "(no title)",
    "loading_logs": "Loading logs…",
    "no_logs": "No task logs (needs maw board --serve)",
    "copy_prompt": "Copy join prompt",
    "copy_cmd": "Copy show command",
    "copy_wt": "Copy cd into worktree",
    "sec.basic": "Basics",
    "dt.status": "Status",
    "dt.priority": "Priority",
    "dt.owner": "Owner",
    "unassigned": "Unassigned",
    "dt.deps": "Deps",
    "dt.attempts": "Attempts",
    "dt.rev": " · rev ",
    "dt.heartbeat": "Heartbeat",
    "dt.blocked": "Blocked reason",
    "sec.exec": "Execution",
    "dt.project": "Project",
    "dt.path": "Project path",
    "dt.branch": "Branch",
    "empty": "empty",
    "show_cmd": "Show command",
    "one_click_copy": "One-click copy",
    "join_prompt": "Join prompt",
    "sec.logs": "Task log",
    "copied_prompt": "Prompt copied",
    "load_fail": "Failed to load board data",
    "prio_title": "Schedule priority; lower runs first",
    "no_tasks_filter": "No tasks to show under current filters",
    "no_tasks_topo": "No tasks yet; apply a blueprint to see topology",
    "topo.fit": "Fit to view",
    "topo.relayout": "Relayout",
    "topo.legend": "Deps: prerequisite → dependent",
    "topo.legend_cross": "Dashed: deps outside current view",
    "topo.missing": "Missing dep ",
    "topo.outside": "Outside filter / historical dep",
    "topo.not_in_view": "Not in current view",
    "topo.need_project": "Select a single project to view topology",
    "tasks_edges": " tasks · ",
    "edges": " edges",
    "n_projects": " projects",
    "n_match": " matching tasks / ",
    "n_tasks_deps": " tasks · ",
    "n_deps": " deps",
    "other_status": "Other",
    "no_tasks": "No tasks",
    "n_tasks": " tasks",
    "n_stages": " stages",
    "progress": "Progress",
    "flow": "Flow",
    "flow_hint": "Includes self-check, review, merge",
    "deps_ready": "Deps ready",
    "blocked_conflict": "Blocked / merge conflicts",
    "no_hint": "No schedule hint",
    "no_task": "no task",
    "working": "Working",
    "no_working_agents": "No working Agents",
    "copy_prefix": "Copy ",
    "copy_suffix_prompt": " join prompt",
    "pick_project": "Select a project first",
    "pick_project_short": "Select a project",
    "load_task_fail": "Failed to load task",
    "offline": "Confirm maw board --serve is running, then wait for reconnect.",
    "sync_fail": "Sync failed",
    "live": "Live",
    "poll": "3s sync",
    "live_hint": "Live updates connected; 3s poll as fallback",
    "poll_hint": "Syncing every 3s; live reconnects automatically",
    "workspace": "Workspace",
    "join_prompt_btn": "Copy project join prompt",
    "prompt.no_projects": "No projects on this board yet. In the target git repo run: maw init && maw plan apply .multiagent/blueprint.toml --yes",
    "prompt.header": "You are an execution Agent for maw (Multi-Agent Workspace CLI). Connect using the project/task info below and follow the protocol; do not skip steps.",
    "prompt.project_info": "## Project",
    "prompt.name": "- Name: ",
    "prompt.project_id": "- Project ID: ",
    "prompt.path": "- Local path: ",
    "prompt.trunk": "- Trunk branch: ",
    "prompt.target": "## Target task",
    "prompt.task_id": "- Task ID: ",
    "prompt.title": "- Title: ",
    "prompt.status": "- Status: ",
    "prompt.priority": "- Priority: ",
    "prompt.deps": "- Deps: ",
    "prompt.owner": "- Current owner: ",
    "prompt.worktree": "- Worktree (repo-relative): ",
    "prompt.branch": "- Task branch: ",
    "prompt.blocked": "- Blocked reason: ",
    "prompt.write_scope": "- write_scope (only these): ",
    "prompt.write_empty": "- (empty; run maw task show ",
    "prompt.write_empty2": " to confirm)",
    "prompt.read_scope": "- read_scope: ",
    "prompt.acceptance": "- acceptance: ",
    "prompt.accept_none": "- (none; use the task card in the worktree)",
    "prompt.steps": "## Join & execute",
    "prompt.step1": "1. cd into the project:",
    "prompt.step2_owner": "2. If continuing the same owner, reuse that Agent name; otherwise pick a unique unused name from the board.",
    "prompt.current_owner": "   Current owner: ",
    "prompt.export": "   export MAW_AGENT=<unique-name>",
    "prompt.step3_wt": "3. If the task already has a worktree: enter it and keep coding; re-bind with maw next if needed (same agent is idempotent).",
    "prompt.keep_task": "4. Keep the target task as ",
    "prompt.keep_task2": "; do not touch other agents' worktrees.",
    "prompt.step2_new": "2. Set a unique Agent name for this session (must not collide",
    "prompt.occupied": "; in use: ",
    "prompt.step3_next": "3. Register and claim the next ready task (by priority / merge_order):",
    "prompt.or_next": "   or: maw next --agent <unique-name>",
    "prompt.step4_check": "4. Check the claimed task id. Expected: ",
    "prompt.step4a": "   - If it matches: work in the printed worktree.",
    "prompt.step4b": "   - If another task: continue with what you got (queue order), or re-check the board; do not edit others' worktrees.",
    "prompt.step5": "5. Only modify inside the maw worktree; only write_scope; run maw request-change before going out of scope.",
    "prompt.step6": "6. Milestone: maw progress -m \"msg\"",
    "prompt.step6b": "; done: maw submit; blocked: maw block (add reason per CLI).",
    "prompt.step7": "7. After submit you may merge: maw merge --yes; do not git merge/rebase/push trunk directly.",
    "prompt.step8": "8. After merge, if ready tasks remain, maw next again until none left or the user stops you.",
    "prompt.step9": "9. Global board: maw board --serve (default http://127.0.0.1:7420/).",
    "prompt.redlines": "## Protocol red lines",
    "prompt.red1": "- State DB is ~/.maw/maw.db; this repo's .multiagent/ is config/blueprint only.",
    "prompt.red2": "- Before handoff/interrupt: maw handoff; maw unregister when this session has no more tasks.",
    "prompt.red3": "- On errors, read maw's Next hint first; do not bypass.",
    "prompt.show_before": "- Before starting: maw task show ",
    "prompt.ready_header": "You are an execution Agent for maw. Connect to the project below, claim and finish the next ready task.",
    "prompt.ready_list": "## Ready tasks (reference; maw next is authoritative)",
    "prompt.ready_more": "- …total ",
    "prompt.ready_more2": " tasks",
    "prompt.no_ready": "- (no ready under current filters; check the board or wait for deps)",
    "prompt.path_ph": "<project-path>",
    "prompt.export2": "2. export MAW_AGENT=<unique-name>",
    "prompt.occupied2": "  # in use: ",
    "prompt.step4_generic": "4. Enter the printed worktree; only that task's write_scope; maw progress / maw submit; then maw merge --yes if appropriate.",
    "prompt.step5_loop": "5. After merge, if ready remains, maw next again until none left.",
    "prompt.step6_board": "6. Board: maw board --serve",
    "prompt.red_short1": "- DB ~/.maw/maw.db; work only in maw worktrees; merge with maw merge --yes.",
    "prompt.red_short2": "- On errors, read maw Next hints first."
  },
  zh: {
    "title": "maw 全局任务看板",
    "aria.sidebar": "工程与 Agent",
    "label.workspace": "工作空间",
    "aria.project_nav": "工程导航",
    "aria.agents": "执行中 Agent",
    "loading": "正在加载…",
    "connecting": "正在连接…",
    "theme": "主题",
    "theme.title": "主题：默认跟随系统外观",
    "theme.system": "跟随系统",
    "theme.light": "浅色",
    "theme.dark": "深色",
    "lang": "语言",
    "all_projects": "全部工程",
    "heading.board": "任务看板",
    "heading.desc": "跟踪任务流转，协调每一个 Agent。",
    "copy.join": "复制工程接入提示词",
    "aria.stats": "当前筛选范围任务统计",
    "filter.project": "工程",
    "filter.agent": "Agent",
    "filter.status": "状态",
    "all_agents": "全部 Agent",
    "all_statuses": "全部状态",
    "reset": "重置",
    "reset.title": "重置所有筛选",
    "aria.views": "视图切换",
    "view.board": "看板",
    "view.topo": "依赖拓扑",
    "view.factory": "工坊",
    "loading_tasks": "正在加载任务…",
    "search": "搜索任务",
    "search.ph": "搜索任务、编号、负责人…",
    "aria.flow": "任务流程",
    "aria.topo": "任务依赖拓扑",
    "next.reading": "正在读取调度建议…",
    "close_detail": "关闭详情",
    "status.backlog": "待排期",
    "status.ready": "可领取",
    "status.claimed": "已领取",
    "status.in_progress": "进行中",
    "status.self_check": "自检中",
    "status.in_review": "评审中",
    "status.merging": "合并中",
    "status.conflict": "冲突",
    "status.blocked": "阻塞",
    "status.done": "已完成",
    "status.cancelled": "已取消",
    "lane.queued": "待处理",
    "lane.queued.empty": "等待新的任务",
    "lane.running": "执行中",
    "lane.running.empty": "暂无执行中的任务",
    "lane.integrating": "评审与合并",
    "lane.integrating.empty": "等待提交评审",
    "lane.attention": "需处理",
    "lane.attention.empty": "暂无阻塞或冲突",
    "lane.finished": "已结束",
    "lane.finished.empty": "完成的任务会出现在这里",
    "ev.task_created": "创建任务",
    "ev.task_claimed": "领取任务",
    "ev.status_changed": "状态变更",
    "ev.progress": "进度里程碑",
    "ev.agent_registered": "Agent 注册",
    "ev.agent_unregistered": "Agent 注销",
    "ev.review_auto": "自动通过评审",
    "ev.review_result": "评审结果",
    "ev.gate_failed": "门禁失败",
    "ev.merged": "已合并",
    "ev.plan_applied": "应用蓝图",
    "none": "无",
    "files_n": " 个",
    "file_n": "文件 ",
    "unknown_project": "未知工程",
    "unknown_path": "（未知路径，请在看板「工程」中确认）",
    "copied": "已复制",
    "copy_failed": "复制失败",
    "detail_unavailable": "详情不可用",
    "task_missing": "任务不存在或已筛选掉",
    "task_not_found": "当前筛选下找不到该任务",
    "no_title": "（无标题）",
    "loading_logs": "正在加载日志…",
    "no_logs": "暂无任务日志（需 maw board --serve）",
    "copy_prompt": "复制接入提示词",
    "copy_cmd": "复制查看命令",
    "copy_wt": "复制进入 worktree",
    "sec.basic": "基本信息",
    "dt.status": "状态",
    "dt.priority": "优先级",
    "dt.owner": "负责人",
    "unassigned": "未分配",
    "dt.deps": "依赖",
    "dt.attempts": "尝试次数",
    "dt.rev": " · 修订 ",
    "dt.heartbeat": "心跳",
    "dt.blocked": "阻塞原因",
    "sec.exec": "执行节点",
    "dt.project": "工程",
    "dt.path": "工程路径",
    "dt.branch": "分支",
    "empty": "空",
    "show_cmd": "查看命令",
    "one_click_copy": "一键复制",
    "join_prompt": "接入提示词",
    "sec.logs": "任务日志",
    "copied_prompt": "已复制提示词",
    "load_fail": "无法加载看板数据",
    "prio_title": "调度优先级，数值越小越优先",
    "no_tasks_filter": "当前筛选下没有可展示的任务",
    "no_tasks_topo": "暂无任务，应用蓝图后可查看依赖拓扑",
    "topo.fit": "适应画布",
    "topo.relayout": "重布局",
    "topo.legend": "依赖方向：前置 → 后续",
    "topo.legend_cross": "虚线：当前视图外的依赖",
    "topo.missing": "缺失依赖 ",
    "topo.outside": "筛选外 / 历史依赖",
    "topo.not_in_view": "不在当前视图",
    "topo.need_project": "请先选择单个工程再查看拓扑",
    "tasks_edges": " 任务 · ",
    "edges": " 边",
    "n_projects": " 个工程",
    "n_match": " 个匹配任务 / ",
    "n_tasks_deps": " 个任务 · ",
    "n_deps": " 条依赖",
    "other_status": "其他状态",
    "no_tasks": "暂无任务",
    "n_tasks": " 个任务",
    "n_stages": " 个阶段",
    "progress": "完成进度",
    "flow": "执行与流转",
    "flow_hint": "含自检、评审与合并",
    "deps_ready": "依赖已就绪",
    "blocked_conflict": "阻塞 / 合并冲突",
    "no_hint": "暂无调度建议",
    "no_task": "无任务",
    "working": "执行中",
    "no_working_agents": "暂无执行中 Agent",
    "copy_prefix": "复制 ",
    "copy_suffix_prompt": " 接入提示词",
    "pick_project": "请先选择一个工程",
    "pick_project_short": "请先选择工程",
    "load_task_fail": "未能加载任务",
    "offline": "请确认 maw board --serve 正在运行，然后等待自动重连。",
    "sync_fail": "同步失败",
    "live": "实时",
    "poll": "3s 同步",
    "live_hint": "实时更新已连接，每 3 秒同步兜底",
    "poll_hint": "每 3 秒同步，实时连接会自动重试",
    "workspace": "工作空间",
    "join_prompt_btn": "复制工程接入提示词",
    "prompt.no_projects": "当前看板尚无已接入工程。请先在目标 git 仓库内运行：maw init && maw plan apply .multiagent/blueprint.toml --yes",
    "prompt.header": "你是接入 maw（Multi-Agent Worktree CLI）的执行 Agent。请严格按下列工程与任务信息连接 maw 并完成工作，不要跳过协议步骤。",
    "prompt.project_info": "## 工程信息",
    "prompt.name": "- 名称：",
    "prompt.project_id": "- 工程 ID：",
    "prompt.path": "- 本地路径：",
    "prompt.trunk": "- 主干分支：",
    "prompt.target": "## 目标任务",
    "prompt.task_id": "- 任务 ID：",
    "prompt.title": "- 标题：",
    "prompt.status": "- 状态：",
    "prompt.priority": "- 优先级：",
    "prompt.deps": "- 依赖：",
    "prompt.owner": "- 当前负责人：",
    "prompt.worktree": "- Worktree（相对仓库）：",
    "prompt.branch": "- 任务分支：",
    "prompt.blocked": "- 阻塞原因：",
    "prompt.write_scope": "- 可写范围 write_scope（只改这些）：",
    "prompt.write_empty": "- （空；先运行 maw task show ",
    "prompt.write_empty2": " 确认）",
    "prompt.read_scope": "- 只读范围 read_scope：",
    "prompt.acceptance": "- 验收标准 acceptance：",
    "prompt.accept_none": "- （无；以 worktree 内任务卡为准）",
    "prompt.steps": "## 接入与执行步骤",
    "prompt.step1": "1. 终端进入工程目录：",
    "prompt.step2_owner": "2. 若你接续同一负责人，复用其 Agent 名；否则另选看板未占用的唯一名。",
    "prompt.current_owner": "   当前负责人：",
    "prompt.export": "   export MAW_AGENT=<唯一名>",
    "prompt.step3_wt": "3. 若任务已有 worktree：直接进入该目录继续改代码；需要重新绑定可再执行 maw next（同名 agent 幂等）。",
    "prompt.keep_task": "4. 目标任务保持为 ",
    "prompt.keep_task2": "；勿改其他 agent 的 worktree。",
    "prompt.step2_new": "2. 设定本会话唯一 Agent 名（勿与已注册冲突",
    "prompt.occupied": "；已占用：",
    "prompt.step3_next": "3. 注册并领取下一道 ready 任务（按 priority / merge_order）：",
    "prompt.or_next": "   或：maw next --agent <唯一名>",
    "prompt.step4_check": "4. 核对输出的任务 ID。期望领取：",
    "prompt.step4a": "   - 若正是该任务：进入输出的 worktree 目录工作。",
    "prompt.step4b": "   - 若领到其他任务：以实际领到的任务为准继续（队列按优先级）；或查看看板后再调度，勿改他人 worktree。",
    "prompt.step5": "5. 只在 maw 创建的 worktree 中修改；只改 write_scope；越界前运行 maw request-change。",
    "prompt.step6": "6. 里程碑：maw progress -m \"说明\"",
    "prompt.step6b": "；完成：maw submit；阻塞：maw block（按 CLI 提示附原因）。",
    "prompt.step7": "7. 完成 submit 后可自行合入：maw merge --yes；禁止直接 git merge / rebase / push 主干。",
    "prompt.step8": "8. 合入后若仍有 ready 任务，继续 maw next 领取并执行下一题，循环直至无题可领或用户叫停。",
    "prompt.step9": "9. 全局看板：maw board --serve（默认 http://127.0.0.1:7420/）。",
    "prompt.redlines": "## 协议红线",
    "prompt.red1": "- 状态库在本机 ~/.maw/maw.db；本仓库 .multiagent/ 仅存配置与蓝图。",
    "prompt.red2": "- 换人或中断前：maw handoff；本会话无更多可领任务时再 maw unregister。",
    "prompt.red3": "- 出错时先读 maw 输出的「下一步」提示，不要绕过。",
    "prompt.show_before": "- 开始前建议：maw task show ",
    "prompt.ready_header": "你是接入 maw（Multi-Agent Worktree CLI）的执行 Agent。请连接下列工程，领取并完成下一道可领取（ready）任务。",
    "prompt.ready_list": "## 当前可领取任务（供参考，以 maw next 实际领取为准）",
    "prompt.ready_more": "- …共 ",
    "prompt.ready_more2": " 道",
    "prompt.no_ready": "- （当前筛选下无 ready；先看看板或等待依赖完成）",
    "prompt.path_ph": "<工程路径>",
    "prompt.export2": "2. export MAW_AGENT=<唯一名>",
    "prompt.occupied2": "  # 已占用：",
    "prompt.step4_generic": "4. 进入输出的 worktree，只改该任务 write_scope；maw progress / maw submit；完成后可自行 maw merge --yes。",
    "prompt.step5_loop": "5. 合入后若仍有 ready，继续 maw next 领下一题并执行，循环直至无题可领。",
    "prompt.step6_board": "6. 看板：maw board --serve",
    "prompt.red_short1": "- 状态库 ~/.maw/maw.db；只在 maw worktree 内工作；合入用 maw merge --yes（可自行执行）。",
    "prompt.red_short2": "- 出错先读 maw「下一步」提示。",
  }
};

let uiLang = "en";
function t(key) {
  const pack = I18N[uiLang] || I18N.en;
  return (pack[key] != null ? pack[key] : (I18N.en[key] != null ? I18N.en[key] : key));
}
function readLang() {
  try {
    const q = new URLSearchParams(location.search).get("lang");
    if (q === "en" || q === "zh") return q;
  } catch (_) {}
  try {
    const v = localStorage.getItem("maw-board-lang");
    if (v === "en" || v === "zh") return v;
  } catch (_) {}
  try {
    if ((navigator.language || "").toLowerCase().startsWith("zh")) return "zh";
  } catch (_) {}
  return "en";
}
function setLang(lang) {
  uiLang = (lang === "zh") ? "zh" : "en";
  try { localStorage.setItem("maw-board-lang", uiLang); } catch (_) {}
  try {
    if (document.documentElement) document.documentElement.lang = uiLang === "zh" ? "zh-CN" : "en";
  } catch (_) {}
  rebuildLocalizedLists();
  try { applyStaticI18n(); } catch (_) {}
  try { updateTopoButtonState(); } catch (_) {}
  const sel = document.getElementById("lang-select");
  if (sel) sel.value = uiLang;
  if (typeof lastSnap !== "undefined" && lastSnap) {
    try { render(lastSnap); } catch (_) {}
  }
}
function rebuildLocalizedLists() {
  COLUMNS.length = 0;
  [
    ["backlog","status.backlog"],["ready","status.ready"],["claimed","status.claimed"],
    ["in_progress","status.in_progress"],["self_check","status.self_check"],["in_review","status.in_review"],
    ["merging","status.merging"],["conflict","status.conflict"],["blocked","status.blocked"],
    ["done","status.done"],["cancelled","status.cancelled"]
  ].forEach(([k, ik]) => COLUMNS.push([k, t(ik)]));
  LANES.length = 0;
  [
    { id: "queued", label: "lane.queued", statuses: ["backlog", "ready"], symbol: "≡", empty: "lane.queued.empty" },
    { id: "running", label: "lane.running", statuses: ["claimed", "in_progress", "self_check"], symbol: "⌁", empty: "lane.running.empty" },
    { id: "integrating", label: "lane.integrating", statuses: ["in_review", "merging"], symbol: "⑂", empty: "lane.integrating.empty" },
    { id: "attention", label: "lane.attention", statuses: ["conflict", "blocked"], symbol: "!", empty: "lane.attention.empty" },
    { id: "finished", label: "lane.finished", statuses: ["done", "cancelled"], symbol: "✓", empty: "lane.finished.empty" }
  ].forEach(L => LANES.push({ id: L.id, label: t(L.label), statuses: L.statuses, symbol: L.symbol, empty: t(L.empty) }));
}
function applyStaticI18n() {
  document.title = t("title");
  const map = [
    ["workspace-label", "label.workspace"],
    ["subtitle", "connecting"],
    ["page-h1", "heading.board"],
    ["page-desc", "heading.desc"],
    ["f-copy-join", "copy.join"],
    ["view-board", "view.board"],
    ["view-topo", "view.topo"],
    ["view-factory", "view.factory"],
    ["board-count", "loading_tasks"],
    ["f-reset", "reset"],
    ["next-hint-text", "next.reading"],
  ];
  map.forEach(([id, key]) => { const el = document.getElementById(id); if (el) el.textContent = t(key); });
  const setAria = (id, key) => { const el = document.getElementById(id); if (el) el.setAttribute("aria-label", t(key)); };
  setAria("app-shell-aside", "aria.sidebar");
  setAria("project-nav", "aria.project_nav");
  setAria("agents-rail", "aria.agents");
  setAria("theme-select", "theme");
  setAria("lang-select", "lang");
  setAria("stats", "aria.stats");
  setAria("f-project", "filter.project");
  setAria("f-agent", "filter.agent");
  setAria("f-status", "filter.status");
  setAria("view-toggle", "aria.views");
  setAria("task-search", "search");
  setAria("columns", "aria.flow");
  setAria("topology", "aria.topo");
  setAria("factory", "view.factory");
  setAria("d-close", "close_detail");
  const th = document.getElementById("theme-select");
  if (th) {
    th.title = t("theme.title");
    const opts = th.querySelectorAll("option");
    if (opts[0]) opts[0].textContent = t("theme.system");
    if (opts[1]) opts[1].textContent = t("theme.light");
    if (opts[2]) opts[2].textContent = t("theme.dark");
  }
  const ts = document.getElementById("task-search");
  if (ts) ts.placeholder = t("search.ph");
  const bc = document.querySelector(".breadcrumb .dim");
  if (bc) bc.textContent = t("workspace");
  const wn = document.getElementById("workspace-name");
  if (wn && (!wn.dataset.locked)) wn.textContent = t("all_projects");
  const agentsEmpty = document.querySelector("#agents .empty");
  if (agentsEmpty) agentsEmpty.textContent = t("loading");
}

const COLUMNS = [];
const LANES = [];
const VIEWS = ["board", "topo", "factory"];
const TOPO_NODE_W = 200;
const TOPO_NODE_H = 98;
const TOPO_GAP_X = 72;
const TOPO_GAP_Y = 22;
const TOPO_PAD = 20;

function readView() {
  try {
    const value = localStorage.getItem("maw-board-view");
    return VIEWS.includes(value) ? value : "board";
  } catch (_) { return "board"; }
}

/** 拓扑仅在选中单个工程时可用（「全部工程」会把多仓叠成超大图）。 */
function canUseTopo() {
  return !!filters.project;
}

function updateTopoButtonState() {
  const btn = document.getElementById("view-topo");
  if (!btn) return;
  const ok = canUseTopo();
  btn.disabled = !ok;
  btn.title = ok ? "" : t("topo.need_project");
  btn.setAttribute("aria-disabled", ok ? "false" : "true");
}

function setView(value) {
  let next = VIEWS.includes(value) ? value : "board";
  if (next === "topo" && !canUseTopo()) next = "board";
  boardView = next;
  try { localStorage.setItem("maw-board-view", boardView); } catch (_) {}
  document.getElementById("view-board").classList.toggle("active", boardView === "board");
  document.getElementById("view-topo").classList.toggle("active", boardView === "topo");
  document.getElementById("view-board").setAttribute("aria-pressed", boardView === "board" ? "true" : "false");
  document.getElementById("view-topo").setAttribute("aria-pressed", boardView === "topo" ? "true" : "false");
  document.getElementById("columns").hidden = boardView !== "board";
  document.getElementById("topology").hidden = boardView !== "topo";
  const factoryRoot = document.getElementById("factory");
  if (factoryRoot) {
    factoryRoot.hidden = boardView !== "factory";
    document.getElementById("view-factory")?.classList?.toggle("active", boardView === "factory");
    document.getElementById("view-factory")?.setAttribute("aria-pressed", String(boardView === "factory"));
    if (typeof window !== "undefined") window.MawBoard?.setFactoryVisible?.(factoryRoot, boardView === "factory");
  }
  updateTopoButtonState();
  if (lastSnap) renderWorkspace(lastSnap);
}

let searchTerm = "";
let boardView = "board";
let renderedBoardHTML = "";
let renderedTopoHTML = "";
let snapshotRequest = 0;
let eventStream = null;
let sseConnected = false;
let snapshotHealthy = true;
let syncRunning = false;
let syncPending = false;
let detailRequest = 0;
let drawerReturnFocus = null;
let closeTimer;
const filters = { project: "", agent: "", status: "" };
let agentOptions = [];
let projectOptions = [];
let lastSnap = null;
let selectedTid = null;
let selectedPid = null;
let drawerEvents = [];
let drawerLoading = false;
let writingProjectHash = false;

/** 从 URL #锚点读取工程 ID（刷新后恢复选中）。 */
function readProjectHash() {
  try {
    if (typeof location === "undefined" || !location) return "";
    return decodeURIComponent((location.hash || "").replace(/^#/, ""));
  } catch (_) {
    return "";
  }
}

/** 将当前工程写入 #锚点；全部工程时清除锚点。用 replaceState 避免污染历史。 */
function writeProjectHash(projectId) {
  const id = projectId || "";
  if (readProjectHash() === id) return;
  if (typeof location === "undefined" || !location) return;
  const url = id
    ? location.pathname + location.search + "#" + encodeURIComponent(id)
    : location.pathname + location.search;
  writingProjectHash = true;
  try {
    if (typeof history !== "undefined" && history.replaceState) {
      history.replaceState(null, "", url);
    } else {
      location.hash = id;
    }
  } catch (_) {
    try { location.hash = id; } catch (__) {}
  }
  writingProjectHash = false;
}

function setProjectFilter(projectId, { syncHash = true } = {}) {
  filters.project = projectId || "";
  filters._inited = true;
  if (syncHash) writeProjectHash(filters.project);
  updateTopoButtonState();
  // 「全部工程」时拓扑不可用，自动退回看板。
  if (!filters.project && boardView === "topo") setView("board");
}

const EVENT_LABEL = {
  task_created: t("ev.task_created"),
  claimed: t("ev.task_claimed"),
  status_changed: t("ev.status_changed"),
  progress: t("ev.progress"),
  agent_registered: t("ev.agent_registered"),
  agent_unregistered: t("ev.agent_unregistered"),
  review_auto_approved: t("ev.review_auto"),
  review_result: t("ev.review_result"),
  gate_failed: t("ev.gate_failed"),
  merged: t("ev.merged"),
  plan_applied: t("ev.plan_applied")
};

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c]));
}

/** 统一为 YYYY-MM-DD HH:mm:ss（本地时区）；无法解析则原样返回。 */
function fmtTime(v) {
  if (v == null || v === "") return "";
  const s = String(v).trim();
  if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(s)) return s;
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return s;
  const p = n => String(n).padStart(2, "0");
  return d.getFullYear() + "-" + p(d.getMonth() + 1) + "-" + p(d.getDate()) +
    " " + p(d.getHours()) + ":" + p(d.getMinutes()) + ":" + p(d.getSeconds());
}

function statusLabel(st) {
  for (const [k, lab] of COLUMNS) if (k === st) return lab;
  return st || "—";
}

function findTask(tid, pid) {
  const list = lastSnap?.tasks || [];
  if (pid) {
    const hit = list.find(x => x.id === tid && x.project_id === pid);
    return hit || null;
  }
  return list.find(x => x.id === tid) || null;
}

function findProject(projectId) {
  const list = lastSnap?.projects || [];
  if (projectId) {
    const hit = list.find(p => p.id === projectId);
    if (hit) return hit;
  }
  if (filters.project) {
    const hit = list.find(p => p.id === filters.project);
    if (hit) return hit;
  }
  return list[0] || null;
}

function listBullets(arr, empty) {
  if (!arr || !arr.length) return '<p class="empty">' + esc(empty || t("none")) + '</p>';
  return '<ul class="list-plain">' + arr.map(x => '<li>' + esc(x) + '</li>').join("") + '</ul>';
}

function statusBadge(st) {
  let cls = "badge";
  if (st === "blocked" || st === "conflict") cls += " danger";
  else if (st === "done") cls += " ok";
  else if (st === "in_progress" || st === "claimed") cls += " warn";
  return '<span class="' + cls + '">' + esc(statusLabel(st)) + '</span>';
}

function formatEventData(ev) {
  const d = ev.data || {};
  if (ev.type === "progress" && d.message) return String(d.message);
  if (ev.type === "status_changed") {
    const parts = [];
    if (d.to) parts.push("→ " + statusLabel(d.to) + " (" + d.to + ")");
    if (d.reason) parts.push(String(d.reason));
    if (d.files && d.files.length) parts.push("文件 " + d.files.length + " 个");
    return parts.join(" · ") || "";
  }
  if (ev.type === "review_result") {
    return (d.result || "") + (d.message ? "：" + d.message : "");
  }
  if (ev.type === "gate_failed" && d.error) return String(d.error);
  const keys = Object.keys(d);
  if (!keys.length) return "";
  try { return JSON.stringify(d); } catch (_) { return ""; }
}

function fmtBullet(arr, empty) {
  if (!arr || !arr.length) return empty || "- （无）";
  return arr.map(x => "- " + x).join("\n");
}

function takenAgentNames() {
  const seen = new Set();
  (lastSnap?.agents || []).forEach(a => { if (a.id) seen.add(a.id); });
  (lastSnap?.tasks || []).forEach(t => { if (t.owner) seen.add(t.owner); });
  return [...seen].sort();
}

function buildTaskPrompt(task) {
  const p = findProject(task.project_id) || {};
  const projName = task.project_name || p.name || task.project_id || t("unknown_project");
  const projPath = p.path || t("unknown_path");
  const mainBranch = p.main_branch || "main";
  const taken = takenAgentNames();
  const lines = [];
  lines.push("你是接入 maw（Multi-Agent Worktree CLI）的执行 Agent。请严格按下列工程与任务信息连接 maw 并完成工作，不要跳过协议步骤。");
  lines.push("");
  lines.push("## 工程信息");
  lines.push("- 名称：" + projName);
  lines.push("- 工程 ID：" + (task.project_id || p.id || "—"));
  lines.push("- 本地路径：" + projPath);
  lines.push("- 主干分支：" + mainBranch);
  lines.push("");
  lines.push("## 目标任务");
  lines.push("- 任务 ID：" + task.id);
  lines.push("- 标题：" + (task.title || "—"));
  lines.push("- 状态：" + statusLabel(task.status) + "（" + (task.status || "—") + "）");
  lines.push("- 优先级：" + (task.priority ?? "—") + " · merge_order：" + (task.merge_order ?? "—"));
  lines.push("- 依赖：" + ((task.deps && task.deps.length) ? task.deps.join(", ") : t("none")));
  if (task.owner) lines.push("- 当前负责人：" + task.owner);
  if (task.worktree) lines.push("- Worktree（相对仓库）：" + task.worktree);
  if (task.branch) lines.push("- 任务分支：" + task.branch);
  if (task.blocked_reason) lines.push("- 阻塞原因：" + task.blocked_reason);
  lines.push("- 可写范围 write_scope（只改这些）：");
  lines.push(fmtBullet(task.write_scope, "- （空；先运行 maw task show " + task.id + " 确认）"));
  lines.push("- 只读范围 read_scope：");
  lines.push(fmtBullet(task.read_scope, "- （无）"));
  lines.push("- 验收标准 acceptance：");
  lines.push(fmtBullet(task.acceptance, "- （无；以 worktree 内任务卡为准）"));
  lines.push("");
  lines.push("## 接入与执行步骤");
  lines.push("1. 终端进入工程目录：");
  lines.push("   cd " + projPath);
  if (task.status === "in_progress" || task.status === "claimed" || task.status === "self_check" || task.status === "conflict") {
    lines.push("2. 若你接续同一负责人，复用其 Agent 名；否则另选看板未占用的唯一名。");
    if (task.owner) lines.push("   当前负责人：" + task.owner + (task.worktree ? "；worktree：" + task.worktree : ""));
    lines.push("   export MAW_AGENT=<唯一名>");
    lines.push("3. 若任务已有 worktree：直接进入该目录继续改代码；需要重新绑定可再执行 maw next（同名 agent 幂等）。");
    lines.push("   " + (task.worktree ? ("cd " + projPath.replace(/\\/g,"/") + "/" + task.worktree) : "maw next"));
    lines.push("4. 目标任务保持为 " + task.id + "；勿改其他 agent 的 worktree。");
  } else {
    lines.push("2. 设定本会话唯一 Agent 名（勿与已注册冲突" + (taken.length ? "；已占用：" + taken.join(", ") : "") + "）：");
    lines.push("   export MAW_AGENT=<唯一名>");
    lines.push("3. 注册并领取下一道 ready 任务（按 priority / merge_order）：");
    lines.push("   maw register && maw next");
    lines.push("   或：maw next --agent <唯一名>");
    lines.push("4. 核对输出的任务 ID。期望领取：" + task.id + "（" + (task.title || "") + "）。");
    lines.push("   - 若正是该任务：进入输出的 worktree 目录工作。");
    lines.push("   - 若领到其他任务：以实际领到的任务为准继续（队列按优先级）；或查看看板后再调度，勿改他人 worktree。");
  }
  lines.push("5. 只在 maw 创建的 worktree 中修改；只改 write_scope；越界前运行 maw request-change。");
  lines.push("6. 里程碑：maw progress -m \"说明\"；完成：maw submit；阻塞：maw block（按 CLI 提示附原因）。");
  lines.push("7. 完成 submit 后可自行合入：maw merge --yes；禁止直接 git merge / rebase / push 主干。");
  lines.push("8. 合入后若仍有 ready 任务，继续 maw next 领取并执行下一题，循环直至无题可领或用户叫停。");
  lines.push("9. 全局看板：maw board --serve（默认 http://127.0.0.1:7420/）。");
  lines.push("");
  lines.push("## 协议红线");
  lines.push("- 状态库在本机 ~/.maw/maw.db；本仓库 .multiagent/ 仅存配置与蓝图。");
  lines.push("- 换人或中断前：maw handoff；本会话无更多可领任务时再 maw unregister。");
  lines.push("- 出错时先读 maw 输出的「下一步」提示，不要绕过。");
  lines.push("- 开始前建议：maw task show " + task.id);
  return lines.join("\n");
}

function buildProjectJoinPrompt(projectId) {
  const p = findProject(projectId);
  if (!p) {
    return "当前看板尚无已接入工程。请先在目标 git 仓库内运行：maw init && maw plan apply .multiagent/blueprint.toml --yes";
  }
  const taken = takenAgentNames();
  const ready = (lastSnap?.tasks || []).filter(t => t.status === "ready" && (!projectId || t.project_id === p.id));
  const lines = [];
  lines.push("你是接入 maw（Multi-Agent Worktree CLI）的执行 Agent。请连接下列工程，领取并完成下一道可领取（ready）任务。");
  lines.push("");
  lines.push("## 工程信息");
  lines.push("- 名称：" + (p.name || "—"));
  lines.push("- 工程 ID：" + (p.id || "—"));
  lines.push("- 本地路径：" + (p.path || "—"));
  lines.push("- 主干分支：" + (p.main_branch || "main"));
  lines.push("");
  lines.push("## 当前可领取任务（供参考，以 maw next 实际领取为准）");
  if (ready.length) {
    ready.slice(0, 8).forEach(t => {
      lines.push("- " + t.id + "：" + (t.title || "") + "（priority=" + (t.priority ?? "?") + "）");
    });
    if (ready.length > 8) lines.push("- …共 " + ready.length + " 道");
  } else {
    lines.push("- （当前筛选下无 ready；先看看板或等待依赖完成）");
  }
  lines.push("");
  lines.push("## 接入与执行步骤");
  lines.push("1. cd " + (p.path || "<工程路径>"));
  lines.push("2. export MAW_AGENT=<唯一名>" + (taken.length ? "  # 已占用：" + taken.join(", ") : ""));
  lines.push("3. maw register && maw next");
  lines.push("4. 进入输出的 worktree，只改该任务 write_scope；maw progress / maw submit；完成后可自行 maw merge --yes。");
  lines.push("5. 合入后若仍有 ready，继续 maw next 领下一题并执行，循环直至无题可领。");
  lines.push("6. 看板：maw board --serve");
  lines.push("");
  lines.push("## 协议红线");
  lines.push("- 状态库 ~/.maw/maw.db；只在 maw worktree 内工作；合入用 maw merge --yes（可自行执行）。");
  lines.push("- 出错先读 maw「下一步」提示。");
  return lines.join("\n");
}

async function copyText(btn, text, okLabel) {
  const prev = btn.innerHTML;
  try {
    await navigator.clipboard.writeText(text);
    btn.textContent = okLabel || t("copied");
    btn.classList.add("ok");
    setTimeout(() => { btn.innerHTML = prev; btn.classList.remove("ok"); }, 2500);
  } catch (_) {
    btn.textContent = t("copy_failed");
    setTimeout(() => { btn.innerHTML = prev; }, 1400);
  }
}

function syncSelectionHighlight() {
  document.querySelectorAll(".card.clickable, .topo-node.clickable").forEach(el => {
    el.classList.toggle("active", !!selectedTid && el.dataset.tid === selectedTid && el.dataset.pid === (selectedPid || ""));
  });
}

function closeDrawer() {
  selectedTid = null;
  selectedPid = null;
  detailRequest++;
  drawerEvents = [];
  const mask = document.getElementById("drawer-mask");
  const drawer = document.getElementById("drawer");
  mask.classList.remove("open");
  drawer.classList.remove("open");
  drawer.setAttribute("aria-hidden", "true");
  drawer.inert = true;
  document.getElementById("app-shell").inert = false;
  clearTimeout(closeTimer);
  closeTimer = setTimeout(() => { mask.hidden = true; }, 220);
  syncSelectionHighlight();
  const replacement = [...document.querySelectorAll(".card.clickable, .topo-node.clickable")].find(el =>
    el.dataset.tid === drawerReturnFocus?.tid && el.dataset.pid === drawerReturnFocus?.pid);
  (replacement || document.getElementById("task-search")).focus({ preventScroll: true });
}

async function refreshTaskDetail(tid, pid) {
  const request = ++detailRequest;
  try {
    const q = new URLSearchParams({ id: tid, t: Date.now() });
    if (pid) q.set("project", pid);
    const res = await fetch("/api/task?" + q, { cache: "no-store" });
    if (!res.ok) throw new Error(t("detail_unavailable"));
    const d = await res.json();
    if (request !== detailRequest || selectedTid !== tid || selectedPid !== pid) return;
    drawerEvents = d.events || [];
    if (d.task && lastSnap) {
      const idx = (lastSnap.tasks || []).findIndex(x => x.id === tid && (x.project_id || "") === pid);
      if (idx >= 0) lastSnap.tasks[idx] = { ...lastSnap.tasks[idx], ...d.task };
    }
  } catch (_) {
    if (request !== detailRequest) return;
    drawerEvents = [];
  }
  if (request !== detailRequest || selectedTid !== tid || selectedPid !== pid) return;
  drawerLoading = false;
  const body = document.getElementById("drawer-body");
  const scroll = body.scrollTop;
  const focusedID = document.activeElement?.id;
  renderDrawerBody();
  body.scrollTop = scroll;
  if (focusedID) document.getElementById(focusedID)?.focus({ preventScroll: true });
}

function openDrawer(tid, pid) {
  selectedTid = tid;
  selectedPid = pid || "";
  drawerReturnFocus = { tid, pid: selectedPid };
  drawerEvents = [];
  drawerLoading = true;
  const mask = document.getElementById("drawer-mask");
  const drawer = document.getElementById("drawer");
  clearTimeout(closeTimer);
  mask.hidden = false;
  drawer.inert = false;
  document.getElementById("app-shell").inert = true;
  requestAnimationFrame(() => {
    mask.classList.add("open");
    drawer.classList.add("open");
  });
  drawer.setAttribute("aria-hidden", "false");
  syncSelectionHighlight();
  renderDrawerBody();
  document.getElementById("drawer-body").scrollTop = 0;
  document.getElementById("d-close").focus();
  refreshTaskDetail(tid, selectedPid);
}

function secCopy(title, btnId, label) {
  return '<div class="sec-head"><h3>' + title + '</h3>' +
    '<button type="button" class="copy primary" id="' + btnId + '">' + label + '</button></div>';
}

function renderDrawerBody() {
  const task = findTask(selectedTid, selectedPid);
  if (!task) {
    document.getElementById("d-id").textContent = "";
    document.getElementById("d-title").textContent = t("task_missing");
    document.getElementById("drawer-body").innerHTML = '<p class="empty">当前筛选下找不到该任务</p>';
    return;
  }
  selectedPid = task.project_id || selectedPid;
  const p = findProject(task.project_id) || {};
  const cmd = "maw task show " + task.id;
  const prompt = buildTaskPrompt(task);
  const cdWt = task.worktree
    ? "cd " + ((p.path || "<工程路径>").replace(/\\/g, "/") + "/" + task.worktree)
    : "";
  document.getElementById("d-id").textContent = task.id;
  document.getElementById("d-title").textContent = task.title || t("no_title");

  let logsHtml;
  if (drawerLoading) {
    logsHtml = '<p class="empty">正在加载日志…</p>';
  } else if (drawerEvents.length) {
    logsHtml = '<ul class="timeline">' + drawerEvents.map(ev => {
      const lab = EVENT_LABEL[ev.type] || ev.type;
      const msg = formatEventData(ev);
      return '<li><div class="ts">' + esc(fmtTime(ev.ts)) + '</div>' +
        '<div><span class="ev-type">' + esc(lab) + '</span>' +
        '<span class="ev-actor">' + esc(ev.actor || "") + '</span></div>' +
        (msg ? '<div class="ev-msg">' + esc(msg) + '</div>' : "") +
        '</li>';
    }).join("") + '</ul>';
  } else {
    logsHtml = '<p class="empty">暂无任务日志（需 <code>maw board --serve</code>）</p>';
  }

  const agent = (lastSnap?.agents || []).find(a =>
    a.task_id === task.id && (!task.project_id || a.project_id === task.project_id)
  );

  document.getElementById("drawer-body").innerHTML =
    '<div class="drawer-actions">' +
      '<button type="button" class="copy primary" id="d-copy-prompt-top">复制接入提示词</button>' +
      '<button type="button" class="copy" id="d-copy-cmd-top">复制查看命令</button>' +
      (cdWt ? '<button type="button" class="copy" id="d-copy-wt">复制进入 worktree</button>' : "") +
    '</div>' +
    '<div class="drawer-section"><h3>基本信息</h3><dl class="kv">' +
      '<dt>状态</dt><dd>' + statusBadge(task.status) + '<code style="font-size:.68rem;color:var(--muted)">' + esc(task.status) + '</code></dd>' +
      '<dt>优先级</dt><dd>' + esc(task.priority ?? "—") + ' · merge_order ' + esc(task.merge_order ?? "—") + '</dd>' +
      '<dt>负责人</dt><dd>' + esc(task.owner || t("unassigned")) + '</dd>' +
      '<dt>依赖</dt><dd>' + esc((task.deps && task.deps.length) ? task.deps.join(", ") : t("none")) + '</dd>' +
      '<dt>尝试次数</dt><dd>' + esc(task.attempts ?? 0) + ' · 修订 ' + esc(task.revision ?? 0) + '</dd>' +
      '<dt>心跳</dt><dd>' + esc(fmtTime(task.heartbeat) || "—") + '</dd>' +
      (task.blocked_reason ? '<dt>阻塞原因</dt><dd>' + esc(task.blocked_reason) + '</dd>' : "") +
    '</dl></div>' +
    '<div class="drawer-section"><h3>执行节点</h3><dl class="kv">' +
      '<dt>工程</dt><dd>' + esc(task.project_name || p.name || "—") + '</dd>' +
      '<dt>工程路径</dt><dd>' + esc(p.path || "—") + '</dd>' +
      '<dt>Worktree</dt><dd>' + esc(task.worktree || "—") + '</dd>' +
      '<dt>分支</dt><dd>' + esc(task.branch || "—") + '</dd>' +
      '<dt>Agent</dt><dd>' + esc(agent ? (agent.id + " · " + (agent.status || "") + " · " + (fmtTime(agent.last_seen) || "")) : (task.owner || "—")) + '</dd>' +
    '</dl></div>' +
    '<div class="drawer-section"><h3>write_scope</h3>' + listBullets(task.write_scope, t("empty")) + '</div>' +
    '<div class="drawer-section"><h3>read_scope</h3>' + listBullets(task.read_scope, t("none")) + '</div>' +
    '<div class="drawer-section"><h3>acceptance</h3>' + listBullets(task.acceptance, t("none")) + '</div>' +
    '<div class="drawer-section">' + secCopy(t("show_cmd"), "d-copy-cmd", t("one_click_copy")) +
      '<div class="cmd-box">' + esc(cmd) + '</div></div>' +
    '<div class="drawer-section">' + secCopy(t("join_prompt"), "d-copy-prompt", t("one_click_copy")) +
      '<div class="prompt-box" id="d-prompt">' + esc(prompt) + '</div></div>' +
    '<div class="drawer-section"><h3>任务日志</h3>' + logsHtml + '</div>';

  const bind = (id, text, ok) => {
    const el = document.getElementById(id);
    if (!el) return;
    el.onclick = async (e) => { e.stopPropagation(); await copyText(el, text, ok); };
  };
  bind("d-copy-prompt-top", prompt, t("copied_prompt"));
  bind("d-copy-prompt", prompt, t("copied_prompt"));
  bind("d-copy-cmd-top", cmd, t("copied"));
  bind("d-copy-cmd", cmd, t("copied"));
  bind("d-copy-wt", cdWt, t("copied"));

  document.querySelectorAll(".card.clickable").forEach(el => {
    const on = el.dataset.tid === task.id && (!selectedPid || el.dataset.pid === selectedPid);
    el.classList.toggle("active", on);
  });
}

function qs() {
  const p = new URLSearchParams();
  if (filters.project) p.set("project", filters.project);
  if (filters.agent) p.set("agent", filters.agent);
  if (filters.status) p.set("status", filters.status);
  p.set("t", Date.now());
  return p.toString();
}

async function loadSnapshot() {
  const q = qs();
  const urls = ["/api/board?" + q, "./snapshot.json?" + q];
  let lastErr;
  for (const u of urls) {
    try {
      const res = await fetch(u, { cache: "no-store" });
      if (!res.ok) throw new Error(res.status + " " + res.statusText);
      return await res.json();
    } catch (e) { lastErr = e; }
  }
  throw lastErr || new Error(t("load_fail"));
}

function fillSelect(el, items, valueKey, labelKey, current, allLabel) {
  const cur = current || "";
  const html = '<option value="">' + esc(allLabel) + '</option>' +
    items.map(it => {
      const v = it[valueKey] || "";
      const lab = it[labelKey] || v;
      return '<option value="'+esc(v)+'"'+(v===cur?' selected':'')+'>'+esc(lab)+'</option>';
    }).join("");
  if (el.innerHTML !== html) el.innerHTML = html;
}

function syncFilterControls(s) {
  projectOptions = s.projects || [];
  const agents = s.agents || [];
  const seen = new Set(agentOptions.map(a => a.id));
  agents.forEach(a => {
    if (!seen.has(a.id)) { seen.add(a.id); agentOptions.push({id: a.id, label: a.id}); }
  });
  (s.tasks || []).forEach(t => {
    if (t.owner && !seen.has(t.owner)) {
      seen.add(t.owner);
      agentOptions.push({id: t.owner, label: t.owner});
    }
  });
  agentOptions.sort((a,b) => a.id.localeCompare(b.id));
  fillSelect(document.getElementById("f-project"), projectOptions, "id", "name", filters.project, t("all_projects"));
  fillSelect(document.getElementById("f-agent"), agentOptions, "id", "label", filters.agent, t("all_agents"));
  document.getElementById("f-status").value = filters.status || "";
}

function visibleTasks(s) {
  const query = searchTerm.trim().toLocaleLowerCase();
  return (s.tasks || []).filter(t =>
    (!filters.project || t.project_id === filters.project) &&
    (!filters.agent || t.owner === filters.agent) &&
    (!filters.status || t.status === filters.status) &&
    (!query || [t.id, t.title, t.owner, t.project_name, t.project_id].join(" ").toLocaleLowerCase().includes(query)));
}

/** 中位数；空数组返回 null（调用方回退到原序）。 */
function topoMedian(values) {
  if (!values.length) return null;
  const s = values.slice().sort((a, b) => a - b);
  const mid = Math.floor((s.length - 1) / 2);
  return s.length % 2 === 1 ? s[mid] : (s[mid] + s[mid + 1]) / 2;
}

/** 轨道取整：多依赖中位可能为 .5，落到最近整数行。 */
function topoTrackRound(v) {
  if (v == null || Number.isNaN(v)) return null;
  return Math.round(v);
}

/**
 * 在 used 集合中找离 want 最近的空闲轨道（同行优先，再上下对称扩展）。
 * 保证有依赖关系的节点尽量同一行，冲突时也彼此靠近。
 */
function topoNearestFreeTrack(want, used) {
  let t = Math.max(0, want | 0);
  if (!used.has(t)) return t;
  for (let delta = 1; delta < 10000; delta++) {
    const up = t - delta;
    const down = t + delta;
    if (up >= 0 && !used.has(up)) return up;
    if (!used.has(down)) return down;
  }
  return t;
}

/**
 * 同工程内分层：依赖在左、依赖方在右（完整依赖链深度，含已完成）。
 * 已完成链也必须左右排开，否则会叠成竖排、边变成上下连线。
 * 纵轴：barycenter 减交叉；单链尽量同一行；多依赖取中位行；冲突选最近空闲行。
 */
function layoutTopoBands(tasks) {
  const groups = new Map();
  for (const t of tasks) {
    const pid = t.project_id || "";
    if (!groups.has(pid)) groups.set(pid, []);
    groups.get(pid).push(t);
  }
  const bands = [];
  for (const [pid, group] of groups) {
    const byId = new Map(group.map(t => [t.id, t]));
    const stubs = new Set();
    for (const t of group) {
      for (const d of (t.deps || [])) if (d && !byId.has(d)) stubs.add(d);
    }
    const level = new Map();
    const walk = (id, stack) => {
      if (level.has(id)) return level.get(id);
      if (stack.has(id)) return 0;
      stack.add(id);
      const task = byId.get(id);
      let L = 0;
      if (task) {
        for (const d of (task.deps || [])) {
          if (!d) continue;
          // 含已完成依赖：VN-801→802→804 应落在相邻波次，左右连线。
          L = Math.max(L, (byId.has(d) || stubs.has(d) ? walk(d, stack) : -1) + 1);
        }
      }
      level.set(id, L);
      stack.delete(id);
      return L;
    };
    for (const id of stubs) walk(id, new Set());
    for (const t of group) walk(t.id, new Set());
    const layers = [];
    for (const [id, L] of level) {
      if (!layers[L]) layers[L] = [];
      layers[L].push(id);
    }

    const preds = new Map(), succs = new Map();
    for (const id of level.keys()) {
      preds.set(id, []);
      succs.set(id, []);
    }
    for (const t of group) {
      for (const d of (t.deps || [])) {
        if (!d || !level.has(d) || !level.has(t.id)) continue;
        preds.get(t.id).push(d);
        succs.get(d).push(t.id);
      }
    }

    const tieBreak = (a, b) => {
      const ta = byId.get(a), tb = byId.get(b);
      const pa = ta?.priority ?? 1e9, pb = tb?.priority ?? 1e9;
      if (pa !== pb) return pa - pb;
      return a.localeCompare(b);
    };
    for (const layer of layers) {
      if (!layer) continue;
      layer.sort(tieBreak);
    }

    // 多轮 barycenter：稳定兄弟相对顺序、减少边交叉。
    for (let pass = 0; pass < 4; pass++) {
      for (let li = 1; li < layers.length; li++) {
        const layer = layers[li];
        const prev = layers[li - 1];
        if (!layer || !prev || !prev.length) continue;
        const order = new Map(prev.map((id, i) => [id, i]));
        layer.sort((a, b) => {
          const ma = topoMedian((preds.get(a) || []).map((p) => order.get(p)).filter((v) => v != null));
          const mb = topoMedian((preds.get(b) || []).map((p) => order.get(p)).filter((v) => v != null));
          if (ma == null && mb == null) return tieBreak(a, b);
          if (ma == null) return 1;
          if (mb == null) return -1;
          if (ma !== mb) return ma - mb;
          return tieBreak(a, b);
        });
      }
      for (let li = layers.length - 2; li >= 0; li--) {
        const layer = layers[li];
        const next = layers[li + 1];
        if (!layer || !next || !next.length) continue;
        const order = new Map(next.map((id, i) => [id, i]));
        layer.sort((a, b) => {
          const ma = topoMedian((succs.get(a) || []).map((s) => order.get(s)).filter((v) => v != null));
          const mb = topoMedian((succs.get(b) || []).map((s) => order.get(s)).filter((v) => v != null));
          if (ma == null && mb == null) return tieBreak(a, b);
          if (ma == null) return 1;
          if (mb == null) return -1;
          if (ma !== mb) return ma - mb;
          return tieBreak(a, b);
        });
      }
    }

    // 水平轨道：单链同行；多依赖取前驱轨道中位；冲突选最近空闲行（而非整列下推）。
    const track = new Map();
    const desiredOf = (id) => {
      const up = (preds.get(id) || []).filter((p) => track.has(p)).map((p) => track.get(p));
      const down = (succs.get(id) || []).filter((s) => track.has(s)).map((s) => track.get(s));
      // 单前驱：优先与前驱同一行（链式对齐）。
      if (up.length === 1) return up[0];
      // 多前驱：取离它们都近的中位行。
      if (up.length >= 2) {
        const m = topoTrackRound(topoMedian(up));
        if (m != null) return m;
      }
      // 仅有后继时（回溯），靠后继中位。
      if (down.length === 1) return down[0];
      if (down.length >= 2) {
        const m = topoTrackRound(topoMedian(down));
        if (m != null) return m;
      }
      return null;
    };

    const assignLayer = (layer, fallbackStart) => {
      if (!layer || !layer.length) return fallbackStart;
      const wants = new Map();
      let fb = fallbackStart;
      for (const id of layer) {
        let w = desiredOf(id);
        if (w == null) w = fb++;
        wants.set(id, w);
      }
      const ordered = layer.slice().sort((a, b) => {
        const d = wants.get(a) - wants.get(b);
        if (d !== 0) return d;
        return tieBreak(a, b);
      });
      const used = new Set();
      for (const id of ordered) {
        const got = topoNearestFreeTrack(wants.get(id), used);
        track.set(id, got);
        used.add(got);
      }
      layer.length = 0;
      layer.push(...ordered.sort((a, b) => track.get(a) - track.get(b) || tieBreak(a, b)));
      let maxT = fallbackStart;
      for (const id of layer) maxT = Math.max(maxT, track.get(id) + 1);
      return maxT;
    };

    // 前向：按波次落到靠近前驱的行。
    let nextFree = 0;
    for (let li = 0; li < layers.length; li++) {
      nextFree = Math.max(nextFree, assignLayer(layers[li], nextFree));
    }

    // 回溯 2 轮：把分叉源 / 汇合点拉到相连轨道中位，再按层消冲突，保持靠近。
    for (let pass = 0; pass < 2; pass++) {
      for (let li = layers.length - 1; li >= 0; li--) {
        const layer = layers[li];
        if (!layer) continue;
        for (const id of layer) {
          const outs = (succs.get(id) || []).filter((s) => track.has(s)).map((s) => track.get(s));
          const ins = (preds.get(id) || []).filter((p) => track.has(p)).map((p) => track.get(p));
          if (outs.length >= 1) {
            const m = outs.length === 1 ? outs[0] : topoTrackRound(topoMedian(outs));
            if (m != null) track.set(id, m);
          } else if (ins.length >= 2) {
            const m = topoTrackRound(topoMedian(ins));
            if (m != null) track.set(id, m);
          }
        }
        const used = new Set();
        const ordered = layer.slice().sort((a, b) => track.get(a) - track.get(b) || tieBreak(a, b));
        for (const id of ordered) {
          const got = topoNearestFreeTrack(track.get(id), used);
          track.set(id, got);
          used.add(got);
        }
        layer.length = 0;
        layer.push(...ordered.sort((a, b) => track.get(a) - track.get(b) || tieBreak(a, b)));
      }
      // 再前向一次，让后继重新贴齐前驱。
      for (let li = 1; li < layers.length; li++) {
        const layer = layers[li];
        if (!layer) continue;
        for (const id of layer) {
          const w = desiredOf(id);
          if (w != null) track.set(id, w);
        }
        const used = new Set();
        const ordered = layer.slice().sort((a, b) => track.get(a) - track.get(b) || tieBreak(a, b));
        for (const id of ordered) {
          const got = topoNearestFreeTrack(track.get(id), used);
          track.set(id, got);
          used.add(got);
        }
        layer.length = 0;
        layer.push(...ordered.sort((a, b) => track.get(a) - track.get(b) || tieBreak(a, b)));
      }
    }

    const positions = new Map();
    let width = TOPO_PAD * 2, height = TOPO_PAD * 2;
    layers.forEach((layer, li) => {
      if (!layer) return;
      layer.forEach((id) => {
        const x = TOPO_PAD + li * (TOPO_NODE_W + TOPO_GAP_X);
        const y = TOPO_PAD + track.get(id) * (TOPO_NODE_H + TOPO_GAP_Y);
        positions.set(id, { x, y, stub: !byId.has(id) });
        width = Math.max(width, x + TOPO_NODE_W + TOPO_PAD);
        height = Math.max(height, y + TOPO_NODE_H + TOPO_PAD);
      });
    });
    const edges = [];
    for (const t of group) {
      for (const d of (t.deps || [])) {
        if (!d || !positions.has(d) || !positions.has(t.id)) continue;
        edges.push({ from: d, to: t.id, cross: !byId.has(d) });
      }
    }
    const edgeCount = edges.length;
    const indeg = new Map(), outdeg = new Map();
    for (const e of edges) {
      outdeg.set(e.from, (outdeg.get(e.from) || 0) + 1);
      indeg.set(e.to, (indeg.get(e.to) || 0) + 1);
    }
    bands.push({
      pid,
      name: group[0]?.project_name || pid || t("unknown_project"),
      tasks: group,
      byId,
      positions,
      edges,
      edgeCount,
      indeg,
      outdeg,
      width,
      height,
    });
  }
  bands.sort((a, b) => a.name.localeCompare(b.name, "zh"));
  return bands;
}

function topoEdgePath(from, to) {
  const x1 = from.x + TOPO_NODE_W;
  const y1 = from.y + TOPO_NODE_H / 2;
  const x2 = to.x;
  const y2 = to.y + TOPO_NODE_H / 2;
  const mx = (x1 + x2) / 2;
  return "M " + x1 + " " + y1 + " C " + mx + " " + y1 + ", " + mx + " " + y2 + ", " + x2 + " " + y2;
}

/** 与 topoEdgePath 同控制点的曲线中点（用于中点圆点）。 */
function topoEdgeMid(from, to) {
  const x1 = from.x + TOPO_NODE_W;
  const y1 = from.y + TOPO_NODE_H / 2;
  const x2 = to.x;
  const y2 = to.y + TOPO_NODE_H / 2;
  return { x: (x1 + x2) / 2, y: (y1 + y2) / 2 };
}

function renderTopoNode(task, pos, indeg, outdeg) {
  const active = selectedTid === task.id && selectedPid === (task.project_id || "");
  const deps = task.deps || [];
  const inN = indeg.get(task.id) || 0;
  const outN = outdeg.get(task.id) || 0;
  const st = task.status || "";
  return '<article class="topo-node clickable'+(active?' active':'')+'" tabindex="0" data-tid="'+esc(task.id)+'" data-pid="'+esc(task.project_id||'')+'" data-status="'+esc(st)+'" style="left:'+pos.x+'px;top:'+pos.y+'px" aria-label="'+esc(task.id+' '+task.title+'，'+statusLabel(st))+'">'+
    '<div class="card-top"><span class="id">'+esc(task.id)+'</span>'+statusBadge(st)+'</div>'+
    '<h4 class="task-title">'+esc(task.title || t("no_title"))+'</h4>'+
    '<div class="topo-meta">'+
      '<span class="owner">'+esc(task.owner || t("unassigned"))+'</span>'+
      '<span class="priority" title="'+esc(t("prio_title"))+'">P'+esc(task.priority ?? "—")+'</span>'+
      '<span title="入度 / 出度">←'+inN+' · '+outN+'→</span>'+
    '</div>'+
    (deps.length ? '<div class="topo-deps">deps: '+esc(deps.join(", "))+'</div>' : '')+
  '</article>';
}

function topoCanvas() {
  const bundle = typeof MawBoard !== "undefined" ? MawBoard : null;
  return bundle && typeof bundle.renderTopology === "function" ? bundle : null;
}

function bandCountText(band) {
  return band.tasks.length + t("tasks_edges") + band.edgeCount + t("edges");
}

function renderTopoCanvas(root, bands) {
  const bundle = topoCanvas();
  const statusLabels = Object.fromEntries(COLUMNS);
  bundle.renderTopology({
    container: root,
    bands,
    selected: selectedTid ? { tid: selectedTid, pid: selectedPid || "" } : null,
    onOpen: (tid, pid) => openDrawer(tid, pid),
    statusLabels,
    strings: {
      fit: t("topo.fit"),
      relayout: t("topo.relayout"),
      legend: t("topo.legend"),
      legendCross: t("topo.legend_cross"),
      wave: "W",
      stubTitle: t("topo.outside"),
      stubHint: t("topo.not_in_view"),
    },
  });
}

function renderTopology(s) {
  const tasks = visibleTasks(s);
  const root = document.getElementById("topology");
  const scrollLeft = root.scrollLeft, scrollTop = root.scrollTop;
  const oldFocus = document.activeElement?.closest(".topo-node.clickable");
  const bands = layoutTopoBands(tasks);
  const edgeTotal = bands.reduce((n, b) => n + b.edgeCount, 0);
  const canvas = topoCanvas();
  if (canvas) {
    for (const band of bands) band.countText = bandCountText(band);
  }
  let html;
  if (!tasks.length) {
    html = '<p class="topo-empty">'+(searchTerm || filters.status || filters.agent ? t("no_tasks_filter") : t("no_tasks_topo"))+'</p>';
  } else {
    html = '<div class="topo-legend"><span><i></i>依赖方向：前置 → 后续</span><span><i class="cross"></i>虚线：当前视图外的依赖</span></div>' + bands.map(band => {
      const markerId = "topo-arrow-" + esc(band.pid || "all").replace(/[^a-zA-Z0-9_-]/g, "_");
      const paths = band.edges.map(e => {
        const a = band.positions.get(e.from), b = band.positions.get(e.to);
        if (!a || !b) return "";
        const mid = topoEdgeMid(a, b);
        return '<path class="'+(e.cross ? "cross" : "")+'" d="'+topoEdgePath(a, b)+'" marker-end="url(#'+markerId+')"/>'+
          '<circle class="topo-edge-dot'+(e.cross ? " cross" : "")+'" cx="'+mid.x+'" cy="'+mid.y+'" r="3.5"/>';
      }).join("");
      const nodes = [...band.positions.entries()].map(([id, pos]) => {
        const task = band.byId.get(id);
        if (pos.stub || !task) {
          return '<article class="topo-node stub" style="left:'+pos.x+'px;top:'+pos.y+'px" aria-label="缺失依赖 '+esc(id)+'"><div class="id">'+esc(id)+'</div><h4 class="task-title">筛选外 / 历史依赖</h4><div class="topo-meta"><span>不在当前视图</span></div></article>';
        }
        return renderTopoNode(task, pos, band.indeg, band.outdeg);
      }).join("");
      return '<section class="topo-band" data-project="'+esc(band.pid)+'">'+
        '<div class="topo-band-label"><span>'+esc(band.name)+'</span><span class="count">'+band.tasks.length+' 任务 · '+band.edgeCount+' 边</span></div>'+
        '<div class="topo-canvas" style="width:'+band.width+'px;height:'+band.height+'px">'+
          '<svg class="topo-edges" viewBox="0 0 '+band.width+' '+band.height+'" width="'+band.width+'" height="'+band.height+'" aria-hidden="true">'+
            '<defs><marker id="'+markerId+'" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z"/></marker></defs>'+
            paths+
          '</svg>'+
          nodes+
        '</div></section>';
    }).join("");
  }
  if (!tasks.length && canvas) {
    canvas.disposeTopology(root);
  } else if (tasks.length && canvas) {
    renderedTopoHTML = "";
    renderTopoCanvas(root, bands);
    html = null;
  }
  if (html !== null && renderedTopoHTML !== html) {
    renderedTopoHTML = html;
    root.innerHTML = html;
    root.scrollLeft = scrollLeft;
    root.scrollTop = scrollTop;
    if (oldFocus && !selectedTid) {
      const replacement = [...root.querySelectorAll(".topo-node.clickable")].find(el => el.dataset.tid === oldFocus.dataset.tid && el.dataset.pid === oldFocus.dataset.pid);
      replacement?.focus({ preventScroll: true });
    }
  }
  const bandNote = bands.length > 1 ? " · " + bands.length + " 个工程" : "";
  document.getElementById("board-count").textContent = searchTerm
    ? tasks.length + " 个匹配任务 / " + (s.total || 0) + " 个任务 · " + edgeTotal + " 条依赖"
    : tasks.length + " 个任务 · " + edgeTotal + " 条依赖" + bandNote;
}

function renderBoard(s) {
  const tasks = visibleTasks(s);
  const root = document.getElementById("columns");
  const scrolls = new Map([...root.querySelectorAll(".col")].map(el => [el.dataset.lane, el.querySelector(".col-body").scrollTop]));
  const oldFocus = document.activeElement?.closest(".card");
  const focusedAction = document.activeElement?.dataset.action;
  const lanes = LANES.filter(lane => lane.id !== "attention" || tasks.some(t => lane.statuses.includes(t.status)) || lane.statuses.includes(filters.status));
  // Unknown future statuses stay visible instead of silently dropping tasks.
  if (tasks.some(t => !COLUMNS.some(([status]) => status === t.status))) {
    lanes.push({ id: "other", label: t("other_status"), statuses: [...new Set(tasks.map(t => t.status))].filter(status => !COLUMNS.some(([key]) => key === status)), symbol: "?", empty: t("no_tasks") });
  }
  const html = lanes.map((lane, i) => {
    const items = tasks.filter(t => lane.statuses.includes(t.status));
    const cards = items.map(task => {
      const active = selectedTid === task.id && selectedPid === (task.project_id || "");
      return '<article class="card clickable'+(active?' active':'')+'" tabindex="0" data-tid="'+esc(task.id)+'" data-pid="'+esc(task.project_id||'')+'" aria-label="'+esc(task.id+' '+task.title+'，'+statusLabel(task.status))+'">'+
        '<div class="card-top"><span class="id">'+esc(task.id)+'</span>'+statusBadge(task.status)+'</div>'+
        '<h4 class="task-title">'+esc(task.title)+'</h4>'+
        '<div class="card-project">'+esc(task.project_name || task.project_id || t("unknown_project"))+'</div>'+
        (task.blocked_reason ? '<div class="task-reason">'+esc(task.blocked_reason)+'</div>' : '')+
        '<div class="card-bottom"><span class="owner">'+esc(task.owner || t("unassigned"))+'</span><span class="priority" title="'+esc(t("prio_title"))+'">P'+esc(task.priority ?? "—")+'</span></div>'+
        '<div class="actions"><button type="button" class="copy primary" data-action="prompt" aria-label="'+esc('复制 '+task.id+' 接入提示词')+'">接入提示词 ↗</button><button type="button" class="copy" data-action="command" aria-label="'+esc('复制 '+task.id+' 查看命令')+'">⌘ 命令</button></div></article>';
    }).join("");
    return '<section class="col" data-lane="'+lane.id+'" aria-label="'+lane.label+'"><h3><span class="lane-index">'+String(i+1).padStart(2,"0")+'</span>'+lane.label+'<span class="count">'+items.length+'</span><span class="lane-symbol" aria-hidden="true">'+lane.symbol+'</span></h3><div class="col-body">'+(cards || '<div class="lane-empty"><span aria-hidden="true">'+lane.symbol+'</span>'+lane.empty+'</div>')+'</div></section>';
  }).join("");
  // Unchanged polls must not replace focused controls or reset column scrolling.
  if (renderedBoardHTML !== html) {
    renderedBoardHTML = html;
    root.innerHTML = html;
    root.querySelectorAll(".col").forEach(el => { el.querySelector(".col-body").scrollTop = scrolls.get(el.dataset.lane) || 0; });
    if (oldFocus && !selectedTid) {
      const replacement = [...root.querySelectorAll(".card")].find(el => el.dataset.tid === oldFocus.dataset.tid && el.dataset.pid === oldFocus.dataset.pid);
      (focusedAction ? replacement?.querySelector('[data-action="'+focusedAction+'"]') : replacement)?.focus({ preventScroll: true });
    }
  }
  document.getElementById("board-count").textContent = searchTerm ? tasks.length + " 个匹配任务 / " + (s.total || 0) + " 个任务" : tasks.length + " 个任务 · " + lanes.length + " 个阶段";
}

function renderWorkspace(s) {
  if (boardView === "factory") {
    const root = document.getElementById("factory");
    const tasks = visibleTasks(s);
    if (window.MawBoard?.renderFactory) {
      window.MawBoard.renderFactory(root, { tasks, agents: s.agents || [], lang: uiLang, onTask: openDrawer });
      window.MawBoard.setFactoryVisible(root, true);
    } else {
      root.textContent = uiLang === "zh" ? "三维资源尚未加载，请稍候；也可切回任务看板。" : "3D assets are loading. You can also use the Board view.";
    }
    document.getElementById("board-count").textContent = tasks.length + (uiLang === "zh" ? " 件货物 · 实时状态" : " boxes · live status");
  }
  else if (boardView === "topo") renderTopology(s);
  else renderBoard(s);
}

function render(s) {
  lastSnap = s;
  if (!filters._inited) {
    // 优先 URL #工程ID，其次服务端筛选参数
    filters.project = readProjectHash() || s.filter_project || "";
    filters.agent = s.filter_agent || "";
    filters.status = s.filter_status || "";
    filters._inited = true;
    if (filters.project) writeProjectHash(filters.project);
  }
  syncFilterControls(s);
  const pct = Math.max(0, Math.min(100, Number(s.percent) || 0));
  const projects = s.projects || [];
  const selectedProject = projects.find(p => p.id === filters.project);
  document.getElementById("workspace-name").textContent = selectedProject?.name || t("all_projects");
  document.getElementById("projects-count").textContent = String(projects.length).padStart(2, "0");
  snapshotHealthy = true;
  updateSyncStatus();
  const attention = (s.tasks || []).filter(t => ["blocked", "conflict"].includes(t.status)).length;
  const stat = (label, value, title, extra = "", cls = "") =>
    '<div class="stat" title="'+esc(title)+'"><div class="stat-label">'+label+'</div><div class="stat-value '+cls+'">'+value+'</div>'+extra+'</div>';
  document.getElementById("stats").innerHTML =
    stat(t("progress"), pct+'<small>% · '+esc(s.done || 0)+' / '+esc(s.total || 0)+'</small>', t("progress"),
      '<div class="bar-wrap" role="progressbar" aria-label="'+esc(t("progress"))+'" aria-valuenow="'+pct+'" aria-valuemin="0" aria-valuemax="100"><span class="bar" style="width:'+pct+'%"></span></div>', "accent")+
    stat(t("flow"), esc(s.active || 0), t("flow_hint"))+
    stat("可领取", esc(s.counts?.ready || 0), t("deps_ready"))+
    stat("需处理", attention, t("blocked_conflict"), "", attention ? "danger" : "");
  const nextText = s.next_hint || t("no_hint");
  document.getElementById("next").innerHTML = '<b>NEXT →</b><span>'+esc(nextText)+'</span>';
  const risks = s.risks || [];
  const riskEl = document.getElementById("risks");
  riskEl.className = "hint-risks" + (risks.length ? "" : " empty");
  riskEl.textContent = risks.join("；");
  const navHTML = [{id: "", name: t("all_projects")}, ...projects].map(p => '<button type="button" class="nav-item'+(filters.project === p.id ? ' active' : '')+'" data-project="'+esc(p.id)+'"'+(filters.project === p.id ? ' aria-current="page"' : '')+' title="'+esc(p.path || p.name)+'"><span class="nav-icon" aria-hidden="true">'+(p.id ? '⌁' : '▦')+'</span><span class="nav-name">'+esc(p.name)+'</span></button>').join("");
  const nav = document.getElementById("project-nav");
  if (nav.innerHTML !== navHTML) nav.innerHTML = navHTML;
  // 侧栏只展示执行中 Agent（不展示 idle / offline）
  const agents = (s.agents || []).filter(a => a.status === "working");
  document.getElementById("agents-count").textContent = String(agents.length).padStart(2,"0");
  document.getElementById("agents").innerHTML = agents.length ? agents.map(a =>
    '<div class="agent-chip" title="'+esc([a.project_name || a.project_id, a.id, a.task_id || t("no_task"), fmtTime(a.last_seen)].join(' / '))+'"><div class="agent-top"><span class="agent-dot st-working"></span><span class="id">'+esc(a.id)+'</span><span class="agent-state">执行中</span></div><div class="meta">'+esc(a.task_id || t("no_task"))+' · '+esc(a.project_name || a.project_id)+'</div></div>'
  ).join("") : '<p class="empty" style="padding:0 9px">暂无执行中 Agent</p>';
  document.getElementById("f-copy-join").disabled = !projects.length;
  document.getElementById("f-copy-join").title = selectedProject ? "复制 " + selectedProject.name + " 接入提示词" : projects.length > 1 ? t("pick_project") : t("join_prompt_btn");
  renderWorkspace(s);
  if (selectedTid && !findTask(selectedTid, selectedPid)) closeDrawer();
}

function applyFiltersFromUI() {
  setProjectFilter(document.getElementById("f-project").value || "");
  filters.agent = document.getElementById("f-agent").value || "";
  filters.status = document.getElementById("f-status").value || "";
  tick();
}

document.getElementById("f-reset").onclick = () => {
  setProjectFilter("");
  filters.agent = ""; filters.status = "";
  searchTerm = "";
  document.getElementById("task-search").value = "";
  tick();
};
["f-project","f-agent","f-status"].forEach(id => {
  document.getElementById(id).addEventListener("change", applyFiltersFromUI);
});
document.getElementById("f-copy-join").onclick = async () => {
  const btn = document.getElementById("f-copy-join");
  if (!filters.project && projectOptions.length > 1) {
    document.getElementById("f-project").focus();
    btn.textContent = t("pick_project_short");
    setTimeout(() => { btn.textContent = t("join_prompt_btn"); }, 2000);
    return;
  }
  await copyText(btn, buildProjectJoinPrompt(filters.project || ""), t("copied_prompt"));
};
document.getElementById("d-close").onclick = closeDrawer;
document.getElementById("drawer-mask").onclick = closeDrawer;
document.getElementById("f-status").innerHTML += COLUMNS.map(([key, label]) => '<option value="'+key+'">'+label+'</option>').join("");
document.getElementById("task-search").addEventListener("input", e => {
  searchTerm = e.target.value;
  if (lastSnap) renderWorkspace(lastSnap);
});
document.getElementById("project-nav").addEventListener("click", e => {
  const button = e.target.closest("[data-project]");
  if (!button) return;
  setProjectFilter(button.dataset.project);
  tick();
});
if (typeof window !== "undefined" && window.addEventListener) {
  window.addEventListener("hashchange", () => {
    if (writingProjectHash) return;
    const id = readProjectHash();
    if (id === (filters.project || "")) return;
    setProjectFilter(id, { syncHash: false });
    tick();
  });
}
document.getElementById("view-toggle").addEventListener("click", e => {
  const button = e.target.closest ? e.target.closest("[data-view]") : null;
  if (!button || button.disabled || button.dataset.view === boardView) return;
  if (button.dataset.view === "topo" && !canUseTopo()) {
    updateTopoButtonState();
    document.getElementById("f-project")?.focus();
    return;
  }
  setView(button.dataset.view);
});
document.getElementById("columns").addEventListener("click", async e => {
  const card = e.target.closest(".card");
  if (!card) return;
  const btn = e.target.closest("button[data-action]");
  if (btn) {
    const task = findTask(card.dataset.tid, card.dataset.pid);
    if (task) await copyText(btn, btn.dataset.action === "prompt" ? buildTaskPrompt(task) : "maw task show " + task.id, t("copied"));
  } else openDrawer(card.dataset.tid, card.dataset.pid);
});
document.getElementById("columns").addEventListener("keydown", e => {
  if (e.target.matches(".card") && ["Enter", " "].includes(e.key)) {
    e.preventDefault();
    openDrawer(e.target.dataset.tid, e.target.dataset.pid);
  }
});
document.getElementById("topology").addEventListener("click", e => {
  const node = e.target.closest(".topo-node.clickable");
  if (node) openDrawer(node.dataset.tid, node.dataset.pid);
});
document.getElementById("topology").addEventListener("keydown", e => {
  if (e.target.matches(".topo-node.clickable") && ["Enter", " "].includes(e.key)) {
    e.preventDefault();
    openDrawer(e.target.dataset.tid, e.target.dataset.pid);
  }
});
document.addEventListener("keydown", e => {
  if (selectedTid) {
    if (e.key === "Escape") closeDrawer();
    if (e.key === "Tab") {
      const controls = [...document.getElementById("drawer").querySelectorAll('button, [href], input, select, textarea, [tabindex="0"]')].filter(el => !el.disabled);
      const first = controls[0], last = controls[controls.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last?.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first?.focus(); }
    }
  } else if (e.key === "/" && !e.metaKey && !e.ctrlKey && !e.altKey && !e.target.closest("input, select, textarea, [contenteditable]")) {
    e.preventDefault();
    document.getElementById("task-search").focus();
  }
});

async function tick() {
  const request = ++snapshotRequest;
  try {
    const s = await loadSnapshot();
    if (request !== snapshotRequest) return;
    render(s);
    if (selectedTid) refreshTaskDetail(selectedTid, selectedPid);
  } catch (e) {
    if (request !== snapshotRequest) return;
    snapshotHealthy = false;
    updateSyncStatus(String(e.message || e));
    if (!lastSnap) {
      document.getElementById("board-count").textContent = t("load_task_fail");
      document.getElementById("next").innerHTML = '<b>OFFLINE</b><span>请确认 maw board --serve 正在运行，然后等待自动重连。</span>';
    }
  }
}
function updateSyncStatus(error = "") {
  const subtitle = document.getElementById("subtitle");
  const label = !snapshotHealthy ? t("sync_fail") : sseConnected ? t("live") : t("poll");
  subtitle.innerHTML = '<span class="live'+(!snapshotHealthy ? ' err' : '')+'"><span class="dot"></span>'+label+'</span>' + (lastSnap ? '<time>'+esc(fmtTime(lastSnap.updated_at))+'</time>' : '');
  subtitle.title = error || (sseConnected ? t("live_hint") : t("poll_hint"));
}

async function syncBoard() {
  if (syncRunning) { syncPending = true; return; }
  syncRunning = true;
  try {
    do {
      syncPending = false;
      await tick();
    } while (syncPending);
  } finally { syncRunning = false; }
}

function connectEvents() {
  if (typeof EventSource === "undefined" || eventStream) return;
  try { eventStream = new EventSource("/api/events"); } catch (_) { return; }
  eventStream.onopen = () => { sseConnected = true; updateSyncStatus(); };
  // Every reconnect refreshes current state, including changes during disconnection.
  eventStream.addEventListener("ready", () => syncBoard());
  eventStream.addEventListener("board-changed", () => syncBoard());
  eventStream.onerror = () => {
    sseConnected = false;
    updateSyncStatus();
    // EventSource reconnects automatically; the independent polling loop stays active.
  };
}


uiLang = readLang();
rebuildLocalizedLists();
document.addEventListener("DOMContentLoaded", () => {
  applyStaticI18n();
  const ls = document.getElementById("lang-select");
  if (ls) {
    ls.value = uiLang;
    ls.addEventListener("change", () => setLang(ls.value));
  }
  document.documentElement.lang = uiLang === "zh" ? "zh-CN" : "en";
});

// Bootstrap stays separate so the inline application can be exercised without a server.
const themeSelect = document.getElementById("theme-select");
themeSelect.value = readTheme();
themeSelect.addEventListener("change", e => setTheme(e.target.value));
// 首屏请求前从 #锚点恢复工程，再决定视图（拓扑依赖单工程）。
{
  const hashProject = readProjectHash();
  if (hashProject) setProjectFilter(hashProject, { syncHash: false });
}
boardView = readView();
if (boardView === "topo" && !canUseTopo()) boardView = "board";
document.getElementById("view-board").classList.toggle("active", boardView === "board");
document.getElementById("view-topo").classList.toggle("active", boardView === "topo");
document.getElementById("view-board").setAttribute("aria-pressed", boardView === "board" ? "true" : "false");
document.getElementById("view-topo").setAttribute("aria-pressed", boardView === "topo" ? "true" : "false");
document.getElementById("columns").hidden = boardView !== "board";
document.getElementById("topology").hidden = boardView !== "topo";
{
  const factoryRoot = document.getElementById("factory");
  if (factoryRoot) {
    factoryRoot.hidden = boardView !== "factory";
    document.getElementById("view-factory")?.classList?.toggle("active", boardView === "factory");
    document.getElementById("view-factory")?.setAttribute("aria-pressed", String(boardView === "factory"));
    if (typeof window !== "undefined") window.MawBoard?.setFactoryVisible?.(factoryRoot, boardView === "factory");
  }
}
updateTopoButtonState();
syncBoard();
connectEvents();
setInterval(syncBoard, 3000);

window.addEventListener("load", () => { if (boardView === "factory" && lastSnap) renderWorkspace(lastSnap); });
