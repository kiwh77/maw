# maw 缺口可执行任务清单（装配 + 迭代 MVP + 系统 Agent）

**主分支：`main`**（本波合入目标与 `maw next` worktree 基线）。

版本：拆包稿 v1.0，2026-11-03。状态：**仅规划人读清单**；未 `plan apply`、不改现有 E2E 活题链、不重标已 done 的 VN 卡。

依据：

- [综合功能清单 F01–F58](maw_integrated_feature_list.zh-CN.md)
- [vNext 差距分析](vnext-gap-analysis.zh-CN.md)
- 旧清单 [maw_vnext_可执行任务清单.md](maw_vnext_可执行任务清单.md)（覆盖 F01–F48 模块卡；大量已 done 但未总装）
- 系统 Agent 既有实现：`internal/systemagent/**`、`maw system *`（默认关；尚无自动派发 ready 题）

## 1. 与旧清单的关系

| 原则 | 说明 |
|---|---|
| 不重开 VN done | 模块包已交付的不重新拆实现卡 |
| 只补装配 / 新域 | `GAP-*` 总装、`IT-*` 迭代容器、`SA-*` 系统 Agent=自动派发、`EV-*` 执行视图骨架 |
| 验收以真实库为准 | 单测绿不算完成；须在 `~/.maw/maw.db` 或受控测试库看到行/字段 |
| 压入方式 | 确认后由 `maw-plan-push` **追加**进 `.multiagent/blueprint.toml` 末尾再 `plan apply --yes`；禁止单题临时 toml |

## 2. Lane 与并行

共 **22 题、5 条 Lane**。推荐 2～3 worker：入口可并行 **`GAP-001` ‖ `IT-001` ‖ `SA-001`**（写盘不交叉）。

| Lane | 题 ID | Surface 族 |
|---|---|---|
| WIRE-GAP | `GAP-001`…`GAP-006` | `internal/app`、`httpapi`、`runner`、`engine/dispatch`、`worker_cmd` |
| SYSTEM-AGENT | `SA-001`…`SA-004` | `internal/systemagent/**`、`app/system_cmd.go`、`config`、运维文档 |
| ITERATION | `IT-001`…`IT-005` | `internal/db`、`blueprint`、`plan_cmd`、iteration CLI、board 筛选 |
| EXEC-VIEW | `EV-001`…`EV-003` | board 执行视图 / settings / 默认落地路由 |
| QUALITY-GAP | `GAP-010`、`GAP-011` | `tests/managed`、迭代 apply 回归 |

依赖要点：

- `SA-002` ← `GAP-001`、`GAP-002`、`SA-001`（及建议 `GAP-004`）
- `SA-003` ← `GAP-005`、`SA-002`
- `SA-004` ← `SA-002`、`GAP-006`
- `EV-001` ← `IT-005`；`EV-003` ← `EV-001`、`EV-002`
- `GAP-010` ← `GAP-002`、`SA-003`；`GAP-011` ← `IT-003`

```text
GAP-001 → GAP-002 → SA-002 → SA-004
              ↘ GAP-005 → SA-003 → GAP-010
GAP-003 ────────↗
GAP-004 ────────↗
GAP-006 ─────────────↗ SA-004
IT-001 → IT-002 → IT-003 → IT-004 → IT-005 → EV-001 → EV-003
                                      EV-002 ──────────↗
IT-003 → GAP-011
```

## 3. 领取硬规则

- 一次一题；只改本卡 Surface；越界先 `maw request-change`。
- Goal 两句「现状 / 目标」；Acceptance 可判定；Verify 独立可执行（或 `manual:`）。
- 实现题 Read-scope ≥1；字段名用 `deps`（禁止拼成 `depends`）。
- macOS 进程类测试沿用 `requireDarwinProcessTools` 跳过守卫。

---

## 4. Lane WIRE-GAP

### GAP-001 Managed/系统路径写入 launch_spec 与 run_meta

- **Goal**：现状：`__runner` 仅在已有 `launch_spec.json` 时才跑 session，但生产路径无人写入该文件，受管启动空转。目标：`ManagedStart` / 系统派发在 Launch 前写出 `ops/<op>/launch_spec.json` 与 `run_meta.json`。
- **Non-goals**：不接具体厂商 Decode 语义（留给 GAP-002）；不改 matching；不启用 auto 循环。
- **Depends**：无
- **Surface / write_scope**：`internal/app/runtime.go`、`internal/app/managed_cmd.go`、`internal/app/*_test.go`（仅本卡相关测）
- **Read-scope**：`docs/vnext-gap-analysis.zh-CN.md`、`docs/contracts/vnext/execution/`、`internal/engine/model/spec.go`、`internal/runner/session/runner.go`
- **Contract**：LaunchSpec / RunMeta JSON 字段与既有 `maybeRunSession` 读取形状一致；不改契约文件除非缺字段（若缺则另开契约小卡，本卡禁止）。
- **Lane / 范围**：`WIRE-GAP`；**maw**：priority=10 · merge_order=10
- **需求／门槛**：`F13`
- **Acceptance**：
  - [ ] 一次 `maw managed start --task … --node …`（或等价测试）后，`ops/<operation_id>/launch_spec.json` 与 `run_meta.json` 存在且含 `run_id`、`project_id`、`global_db`。
  - [ ] 无 spec 时行为与今日一致（可文档化）；有 spec 时 `__runner` 进入 session 路径。
