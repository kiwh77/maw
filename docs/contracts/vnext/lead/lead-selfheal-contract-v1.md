# lead 自愈与系统统计契约 v1

契约标识：`maw.lead.selfheal/v1`
上游契约：`maw.runtime.state/v1`（`../execution/runtime-state-contract-v1.md`）——Run 阶段、核实状态与资源占用语义以它为准，本文件只补「系统层如何发现、核实、收尾」。
状态：**v1 草案（本卡定稿口径）**。SH-302…SH-503 以它为引用基线；改含义出 v2。

范围：系统级信号的判据与证据源、证据到动作的映射、`system_jobs` 的 kind 与状态机、`run_reconcile` 的释放判据、系统健康指标口径。**不写业务代码，不建表，不改现有实现。**

## 0. 为什么需要这份契约

现状的 lead 层零件多、口径散：

1. **信号与动作没有一张表。** `internal/systemagent/signals.go` 的 4 类信号、`decision.go` 的 6 个动作、`orchestrator.go` 的预分类升级路径，三者关系只存在于注释里；新增一类信号要重新推导「会不会真改状态」。
2. **「默认只观察」没有可验收的口径。** 它靠 `auto_kinds` 配置 + 代码预分类双保险，但没有写成规则，评审时无法逐条核对。
3. **Run 核实结论与资源释放没有对照。** `internal/runner/recovery/reconcile.go` 给出 `Evidence`（`KeepsResources` / `Reusable`），却没有任何生产调用方；哪份证据能放资源、放哪些资源、留什么事件，无据可查。
4. **系统层零统计。** `system_jobs`、`operations`、`runs` 不进 analytics；即使要算，也没有「分母是什么、没有数据时填什么」的定义，必然出现把 unknown 写成 0 的报表。

本契约把四件事拆开写：**观察（信号）→ 处置意图（动作/作业）→ 核实（证据）→ 计量（指标）**，每层可单独验收。

## 1. 三层边界

| 层 | 负责 | 不负责 | 代码位置 |
| --- | --- | --- | --- |
| lead（系统层） | 发现卡死、核实现场、收尾、调度、统计 | 具体任务实现；杀死未知归属的进程 | `internal/systemagent`、`internal/app/lead_*` |
| 执行节点 | 领取并执行一道题 | 判断自己该不该被回收 | `internal/app/runtime.go`、`claim` |
| 人 | 打开开关、放行 `auto_kinds`、处置 escalated | 不允许绕过 `maw merge` 改主干 | 配置与看板 |

lead 是**本机能力标记**（`[identity] leads`），不是新角色体系：不改 `agents.role` 的三值集合，不新增云端身份，不参与领题。

## 2. 信号目录

信号回答「观察到了什么」，`system_jobs.kind` 回答「要入什么作业」，两者是两套概念（`signals.go:28-30`）。

所有信号由 `SignalFacts` 注入证据、`DetectSystemSignals` 纯函数判定；`Signal.Key()` 是去重与冷却键（`signals.go:60-66`）。

