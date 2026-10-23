# 应用端口与接入器契约 v1（冻结）

契约标识：`maw.app.ports/v1`
上游契约：`maw.task.instance/v1`（`../task/`）、`maw.runtime.state/v1`（`../execution/`）、`maw.delivery.manifest/v1`（`../delivery/`）——本文件只谈「谁通过哪个接口、在哪个事务边界里、能做什么」，三份上游契约的对象定义不在这里重复。
状态：**冻结**。VN-005 起以它为引用基线；改含义只能出 v2。

范围：`ClaimExecution`、`Store`、`Probe`／`Launch`／`Decode`／`Interpret`、`ProcessController` 的输入、结果、错误、事务边界与依赖方向。**不实现端口、不建包、不引微服务或外部消息队列。**

## 0. 为什么需要这份契约

现有实现把四件事压在同一条调用栈里，边界只能靠读代码猜：

1. **事务边界靠注释维持。** `internal/app/worker_cmd.go:31` 用短事务只做领取，注释写着「Git worktree + task card happen outside」，随后 `:121`–`:141` 在事务外建 worktree、写卡片，失败再走 `rollbackClaim`（`:154`）补偿。规则存在，但没有接口约束：任何新调用点都可以把 git 操作挪进事务，或把状态写挪到事务外，编译器不会拦。
2. **接入器没有身份边界。** 现在还没有 `internal/adapters/` 与 `internal/engine/ports`。一旦按现状扩展，最省事的写法就是适配器直接 `db.UpsertTask` 把任务标成 `in_progress`——业务状态被来源侧改写，VN-002 的代际与占用规则当场失效。
3. **错误只有一种形状。** `db.IsBusy`（`internal/db/db.go:208`）单独判 SQLITE_BUSY，其余失败一律向上抛。于是「没题可领」「资源冲突」「版本不匹配」「协议不认识」「平台不支持」在调用方眼里长得一样，只能靠字符串匹配，正是 G04／G07 想避免的。
4. **能力没有落点。** `Probe` 该输出什么、`Interpret` 凭什么判成功，目前散在文档里；VN-301 已经给出实测证据（退出码 0 可能是认证失败），但没有任何接口强制接入器消费它。

本契约把「用例、端口、实现、进程」四层切开，并给每一层划死可写的副作用。

## 1. 四层与依赖方向

| 层 | 职责 | 目录（本卡只登记，不创建） |
| --- | --- | --- |
| `app`（用例） | 编排端口、持有事务、决定业务状态 | `internal/engine/claim`、`internal/engine/dispatch`、`internal/engine/reconcile`、`internal/runner`、`internal/app` |
| `port`（接口） | 只有类型与接口声明，零依赖 | `internal/engine/ports` |
| `adapter`（来源适配） | 把厂商 CLI 翻译成端口语义 | `internal/adapters/codebuddy`、`internal/adapters/workbuddy`、`internal/adapters/trae`、`internal/adapters/zcode` |
| `infra`（基础设施） | 存储、git、进程、时钟 | `internal/db`、`internal/gitops`、`internal/runner/process` |

允许的依赖（只准向下，`→` 表示「可以 import」）：

```text
app     → port, infra
adapter → port
infra   → port（只为实现接口）
port    → （无）
```

**禁止的边（P120／P121）**：

- `adapter` 不得 import `app`、`infra`、`schedule`、`machine`——接入器不认识业务状态机，也不认识数据库。
- `port` 不得 import 任何实现包，包括 `internal/db`；端口包里出现 `*sql.Tx` 就是泄漏。
- `infra` 不得 import `app`：`internal/db` 不知道「领取」是什么意思，只提供事务与行读写。

这条边界直接对应验收第二条：**接入器不允许直接写业务任务状态**（P130）。接入器的产出只能是「端口结果」，落库由 `app` 层在事务里完成。

## 2. 端口清单

七个端口，名字与 `ports_contract.py` 的 `PORTS` 表一一对应（多一个、少一个测试都失败）。

### 2.1 ClaimExecution（用例，不是端口实现）

