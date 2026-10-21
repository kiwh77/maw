# maw 设计说明

命令行应用 **maw**（Go 单二进制）：以 CLI 为产品本体。

- 语言：Go，`CGO_ENABLED=0` + modernc.org/sqlite
- 状态：`.multiagent/maw.db`（SQLite WAL）
- 蓝图：`blueprint.toml` → `maw plan apply`
- 协议：`AGENTS.md` 等标记段 + git hooks
- M1 六命令：`init` / `plan apply` / `next` / `submit` / `merge` / `board`

权威产品规格为设计文档 v0.2。
