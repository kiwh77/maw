# maw Pi 执行与 Lead 实施计划

2026 年 11 月 18 日 · 迭代 `IT-PI-001` · 主分支 `main`

Worker、目标级 Lead 与系统 Agent 诊断共享固定版本的 Pi SDK 执行器。Worker 在受管 worktree 内开发。Lead 产出结构化计划，maw 校验、审批并追加到完整蓝图。派发、写入范围、交付与合入判定由 maw 执行。Lead 读取交付证据，按目标验收条件决定是否提出返工。

首版以一个供应商和一个模型验证。统计同时覆盖 Worker、Lead 规划和系统诊断，先证明归属、用量、缓存口径、缺失值和补录，再开放无人值守模式。旧 CLI 的运行级证据继续按原粒度展示。

## 依赖与可并行入口

- `PI-001` 定义 Pi 执行与计量协议，`PI-002` 定义目标和入队契约。两题文件独立，可以并行领取。
- 执行线从 `PI-101` 分出 Worker 接线 `PI-102` 和发布打包 `PI-103`。
- Lead 线从 `PI-301` 到 `PI-302`、`PI-303`，完成目标持久化、增量入队和规划入口。
- `PI-201` 汇合 Worker 与目标归属，再接 `PI-202`。`PI-304`、`PI-305` 验证 Lead 和诊断用量后接看板 `PI-203`。
- `PI-901` 用真实任务收口，`PI-902` 按实际验收结果发布操作说明。

计划卡由 `.multiagent/blueprint.toml` 压入。每题独立领取，写入范围依照卡片，验收通过后再提交。

## 契约

### PI-001 冻结 Pi 执行与计量契约

现状：Runner 的来源解码和计量以 CLI 证据为主。目标：为 Pi 的 Worker 与 Lead 冻结同一套执行、停止和观测协议。

- **前置** 无
- **写入范围** `docs/contracts/vnext/execution/pi-execution-contract-v1.md`
- **参考** `docs/contracts/vnext/execution/`、`docs/contracts/vnext/observability/timeline-metering-contract-v1.md`、`internal/runner/session/source.go`
- **验收 1** 明确版本、worker/lead/diagnose 模式、身份、事件边界、工具授权与终态
- **验收 2** 定义逐次调用和运行级用量、缓存归一、缺失值、估算、重放键及样例
- **验证 1** 对照 Pi SDK、消息类型及 maw 计量契约复核成功、失败、取消和缓存样例
- **边界** 业务实现留给后续卡
- **优先级与合流顺序** 10 / 10

### PI-002 冻结目标级 Lead 与蓝图入队契约

现状：Lead 有拆题草稿和异常处置。目标：定义 Pi 接收目标、规划、追加任务、观察交付和有限返工的工作流。

- **前置** 无
- **写入范围** `docs/contracts/vnext/lead/pi-goal-workflow-v1.md`
- **参考** `docs/contracts/vnext/lead/`、`docs/contracts/vnext/iteration/`、`internal/app/blueprint_append.go`、`internal/app/plan_cmd.go`
- **验收 1** 定义目标身份、验收条件、计划版本、审批、任务映射、预算、暂停与重启恢复
- **验收 2** 定义结构化计划字段、依赖和写入范围校验、失效提案、追加幂等键与整体验收退出条件
- **验证 1** 用三张计划卡、一次并发冲突和一次集成失败追踪完整状态与证据
- **边界** 接口实现留给后续卡
- **优先级与合流顺序** 10 / 20

## 执行器与 Worker

### PI-101 实现共享 Pi SDK 执行器

现状：运行依赖各厂商 CLI。目标：由固定版本 Pi SDK 驱动 Worker、Lead 规划与诊断三种模式，模型请求使用宿主提供的配置。

- **前置** `PI-001`
- **写入范围** `runtimes/pi/**`
- **参考** `docs/contracts/vnext/execution/pi-execution-contract-v1.md`、`internal/runner/session/starter.go`
- **验收 1** 三个模式复用同一执行器与协议。Lead 规划和诊断只能请求获授权的结构化动作。Worker 工具按配置和作业范围启用
- **验收 2** 模拟模型和工具能验证消息终态、取消、逐次用量、失败及无密钥的观测事件
- **验证 1** `npm --prefix runtimes/pi ci`
- **验证 2** `npm --prefix runtimes/pi test`
- **边界** Go 节点注册与发布由各自任务实现
- **优先级与合流顺序** 20 / 30

### PI-102 接通 Pi Worker 节点和受管启动

现状：节点注册和受管启动以现有 CLI 为主。目标：通过现有节点与 Runner 入口启动 Pi Worker，绑定 task/run/generation/node 和 worktree。

