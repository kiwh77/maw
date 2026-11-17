# Changelog

> 语言：[English](CHANGELOG.md) | 简体中文

格式参考 [Keep a Changelog](https://keepachangelog.com/)。版本遵循语义化版本。

## [Unreleased]

### Added

- `maw task cancel <id> [--reason] [--superseded-by]`：可审计取消；清空 `blocked_reason`/`branch`/`worktree` 并归档到 `extra`
- `maw request-change [--add path/**] [-m …]`：扩写当前题 write_scope（可自动探测越界改动）
- `maw prune` 输出 `kept_reasons`；idle 超 48h / offline 的持久 worktree 可清；删除前剥离 `TASK.md`/`.maw/`
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
