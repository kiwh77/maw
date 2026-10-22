# maw 设计说明

**maw**（Multi-Agent Workspace）是本机多 Agent **任务中枢**的 CLI（Go 单二进制）：编排并行开发、跨工程汇聚状态；客户端状态栏等见 [ROADMAP.md](ROADMAP.md)。

- 语言：Go，`CGO_ENABLED=0` + modernc.org/sqlite
- **全局状态**：`~/.maw/maw.db`（可用 `MAW_GLOBAL` 覆盖），含 `projects` / `tasks` / `agents`（均带 `project_id`）
- **工程本地**：各仓库 `.multiagent/` 存 `config.toml`、`blueprint.toml`、`project.json`（旧版本地 `maw.db` 在 init 时一次性迁移）
- 蓝图：`blueprint.toml` → `maw plan apply`（**0.3.2 起增量合并**进全局库该工程任务；`--reset` 才全量清空重灌）
- **主干分支（0.3.9）**：不以协议写死 `main`；`init` 仅探测占位。创建任务清单 / 写蓝图时在 `[meta] main_branch` 显式指定本波合入与 worktree 基线；`plan apply` 同步到 `.multiagent/config.toml` 与全局 `projects.main_branch`。缺省 meta 则沿用现有 config（兼容旧蓝图）
- **合入清理（0.3.10）**：`maw merge` 成功后 best-effort 删除对应 `agent/*` 本地分支
- **每 Agent 持久 worktree（0.4.0 / 协议 v7）**：路径固定为 `.worktrees/{agent}`；`next` 时 reset 到主干 tip 并 `checkout -B` 新题分支；`merge` 成功后 **保留** worktree（ignored 依赖可复用），仅 detach 后删题分支。残留用 `maw prune`（默认预览，`--yes` 删除）清理旧「一题一树」目录与无主 `agent/*` 分支
- 协议：`AGENTS.md` 兜底 + 按检测结果写 Cursor/Qoder/Trae/CodeBuddy 等专属规则 + git hooks（v7：Worker 名 `{应用}-{工程}-{序号}`，可自行 merge，合入后可继续 next）
- **全局看板**：`maw board --serve`，HTTP API `/api/board?project=&agent=&status=`，页面内可筛选
- **调度（0.3.1）**：`internal/schedule` — 同 `write_scope` 重叠的任务可并存于蓝图；`promoteReady` / `maw next` 仅激活「deps 已满足且写盘序位最前」的题（序位键：`priority` 升序 → `merge_order` → `id`）
- **上下文亲和（0.3.5）**：`maw next` 为同一 Agent 连续领题时，在可激活 ready 中优先选与其近期完成题亲和的题（直接 deps 续链 > 写盘重叠 > 同模块路径前缀 ≥2 段），再回退序位键；拆题时应按 Lane/模块成簇，避免一波里交叉甩无关域
- **派活不卡槽（0.3.6）**：`maw next` 不再因 `max_parallel` 拒绝领取；Agent 来拉且存在可激活 ready 即派活。写盘重叠仍串行；`max_parallel` 仅看板参考

权威产品规格为设计文档 v0.2；多工程为 0.3.x 增量。
