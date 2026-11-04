# maw 协作原语补齐（可执行任务清单）

**主分支：`main`**  
**迭代：`IT-COL-001`（协作原语补齐）**

版本：拆包稿 v1.0，2026-11-04。依据 [maw_from_deepseek_agent_teams.zh-CN.md](maw_from_deepseek_agent_teams.zh-CN.md)。  
本清单**已** `plan apply`（蓝图 v16 追加 `IT-COL-001`），十题全部合入 `main`——见下方状态表；压入前置条件与写盘图保留作历史依据。

> **状态（2026-11-04 收尾）：`IT-COL-001` 十题全 `done`，本文件转为设计归档。**
>
> | 题 | 合入提交 | 落地要点 |
> | --- | --- | --- |
> | `COL-CTR-001` 协作原语契约 | `d23e87f` | 冻结 revision / kind / result / activity 与权限语义 |
> | `COL-CAS-001` 变更统一 CAS | `cae2daf` | 元数据/归属变更校验 expected_revision |
> | `COL-MSG-001` status 分型 kind | `5ddb87b` | progress / question / blocker / final，缺省兼容旧行为 |
> | `COL-RES-001` 结构化 result | `8264b2e` | submit 可选 summary / artifacts / acceptance_checks |
> | `COL-STL-001` activity 三态 | `4303ad6` | working / stalled / idle 纯展示推导，CQ-003 释放语义不动 |
> | `COL-WAIT-001` `maw wait` | `9e84e33` | 等 deps / 事件 / 超时，退出码区分 |
> | `COL-ROLE-001` 标签与 reassign | `0e5f1fa` | 走 CAS，mismatch 不改 owner |
> | `COL-CTX-001` fresh / fork | `a4d14f2` | context 写进 journal 冻结规格，非法值拒绝 |
> | `COL-ACL-001` 权限分层 | `41f061b` | 仅中枢/Lead 可 reassign/interrupt |
> | `COL-ARCH-001` 活动视图 + archive | `eceb7a2` | 归档写 `tasks.extra_json` + 审计事件，不新增 TaskStatus 值 |
>
> 收尾发现并已修复：`maw iteration` 原先只有 create/list/show，`iterations.status`
> 全仓没有写入路径，`IT-COL-001` 会一直显示 `running`（`IT-BP-001` 当时由另一个
> Agent 用 `direct_db_close` 直改库关掉，正是这个缺口的证据）。已由 `ITR-001`
> （提交 `1c5a5aa`）补上 `maw iteration close`：状态机单向推进、终态不可回退、
> 迭代内仍有未完成任务时默认拒绝（`--force` 放行并回报被跳过的任务）、
> 同状态幂等且写 `iteration_status_changed` 审计事件。
>
> 本迭代已用该命令收口：`IT-COL-001` → `merged`（`41f061b`）；同批把 `IT-BUX-001`
> 与 `IT-BP-001` 一并对齐到 `merged`。


硬规则：一次一题、不越 Surface、Verify 先于完成；**不**对接 DeepSeek Harness 任务板；**不**弱化 worktree / 强制 `write_scope`。

与既有清单：

- `IT-BP-001`（看板补齐）不阻塞本波；`COL-ARCH-001` 勿与 `BP-ITF-001` / `BP-STA-001` 抢同一默认筛选语义，归档与 iteration 筛选可并存。
- `CQ-003`（人工无进程占用过期释放）**已在代码落地**（`heartbeat.go` + expire 测）。`COL-STL-001` **不重做**释放逻辑，只补 working/stalled/idle 可见性，并在验收中引用 CQ-003 行为不变。

## 写盘图（可并行 / 须串行）

```text
docs/contracts/vnext/collaboration/**     CTR（闸门）
internal/db/task_cas* + helpers 变更路径   CAS  ‖  status 分型 MSG  ‖  activity STL  ‖  launch CTX  ‖  看板归档 ARCH
cmd/maw/main.go（新子命令/flag）          串行：MSG → WAIT → ROLE（均碰 main 时）
ROLE reassign ──► ACL 强制
MSG ──► RES（submit result）
MSG ──► WAIT（事件等待）
```

## Lane 与题 ID

