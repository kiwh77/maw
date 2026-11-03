# Changelog

> 语言：[English](CHANGELOG.md) | 简体中文

格式参考 [Keep a Changelog](https://keepachangelog.com/)。版本遵循语义化版本。

## [Unreleased]

### Added

- 系统 Agent 层：`[system_agent]` 配置、`maw system status|trigger|run|watch`、阻塞时 Raise 通知并入队 `unblock_assist`；同总线支持 `task_split` / `ops_run`；HTTP `GET/POST …/system-jobs`；`__runner` 可按 `launch_spec.json` 接入 `session.Runner`。说明见 `docs/operations/vnext/system-agent.zh-CN.md`。
- 中英双语：文档镜像、看板语言切换、CLI / 终端看板 / 协议 / skills（`MAW_LANG`，默认 en）
- 工程可从本机中枢移除：看板「工程」页两段式移除按钮，`maw project list` / `maw project rm <id|名称>`，以及 `DELETE /api/v1/projects/{project_id}`。有未完成任务或在线 Agent 时默认拒绝（`--force` / `?force=1` 才连现场一起清）；事件流水、冻结版本与用量台账保留。


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
