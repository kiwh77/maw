---
name: maw-plan-push
description: >-
  在多 Agent 跑完一轮后，按 maw 规范把剩余任务一次写入蓝图并 plan apply 压入队列。
  Use when the user asks to 压入 maw、补仓 maw、maw plan apply、刷新蓝图、任务不饱和、
  sync blueprint、或 agents 完成后要把后续题灌进 maw。
---

# maw 任务压入（plan push）

在**工程根目录**操作。需要 `maw >= 0.3.1`（`write_scope` 可重叠，靠 priority 串行）。  
系列总览：[maw](../maw/SKILL.md)。未 init → [maw-init](../maw-init/SKILL.md)；尚无合理题库 → 先 [maw-task-list](../maw-task-list/SKILL.md)。

## 何时用

- 一轮 Agent 做完，看板 ready≈0 / 饥饿
- **任务清单已按 maw-task-list 拆好**，要灌进 / 刷新 `.multiagent/blueprint.toml`
- 用户说「压入 maw」「补仓」「一次灌全剩余」
- **已 init** 的工程需要刷新/重灌队列

## 硬规则

1. **工程必须已 `maw init`**；否则先 maw-init，再回来压入。
2. **`maw plan apply`（≥0.3.2）默认增量合并**：保留 done / 进行中记录；蓝图只写**尚未完成的新题**即可。需要彻底重建时才加 **`--reset`**（会清空历史，并抹掉 in_progress CLAIM）。
3. **一次压入全剩余流水线**（0.3.1+）；同目录后续题用更大 `priority` 或 `deps`，不要拆多波蓝图。
4. **勿把已 done 题再写进蓝图**（除非要用 `--reset` 做全量重置）。
5. 改蓝图后必须：`maw plan check` → 用户确认或已授权后 `maw plan apply --yes`（勿默认加 `--reset`）。
6. 压入后更新 `.multiagent/STATUS.md`（若存在）并 `maw board` 核对 ready 数。

## 工作流

```text
[0] doctor/init → [1] 盘点 → [2] 写蓝图 → [3] check → [4] apply → [5] 核对看板
```

### 0) 确认已 init

```bash
maw doctor
# 失败含「未 init」→ 切换执行 maw-init 技能，完成后再继续
```

### 1) 盘点剩余题

并行收集：

```bash
maw --version          # 需 >= 0.3.1；否则提示升级本仓库并 ./install.sh
maw board --json       # 看 done / ready / in_progress
```

对照来源（按存在性选用）：

- `docs/**任务*台账*.md` / `docs/**可执行任务清单*.md`
- `.multiagent/STATUS.md`
- 仓库实况（目录/测试是否已落地）

产出三列：**done（不入库）** / **剩余（写蓝图）** / **进行中（先收口再 apply）**。

若有 `in_progress`：

- **默认 apply 会保留**该 CLAIM（0.3.2+），不必为补仓而 unregister。
- 仅当要用 `--reset` 全量重建，或该题已合入主仓要清槽时：

```bash
export MAW_AGENT=<名>
maw unregister         # 或先在 worktree 内 maw submit
```

### 2) 写 `.multiagent/blueprint.toml`

```toml
[meta]
name = "<短名>-剩余"
version = <递增整数>
main_branch = "<本波主干>"   # 必填：与任务清单文首一致；apply 时写入 config

[[task]]
id = "T-xxx"
title = "一句话目标"
priority = 10          # 越小越先；同盘串行用 10,20,30…
deps = []              # 或 ["T-yyy"] 逻辑前置
write_scope = ["path/**"]
read_scope = []        # 可选
acceptance = ["可验证的完成条件"]
merge_order = 10       # 建议与领取/合流顺序一致，步长 10
```

**主分支（maw ≥ 0.3.9）**

- 新建/刷新波次：**必须**写 `[meta] main_branch`（来自任务清单，勿默认假设 `main`）。  
- `plan apply` 会校验分支存在，并同步 `.multiagent/config.toml` 与全局库；其后 `next` / `merge` / hooks 均按该主干。  
- 旧蓝图缺省该字段时：沿用现有 config，不改写。

**编优先级（必做）**

| 情况 | 做法 |
|---|---|
| 写盘不交叉、可并行（不同 Lane） | 相同或接近的 `priority`（如都 10）——各 Agent 各吃一 Lane |
| 同 `write_scope` / 同 Lane 串行 | 后者更大 `priority`，或 `deps = ["前者"]`（推荐 deps+priority；deps 续链最利 next 亲和） |
| 逻辑依赖（不同盘） | 只写 `deps`，priority 可并行带 |
| 同 Lane 多题 | **priority/merge_order 成带**，勿与其它 Lane 插花编号 |

**上下文亲和（0.3.5+）**

- 压入时保证：每条 Lane 在首题之后仍有同模块 / deps 续题在蓝图里，避免 Agent 合入后只能领到无关域。  
- 不要把「全仓库按 ID 绝对顺序」当成唯一优先；并行度靠 **多 Lane 同档 priority**，连贯性靠 **Lane 内 deps + 写盘前缀**。

**write_scope**

- 尽量窄：`backend/internal/foo/**`，避免无必要的 `docs/**` 大盘互锁
- 同 Lane 题保持**同一 Surface 根前缀**（便于亲和命中）
- 共享热区（`app.go`、router）要么独占一题，要么 `deps` 串行 + 更高 priority

字段细节见 [blueprint-spec.md](blueprint-spec.md)。

### 3) 校验

```bash
maw plan check .multiagent/blueprint.toml
# 或 --json
```

失败则修依赖环 / 缺字段，**不要** apply。

### 4) 压入

```bash
maw plan apply .multiagent/blueprint.toml --yes
# 危险：maw plan apply .multiagent/blueprint.toml --yes --reset
```

### 5) 核对

```bash
maw board
# ready 数应 ≥ 计划同时开工的 Agent 数；其余为 backlog（写盘重叠会暂不激活）
grep main_branch .multiagent/config.toml   # 应与蓝图 meta.main_branch 一致
```

更新 `.multiagent/STATUS.md`：蓝图 version、**main_branch**、ready 列表、backlog 依赖链、`maw --version`。

可选同步台账「当前可领」表，避免人机两套队列。

## 压入后派工（可选）

按 **[maw-task-pull](../maw-task-pull/SKILL.md)** 拉取（本工程或全局选仓）：

```bash
maw next --json   # 自动分配 {应用}-{工程}-{序号}
# 或：maw board --json --status ready → maw next -C "<path>" --agent w2 --json
maw board --serve
```

## 输出给用户（中文）

```markdown
**maw 压入**
- 版本：maw x.y.z · 蓝图 vN · 主分支：`<branch>` · 任务总数 K
- ready：T-…（可并行）
- backlog：T-…←依赖
- 已排除 done：…
- 下一步：`maw next`（Worker 名自动为 `{应用}-{工程}-{序号}`）
```

## 反模式

- 只灌 3～5 道尾巴题、不带后续同盘题 → 易再次饥饿  
- apply 前不看 in_progress → 抹掉他人 CLAIM  
- 全员 `priority = 1` 且同盘无 deps → 靠 id 偶然串行，难读  
- 未 `plan check` 直接 apply  
- 把整份历史 53 题含已完成全部灌入（除非用户要重置）  
- 多 Lane 题按全局 ID 插花排序、同档又不写 deps → Agent 连 next 上下文乱跳
