# maw 看板与受管能力补齐（可执行任务清单）

**主分支：`main`**  
**迭代：`IT-BP-001`（看板与受管能力补齐）**

版本：拆包稿 v1.3，2026-11-04。蓝图 v15；六题同属 `IT-BP-001`。`BP-STA-001` write_scope 含 registry/i18n/view-context/VnextHost/board store/dist。

定案：统计组保留观测四页并另加问题/日志；节点先做通用 local_cli。

## Lane 与题 ID

| Lane | 题 ID | 依赖 | 说明 |
|---|---|---|---|
| NAV | `BP-NAV-001` | — | 侧栏拆除 control；nodes→settings |
| FILTER | `BP-ITF-001` | — | 工作台 iteration 筛选 |
| PROJECT | `BP-PRJ-001` | — | 工程编辑 + project set / PATCH |
| NODE | `BP-NOD-001` | — | local_cli 节点登记 CLI/HTTP |
| DISPATCH | `BP-DSP-001` | `BP-NOD-001` | allowed_nodes → Match；argv→launch_spec |
| STATS | `BP-STA-001` | `BP-NAV-001` | 回顾→统计；problems/logs |

并行入口：`BP-NAV-001` ‖ `BP-ITF-001` ‖ `BP-PRJ-001` ‖ `BP-NOD-001`。筛看板：`?iteration=IT-BP-001`。

## 运维速查

```bash
maw node register --id cli-a --cmd '["/bin/echo","a"]' --slots 1
maw project set goal-agents --auto-enabled=true --allowed-nodes=cli-a --limit=1
maw board --serve
```
