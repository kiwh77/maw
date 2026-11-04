# 协作原语契约 v1（冻结）

契约标识：`maw.collaboration.primitives/v1`
状态：**冻结**。本文件是 `COL-CTR-001` 的交付物；`COL-CAS-001` / `COL-MSG-001` / `COL-RES-001` / `COL-STL-001` / `COL-WAIT-001` / `COL-ROLE-001` / `COL-ACL-001` / `COL-CTX-001` / `COL-ARCH-001` 只读引用本文件的字段表，**不得改写本文含义**；需要变更语义时新增 v2。
范围：任务变更乐观锁、协作消息 `kind`、结构化完成物 `result`、Agent `activity` 三态、改派/中断权限、归档视图的最小语义。**不**含任务身份与版本（见 `maw.task.instance/v1`）、**不**含 HTTP 传输细节（见 `http-api-contract-v1`）、**不**含 Run 占用与阶段（见 `runtime-state-contract-v1`）。

## 0. 为什么需要这份契约

现状（`internal/app/status_cmd.go`、`internal/db/db.go`、`internal/board/serve.go`、`internal/app/heartbeat.go`）：

1. 任务有 `revision` 列，但只有 claim/PATCH 一条路径真正校验期望版本；其余 CLI/helper 直接 `Revision++` 后 `UpsertTask`，并发改派、放回、改描述会**静默覆盖**对方决策。
2. 协作消息只有自由文本 `message` + `phase`，编排侧无法区分「提问」与「阻塞」，只能靠人读。
3. 完成态只体现在 `submit` 的状态翻转，交付摘要散落在对话里，看板与 system agent 无处读取。
4. Agent 展示只有 `idle|working|offline`，「进程还在但 40 分钟没动静」与「没事做待命」同形，无法区分停滞。
5. 改派/中断没有任何权限分层：任何本机 worker 都能替别人改归属。

本契约把这五件事各冻结成**一张可测试的字段表**，实现题按表落地，不再各自发明语义。

## 1. 任务变更 CAS（`expected_revision` / `revision_mismatch`）

### 1.1 规则

- 每个任务快照携带单调递增整数 `revision`（`tasks.revision`，已有列）。
- 修改**已存在任务**的元数据或归属时，调用方必须给出 `expected_revision`；服务端在**同一事务**内读取当前 `revision`：
  - 相等 → 写入并把 `revision` 加 1；
  - 不等 → 不落盘，返回 `revision_mismatch`，`details.actual_revision` 为当前值。
- 一次成功写入只前进 1；不得跳号，不得在未读到期望值时写。

### 1.2 适用面（必须带 `expected_revision`）

| 变更 | 入口 | 说明 |
| --- | --- | --- |
| 改 owner（改派 / 放回） | `maw … reassign`、`PATCH /api/v1/.../tasks/{id}` | 并发改派不得互相覆盖 |
| 改描述性定义（title / goal / write_scope / read_scope / acceptance / not_doing） | `plan apply` 对保留态任务的字段刷新 | 蓝图覆盖写必须带读到的版本 |
| 改优先级 / 排序（priority / merge_order） | 同上 | 与定义变更同批 |
| 改归档标记 | `archive` / `unarchive` 动作 | 见 §6 |
| 释放回板（人工放回 ready / blocked） | CLI 放回、system agent 自愈 | 与 expire 回收不同，见 1.3 |

### 1.3 不适用面（仍由各自事务守卫，禁止重复实现）

| 路径 | 守卫位置 | 原因 |
| --- | --- | --- |
| 领取（claim） | `internal/engine/claim` 事务内已有的版本与占用校验 | 领取是「建 Run + 占目录 + 改状态」的复合事务，不能拆成两次 CAS |
| 心跳过期回收（CQ-003） | `internal/app/heartbeat.go` 的 `expireStaleAgents` | 回收是系统被动动作，无外部期望版本可传；语义见 §4.4 |
| 受管 Run 阶段推进 | `runtime-state-contract-v1` 的 generation 校验 | 与任务 revision 是两套代际 |
| 事件追加 | 事件表自增 id | 事件不可变，无更新 |

### 1.4 错误形状

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `code` | string | 固定 `revision_mismatch` |
| HTTP 状态 | int | `409` |
| `details.actual_revision` | int | 当前真实 `revision`，调用方重新读取后重试 |
| CLI 等价 | 非零退出 + stderr 含 `revision_mismatch` | 与 HTTP 同码同名，便于脚本判断 |

## 2. 协作消息 `kind`

### 2.1 枚举

| `kind` | 语义 | 期望的后续动作 | 谁能发 |
| --- | --- | --- | --- |
| `progress` | 进行中，无需响应 | 更新看板进度 | 任务 owner（或旁路上报者，按 §5） |
| `question` | 需要答复才能继续 | 转人工/Lead 答复 | 任务 owner |
| `blocker` | 已阻塞，需要介入 | 入 system agent 自愈队列（`unblock_assist` 类） | 任务 owner |
| `final` | 本轮收工，可附 `result`（§3） | 触发交付读取 | 任务 owner |

