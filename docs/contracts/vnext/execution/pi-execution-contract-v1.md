# Pi 执行与计量契约 v1（冻结）

契约标识：`maw.pi.execution/v1`
上游契约：`maw.runtime.state/v1`（`./runtime-state-contract-v1.md`，Run 阶段／核实／占用／事件信封）、`maw.task.instance/v1`（`../task/`，实例与定义版本）、`maw.observability.timeline/v1`（`../observability/timeline-metering-contract-v1.md`，轮次与用量口径）、`maw.delivery.manifest/v1`（`../delivery/`，终态之后怎么判交付）、`maw.app.ports/v1`（`../ports/`，`Source`／`Decode`／`Interpret` 端口与事务边界）。
状态：**冻结**（本卡 PI-001 定稿）。PI-101…PI-103、PI-201、PI-305、PI-901 以它为引用基线；改含义只能出 v2。

范围：Pi 运行时的**协议版本、三种模式、身份绑定、事件边界、工具授权、终态与停止**，以及**逐次调用／运行级用量、缓存归一、缺失值与估算、重放键**。**不写业务代码，不建表，不改 `internal/runner`、`internal/adapters`。**

## 0. 为什么需要这份契约

现状的来源解码与计量是「一个厂商一套解释」：

1. **版本靠猜。** `SourceFor` 只认实测过的协议（`internal/runner/session/source.go:20`）：`IsCodeBuddyProtocol` 要求 `format=stream-json` 且版本严格相等（`internal/runner/session/source.go:43`），对不上就走 `PassthroughSource`——只按退出码判定。新增 Pi 时若不先冻结协议标识与版本，同一条输出会被两条来源各自解释。
2. **模式没有边界。** Worker、Lead、诊断复用同一个 SDK 是目标，但「Lead 能不能写盘」「诊断能不能改任务状态」现在只存在于调用方约定里，没有可核对的授权表。一个模式跑错，后果是改错文件或给出越权结论。
3. **事件与阶段混在一起。** SDK 的中间消息（assistant 分片、工具调用、心跳）与 Run 的阶段（`launching`→`running`→`completed`）不是一回事。若不划边界，厂商内部步骤会被当成运行阶段写进 `runs.phase`，`unknown` 与「还在跑」就分不开。
4. **用量口径不统一。** 一次运行可能既有逐次调用的 usage，也有整次运行的汇总；缓存字段是否已含在 `input_tokens` 里，各来源不同；拿不到用量时是否填 0，决定了报表能不能对账（契约 T142 已明确「未知不得填 0」，但 Pi 的字段映射没冻结）。

本契约把 Pi 拆成**协议（版本／消息）→ 模式（授权）→ 事件（身份／边界）→ 终态（证据）→ 计量（逐次／运行级／重放）**五段，每段可单独验收。

## 1. 三层边界

| 层 | 负责 | 不负责 | 位置 |
| --- | --- | --- | --- |
| Pi 运行时（`runtimes/pi`） | 启动 SDK、执行模型请求、执行**被授权**的工具、输出结构化流、自报版本、泄漏面控制 | 改任务状态、判交付、决定阶段、写蓝图 | PI-101 新增 |
| maw 适配器（`internal/adapters/pi`） | 解析流、判定阶段与错误类别、产出用量观测、映射终态 | 解释厂商内部语义之外的字段、补造缺失值 | PI-102 新增 |
| maw Runner（既有） | 起停进程、阶段推进、占用与释放、事件入库、交付判定 | 理解 Pi 的工具协议 | `internal/runner/session`、`internal/runner/process` |

一条硬规则：**运行时只描述事实，maw 才改状态**（对齐 `maw.app.ports/v1`：状态写入在事务内，进程与 Git 在事务外）。

## 2. 版本与协议标识

| 项 | 冻结值 | 说明 |
| --- | --- | --- |
| 契约版本 | `maw.pi.execution/v1` | 本文件；事件与观测记录都带 `contract` |
| 协议标识 | `format = "pi-stream-json"`、`version = "1"` | 与 CodeBuddy 的 `stream-json/1` **不同名**：两条来源不得互相解释 |
| SDK 版本 | `pinned_sdk_version`，形如 `X.Y.Z` | 由 `runtimes/pi/package-lock.json` **精确锁定**；禁止 `^`／`~`／`latest`；首次锁定值由 PI-101 写入能力矩阵 |
| 协议与 SDK 解耦 | 协议版本只随**消息形状**变化 | SDK 换号不自动等于协议换版本；换号必须重跑能力矩阵，协议不变则仍走 v1 |
| 运行时二进制版本 | 与发布包 `checksums.txt` 一致 | 安装后探测（PI-103） |

