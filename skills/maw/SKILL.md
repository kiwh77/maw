---
name: maw
description: >-
  maw 多 Agent 工作流系列技能路由：初始化、拆任务清单、压入蓝图、领取任务、看板约定。
  Use when the user mentions maw、多 Agent worktree、maw init、拆任务、拉取任务、
  maw next、maw-task-pull、压入任务、plan apply、maw board、或不确定该用哪一个 maw-* 技能。
---

# maw 系列（路由）

按阶段选技能；需要细节时 **Read** 对应 `SKILL.md`。

| 阶段 | 技能 | 何时 |
|---|---|---|
| 1. 接入仓库 | **[maw-init](../maw-init/SKILL.md)** | 无 `.multiagent/`、doctor 未 init、首次挂看板 |
| 2. 拆任务边界 | **[maw-task-list](../maw-task-list/SKILL.md)** | 建可执行任务清单、Surface/Deps/粒度 |
| 3. 灌任务 | **[maw-plan-push](../maw-plan-push/SKILL.md)** | 一次压入剩余题到 blueprint / apply |
| 4. **拉取任务** | **[maw-task-pull](../maw-task-pull/SKILL.md)** | 本工程或全局看板选仓后 `maw next` |
| 5. 执行中 | （本页速查） | progress / submit / merge / board |

要求 **maw CLI ≥ 0.4.0**（每 Agent 持久 worktree；主干由清单指定需 **≥ 0.3.9**）。安装：本仓库 `./install.sh` → `~/.local/bin/maw`。

## 推荐全流程

```text
maw-init（main_branch 仅为探测占位）
  → maw-task-list（文首写明主分支 + 人读任务卡）
  → maw-plan-push（蓝图 meta.main_branch → plan apply 同步 config）
  → maw-task-pull（本工程 next，或 board 选仓后 next -C）
  → worktree 内实现 → submit → maw merge --yes
  → （仍有 ready）maw next → … 循环
  → maw board --serve
```

## 执行期速查

```bash
# 本工程领题（Worker 名：{应用}-{工程}-{序号}，如 cursor-demo-1）
maw register --json          # 未指定则自动分配规范名
export MAW_AGENT=<返回的 agent>
maw next --json
# 或一步：maw next --json（无 MAW_AGENT 时同样自动分配）
# 全局：先看再进仓领
maw board --json --status ready
maw next -C "<project_path>" --json

maw progress -m "里程碑"
maw submit
maw merge --yes          # 成功后保留 .worktrees/{agent}，仅删除 agent/{agent}/{task} 分支
# 仍有 ready 则继续（同树 reset 到主干 tip 后换新题分支）
maw next
# 历史「一题一树」残留：
# maw prune            # 预览
# maw prune --yes      # 删除
```

**铁律**

- 只改任务 `write_scope`；状态在 `~/.maw/maw.db`
- 每个 Agent 一个持久 worktree：`.worktrees/{agent}`；题间复用，不新建目录
- **`maw submit` 前必须 `git commit`**（仅 scope 内）；未提交的 staged/unstaged 会被 submit 直接拒绝，避免 merge 报 `working tree dirty` 进 conflict
- submit 后可自行 `maw merge --yes`；禁止直接 git merge/rebase/push 主干
- 合入后若仍有 ready，继续 `maw next` 领下一题，直至无题可领或用户叫停
- `maw next`（≥0.3.5）对同一 Agent **上下文亲和**：优先同链/同模块续题，拆题按 Lane 成簇（见 maw-task-list）
- `maw next`（≥0.3.6）**不卡并行槽**：有可激活 ready 即派活；写盘重叠仍串行
- `plan apply` 默认增量合并；`--reset` 才会清空该工程任务表后重灌
- 题已 `done` 仍见 dirty 文案：多为旧 `blocked_reason` 残留，以合入记录为准

## 相关文件

- `.multiagent/config.toml` / `blueprint.toml` / `STATUS.md`
- `docs/**可执行任务清单*.md`
- `AGENTS.md` / `.cursor/rules/maw.mdc`
