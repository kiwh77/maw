# Changelog

格式参考 [Keep a Changelog](https://keepachangelog.com/)。版本遵循语义化版本。

## [Unreleased]

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
