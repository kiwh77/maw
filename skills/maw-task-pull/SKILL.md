---
name: maw-task-pull
description: >-
  按 maw 规范拉取任务：在本工程目录下领取本仓 ready 题，或面向全局看板跨工程选仓后拉取。
  Use when the user asks to 拉取任务、领取任务、maw next、maw-task-pull、接题、领一道题、
  或 Agent 开工前要从 maw 拿 worktree。
---

# maw 拉取任务（task-pull / next）

系列总览：[maw](../maw/SKILL.md)。未 init → [maw-init](../maw-init/SKILL.md)；无题可拉 → [maw-plan-push](../maw-plan-push/SKILL.md)。

需要 **maw ≥ 0.4.0**。拉取会复用/创建该 Agent 的持久 git worktree（`.worktrees/{agent}`），之后**只在该 worktree 内改 `write_scope`**。

## 模式选择

```text
当前目录能 resolve 到已 init 工程？
  ├─ 是，且用户未要求「全局/跨工程」 → 【本工程拉取】
  └─ 否，或用户说全局/任意工程/看板选仓 → 【全局拉取】
```

判定本工程：

```bash
maw doctor 2>/dev/null && test -f .multiagent/config.toml
```

## 身份

Worker 名统一为 `{应用}-{工程slug}-{序号}`（例：`cursor-demo-1`）。

```bash
# 推荐：不设 MAW_AGENT，让 maw 按当前应用自动分配
maw register --json   # 记下返回的 agent，本会话 export MAW_AGENT=<agent>
# 或显式：
# export MAW_AGENT=cursor-demo-1
# 应用段可用 MAW_APP=cursor|codebuddy|trae|qoder|claude|windsurf 覆盖
```

- 同一 `MAW_AGENT` 若已有进行中任务：`maw next` **幂等**，回到原 worktree（不新领）。
- 换题前须 `maw submit` / `block` / `unregister`，勿多人共用一名。
- 禁止再用 `w1`/`w2` 或业务绰号（如 `cursor-admin-fe`）。

---

## A. 本工程拉取

在工程根（或任意子目录，maw 会找 git root）执行：

```bash
maw doctor
maw board --json --status ready          # 可选：看 ready
maw next --agent "$MAW_AGENT" --json     # 自动 register
# 预览不落盘：
# maw next --agent "$MAW_AGENT" --dry-run --json
```

成功后：

1. 记下 JSON 里的 `worktree`、`task.id`、`branch`
2. `cd <worktree>`
3. 读 worktree 内任务卡（常见 `.maw/TASK.md` 或根 `TASK.md`）与清单中对应 `T-xxx`
4. **只改** `write_scope`；需要越界 → `maw request-change`（若不可用则停并报告，勿擅改）
5. 里程碑：`maw progress -m "..."`；完成：`maw submit`

无 ready：

```text
→ maw board 看 backlog/阻塞
→ 需要补题：maw-plan-push
→ 写盘与进行中题重叠：等对方 submit/merge（非槽位限制）
```

---

## B. 全局拉取（跨工程）

状态库是全局的（`~/.maw/maw.db`），但 **`maw next` 按「当前 -C 工程」领题**。全局流程 = 先选仓，再 `-C` 拉取。

```bash
# 1) 全局看板：所有工程的 ready
maw board --json --status ready

# 2) 选出目标工程 path（JSON 里 projects[].path / 任务上的 project_id）
#    优先：用户指定工程名 > 与本 Agent 近期上下文同仓 > ready 最多 > 与当前对话相关的仓

# 3) 在该工程路径下拉取
export MAW_AGENT="${MAW_AGENT:-w1}"
maw next -C "<project_path>" --agent "$MAW_AGENT" --json
```

筛选示例：

```bash
maw board --json --project "<project_id>" --status ready
maw board --serve   # 浏览器看全局；仍须用 CLI next -C 拉取
```

**不要**在未 init 的目录硬 next。选中的 `project_path` 必须已在全局库登记（曾 `maw init`）。

---

## 拉取后执行清单

```text
[ ] cd worktree（不是主工作区乱改）
[ ] 确认 task id / write_scope / acceptance
[ ] 对照 docs 任务清单中同 ID 的 Steps/Verify
[ ] 实现并跑 Verify
[ ] git commit（仅 scope 内）
[ ] maw submit
[ ] maw merge --yes —— submit 后可自行合入（保留 `.worktrees/{agent}`，删题分支）
[ ] 仍有 ready → maw next（同树 reset 主干 tip）领下一题；无题可领再停
```

## 硬规则

1. **一次一题**：有进行中任务时 next 只恢复原题，不双开（除非换 agent 名）。
2. **写盘不越界**：超出 write_scope 的改动 submit 会失败。
3. submit 后可自行 `maw merge --yes`；**禁止**直接 git `merge` / `rebase` / `push` 主干。
4. 合入后若仍有 ready，继续 `maw next` 循环执行，直至无题可领或用户叫停。
5. 全局模式下先 **board 选仓**，再 **`next -C`**；没有「一条命令跨库自动领最优题」时，由本技能按优先级选仓。
6. 选仓启发式：`ready>0` → priority 更优的题所在仓 → 用户当前打开的工程优先。**不按 max_parallel 卡槽**（≥0.3.6：有可激活 ready 即派活）。
7. **上下文亲和（maw ≥0.3.5）**：同一 `MAW_AGENT` 再 `next` 时，调度优先领与其近期完成题亲和的 ready（deps 续链 > 写盘重叠 > 同模块路径前缀），再比 `priority`/`merge_order`/`id`。无锚点（新 agent / 首题）则纯序位。  
   - 连跑时**不要**为了「换换口味」手动挑别的仓/题，除非用户指定或本 Lane 已无 ready。  
   - dry-run 可预览下一题是否仍在同模块；若漂到无关域，先查清单 Lane / 补同簇题（maw-plan-push），而不是硬做。

## 失败对照

| 现象 | 处理 |
|---|---|
| 工程未 init | maw-init |
| no ready tasks | board 查 deps；maw-plan-push 补仓 |
| no ready / 写盘被占 | board 看 deps 与进行中重叠；或 maw-plan-push 补仓 |
| 无法定位全局库 | 检查 `MAW_GLOBAL` / `~/.maw` |
| worktree 已存在冲突 | 按 maw 报错「下一步」处理；勿手删他人树 |
| `working tree dirty`（submit/merge） | **先在 worktree 内 `git add` + `git commit`（仅 write_scope）再 submit**；勿只 add 不 commit |
| 题已 `done` 仍显示 dirty/阻塞原因 | 多为历史 `blocked_reason` 残留；以 `status`/`git log` 合入记录为准（新版 merge 成功会清空该字段） |

## 输出给用户（中文）

```markdown
**已拉取**
- 模式：本工程 | 全局（工程=`…`）
- Agent：`w1` · 任务：`T-xxx` — 标题
- worktree：`…/.worktrees/w1`（Agent 持久树；题间复用）
- write_scope：…
- 下一步：cd worktree → 按 Verify 实现 → `maw submit` → `maw merge --yes` → 有 ready 则继续 `maw next`
```

预览（dry-run）时标明「未落盘」。