- **前置** `PI-101`
- **写入范围** `internal/adapters/pi/**`、`internal/engine/model/spec.go`、`internal/engine/ports/catalog.go`、`internal/nodes/model.go`、`internal/app/node_probe.go`、`internal/app/node_cmd.go`、`internal/app/runtime.go`、`internal/app/managed_cmd.go`、`internal/runner/session/source.go`、`internal/runner/session/starter.go`、`internal/runner/process/**`
- **参考** `docs/contracts/vnext/execution/pi-execution-contract-v1.md`、`internal/adapters/codebuddy/`、`internal/runner/session/`
- **验收 1** 能登记、探测并显式启动 Pi 节点，解析结构化失败和终态，对现有 CLI 保持兼容
- **验收 2** 停止覆盖 Pi 会话及工具进程树。由执行边界约束文件访问，验证模型凭据不进入工具进程
- **验证 1** `go test ./internal/adapters/pi ./internal/nodes ./internal/app ./internal/runner/session ./internal/runner/process -count=1`
- **边界** 自动派发的开放以端到端验收为准
- **优先级与合流顺序** 30 / 40

### PI-103 打包并探测 Pi 运行时

现状：发布主要输出 Go 二进制。目标：发布可校验、固定版本的 Pi 执行组件和运行时，用户无需另装 Pi CLI。

- **前置** `PI-101`
- **写入范围** `.github/workflows/ci.yml`、`.github/workflows/release.yml`、`install.sh`
- **参考** `runtimes/pi/`、`docs/contracts/vnext/execution/pi-execution-contract-v1.md`
- **验收 1** 安装产物能在干净环境探测 Pi 版本与依赖，缺少运行时给出可操作错误
- **验收 2** 现有安装流程有通过证据，实际支持平台按冒烟结果记录
- **验证 1** 在干净的受支持 macOS 环境从发布包安装并运行无模型密钥的版本与协议探测
- **边界** 跨平台受管支持按实测另行开放
- **优先级与合流顺序** 40 / 50

## 目标级 Lead 与计划入队

### PI-301 持久化项目级目标、计划与任务归属

现状：任务与 Run 有独立的身份，原始目标需要持续追踪。目标：建立目标、计划修订、任务关联和预算的持久化状态。

- **前置** `PI-002`
- **写入范围** `internal/goal/**`、`internal/db/goal.go`、`internal/db/goal_test.go`、`internal/db/migrations.go`
- **参考** `docs/contracts/vnext/lead/pi-goal-workflow-v1.md`、`internal/db/runtime.go`、`internal/db/task_cas.go`
- **验收 1** 迁移保留历史任务，重启可读取当前目标、验收、计划版本及所关联任务
- **验收 2** 状态和修订校验并发提交，重复记录同一关联时保持幂等
- **验证 1** `go test ./internal/goal ./internal/db -count=1`
- **边界** 模型调用、CLI 和写蓝图由后续卡负责
- **优先级与合流顺序** 20 / 60

### PI-302 实现可校验且防重复的蓝图追加入队

现状：应用片段可能退役缺席的旧任务。目标：Lead 只能通过专用的增量入口预览、校验并提交审批后的目标计划。

- **前置** `PI-301`
- **写入范围** `internal/app/blueprint_append.go`、`internal/app/plan_cmd.go`、`internal/app/plan_pi_append.go`、`internal/app/plan_pi_append_test.go`、`internal/blueprint/**`
- **参考** `docs/contracts/vnext/lead/pi-goal-workflow-v1.md`、`internal/app/task_identity_test.go`、`internal/app/plan_apply_test.go`
- **验收 1** 合并到完整蓝图后校验字段、ID、依赖和写入范围，绑定蓝图基线和已获准的计划版本
- **验收 2** 同一批准操作只入队一次。过期、并发和落库失败可恢复。既有任务、活跃 Run 和实例版本保持一致
- **验证 1** `go test ./internal/blueprint ./internal/app -count=1`
- **边界** 自动合入和重置由现有权限与流程管理
- **优先级与合流顺序** 30 / 70

### PI-303 让 Pi Lead 生成并提交可审查计划

现状：lead split 仅生成模板卡。目标：经授权的 Lead 使用共享 Pi 执行器读取限定上下文、产出结构化计划，批准后增量入队。