| 项 | 内容 |
| --- | --- |
| 输入 | `project_id`、`agent_id`、`requested_task`（可空＝自动选题）、`expected_revision`（可空）、`idempotency_key`、`capacity{node_slot_limit, managed_limit}` |
| 输出 | `task_instance`、`task_version`、`run_id`、`generation`、`leases{node_slot, workspace}`、`operation_id` |
| 错误 | `no_ready`、`conflict`（写盘／共享文件／占用）、`revision_mismatch`、`capacity_exhausted`、`idempotent_replay` |
| 事务 | **必须在短事务内**：验版本、占槽、预定目录、建 Run 与 Operation、追加事件，一次提交 |
| 禁止 | 事务内建 worktree、跑 git、起进程、调验证命令；指定任务时内部再选题 |

`no_ready` 与 `conflict` 必须分开：前者是「没有满足条件的题」，后者是「有题但被资源挡住」。混成一个错误，看板只能显示「没题」，用户看不到「被谁挡住」（VN-002 §4.1）。

### 2.2 Store（事务端口）

| 项 | 内容 |
| --- | --- |
| 输入 | `ops[]`：`task_status`、`run_phase`、`lease_acquire`、`lease_release`、`event_append`、`operation_update`；每个 op 带 `expected_revision` 或 `generation` |
| 输出 | `cursor`（事件入库序号）、`revision`、`applied[]` |
| 错误 | `busy`（可重试）、`conflict`（版本／代际不匹配，不可重试）、`invalid_transition`、`unknown_identity` |
| 事务 | 一次调用＝一个事务；调用方不持有 `*sql.Tx`，也不得跨调用拼事务 |
| 禁止 | 部分提交；把 `busy` 当成功；用「以最新为准」掩盖 `conflict` |

`busy` 是唯一可自动重试的错误，且重试次数与间隔由调用方决定，端口不内建重试循环——内建重试会让「事务到底提交了几次」不可观测。

### 2.3 Probe

| 项 | 内容 |
| --- | --- |
| 输入 | 节点配置（绝对入口、运行时、配置档引用、平台） |
| 输出 | `entry_state`（ready／missing／incompatible／unknown）、`version`、`readiness{entry, auth, connection, capacity}`、`capabilities[]`、`evidence_ref` |
| 错误 | `entry_missing`、`version_unparsed`、`probe_timeout`、`platform_unsupported` |
| 副作用 | **只读**：可执行 `--version`／`--help`，不登录、不发模型任务、不改配置 |
| 版本 | 输出必须带 `version`；能力必须绑定该版本，版本变化即失效（P140） |

### 2.4 Launch（BuildLaunchSpec）

| 项 | 内容 |
| --- | --- |
| 输入 | 冻结的 `RunSpec`（任务实例、版本、工作目录、输入、权限档、MCP／会话配置、超时） |
| 输出 | `executable`、`argv[]`（数组，非 shell 字符串）、`cwd`、`stdin_mode`、`env_allowlist[]`、`expected_protocol{format, version}` |
| 错误 | `invalid_spec`、`capability_unsupported`、`entry_unavailable` |
| 禁止 | 拼 shell；透传宿主私有环境；把 `expected_protocol` 写成「看情况」 |

`env_allowlist` 是白名单，不是黑名单：VN-301 实测过继承宿主私有环境会改变认证结果，白名单外的变量一律不传。

### 2.5 Decode

| 项 | 内容 |
| --- | --- |
| 输入 | 一条来源记录（带稳定日志位置）、`cursor`、`protocol{format, version}` |
| 输出 | `events[]`（零到多条标准事件）、`raw_ref`、`quality{observed/estimated/unknown}` |
| 错误 | `unparseable_record`、`unknown_record_type`、`version_unsupported` |
| 禁止 | 为凑事件而猜测字段；把未知记录映射成状态迁移；丢弃原始引用 |

未知记录只保留 `raw_ref` 与 `unknown_record_type`，**不产生事件**（VN-002 §7 的 E263 只允许引用已存在的身份）。

### 2.6 Interpret