| Lane | 题 ID 序列 | Surface 根 | 说明 |
| --- | --- | --- | --- |
| CTR | `COL-CTR-001` | `docs/contracts/vnext/collaboration/**` | P0 协作契约闸门 |
| CAS | `COL-CAS-001` | `internal/db/**`（CAS）、`internal/app/helpers.go` 等 | 元数据/归属变更乐观锁 |
| MSG | `COL-MSG-001` | status / board StatusReport / i18n / main flag | 分型消息 |
| RES | `COL-RES-001` | submit / delivery 事件 | 结构化完成物 |
| STL | `COL-STL-001` | board 快照 / activity 推导 | 三态；不改 expire 语义 |
| WAIT | `COL-WAIT-001` | wait CLI + main 子命令 | 事件等待 |
| ROLE | `COL-ROLE-001` | agents 标签 + reassign | 具名角色与改派 |
| CTX | `COL-CTX-001` | runner journal / managed launch | fresh/fork |
| ACL | `COL-ACL-001` | 契约补充 + reassign/interrupt 闸 | 权限分层 |
| ARCH | `COL-ARCH-001` | board 任务默认过滤 + archive | 活动视图 |

并行入口（契约合入后）：`COL-CAS-001` ‖ `COL-MSG-001` ‖ `COL-STL-001` ‖ `COL-CTX-001` ‖ `COL-ARCH-001`。  
首题闸门：`COL-CTR-001`（无 deps）。  
`cmd/maw/main.go` 链：`COL-MSG-001` → `COL-WAIT-001` → `COL-ROLE-001`（后两者 deps 保证不并行改 main）。

筛看板：`?iteration=IT-COL-001`。

---

### COL-CTR-001 冻结协作原语契约（revision / kind / result / activity）

- **Goal**：现状：借鉴决议只在设计 md，API/事件字段未冻结。目标：在 `docs/contracts/vnext/collaboration/` 冻结 v1：任务变更 CAS、`kind` 枚举、`result` 形状、`activity` 三态与权限角色语义，供实现题只读引用。
- **Non-goals**：不写业务代码；不改既有 `task-instance-contract-v1` 正文含义；不规定 Harness 桥接。
- **Depends**：无
- **Surface**：`docs/contracts/vnext/collaboration/**`；禁止改 `internal/**`、看板前端、蓝图。
- **Contract**：本卡即契约正文；文件建议 `collaboration-primitives-contract-v1.md`（可附 samples）。
- **Read-scope**：`docs/maw_from_deepseek_agent_teams.zh-CN.md`、`docs/contracts/vnext/task/task-instance-contract-v1.md`、`docs/contracts/vnext/http/http-api-contract-v1.md`、`docs/contracts/vnext/execution/runtime-state-contract-v1.md`
- **Steps**：
  1. 写明 `expected_revision` / `revision_mismatch` 适用于哪些变更（owner 改派、放回、描述/scope 更新等；claim 事务可引用既有引擎）。
  2. 冻结 `kind`：`progress` | `question` | `blocker` | `final` 与旧 `phase`/`status` 兼容映射。
  3. 冻结可选 `result`：`summary`、`artifacts[]`、`acceptance_checks[]`。
  4. 冻结 `activity`：`working` | `stalled` | `idle`（及与 `offline` 关系）；声明占用过期释放以 CQ-003 为准。
  5. 冻结 Lead/中枢 vs 成员：谁可 reassign / interrupt。
- **Acceptance**：
  - 契约文件存在且含上述四块可测试字段表；标明契约 id（如 `maw.collaboration.primitives/v1`）。
  - 明确「不采用」列表与设计 md 一致（无 Harness 桥、不弱化 write_scope）。
- **Verify**：`manual: 打开契约 md，核对四块字段表与设计 md P0 对齐`
- **Lane**：CTR
- **Iteration**：`IT-COL-001`
- **maw**：priority=5 · merge_order=10

---

### COL-CAS-001 任务元数据与归属变更统一 CAS