启动前必须校验、任一项不符**拒绝启动**（不回退猜兼容）：

```text
expected_protocol == {format:"pi-stream-json", version:"1"}   // LaunchSpec.ExpectedProtocol
runtime_reported.sdk_version == pinned_sdk_version            // 运行时 --version 探测
runtime_reported.mode == requested_mode                       // 运行时回显，见 §3
```

对齐既有口径：`IsCodeBuddyProtocol` 的「大概是兼容的不算兼容」在这里升级为**三项全等**。拒绝启动必须留下可读原因（缺运行时／版本不匹配／模式不匹配），不得静默降级成 `PassthroughSource` 后照样跑。

证据要求（PI-101／PI-103 交付时）：

- `docs/evidence/pi/capability-matrix.json`：逐条列「字段／消息／行为」与 `observed|unknown`，unknown 的条目不得在实现里被当成已知使用；
- `docs/evidence/pi/samples/`：成功、失败、取消、缓存、无用量五类原始样本（脱敏），每条结论都要能指回样本。

## 3. 三种模式

一个进程只服务一种模式；模式在启动参数里显式声明，运行时必须**回显**且与请求一致。跨模式一律新进程——同一上下文里既规划又执行，授权就无法单独撤销。

| 维度 | `worker` | `lead` | `diagnose` |
| --- | --- | --- | --- |
| 用途 | 在受管 worktree 内执行一张已领取的卡 | 读目标与证据，产出结构化计划／返工提案 | 系统 Agent 诊断，产出既有决策协议 |
| 可写盘 | 仅 `write_scope` 内 | **否** | **否** |
| 文件工具 | 读全仓 + 写 scope 内 | 只读（`read_scope` + 必要上下文） | 只读（诊断所需上下文） |
| 终端／命令工具 | 按节点配置启用，受 scope 约束 | **否** | 仅只读白名单（§6） |
| 网络工具 | 默认关；开启须显式配置并记入审计 | 默认关 | 默认关 |
| 结构化输出 | 终态 + 产物候选（`InterpretResult`） | 计划卡数组（`maw.lead.goal/v1` §6） | `Action` 七值 + 证据（`maw.lead.selfheal/v1` §3） |
| 可改任务／Run 状态 | **否**（只能由 maw 落） | **否** | **否** |
| 可领题／合入 | 否（领取由 maw claim） | 否 | 否 |
| 用量归属 | `usage_kind=worker` + task/run | `usage_kind=lead` + goal/plan_rev | `usage_kind=diagnose` + job/action |
| 终态要求 | 必须有 `result`（§7） | 必须有 `result`，payload 为计划卡 JSON | 必须有 `result`，payload 为决策 JSON |

模式违规的处置：运行时检测到越权请求（例如 `lead` 请求写文件）必须**拒绝并上报 `tool_denied`**，运行继续；maw 侧把 `tool_denied` 记进证据，不因一次拒绝就判 `failed`——除非该工具是完成任务的必要条件（此时终态为 `failed(reason=tool_denied)`）。

## 4. 身份与绑定

每次启动由 maw 绑定，运行时只负责回显与在事件里携带：

| 身份 | 来源 | 用途 |
| --- | --- | --- |
| `project_id` | 工程 | 隔离与统计 |
| `task_instance` / `task_version` | 领取时冻结（`maw.task.instance/v1`） | 交付与验收绑定，重跑不得换版本 |
| `run_id` / `generation` | Runner | 条件写入与释放判据（E243） |
| `node_id` / `mode` | 节点登记 + 本次启动 | 能力归属与计量口径 |
| `workspace` | 受管 worktree 绝对路径 | 写盘边界 |
| `session_id` | 运行时／来源侧 | 与 `source_sessions` 对齐 |
| `call_id` | 运行时为每次模型请求生成 | 逐次用量的稳定身份 |

约束：

