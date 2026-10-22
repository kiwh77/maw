---
name: maw-task-list
description: >-
  按 maw 规范把需求/差距拆成可领取的任务清单（写盘边界、deps、验收、粒度），
  便于多 Agent 合理拆分与后续压入蓝图。Use when the user asks to 拆任务、建任务清单、
  可执行任务卡、maw 任务边界、Lane/Surface、细粒度拆包、或为 maw-plan-push 准备题库。
---

# maw 任务清单（拆包）

把「要做什么」拆成 **一题一领取边界** 的任务卡，使人读台账与 `blueprint.toml` 同构。  
系列：[maw](../maw/SKILL.md) → 本技能 → [maw-plan-push](../maw-plan-push/SKILL.md)。工程未接入时先 [maw-init](../maw-init/SKILL.md)。

## 何时用

- 新需求/改造要开多 Agent，尚无细粒度题库
- 现有任务过大（一题跨多模块）或过碎（无法独立验收）
- 领取后经常越界改盘 / 互相撞 `router`、`app.go`
- 准备一次性 `plan apply`，需要先有合理 ID/Surface/Deps

## 目标产物

优先写到工程文档（二选一或都要）：

1. **`docs/**可执行任务清单*.md`**（或用户指定路径）— 人读任务卡  
2. 可选同步草案：字段已能映射到 `.multiagent/blueprint.toml`（真正 apply 交给 maw-plan-push）

每题必须能直接填蓝图：`id / title / priority / deps / write_scope / acceptance`。

**文首必填主分支**：本波合入与 `maw next` worktree 基线（如 `main` / `dev` / 集成分支）。  
maw **不写死**分支名 `main`；`init` 探测值只是占位。正式主干在本清单写明，压入蓝图 `[meta] main_branch` 后由 `plan apply` 同步到 config。

## 拆包原则（领取边界）

### 1) 一题一职责

- **Goal** 只用一句话；做完有可跑的 Verify。  
- **Non-goals** 写清「本包不做」，防止顺手大改。  
- 禁止：一题同时「改契约 + 写实现 + 改前端菜单」。

### 2) Surface = 未来 write_scope

- 只列**允许改动的路径**（目录 glob 或明确文件）。  
- **越窄越好**；共享热区单独成题或标明 Owner：  
  - `backend/internal/app.go`  
  - `frontend/src/router/**`、`**/MainLayout.vue`  
  - 根 `Makefile` / 根 `README.md`  
- 同盘串行题：保留相同 Surface，用 **Deps + 更大 priority**（压入时串行），不要假装并行。

### 3) Lane = 上下文簇（连续领题不漂移）

同一 Agent 会 `submit → merge → next` 连吃多题；**拆包必须让「下一条 ready」尽量仍在同一认知上下文**。

- **先画 Lane，再拆题**：Lane = 同一业务模块 / 同一 Surface 族 / 同一契约域（如 `auth`、`billing`、`FE-PAGES-orders`）。  
- **Lane 内**：题与题用 **Deps 续链** 或 **相近 write_scope 前缀**（≥2 段路径，如 `backend/internal/foo`）；ID 连续、priority/merge_order 成带，便于 `maw next` 亲和调度。  
- **Lane 间**：写盘不交叉、priority 可同档，供多 Agent 并行；**禁止**把无关 Lane 的题交错排成「全局唯一优先级瀑布」（会导致一人连领时前后端/多域乱跳）。  
- **一 Agent 一 Lane 优先**：按计划同时开工的 worker 数准备 ≥N 条可并行 Lane，而不是把一条长链拆成 N 个互不相关的首题。  
- 清单文首可附「Lane 表」：`Lane | 题 ID 序列 | Surface 根`。

`maw next`（≥0.3.5）对同一 Agent 会优先：deps 续链 > 写盘重叠 > 同模块前缀；拆包不按 Lane 成簇时，亲和也救不了上下文漂移。

### 4) 粒度甜区

| 太大（拆开） | 刚好 | 太碎（合并） |
|---|---|---|
| 跨 2+ 业务模块实现 | 单模块一层（domain/app/http 可一题或按层拆） | 纯改名一行且无验收 |
| 「做完整个 Provider」 | CRUD API 一题、前端页一题（FE-PAGES 不碰路由） | 「加个注释」 |
| 契约+实现混题 | 契约题只写 docs；实现题 Depends 契约 | 无 Verify 的空题 |

经验：单 Agent **0.5～1 个工作日**可 Verify 完成；需要读大量无关代码则再拆。  
同 Lane 内宁可多 1～2 题短卡，也不要把下一块甩到另一 Lane 插队。

### 5) 依赖只表达真前置