- **Goal**：现状：v1 PATCH/claim 已有 revision，但部分 CLI/helper 路径直接 `Revision++` 写入，并发改派/放回仍可能静默覆盖。目标：凡改 owner/描述/放回/元数据的共享路径均校验 `expected_revision`，冲突返回明确 `revision_mismatch`（或 CLI 等价错误）。
- **Non-goals**：不做跨机器分布式锁；不改 claim 领取主路径语义；不实现 reassign UX（归 ROLE）。
- **Depends**：`COL-CTR-001`
- **Surface**：`internal/db/task_cas.go`（可新建）、`internal/db/task_cas_test.go`、`internal/app/helpers.go`（仅任务变更相关）、以及本卡为收口而必须改的 `internal/app/*_test.go`；禁止改 board 前端、status 分型、heartbeat 过期策略。
- **Contract**：以 `COL-CTR-001` 为准
- **Read-scope**：`docs/contracts/vnext/collaboration/`（CTR 落地后）、`docs/contracts/vnext/http/http-api-contract-v1.md`、`internal/engine/claim/claim.go`、`internal/httpapi/resources/handler.go`
- **Steps**：
  1. 盘点 `UpsertTask` 前 `Revision++` 且无期望版本的元数据/归属路径。
  2. 抽出统一 CAS 写入（期望版本不匹配则失败）；caller 传入 expected。
  3. 单测：并发二次更新，第二次得 mismatch；成功路径 revision 单调 +1。
- **Acceptance**：
  - 至少一条单测覆盖 mismatch；至少一条覆盖成功递增。
  - 文档化：哪些入口仍由 claim 事务内 revision 守卫（避免重复实现）。
- **Verify**：`CGO_ENABLED=0 go test ./internal/db ./internal/app -count=1 -run 'CAS|Revision|Mismatch|Helpers'`
- **Lane**：CAS
- **Iteration**：`IT-COL-001`
- **maw**：priority=10 · merge_order=20

---

### COL-MSG-001 status / feed 支持分型 kind

- **Goal**：现状：`status report` / `POST /api/status` 以自由 `message` + `phase` 为主，编排难区分提问与阻塞。目标：支持 `kind=progress|question|blocker|final`（缺省兼容旧行为），事件与 feed 可辨，CLI/i18n 可传该字段。
- **Non-goals**：不做 Agent 间邮箱 ack/重试/TTL；不改 submit 的 result 载荷（归 RES）；不实现 `maw wait`。
- **Depends**：`COL-CTR-001`
- **Surface**：`internal/app/status_cmd.go`、`internal/app/status_test.go`、`internal/board/serve.go`（`StatusReport` 及相关 handler）、`internal/i18n/catalog_zh.go`、`internal/i18n/catalog_en.go`、`cmd/maw/main.go`（**仅** status report 相关 flag/帮助）；禁止改 submit、heartbeat expire、看板 Vue 大改。
- **Contract**：以 `COL-CTR-001` 为准
- **Read-scope**：`docs/contracts/vnext/collaboration/`、`docs/maw_from_deepseek_agent_teams.zh-CN.md`
- **Steps**：
  1. 扩展 StatusReport / StatusReportInput 增加 `kind`；非法值拒绝。
  2. 写入事件 data；旧客户端不传 kind 时映射为 `progress`（或契约规定的默认）。
  3. CLI flag + 中英帮助；单测注入与兼容。
- **Acceptance**：
  - 单测：四种 kind 各至少一次写入事件可被读回。
  - 单测：省略 kind 行为与契约默认一致，不破坏旧调用。
- **Verify**：`CGO_ENABLED=0 go test ./internal/app ./internal/board ./internal/i18n -count=1 -run 'Status|Kind|Report'`
- **Lane**：MSG
- **Iteration**：`IT-COL-001`
- **maw**：priority=10 · merge_order=30

---

### COL-RES-001 submit / 收工可选结构化 result

- **Goal**：现状：完成几乎只体现在 git/submit 状态翻转。目标：`submit`（及 kind=final 的收工 report）可附可选 `result`（summary / artifacts / acceptance_checks），落入事件或交付可读字段，供看板与 system agent 消费。
- **Non-goals**：不替代 commit/merge；不强制所有题必须带 result；不做预算/token。
- **Depends**：`COL-MSG-001`
- **Surface**：`internal/app/worker_cmd.go`（submit/delivery 相关）、对应 `*_test.go`、必要时 `internal/db/delivery.go` 只读扩展字段写入事件；禁止改 CAS 核心、board 路由壳。
- **Contract**：以 `COL-CTR-001` 的 `result` 节为准
- **Read-scope**：`docs/contracts/vnext/collaboration/`、`docs/contracts/vnext/delivery/`
- **Steps**：
  1. CLI/API 可选传入 result JSON；校验形状。
  2. submit 成功事件携带 result；无 result 时行为与旧版一致。
  3. 单测：有/无 result 两条路径。