- `call_id` 必须在**一次 Run 内唯一且可重放**（同一请求重试产生新 `call_id`，不得复用旧号冒充同一调用）。
- 事件必须带 `run_id` + `generation`；缺任一项的事件视为不可归属，不得入库（E263 同源）。
- Lead／诊断的调用**独立归属**：`goal_id` + `plan_rev`（Lead）或 `job_id` + `action`（诊断）随用量落库；不得并进 Worker 的 Run 用量（`maw.lead.goal/v1` §10 第 1 条）。
- 身份不得由模型自述决定：模型输出里的任何「我是谁／我这轮属于哪题」都不作为绑定依据。

## 5. 事件边界

### 5.1 三类流，三个去处

| 运行时输出 | 去处 | 是否改阶段 | 说明 |
| --- | --- | --- | --- |
| `session_started` / `session_ended` | `runs.phase_history`（经 Runner 判定） | 是（仅 `launching→running`／终态） | 运行时只说「进程起来了／结束了」，阶段由 Runner 定 |
| `stage_hint`（如 `thinking`、`tool_use`） | journal（原始证据） | **否** | SDK 内部进度不是运行阶段 |
| `tool_call` / `tool_result` | journal；节点配置要求详细证据时另进 `run_events` | **否** | 工具步骤不得写成 `runs.phase` |
| `usage` | `usage_records` + `source_turns` | **否** | 见 §8–§12 |
| `error` | `run_events` + `InterpretResult` 证据 | 是（只经 Runner） | 必须带错误类别（§7.2） |
| `result` | `run_events` + `InterpretResult` | 是（唯一终态来源） | 见 §7 |
| `version_report` | `run_events`（一次性） | 否 | 与 §2 的期望串比对 |
| 其他未知消息 | journal | 否 | **不得静默丢弃**，也不得据它改状态 |

边界规则：

1. **一个终态只由一条 `result` 产生**；`error` 只能触发 `failed`，且必须带证据（E230）。
2. 未知消息类型按 `unknown_message` 记 journal，计数进证据；未知即未知，不猜语义。
3. 事件身份 `event_id` 全局唯一（E212）；同一 `record_uuid` 重复到达只入一次。
4. 时间：`occurred_at` 与 `received_at` 分开记录，入库顺序以 `cursor` 为准（E213）；`occurred_at > received_at` 必须降级为 `estimated`，不得标 `observed`（E262）。
5. 阶段推进只写在 `run_events` + `runs.phase` 同事务里（E260），运行时不直接写库。

### 5.2 消息信封

```jsonc
{
  "contract": "maw.pi.execution/v1",
  "event_id": "pe_01J9Z3K4",
  "type": "usage",                  // session_started|stage_hint|tool_call|tool_result|usage|error|result|version_report|unknown_message
  "run_id": "run_a001",
  "generation": 3,
  "call_id": "pc_01J9Z3K4",         // usage / tool_* / error / result 必填
  "occurred_at": "2026-11-18T02:41:09.120Z",
  "received_at": "2026-11-18T02:41:09.180Z",
  "payload": { }
}
```

## 6. 工具授权

授权是**执行边界**的事，不是提示词的嘱咐：

| 工具类别 | `worker` | `lead` | `diagnose` |
| --- | --- | --- | --- |
| 读文件 | 仓库内 | `read_scope` + 显式上下文 | 诊断上下文 + 只读仓 |
| 写／改文件 | 仅 `write_scope` 命中 | 拒绝 | 拒绝 |
| 执行命令 | 节点配置允许的命令 | 拒绝 | 白名单只读探测（`git status`、`go env` 一类，见 `ops_allowlist` 口径） |
| 访问网络 | 默认关 | 默认关 | 默认关 |
| 读取环境变量 | 仅 `EnvAllowlist` | 同左 | 同左 |

不变量：

- **写盘越界必须失败**：命中 `write_scope` 之外的写入请求由运行时拒绝，并上报 `tool_denied{reason:"out_of_scope", path}`。不得「先写了再让人 review」。
- **凭据不进工具进程**：模型密钥只出现在运行时进程的环境白名单里；工具子进程的 env 必须剔除密钥前缀（`*_API_KEY`、`*_TOKEN`、`*_SECRET`），argv 里不得出现密钥，journal 与事件里不得出现密钥原文（红线：本机令牌只允许出现在 Authorization 头或 0600 文件）。
- **禁止拼 shell**：启动与工具执行都用 argv 数组，不经 `sh -c`（`LaunchSpec.Validate` 已拒绝 shell 解释器）。
- **授权不可热改**：一次 Run 内工具集合固定；要放宽只能开新 Run，旧 Run 的证据按旧授权解释。
- **拒绝要留证据**：每次 `tool_denied` 都进 journal + 计数，供评审判断「是模型乱试还是授权配错」。

