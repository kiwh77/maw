# Changelog

> 语言：[English](CHANGELOG.md) | 简体中文

格式参考 [Keep a Changelog](https://keepachangelog.com/)。版本遵循语义化版本。

## [Unreleased]

### Removed

- **Lead / System / Managed 调度层（破坏性）**：移除 `maw lead`、`maw system`、`maw managed`、`maw node`、隐藏 `__runner`；`board --serve` 不再启动 dispatch Leader / system Watcher；删除 `systemagent`、`engine/dispatch`、`runner`、`goal`、`httpapi/systemjobs` 与 Pi runtime。协议升至 **v9**（看板仅观测；旁路不得改 claimed）。人工主路径与 `[identity] leads` / `interrupt` / `task unblock` / `manual-compat` nodes 保留。
- **看板调度相关 UI**：侧栏隐藏「节点」「受管策略」；统计页去掉自愈页签；工程编辑去掉自动派发／节点策略字段；移除节点编辑模态与 Router 问题类型词。
- **Git hook 子系统**：删除已无入口的 `internal/hooks`、隐藏命令 `maw hook pre-commit|pre-push` 及相关 i18n（树外 attach 后 `init` 本就不装钩子）。

### Changed

- **提交后等待合入不再因心跳过期变 blocked**：`merging`/`in_review` 过期只下线 Agent（`agent_expired_waiting_merge`），保留 Owner/branch。
- **submit 自动 rebase 到主干 tip**：消灭并行合入后的 E313；并提供 `maw sync`。允许 `merging`/`in_review` 再交；必须在任务 worktree 内执行。
- **merge 不强制 MAW_AGENT**；支持 `--task` / `--abort`。prune 保护未合入产物（需 `--force-unmerged` 才删）。
- **隔离合入（脏主干不再拦）**：`maw merge` 在临时 detached worktree 上 squash，再 `update-ref` 推进主干；主检出可有无关未提交改动。`merging` 上 `[conflict]/`[env]`/`[gate]` 的 `maw block` 不再把交付打成 `changes_requested`；`unblock` 回 merging 前校验交付，失效则落到 `in_review`。
- **空 squash 早拒**：`maw submit` 发现相对主干无树差异时直接失败，避免拖到 merge 才爆。
- **统计归属**：仅上报轮次、无 `usage_records` 时，来源维度仍按 `source_turns` 出表（`usage_unknown`）。
- **仓外-only 接入（破坏性）**：运行时配置与蓝图一律在 `~/.maw/projects/<id>/`；不再读取仓内 `.multiagent/`、不再由 `maw init` 注入协议段或安装 git hooks。`register`/`next`/`board` 自动 `EnsureAttached`。旧仓内 config 需手工拷到仓外 home。
- **合入后清 worktree（协议 v8 / CLI 0.4.1）**：`maw merge --yes` 成功后删除 `.worktrees/{agent}` 与题分支；下一题 `maw next` 重建同路径树。`maw prune` 不再保留 idle 48h 温热树，仅保留 `working` / 进行中题占用。

### Added

- `maw lease list` / `maw lease release`；`unblock`/`unregister`/`reconcile` 释放可安全释放的 workspace lease
- `maw next --task`；`maw board`/`task list --iteration`；board JSON 返回 `tasks`；`maw block --agent`；`maw sync`
- `maw project add`：显式登记当前 git 仓（只写本机 `~/.maw`，零工程写盘）


- **阻塞反馈迭代**：`maw block` 写入 `block_category` + 分类 followups；看板 problems 展示类别；`request-change` 成功后刷新 `.maw/task.json`/`TASK.md`；蓝图 `write_scope` 清洗中文括号残片；领取时 `required_paths`/acceptance `required:` 基线预检；无关全仓闸门 → 可恢复 `blocked`（类别 gate）；队列卡死信号冷却 30m；`maw prune --history [--yes]` 导出并清理终态题的历史 →blocked 事件与过期 `unblock_assist`
- **工作台「自动执行」开关**：工具栏「接入」左侧一键打开/关闭无人值守执行。打开时原子写齐 `managed.auto_enabled` + `managed.auto_select` + `allowed_nodes` + `system_agent.worker_node`，并当场探活节点；节点接不了活时**拒绝开启**并给出原因码（不再出现「打开却不动」）。`GET/PUT /api/v1/projects/{id}/auto-execution`。
- **开关热生效**：`maw serve` 内的 leader 每轮读一次 `config.toml`（mtime+size），开关变化就地生效，无需重启；三开关快照落 `lead_switches_changed` 事件。
- **Router 介入闭环**：五类「队列卡死」信号（`dispatch_op_stalled`／`claimed_not_started`／`dep_deadlock`／`no_capable_node`／`repeated_failure`）从「只上报」改为按 `auto_kinds` 产作业；`GET/PUT /api/v1/projects/{id}/system-agent` 可写 `llm_command`／`auto_kinds`／`ops_allowlist`，`llm_ready` 直说 Router 能不能真正诊断；`ops_run` 结果收录命令原文、命中的白名单规则与退出码。
- **Router 可见性**：看板问题审查新增 `router`（已介入／升级／处理中）与 `router-idle`（无人接管）两类问题，与 blocked 任务逐条对账。

- `maw task cancel <id> [--reason] [--superseded-by]`：可审计取消；清空 `blocked_reason`/`branch`/`worktree` 并归档到 `extra`
- `maw request-change [--add path/**] [-m …]`：扩写当前题 write_scope（可自动探测越界改动）
- `maw prune` 输出 `kept_reasons`；idle / offline 的持久 worktree 可清（合入后不再温热保留）；删除前剥离 `TASK.md`/`.maw/`
- `plan apply` 对蓝图外仍 blocked 的题返回 `stale_blocked` 提示
- Agent 过期前若题上仍有近期 heartbeat，则续命（`agent_heartbeat_renewed`）
- 系统 Agent 层：`[system_agent]` 配置、`maw system status|trigger|run|watch`、阻塞时 Raise 通知并入队 `unblock_assist`；同总线支持 `task_split` / `ops_run`；HTTP `GET/POST …/system-jobs`；`__runner` 可按 `launch_spec.json` 接入 `session.Runner`。说明见 `docs/operations/vnext/system-agent.zh-CN.md`。
- 中英双语：文档镜像、看板语言切换、CLI / 终端看板 / 协议 / skills（`MAW_LANG`，默认 en）
- 工程可从本机中枢移除：看板「工程」页两段式移除按钮，`maw project list` / `maw project rm <id|名称>`，以及 `DELETE /api/v1/projects/{project_id}`。有未完成任务或在线 Agent 时默认拒绝（`--force` / `?force=1` 才连现场一起清）；事件流水、冻结版本与用量台账保留。

### Changed

- 看板：`cancelled` 与 `done` 分列；终态卡不再展示 `blocked_reason`；活动视图默认隐藏 cancelled
- `register` 拒绝把 JSON dump 当成 Agent 名
- merge 进入 done 时统一走终态字段收尾（与 cancel 一致）
- submit 在 freeze/gate 后再验工作区干净，避免先 auto-approve 再因 dirty 翻车
- merge 遇脏树改为可恢复的 `blocked`（不再进 conflict）；忽略误跟踪的 `TASK.md`/`.maw/`
- 默认 `heartbeat_ttl_sec` 900→1800；`block` 对写盘越界文案给出 `suggested_followups`


## [0.4.0] - 2026-10-22

### Added

- 每 Agent 持久 worktree（协议 v7）；`maw prune`
- 全局看板 SSE、深浅主题与筛选
- Agent Skills（`skills/maw*`）与看板示意截图
- GitHub Releases：多平台预编译包（tag `v*` 触发）

### Changed

- 公开品牌与 Go module 统一为 `github.com/kiwh77/maw`（原 `goal-agents`）
- 产品定位调整为「本机多 Agent 任务中枢」；README / ROADMAP 对齐后续状态汇聚与状态栏能力
- 协议与文档示例 Agent 名改为中性 `cursor-demo-1`
- `maw next` 不再受 `max_parallel` 限制；上下文亲和领题

## [0.3.x]

- 全局库多工程、增量 `plan apply`、统一 Worker 命名、优先级调度等