- **Acceptance**：
  - 带 result 的 submit 后事件或查询可见 summary 与 artifacts。
  - 不带 result 的 submit 仍可通过既有单测路径。
- **Verify**：`CGO_ENABLED=0 go test ./internal/app -count=1 -run 'Submit|Delivery|Result'`
- **Lane**：RES
- **Iteration**：`IT-COL-001`
- **maw**：priority=20 · merge_order=40

---

### COL-STL-001 Agent activity 三态（working / stalled / idle）

- **Goal**：现状：展示偏 idle/working/offline，难以区分「停滞」与「空闲待命」；占用过期释放已由 CQ-003 处理。目标：在快照/API 推导并暴露 `activity`（working|stalled|idle），看板或 JSON 可读；**保持** CQ-003 释放语义不变。
- **Non-goals**：不重写 `expireStaleAgents` 主逻辑；不改受管 occupancy_kept 硬边界；不做 token 预算。
- **Depends**：`COL-CTR-001`
- **Surface**：`internal/board/`（快照/API 组装 activity 的最小集合）、必要时新建 `internal/app/agent_activity.go` + 测；禁止改 `internal/app/heartbeat.go` 释放策略（只允许只读引用）。
- **Contract**：以 `COL-CTR-001` activity 节为准
- **Read-scope**：`docs/contracts/vnext/collaboration/`、`internal/app/heartbeat.go`、`internal/app/heartbeat_expire_test.go`、`docs/maw_gap_可执行任务清单.md`（CQ-003）
- **Steps**：
  1. 按 last_seen / 最近 progress 事件 / 阈值阈值定义 stalled。
  2. 快照或 `/api/board`（及等价）带上 activity 字段。
  3. 单测：working vs stalled vs idle 各一例；注明 expire 测仍绿。
- **Acceptance**：
  - 三种 activity 至少各有一条单测或可脚本观察点。
  - `go test` 现有 `Expire|Occupancy|Heartbeat` 相关测仍通过（CQ-003 不回退）。
- **Verify**：`CGO_ENABLED=0 go test ./internal/board ./internal/app -count=1 -run 'Activity|Stalled|Expire|Occupancy|Heartbeat|Status'`
- **Lane**：STL
- **Iteration**：`IT-COL-001`
- **maw**：priority=10 · merge_order=50

---

### COL-WAIT-001 `maw wait`：等 deps / 事件或超时

- **Goal**：现状：下游只能轮询 `next`。目标：提供 `maw wait`（或等价），可等待指定任务 deps 满足、feed 事件或超时，退出码区分完成/超时。
- **Non-goals**：不做跨机器唤醒；不替代 SSE 看板；不实现 Team 邮箱。
- **Depends**：`COL-MSG-001`
- **Surface**：`internal/app/wait_cmd.go`（新建）、`internal/app/wait_cmd_test.go`、`cmd/maw/main.go`（**仅**注册 wait 子命令与 flag）、必要 i18n 键；禁止改 status kind 语义、submit。
- **Contract**：以 `COL-CTR-001`；等待条件与退出码写在本卡 Acceptance，并在 ops 一句说明可放本卡 Steps。
- **Read-scope**：`docs/contracts/vnext/collaboration/`、`internal/app/status_cmd.go`、`internal/board/serve.go`（feed）
- **Steps**：
  1. CLI：`--task` / `--deps-of` / `--timeout` 等最小集。
  2. 轮询或订阅读本地 DB/feed；超时非零退出。
  3. 单测：用短超时与夹具事件覆盖成功与超时。
- **Acceptance**：
  - 超时路径可测且退出语义稳定。
  - deps 已满足时立即成功（或契约规定的短路径）。
- **Verify**：`CGO_ENABLED=0 go test ./internal/app -count=1 -run 'Wait'`
- **Lane**：WAIT
- **Iteration**：`IT-COL-001`
- **maw**：priority=20 · merge_order=60

