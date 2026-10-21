# maw 设计说明（实施对照 v0.2）

本仓库实现命令行应用 **maw**：以 CLI 为产品本体，skills 可选。

权威规格为用户文档 **v0.2（2026-10-21）**。落地决策摘要：

- 状态：`.multiagent/maw.db`（SQLite WAL）
- 蓝图：`blueprint.toml` → `maw plan apply`
- 协议：`AGENTS.md` 等标记段 + git hooks
- M1 六命令：`init` / `plan apply` / `next` / `submit` / `merge` / `board`
- 合并：仅 `MAW_AGENT=integrator`；冲突不自动解决
- 看板：终端 + `.multiagent/board/index.html`

完整条款见项目外设计原稿；本文件随实现演进补充迁移与命令契约。