## 7. 终态与停止

### 7.1 终态映射

| 现场 | 终态 | 必要条件 |
| --- | --- | --- |
| `result.ok=true` 且带结果文本／产物候选 | `completed` | `result` 事件 + 非空证据 |
| `result.ok=false`，错误类别可识别 | `failed` | 错误类别（§7.2）+ 原始错误摘要 |
| 进程崩溃／非零退出且无 `result` | `failed(crash)` | 退出码 + stderr 摘要（退出码单独不算证据，E333） |
| 认证失败／配额拒绝 | `failed(auth)` / `failed(quota)` | 来源错误码或消息原文 |
| 取消请求后确认退出 | `cancelled` | 停止证据（E231） |
| 超时且确认退出 | `timed_out` | 超时预算 + 停止证据 |
| 进程可能仍在但查不清 | `unknown` | **不落终态**（E232），占用保留 |

硬规则：

- 退出码 0 不能单独产生 `completed`（对齐交付契约 E333）；必须有 `result`。
- 崩溃不得被 `completed` 覆盖；`error` 与 `result` 同时到达时，**终态以 `result` 为准**，但必须把 `error` 记进证据。
- 终态不可逆（`maw.runtime.state/v1` §2）；重跑一律新 Run、新 `generation`。

### 7.2 错误类别（封闭集合）

`auth` / `quota` / `rate_limit` / `timeout` / `tool_denied` / `invalid_output` / `crash` / `env` / `unknown`。

- `env` 只用于环境类失败（缺可执行文件、权限、`EPERM`），与业务失败分开，便于自愈链路走 `unblock_assist`（`maw.lead.selfheal/v1` §2）。
- `unknown` 不得被下游当成可重试：先补证据，再决定重派。
- 错误事件 payload：`category`（必填，取上表）、`message`（脱敏原文，不含密钥）、`retryable`（可选布尔；`unknown` 类别不得标 `true`）。

### 7.3 停止

```text
请求停止 → 记 desired_action=stop（不改 phase）
        → 等进程退出确认（grace 期）
        → 确认已退出：落 cancelled/timed_out + 释放判据按 runtime 契约
        → 未确认：phase 不变，reconciliation_state=unknown，占用保留
```

不杀进程组之外的任何进程；停止失败本身要留事件，禁止重试到把证据洗掉。

## 8. 逐次调用用量（call-level）

一次模型请求 = 一条调用事实。字段与 `internal/runner/observe` 的 `Facts`、`usage_records` 对齐：

| 字段 | 必填 | 规则 |
| --- | --- | --- |
| `call_id` | 是 | Run 内稳定唯一；与 `turn_id` 的对应关系见 §11 |
| `scope` | 是 | `call`（本层）／`run`（§9） |
| `semantic` | 是 | `delta`（当前唯一合法值；`cumulative` 只在实测到累计口径时启用） |
| `input_tokens` / `output_tokens` | 否 | 非负整数或 `null`；`null` 表示来源没给，**不得填 0** |
| `cache_read_tokens` / `cache_write_tokens` | 否 | 单独字段，**不得并入 `input_tokens`** |
| `total_tokens` | 否 | 出现时必须 = `input + output`（不含缓存字段） |
| `quality` | 是 | `observed`／`estimated`／`unknown`；`estimated` 必须带 `basis` |
| `occurred_at` | 是 | 调用结束时刻；与 `received_at` 分开 |

规则：

- 只解析 `result` 记录携带的 usage；assistant 中间消息上的 usage **不解析、不求和**（CodeBuddy 实测口径 C19 同源：中间记录语义未验证）。
- 一次调用只有一条用量事实；重复投递按 §11 去重，不产生第二条。
- 拿不到任何 token 字段时不写用量行，只记「调用发生过」（`turn_count` 仍计一次，见 §10）。
- 缓存字段若来源只给合并值（`input` 含缓存但没分解），`quality=unknown`，不得自行估算拆分。

## 9. 运行级用量（run-level）

两类事实**分开存放、不得相加**：

