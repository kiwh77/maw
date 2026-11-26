# maw vNext 需求目标与代码实现差距分析

日期：2026-10-24。分析对象：`docs/ARCHITECTURE_VNEXT.zh-CN.md`（架构 v1.1）、`docs/maw_integrated_feature_list.zh-CN.md`（F01–F48）、`docs/maw_vnext_可执行任务清单.md`（270 题）与当前 `main` 上的 Go 实现（45,192 行生产代码 / 28,931 行测试）。

判定方法：以**真实运行数据 + 生产代码引用图**为准，不以任务卡 `done` 状态、README 能力表或单测绿灯为准。

---

## 0. 结论

**vNext 的零件造完了，「总装」只接上了旧人工链路那一条。**

- 270 道题全部 `done`，每个包单独编译、单测通过；但 **7,351 行（16.2%）生产代码零生产调用**，其单测照样全绿。
- 受管执行的 Runner（`maw __runner`）是**空壳**：拿锁、写 `process.json`、等信号，不启动任何工具子进程。
- `/api/v1` 的**全部写接口**（派发／领取／取消／重试／交付／验证／评审／合入／建任务／登记节点）代码存在但**没有挂载**。
- 真实库中 vNext 新实体只被写入过一次（人工兼容路径），时间轴与统计读的表全空。

差距不在「写得少」，在「没人负责把 A 接到 B 上」。根因是任务卡按**单包 Surface + 单包 Verify** 切分，270 张卡没有任何一张跨越装配层。

---

## 1. 证据一：真实运行数据（`~/.maw/maw.db`）

### 1.1 vNext 实体表行数

| 表 | 行数 | 说明 |
| --- | --- | --- |
| `tasks` | 270 | 全部 done（旧模型） |
| `task_instances` | 300 | 稳定实例已建（含退役快照） |
| `task_versions` | 300 | 定义版本已建 |
| `runs` | **1** | 唯一一条，`started_at`/`finished_at` 均为 NULL |
| `operations` | **1** | 同上 |
| `nodes` | **1** | managed start 隐式登记 |
| `deliveries` | **1** | 同上一次运行 |
| `delivery_verifications` | **1** | 同上 |
| `run_events` | **5** | 标准信封字段齐全，覆盖 reserved→preparing→launching→…→completed |
| `usage_records` | **0** | 计量从未写入 |
| `source_sessions` / `source_turns` | **0** / **0** | 会话／轮次从未采集 |
| `run_source_links` | **0** | 运行与来源未关联 |
| `notification_deliveries` | **0** | 通知从未产生 |
| `projection_state` | **0** | 投影从未推进 |
| `resource_leases` | 2 | 旧占用 |

唯一那条 Run 的 `payload_json` 写着 `{"strategy":"manual_client_compat"}`，创建时间 `2026-10-23T23:29:50Z`，来自人工兼容路径的一次验收样本，**不是**受管执行。

### 1.2 事件类型分布（4,386 条）

```
status_changed 1337 | task_plan_updated 433 | agent_registered 410 | task_created 386
task_updated 356 | claimed 318 | review_auto_approved 309 | merged 284 | progress 243
gate_failed 53 | external_progress 50 | ...
```

全部是**旧版扁平事件**（`events` 表结构仍是 `id/ts/actor/type/task_id/data_json`）。架构 §11.1 要求的标准信封只落在 `run_events`（5 条）。

零条 `run.started` / `run.finished` / `artifact.collected` / `verification.finished` / `turn.started` / `turn.finished` / `usage.reported`。

### 1.3 直接后果

`internal/httpapi/observability/*` 与 `internal/analytics/*` 的 SQL 只读 `run_events` / `usage_records` / `source_turns` / `source_sessions` / `run_source_links` / `deliveries`。这些表除根目录外全空，因此：

- **timeline / activity / sessions / analytics 四个视图在真实库上只能渲染空态**（F34–F43）。
- 前端 10 个视图（`internal/board/assets/views/`）里，有数据的只有 projects / tasks / runs / deliveries / nodes 的旧投影。

---

## 2. 证据二：孤岛模块 7,351 行，单测全绿、生产零调用

判定方式：对所有 `internal/*` 包做「非测试文件的外部 import」统计，再逐个回溯调用链。

| 包 | 生产行数 | 按设计该做什么 | 生产引用方 |
| --- | --- | --- | --- |
| `internal/adapters/**` | 4,598 | 4 家 CLI 接入器（CodeBuddy/WorkBuddy/Trae/ZCode）：探测、构造 LaunchSpec、解事件、解释结果 | **0**（仅 `ports/catalog.go` 白名单字符串提及） |
| `internal/runner/session` | 806 | Runner 主体：起子进程、解码记录、上报事件、交付、心跳 | **0**（连 import 都没有） |
| `internal/engine/matching` | 595 | 按能力／容量／依赖／策略选节点（F14） | **0** |
| `internal/engine/reconcile` | 549 | 取消、重试、未知状态核实（F19/F21） | **0** |
| `internal/retention` | 573 | 数据保留策略与清理（F47） | **0**（`maw prune` 自己另写了一套） |
| `internal/runner/recovery` | 230 | 中枢重启后的状态对账（架构 §8 表） | **0** |

