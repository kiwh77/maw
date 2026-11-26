# 迭代蓝图契约 v1（冻结）

契约标识：`maw.iteration.blueprint/v1`
状态：**冻结**。IT-002 起以本文件为蓝图解析基线；改含义只能出 v2。

范围：蓝图 TOML 中的 `[[iteration]]` 与 `task.iteration`，以及它们解析后的模型。
**不描述**数据库表、迭代分支、收尾合回或 CLI 展示；这些分别由后续卡承接。

## 1. 输入形态

迭代在蓝图里是一组数组表，任务用 `iteration` 引用迭代 id：

```toml
[meta]
name = "batch"
main_branch = "main"

[[iteration]]
id = "IT-001"
name = "第一批交付"
goal = "现状：任务散落无批次。目标：统一归属并作为合入单位。"
target_branch = "main"
status = "planning"
deps = []

[[task]]
id = "T-001"
title = "更新 README"
iteration = "IT-001"
write_scope = ["README.md"]
acceptance = ["README 含安装命令"]
verify = ["manual: 按 README 安装后 maw --version 可运行"]
```

没有 `[[iteration]]` 的旧蓝图继续合法；此时任务 `iteration` 必须留空。

## 2. 字段

### 2.1 `[[iteration]]`

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `id` | 是 | 工程内唯一的迭代 id；任务用它引用 |
| `name` | 是 | 人读名称 |
| `goal` | 否 | 现状→目标式说明 |
| `target_branch` | 否 | 收尾时合回的分支；留空继承 `meta.main_branch` |
| `status` | 否 | 留空为 `planning`；合法值见 §3 |
| `deps` | 否 | 迭代间依赖，只能引用本蓝图里声明的迭代 id |

### 2.2 `task.iteration`

- 空串表示「不属于任何迭代」。
- 非空时必须是本蓝图 `[[iteration]].id` 之一；否则 `plan check` 失败。

## 3. 状态

规范值（写库使用）：`planning` / `running` / `finishing` / `merged` / `abandoned`。
解析时同时接受中文标签：`规划中` / `执行中` / `收尾中` / `已合入` / `已放弃`，并归一为规范值。

## 4. 不变量

| 编号 | 规则 | 违反结果 |
| --- | --- | --- |
| I100 | 迭代 `id`、`name` 非空 | 校验错误 |
| I101 | 迭代 `id` 不重复 | 校验错误 |
| I102 | `status` 属于 §3 集合 | 校验错误 |
| I103 | `target_branch` 不含空白 | 校验错误 |
| I104 | `task.iteration` 指向已声明迭代 | 校验错误（未知引用） |
| I105 | 迭代 `deps` 指向已声明迭代 | 校验错误 |
| I106 | 迭代依赖无环 | 校验错误 |

## 5. 解析后的模型

- 任务：`db.Task.IterationID` 取 `task.iteration`；空值保持空串。
- 迭代：`db.Iteration{ID, Name, Goal, Status, TargetBranch}`；`TargetBranch` 空值已继承 `meta.main_branch`，`Status` 已归一；`deps` 保留在 `Extra["deps"]`。
- 调用方（IT-003）负责补 `ProjectID` 与时间戳后写库。