| 信号 kind | 判据 | 证据源（SignalFacts / 表） | 默认动作 | 需 `auto_kinds` 放行 | 引入卡 |
| --- | --- | --- | --- | --- | --- |
| `gate_env_failure` | 最近一次 submit 门禁失败命中环境失败词表（`gitops.IsEnvFailure` + `/bin/ps`、`operation not permitted`、`EPERM`） | `RecentGateFailures()` ← `events` 的 `gate_env_failed` / `gate_failed`（最近 30 分钟） | 入队 `unblock_assist`，`decision.action=escalate_human`，终态 Escalated | 需 `auto_kinds` 才入队（默认已含 `unblock_assist`）；但预分类保证**放行与否都不改状态** | 已实现 |
| `all_agents_offline` | 工程内不存在「状态非 offline 且 `last_seen` 在 15 分钟窗口内」的 Agent | `Projects()` + `AgentHeartbeats()` ← `agents.last_seen/status` | 入队 `dispatch_ready` 仅作观察，预分类 `escalate_human` + `observation=true` | **是**（默认未放行；即便放行也因预分类不改任务状态） | 已实现 |
| `merging_stalled` | 任务停在 `merging` 超过 30 分钟且未推进 | `MergingTasks()` ← `tasks.status='merging'` 与 `tasks.heartbeat`（`Since` 为零值不判定） | 入队 `ops_run`，`ops_command` 必须在 `ops_allowlist` 内 | **是 + 命令白名单**（双门禁，缺一不产出信号） | 已实现 |
| `stale_lock` | 锁/租约过期超过 30 分钟，且占用者为空或身份不明 | `HeldLocks()` ← `db.ListActiveLeases`（活跃租约，占用者按「证据不足」处理） | **只上报**（`JobKind` 为空，不产 job） | 否（无作业可放行） | 已实现 |
| `run_unverified` | Run 处于 `unknown`，或非终态但心跳缺失超过阈值，且未在本轮核实过 | `UnverifiedRuns()` ← `runs.reconciliation_state/updated_at`（新增） | 入队 `run_reconcile`，只按 `ReconcilePort` 结论记结果 | **是**（迁移放行 CHECK，默认不放行 ⇒ 只入队观察） | SH-302 / SH-303 |
| `dispatch_op_stalled` | `operations` 存在 `result=pending` 且 `lease_expires_at` 已过期超过 30 分钟 | `StalledOperations()` ← `operations.created_at/lease_expires_at/result` | **只上报**（`JobKind` 为空，不代执行） | 否 | SH-304 |
| `claimed_not_started` | 任务 `in_progress` 且 `heartbeat` 为空/过期超过 15 分钟，同时无对应 Run 或 Run 非 `running` | `ClaimedNotStarted()` ← `tasks.heartbeat` + `runs` | **只上报**（不自动回 ready） | 否 | SH-304 |
| `dep_deadlock` | 工程内存在依赖环，或全部 ready 题都被未完成依赖堵住超过阈值 | `DependencyState()` ← `tasks.deps_json`/`status` | **只上报**（不自动改 deps） | 否 | SH-304 |
| `no_capable_node` | 存在 ready 题，但按 matching 规则无任何可用节点（能力/槽位/工程路径都不匹配） | `MatchedCapacity()` ← `nodes` + `matching.Match` | **只上报**（不自动放宽匹配） | 否 | SH-304 |
| `repeated_failure` | 同一 task/instance 在窗口内 `failed` 的 Run 次数 ≥ `max_attempts` | `RecentFailures()` ← `runs.phase_history_json`/`phase` | **只上报**（不自动重派） | 否 | SH-304 |

判定窗口（`signals.go:22-26`）当前是包级常量：`agentHeartbeatWindow=15m`、`mergingStallThreshold=30m`、`staleLockAge=30m`。契约固定这三个默认值；改动窗口只改常量，不改判据结构。

## 3. 证据 → 动作映射

动作集合是封闭的（`internal/systemagent/decision.go:12-21`）：

| 动作 | 含义 | 是否改任务/Run 状态 | 谁能产出 |
| --- | --- | --- | --- |
| `notify_only` | 只通知 | 否 | 规则层/LLM |
| `escalate_human` | 升级人工 | 否（终态 Escalated） | 规则层/预分类 |
| `unblock_assist` | 协助解阻 | 只写 `handoffs/` 提案，不直接改题 | 编排器 |
| `split_tasks` | 拆题提案 | 只落待审提案 | 编排器 |
| `ops_run` | 执行白名单命令 | 由命令自身决定，作业只记结果 | 编排器 |
| `relaunch_worker` | 重拉 worker | 是（受 `worker_node` 约束） | LLM |
| `dispatch_ready` | 选题并派发 | 是（Match → Claim → Run） | 编排器（系统内动作，不进 LLM 空间） |

### 3.1 「默认只观察」的代码层保证

