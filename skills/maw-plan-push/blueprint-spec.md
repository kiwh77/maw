# maw 蓝图字段速查（配合 maw-plan-push）

## [meta] 字段

| 字段 | 必填 | 说明 |
|---|---|---|
| `name` | 建议 | 蓝图短名 |
| `version` | 建议 | 递增整数 |
| `main_branch` | **新建/刷新波次必填**（≥0.3.9） | 本波合入与 worktree 基线；`plan apply` 同步到 config 与全局库。缺省则不改现有 `project.main_branch`（兼容旧蓝图） |

## [[task]] 字段

| 字段 | 必填 | 说明 |
|---|---|---|
| `id` | 是 | 稳定 ID，如 `T-061`；全蓝图唯一 |
| `title` | 是 | 短标题，看板展示 |
| `priority` | 建议 | 整数，**越小越先**；默认缺失时按 100 |
| `merge_order` | 建议 | 合流/展示序；缺省回落为 priority |
| `deps` | 否 | 前置任务 id 列表；未完成则不可 ready |
| `write_scope` | 是 | 允许改动的路径 glob（`/**` 前缀匹配） |
| `read_scope` | 否 | 只读参考，不参与写盘冲突 |
| `acceptance` | 建议 | 字符串数组，完成定义 |

## 调度（maw ≥ 0.3.1）

激活/领取条件（`internal/schedule`）：

1. 所有 `deps` 状态为 `done`
2. 不与任何 **ActiveOwner** 状态任务写盘重叠
3. 不与任何**未完成且排序更靠前**的任务写盘重叠  
   排序键：`priority` ASC → `merge_order` ASC → `id` ASC

因此：**同盘后续题可以进同一蓝图**。

## 推荐 priority 带宽

| 带 | 用途 |
|---|---|
| 10 | 当前可并行开工 |
| 20 | 紧随其后 / 轻依赖 |
| 30+ | 同盘后续或深管道（AI 迁出等） |
| 步长 10 | 便于插入题 |

## apply 行为（maw ≥ 0.3.2）

- **默认增量合并**（不再清空历史）：
  - 保留 `done` / `cancelled` / `blocked` / 进行中（ActiveOwner）任务与状态
  - 蓝图新题 → `backlog` 再 `promoteReady`
  - 蓝图内 idle（`backlog`/`ready`）→ 按蓝图覆盖并置回 `backlog`
  - 蓝图外 idle → 删除；蓝图外保留态 → 不动
- **`--reset`**：才清空该工程全部任务后再灌入（显式销毁历史）
- 工程配置 `approvals.plan = "manual"` 时必须 `--yes`

## 最小可并行示例

```toml
[[task]]
id = "T-A"
title = "模块 A"
priority = 10
write_scope = ["mod_a/**"]
acceptance = ["ok"]
merge_order = 10

[[task]]
id = "T-B"
title = "模块 B"
priority = 10
write_scope = ["mod_b/**"]
acceptance = ["ok"]
merge_order = 20

[[task]]
id = "T-A2"
title = "模块 A 后续"
priority = 20
deps = ["T-A"]
write_scope = ["mod_a/**"]
acceptance = ["ok"]
merge_order = 30
```

预期 ready：`T-A` ‖ `T-B`；`T-A2` backlog 至 `T-A` done。