| 事实 | `scope` | 何时产生 | 用途 |
| --- | --- | --- | --- |
| 逐次调用 | `call` | 每次模型请求 | 主口径：归属、去重、覆盖率 |
| 运行汇总 | `run` | 来源自报整次运行用量（如 `result.usage` 只有一条汇总） | 仅当**没有** call 级证据时的兜底 |

规则：

1. **优先 call 级**；同一 Run 同时存在两类证据时，报表只取 call 级，`run` 行标 `superseded=true` 的等价语义（记录保留，不参与汇总）。
2. `run` 级不得按「调用次数」重复累加。
3. Run 级用量必须绑定 `run_id` + `generation`；跨代际不得合并。
4. 覆盖率分母是**该 Run 内的调用数**：`calls_with_usage / calls_total`；分母为 0 时返回 `null`，不是 0（`maw.observability.timeline/v1` §4.3 同源）。

## 10. 缓存归一、缺失值与估算

### 10.1 缓存

```text
total_tokens = input_tokens + output_tokens        // 缓存字段不参与
cache_read_tokens / cache_write_tokens             // 独立记账，独立展示
input_tokens 已含缓存分解这一前提必须有实测证据；没有证据就按 §10.2 降级
```

- 禁止 `input + output + cache_read + cache_write` 求总量：重复计量比缺失更难查。
- 缓存类比率必须写明分子与分母（例如命中率、写缓存占比），并标 `basis`；没有分母的百分比一律视为不可比较。

### 10.2 缺失、估算与未知

| 情况 | 处理 |
| --- | --- |
| 来源没给 token | 字段 `null`，`quality=unknown`，**不填 0**；计入 `turn_count`，不进 token 总量 |
| 来源给的是字符串／浮点／负数 | 视为格式错误：`quality=unknown` + `invalid_usage` 证据；不猜数值 |
| 能从上下文长度与单价推导 | `estimated` + 必填 `basis`（公式与输入来源）；不得伪装 `observed` |
| 累计值下降 | 新代际：`epoch+1` + `reset_observed=true`；增量按 `max(0, current - previous)`（计量契约 §4.1） |
| 同一轮服务多道题 | 只记一次；`shared_with[]` 非空；分摊之和必须等于总量，不得放大（§4.2） |

## 11. 重放键与补录

- **去重身份**：`(source_ns, turn_id, epoch, scope)`；`usage_records` 已按这四项建唯一约束，写库必须冲突即忽略或幂等更新，不得插入第二条。
- `turn_id` 由 `source_ns + session_id + record_uuid` 组合（与 `source_turns` 的唯一键一致）；**不得由模型或时间戳临时生成**。
- `usage_id` 从去重身份派生（确定性），重放得到同一 id。
- 事件级去重：`event_id` 唯一；`record_uuid` 重复到达只入一次。
- 补录：DB 短暂失败时**保留 journal 原始记录**，失败计数上报，后台按重放键补写；补录顺序无关，必须幂等。补录不得改变既有 `epoch` 与 `scope`。
- 禁止「先写 0 再补」：0 是实测为零，补录前就该是 `null`。

## 12. 样例

### 12.1 成功（worker，逐次调用）

```jsonc
{ "contract": "maw.pi.execution/v1", "event_id": "pe_01", "type": "usage", "run_id": "run_a001",
  "generation": 1, "call_id": "pc_01", "occurred_at": "2026-11-18T02:41:09.120Z",
  "received_at": "2026-11-18T02:41:09.180Z",
  "payload": { "scope": "call", "semantic": "delta", "input_tokens": 1200, "output_tokens": 340,
    "cache_read_tokens": 800, "cache_write_tokens": 0, "total_tokens": 1540, "quality": "observed" } }
```

### 12.2 失败（认证，无用量）

```jsonc
{ "type": "error", "payload": { "category": "auth", "message": "401 invalid api key (redacted)",
  "retryable": false } }
{ "type": "result", "payload": { "ok": false, "evidence": ["exit_code:1", "category:auth"] } }
```

### 12.3 取消（确认退出）

```jsonc
{ "type": "session_ended", "payload": { "reason": "stop_confirmed", "signal": "SIGTERM",
  "exit_code": 143, "stopped_at": "2026-11-18T02:45:00Z" } }
```

### 12.4 缓存与未知混用

```jsonc
{ "payload": { "scope": "call", "semantic": "delta", "input_tokens": 900, "output_tokens": null,
  "cache_read_tokens": null, "cache_write_tokens": null,
  "quality": "unknown", "basis": "来源只给了 input_tokens；output 与缓存缺失" } }
```