| 路径 | 是否真派发/改状态 | 依据 |
| --- | --- | --- |
| `EnqueueSignals(explicit=false)` | **否** | `KindAllowed` 先按 `auto_kinds` 过滤；信号侧预置 `action=escalate_human` 时 `Orchestrator.Step` 在 switch kind 前直接 finish 为 Escalated（`orchestrator.go:190-192`） |
| `maw lead dispatch` / `maw system trigger`（`explicit=true`） | 是 | 显式入口绕过白名单，由 `RequireLead` 权限门把守 |
| `stale_lock` / SH-304 五类 | 否 | `JobKind` 为空，`EnqueueSignals` 第一步即跳过（`signals.go:309-311`） |

因此不放行 `auto_kinds` 是默认态；**任何「自动处置」都必须同时满足：kind 在 `auto_kinds` 内、信号未预分类升级、命令在 `ops_allowlist` 内**。

## 4. `system_jobs`：kind、状态机与租约

### 4.1 kind 封闭集合（目标 v11）

| kind | 用途 | 现状 |
| --- | --- | --- |
| `unblock_assist` | 阻塞协助/环境失败升级 | 已有 |
| `task_split` | 拆题提案 | 已有 |
| `ops_run` | 白名单运维命令 | 已有 |
| `dispatch_ready` | 选题派发 | v10 已放行 |
| `run_reconcile` | 核实受管 Run 并收尾 | **本契约新增；SH-302 迁移重建 CHECK** |

### 4.2 状态机

```text
queued -> running -> done | failed | escalated
queued -> cancelled
running -> queued   （租约过期回收，result.reaped_from=running）
```

- `escalated` / `failed` / `done` / `cancelled` 为终态，不再出发转移；重试一律新建作业或回收为 `queued`（`attempts` 保留）。
- `running` 的租约默认 10 分钟（`locked_at`）；超时回收为 `queued`，`result_json` 写 `reaped_from=running`、`reaped_at`；回收后 `attempts` 不变。
- 回收时 `attempts >= max_attempts` 直接落 `failed`，不回到队列。
- 重申领必须等退避窗口：`30s · 2^(attempts-1)`，上限 10 分钟；窗口内即使 `queued` 也不得被领取。
- 终态必须写 `finished_at`；`decision_json` 至少保留 `signal_key`、`signal_kind`（来自信号）与 `action`。

### 4.3 `run_reconcile` 的结果键

| result 键 | 取值 | 含义 |
| --- | --- | --- |
| `reconcile_state` | `running` / `finished` / `not_started` / `unknown` | 核实结论（与 `recovery.State` 同值域） |
| `mode` | `release` / `keep` / `escalate` / `unwired` | 本作业实际动作 |
| `evidence_reason` | 字符串 | 直接取 `Evidence.Reason`，看板可读 |
| `resources_released` | 数组，元素 ∈ `node_slot`/`workspace`/`lease` | 仅在 release 时非空 |
| `journal_pending` | 整数 | `Evidence.Journal.PendingRecords` |
| `rechecked_at` | RFC3339 | 核实时间；keep 也要写 |

端口为 nil（未接线）时作业终态 `escalated` 且 `mode=unwired`，**不修改任何资源**。

## 5. `run_reconcile` 释放判据与 `recovery.Evidence` 对照

`recovery.Evidence` 的两个方法就是契约的全部判据（`internal/runner/recovery/reconcile.go:59-71`）：

- `KeepsResources() = State ∈ {running, unknown}`
- `Reusable() = !KeepsResources() && !Journal.Incomplete`
- `BlocksRerun() = KeepsResources()`

一一对应表：

| `Evidence.State` | `KeepsResources` | `Reusable` | `BlocksRerun` | `Journal.Incomplete` | 契约结论 `mode` | 资源与事件 |
| --- | --- | --- | --- | --- | --- | --- |
| `running` | true | false | true | 任意 | `keep` | 不退租约、不换目录、不重派；写 `run.reconcile_keep`，`rechecked_at` 更新 |
| `unknown` | true | false | true | 任意 | `keep`（信号预分类为 escalate） | 同上；写 `run.reconcile_unknown`，**不落终态**（`runtime.state/v1` E232） |
| `finished` | false | true | false | false | `release` | 单事务：Run 落 `completed`（按结果）→ 释放 `lease`/`node_slot` → 任务回 `ready`（或 `blocked` 带回 blocked_reason）→ 写 `run_reconciled`（release）与 `lease.released` |
| `finished` | false | false | false | true | `keep`（待补交） | 不释放；先补交日志（`PendingReplay`），补交后重跑一次核实转 release |
| `not_started` | false | true | false | false | `release` | 单事务：Run 落 `failed`（`never_started`）→ 释放 `lease`/`node_slot` → 任务回 `ready` → 写 `run_reconciled`（release） |
| `not_started` | false | false | false | true | `keep`（待补交） | 同 finished 待补交分支；补交完成后转 release |