补充：`internal/observability` 的 `Service` 四个**写入**方法（`EnsureSession` / `EnsureTurn` / `LinkRun` / `ObserveUsage`）在生产代码中零调用；`notifications.Service.Raise`（通知产生）零调用，只有读与 ack 在 HTTP 层被使用。

验证命令（本次实跑，全绿）：

```sh
CGO_ENABLED=0 go test ./internal/engine/matching ./internal/engine/reconcile \
  ./internal/retention ./internal/runner/session ./internal/adapters/codebuddy \
  ./internal/engine/dispatch -count=1
# ok × 6
```

**这 7,351 行是「已验收但未启用」的代码**：符合各自任务卡的验收标准，但离 F13/F14/F19/F21/F34–F43/F47 的用户可见能力差一层装配。

---

## 3. 证据三：装配层四处断点

### 3.1 `maw __runner` 不启动任何工具进程

`internal/app/managed_cmd.go:235` `InternalRunner`：校验令牌 → 取 `runner.lock` → 写 `process.json` → `signal.Notify` 后 `<-ch` 阻塞。

全文没有任何 `session.New(...)`、没有 adapter 实例化、没有子进程。也就是说 `maw managed start` 的实际效果是：**领取任务 + 建 Run/Operation + 拉起一个什么都不做的进程**。架构 §17.2「原子领取 → 工作目录准备 → Runner 监督 CodeBuddy → 收取 Manifest」整条链只有头尾。

### 3.2 `/api/v1` 写接口未挂载

- `internal/httpapi/commands/handler.go` 注册 9 条写路由：`dispatches`、`claims`、`tasks/{id}/cancellations`、`tasks/{id}/retries`、`runs/{id}/cancellations`、`runs/{id}/deliveries`、`deliveries/{id}/verifications`、`tasks/{id}/reviews`、`tasks/{id}/merges`。
- `internal/httpapi/resources/handler.go` 注册 5 条：建任务、改任务、登记节点、改节点、节点探测。
- `internal/app/board_cmd.go:138` 的 `v1` 分流只有三类：`v1Events` / `v1Observability` / `v1Read`。

**commands 与 resources 两个 handler 从未被任何代码实例化**。F04（界面建任务）、F13（界面派发）、F19（界面取消）在 HTTP 面上是 404。

### 3.3 `maw next` 与 `maw managed start` 是两套领取

架构 §6 要求两者都调 `ClaimExecution`。实际：

- `maw next` → `internal/app/worker_cmd.go:288` `pickClaimTarget` + `:313` `commitClaim`，直接 `UPDATE tasks ... WHERE status='ready' AND revision=?`。
- `maw managed start` → `internal/engine/claim` 的 `Claim`，同事务建 Run + Operation + lease。

两者只共享 `internal/schedule` 的选题规则，**不共享领取事务**；且 `next` 不建 Run/Operation，所以人工链路的交付没有 Run 可绑（`worker_cmd.go:1044` 注释自称「共用同一套占用与冻结语义」，代码上不成立）。F15「人工领取与自动派发使用同一队列」未达成。

### 3.4 CLI 命令面只有 managed start/status

实际 `maw --help` 命令清单：`agents/block/board/doctor/init/managed/merge/next/plan/progress/prune/register/review/serve/status/submit/task/unregister`。

没有 `maw dispatch`、`maw node`、`maw run`、`maw delivery`。架构 §12.1 的资源与动作，除 managed start 外全部只能靠 HTTP（而 HTTP 写接口又没挂）。

---

## 4. 证据四：F01–F48 分组状态

| 功能域 | 代码层 | 真实可用 | 缺口 |
| --- | --- | --- | --- |
| 工程／任务／工作区（F01–F07、F22–F25） | 完整 | ✅ 旧人工链路 verified | 界面建任务、非 Git 验收（experimental 且依赖 auto） |
| 节点／派发（F08–F16） | 完整 | ⚠️ 只读可看，不能管 | 节点写接口未挂、matching 孤岛、next 未统一 |
| 执行控制（F17–F21） | 完整 | ❌ Runner 空壳 | 无真实子进程，取消／重试／恢复全未接线 |
| 工作台／时间轴（F30–F38） | 页面 10 个 | ❌ 四个视图数据为空 | 无 run/turn 事实，通知 Raise 零调用 |
| 统计／历史／设置（F39–F48） | 查询完整 | ⚠️ 只有旧链路统计 | usage 无生产者、retention 孤岛 |