### 2.2 与旧 `phase` / `status` 的兼容映射

| 旧输入 | 推导出的 `kind` | 说明 |
| --- | --- | --- |
| 未传 `kind`，但带 `message` 或 `task_id`（旧 `agent_status` / `external_progress`） | `progress` | 缺省值，旧客户端零改动 |
| 未传 `kind`、无 `message` 无 `task_id`（纯心跳） | 不产生协作消息 | 仍写 `agent_status` 事件，不发明 `kind` |
| 旧 `phase` 为任意值 | 原样保留在 `data.phase` | `phase` 是自由文本进度标签，**不参与** `kind` 判定 |
| 旧 `status=blocked` 的任务状态 | **不**自动转成 `blocker` | 任务状态与协作消息是两层；转换需显式发 `blocker` |

### 2.3 事件形状

| 位置 | 值 |
| --- | --- |
| `events.type` | 保持既有取值（有 message → `external_progress`，否则 `agent_status`），**不新增**事件类型 |
| `events.data.kind` | §2.1 之一；缺省 `progress` |
| `events.data.message` / `phase` / `pct` / `client` / `log_path` | 保持不变 |
| feed / SSE | 只读透出 `kind`，不改变分页与 burst 上限 |

### 2.4 校验

- 非法 `kind`（不在 §2.1 内）→ 拒绝：HTTP `400 invalid_kind`，CLI 非零退出。
- `kind=final` 允许不带 `result`（收工但不附结构化产物）。
- 上报不因 `kind` 改变任务归属：旁路上报仍不得夺取 owner（既有 `ownership=not_granted` 行为不变）。

## 3. 结构化完成物 `result`

`result` 是 `submit`（以及 `kind=final` 的收工 report）的**可选**附加载荷；不出现时行为与 v1 之前完全相同。

| 字段 | 类型 | 必填 | 约束 |
| --- | --- | --- | --- |
| `summary` | string | 出现 `result` 时必填 | 去空白后非空；建议 ≤ 2 KB |
| `artifacts[]` | array | 否 | 每项 `{ "path": string, "kind": string?, "note": string? }`；`path` 非空，仓库相对路径或绝对路径；总数 ≤ 64 |
| `acceptance_checks[]` | array | 否 | 每项 `{ "text": string, "ok": bool, "evidence": string? }`；`text` 非空；顺序即验收顺序 |

落点：

| 位置 | 内容 |
| --- | --- |
| submit 成功事件 `data.result` | 原样结构化存入（可被 feed 读回） |
| 交付记录（delivery） | 可读扩展字段，供看板与 system agent 消费；`result` 是交付元数据，**不**替代 commit / merge |

校验失败 → 拒绝整次 submit（不做部分写入）：HTTP `400 invalid_result`，CLI 非零退出。
`result` 不参与 `revision` 计算，也不改变任务状态机。

## 4. Agent `activity` 三态

### 4.1 判定表

`activity` 是**派生只读字段**，由 `agents.last_seen` 与最近一次协作消息推导，不新增可写状态列。

| `activity` | 判定（按顺序短路） | 含义 |
| --- | --- | --- |
| `offline` | `agents.status = "offline"`（心跳过期回收后写入） | 已判离线，见 §4.4 |
| `working` | 未过期，且 `last_seen` 距今 ≤ `stalled_after_sec` | 活着且在最近窗口内有动作 |
| `stalled` | 未过期，`last_seen` 早于 `stalled_after_sec` 但仍未超过心跳 TTL | 进程还在但长时间无进展 |
| `idle` | 未过期，无活跃任务（`agents.task_id` 为空）且最近一次协作消息不是 `progress`/`final` | 待命，没活可干 |

`stalled_after_sec` 默认取 `locks.heartbeat_ttl_sec / 3`（当前配置 900 → 300 秒）；可被配置覆盖，但必须 < 心跳 TTL，否则 `stalled` 不可观测。

### 4.2 输出位置

| 位置 | 字段 |
| --- | --- |
| `/api/board`（及等价快照）的 agents 项 | `activity`: `working` \| `stalled` \| `idle` \| `offline` |
| `maw agents --json` | 同上 |

既有 `status` 字段保持不变（`idle` / `working` / `offline`）；`activity` 是**新增并列字段**，不改旧字段取值，避免旧前端误读。

### 4.3 与任务状态的关系

- `activity` 不驱动任务状态机：`stalled` **不等于**任务 `blocked`，也不触发自动改状态。
- 想表达「任务级阻塞」用 `kind=blocker`（§2），不要靠 `activity`。