判定规则只有一条，调用方不得自行按 `State` 分支：

```text
release  ⟺ Reusable() == true
keep     ⟺ KeepsResources() == true 或 Journal.Incomplete == true
```

补充约束：

1. **`unknown` 不自动释放**：即 `KeepsResources()` 为真时任何配置、任何 `auto_kinds` 组合都不放资源；只能由人显式 `maw run release` 之类入口处置（不在本契约内定义）。
2. **不杀进程**：`running` 只保留占用；回收动作永远不尝试 kill 未归属的进程组。
3. **释放是条件写入**：携带 `generation` 与 `release_generation` 校验（上游 E243），旧代际释放被拒。
4. **同事务**：状态、租约、任务回写与事件必须一批提交；任一写失败整体回滚（`internal/runner/dbstore/observe.go` 的事务边界）。
5. **不自动重派**：release 只把任务回到可再领状态，不产生新的 dispatch operation。

## 6. 系统健康指标

时间窗一律 **半开区间 `[start, end)`**，时间存 UTC；展示按 `timezone`。所有比率在**分母为 0** 或**证据缺口**时返回 `null` + `quality=unknown`，**不得填 0**（与 `maw.observability.timeline/v1` §4.3 同源）。每个指标都必须能标出数据源表与分母。

| # | 指标 key | 定义与公式 | 分母 | 未知值处理 |
| --- | --- | --- | --- | --- |
| 1 | `system_job_total` | `count(system_jobs)`，按 kind 分桶 | 无（计数值） | 表为空 ⇒ `null`（表示「未采集」而不是 0） |
| 2 | `system_job_success_rate` | `done / (done + failed + escalated)` | 已结束作业数（`cancelled` 与 `queued/running` 不计入） | 分母 0 ⇒ `null`；含 `escalated` 时另给 `success_rate_strict = done/(done+failed)` 供对比 |
| 3 | `system_job_backlog` | `queued` 计数与最久 `now - created_at` | 无（两项计数） | `queued=0` 时最长等待 `null` |
| 4 | `system_job_retry_rate` | `count(attempts > 0) / count(lock 过即 status∈{running,done,failed,escalated})` | 被领取过的作业数 | 分母 0 ⇒ `null` |
| 5 | `lease_reclaim_rate` | `count(result.reaped_from='running') / count(曾进入 running)` | 曾领取的作业数 | `system_jobs` 无回收事件字段时整项 `unknown`，不按 0 处理 |
| 6 | `run_reconcile_rate` | `count(mode=release) / count(mode ∈ {release, keep, escalate})` | 已核实 Run 数（`run_reconcile` 结束作业数） | 分母 0 ⇒ `null` |
| 7 | `run_reconcile_mttr_seconds` | 对 `mode=release` 的 Run：`finished_at - created_at` 的 p50/p95 | 参与统计的 release 数 | 样本 < 3 ⇒ 只给均值并标 `quality=unknown`；缺 `finished_at` 的样本剔除但计入 `gaps` |
| 8 | `dispatch_queue_seconds` | `run.created_at - job.created_at` 的 p50/p95 | 由 `dispatch_ready` 作业成功建出的 Run | 无 Run 时 `null`；作业与 Run 无法一一关联时整项 `unknown` |
| 9 | `claim_to_start_seconds` | `run.started_at - run.created_at` 的 p50/p95 | `started_at` 非空的 Run | `started_at` 缺失 ⇒ 该样本不计，整项不足 3 样本标 unknown |
| 10 | `failure_reason_distribution` | 按 `system_jobs.result_json.reason`（缺失时 `decision_json.action`）分桶计数与占比 | 终态 failed + escalated | 无 reason ⇒ 单独桶 `unspecified`，不得并入任何具名原因 |
| 11 | `node_success_rate` | 按节点：`count(run.phase=completed) / count(run.phase ∈ 终态)` | 该节点已结束 Run 数 | 节点无已结束 Run ⇒ 该节点 `null`，不参与全局均值 |
| 12 | `agent_heartbeat_freshness` | `count(last_seen 在窗口内) / count(已登记 agents)` | 工程内登记的 Agent 数 | 分母 0 ⇒ `null`；窗口默认 15 分钟，与 `agentHeartbeatWindow` 同值 |

