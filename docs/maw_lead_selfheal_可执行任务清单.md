# lead 自愈与系统统计：可执行任务清单

版本：v1.0，2026-11-06。**主分支**：`main`。**迭代**：`IT-SH-001`（lead 自愈与系统统计）。

与 maw 的关系：本清单与 `.multiagent/blueprint.toml` v18 末尾追加的 `IT-SH-001` 十四张卡同构；
领取走 `maw next` → `maw next --claim`，只改各卡 write_scope，完成 `maw submit` → `maw merge --yes`。

---

## 0. 背景：审查结论（2026-11-06 实测）

目标架构是两层：**lead** 负责系统层自愈（发现卡死、核实、收尾、调度、统计），**执行节点**负责具体任务。
当前执行层基本可用；lead 层零件多但没接进常驻循环，统计只覆盖执行层。

| 缺口 | 证据 | 对应卡 |
| --- | --- | --- |
| 主工作区 62 个文件未提交、未走任务卡 | `git status`（节点 CLI 预设、`managed start --model`、`stats_cmd`、工程编辑弹窗、看板多视图） | SH-000 |
| 协调锁按工程隔离，但派发队列查全库 | `runtime.go` 锁在 `runtime/<project>/dispatch/coordinator.lock`；`engine/dispatch/queue.go` `Pending` 无工程过滤；op 目录取当前 serve 工程 | SH-101 |
| serve leader 只覆盖启动 serve 的工程；常驻模式未注入信号证据源；错误只打 stderr | `system_cmd.go` `startServeLoops`：Watcher 未设 `SignalFacts` | SH-102 |
| `system_jobs` 卡 running 无回收；失败重排无退避；`Watcher.Run` 吞错 | 全仓无按 `locked_at` 回收；`watcher.go` 错误分支为空 | SH-201 |
| 过期 Agent 只在 worker 调 `maw next` 时被动检测；写库错误被 `_ =` 忽略 | `heartbeat.go` 唯一调用点 `worker_cmd.go:259` | SH-202 |
| `engine/reconcile`、`runner/recovery` 无生产调用方；受管 Run unknown 后永久占用 | 两包只被测试 import | SH-301 / SH-302 / SH-303 |
| 信号只有 4 类且都不修复；缺队列卡死类信号 | `signals.go` | SH-304 |
| `auto_enabled` 只消费已有操作，没有自动选题生产者 | `dispatch_ready` 只有显式入口 | SH-401 |
| 系统层零统计 | `system_jobs` 不出现在 analytics / observability / query | SH-501 / SH-502 / SH-503 |

**硬约束**（沿用 `docs/maw_lead_and_node_model_可执行任务清单.md` §1）：默认关闭、可审计、可回退；
unknown 不自动释放；不新增云端身份；单测绿不算完成，须能在真实命令或受控测试库看到结果。

---

## 1. Lane 表与写盘图

| Lane | 题 ID 序列 | Surface 根 |
| --- | --- | --- |
| NODE-BASE | SH-000 | 现场 62 个文件 |
| LEADER | SH-101 → SH-102 → SH-202 → SH-401 | `internal/engine/dispatch/**`、`internal/app/{system_cmd,serve_leader}.go` |
| LEAD-CORE | SH-201 → SH-202 | `internal/db/system_jobs.go`、`internal/systemagent/{watcher,orchestrator}.go` |
| RECON | SH-301 → SH-302 → SH-303 → SH-304 | `docs/contracts/vnext/lead/**`、`internal/systemagent/{signals,classifier,reconcile_job}.go`、`internal/app/lead_*.go` |
| STATS | SH-501 → SH-502 → SH-503 | `internal/analytics/system_health*`、`internal/httpapi/observability/**`、`internal/board/web/**` |
| DRILL | SH-900 | `tests/resilience/**`、`docs/operations/vnext/**` |

```text
首波可并行：SH-000 ‖ SH-101 ‖ SH-201 ‖ SH-301
串行热点：
  internal/app/system_cmd.go      SH-101 → SH-102 → SH-303
  internal/app/serve_leader.go    SH-102 → SH-202 → SH-401
  internal/systemagent/watcher.go SH-201 → SH-202
  internal/systemagent/signals.go SH-302 → SH-303(facts) → SH-304
  cmd/maw/main.go + i18n          SH-000 → SH-502
  board web + dist                SH-000 → SH-503
```