---

### COL-ROLE-001 Agent 标签与安全 reassign

- **Goal**：现状：Agent 名偏进程身份（`cursor-…-1`），改派缺少一等 CLI/API。目标：支持可选角色/能力标签；中枢可将任务 reassign 给目标 Agent（走 CAS），并写事件。
- **Non-goals**：不实现会话内 `spawn_teammate`；不共享单一 cwd；不做成员改名永久禁令那套 roster。
- **Depends**：`COL-CAS-001`、`COL-MSG-001`（main.go 串行：在 MSG/WAIT 之后；若 WAIT 未合入则仍 deps MSG，WAIT 合入后领取前先 merge 主干）
- **Surface**：`internal/db/`（agents 元数据标签最小改动）、`internal/app/`（reassign 命令实现与测）、`cmd/maw/main.go`（**仅** reassign 子命令）、i18n；禁止改 board 导航壳、archive。
- **Contract**：以 `COL-CTR-001` 权限与 CAS 节为准
- **Read-scope**：`docs/contracts/vnext/collaboration/`、`COL-CAS-001` 落地代码
- **Steps**：
  1. Agent 可选 tags/roles 存储与展示（JSON meta 即可）。
  2. `maw … reassign`（名称以实现为准）要求 expected_revision，校验目标 Agent。
  3. 单测：成功改派；revision 冲突失败。
- **Acceptance**：
  - reassign 成功后 owner 与 revision 更新，事件可查。
  - mismatch 不改 owner。
- **Verify**：`CGO_ENABLED=0 go test ./internal/app ./internal/db -count=1 -run 'Reassign|Role|Tag|CAS|Revision'`
- **Lane**：ROLE
- **Iteration**：`IT-COL-001`
- **maw**：priority=25 · merge_order=70

> 注：与 `COL-WAIT-001` 同碰 `cmd/maw/main.go`。压入后若 WAIT 仍 ready，对 ROLE 增加硬 deps `COL-WAIT-001`，或等待 WAIT merge 后再领 ROLE。推荐蓝图：`deps = ["COL-CAS-001", "COL-MSG-001", "COL-WAIT-001"]`。

---

### COL-CTX-001 受管起跑上下文 fresh / fork

- **Goal**：现状：受管 launch 未显式选择「干净上下文」或「继承上题摘要」。目标：launch_spec / journal 支持 `context=fresh|fork`；fork 时写入可消费的摘要字段（来源任务或上一段 result）。
- **Non-goals**：不接模型 invoke；不实现 Harness fork session；不改人工 next 协议全文。
- **Depends**：`COL-CTR-001`
- **Surface**：`internal/runner/journal/**`、`internal/app/managed_cmd.go`（接线）、相关测 `internal/runner/**`、`internal/app/managed*_test.go`；禁止改 board UI、status kind。
- **Contract**：以 `COL-CTR-001`（若 CTR 含 context 节；否则本卡 Steps 补一小节并只读设计 md）
- **Read-scope**：`docs/maw_from_deepseek_agent_teams.zh-CN.md`、`internal/runner/journal/spec.go`、`docs/contracts/vnext/execution/`
- **Steps**：
  1. launch_spec 增加 context 字段与校验。
  2. fork 时写入 summary 来源；fresh 不带旧摘要。
  3. 单测两种模式。
- **Acceptance**：
  - fresh/fork 在 launch_spec 或 journal 中可区分。
  - 非法 context 值被拒绝。
- **Verify**：`CGO_ENABLED=0 go test ./internal/runner/... ./internal/app -count=1 -run 'Launch|Context|Fresh|Fork|Managed|Journal'`
- **Lane**：CTX
- **Iteration**：`IT-COL-001`
- **maw**：priority=10 · merge_order=80

---

### COL-ACL-001 改派 / 中断权限分层落地