报表必须同时携带：`generated_at`、`window {start,end}`、每项指标的 `numerator/denominator`、`quality ∈ {observed, inferred, unknown}`、以及 `sources`（实际查询的表）。缺任一字段视为不可比较。

### 6.1 口径不变量

- `system_job_success_rate` 的分母**不含** `cancelled`：人工取消不是自愈失败。
- 回收类指标（#5）必须能从 `result_json` 还原；只有 `attempts` 增加而无回收标记时，报 `unknown`，不得把普通重试算成回收。
- 任何「平均/中位数」在样本不足时降级为 `quality=unknown`，不静默给出看起来完整的数字。
- 同上，报表中 `null ≠ 0`：`0` 表示「确实一次都没有」，`null` 表示「没有数据/无法计量」。

## 7. 不变量登记表

| 码 | 判定对象 | 不变量 |
| --- | --- | --- |
| L200 | 信号目录 | 每类信号必须有判据、证据源、默认动作、`auto_kinds` 放行要求四列；缺一列不得实现 |
| L201 | 信号去重 | 同一 `Signal.Key()` 在 `cooldown_sec` 内至多入队一次（`WithinCooldown`） |
| L202 | 默认动作 | `JobKind` 为空的信号不得产出作业；`EnqueueSignals` 必须先跳过再谈冷却 |
| L210 | 动作集合 | 只能是 §3 的 7 个动作；新增动作必须同时补「是否改状态」一列 |
| L211 | 预分类 | `decision.action=escalate_human` 时终态必须是 `escalated`，不得进入 `runDispatchJob`/`runOpsJob` |
| L212 | 显式 vs 隐式 | `explicit=false` 只信 `auto_kinds`；`explicit=true` 才允许绕过，且必须过 `RequireLead` |
| L220 | kind | 只能取 §4.1 封闭集合；新增 kind 必须同时迁移 CHECK 与 i18n 文案 |
| L221 | 租约 | `running` 超过 10 分钟必须可回收为 `queued`，并写 `reaped_from=running` |
| L222 | 退避 | 退避窗口内不得重申领；窗口公式 `30s·2^(attempts-1)`，上限 10 分钟 |
| L223 | 次数 | `attempts >= max_attempts` 的回收必须落 `failed`，不得回 `queued` |
| L230 | 释放判据 | `release ⟺ Evidence.Reusable()`；`keep ⟺ KeepsResources() 或 Journal.Incomplete` |
| L231 | unknown | `reconciliation_state=unknown` 时不得释放 `node_slot`/`workspace`/`lease`，也不得落终态 |
| L232 | 代际 | 释放必须携带且匹配 `generation`（上游 E243） |
| L233 | 事务与事件 | 释放必须与 `run_reconciled` + `lease.released` 同事务提交 |
| L234 | 不重派 | release 不得自动产生新的 dispatch operation |
| L235 | 不杀进程 | 核实与回收链路不得 kill 任何未确认归属的进程组 |
| L240 | 指标分母 | 分母为 0 时返回 `null`，不得填 0；`cancelled` 不计入成功率分母 |
| L241 | 时间窗 | 所有指标按半开区间 `[start, end)` 统计，`end` 不闭合 |
| L242 | 未知降级 | 样本不足或证据缺口时 `quality=unknown`，不得补均值、不得从分母剔除后假装完整 |
| L243 | 可比较性 | 报表缺 `generated_at`/`window`/`quality`/`sources` 任一项视为不可比较 |