### 12.5 运行级兜底

```jsonc
{ "payload": { "scope": "run", "semantic": "delta", "input_tokens": 5200, "output_tokens": 900,
  "total_tokens": 6100, "quality": "observed", "basis": "result.usage 汇总；无 call 级证据" } }
```

## 13. 不变量登记表

| 码 | 判定对象 | 不变量 |
| --- | --- | --- |
| P100 | 全记录 | 必填字段存在且类型正确；时间可解析为 ISO8601 |
| P101 | 根节点 | `contract` 必须是 `maw.pi.execution/v1` |
| P110 | 协议 | 只有 `format=pi-stream-json` 且 `version=1` 才由 Pi 适配器解释 |
| P111 | 版本 | SDK 版本必须精确锁定为 `X.Y.Z`；运行时自报与期望不符必须拒绝启动 |
| P112 | 版本 | 拒绝启动必须给出可读原因，不得降级为 Passthrough |
| P120 | 模式 | 只允许 `worker`／`lead`／`diagnose`；运行时回显必须与请求一致 |
| P121 | 模式 | 一个进程只服务一种模式；切换模式必须新进程 |
| P130 | 身份 | 事件必须带 `run_id` 与 `generation`，否则不得入库 |
| P131 | 身份 | `call_id` 在 Run 内唯一；重试必须是新 `call_id` |
| P132 | 归属 | Lead／诊断用量必须独立归属（goal/plan_rev 或 job/action） |
| P140 | 事件边界 | SDK 内部消息不得直接改 `runs.phase` 或 `tasks.status` |
| P141 | 事件边界 | 终态只能由一条 `result` 产生；`error` 只能触发 `failed` 且带证据 |
| P142 | 事件边界 | 未知消息入 journal 并计数，不得静默丢弃 |
| P143 | 事件边界 | `event_id` 唯一；同一 `record_uuid` 只入一次 |
| P144 | 事件边界 | `occurred_at > received_at` 必须降级为 `estimated` |
| P150 | 工具授权 | 写盘超出 `write_scope` 必须拒绝并上报 `tool_denied` |
| P151 | 工具授权 | 模型凭据不得进入工具子进程 env、argv、日志与事件 |
| P152 | 工具授权 | 只允许 argv 数组，禁止 `sh -c` 拼接 |
| P153 | 工具授权 | 一次 Run 内授权固定，不热改 |
| P160 | 终态 | 退出码 0 不得单独产生 `completed`；必须有 `result` |
| P161 | 终态 | 崩溃不得被 `completed` 覆盖 |
| P162 | 终态 | 未确认停止不得落 `cancelled`／`timed_out`；查不清一律 `unknown` 并保留占用 |
| P163 | 终态 | 错误类别取 §7.2 封闭集合；`env` 与业务失败分开 |
| P170 | 计量 | `cache_*` 独立字段，不得并入 `input`，不得参与 `total` |
| P171 | 计量 | `total_tokens` 出现时必须等于 `input + output` |
| P172 | 计量 | 未知用量为 `null`，不得填 0；`unknown` 仍计入 `turn_count` |
| P173 | 计量 | `estimated` 必须带 `basis`；不得伪装 `observed` |
| P174 | 计量 | 累计值下降 → `epoch+1` + `reset_observed=true`，增量取 `max(0, …)` |
| P175 | 计量 | 共享轮次只记一次；分摊之和不得放大 |
| P176 | 计量 | call 级与 run 级不得相加；有 call 级时 run 级不参与汇总 |
| P180 | 重放 | 去重身份 `(source_ns, turn_id, epoch, scope)` 唯一；重放不产生第二条 |
| P181 | 重放 | 写库失败保留 journal 并可幂等补录；补录不得改 `epoch`／`scope` |
| P182 | 重放 | `turn_id` 由来源身份组合，不得由模型或时间戳临时生成 |

与上游冲突时以上游为准：Run 阶段与占用（E2xx）、交付与验收（E3xx）、任务身份（E1xx）、时间轴与计量（T1xx／`maw.observability.timeline/v1`）。

## 14. 字段对照（契约名 ↔ 代码符号）

