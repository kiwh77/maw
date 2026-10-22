# 任务卡 ↔ maw 蓝图字段映射

| 任务卡（清单 md） | blueprint.toml | 说明 |
|---|---|---|
| `T-0xx` 标题中的 ID | `id` | 稳定、唯一 |
| Goal / 短标题 | `title` | 看板一行能看懂 |
| Depends | `deps` | 数组 |
| Surface | `write_scope` | 同路径 glob |
| （只读参考） | `read_scope` | 契约 md、端口定义等 |
| Verify / Deliverables | `acceptance` | 可勾选短句 |
| maw.priority / 波次建议 | `priority` | 越小越先；同盘后续加大 |
| 合流顺序建议 | `merge_order` | 步长 10 |
| Non-goals / Steps / Contract | （不进蓝图） | 留在清单给人与 Agent 执行 |
| Lane（上下文簇） | （不进蓝图；靠 `deps` + 同前缀 `write_scope` + 成带 `priority` 体现） | 供 next 亲和与人读台账 |

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