## 8. 字段对照（契约名 ↔ 代码符号）

评审时按下表核对命名，禁止发明同义字段：

| 契约名 | 代码符号 / 位置 | 备注 |
| --- | --- | --- |
| `SignalKind` | `internal/systemagent/signals.go:30` | 十类取值见 §2 |
| `Signal.Key()` | `signals.go:60` | 冷却/去重键 |
| `Signal.Escalate` | `signals.go:56` | 对应 `escalate_patterns` 命中 |
| `SignalFacts` | `signals.go:114-125` | 新增证据方法只加接口，不改 `DetectSystemSignals` 结构 |
| `Action` 七值 | `internal/systemagent/decision.go:12-21` | `dispatch_ready` 只由编排器产出 |
| `KindAllowed` | `internal/systemagent/classifier.go:29` | explicit 白名单与 `auto_kinds` 两套 |
| `system_jobs.kind/status` | `internal/db/migrations.go:363-390` | 目标 v11 放行 `run_reconcile` |
| `Evidence.State` | `internal/runner/recovery/reconcile.go:17-29` | 四值与 `reconcile_state` 同域 |
| `Evidence.KeepsResources()` | `reconcile.go:59` | 唯一占用判据 |
| `Evidence.Reusable()` | `reconcile.go:69` | 唯一释放判据 |
| `Evidence.BlocksRerun()` | `reconcile.go:66` | 与 `KeepsResources` 等价 |
| `Evidence.Journal.PendingRecords` | `reconcile.go:47` + `journal.Status` | 决定「待补交」分支 |
| `TaskStatus` 回写 | `tasks.status` | release 回 `ready` 或 `blocked` |
| `ReconcilePort` | SH-302/303 新增（`systemagent` 端口） | 端口 nil ⇒ `mode=unwired`，不改资源 |

## 9. 开关与回退（默认全关）

| 开关 | 默认 | 打开后发生什么 | 回退 |
| --- | --- | --- | --- |
| `[system_agent] enabled` | `false` | Watcher 常驻巡检开始跑 | 改回 false 并重启 serve |
| `auto_kinds` | `["unblock_assist"]` | 仅列出的 kind 自动入队 | 删除新增项即可；已入队作业不会被追改 |
| `[managed] auto_enabled` | `false` | leader 消费派发操作 | 改回 false |
| `auto_select`（SH-401 新增） | `false` | leader 才自动选题 | 改回 false；已建 operation 由人处置 |
| `ops_allowlist` | 4 条 | `ops_run` 只能执行前缀命中的命令 | 删条目 |
| `cooldown_sec` / `max_attempts` | 300 / 3 | 信号去重窗口 / 作业重试上限 | 原值可随时改回 |

任何打开动作都必须留下审计：`system_agent` 作业自身的 `decision_json` + `events`，不另起日志。

## 10. 本契约不做的事

- 不实现信号检测、作业执行、核实端口与统计代码（分别由 SH-302…SH-503 落地）。
- 不定义 `maw run release` 等人工处置命令的参数（只规定「unknown 不自动释放」）。
- 不新增云端身份、不改 `agents.role`、不绕过 `write_scope`。
- 不规定进程探测的具体实现（PID 复用、进程组清空由 `process` 包负责）。
- 不改变 `auto_enabled` 语义，也不把任何开关默认打开。

## 11. 评审入口（本卡 Verify 为人工评审）

```sh
# 字段名核对：契约 §8 与以下两处不得出现同义不同名
sed -n '28,70p;110,130p' internal/systemagent/signals.go
sed -n '17,90p' internal/runner/recovery/reconcile.go
# 动作与 kind 核对
sed -n '12,21p' internal/systemagent/decision.go
sed -n '363,390p' internal/db/migrations.go
```

评审结论记入提交说明：逐项确认 §2 每行四列齐全、§5 六行与 `Evidence` 方法值一致、§6 十二项指标都有分母与未知值处理、§7 不变量与上游契约不冲突。