| 契约名 | 代码符号 / 位置 | 备注 |
| --- | --- | --- |
| 协议标识 | `model.Protocol{Format,Version}`（`internal/engine/model/spec.go:137`） | Pi 取 `pi-stream-json`／`1` |
| 启动规格 | `model.LaunchSpec`（`internal/engine/model/spec.go:143`） | argv 数组、`EnvAllowlist`、`ExpectedProtocol` |
| 来源选择 | `SourceFor`（`internal/runner/session/source.go:20`） | PI-102 在此分支接入 Pi |
| 协议判定 | `IsCodeBuddyProtocol`（`internal/runner/session/source.go:43`） | 新 Pi 判定必须同名同形：严格相等 |
| 端口 | `session.Source`（`internal/runner/session/runner.go:43`）：`Decode`／`Interpret` | Pi 适配器实现这两个方法 |
| 解释结果 | `model.InterpretResult`（`internal/engine/model/spec.go:355`） | `Outcome` / `Quality` / 产物候选 |
| 观测事实 | `observe.Facts`（`internal/runner/observe/observe.go`） | token 全为 `*int64`，nil=未知 |
| 观测端口 | `observe.Writer`（`internal/runner/observe/observe.go:65`） | 没有实现就不落观测，不编数 |
| 用量表 | `usage_records`（`internal/db/migrations.go`） | 唯一键 `(source_ns, turn_id, epoch, scope)` |
| 会话／轮次 | `source_sessions` / `source_turns` | `turn_id` 与 `shared_with_json` |
| 用量口径 | `UsageSemantic` / `UsageObservation`（`internal/adapters/codebuddy/usage.go`） | Pi 适配器按同一形状产出 |
| 阶段与终态 | `maw.runtime.state/v1` §2 | Pi 只上报事实，不改阶段 |
| 轮次计量 | `maw.observability.timeline/v1` §4 | §8–§11 是它在 Pi 上的落地 |

## 15. 与其它契约的关系

| 契约 | 谁说了算 |
| --- | --- |
| `maw.runtime.state/v1` | Run 阶段、核实状态、占用与释放、事件入库顺序 |
| `maw.observability.timeline/v1` | 轮次去重、epoch、共享、unknown、报表完整性 |
| `maw.delivery.manifest/v1` | `completed` 之后是否算交付完成（本契约不判 done） |
| `maw.lead.goal/v1` | Lead／诊断的用量归属键与预算口径 |
| 本契约 | Pi 的协议标识、三模式授权、事件边界、终态映射、Pi 特有字段映射 |

## 16. 验收对照（本卡）

| 验收 | 落点 |
| --- | --- |
| 明确版本 | §2 三项全等（契约版本 `maw.pi.execution/v1`、协议 `pi-stream-json/1`、SDK 精确锁 `X.Y.Z`） |
| worker／lead／diagnose 模式 | §3 权限表 + 模式回显 + 违规处置 |
| 身份 | §4 七个绑定字段 + `call_id` 唯一性 + Lead／诊断独立归属 |
| 事件边界 | §5 三类流三个去处 + 八条边界规则 + 消息信封 |
| 工具授权 | §6 授权矩阵 + 凭据禁令 + 禁止拼 shell + 不热改 |
| 终态 | §7 七种现场映射 + 错误类别封闭集合 + 停止流程 |
| 逐次调用与运行级用量 | §8 call 级字段 + §9 run 级兜底与不相加 |
| 缓存归一 | §10.1 `total = input + output`，缓存独立 |
| 缺失值／估算 | §10.2 五类情况 + `null ≠ 0` + `basis` 必填 |
| 重放键 | §11 去重身份 + 幂等补录 + `turn_id` 组合规则 |
| 样例 | §12 五个现场（成功／失败／取消／缓存未知／运行级兜底） |

人工复核方式（本卡 Verify）：按上表逐行对照 §12 样例与 `internal/runner/observe`、`usage_records` 字段，确认每个字段都有唯一去处、每条「不得」都有对应反例描述。

## 17. 本契约不做的事

- 不实现 Pi 运行时、适配器、节点注册、打包（PI-101／PI-102／PI-103）。
- 不定义观测落库与报表的具体 SQL（PI-201／PI-202）。
- 不定义 Lead 目标流程与入队（`maw.lead.goal/v1`）。
- 不定义交付验收与合入（`maw.delivery.manifest/v1`）。
- 不规定 Pi SDK 内部的 API 形状与函数名；只规定 maw 侧接收什么、拒绝什么。
- 不把任何开关默认打开；网络工具、自动派发仍按既有默认关闭策略。
