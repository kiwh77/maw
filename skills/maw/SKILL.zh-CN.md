---
name: maw
description: >-
  maw 多 Agent 工作流系列技能路由：初始化、拆任务清单、压入蓝图、领取任务、
  旁路 status 接入、看板约定。Use when the user mentions maw、多 Agent worktree、
  maw init、拆任务、拉取任务、maw next、maw-task-pull、旁路接入、status report、
  压入任务、plan apply、maw board、或不确定该用哪一个 maw-* 技能。
---

> 语言：[English](SKILL.md) | 简体中文

# maw 系列（路由）

按阶段选技能；需要细节时 **Read** 对应 `SKILL.md`。

| 阶段 | 技能 | 何时 |
|---|---|---|
| 1. 接入仓库 | **[maw-init](../maw-init/SKILL.md)** | 首次挂看板 / doctor 未接线（仓外 `~/.maw/projects/<id>/`；进仓 next 也会自动登记） |
| 2. 拆任务边界 | **[maw-task-list](../maw-task-list/SKILL.md)** | 建可执行任务清单、Surface/Deps/粒度 |
| 3. 灌任务 | **[maw-plan-push](../maw-plan-push/SKILL.md)** | 一次压入剩余题到 blueprint / apply |
| 4. **拉取任务** | **[maw-task-pull](../maw-task-pull/SKILL.md)** | 本工程或全局看板选仓后 `maw next` |
| 4b. **旁路接入** | **[maw-status-attach](../maw-status-attach/SKILL.md)** | 不走 next 的用户指派/adhoc 工作；`maw status report` 及时告之中枢 |
| 5. 执行中 | （本页速查） | progress / submit / merge / board；旁路用 status report |

要求 **maw CLI ≥ 0.4.1**（每 Agent 一路径 worktree，合入后删除；主干由清单指定需 **≥ 0.3.9**）。安装：本仓库 `./install.sh` → `~/.local/bin/maw`。

## 推荐全流程

```text
maw-init（main_branch 仅为探测占位）
  → maw-task-list（文首写明主分支 + 人读任务卡）
  → maw-plan-push（蓝图 meta.main_branch → plan apply 同步 config）
  → maw-task-pull（本工程 next，或 board 选仓后 next -C）
  → worktree 内实现 → submit → maw merge --yes
  → （仍有 ready）maw next 查询 → maw next --claim 领取 → … 循环
  → maw board --serve
```

## 执行期速查

```bash
# 本工程领题（Worker 名：{应用}-{工程}-{序号}，如 cursor-demo-1）
maw register --json          # 未指定则自动分配规范名
export MAW_AGENT=<返回的 agent>
maw next --json          # 先查询：只预览候选，不占坑
maw next --claim --json  # 确认后领取（无 MAW_AGENT 时同样自动分配）
# 全局：先看再进仓领
maw board --json --status ready
maw next -C "<project_path>" --json         # 先查询预览
maw next -C "<project_path>" --claim --json # 再领取

maw progress -m "里程碑"
maw submit
maw merge --yes          # 成功后删除 .worktrees/{agent} 与 agent/{agent}/{task} 分支
# 仍有 ready 则继续（重建 .worktrees/{agent} 到主干 tip 后换新题分支）
maw next          # 查询
maw next --claim  # 领取
# 历史残留：
# maw prune            # 预览
# maw prune --yes      # 删除

# 旁路（未 next）：注册后及时注入中枢，见 maw-status-attach
maw status report -m "实现中" --phase code --pct 40 --status working

# 中枢维护：工程登记清单 / 移除（有未完成任务或在线 Agent 时默认拒绝）
maw project list
maw project add                 # 显式登记当前仓（不写工程文件）
maw project rm <id|名称>        # --force 连现场一起清
```

**铁律**

- 只改任务 `write_scope`；越界用 `maw request-change --add 'path/**'`（成功后任务卡会刷新）；状态在 `~/.maw/maw.db`
- **遇阻一律 `maw block -m "[类别] 卡点：…；需要：…"`**（类别：scope/deps/env/spec/conflict/gate/baseline/human/other）；禁止只聊天汇报。scope 不够先 `request-change`，失败再 block。
- 非领取任务接入看板时必须 `maw status report` 及时上报（开工/里程碑/收工），勿沉默；旁路阻塞用 `--phase blocked`
- 主工作区脏导致 merge 失败时 **禁止**清别人改动；应 block 等人处理
- 每个 Agent 一个 worktree 路径：`.worktrees/{agent}`；合入后删除，下一题再重建
- **`maw submit` 前必须 `git commit`**（仅 scope 内）；未提交的 staged/unstaged 会被 submit 直接拒绝，避免 merge 报 `working tree dirty` 进 conflict
- submit 后可自行 `maw merge --yes`；禁止直接 git merge/rebase/push 主干
- 合入后若仍有 ready，继续 `maw next` 领下一题，直至无题可领或用户叫停
- `maw next` **子队列粘性**：任务 `lane` 为硬边界；首题自动粘住，可用 `--lane` / `--release-lane`；队内 priority 优先，亲和仅同分打破
- `maw next`（≥0.3.6）**不卡并行槽**：有可激活 ready 即派活；写盘重叠仍串行
- `plan apply` 默认增量合并；`--reset` 才会清空该工程任务表后重灌
- 题已 `done`/`cancelled` 仍见 dirty 文案：旧版残留；新版 merge/cancel 会清空并归档到 `extra.block_history`。蓝图外 blocked：`maw task cancel <id> --superseded-by <新题>`
- 人工领取保留 **实例／版本／来源**（`manual_next`）；令牌不得进入提示词、仓库或事件
- 旁路（`maw status report`）只上报**自己的**观察，**不得夺取 managed Run**
- 受管路径：`maw managed start --task … --node …`（默认关闭自动派发）；协议落后 → `maw init --upgrade`
- 系统 Agent（默认关）：`[system_agent] enabled=true` 后，`maw block` 会入队自愈作业；`maw system watch|trigger|run|status`；拆题 `trigger --kind task_split`；运维白名单 `ops_run`

## 相关文件

- `~/.maw/maw.db` · `~/.maw/projects/<id>/config.toml` / `blueprint.toml`
- `docs/**可执行任务清单*.md`
- （可选）仓库内 Agent 提示词与 IDE rules——CLI 不读