| 项 | 内容 |
| --- | --- |
| 输入 | 结构化最终结果、应用错误、退出码、超时信息、产物声明 |
| 输出 | `outcome{completed/failed/cancelled/timed_out/unknown}`、`result_text`、`artifact_candidates[]`、`quality` |
| 错误 | `missing_result_record`、`auth_required`、`application_error`、`protocol_unknown` |
| 禁止 | 用退出码 0 判成功（P141）；未知协议结果标成功（P142）；把候选产物当已核实产物 |

VN-301 的实测证据就落在这里：同一版本下退出码 0 可能是认证失败，`result.subtype` 才是判据；`usage.input_tokens` 已含缓存分解，不能与 `cache_*` 相加。

### 2.7 ProcessController

| 项 | 内容 |
| --- | --- |
| 输入 | `LaunchSpec`（启动）或 `StopRequest{run_id, generation, mode}`（停止） |
| 输出 | `ProcessHandle{pid, pgid, start_token}`；`StopEvidence{stop_confirmed, method, evidence_ref}` |
| 错误 | `not_started`、`already_stopped`、`stop_unconfirmed`、`platform_unsupported` |
| 事务 | **禁止在事务内**（P132）；进程创建与停止都在事务外，结果回写走 `Store` |
| 禁止 | 用「进程已退出」代替「进程已停止确认」；`stop_unconfirmed` 时释放槽或目录 |

`stop_unconfirmed` 是合法结果而不是失败：它要求保持占用（VN-002 §4），调用方不能重试成 `stop_confirmed`。

## 3. 一次领取的边界

```text
[事务内] ClaimExecution
  验 expected_revision → 依赖与写盘冲突 → 占槽/预定目录 → 建 Run+Operation → 追加事件 → 提交
        │ 提交成功
        ▼
[事务外] gitops.EnsureAgentWorktree → 写任务卡 → ProcessController.Start → Decode/Interpret 循环
        │ 任一步失败
        ▼
[补偿事务] Store: task_status 回退 + lease_release + 事件（不是「撤销」，是新的持久事实）
```

三条硬规则：

- **事务内不碰外部世界**（P132）：git、进程、网络都在事务外；事务里只允许 `db_write` 与 `event_append`。
- **业务状态只在事务内写**（P133）：事务外的 `db_write` 一律非法，包括「顺手把状态改成 in_progress」。
- **补偿不是回滚**（对应 `rollbackClaim`）：失败后写的是新的状态与事件，历史保留；`rollbackClaim` 式的「改回 ready」必须留下事件，否则时间轴上看不出领取曾经发生过。

## 4. 错误模型

| 类别 | 例子 | 调用方行为 |
| --- | --- | --- |
| `retryable` | `busy`、`probe_timeout` | 可重试，但必须记录尝试次数 |
| `conflict` | `conflict`、`revision_mismatch`、`generation_mismatch` | 不重试，重新读取事实再决定 |
| `terminal` | `no_ready`、`invalid_spec`、`capability_unsupported`、`platform_unsupported` | 不重试，向用户说明原因 |
| `unknown` | `protocol_unknown`、`stop_unconfirmed`、`version_unsupported` | 保持占用与待核实，**不得降级为成功** |

`unknown` 是第四类，不是 `terminal` 的同义词：它表示证据不足，处理方式是保留现场并标注，而不是判失败或判完成。

## 5. 能力与版本

- 每个 `adapter` 绑定必须写 `capability_version`（语义化版本）与 `evidence`（证据目录引用），缺失即 P140。
- 一个端口可以由注册表分派给多个来源：此时绑定写成 `providers[]`，每个来源各自带版本与证据，能力不互相继承（正例 `05`）。
- 版本变化使该绑定的能力全部失效，必须重新探测；不允许「沿用上个版本的能力表」。
- 能力状态只允许 `observed`／`unobserved`／`unknown`／`unsupported`（与 `docs/evidence/codebuddy/capability-matrix.json` 同构）；`unobserved` 与 `unknown` 不得参与自动匹配。

## 6. 实现目录与所有者