### 4.4 占用过期释放以 CQ-003 为准（不重做）

- 心跳过期（`last_seen` 超过 `locks.heartbeat_ttl_sec`）后的**回收行为**由既有 `expireStaleAgents` 决定：无进程证据的人工占用释放回 `ready`/合法 `blocked`；受管 Run 仍占用时保留占用并写 `agent_expired_occupancy_kept`。
- 本契约只**读**该结果（`offline` + 任务回落），**不**修改释放策略、不新增第二套心跳语义。`COL-STL-001` 的验收必须引用本条，并保证原有 expire/occupancy 测试仍绿。

## 5. 权限分层：Lead/中枢 vs 成员

### 5.1 动作 × 角色

| 动作 | 普通成员（worker） | Lead / 中枢 | 依据 |
| --- | --- | --- | --- |
| `next` / `next --claim` | ✅ | ✅ | 领取按 deps + 写盘冲突判定 |
| `status report`（任意 kind） | ✅ 仅限自己 | ✅ | 不得夺取他人 owner |
| `submit` / `block` / `progress` | ✅ 仅自己的任务 | ✅ | 任务归属校验 |
| `reassign`（改 owner） | ❌ | ✅ | 走 §1 CAS，带 `expected_revision` |
| `interrupt`（中止他人执行） | ❌ | ✅ | 与 reassign 同级 |
| `archive` / `unarchive` | ❌ | ✅ | 走 §1 CAS |
| `plan apply` | ❌ | ✅ | 既有 `approvals.plan` 之外再加一层角色 |

### 5.2 Lead/中枢判定

- 判定来源是本机配置里的显式名单（`[identity] leads` 或等价键）与系统 actor（`system`）；**不**由 `role=integrator/reviewer` 自动推导。
- 判定为否 → 拒绝：HTTP `403 not_lead`，CLI 非零退出 + stderr 含 `not_lead`。
- 本条只规定「谁可以」，不规定「怎么存名单」；名单键名由实现题落地并在运维指南记录。

### 5.3 与 write_scope 的关系

权限分层**不**替代 `write_scope` 强制检查：越权改归属与越界写文件是两件事，两者都要拦。任何实现都不得把 `write_scope` 降级为警告。

## 6. 归档视图（archive，最小语义）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `archived` | bool | 派生自归档标记；`true` 表示退出日常视图 |
| 默认列表 | — | 只含活动态（`ready` / `claimed` / `in_progress` / `blocked`） |
| 显式查询 | `include=done,archived` | 与 `?iteration=` 筛选**可并存**，互不覆盖 |
| 归档动作 | `archive` / `unarchive` | 走 §1 CAS（带 `expected_revision`）+ 追加事件；不物理删除历史 |

归档**不是**新任务状态：`tasks.status` 取值不变，审计事件与历史行永远可查。

## 7. 错误码汇总

| 码 | HTTP | 触发 |
| --- | --- | --- |
| `revision_mismatch` | 409 | 期望版本与当前不符（§1） |
| `invalid_kind` | 400 | `kind` 不在枚举内（§2.4） |
| `invalid_result` | 400 | `result` 形状不合法（§3） |
| `not_lead` | 403 | 非 Lead/中枢发起受控动作（§5.2） |

## 8. 不采用（与设计 md 一致）

1. **不**接收 DeepSeek Harness Team 任务板，不建跨进程桥（无稳定 API）。
2. **不**在 maw 内嵌 Harness 式 Lead/Teammate 会话运行时，不实现会话内 `spawn_teammate`。
3. **不**弱化 `write_scope` 为提示；越界提交继续拒绝。
4. **不**共享单一 checkout 并行写；并行必须走 worktree + scope。
5. **不**把 `stalled` 当作「不活动即释放 owner」的理由；释放仍以 `runtime-state-contract-v1` 与 CQ-003 为准。
6. **不**把任务真相绑到会话日志；权威仍在 `~/.maw/maw.db`。
7. **不**做 Agent 间邮箱 ack/重试/TTL 的第一版保证（先做到中枢可见、可订阅）。
8. **不**在本次引入 token/时长预算。

## 9. 与既有契约的关系

| 契约 | 关系 |
| --- | --- |
| `maw.task.instance/v1` | 本文不改其身份/版本身份语义；CAS 针对 `tasks.revision`（可变状态），版本针对定义快照（不可变） |
| `maw.http.api/v1` | 本文只冻结语义与错误码，传输细节（`If-Match: rev-<n>`、`409` 形状）沿用该契约 |
| `maw.runtime.state/v1` | 占用、阶段、generation 与回收边界以该契约为准；本文 §4.4 只引用不复述 |

## 修订

| 日期 | 说明 |
| --- | --- |
| 2026-11-04 | v1 冻结：四块字段表（CAS / kind / result / activity）+ 权限分层 + 归档最小语义 |