架构 §12.1 列出的 13 类接口中：**9 类只有 GET 生效**（projects/tasks/nodes/runs/operations/deliveries/events/timeline/analytics），4 类 POST（dispatches/claims/cancellations/deliveries/verifications/reviews/merges/observations）代码在但未挂。

---

## 5. 做得对的部分（避免误判为「全是坑」）

1. **能力分级是诚实的**：`internal/app/runtime.go:264` 的注册表把 9 项能力分 verified（人工领取／CodeBuddy 来源／观测 API）、experimental（ZCode/Trae/WorkBuddy/自动派发/非 Git 工作区）、unsupported（受管模型调用）；`unsupported` 任何配置都开不起来，`experimental` 默认跟随 `MAW_EXTENDED`。README 的「Not supported (no evidence yet)」与代码一致。
2. **来源证据是真的**：`docs/evidence/codebuddy/` 有 S01–S08 实测样本 + 4 条合成反例，能力矩阵带 `cli_version=2.173.0` 与失效条件（版本/入口/平台变化即降级）。`result.exit_zero_with_is_error` 这条正好覆盖架构 §5.3 的假 done 风险。
3. **契约层四件套齐全**：`docs/contracts/vnext/` 下 task/execution/ports/http/delivery/observability/ui 七个域都有正文 + 纯标准库校验器 + samples + 四向单测。
4. **旧链路极稳**：270 题、4,386 事件、0 残留占用、`maw prune` 有 unknown 占用保护。生产可用的部分没有退化。

---

## 6. 根因

任务卡的 `Surface / write_scope` 全部是**单个包**（例：VN-204 `internal/engine/dispatch/**`、VN-207 `internal/engine/matching/**`），`Verify` 也全部是**该包单测**。

后果是每张卡都能合规交付，但没有任何一张卡的验收标准要求「被应用层调用」或「产生一条真实数据」。于是：

- 拆分维度是「模块」，验收维度也是「模块」，**没有一张卡对端到端负责**。
- VN-204 的 Non-goals 明确写「不启用自动选题循环」，VN-207 写「在受管闭环验证后提供」——这些措辞在卡层面正确，但「受管闭环验证」本身没有被任何卡收口。
- 装配工作（app/httpapi 层）落在所有卡的 Surface 之外，`maw request-change` 也没人发。

这不是执行失误，是拆卡方式的结构性缺口：**按层拆、按层验，就必然缺总装**。

---

## 7. 建议补齐的 5 张装配卡（验收以真实数据为准）

| 卡 | 内容 | 验收判据（必须能在真实库上看到） |
| --- | --- | --- |
| RUNNER-WIRE | `InternalRunner` 内实例化 `session.Runner` + CodeBuddy adapter，按 RunSpec 起真子进程、解事件、上报、交付 | `runs.started_at/finished_at` 非空；`run_events` 出现 `run.started`/`run.finished`；`usage_records > 0` |
| HTTP-WIRE | 把 `httpapi/commands` 与 `httpapi/resources` 挂进 `board_cmd` 的 v1 分流，接 `localauth` 与 Idempotency-Key | `POST /api/v1/projects/{id}/dispatches` 返回 202 + Operation 可查；未授权返回 401 |
| CLAIM-UNIFY | `maw next` 改为调 `engine/claim.Claim`，保留旧返回形状与兼容字段 | `maw next` 也产 Run+Operation；两 Agent 同时 next 只有一人赢且另一人看到 `claimLost` |
| METER-WIRE | Runner 交付时调 `meter.ObserveUsage` / `LinkRun`；`session`/`turn` 落 `source_sessions` | `source_turns > 0` 且 timeline 出现 `agent_turn` 区间；同 Turn 多 Run 不重复计量 |
| DISPATCH-LOOP | `maw serve` 内常驻 `Coordinator.Step` 循环（`auto_enabled` 控制），支持撤回排队意图 | 不手敲 CLI，排队 Operation 自动推进；两个 serve 不会同时成为协调者 |

依赖顺序：RUNNER-WIRE → HTTP-WIRE → CLAIM-UNIFY → METER-WIRE → DISPATCH-LOOP。

前置建议：给 `tests/managed/loop_test.go`、`tests/resilience/gates_test.go` 补 `requireDarwinProcessTools` 同款跳过守卫，否则本机 `go test ./...` 门禁会因沙箱禁止 `/bin/ps` 恒红，装配卡的验证无法在门禁内完成（详见 `.workbuddy/memory/MEMORY.md`）。

---

## 8. 一句话给决策

**先把「一条真实的受管 Run」跑出来，再谈统计和多工具。** 现在最贵的不是再写功能，是把已有的 7,351 行孤岛接上并让第一条真实数据落库——那条数据一出现，F17–F21、F30–F43 才有验证对象。
