# 任务卡 ↔ maw 蓝图字段映射

对照权威口径：[`docs/zh/TASK-SPEC.md`](../../docs/zh/TASK-SPEC.md)（L1 必需六项）。maw ≥ 0.4.0 / protocol 7。

| 任务卡（清单 md） | blueprint.toml | 说明 |
|---|---|---|
| `T-0xx` 标题中的 ID | `id` | 稳定、唯一；**L1 必需** |
| 短标题（`###` 行） | `title` | 动词开头可交付结果；与 Goal **分离**；**L1** |
| Goal（`现状：…。目标：…`） | `goal` | **L1**；解析后进 `task.extra`，`maw next --json` 下发 |
| Depends | `deps` | 数组；拼成 `depends` 会被**静默丢弃** |
| Surface | `write_scope` | 同路径 glob；**L1** |
| Read-scope / Contract 路径 | `read_scope` | 契约 md、端口定义等；实现题强制 ≥1；正文 Steps/Contract 仍留 md |
| Acceptance（原 Deliverables 语义） | `acceptance` | **L1 必需**；≥1 条可判定断言（带观察点） |
| Verify | `verify` | **L1 必需**；独立字符串数组；命令或 `manual: …`；**禁止**与 acceptance 合并 |
| Non-goals | `not_doing` | 字符串数组；进 `task.extra`，next 下发 |
| Steps / Contract 正文 | （不进蓝图） | 留在清单；用 `read_scope` 指路 |
| maw.priority / 波次建议 | `priority` | 越小越先；同盘后续加大 |
| 合流顺序建议 | `merge_order` | 步长 10 |
| Lane（上下文簇） | （不进蓝图；靠 `deps` + 同前缀 `write_scope` + 成带 `priority` 体现） | 供 next 亲和与人读台账 |

## 历史坑（TASK-SPEC §2.4）

把 `Verify: go test …` 塞进 `acceptance` 字符串 → 机器提取不出独立 `verify` → `plan apply` 报 `verify required`。  
清单与蓝图必须 **Acceptance / Verify 两行分开**。

## Surface → write_scope 写法

| 清单 Surface | write_scope |
|---|---|
| `backend/internal/foo/**` | `["backend/internal/foo/**"]` |
| 单文件 `Makefile` | `["Makefile"]` |
| 多点 | `["a/**", "b/file.go"]` |

避免：`backend/**`（过大）、`*`（无前缀匹配意义）。

## 首波并行估算

设计划同时开工 worker 数 = P：

- 令 S =「Depends 为空（或仅依赖已 done）且两两 Surface 不重叠」的题集合  
- 健康：`|S| >= P`  
- 若 `|S| < 2` 而总题很多：检查是否错误地把可并行竖井都串进同一 Depends 链  
- 领取不再受 config `max_parallel` 卡槽（≥0.3.6）
- `plan check` 的 `max_parallel_theory` **只按 deps**，不检测 `a/**` 与 `a/foo.go` 重叠——重叠靠人审

## 写完必看（静默丢弃）

**未知字段 / 拼错键名会被 TOML 解析静默丢弃**（例：`depends` 而非 `deps` → 边丢失 → `deps_depth=0`、两题同时 ready）。  
压入后务必核对 `maw plan check` 输出的 **deps_depth / layer_widths / 无 deps 入口数**，并与清单 Lane 表对照。