- **Goal**：现状：改派/中断权限分散，成员旁路可能越权。目标：按契约强制——仅中枢/Lead 角色可 reassign/interrupt；普通成员限 claim/上报/完成；越权返回明确错误码。
- **Non-goals**：不做云账号体系；不引入 DeepSeek roster；不改 write_scope 强制检查。
- **Depends**：`COL-ROLE-001`、`COL-CTR-001`
- **Surface**：`docs/contracts/vnext/collaboration/`（仅权限节勘误/补例，若需）、`internal/app/`（reassign/interrupt 鉴权）、`internal/httpapi/`（若有对应命令的鉴权映射）、相关测；禁止改 FE-SHELL。
- **Contract**：以 `COL-CTR-001` 权限节为准
- **Read-scope**：`docs/contracts/vnext/collaboration/`、`docs/operations/vnext/operations-guide.zh-CN.md`
- **Steps**：
  1. 定义本机如何判定 Lead/中枢（配置角色或系统 actor）。
  2. reassign/interrupt 入口鉴权；单测越权失败。
  3. ops 指南补一句（若落入 Surface 外则只 read，把文案改动收口到本卡允许的 docs 路径：`docs/operations/vnext/operations-guide.zh-CN.md`）。
- **Acceptance**：
  - 非 Lead 调用 reassign/interrupt 失败且错误码稳定。
  - Lead/中枢路径成功（测或 manual）。
- **Verify**：`CGO_ENABLED=0 go test ./internal/app ./internal/httpapi/... -count=1 -run 'ACL|Reassign|Interrupt|Permission|Lead'`
- **Lane**：ACL
- **Iteration**：`IT-COL-001`
- **maw**：priority=30 · merge_order=90

---

### COL-ARCH-001 看板默认活动任务 + archive

- **Goal**：现状：完成题长期占默认任务视图。目标：默认只展示活动态（如 ready/claimed/in_progress/blocked）；提供显示已完成开关；支持 `archive`（或等价）使题退出日常视图但保留审计。
- **Non-goals**：不物理删除历史；不替换 iteration 筛选（与 `BP-ITF-001` 共存）；不做 Harness 任务板 UI。
- **Depends**：`COL-CTR-001`
- **Surface**：`internal/board/`（列表过滤与 archive 状态展示的最小集）、若需状态字段则 `internal/db/` 任务 archive 标记 + 迁移测；禁止改侧栏 NAV、统计组整页（归 BP）。
- **Contract**：以 `COL-CTR-001`（若含 archive；否则本卡与设计 md 对齐并在契约补一句——契约补丁只能改 collaboration 目录，经 CTR 已存在文件追加小节）
- **Read-scope**：`docs/maw_from_deepseek_agent_teams.zh-CN.md`、`docs/maw_board_product_gaps_可执行任务清单.md`
- **Steps**：
  1. 默认过滤活动态；UI 或 query 可 `include=done,archived`。
  2. archive 动作写状态/事件。
  3. 测：默认列表不含 archived/done；显式 include 可见。
- **Acceptance**：
  - 默认视图观察点：不含 archived。
  - archive 后审计事件或状态仍可查。
- **Verify**：`CGO_ENABLED=0 go test ./internal/board ./internal/db -count=1 -run 'Archive|Filter|Board|TaskList'`；若有前端测：`manual: 看板默认列表无已归档题`
- **Lane**：ARCH
- **Iteration**：`IT-COL-001`
- **maw**：priority=15 · merge_order=100

---

## 推荐首波领取

1. 先领并合入：`COL-CTR-001`
2. 再并行：`COL-CAS-001` ‖ `COL-MSG-001` ‖ `COL-STL-001` ‖ `COL-CTX-001` ‖ `COL-ARCH-001`
3. 然后：`COL-RES-001`、`COL-WAIT-001`（均依赖 MSG）
4. 再：`COL-ROLE-001`（CAS+MSG+WAIT）→ `COL-ACL-001`

## 压入前自检

- [x] 文首主分支 `main`；迭代 `IT-COL-001`
- [x] ≥1 无 deps 入口（`COL-CTR-001`）
- [x] 契约后可并行首题分属不同 Lane
- [x] `cmd/maw/main.go` 重叠已用 deps 串起 MSG→WAIT→ROLE
- [x] 每题 Acceptance / Verify 分离；实现题有 Read-scope
- [x] 字段名将用 `deps`（禁止 `depends`）
- [x] 压入后跑 `maw plan check`，核 deps_depth / 入口数（压入时已跑；收尾复核仅剩非阻断告警）
