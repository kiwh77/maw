# Pi 端到端验收证据（PI-901）

范围：目标 → 计划 → 批准入队 → Worker 执行 → 整体验收 → 返工 → 计量，以及
429／断流／缓存／取消／重复回放／写库失败／并发／恢复的受控样本。

**结论**：maw 侧的协议、事件边界、终态判定、用量归一、去重、归属与统计链路已用
**真实进程 + 真实 journal + 真实 SQLite** 跑通；**真实供应商 API 未验**（本机无授权
凭据），未验项逐条登记在 §4，按 §5 的步骤可补验。

机检入口：

```sh
go test ./tests/resilience -run Pi -count=1 -v
```

## 1. 环境与方法

| 项 | 值 |
| --- | --- |
| 平台 | macOS（darwin），Node ≥ 22.19 |
| 隔离工程 | `tests/testsupport.NewHarness`：临时 git 仓 + 临时 `MAW_GLOBAL`，不触碰真实主干 |
| 运行时 | **替换运行时**：测试内生成的 node 脚本 `runtimes/pi/src/main.mjs`（协议与事件形状同 `maw.pi.execution/v1`） |
| 被替换的部分 | 仅模型流：供应商请求与真实 token 计数 |
| 未替换的部分 | Pi 适配器 `internal/adapters/pi`、`session.Runner`、journal、`dbstore`、SQLite、`analytics` 统计层 |

替换运行时只决定「发什么事件」，解码、模式回执校验、终态映射、用量归一、去重与
归属全部走生产实现——因此这些结论对真实供应商同样成立；供应商响应本身不在本次证据内。

## 2. 已验项（机检）

| 验收点 | 测试 | 证据落点 |
| --- | --- | --- |
| Worker：真实子进程 → 适配器 → journal → SQLite；逐次用量按 `worker + run + 节点` 归属 | `TestPiWorkerSessionWritesAttributedUsage` | `usage_records`（`purpose=worker`、`run_id`、`node_id=pi-1`、`input=120/output=30`、`cache_read=40`） |
| 同一调用重放不记第二笔 | 同上（同一 run 跑两次） | `usage_records` 行数 = 1 |
| 统计层能按用途看到真实用量（看板数据层） | 同上 | `analytics.Attribution(dimension=purpose)`：`worker` 一行、token 120/30、覆盖 1/1 |
| 目标→计划：Pi Lead 产出计划卡并落为 proposed 修订 | `TestPiLeadPlanApproveEnqueueAndRework` | 计划修订 rev1、`status=proposed` |
| 批准入队：`auto_enqueue` 后卡进入蓝图 | 同上 | 任务 `PA-101`/`PA-102` 存在 |
| 集成失败 → 返工提案带失败证据 | 同上 | 整体验收 `fail`、返工修订 rev2 |
| 返工不动旧修订、不改蓝图、不创建卡；再次批准才入队 | 同上 | 蓝图字节不变、rev1 摘要不变、`PA-201` 批准后才出现、原卡保留 |
| 429：不得判成功 | `TestPiControlledSamples` | `Interpret` → `failed` |
| 取消：终态 `cancelled` | 同上 | `Interpret` → `cancelled` |
| 断流（无 result + 非零退出）：`failed` 且质量降级 `estimated` | 同上 | `Interpret` → `failed/estimated` |
| 退出码 0 但无 result：保持 `unknown`（不猜成功） | 同上 | `Interpret` → `unknown` |
| 缓存字段独立记账、缺归一证据记 `unverified` | 同上 | `usage_records.cache_state=unverified` |
| 重复回放幂等 | 同上（同一事实写两次） | 行数 = 1 |
| 写库失败：运行不受影响，缺口上报且原文保留；补录幂等 | `TestPiObservationWriteFailureAndBackfill` | `ObservationFailures>0`、`ObservationBacklog` 非空、补录后行数 = 1、二次补录仍 = 1 |
| 并发：两次运行互不污染、各自归属 | `TestPiConcurrentWorkerRunsKeepUsageSeparate` | 两个 `run_id` 各 1 行 |
| 两条来源不得互相解释（协议格式/版本不等即拒绝） | `TestPiProtocolRejectsOtherSources` | `Decode` 返回可读拒绝原因 |
| 观测恢复（补录）幂等 | `TestPiObservationWriteFailureAndBackfill` | 同一去重身份只留一行 |
| Run 级恢复／占用核实 | 不在本卡重复：`TestSH900ReconcileReleasesConfirmedKeepsUnknown`（PI-304 波） | `system_jobs` → reconcile 结论 |

## 3. 数据库与看板对账

- 事件：journal 原文 + `run_events`／`source_turns`／`usage_records` 由同一批事实产生；
  观测失败不改运行结论（只计缺口），补录按去重身份幂等。
- 归属：`usage_records` 的 `project_id / node_id / run_id / purpose / call_id / session_id`
  全部来自 maw 绑定；同一轮在 `run_source_links` 留关联，统计层的调用分母由此而来。
- 看板：本次验到**看板数据层**（`analytics` 归属报表按用途维度返回 worker 用量与覆盖）；
  浏览器截图与真实数据下的 UI 呈现见 §4 未验项。

## 4. 未验项（真实供应商 API）

| 项 | 未验原因 | 影响面 |
| --- | --- | --- |
| 真实供应商响应与字段形状 | 本机无授权凭据（`PI_API_KEY`） | 能力矩阵中 `unknown` 条目不得当成已知使用 |
| 真实 429／配额拒绝 | 需要真实限流现场 | 生产限流时的事件与重试行为 |
| 真实 token 计数与缓存归一 | 需要供应商 usage 原值 | `observed` 与 `estimated` 的边界 |
| 真实断流／半截流 | 需要网络中断现场 | `journal` 补录与 `unknown` 终态 |
| 真实取消时序（SIGTERM／退出确认） | 需要真实长任务 | `cancelled` 的停止证据 |
| 成本估算 | 未配置价目表 | 报表只报 token，不编单价（已按设计留空） |
| 真实窗口下的看板截图 | 依赖真实数据与浏览器 | UI 呈现（数据层已验） |

以上任一项在拿到凭据后按 §5 复走，替换运行时不需要改 maw 代码。

## 5. 补验步骤（有凭据时）

```sh
export PI_PROVIDER=... PI_MODEL=... PI_API_KEY=...     # 只进环境白名单，不进 argv/日志
export MAW_PI_RUNTIME="$PWD/runtimes/pi/src/main.mjs"  # 用真实运行时替换测试替身
export MAW_PI_NODE="$(command -v node)"
go test ./tests/resilience -run Pi -count=1 -v
```

对着真实运行时重跑同一批用例：§2 的机检项应保持通过；再补 §4 的现场样本
（限流、断流、取消）并在此文件登记新的证据位置。真实 API 证据里不得出现密钥原文，
只记录脱敏后的类别与计数。