| 端口 | 实现目录 | 调用方 | 不得出现在 |
| --- | --- | --- | --- |
| `ClaimExecution` | `internal/engine/claim` | `internal/app/worker_cmd.go`、HTTP 入口 | `internal/adapters/**` |
| `Store` | `internal/db` | `internal/engine/**`、`internal/runner` | `internal/adapters/**` |
| `Probe`／`Launch`／`Decode`／`Interpret` | `internal/adapters/{codebuddy,workbuddy,trae,zcode}` | `internal/runner` | `internal/db`、`internal/app` |
| `ProcessController` | `internal/runner/process` | `internal/runner` | `internal/adapters/**` |

`internal/schedule` 只做纯函数选题（`PickNextReadyForShared`），不持有事务、不写库；`internal/gitops` 只做 git 动作，不认识任务状态。

## 7. 不变量登记表

机检主体是 `ports_contract.py`；本表由 `test_ports_contract.py` 强制与代码对齐，且**每条都必须有反例触发**。

| 码 | 判定对象 | 不变量 |
| --- | --- | --- |
| P100 | 全文档 | 必填字段存在且类型正确；时间可解析为 ISO8601 |
| P101 | 根节点 | `contract` 必须是 `maw.app.ports/v1` |
| P110 | `modules[].layer` | 只能是 app／port／adapter／infra |
| P111 | `bindings[].port` | 必须是登记过的七个端口之一 |
| P112 | `bindings` | 同一端口不得重复绑定 |
| P113 | `bindings` | 七个端口必须全部有绑定，声明了不接线等于没有边界 |
| P120 | 模块依赖 | adapter 不得 import app／infra／schedule／machine |
| P121 | 模块依赖 | port 层不得 import 任何模块 |
| P122 | 模块依赖 | 被 import 的模块必须在 `modules` 中声明 |
| P123 | 模块依赖 | 依赖图不得成环 |
| P130 | 适配器副作用 | 接入器不得产生 `task_status_write` |
| P131 | `calls[].errors` | 每次调用必须声明可能错误，不得为空 |
| P132 | 事务边界 | `in_tx=true` 的调用不得含进程／git／网络副作用 |
| P133 | 事务边界 | `in_tx=false` 的调用不得写业务状态 |
| P134 | 领取调用 | `ClaimExecution` 调用必须带幂等键与冲突检查 |
| P140 | 适配器绑定 | 必须写 `capability_version`（语义化）与 `evidence` |
| P141 | 结果判定 | 不得声明「退出码 0 即成功」 |
| P142 | 结果判定 | 未知协议结果不得标为成功 |

## 8. 正反例

正例（`samples/valid/`）：

| 文件 | 覆盖现场 |
| --- | --- |
| `01-minimal-wiring.json` | 七个端口全绑定、四层依赖单向、无环的最小装配 |
| `02-claim-in-tx.json` | 领取在事务内只写库与事件；worktree 与卡片在事务外 |
| `03-adapter-isolated.json` | 接入器只依赖 port／model，四个协议端口全绑 2.173.0 并挂证据 |
| `04-process-outside-tx.json` | 启动与停止在事务外，`stop_unconfirmed` 保持占用 |
| `05-multi-adapter.json` | 两个接入器各自绑版本，能力不互相继承 |
| `06-unknown-retained.json` | 未知协议记录只留原始引用，结果标 unknown 且不产生 done |

反例（`samples/invalid/`，每个用 `expect_error` 声明应触发的码）：未知端口、重复绑定、缺绑定、适配器依赖 db、端口依赖实现、import 未声明模块、依赖成环、适配器写任务状态、调用无错误集、事务内跑 git、事务外写状态、领取缺幂等键、适配器无版本、退出码当成功、未知当成功、非法层。

验证命令（本卡 Verify）：

```sh
python3 -m unittest discover -s docs/contracts/vnext/ports -p 'test_*.py'
```

## 9. 本契约不做的事

- 不创建 `internal/engine/ports` 等包，不写任何 Go 实现，不改 `internal/db`、`internal/app`、`internal/schedule`、`internal/gitops`。
- 不定义 HTTP 入口与本机授权（VN-005）、节点登记（VN-302）、启动规格（VN-303）、事件解析（VN-304）。
- 不引入微服务、消息队列或跨进程 RPC：所有端口都是同进程内的 Go 接口。
- 不规定重试次数、超时数值与日志格式。