---

## 2. 任务卡

### SH-000 收口主工作区未提交现场

- **Goal**：现状：主工作区 62 个文件未提交（节点 CLI 预设、`managed start --model`、`stats_cmd`、工程编辑弹窗、看板多视图），未走任务卡。目标：完整备份后在 worktree 承接并经 `maw merge` 合入，主工作区回到干净状态。
- **Non-goals**：不改功能、不重构、不删除现场任何内容；不碰 `system_cmd.go` 与 `internal/systemagent/**`。
- **Depends**：无
- **Surface**：现场 62 个文件（见蓝图 write_scope）。
- **Steps**：
  1. 备份到 `/tmp/maw-wip-backup-<日期>`：`git diff` patch、8 个未跟踪文件、当前 HEAD。
  2. 在 worktree 内 apply patch 并拷入未跟踪文件，跑 Verify。
  3. `maw submit` → `maw merge --yes`；若因主工作区有差异被拒，`maw block` 交人处置，不自行清理主工作区。
- **Acceptance**：备份目录含 patch / 未跟踪文件 / HEAD；合入后主工作区 `git status` 不再列出这 62 个文件；Verify 全绿。
- **Verify**：`CGO_ENABLED=0 go test ./...`；`npm run build --prefix internal/board/web`；`node --test internal/board/page.test.cjs`

### SH-101 派发队列按工程隔离

