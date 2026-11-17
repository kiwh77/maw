# maw 设计说明

**maw**（Multi-Agent Workspace）是本机多 Agent **任务中枢**的 CLI（Go 单二进制）：编排并行开发、跨工程汇聚状态；客户端状态栏等见 [ROADMAP.md](ROADMAP.md)。

- 语言：Go，`CGO_ENABLED=0` + modernc.org/sqlite
- **全局状态**：`~/.maw/maw.db`（可用 `MAW_GLOBAL` 覆盖），含 `projects` / `tasks` / `agents`（均带 `project_id`）
- **工程本地**：各仓库 `.multiagent/` 存 `config.toml`、`blueprint.toml`、`project.json`（旧版本地 `maw.db` 在 init 时一次性迁移）
- 蓝图：`blueprint.toml` → `maw plan apply`（**0.3.2 起增量合并**进全局库该工程任务；`--reset` 才全量清空重灌）
- **主干分支（0.3.9）**：不以协议写死 `main`；`init` 仅探测占位。创建任务清单 / 写蓝图时在 `[meta] main_branch` 显式指定本波合入与 worktree 基线；`plan apply` 同步到 `.multiagent/config.toml` 与全局 `projects.main_branch`。缺省 meta 则沿用现有 config（兼容旧蓝图）
- **合入清理（0.3.10）**：`maw merge` 成功后 best-effort 删除对应 `agent/*` 本地分支
- **每 Agent 持久 worktree（0.4.0 / 协议 v7）**：路径固定为 `.worktrees/{agent}`；`next` 时 reset 到主干 tip 并 `checkout -B` 新题分支；`merge` 成功后 **保留** worktree（ignored 依赖可复用），仅 detach 后删题分支。残留用 `maw prune`（默认预览，`--yes` 删除）清理：无主 `agent/*` 分支、offline / idle>48h 的 Agent 树；输出 `kept_reasons`。终态收尾：`merge`/`task cancel` 清空 `blocked_reason` 并归档
- 协议：`AGENTS.md` 兜底 + 按检测结果写 Cursor/Qoder/Trae/CodeBuddy 等专属规则 + git hooks（v7：Worker 名 `{应用}-{工程}-{序号}`，可自行 merge，合入后可继续 next）
- **全局看板**：`maw board --serve`，HTTP API `/api/board?project=&agent=&status=`，页面内可筛选
- **调度（0.3.1）**：`internal/schedule` — 同 `write_scope` 重叠的任务可并存于蓝图；`promoteReady` / `maw next` 仅激活「deps 已满足且写盘序位最前」的题（序位键：`priority` 升序 → `merge_order` → `id`）
- **子队列粘性（lane）**：拆题时按上下文写入任务 `lane`；Agent 默认首题粘住该子队列，之后只从本队领（`maw next --claim --lane` 可显式指定/换队，`--release-lane` 解除）。并行靠多队×多 Agent；本队暂无可激活题时不自动跳队
- **上下文亲和**：降级为**队内** `priority`/`merge_order` 同分时的打破（deps 续链 > 写盘重叠 > 同模块前缀），不再跨队压过序位键
- **派活不卡槽（0.3.6）**：`maw next` 不再因 `max_parallel` 拒绝领取；Agent 来拉且存在可激活 ready 即派活。写盘重叠仍串行；`max_parallel` 仅看板参考

权威产品规格为设计文档 v0.2；多工程为 0.3.x 增量。

外部对照：DeepSeek Harness「智能团队」可借鉴点与不采纳项见 [maw_from_deepseek_agent_teams.zh-CN.md](../maw_from_deepseek_agent_teams.zh-CN.md)。