- **前置** `PI-101`、`PI-302`
- **写入范围** `internal/app/pi_lead.go`、`internal/app/pi_lead_test.go`、`internal/app/lead_goal_cmd.go`、`internal/app/lead_goal_cmd_test.go`、`internal/goal/planner.go`、`internal/goal/planner_test.go`、`cmd/maw/pi_lead.go`、`cmd/maw/pi_lead_test.go`、`cmd/maw/main.go`
- **参考** `docs/contracts/vnext/lead/pi-goal-workflow-v1.md`、`docs/contracts/vnext/execution/pi-execution-contract-v1.md`、`internal/app/lead_cmd.go`
- **验收 1** 提供正常 CLI 入口支持目标、验收、计划预览和批准入队。Pi 输出每卡的依赖、写入范围、验收、验证与来源
- **验收 2** 默认进入计划审查；只有明确授权的策略才允许自动入队。规划失败或校验失败无任务副作用
- **验证 1** `go test ./cmd/maw ./internal/app ./internal/goal -count=1`
- **边界** 目标级连续回看由后续卡接入
- **优先级与合流顺序** 40 / 80

## 计量采集与聚合

### PI-201 接通多来源观测、调用归属与补录

现状：Runner 的用量提取和日志来源绑定 CodeBuddy。目标：用统一观测端口记录 Pi 每次调用和旧 CLI 运行级用量。

- **前置** `PI-102`、`PI-301`
- **写入范围** `internal/runner/observe/**`、`internal/runner/session/observe.go`、`internal/runner/session/observe_test.go`、`internal/runner/session/runner.go`、`internal/runner/journal/**`、`internal/runner/dbstore/**`、`internal/db/observability.go`、`internal/db/observability_test.go`、`internal/db/migrations.go`、`internal/app/managed_cmd.go`
- **参考** `docs/contracts/vnext/execution/pi-execution-contract-v1.md`、`docs/contracts/vnext/observability/timeline-metering-contract-v1.md`
- **验收 1** 按项目、节点、Run、代际、会话和模型调用归属，保留用途、时间、实际供应商模型及原始字段；区分运行基线和调用事实
- **验收 2** 用证据完成缓存归一，缺字段保留未知，重放只入账一次，数据库短暂失败后可独立补录
- **验证 1** `go test ./internal/runner/... ./internal/db ./internal/app -count=1`
- **边界** 看板聚合与展示由后续卡负责
- **优先级与合流顺序** 50 / 90

### PI-202 发布可核对的用量与节点统计

现状：汇总以轮次为主，节点成功率缺乏归属。目标：让 API 可按节点、模型、来源和用途查询可对账的用量、覆盖率及运行结果。

- **前置** `PI-201`
- **写入范围** `internal/analytics/**`、`internal/observability/**`、`internal/projection/**`、`internal/httpapi/observability/**`、`internal/app/stats_cmd.go`、`internal/app/stats_cmd_test.go`
- **参考** `docs/contracts/vnext/execution/pi-execution-contract-v1.md`、`docs/contracts/vnext/observability/timeline-metering-contract-v1.md`、`internal/db/observability.go`
- **验收 1** 区分调用级与运行级观测，逐次去重，按明确定义的分母统计用量覆盖、运行成功和交付通过
- **验收 2** 分开归集模型、工具、等待和 Run 时间，回传缺口、质量、估算成本及证据
- **验证 1** `go test ./internal/analytics ./internal/observability ./internal/projection ./internal/httpapi/observability -count=1`
- **边界** 供应商账单保持独立证据
- **优先级与合流顺序** 60 / 100

## Lead 运行与系统 Agent 诊断

### PI-304 闭合 Pi Lead 的验收、有限返工和恢复

现状：项目可派发任务，目标的整体验收与返工需要协调。目标：Pi Lead 根据持久化交付证据核对目标，有限地追加修复或升级人工。

- **前置** `PI-303`、`PI-201`
- **写入范围** `internal/goal/lifecycle.go`、`internal/goal/lifecycle_test.go`、`internal/app/lead_goal_loop.go`、`internal/app/lead_goal_loop_test.go`、`internal/app/serve_leader.go`、`internal/app/serve_leader_test.go`、`internal/app/pi_lead.go`、`internal/app/pi_lead_test.go`
- **参考** `docs/contracts/vnext/lead/pi-goal-workflow-v1.md`、`internal/engine/state/delivery.go`、`internal/systemagent/orchestrator.go`
- **验收 1** 整体验收失败时产生带证据的修复提案，沿用同一计划审批与增量入队。可重启恢复，重复事件不造重复任务
- **验收 2** 满足目标验收才完成；权限、预算耗尽和无法自动判断时暂停或升级人工。Lead 的模型调用独立归属
- **验证 1** `go test ./internal/goal ./internal/app -count=1`
- **边界** 任务提交、合入和目标验收均复用现有规则
- **优先级与合流顺序** 60 / 110

### PI-305 让系统 Agent 诊断使用同一 Pi 执行器