- **Deps**：没有它无法正确开工（契约、端口、表）。  
- **同 Lane 续作**也可用 Deps 表达「做完 A 再做 A'」（利于 next 亲和），不要只靠口头顺序。  
- 不要把「最好先做」的跨 Lane 偏好全写成硬依赖（会串行化过度）。  
- 文档勘误类可 `Depends: 无`，与实现并行。

### 6) 契约 / 实现分离

- 契约题：Surface 仅 `docs/contracts/**`（或设计 md）；**禁止**业务代码。  
- 实现题：Contract 写「以 T-0xx 为准」；不得发明第二套字段名。

### 7) FE 拆档（若有前端）

- **FE-PAGES**：只新建 `views/**`、`api/**`、`utils/**`；**禁止** router/布局。  
- **FE-SHELL**：独占 router + 布局 + 菜单；多页挂载收成一题或明确串行。  
- 多页同业务：归同一 FE Lane，连续 ID，避免与后端 Lane 在清单里交叉编号抢全局第一优先。

## 任务卡模板（写入清单 md）

```markdown
### T-0xx <短标题>

- **Goal**：本包唯一职责
- **Non-goals**：明确不做
- **Depends**：T-… | 无
- **Surface**：\`path/**\`；禁止 …
- **Contract**：接口/表/错误码（实现题勿改）；或「见 docs/contracts/….md」
- **Steps**：
  1. …
  2. …
- **Verify**：必须跑通的命令（如 \`go test ./…\` / \`npm run build\`）
- **Deliverables**：交付物
- **Lane**（可选）：模块/域短名，同簇题共用  
- **maw**（可选，便于压入）：priority=10 · merge_order=10 · write_scope 同上
```

ID 规则：稳定前缀 + 数字（`T-061`）；**同 Lane 连续编号**；同波次预留空号便于插入。

## 工作流

```text
[1] 吃需求/差距 → [2] 画模块与写盘图 → [3] 拆卡 → [4] 并行度审查 → [5] 落盘文档
```

### 1) 吃输入

收集：需求澄清、差距表、现有代码目录、`max_parallel`（config）。  
标出 **基建闸门**（契约/边界）vs **可并行竖井**。

### 2) 写盘图（必做）

列出将被动到的顶层包，标「可并行 / 必须串行」：

```text
billing/**  ‖  sync/domain/**  ‖  desktop/**
app.go（WIRE 收口，串行）
MainLayout + router（FE-SHELL，串行）
```

### 3) 拆卡

按竖井拆实现题；共享文件单独 Owner 题。  
每题填满模板；**Verify 不能是「手动点点」**。

### 4) 并行度与上下文审查（自检）

- [ ] 文首已写 **主分支**，且与真实要合入的分支一致（`git branch -A`）  
- [ ] 任意两题若 Surface 重叠：要么 Deps 串行，要么标清 priority 序，**禁止**两 Agent 同领  
- [ ] 同时 `Depends` 已满足的题数 ≥ 计划 worker 数（否则必饿）  
- [ ] 可并行首题分属 **不同 Lane**（否则多人抢同一上下文）  
- [ ] 任一 Lane 内后续题对前题有 Deps 或同 Surface 族（连续 next 不漂到别域）  
- [ ] 无「整仓 refactor」题  
- [ ] 每题 Surface 不包含「必要时再改别的模块」这种开口  
- [ ] 字段名可映射蓝图（见 [task-card-map.md](task-card-map.md)）

### 5) 落盘

写入约定 md；文首加：

- **主分支**：`<branch>`（本波合入目标；须本地/远程已存在）  
- 与 maw 的关系（压入用 maw-plan-push → 蓝图 `meta.main_branch`）  
- 硬规则：一次一题、不越 Surface、Verify 先于完成  

可选：附「推荐第一波可并行 ID 列表」供立即 plan-push。

## 反模式

- Surface 写「相关代码」而不写路径  
- 一题含前端+后端+迁移  
- 用 CLAIM/台账代替清晰 Surface（台账是占用，不是边界）  
- 为凑并行把有真实数据依赖的两题标成无 Depends  
- 先写满 blueprint 再补清单（应先人读边界，再压入）  
- **优先级全局插花**：T-010 前端 → T-011 桌面 → T-012 又回前端（同一 Agent 连领必漂）  
- 只拆「可并行首题」、不给每条 Lane 准备续题（亲和无锚、再次饥饿）

## 输出给用户（中文）

```markdown
**任务清单**
- 路径：docs/….md · 主分支：`<branch>` · 题数 N · Lane：L1(…) / L2(…) …
- 首波可并行（每 Lane 一首题）：T-… ‖ T-…
- 串行热点：app.go / router / …
- 下一步：maw-plan-push 压入（蓝图 meta.main_branch 与清单一致）
```