- **Steps**：
  1. 在 `runnerLauncher.Launch` 或 Claim 成功后的准备阶段组装 LaunchSpec（可先用可执行探测到的 argv 占位或 adapter BuildLaunchSpec）。
  2. 原子写入 op 目录；补单测用临时 stateDir。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/app -count=1 -run 'Managed|LaunchSpec|Runner'
  ```

---

### GAP-002 __runner 接入 CodeBuddy adapter

- **Goal**：现状：session 使用 PassthroughSource，无真实协议解释与用量。目标：当节点为 CodeBuddy 时实例化 adapter 作 Source，真子进程跑通并回写 run 起止。
- **Non-goals**：不一次接齐 WorkBuddy/Trae/ZCode；不做自动选题。
- **Depends**：`GAP-001`
- **Surface / write_scope**：`internal/app/managed_cmd.go`、`internal/runner/dbstore/**`、`internal/runner/session/**`（仅接线所需）、`tests/managed/**`（本卡相关）
- **Read-scope**：`internal/adapters/codebuddy/**`、`docs/contracts/vnext/ports/`、`docs/vnext-gap-analysis.zh-CN.md`
- **Contract**：以 ports Probe/Launch/Decode/Interpret 为准。
- **Lane / 范围**：`WIRE-GAP`；**maw**：priority=20 · merge_order=20
- **需求／门槛**：`F17`、`F18`、`F13`
- **Acceptance**：
  - [ ] 受管 Run 在库中 `started_at`/`finished_at` 非空（假 CLI 或 Darwin 真机）。
  - [ ] `run_events` 出现阶段推进；Passthrough 仅作非 CodeBuddy 回退。
- **Steps**：
  1. 按 `run_meta` / 节点配置选择 adapter；Probe 失败明确 failed，不空转成功。
  2. 补 managed 测试夹具。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/app ./internal/runner/dbstore ./tests/managed -count=1
  ```

---

### GAP-003 挂载 v1 commands 与 resources 写路由

- **Goal**：现状：`httpapi/commands` 与多数 resources 未挂到 Hub，界面/API 派发 404。目标：在 `board_cmd` v1 分流挂载写路由，接 localauth 与 Idempotency-Key。
- **Non-goals**：不实现新业务语义；不改桌面壳。
- **Depends**：无
- **Surface / write_scope**：`internal/app/board_cmd.go`、`internal/httpapi/commands/**`、`internal/httpapi/resources/**`、相关 `*_test.go`
- **Read-scope**：`docs/contracts/vnext/http/http-api-contract-v1.md`、`docs/vnext-gap-analysis.zh-CN.md`
- **Contract**：契约已有路由名；本卡只接线。
- **Lane / 范围**：`WIRE-GAP`；**maw**：priority=15 · merge_order=15
- **需求／门槛**：`F04`、`F13`、`F19`
- **Acceptance**：
  - [ ] `POST /api/v1/projects/{id}/dispatches` 在授权下返回 202 且可查 Operation；未授权 401。
  - [ ] 既有 `DELETE /api/v1/projects/{id}` 与 `system-jobs` 不回归。
- **Steps**：
  1. 扩展 `v1Route`；实例化 commands/resources handler。
  2. 表驱动路由测试 + 最小 HTTP 测。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/app ./internal/httpapi/commands ./internal/httpapi/resources -count=1
  ```

---

### GAP-004 maw next 统一走 ClaimExecution

- **Goal**：现状：`maw next` 与 `managed start` 两套领取事务，next 不建 Run/Operation。目标：next 改为 `engine/claim.Claim`，保留旧 JSON 形状与兼容字段。
- **Non-goals**：不改选题亲和算法语义；不强制 next 起 Runner。
- **Depends**：无
- **Surface / write_scope**：`internal/app/worker_cmd.go`、`internal/app/claim_compat_test.go`、`internal/engine/claim/**`（仅若缺兼容钩子）
- **Read-scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/contracts/vnext/ports/`、`docs/vnext-gap-analysis.zh-CN.md`
- **Contract**：Claim 端口 §2.1。
- **Lane / 范围**：`WIRE-GAP`；**maw**：priority=25 · merge_order=25
- **需求／门槛**：`F15`
- **Acceptance**：
  - [ ] `maw next` 成功后存在对应 Run（及策略允许时的 Operation）。
  - [ ] 两 Agent 同时 next 同一题仅一人赢；另一人可见 claimLost/等价错误。
- **Steps**：
  1. 将 `commitClaim` 路径迁到 Claim；保留返回字段。
  2. 强化并发单测。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/app -count=1 -run 'Claim|Next|Compat'
  ```

---

### GAP-005 Runner 交付计量与来源会话

- **Goal**：现状：`source_sessions`/`usage_records` 生产零写入，时间轴/统计空。目标：Runner 交付时 `EnsureSession`/`EnsureTurn`/`LinkRun`/`ObserveUsage`（能力允许时）。
- **Non-goals**：不伪造 Token；来源无用量时显式 unknown。
- **Depends**：`GAP-002`
- **Surface / write_scope**：`internal/runner/**`、`internal/app/managed_cmd.go`（接线点）、`internal/db/observability.go`（若缺辅助）
- **Read-scope**：`docs/contracts/vnext/observability/`、`docs/maw_integrated_feature_list.zh-CN.md`
- **Contract**：observability 质量标记规则。
- **Lane / 范围**：`WIRE-GAP`；**maw**：priority=30 · merge_order=30
- **需求／门槛**：`F37`、`F42`、`F46`
- **Acceptance**：
  - [ ] 一次成功受管 Run 后 `source_sessions` 或 `run_source_links` ≥1（测库）。
  - [ ] 无用量来源时不写 0 Token 冒充观测。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/runner/... ./internal/db -count=1 -run 'Usage|Session|Link|Meter'
  ```

---

### GAP-006 serve 内 DISPATCH-LOOP 与 system watch 共用 leader

- **Goal**：现状：无常驻 `Coordinator.Step` 循环；system watch 已抢 `coordinator.lock`。目标：`auto_enabled` 时 serve 推进排队 Operation；与 system agent 共用同一 leader 锁，双 serve 不双主。
- **Non-goals**：不在本卡实现 matching 选题（SA-002）；不改 limit 语义。
- **Depends**：`GAP-001`
- **Surface / write_scope**：`internal/app/board_cmd.go`、`internal/app/system_cmd.go`、`internal/engine/dispatch/**`（若需导出锁路径助手）
- **Read-scope**：`docs/vnext-gap-analysis.zh-CN.md`、`docs/operations/vnext/system-agent.zh-CN.md`
- **Lane / 范围**：`WIRE-GAP`；**maw**：priority=35 · merge_order=35
- **需求／门槛**：`F14`
- **Acceptance**：
  - [ ] `auto_enabled=true` 时 serve 进程能推进已排队 Operation，无需手敲 `managed start` 步进。
  - [ ] 第二实例抢锁失败且不破坏第一实例。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/app ./internal/engine/dispatch -count=1 -run 'Dispatch|Leader|Watch|Auto'
  ```

---

## 5. Lane SYSTEM-AGENT

### SA-001 system_jobs 增加 dispatch_ready

- **Goal**：现状：job kind 仅 unblock_assist/task_split/ops_run。目标：迁移放行 `dispatch_ready`；分类器/入队/CLI/HTTP 识别该 kind。
- **Non-goals**：不实现 Match 派发循环（SA-002）。
- **Depends**：无
- **Surface / write_scope**：`internal/db/migrations.go`、`internal/db/system_jobs.go`、`internal/systemagent/**`、`internal/httpapi/systemjobs/**`、`internal/app/system_cmd.go`
- **Read-scope**：`docs/operations/vnext/system-agent.zh-CN.md`、`docs/maw_gap_可执行任务清单.md`
- **Lane / 范围**：`SYSTEM-AGENT`；**maw**：priority=10 · merge_order=40
- **需求／门槛**：自动派发（功能清单 F14）；用户需求「系统 Agent 可直接执行任务」
- **Acceptance**：
  - [ ] `maw system trigger --kind dispatch_ready` 可入队；非法 kind 仍拒绝。
  - [ ] 新库迁移 CHECK 含 `dispatch_ready`；升级路径文档注明需排空后 `maw init --upgrade`。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/db ./internal/systemagent ./internal/httpapi/systemjobs -count=1
  ```

---

### SA-002 Orchestrator：Match → Claim → launch_spec → Step

- **Goal**：现状：系统 Agent 不能挑 ready 题执行。目标：处理 `dispatch_ready`（及 auto 循环）时调用 `matching.Match`，再 Claim、写 launch_spec、Coordinator.Step；容量遵守 `managed.limit`。
- **Non-goals**：不接模型 invoke 实验开关以外的来源；不绕过 write_scope/lease。
- **Depends**：`SA-001`、`GAP-001`、`GAP-002`
- **Surface / write_scope**：`internal/systemagent/**`、`internal/app/system_cmd.go`、`internal/engine/matching/**`（仅调用侧适配，不改匹配纯函数语义除非测缺）
- **Read-scope**：`internal/engine/matching/**`、`docs/contracts/vnext/execution/`、本清单
- **Lane / 范围**：`SYSTEM-AGENT`；**maw**：priority=40 · merge_order=50
- **需求／门槛**：`F14`、`F15`
- **Acceptance**：
  - [ ] 有 ready 题 + 就绪节点 + `auto_enabled`/`dispatch_ready` 时产生 Run/Operation 且非空转（依赖 GAP-002）。
  - [ ] 无匹配时 job 结果含可解释 reasons，不空转成功。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/systemagent ./internal/engine/matching ./internal/app -count=1 -run 'Dispatch|Match|System'
  ```

---

### SA-003 执行审计日志与 job↔run 关联

- **Goal**：现状：LLM 原文不落盘；`session.Report` 不持久；job 无 run_id。目标：每次执行写 `ops/<op>/report.json`；`system_jobs.result_json` 含 `run_id`/`operation_id`/`journal_dir`；保留 LLM 原文（截断上限可配）；journal 设 MaxBytes。
- **Non-goals**：不做云端上报；不默认采集完整用户隐私提示词到全局 DB（原文仅本机 op 目录或受限字段）。
- **Depends**：`SA-002`、`GAP-005`
- **Surface / write_scope**：`internal/systemagent/**`、`internal/app/managed_cmd.go`、`internal/runner/journal/**`、`internal/db/system_jobs.go`
- **Read-scope**：`docs/operations/vnext/system-agent.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`（F29/F44）
- **Lane / 范围**：`SYSTEM-AGENT`；**maw**：priority=50 · merge_order=60
- **需求／门槛**：`F29`、`F44`；用户需求「执行后保留日志供复盘」
- **Acceptance**：
  - [ ] 完成一次系统/受管执行后，op 目录存在 `report.json` 与 journal；job 可反查 run。
  - [ ] 诊断类作业保存 raw 决策文本或侧车文件路径。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/systemagent ./internal/runner/journal ./internal/app -count=1 -run 'Audit|Report|Journal|System'
  ```

---

### SA-004 auto_enabled 与系统 Agent 配置合一

- **Goal**：现状：`managed.auto_enabled` 与 `system_agent.enabled` 双开关语义不清。目标：文档与运行时约定——`auto_enabled=true` 驱动 SA 派发循环；`system_agent` 管阻塞自愈/拆题等 kinds；设置页只读展示两段关系。
- **Non-goals**：不做设置页可写保存 API（可另卡）。
- **Depends**：`SA-002`、`GAP-006`
- **Surface / write_scope**：`internal/config/config.go`、`internal/app/system_cmd.go`、`internal/app/runtime.go`、`docs/operations/vnext/system-agent.zh-CN.md`、`docs/operations/vnext/*.zh-CN.md`（相关）、`internal/board/assets/views/settings/**`（只读文案）
- **Read-scope**：`docs/maw_integrated_feature_list.zh-CN.md`、`SECURITY.md`
- **Lane / 范围**：`SYSTEM-AGENT`；**maw**：priority=60 · merge_order=70
- **需求／门槛**：`F47`
- **Acceptance**：
  - [ ] 运维文档用一张表说清两开关；错误开启时 CLI 提示指向文档。
  - [ ] 设置页展示当前 auto/system 状态且不误导「已自动跑模型」若 Runner 未就绪。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/config ./internal/app -count=1 -run 'System|Managed|Config'
  manual: 打开设置页核对文案与 config 一致
  ```

---

## 6. Lane ITERATION

### IT-001 iterations 表与 tasks.iteration_id

- **Goal**：现状：仅有 project→task。目标：迁移增加 `iterations` 与 `tasks.iteration_id`；purge 工程时清理；读写辅助函数齐备。
- **Non-goals**：不做迭代分支合入（F50/F51）；不改蓝图解析（IT-002）。
- **Depends**：无
- **Surface / write_scope**：`internal/db/migrations.go`、`internal/db/**`（task/iteration/purge/helpers）、相关测
- **Read-scope**：`docs/maw_integrated_feature_list.zh-CN.md` §12、`docs/ARCHITECTURE_VNEXT.zh-CN.md`
- **Lane / 范围**：`ITERATION`；**maw**：priority=10 · merge_order=10
- **需求／门槛**：`F49`
- **Acceptance**：
  - [ ] 新库含 iterations；任务可空或绑定 iteration_id；PurgeProject 删除迭代行。
  - [ ] 迁移版本递增且旧库升级路径可测。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/db -count=1 -run 'Iteration|Migrate|Purge|Task'
  ```

---

### IT-002 蓝图迭代契约与解析

- **Goal**：现状：blueprint 无 iteration。目标：契约 + `[[iteration]]` / `task.iteration` 解析落入模型；非法引用 plan check 失败。
- **Non-goals**：不在本卡改 plan apply 写库（IT-003）。
- **Depends**：`IT-001`
- **Surface / write_scope**：`docs/contracts/vnext/**`（迭代相关新增）、`internal/blueprint/**`
- **Read-scope**：`docs/maw_integrated_feature_list.zh-CN.md` §12
- **Lane / 范围**：`ITERATION`；**maw**：priority=20 · merge_order=20
- **需求／门槛**：`F49`
- **Acceptance**：
  - [ ] 合法蓝图解析出迭代列表与任务归属；未知 iteration id 被拒绝。
  - [ ] 契约样例与校验脚本（若有）更新。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/blueprint -count=1
  ```

---

### IT-003 plan apply 按迭代归属

- **Goal**：现状：apply 整工程增量无批次容器。目标：一次 apply 的新建/更新任务写入指定或新建迭代；事件带 iteration_id；retire 默认不跨迭代误伤（策略写清）。
- **Non-goals**：不实现迭代分支 merge。
- **Depends**：`IT-002`
- **Surface / write_scope**：`internal/app/plan_cmd.go`、相关测、必要 helpers
- **Read-scope**：`internal/blueprint/**`、契约、本清单
- **Lane / 范围**：`ITERATION`；**maw**：priority=30 · merge_order=30
- **需求／门槛**：`F49`
- **Acceptance**：
  - [ ] 连续两波 apply 可落入两个 iteration；任务行带对的 iteration_id。
  - [ ] `plan_applied`（或等价）事件可统计每迭代 created/updated。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/app -count=1 -run 'Plan|Iteration|Apply'
  ```

---

### IT-004 CLI：iteration list/create/show 与状态子集

- **Goal**：现状：无迭代命令。目标：`maw iteration list|create|show`；状态至少支持规划中/执行中（F52 子集）；同工程未收尾策略可先警告。
- **Non-goals**：不做收尾合回主干；不做完整互斥强制（可 warn）。
- **Depends**：`IT-003`
- **Surface / write_scope**：`internal/app/iteration_cmd.go`（新）、`cmd/maw/main.go`、`internal/i18n/catalog_*.go`
- **Read-scope**：本清单、`docs/maw_integrated_feature_list.zh-CN.md` §12
- **Lane / 范围**：`ITERATION`；**maw**：priority=40 · merge_order=40
- **需求／门槛**：`F52`（子集）
- **Acceptance**：
  - [ ] CLI JSON 可列出迭代与任务计数。
  - [ ] create 写库；show 含状态与任务 id 列表。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/app ./internal/i18n -count=1 -run 'Iteration'
  ```

---

### IT-005 看板与 API 按 iteration 筛选

- **Goal**：现状：筛选仅 project/agent/status。目标：快照与 read API 支持 `iteration_id`；板面可筛选（最小：查询参数 + 列表字段）。
- **Non-goals**：不做完整执行视图（EV-001）。
- **Depends**：`IT-004`
- **Surface / write_scope**：`internal/board/**`、`internal/httpapi/read/**`、`internal/app/board_cmd.go`（若装过滤）、前端筛选最小改动 `internal/board/web/src/**` 或 `assets/**`（本卡择一主路径，Steps 写死）
- **Read-scope**：`docs/contracts/vnext/ui/`、本清单
- **Lane / 范围**：`ITERATION`；**maw**：priority=50 · merge_order=50
- **需求／门槛**：`F53`（子集）
- **Acceptance**：
  - [ ] 指定 iteration 时任务列表仅含该迭代（含空迭代）。
  - [ ] 快照任务对象暴露 `iteration_id`（可空）。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/board ./internal/httpapi/read ./internal/app -count=1 -run 'Iteration|Snapshot|Filter'
  ```

---

## 7. Lane EXEC-VIEW

### EV-001 执行视图页按迭代聚合

- **Goal**：现状：默认任务流水，无按迭代聚合主视图。目标：新增执行视图：按迭代展示任务/运行/交付摘要，详情走抽屉。
- **Non-goals**：不删除旧 tasks/runs 顶级入口（EV-003 再收）；不做节点管理迁入。
- **Depends**：`IT-005`
- **Surface / write_scope**：`internal/board/web/src/views/**`（新执行视图）、`internal/board/assets/views/**`（若双轨需同步）、相关 css/spec；**禁止**改默认路由（EV-003）
- **Read-scope**：`docs/maw_integrated_feature_list.zh-CN.md` §13、`docs/contracts/vnext/ui/`
- **Lane / 范围**：`EXEC-VIEW`；**maw**：priority=60 · merge_order=60
- **需求／门槛**：`F56`、`F58`（子集）
- **Acceptance**：
  - [ ] 选中迭代可见三类摘要；点任务开抽屉不跳顶级页。
  - [ ] 无迭代时有明确空态，不冒充「无数据故障」。
- **Verify**：

  ```sh
  manual: 浏览器打开执行视图选迭代核对三栏
  # 若有组件测：
  npm test --prefix internal/board/web 2>/dev/null || true
  ```

---

### EV-002 设置页收敛入口声明

- **Goal**：现状：主题/语言在顶栏，节点独立导航，设置页偏受管只读。目标：设置页增加「节点与执行 / 外观」分区与说明链；顶栏语言/主题可保留但文案指向设置（渐进）。
- **Non-goals**：不实现节点登记写 API；不一次拆掉顶级 nodes 路由。
- **Depends**：无
- **Surface / write_scope**：`internal/board/assets/views/settings/**`、`internal/board/web/src/views/**`（settings）、`internal/board/web/src/components/AppChrome.vue`（仅链到设置的文案/入口）
- **Read-scope**：`docs/maw_integrated_feature_list.zh-CN.md` §13 F57
- **Lane / 范围**：`EXEC-VIEW`；**maw**：priority=55 · merge_order=55
- **需求／门槛**：`F57`（子集）
- **Acceptance**：
  - [ ] 设置页可见节点/外观/受管/系统 Agent 状态说明区块。
  - [ ] 不破坏现有受管只读策略展示。
- **Verify**：

  ```sh
  manual: 打开设置页核对分区
  ```

---

### EV-003 默认落地页改为执行视图

- **Goal**：现状：默认旧 board。目标：壳默认路由进入执行视图；旧看板可作次级入口保留。
- **Non-goals**：不删除 tasks/runs/deliveries 路由（可降级为次级）；不做桌面壳特殊分叉。
- **Depends**：`EV-001`、`EV-002`
- **Surface / write_scope**：`internal/board/assets/shell/**`、`internal/board/web/src/lib/route.ts`、`view-registry`、必要 i18n
- **Read-scope**：本清单、`docs/maw_integrated_feature_list.zh-CN.md` §13
- **Lane / 范围**：`EXEC-VIEW`；**maw**：priority=70 · merge_order=70
- **需求／门槛**：`F56`
- **Acceptance**：
  - [ ] 冷启动默认落地执行视图。
  - [ ] 深链旧 board 仍可用。
- **Verify**：

  ```sh
  manual: 无 hash 打开中枢确认默认视图；带旧路由仍可进入
  ```

---

## 8. Lane QUALITY-GAP

### GAP-010 受管真实 Run 端到端验收

- **Goal**：现状：装配卡各自单测不足以证明总装。目标：一条受控 e2e（假 CodeBuddy 或 Darwin）证明 Run 起止、journal、system_job 关联（若启用 SA）在库/盘可见。
- **Non-goals**：不验收多工具矩阵。
- **Depends**：`GAP-002`、`SA-003`
- **Surface / write_scope**：`tests/managed/**`、`docs/verification/**`（本卡报告）、必要时测试夹具目录
- **Read-scope**：本清单、`docs/vnext-gap-analysis.zh-CN.md`
- **Lane / 范围**：`QUALITY-GAP`；**maw**：priority=80 · merge_order=80
- **需求／门槛**：首闭环证据；F17/F18/F44
- **Acceptance**：
  - [ ] 报告记录命令、环境、库行数前后差、op 目录文件列表。
  - [ ] CI/本地 Darwin 守卫下失败原因可区分「跳过」与「失败」。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./tests/managed -count=1
  ```

---

### GAP-011 迭代 apply 回归

- **Goal**：现状：增量 apply 无迭代隔离回归。目标：自动化证明两波蓝图分属两迭代；retire 策略不误删他迭代 idle 题（按 IT-003 约定）。
- **Non-goals**：不测分支合入。
- **Depends**：`IT-003`
- **Surface / write_scope**：`internal/app/*iteration*_*test.go`、`tests/**`（若放集成）、`docs/verification/**` 短文
- **Read-scope**：IT-003 Acceptance、本清单
- **Lane / 范围**：`QUALITY-GAP`；**maw**：priority=45 · merge_order=45
- **需求／门槛**：`F49`
- **Acceptance**：
  - [ ] 测试红绿稳定；失败信息含 iteration_id。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/app -count=1 -run 'IterationApply|PlanIteration'
  ```

---

## 9. 附录 A：Fxx → 题 ID 映射

| F | 首波题 | 备注 |
|---|---|---|
| F04 | GAP-003 | 写路由可达后界面建任务仍可另卡 |
| F13 | GAP-001、GAP-002、GAP-003 | 真启动闭环 |
| F14 | GAP-006、SA-002、SA-004 | 自动选择 + 循环 |
| F15 | GAP-004、SA-002 | 统一领取 |
| F17–F18 | GAP-002、GAP-010 | 真实批次 |
| F19 | GAP-003 | 取消等写入口挂载；语义仍依赖 reconcile 后波 |
| F29 / F44 | SA-003、GAP-010 | 日志与证据 |
| F37 / F42 | GAP-005 | 会话/用量生产者 |
| F46 | GAP-005 | 质量标记不造假 |
| F47 | SA-004 | 配置说明合一 |
| F49 | IT-001…IT-003、GAP-011 | 容器 MVP |
| F52 | IT-004 | 状态机子集 |
| F53 | IT-005、EV-001 | 按迭代查看 |
| F56 | EV-001、EV-003 | 执行视图默认 |
| F57 | EV-002 | 设置收敛子集 |
| F58 | EV-001 | 抽屉属性呈现子集 |

未在首波拆满卡的 **partial**（延后，仅索引）：

| F | 延后原因 |
|---|---|
| F03 | 工具来源/节点/日期组合筛选 |
| F07 / F23 / F27 | 非 Git 工作区与非代码验收接线 |
| F08–F11 | 节点登记/探测生产化（依赖 adapter 接线后波） |
| F20–F21 | 失败分类入口、reconcile/recovery 总装 |
| F24–F25 | 执行前齐套检查、打开目录 |
| F26 / F28 | 统一交付清单抛光 |
| F30–F31 / F33 | 主屏并入节点、通知策略可配 |
| F34–F36 / F38–F41 / F43 | 投影喂数与统计图（待 GAP-005/010 有数据） |
| F45 | 投影生产构建全量 |
| F50 / F51 / F54 / F55 | 迭代分支合回、迭代间依赖、归档证据链（第二波） |

---

## 10. 附录 B：第二波 backlog 索引（不拆满卡）

1. **IT-1xx**：迭代目标分支、任务合入迭代分支、收尾合回主干、互斥强约束、迭代间 deps、归档包（F50/F51/F54/F55）。
2. **GAP-1xx**：`reconcile`/`recovery` 生产接线；取消/超时 HTTP；失败分类 UI。
3. **ADAPT-xxx**：WorkBuddy/Trae/ZCode 受管能力分级启用（各工具独立验收）。
4. **OBS-xxx**：projection 生产构建、热图/并发统计在有区间数据后点亮。
5. **FE-xxx**：旧顶级 tasks/runs/deliveries 降级或并入执行视图属性；节点只留设置页。

---

## 11. 推荐首波压入顺序（供 maw-plan-push）

并行入口：`GAP-001`、`GAP-003`、`IT-001`、`SA-001`、`EV-002`。

串行主链（受管真跑）：`GAP-001 → GAP-002 → GAP-005 → SA-001 → SA-002 → SA-003 → GAP-010`。

迭代链：`IT-001 → IT-002 → IT-003 → IT-004 → IT-005 → EV-001 → EV-003`；`GAP-011` 接在 `IT-003` 后。

自动循环：`GAP-006` 可与 `GAP-002` 后并行开工，`SA-004` 收口。

**本文件确认前不要 `plan apply`。** 压入时必须追加蓝图末尾并保留 E2E/既有题全集，禁止单题 toml。

---

## 12. 紧急修复波：查询/领取分离（CQ，2026-11-04）

**背景（personal-work-center / T-370）**：普通 zsh 因 `PATH` 含 `.claude-code` 被 `DetectApp` 误判为 `claude-*`；裸跑 `maw next` 立刻领取并建占用 Run；零 progress 后挂尸。同分钟 `claude-goalagents-1` 领的 E2E-108 约 16 分钟后 `agent_expired` 被回收，但 GAP-004 后人工 next 亦建占用 Run 时，`agent_expired` 可能 **occupancy_kept** 永不退回 ready。

**主分支**：`main`。Lane：`CLAIM-SAFE`。并行入口：`CQ-001` ‖ `CQ-002` ‖ `CQ-003`；`CQ-004` ← `CQ-002`。

### CQ-001 修复 DetectApp 对 PATH 含 claude 的误判

- **Goal**：现状：`DetectApp` 用 `strings.Contains(PATH, "claude")`，本机装过 Claude Code 的目录进 PATH 就会在无 IDE 环境的普通终端里登记成 `claude-*`。目标：仅凭明确的 Claude 运行时环境变量判定；PATH 子串不得单独认定 app。
- **Non-goals**：不改 Worker 命名格式；不清理历史错误注册的 agent 行（可另开运维卡）。
- **Depends**：无
- **Surface / write_scope**：`internal/identity/**`
- **Read-scope**：`docs/maw_gap_可执行任务清单.md`（本节）
- **Lane / maw**：`CLAIM-SAFE`；priority=5 · merge_order=400
- **Acceptance**：
  - [ ] `PATH` 含 `$HOME/.claude-code/bin`、且无 `CLAUDE_*` / `CLAUDECODE` / IDE 变量时，`DetectApp()` 返回 `agent`（或非 `claude`），不得返回 `claude`。
  - [ ] 设置 `CLAUDECODE=1` 或任一 `CLAUDE_*` 时仍判定为 `claude`；`MAW_APP` 覆盖仍优先。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/identity -count=1
  ```

### CQ-002 CLI 查询与领取分离（裸 next 不再领取）

- **Goal**：现状：裸 `maw next` 直接 claim（改状态、建 Run/租约、写 worktree）。目标：查询与领取分离——默认 `maw next` 只预览下一题候选（不改任务状态、不建 Run、不写 worktree、不把 Agent 标 working）；真正领取必须显式 `--claim`（或等价子命令），行为对齐今日的正式领取。
- **Non-goals**：不改选题/亲和算法；不改 managed start；不删除 `--dry-run`（可与默认查询语义对齐或标为同义）。
- **Depends**：无
- **Surface / write_scope**：`internal/app/worker_cmd.go`、`internal/app/claim_compat_test.go`、`cmd/maw/main.go`、`internal/i18n/catalog_zh.go`、`internal/i18n/catalog_en.go`
- **Read-scope**：`docs/maw_gap_可执行任务清单.md`、`internal/schedule/schedule.go`
- **Lane / maw**：`CLAIM-SAFE`；priority=8 · merge_order=410
- **Acceptance**：
  - [ ] 临时库中裸 `Next(..., dryRun=false 的旧默认路径)` 在改造后：无 `--claim` 时任务仍为 `ready`、无新 Run、无 worktree 写入。
  - [ ] `maw next --claim`（或文档声明的等价入口）成功后状态/`run_id`/worktree 与改造前正式领取一致。
  - [ ] 已持有任务时幂等续做行为保留（仍返回原题，不误开第二题）。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/app ./internal/i18n -count=1 -run 'Next|Claim|DryRun|Peek|Manual'
  ```

### CQ-003 人工领取无进程时 agent_expired 须回收占用

- **Goal**：现状：GAP-004 后人工 `next` 建占用 Run；`expireStaleAgents` 见 `Occupies()` 就 `agent_expired_occupancy_kept`，题永不回 ready（T-370 挂尸类）。目标：来源为人工领取、且从未启动受管进程／无进程证据的 Run，心跳过期时必须释放任务回 `ready`（或合法 `blocked`），不得永久占坑。
- **Non-goals**：不削弱「受管进程可能仍在写盘」时的 occupancy_kept；不改 reconcile 人工核实流。
- **Depends**：无
- **Surface / write_scope**：`internal/app/heartbeat.go`、`internal/app/heartbeat_expire_test.go`（本卡新建测）
- **Read-scope**：`docs/maw_gap_可执行任务清单.md`、`docs/contracts/vnext/execution/`、`internal/engine/model/runtime.go`、`internal/app/claim_compat_test.go`
- **Lane / maw**：`CLAIM-SAFE`；priority=6 · merge_order=405
- **Acceptance**：
  - [ ] 单测：人工领取产生占用 Run、Agent last_seen 超时后，任务回 `ready`（或文档化的 blocked），事件非长期 `occupancy_kept` 死锁。
  - [ ] 单测：受管 Run 仍在占用（进程未确认停止）时，行为仍为 occupancy_kept。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/app -count=1 -run 'Expire|Occupancy|Heartbeat|ManualNext'
  ```

### CQ-004 协议与 skill 改为先查后领

- **Goal**：现状：AGENTS／skill／protocol 写「直接 `maw next` 领取」。目标：文案与模板改为「先查询／预览 → 确认后 `--claim` 领取」；中英协议同步。
- **Non-goals**：不改 board UI；不强制改用户家目录 `~/.cursor/skills`（仓库内 skills 为准）。
- **Depends**：`CQ-002`
- **Surface / write_scope**：`skills/**`、`internal/protocol/**`、`docs/operations/vnext/**`、`AGENTS.md`、`.cursor/rules/maw.mdc`（若本卡需与 protocol 生成物一致；仅协议段相关）
- **Read-scope**：`docs/maw_gap_可执行任务清单.md`、`CQ-002` 落地后的 CLI 帮助文案
- **Lane / maw**：`CLAIM-SAFE`；priority=20 · merge_order=420
- **Acceptance**：
  - [ ] 中英 protocol 均出现查询与 `--claim`（或等价）分离说明；不再把裸 `maw next` 写成唯一领取动作。
  - [ ] 仓库 `skills/maw-task-pull`（及系列路由）示例先预览再领取。
- **Verify**：

  ```sh
  CGO_ENABLED=0 go test ./internal/protocol -count=1
  ```