现状：系统 Agent 用 llm_command 请求诊断。目标：配置 Pi 后共享模型运行时，用无写权限的诊断模式生成现有决策协议。

- **前置** `PI-102`、`PI-201`
- **写入范围** `internal/systemagent/invoker.go`、`internal/systemagent/pi_invoker.go`、`internal/systemagent/pi_invoker_test.go`、`internal/config/config.go`、`internal/config/config_test.go`、`internal/app/system_cmd.go`、`internal/app/system_agent_settings.go`、`internal/app/system_agent_test.go`
- **参考** `docs/contracts/vnext/execution/pi-execution-contract-v1.md`、`docs/operations/vnext/system-agent.zh-CN.md`、`internal/systemagent/orchestrator.go`
- **验收 1** 显式配置的 Pi 成为诊断后端，决策仍经过解析和既有动作策略。旧配置可读，未就绪时清楚降级
- **验收 2** 调用用量归属诊断与对应作业。超时、认证失败和非法结构都有可审计结果
- **验证 1** `go test ./internal/systemagent ./internal/config ./internal/app -count=1`
- **边界** 运维权限与系统动作仍由现有策略约束
- **优先级与合流顺序** 60 / 120

## 看板与交付验收

### PI-203 展示 Pi Worker 和 Lead 的用量与覆盖率

现状：统计页缺少来源粒度、模型及用途信息。目标：让用户可查看 Worker、Lead 和诊断用量及采集完整度。

- **前置** `PI-202`、`PI-304`、`PI-305`
- **写入范围** `internal/board/assets/views/analytics/**`、`internal/board/web/src/views/AnalyticsView.vue`、`internal/board/web/src/views/SystemHealthView.vue`、`internal/board/assets/dist/board-web.js`
- **参考** `docs/contracts/vnext/observability/timeline-metering-contract-v1.md`、`internal/httpapi/observability/stats.go`、`internal/analytics/report.go`
- **验收 1** 支持按来源、节点、模型和用途查看，并能从汇总追到 Run
- **验收 2** 显式显示未知、估算与部分覆盖，旧 CLI 数据仍以原有粒度显示
- **验证 1** `node --test internal/board/assets/views/analytics/analytics.test.cjs`
- **验证 2** `npm --prefix internal/board/web run build`
- **边界** 任务派发与目标管理界面另由已有入口承担
- **优先级与合流顺序** 70 / 130

### PI-901 验收 Pi Worker、Lead 压题和统计闭环

现状：Pi 能力尚无 maw 端到端证据。目标：在隔离工程验证目标、批准入队、Worker 执行、交付验收、返工和计量。

- **前置** `PI-103`、`PI-203`、`PI-304`、`PI-305`
- **写入范围** `tests/resilience/pi_*.go`、`tests/browser/pi/**`、`docs/verification/pi/**`
- **参考** `docs/contracts/vnext/execution/pi-execution-contract-v1.md`、`docs/contracts/vnext/lead/pi-goal-workflow-v1.md`、`docs/contracts/vnext/observability/timeline-metering-contract-v1.md`
- **验收 1** 用固定模型跑真实工作区，对比供应商响应、事件、数据库和看板。用可控样本复现并发、重复回放、写库错误、429、断流、缓存、取消及恢复
- **验收 2** 预置一个集成失败，验证 Lead 追加修复、旧蓝图与活跃 Run 保留，最后按原目标验收。真实 API 缺凭据时显式记录未验项
- **验证 1** `go test ./tests/resilience -run Pi -count=1`
- **验证 2** 用授权测试凭据在隔离项目完成真实 Pi 验收并留存脱敏证据
- **边界** 验收场景不触碰真实工程主干
- **优先级与合流顺序** 90 / 140

### PI-902 发布 Pi 节点与 Lead 操作说明

现状：操作指南以现有 CLI 为主。目标：根据实测证据说明 Pi 安装、凭据引用、受管执行、Lead 入队、统计口径与排障。

- **前置** `PI-901`
- **写入范围** `docs/operations/vnext/pi.zh-CN.md`
- **参考** `docs/verification/pi/`、`docs/contracts/vnext/execution/pi-execution-contract-v1.md`、`docs/contracts/vnext/lead/pi-goal-workflow-v1.md`
- **验收 1** 安装到首个 Run 和 Lead 批准入队的步骤可复现。发布说明准确区分支持、实验和未验证的平台与能力
- **验收 2** 解释旧 CLI 与 Pi 统计覆盖、费用估算、取消与恢复证据
- **验证 1** 从发布产物按文档复走安装、手动启动、Lead 提案、批准入队与统计查看
- **边界** 上线范围以已通过的验收证据为准
- **优先级与合流顺序** 100 / 150