- **Goal**：现状：协调锁按工程隔离，但 `DBQueue.Pending` 查全库，两个工程各开 serve 会互相消费对方操作，op 目录落到错误工程。目标：`DBQueue` 支持按工程过滤，`system_cmd.go` 两处生产装配传入本工程 ID。
- **Non-goals**：不改锁路径、不做全局 leader、不改 `managed_cmd.go`、不改 schema。
- **Depends**：无
- **Surface**：`internal/engine/dispatch/queue.go`、`queue_test.go`、`internal/app/system_cmd.go`
- **Read-scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/contracts/vnext/execution/runtime-state-contract-v1.md`、`internal/app/managed_cmd.go`
- **Acceptance**：两工程各一条 pending 时，A 的队列只见 A（Pending 与 Claim 都过滤）；不指定工程时行为与现状一致；`managed_cmd.go:168` 的影响只读核实后写进 submit result。
- **Verify**：`CGO_ENABLED=0 go test ./internal/engine/dispatch ./internal/app -count=1`

### SH-102 serve leader 独立成文件并覆盖全部已登记工程

- **Goal**：现状：`startServeLoops` 只为启动 serve 的工程起 leader，常驻 Watcher 未注入 `SignalFacts`，错误只打 stderr。目标：抽到 `serve_leader.go`，为全局库每个已登记且路径存在的工程各起一个 leader（各自锁和开关），注入信号证据源，循环错误写事件并节流。
- **Non-goals**：不改 `board_cmd.go` 调用点（保持 `startServeLoops(cwd)` 签名）；不改默认开关；不新增 CLI。
- **Depends**：SH-101
- **Surface**：`internal/app/system_cmd.go`、`internal/app/serve_leader.go`(新)、`internal/app/serve_leader_test.go`(新)
- **Read-scope**：`docs/maw_lead_and_node_model_可执行任务清单.md`、`internal/engine/dispatch/leader.go`
- **Acceptance**：N 个已登记工程各尝试起 leader，抢不到锁的跳过并记录；常驻 Watcher 的 `SignalFacts` 非 nil；`OnError` 写 `lead_loop_error` 事件，同类错误 60 秒内只写一次；两个开关全关的工程不起 goroutine。
- **Verify**：`CGO_ENABLED=0 go test ./internal/app -run 'Serve|Leader' -count=1`

### SH-201 system_jobs 租约回收与失败退避

- **Goal**：现状：作业领为 running 后进程崩溃会永久卡住；失败重排无退避；`Watcher.Run` 吞错。目标：db 提供 `ReapStaleSystemJobs`，领取按 attempts 指数退避，Watcher 每轮先回收，循环错误交 `OnError`。
- **Non-goals**：不改表结构与 CHECK；不改 job kind；不改 dispatch 语义。
- **Depends**：无
- **Surface**：`internal/db/system_jobs.go`、`internal/db/system_jobs_reap_test.go`(新)、`internal/systemagent/watcher.go`、`internal/systemagent/orchestrator.go`、`internal/systemagent/orchestrator_test.go`、`internal/systemagent/watcher_reap_test.go`(新)
- **Read-scope**：`docs/operations/vnext/system-agent.zh-CN.md`、`internal/db/migrations.go`
- **Acceptance**：`locked_at` 超租约（默认 10 分钟，可注入）的 running 作业回收为 queued，result 含 `reaped_from=running`；超 `max_attempts` 回收为 failed；失败后退避窗口内（`30s·2^(attempts-1)`，上限 10 分钟）不被再领，用注入时钟断言；`Watcher.Run` 非锁错误调用 `OnError`。
- **Verify**：`CGO_ENABLED=0 go test ./internal/db ./internal/systemagent -count=1`

### SH-202 lead 主动巡检：离线过期与写错误不再静默

- **Goal**：现状：`expireStaleAgents` 只在 worker 调 `maw next` 时被动触发，函数内 `_ =` 忽略写库与事件错误。目标：Watcher 增加 Housekeeping 钩子每轮执行，app 实现按工程跑过期检测；`heartbeat.go` 写错误向上返回，事务整体回滚。
- **Non-goals**：不改心跳 TTL 语义；不释放有进程证据的受管 Run；不改 `maw next` 调用点。
- **Depends**：SH-201、SH-102
- **Surface**：`internal/systemagent/watcher.go`、`internal/systemagent/watcher_housekeeping_test.go`(新)、`internal/app/heartbeat.go`、`internal/app/heartbeat_expire_test.go`、`internal/app/lead_housekeeping.go`(新)、`internal/app/lead_housekeeping_test.go`(新)、`internal/app/serve_leader.go`
- **Read-scope**：`docs/contracts/vnext/execution/runtime-state-contract-v1.md`
- **Acceptance**：无 worker 调 next 时，心跳超时 Agent 在一个 poll 周期内被置 offline 并写 `agent_expired`；有进程证据的受管 Run 仍保留占用（回归既有测试）；注入写失败时返回错误且无半状态。
- **Verify**：`CGO_ENABLED=0 go test ./internal/app ./internal/systemagent -count=1`

### SH-301 lead 自愈与系统统计契约 v1

- **Goal**：现状：自愈信号、动作与系统统计口径散落在清单与注释里。目标：写契约：信号目录、证据→动作→是否默认自动表、Run 核实释放判据、job kind 与 action 列表、系统健康指标定义。
- **Non-goals**：不写业务代码。
- **Depends**：无
- **Surface**：`docs/contracts/vnext/lead/**`
- **Read-scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_lead_and_node_model_可执行任务清单.md`、`docs/maw_lead_selfheal_可执行任务清单.md`、`docs/contracts/vnext/execution/runtime-state-contract-v1.md`、`docs/contracts/vnext/observability/timeline-metering-contract-v1.md`
- **Acceptance**：信号目录每行有判据、证据源、默认动作、是否需 `auto_kinds` 放行；`run_reconcile` 释放判据与 `recovery.Evidence` 的 `Reusable` / `KeepsResources` 一一对应；系统健康指标 ≥8 项，各有公式、分母、未知值处理；写明「unknown 不自动释放」等不变量。
- **Verify**：`manual: 评审契约与 internal/runner/recovery/reconcile.go、internal/systemagent/signals.go 字段名无冲突`

### SH-302 run_reconcile 作业类型与编排（systemagent 侧）

- **Goal**：现状：受管 Run 进入 unknown 或保留占用后无人核实。目标：新增 job kind `run_reconcile`（迁移放行 CHECK）、信号 `run_unverified`，Orchestrator 经 `ReconcilePort` 处理，只按端口结论（release / keep / escalate）记结果。
- **Non-goals**：不实现真实进程探测（SH-303）；不改 `heartbeat.go`；不默认放行 `auto_kinds`。
- **Depends**：SH-301、SH-201
- **Surface**：`internal/db/migrations.go`、`internal/db/migrations_reconcile_test.go`(新)、`internal/db/system_jobs.go`、`internal/systemagent/classifier.go`、`internal/systemagent/signals.go`、`internal/systemagent/signals_test.go`、`internal/systemagent/orchestrator.go`、`internal/systemagent/reconcile_job.go`(新)、`internal/systemagent/reconcile_job_test.go`(新)
- **Read-scope**：`docs/contracts/vnext/lead/**`
- **Acceptance**：迁移后可插入 `kind=run_reconcile` 且旧数据保留；超阈值未核实 Run 产出 `run_unverified`；端口为 nil 时作业 Escalated 且 `mode=unwired`；keep 结论不改任何资源；escalate 结论终态为 Escalated。
- **Verify**：`CGO_ENABLED=0 go test ./internal/db ./internal/systemagent -count=1`

### SH-303 接通 recovery.Reconcile：自动核实受管 Run

- **Goal**：现状：`runner/recovery.Reconcile` 与 `engine/reconcile` 只有测试调用。目标：app 实现 `ReconcilePort`，仅当证据为「确定结束或从未启动」时在单事务内收尾 Run、释放租约、任务回 ready 或 blocked 并写事件；其余保留占用并记核实时间；`buildOrchestrator` 注入端口。
- **Non-goals**：不杀进程；unknown 时不释放；不自动重派（只回到可领状态）。
- **Depends**：SH-302、SH-102
- **Surface**：`internal/app/lead_reconcile.go`(新)、`internal/app/lead_reconcile_test.go`(新)、`internal/app/lead_signals_facts.go`、`internal/app/lead_signals_facts_test.go`(新)、`internal/app/system_cmd.go`
- **Read-scope**：`docs/contracts/vnext/lead/**`、`internal/runner/recovery/reconcile.go`
- **Acceptance**：进程已退出且有结束证据的 Run 经一次 `run_reconcile` 后租约释放、任务可再领、有 `run_reconciled`（release）事件；进程存活或证据不明时资源不变、事件为 keep 或 unknown；释放事务任一写失败则全部回滚；`maw lead signals --json` 能列出 `run_unverified`。
- **Verify**：`CGO_ENABLED=0 go test ./internal/app -run 'Reconcile|Signal' -count=1`；`CGO_ENABLED=0 go test ./...`

### SH-304 补齐队列卡死类信号

- **Goal**：现状：信号只覆盖门禁环境、全员离线、merging、残留锁。目标：新增 `dispatch_op_stalled`、`claimed_not_started`、`dep_deadlock`、`no_capable_node`、`repeated_failure`，默认只上报或升级人工。
- **Non-goals**：不新增自动处置动作；不改 matching。
- **Depends**：SH-303
- **Surface**：`internal/systemagent/signals.go`、`internal/systemagent/signals_queue_test.go`(新)、`internal/app/lead_signals_facts.go`、`internal/app/lead_signals_facts_test.go`
- **Read-scope**：`docs/contracts/vnext/lead/**`
- **Acceptance**：每类信号各一条正例和反例；enqueue 后终态为 Escalated 或只上报，tasks 表不变；`maw lead signals --json` 含新 kind 与 reason。
- **Verify**：`CGO_ENABLED=0 go test ./internal/systemagent ./internal/app -run Signal -count=1`

### SH-401 自动选题生产者（默认关闭）

- **Goal**：现状：`auto_enabled` 只消费已存在的派发操作，执行队列没有自动生产者。目标：新增 `[managed] auto_select`（默认 false）；开启后 leader 每轮在「有 ready 题且受管空槽>0」时入队并立即执行一条 `dispatch_ready`，走既有 Match → Claim 链，不依赖 `system_agent.enabled`。
- **Non-goals**：不改 `auto_enabled` 语义；不默认开启；不绕过 matching / claim。
- **Depends**：SH-202、SH-102
- **Surface**：`internal/config/config.go`、`internal/config/config_test.go`、`internal/engine/dispatch/leader.go`、`internal/engine/dispatch/leader_test.go`、`internal/systemagent/producer.go`(新)、`internal/systemagent/producer_test.go`(新)、`internal/app/serve_leader.go`
- **Read-scope**：`docs/contracts/vnext/lead/**`、`internal/systemagent/dispatch.go`
- **Acceptance**：`auto_select=false` 时零入队；为 true 且有 ready 与空槽时一个周期恰好 1 条，未完成的同工程 `dispatch_ready` 存在或在 `cooldown_sec` 内则跳过；无 ready 或无空槽不入队；`system_agent.enabled=false` 时入队的作业同周期被执行并产生 Run。
- **Verify**：`CGO_ENABLED=0 go test ./internal/config ./internal/engine/dispatch ./internal/systemagent ./internal/app -count=1`

### SH-501 系统健康统计（analytics）

- **Goal**：现状：analytics 只统计轮次、Token、交付与区间，`system_jobs`、operations、runs 无统计。目标：新增 SystemHealth 报表，按 SH-301 口径计算自愈作业分布与成功率、MTTR、排队等待、派发时延、重试、失败原因、节点成功率。
- **Non-goals**：不加表、不改既有 Report、不接 HTTP。
- **Depends**：SH-301
- **Surface**：`internal/analytics/system_health.go`(新)、`internal/analytics/system_health_test.go`(新)
- **Read-scope**：`docs/contracts/vnext/lead/**`、`internal/analytics/report.go`
- **Acceptance**：固定夹具下各指标与手算一致（≥8 项）；无数据返回 unknown 而非 0；时间窗为半开区间 `[start, end)`。
- **Verify**：`CGO_ENABLED=0 go test ./internal/analytics -count=1`

### SH-502 系统健康 API 与 CLI

- **Goal**：现状：系统健康报表没有对外入口。目标：`GET /api/v1/system/health` 与 `maw system health --json` 同源输出，中英帮助一致。
- **Non-goals**：不改 analytics 计算；不做前端。
- **Depends**：SH-501、SH-000
- **Surface**：`internal/httpapi/observability/{handler,ports,adapters}.go`、`internal/httpapi/observability/system_health.go`(新)、`system_health_test.go`(新)、`internal/app/system_health_cmd.go`(新)、`system_health_cmd_test.go`(新)、`internal/app/board_cmd.go`、`cmd/maw/main.go`、`internal/i18n/catalog_{zh,en}.go`
- **Read-scope**：`docs/contracts/vnext/lead/**`、`docs/contracts/vnext/http/**`
- **Acceptance**：API 支持 `start` / `end` / `project_id` 并返回水位；CLI 与 API 同一夹具同值；中英帮助条目齐全。
- **Verify**：`CGO_ENABLED=0 go test ./internal/httpapi/observability ./internal/app ./internal/i18n -count=1`

### SH-503 看板「自愈统计」视图

- **Goal**：现状：看板没有系统层统计。目标：新增视图展示指标卡、自愈作业列表（kind / 状态 / 结果）与未知值标注，工程筛选联动。
- **Non-goals**：不改后端；不做作业操作按钮。
- **Depends**：SH-502
- **Surface**：`internal/board/web/**`、`internal/board/assets/dist/board-web.js`
- **Read-scope**：`docs/contracts/vnext/lead/**`、`docs/contracts/vnext/ui/WORKBENCH_UI_CONTRACT.md`
- **Acceptance**：有数据时指标卡与列表渲染，未知值显示为「未知」而非 0；工程筛选联动；中英文案；构建通过且 dist 已更新。
- **Verify**：`npm run build --prefix internal/board/web`；`node --test internal/board/page.test.cjs`

### SH-900 自愈演练与运维文档

- **Goal**：现状：自愈链路没有端到端演练。目标：用受控测试库演练五个故障场景，并写启用与回退文档。
- **Non-goals**：不改生产代码。
- **Depends**：SH-304、SH-401、SH-503
- **Surface**：`tests/resilience/lead_selfheal_test.go`(新)、`docs/operations/vnext/lead-self-heal.zh-CN.md`(新)
- **Read-scope**：`docs/contracts/vnext/lead/**`、`docs/operations/vnext/system-agent.zh-CN.md`
- **Acceptance**：演练覆盖 leader 中途崩溃作业被回收、两工程各自 serve 不互相消费、结束证据齐全的 Run 被释放而证据不明的保留、无 worker 时离线 Agent 被巡检、`auto_select` 开启后 ready 题被自动派发一次；文档列出启用步骤、开关含义与回退方法。
- **Verify**：`CGO_ENABLED=0 go test ./tests/resilience/... -count=1`；`CGO_ENABLED=0 go test ./...`
