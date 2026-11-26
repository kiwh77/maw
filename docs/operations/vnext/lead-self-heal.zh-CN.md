# Lead 自愈与受管执行的启用说明

本页是 SH-302…SH-503 落地后的操作入口。契约口径见
[lead-selfheal-contract-v1.md](../../contracts/vnext/lead/lead-selfheal-contract-v1.md)，
本文只讲「怎么开、开了会发生什么、怎么回退」。

**默认全关**：不打开任何开关时，maw 只做人工领取与人工合入，不自动派发、不自动回收。

## 1. 启用步骤

1. 确认 `serve` 或 `board --serve` 已在本机运行（leader 循环只在这里拉起）。
2. 按需打开开关（见第 2 节），先只开一个，确认没有非预期动作后再叠加。
3. 用只读入口核对配置生效：

```bash
maw system status      # enabled / dispatch_enabled / managed_execution_limit / queue
maw lead signals       # 本轮观察到的信号与原因码（不改任何状态）
```

4. 观察一个轮询周期（`poll_interval_sec`，默认 5 秒）后再决定是否放行 `auto_kinds`。

最小可用配置（保持「只观察」）：

```toml
[system_agent]
enabled = true
poll_interval_sec = 5
auto_kinds = ["unblock_assist"]   # 不追加任何新 kind 即保持默认
```

## 2. 开关含义

四个开关各自驱动一段独立逻辑，**互不推导**。只看一个容易误判「为什么没有自动跑」。

| 开关 | 默认 | 打开后发生什么 | 不打开时 |
| --- | --- | --- | --- |
| `[system_agent] enabled` | `false` | Watcher 常驻巡检：回收失联作业、检测系统信号、按 `auto_kinds` 入队 | 信号不检测，`system_jobs` 不入队，`maw system watch` 直接报错 |
| `[managed] auto_enabled` | `false` | leader 消费本工程的派发操作（受 `managed_execution_limit` 与平台能力约束） | 派发操作留在队列里，任务停在可领取状态 |
| `[managed] auto_select` | `false` | leader 在无待消费操作时，按匹配规则自动选题建操作 | 只消费已有操作，不自己找题 |
| `[managed] managed_execution_limit` | `1` | 单工程同时允许的受管执行数上限；`0` 或负值视为关闭 | 匹配判定为不可执行 |

补充口径：

- `auto_enabled` 为真但 `managed_execution_limit < 1` 或平台不支持受管启动时，匹配结果仍按「不可执行」处理，不会偷偷放宽。
- `auto_kinds` 是**放行名单**，不是「检测名单」：不在名单内的 kind 连入队都不会发生；放行后仍受「信号预分类升级」约束。
- 显式入口（`maw system trigger`、`maw lead dispatch`）绕开放行名单，但需要 lead 权限，且不属于「自动」。

## 3. 五类队列卡死信号

这五类信号都**只上报**：`JobKind` 为空，不产出作业，不改任务／Run／节点状态。
它们出现在 `maw lead signals` 与看板信号列表里，供人判断要不要介入。

| 信号 | 判据 | 适用场景 |
| --- | --- | --- |
| `dispatch_op_stalled` | 存在 `result=pending` 的操作，且租约或创建时刻已过期超过 30 分钟 | 派发操作被领取后没有推进，队列里留了个僵尸 op |
| `claimed_not_started` | 任务 `in_progress` 但心跳为空/过期，且没有 Run 或 Run 不在 `running` | 有人领了题却没起来，题被占着无法被他人领取 |
| `dep_deadlock` | 工程内存在依赖环，或全部 ready 题都被未完成依赖堵住 | 蓝图写错依赖导致的整队停摆 |
| `no_capable_node` | 有 ready 题，但按匹配规则无任何节点可用（能力／槽位／工程路径任一不匹配） | 处于「有题没人能跑」，与自动派发开关无关 |
| `repeated_failure` | 同一 task/instance 在 30 分钟窗口内 `failed` 的 Run 次数达到 `max_attempts` | 反复失败但还在被重派，需要人看日志 |

时间窗口是包级常量（30 分钟），改窗口只改常量，不改判据结构。

## 4. 回退方式

| 想回退什么 | 怎么做 | 影响 |
| --- | --- | --- |
| 停止自动巡检 | `[system_agent] enabled = false`，重启 serve | 后续不再检测信号；已入队作业保留，可按第 5 节处理 |
| 停止自动派发 | `[managed] auto_enabled = false` | 已建的操作不自动消费；执行中的 Run 不被中断 |
| 停止自动选题 | `auto_select = false` | 只保留「消费已有操作」能力 |
| 撤销某类自动处置 | 从 `auto_kinds` 删掉对应 kind | 已入队作业不会被追改，仍需人工处置 |
| 阻止某条运维命令 | 从 `ops_allowlist` 删掉对应前缀 | 新作业在编排时即被拒，不会执行 |

回退不需要清库，也不需要删 `system_jobs`；配置改回即生效，重启 serve 后旧配置不再参与判定。

## 5. 安全边界

1. **只上报不等于会自愈**：第 3 节五类信号永远不改状态。若要处置，走人工命令或显式 trigger。
2. **释放有唯一判据**：`run_reconcile` 的 release 等价于 `Evidence.Reusable()`；`unknown` 状态在任何开关组合下都不释放节点槽、workspace 或租约。
3. **不杀进程**：核实与回收链路不 kill 任何未确认归属的进程组；失联作业只回收租约。
4. **跨工程隔离**：CLI 自建协调者的队列绑定本工程；一个工程的自动派发不会消费另一个工程排队的操作。
5. **不自动重派**：release 只把任务回到可再领状态，不产生新的派发操作；重派仍由 `auto_select`/人工决定。
6. **审计**：每次开关打开后的动作都留在 `system_jobs.decision_json` 与 `events`，不另起日志系统。

## 6. 验收与回归

```bash
# 场景回归（崩溃回收／跨工程隔离／reconcile 释放与保留／离线过期／自动选题只跑一次）
CGO_ENABLED=0 LANG=en_US.UTF-8 go test ./tests/resilience/... -count=1
```

`LANG` 需为 `en_US.UTF-8`：`internal/gitops` 的部分断言依赖英文输出。
