# 目标级 Lead 与蓝图入队契约 v1（冻结）

契约标识：`maw.lead.goal/v1`
上游契约：`maw.task.instance/v1`（`../task/`，任务身份与定义版本）、`maw.runtime.state/v1`（`../execution/`，Run 阶段、核实状态与代际）、`maw.delivery.manifest/v1`（`../delivery/`，交付与验收结论）、`maw.iteration.blueprint/v1`（`../iteration/`，迭代字段与不变量）、`maw.observability.timeline/v1`（`../observability/`，用量计量与 unknown 口径）；兄弟契约 `maw.lead.selfheal/v1`（`./lead-selfheal-contract-v1.md`，系统层自愈）——**本文件只补「目标如何被规划、批准、入队、观察与收尾」，不重复定义上面任何一条**。
状态：**冻结**（本卡 PI-002 定稿）。PI-301…PI-304、PI-901 以它为引用基线；改含义只能出 v2。

范围：目标身份与目标版本、验收条件与证据、计划修订与审批、结构化计划卡字段与校验、任务映射、追加入队的幂等键与崩溃恢复、失效提案、预算、暂停与重启恢复、交付观察与有限返工、整体验收退出条件。**不写业务代码，不建表，不改 `internal/app`、`internal/db`、`internal/blueprint`。**

## 0. 为什么需要这份契约

现状的 Lead 只有「一次性拆卡」和「异常处置」两件事，中间那段没人负责：

1. **拆完即失联。** `lead split` 把需求切成 `[[task]]` 模板（`internal/app/lead_split.go:48`），输出是片段不是计划。谁批准、这是第几版、后面还差几张、失败要不要返工，全靠人记；目标本身没有身份，无法被查询、暂停或恢复。
2. **入队没有版本与批准。** 入队走 `AppendTasks`（`internal/app/blueprint_append.go:27`）+ `PlanApply`（`internal/app/plan_cmd.go:50`）。前者只防「截断／重号」两类事故，后者按蓝图增量合入；两者都不绑定「这份提案是谁、在哪一版、基于哪个蓝图基线批准的」。批准与写入之间没有可比对的摘要。
3. **重复入队无键。** Lead 重试、进程重启、两个 Lead 并行规划同一目标，都会重复追加同一张卡。`AppendTasks` 只能靠 id 重号事后拒绝，无法回答「这次追加等价于上次那次吗」。
4. **预算与退出条件没有口径。** 目标跑到什么时候算完成、超预算怎么办、验收证据 `unknown` 时能不能算通过，现状没有定义。按现有习惯补，必然出现「任务 100% done 就宣布目标完成」和「把 `unknown` 当 0」两类报表。

本契约把目标级 Lead 拆成五段，每段可单独验收：**目标（身份／版本／验收条件）→ 计划修订（结构／审批）→ 入队（幂等键／追加）→ 运行观察（映射／预算／暂停恢复）→ 收尾（返工／整体验收／退出码）**。

## 1. 边界、角色与三条硬规则

| 角色 | 负责 | 不负责 | 载体 |
| --- | --- | --- | --- |
| Lead（目标级，Pi `lead` 模式） | 接收目标、产出计划卡、观察交付证据、提失效／返工提案、按验收条件给结论 | 改任务状态、改蓝图、领题执行、合入、批准自己 | `internal/goal`、`internal/app/pi_lead.go`、`internal/app/lead_goal_*` |
| maw（校验与执行方） | 校验计划、记录审批、追加蓝图、增量 apply、派发、交付判定 | 代替人判断业务是否达标 | `internal/blueprint`、`internal/app/plan_*`、`internal/app/blueprint_append.go` |
| 人 | 批准计划、处置 `escalated`、给预算、暂停／放弃目标 | 绕过 `maw merge` 改主干 | 配置、CLI、看板 |
| Worker | 领取并执行一张卡 | 判断目标是否完成 | `internal/app/runtime.go`、`claim` |

三条硬规则（后文所有条目都由它们推出）：

- **R1 Lead 无写权。** Lead 只能产出结构化提案；写蓝图、建任务、改状态只能由 maw 在校验与批准之后执行。
- **R2 入队只经批准修订。** 没有 `approved` 的计划修订与匹配的 `plan_digest`，一条卡也不得写入蓝图（G202、G304）。
- **R3 完成只由证据推导。** `done` 必须每个目标验收条件 `pass` 且证据绑定当前修订、当前任务版本与合入提交；`unknown` 不支撑 `pass`，也不支撑 `done`（G600、G601）。

## 2. 目标身份与目标版本

目标是一个带身份的实体，不是一段提示词。

```jsonc
{
  "contract": "maw.lead.goal/v1",
  "project_id": "716c30878ba1b95f",
  "goal_id": "gl_pi001ax9",
  "iteration_id": "IT-PI-001",
  "active_version": 1,
  "status": "active",
  "versions": [
    {
      "goal_version": 1,
      "frozen_at": "2026-11-18T02:40:00Z",
      "title": "打通目标级 Lead 与入队闭环",
      "statement": "现状：Lead 只有一次性拆卡。目标：目标可被批准、追加、观察、验收与有限返工。",
      "acceptance": [
        { "id": "AC-1", "kind": "command", "check": "maw lead goal status --json", "statement": "目标状态与修订可查询", "evidence_required": true },
        { "id": "AC-2", "kind": "command", "check": "go test ./internal/goal -count=1", "statement": "计划与生命周期逻辑通过", "evidence_required": true }
      ],
      "budget": { "model_calls": 200, "tokens_in": 4000000, "tokens_out": 800000, "wall_clock_seconds": 28800, "max_plan_revisions": 5, "max_rework_rounds": 2 }
    }
  ],
  "created_at": "2026-11-18T02:39:00Z",
  "updated_at": "2026-11-18T02:41:00Z"
}
```

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `goal_id` | 是 | `gl_` + ≥8 位小写字母数字，工程内唯一、**永不复用**；不得占用显示编号形态（G110、G111） |
| `project_id` | 是 | 目标归属工程，跨工程不共享目标 |
| `iteration_id` | 否 | 目标主迭代；留空表示跨迭代目标。非空时必须指向蓝图已声明的迭代（I104） |
| `title` / `statement` | 是 | `statement` 用「现状→目标」句式，和蓝图 `[[iteration]].goal` 同风格 |
| `acceptance[]` | 是 | 目标级验收条件，字段与判定见 §4；不得为空（G121） |
| `budget` | 是 | 目标预算，字段见 §10；缺省值由 §16 补齐，但写入时必须显式落库 |
| `active_version` | 是 | 当前生效的目标定义版本，必须等于已冻结最大版本（G122） |
| `status` | 是 | 七值封闭集合，见 §3 |

**目标版本**（`goal_version`）与任务定义版本同构：从 1 连续递增、冻结后不可改、指针不得回退。改验收条件、改预算、改目标陈述一律产生新版本，而不是原地修改；已批准的旧修订继续指向它当时冻结的目标版本（G120、G201）。

归属约束（防重复规划）：

- 一个任务实例至多归属一个目标修订（G112）——否则同一张卡会被两个目标各算一次完成。
- 同一 `iteration_id` 同时至多一个非终态目标（G113）；要并行跑两个目标，必须显式给不同迭代或留空 `iteration_id`。

## 3. 目标状态机

```text
draft -> active
active -> verifying
active -> paused | escalated | abandoned
verifying -> active          （返工修订已入队）
verifying -> done | paused | escalated | abandoned
paused -> active | abandoned
escalated -> paused | abandoned
```

- `done` / `abandoned` 为终态，不再出发任何转移；重开目标一律新建 `goal_id`。
- `paused` 必须带 `reason ∈ {human_pause, permission_denied, budget_exhausted, invariant_violation}`；`escalated` 必须带 `reason` 与一条人可执行的处置建议（例如「补授权后 `maw lead goal resume`」）。
- 每次转移必须与 `goal_state` 事件同事务提交，事件至少含 `from`、`to`、`reason`、`evidence`（指向验收结论、预算快照或人工输入）。无事件的状态变化视为未发生（G123）。
- **状态只由持久化证据重算**：重启后不得沿用内存里的目标状态（G500、G502）。

## 4. 验收条件与证据

目标级验收条件是「可判定的断言」，不是愿望清单。

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `id` | 是 | 目标内唯一，`AC-<n>` 形态；返工提案按它定位失败项 |
| `kind` | 是 | `command` / `manual` / `evidence`；`manual` 只允许无代码产物的目标（沿用 E350 口径） |
| `check` | 是 | `command` 为可复跑命令；`manual` 为人工核对步骤；`evidence` 为交付 Manifest 的查询条件 |
| `statement` | 是 | 一句话判据，必须能被判成 pass／fail，不允许「已完善」「基本可用」 |
| `evidence_required` | 是 | `true` 时 `pass` 必须带结果类证据（非 `exit_code:` 前缀的记录，沿用 E332／E333） |

判定只有三值：

| 判定 | 判据 | 后果 |
| --- | --- | --- |
| `pass` | 校验通过且证据绑定当前修订、当前任务版本、当前合入提交（代码类含产物哈希） | 可支撑 `done` |
| `fail` | 校验明确失败并带输出 | 可触发返工（§12）；阻塞 `done` |
| `unknown` | 证据缺失、矛盾、采集缺口或运行核实为 `unknown`（E232） | **阻塞 `done`**，目标进 `escalated`，退出码 3；不得折算成 `pass` 或 `fail` |

证据绑定键（缺一即为不可判定）：`goal_id`、`plan_rev`、`task_instance`、`task_version`、`commit`（代码类）、`artifact_sha256`（文件类）、`delivery_id` + `verification_id`（来自 `maw.delivery.manifest/v1`）。旧提交、旧哈希上的证据不得给当前产物背书（E331、E334）。

## 5. 计划修订、摘要与审批

Lead 每次规划产出一个**不可变的计划修订**。

```jsonc
{
  "contract": "maw.lead.goal/v1",
  "goal_id": "gl_pi001ax9",
  "goal_version": 1,
  "plan_rev": 1,
  "kind": "initial",              // initial | rework
  "status": "proposed",           // proposed|approved|applied|rejected|invalidated|superseded
  "baseline": {
    "blueprint_sha256": "<64 hex>",   // 读取时的整份蓝图文件哈希
    "tasks_revision": 1841,           // 任务表快照 revision（并发判据）
    "main_branch_tip": "a0a0720"
  },
  "plan_digest": "<64 hex>",      // 规范化 JSON 的 sha256，见 §5.1
  "cards": [ /* §6 */ ],
  "approval": {
    "state": "pending",           // pending|approved|rejected|expired
    "approver": null,
    "approved_at": null,
    "policy_source": null         // manual | auto_enqueue
  },
  "created_at": "2026-11-18T02:42:00Z",
  "applied_at": null
}
```

| 规则 | 内容 |
| --- | --- |
| 编号 | `plan_rev` 从 1 连续递增、不跳号不复用；跳号视为历史被删过，拒绝（G200） |
| 不可变 | 一旦 `approved`，`cards`、`baseline`、`goal_version` 不得再改；要调整只能新修订并 `supersede` 旧修订（G201） |
| 至多一次 applied | 同一修订最多入队一次；重放返回原结果，不重复写入（G204、G301） |
| 批准粒度 | 批准作用于 `plan_digest`。摘要不匹配即视为未批准（G202） |
| 批准留痕 | 必须记录 `approver` 身份、`approved_at`、`policy_source`；`auto_enqueue` 打开时也要落一条策略记录，不能以「配置里开着」代替审计（G203） |
| 过期 | 批准后 `approval_ttl_hours`（默认 24）内未入队则审批置 `expired`，修订 `invalidated(approval_expired)`；过期修订不得入队 |
| 拒绝 | `rejected` 必须写 `reason`，否则返工方无据可依 |

### 5.1 `plan_digest` 规范化

摘要对象是「修订内容」，不含审批字段本身：`{goal_id, goal_version, plan_rev, kind, cards}`。

```text
canonical = JSON，对象键按 UTF-8 字节升序，无多余空白，
            数组保持声明顺序，数字不带前导零/指数，字符串原样（不做 Unicode 归一），
            可选字段按声明值原样包含，缺省不补 null（null 只表示显式声明为空）
plan_digest = lowercase(hex(sha256(canonical)))
```

理由：键序、空白、数字写法任一不稳定，都会让「同一份计划」两次摘要不同，审批就变成走过场。

## 6. 结构化计划卡：字段与校验

一张卡对应蓝图里的一个 `[[task]]` 块，字段名与蓝图一致，另加来源与幂等所需的三项。

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `card_id` | 是 | 修订内稳定标识（`c01`…），入队幂等键与映射都以它为准；与 `display_id` 分属两个命名空间 |
| `id`（显示编号） | 是 | 蓝图 `id`；不得与既有 id 重复，不得使用保留前缀 `ti_`／`gl_`／`run_`／`op_`／`dlv_`（G211） |
| `title` | 是 | 非空；不得含「优化／完善／支持／待定」等模糊词（沿用 `ValidateLint` 规则 1，作为告警而非拒绝） |
| `goal` | 是 | 「现状→目标」句式；这是 Lead 与评审之间的合同条款，不是备注 |
| `iteration` | 否 | 必须指向已声明迭代，留空合法（I104） |
| `priority` / `merge_order` | 是 | 整数；`merge_order` 决定合入序列，缺省按 `priority` 补齐 |
| `deps` | 是 | 显式列表（可为空）；解析规则见下 |
| `write_scope` | 是 | 至少 1 条，仓库相对路径或 glob；本卡独占写入范围 |
| `read_scope` | 是 | 只读参考范围，不得与 `write_scope` 混同 |
| `acceptance` | 是 | ≥1 条可判定断言；不得是占位文本（G214） |
| `verify` | 是 | ≥1 条可执行命令或 `manual:` 步骤 |
| `not_doing` | 是 | ≥1 条边界，防止后续卡重复施工 |
| `origin` | 是 | `{goal_id, plan_rev, card_id, source_refs[]}`；`source_refs` 指向需求文档章节、issue 或人工输入，供评审回溯 |
| `budget` | 否 | 单卡执行上限 `{max_attempts, max_wall_clock_seconds}`；只约束这张卡的执行，不参与 §10 的目标预算账 |

### 6.1 校验规则

校验对象是**合并后的完整蓝图**（既有全部 `[[task]]` + 本次片段），不是片段本身；同时叠加本契约的目标级检查。现状 `AppendTasks` 只做「截断／重号」两道防线，合并后的完整校验属于 PI-302 的实现要求（G303）。

| 码 | 判定对象 | 规则 | 违反结果 |
| --- | --- | --- | --- |
| G210 | 卡片 | 必填字段齐全、类型正确、时间可解析 | 拒绝，修订保持 `proposed` |
| G211 | `card_id` / `id` | `card_id` 修订内唯一；`id` 与「蓝图既有 ∪ 本修订」不重复；形态非空、无空白、不带保留前缀 | 拒绝 |
| G212 | `deps` | 只能引用本修订的卡或蓝图既有题；不得自依赖、不得成环；目标实例不得处于退役（E151）或 `cancelled`（不可满足） | 拒绝（`dependency_invalid`） |
| G213 | `write_scope` | 非空；相对仓库根；不含 `..`、绝对路径、`~`、盘符；不得单独写成 `**`／`*`；**同修订内两卡范围重叠时，必须存在其中一张依赖另一张（或同一依赖链）的排序** | 拒绝（`write_overlap_unordered`） |
| G214 | `acceptance` / `verify` | 不得含 `TODO`、`待补`、`占位`、`TBD` 等未填标记 | 拒绝（`placeholder_card`） |
| G215 | 修订 | 卡数 ≤ `max_cards_per_revision`（默认 30） | 拒绝（`plan_too_large`） |
| G216 | 修订 | `kind=rework` 的每张卡必须带 `origin.evidence[]`（交付／验证／失败断言引用） | 拒绝（`rework_without_evidence`） |

`write_scope` 的重叠规则需要解释：蓝图层面**允许**重叠（`internal/blueprint/blueprint.go:236` 明确按 `deps + priority→merge_order→id` 串行激活，便于一次压入同目录后续题）。本契约不推翻这条，但要求重叠**有序**：无依赖序的重叠卡会同时可领，落到运行期只能拿到 `write_conflict`（`internal/engine/matching/matching.go:30`），等于把计划缺陷推给调度。计划期就拒绝，比运行期回退便宜。

失败原因码是封闭集合，与 G 码一一对应：`field_invalid`（G210）、`duplicate_id`（G211）、`dependency_invalid`（G212）、`write_scope_invalid` / `write_overlap_unordered`（G213）、`placeholder_card`（G214）、`plan_too_large`（G215）、`rework_without_evidence`（G216）。新增原因码必须同时补进本表与 §9。

校验失败一律**无副作用**：不写蓝图、不建任务、不改修订状态（保持 `proposed`），只记一条带失败码的校验证据。规划或校验失败留下半张卡，是比失败本身更贵的故障。

## 7. 任务映射与进度口径

入队的产物不是「蓝图里多了几行」，而是一条不可变的映射：

| 字段 | 说明 |
| --- | --- |
| `goal_id` / `plan_rev` / `card_id` | 来源三元组，回到批准的那张卡 |
| `instance_id` | 任务实例身份（`ti_…`），来自 `PlanApply` 的身份规划 |
| `display_id` / `version_no` | 入队时的显示编号与定义版本 |
| `enqueue_key` | §8 的幂等键 |
| `applied_at` / `applied_by` | 入队时间与操作者 |
| `state` | `applied` / `drifted` |

- 映射不可变；一个实例至多一条映射（G112）。重建编号（旧实例退役、新实例占用同号）不改变旧映射指向的 `instance_id`（沿用 E111／E160 口径）。
- **漂移**：蓝图被人在修订之外改动，导致实例 `active_version` 与映射的 `version_no` 不一致，或卡字段与批准内容不一致 → 该映射标 `drifted`。`drifted` 卡不计入目标进度分子，必须由新修订重新对齐（或人显式批准纳入）。
- 进度口径固定三列，不得合并成一个百分比：`mapped_total`（已映射卡数）、`mapped_done`（交付 `done` 且合入的卡数）、`drifted`（失真卡数）。未映射的卡单列 `pending_enqueue`；**不得把 `pending_enqueue` 算成 0 完成，也不得算成失败**。

## 8. 入队：幂等键、追加顺序与崩溃恢复

### 8.1 幂等键

```text
enqueue_key = "gk_" + lowercase(hex(sha256(canonical_json({
  "project_id": ..., "goal_id": ..., "plan_rev": ..., "plan_digest": ..., "card_id": ...
}))))
```

- 键是**确定性**的：同一批准修订重放得到同一键，无需读库即可算出（G300）。
- 台账（`goal_enqueues`）每键一行，`state ∈ {appended, applied, failed}`，并保存映射与失败原因。
- 同键 + 同摘要 → 幂等成功，返回原映射并标 `replayed=true`（G301）。**不得新建任务、不得再追加一次蓝图**。
- 同键 + 异摘要 → 冲突（HTTP 语义 `409 idempotency_key_reuse`，CLI 非零退出），不得「以最新请求为准」（G301，对齐 H140／E252）。
- 键一旦 `applied` 永久保留；修订作废也不删键，避免同名重建时复用旧键。

### 8.2 入队顺序（五步，禁止重排）

```text
1. 校验批准：修订 approved、digest 匹配、未过期、目标未暂停、预算未耗尽
2. 读基线：蓝图文件 sha256 + 任务快照 revision + 主干 tip
3. 内存校验：构造「既有 + 片段」全文 → blueprint.Validate + ValidateLint + §6.1 全部规则
4. 原子追加：AppendTasks 只追加（保留原文件为前缀），台账写 appended
5. 增量 apply：PlanApply（非 reset）+ 写映射 + 台账 applied，同库事务
```

不变量：

- **只走追加入口**：禁止把整份计划文件交给 `plan apply`（`hasNonTaskTable` 会拒绝含 `[meta]`／`[[iteration]]` 的片段），禁止 `plan apply --reset`（G302）。历史事故：单题临时 toml 做增量 apply 把 30 道 idle 题标成 `removed_from_plan`，`blueprint_append.go:17-26` 就是为它立的防线，本契约不放宽。
- **先校验后写盘**：第 3 步必须在第 4 步之前完成，且校验对象是合并后的全文。这样「写进去了但内容不合法」这个状态不存在（G303）。
- **不改既有**：入队不得改写既有任务字段、既有实例的 `version_no`、活跃 Run 的绑定；定义变化只追加新版本（G305，沿用 E131／E132）。
- **单写者**：同一工程同一时刻只允许一个入队写者；并发第二个批准操作读到基线已变，必须重新校验（§8.3）。

### 8.3 崩溃与并发矩阵

| 崩溃／冲突点 | 现场 | 恢复动作 | 禁止 |
| --- | --- | --- | --- |
| 第 1–2 步内 | 无写入 | 重试整条流程 | 不得先建任务占位 |
| 第 3 步失败 | 无写入，修订 `proposed` | 修卡后出新修订；旧修订 `invalidated` | 不得「先入队再修」 |
| 第 4 步前崩 | 无写入 | 重试 | — |
| 第 4 步后、第 5 步前崩 | 蓝图有卡，任务未建，台账 `appended` | 校验卡 id 全在且属同一 `enqueue_key` → **只重跑第 5 步** | 不得重复追加；不得删除已追加行（可能与其他追加交错） |
| 第 5 步事务内崩 | 事务回滚，台账仍 `appended` | 重跑第 5 步 | 不得留半套映射 |
| 第 5 步后崩 | 台账 `applied` | 重放返回映射 | 不得再 `apply` 一次 |
| 两个批准操作并发 | 后到者基线已变 | 重新校验；仍合法则继续，否则 `invalidated(baseline_drift)` | 不得用旧基线的校验结论写入 |

## 9. 失效提案

「失效」只作用于**尚未 applied** 的提案；已入队任务与映射不回滚（回滚会动到可能已被领取的实例，代价远高于重新修订）。

| 原因码 | 判据 | 后果 | 恢复路径 |
| --- | --- | --- | --- |
| `plan_digest_mismatch` | 批准后修订内容被改，摘要对不上 | 修订 `invalidated`，拒绝入队 | 出新修订并重新批准 |
| `baseline_drift` | 蓝图 sha256 或任务快照 revision 与批准时不一致，且冲突涉及本修订的 id 或范围 | `invalidated` | 基于新基线重出修订 |
| `goal_changed` | 目标版本已前进，修订仍绑旧 `goal_version` | `invalidated` | 按新目标版本重规划 |
| `approval_expired` | 超过 `approval_ttl_hours` 未入队 | 审批 `expired`，修订 `invalidated` | 重新批准原修订（内容不变可复用摘要） |
| `dependency_invalid` | 依赖目标退役、取消或成环（G212） | `invalidated` | 改依赖后重出修订 |
| `budget_exhausted` | 目标预算已判定耗尽 | 全部未入队修订 `invalidated`；目标 `paused` | 人加预算后 `resume` |
| `superseded` | 新修订已 applied 且覆盖本修订未完成卡 | 旧修订 `superseded` | 无需动作；旧修订已完成卡保持映射 |

失效必须写事件（含原因码与证据），并出现在目标详情里；看板与验收追踪都以事件为准，不靠「修订不见了」推断。

## 10. 预算

预算回答两个问题：还能花多少、花超了怎么办。

| 维度 | 单位 | 来源 | 缺省 |
| --- | --- | --- | --- |
| `model_calls` | 次 | 调用级观测（`maw.observability.timeline/v1` §4） | 200 |
| `tokens_in` / `tokens_out` | token | 同上，按轮次去重 | 4,000,000 / 800,000 |
| `wall_clock_seconds` | 秒 | Run 区间并集 + Lead 调用区间；不含 `explicit_wait` | 28,800 |
| `max_plan_revisions` | 次 | 目标内修订计数 | 5 |
| `max_rework_rounds` | 轮 | 返工修订计数（`kind=rework`） | 2 |
| `max_cards_per_revision` | 张 | §6.1 G215 | 30 |

规则：

1. **Lead 的模型调用独立归属**：规划／诊断调用按 `usage_kind=lead|diagnose` 记账，不与 Worker 混算；否则「预算超了」永远定位不到是谁花的。
2. **未知不得当 0**：用量质量三档 `observed` / `inferred` / `unknown`。分母为 0、采集有缺口或本轮未采集，计数为 `null`，不是 `0`（沿用 L240／§4.3）。
3. **保守判定**：只有 `observed`（必要时 `inferred` 且已标明估算）才允许判「未超预算」。`unknown` 时不得判未超，目标是进 `escalated`（「无法自动判断」），除非已确认超限——那时按超限处理（G401）。
4. **超限只暂停，不杀在飞**：达到任一维度上限 → 停止派发新卡、停止入队未批准修订、目标 `paused(reason=budget_exhausted)`；**不取消**在飞 Run、不改任务状态（G402）。理由与 `maw.runtime.state/v1` §1 一致：预算不是停止证据。
5. **重置只能由人**：`resume --budget <json>` 显式给新预算并留痕；系统不得自动加预算或自动降级上限（G403）。

## 11. 暂停与重启恢复

| 暂停来源 | `status` / `reason` | 效果 |
| --- | --- | --- |
| 人执行 `maw lead goal pause` | `paused` / `human_pause` | 停止派发与入队；在飞 Run 继续；可随时 `resume` |
| 预算耗尽 | `paused` / `budget_exhausted` | 同上，且未入队修订全部 `invalidated` |
| 权限不足（`RequireLead` 被拒、批准人不在授权集） | `paused` / `permission_denied` | 只产出提案，不写库 |
| 不变量冲突（如检测到映射重复、台账与蓝图不一致） | `escalated` / `invariant_violation` | 停止自动动作，等人核对 |

恢复顺序固定，不得跳步（G502）：

```text
1. 读目标：goal 行、active_version、status、预算快照（只读）
2. 校验基线：蓝图 sha256 与任务快照 revision 是否仍与「已批准修订的 baseline」相容
3. 对账台账：逐 enqueue_key 比对 applied/appended；缺失映射补建，孤儿映射标 drifted
4. 核实 Run：按 maw.runtime.state/v1 核实每个在飞 Run；unknown 不落终态、不释放资源（E232）
5. 重算验收：按交付证据重算每个验收条件三值，再决定 status 与退出码
```

- `paused` 跨重启保持 `paused`，直到显式 `resume`（G501）。重启不得把 `paused` 当 `active` 重跑，否则预算和授权会被静默绕过。
- 恢复必须**幂等**：同一台账行恢复两次不得造出第二个任务或第二个修订（G503）；台账与蓝图不一致时以台账 + 蓝图 id 双向核对，缺一即 `escalated`，不得猜。
- `escalated` 恢复后只能回到 `paused` 或 `abandoned`，不能直接回 `active`——绕过人工处置等于把升级通道作废。

## 12. 交付观察与有限返工

观察源只有四个，全部是持久化证据：

| 源 | 读什么 | 用途 |
| --- | --- | --- |
| 交付 Manifest | `completion.decision`、`verification[].result`、`artifacts[]`、`merged` | 单卡是否交付完成（E340–E342） |
| 任务状态 | `tasks.status`、`blocked_reason` | 是否在等、被卡、已回 ready |
| Run | `runs.phase`、`reconciliation_state`、`generation` | 执行是否结束、是否 `unknown` |
| 事件 | `task_created`、`task_updated`、`plan_applied`、`goal_state` | 还原时间线，不依赖内存 |

单卡「交付完成」= Manifest `completion.decision=done` **且** 代码任务 `merged=true`（E342）。仅有 Run `completed`、仅有 `pass` 验证、仅有退出码 0，都不算（E333、E340）。

返工触发：

| 触发 | 判据 | 提案类型 |
| --- | --- | --- |
| 目标验收失败 | 某 `AC-n` 判 `fail` 且带证据（典型是集成失败：单卡都过、组合起来不过） | `kind=rework`，卡指向失败断言 |
| 单卡被拒 | Manifest `rejected` 且有 `reason`（E360） | `kind=rework`，卡沿用原范围或缩小 |
| 环境失败 | `gitops.IsEnvFailure`、权限、`EPERM` 一类 | **不是返工**：交 `unblock_assist`／人（`maw.lead.selfheal/v1` §2） |
| 证据 unknown | 核实为 `unknown`、采集缺口 | **不是返工**：`escalated`，等人补证据 |

返工提案约束：

- 每张返工卡必须有 `origin.evidence[]`（`delivery_id`、`verification_id`、失败断言 id、失败输出摘要）（G216、G603）。
- 同一证据集至多一个提案：`rework_key = sha256(goal_id + AC-id/rejected-delivery + 证据 id 集合)`。重复事件、重启重放、两个 Lead 同时观察，都只能落到同一个提案（G603）。
- 返工同样要批准、同样走 §8 入队，**不得因为「只是修复」跳过审批**。
- 返工不是无限循环：`max_rework_rounds` 用尽仍 `fail` → `escalated`，退出码 5。用尽后不得自动再加卡（G403）。

## 13. 整体验收与退出条件

聚合规则（逐条验收条件 → 目标结论）：

| 条件判定分布 | 目标结论 |
| --- | --- |
| 全部 `pass`，且每条证据绑定当前修订／任务版本／合入提交 | `done` |
| 存在 `fail` 且返工未用尽 | `active`（返工修订入队） |
| 存在 `fail` 且返工用尽 | `escalated` |
| 存在 `unknown`（无论是否同时有 `pass`） | `escalated`，不得 `done` |
| 有人显式放弃 | `abandoned`（必须写 `reason` + 操作人） |

`done` 的必需要素（缺一不可）：每个 `AC-n` 为 `pass`；每条 `pass` 带结果类证据（E332／E333）；证据绑定当前 `plan_rev` 与当前任务版本；代码类产物已合入（E342）；没有未处理的 `fail`／`rejected`／待返工卡（G602）；映射表无 `drifted`。

退出码是封闭集合，由目标循环驱动器（`maw lead goal run`，PI-304 实现）返回，并写进 `result.exit_code` 供 API／看板使用：

| 码 | 常量 | 条件 | 目标状态 |
| --- | --- | --- | --- |
| `0` | `goal_done` | 全部验收条件 `pass` 且证据齐全 | `done` |
| `2` | `goal_paused` | 人暂停或权限不足（判据明确的非预算原因） | `paused` |
| `3` | `goal_escalated` | 证据 `unknown`、无法自动判断、不变量冲突 | `escalated` |
| `4` | `goal_budget_exhausted` | 预算维度达到上限 | `paused(budget_exhausted)` |
| `5` | `goal_rework_exhausted` | 返工轮次用尽仍 `fail` | `escalated` |
| `6` | `goal_abandoned` | 人显式放弃 | `abandoned` |

- 非零退出必须附 `reason` 与 `evidence`；**不得用 `0` 表示「不知道」**（G604）。
- 单次子命令（`plan`／`approve`／`enqueue`／`accept`／`pause`／`resume`／`status`）沿用通用语义：退出码表示命令本身是否成功，判定结论写在 `result.verdict`；上表六个码只属于目标循环驱动器 `maw lead goal run` 的**终局**。
- 进度（任务 done 比例）不是验收结论：任务 100% done 但集成失败，退出码只能是 5，不是 0。

## 14. 不变量登记表

G210–G216 的判定细则见 §6.1；两处必须一致，改一处即改另一处。其余码只在本表登记。

| 码 | 判定对象 | 不变量 |
| --- | --- | --- |
| G100 | 全文档 | 必填字段存在且类型正确；时间可解析为 ISO8601 |
| G101 | 根节点 | `contract` 必须是 `maw.lead.goal/v1` |
| G110 | `goal_id` | 形如 `gl_<≥8 位小写字母数字>`，不得占用显示编号形态 |
| G111 | `goal_id` | 同工程唯一；退役后也不得复用 |
| G112 | 映射 | 一个任务实例至多归属一个目标修订 |
| G113 | 目标 | 同一 `iteration_id` 同时至多一个非终态目标 |
| G120 | `goal_version` | 从 1 连续递增，冻结后不可改 |
| G121 | 验收条件 | `id` 目标内唯一；每条都有 `kind`/`check`/`statement`/`evidence_required` |
| G122 | `active_version` | 必须等于已冻结最大版本，不得回退 |
| G123 | 状态转移 | 每次转移必须同事务写 `goal_state` 事件（含 from/to/reason/evidence） |
| G200 | `plan_rev` | 从 1 连续递增，不跳号不复用 |
| G201 | 计划修订 | `approved` 之后内容不可变；调整只能新修订 |
| G202 | 审批 | 批准必须绑定 `plan_digest`；摘要不匹配视为未批准 |
| G203 | 审批 | 必须记录 `approver`／`approved_at`／`policy_source`；`auto_enqueue` 也要落策略记录 |
| G204 | 计划修订 | 同一修订至多一次 `applied` |
| G210 | 计划卡 | 必填字段齐全、类型正确 |
| G211 | `card_id` / `id` | `card_id` 修订内唯一；`id` 与蓝图既有及本修订不重复；不得用保留前缀 |
| G212 | `deps` | 目标存在、非退役、非取消、非自身、不成环 |
| G213 | `write_scope` | 合法路径；同修订内重叠必须有依赖序 |
| G214 | 计划卡 | `acceptance`／`verify` 不得是占位文本 |
| G215 | 计划修订 | 卡数不超过 `max_cards_per_revision` |
| G216 | 返工卡 | `kind=rework` 的卡必须带 `origin.evidence[]` |
| G300 | `enqueue_key` | 由确定性公式生成，同批准修订必得同键 |
| G301 | 幂等 | 同键同摘要返回原映射；同键异摘要冲突，不得复用旧结果 |
| G302 | 入队入口 | 只允许追加片段；禁止整份计划文件、禁止 `--reset` |
| G303 | 校验对象 | 校验合并后的完整蓝图，且必须在写盘之前完成 |
| G304 | 基线 | 蓝图基线不匹配不得入队 |
| G305 | 入队副作用 | 不改写既有任务、既有实例版本、活跃 Run 绑定 |
| G306 | 崩溃恢复 | append 与 apply 绑定同一批准操作；任一失败可恢复且不重复追加 |
| G400 | 预算 | 维度与单位固定；缺省值显式落库 |
| G401 | 预算判定 | `unknown` 用量不得判为「未超」；仅在 `observed`（或标明的 `inferred`）时判定 |
| G402 | 超限动作 | 只暂停派发与入队，不取消在飞 Run、不改任务状态 |
| G403 | 上限 | 修订数、返工轮次、卡数均有上限；上限只能由人调高 |
| G500 | 重启 | 目标状态从持久化证据重算，不依赖内存 |
| G501 | 暂停 | `paused` 跨重启保持，直到显式 `resume` |
| G502 | 恢复顺序 | 目标 → 基线 → 台账 → Run 核实 → 验收，顺序固定 |
| G503 | 恢复幂等 | 恢复／重放不得造重复任务、重复映射或重复修订 |
| G600 | 完成 | `done` 需每条验收条件 `pass`，证据绑定当前修订、当前版本与合入提交 |
| G601 | 未知 | `unknown` 不得支撑 `pass` 或 `done` |
| G602 | 完成 | `done` 时不得存在未处理的 `fail`／`rejected`／待返工卡，映射不得 `drifted` |
| G603 | 返工 | 提案必须引用证据；同一证据集至多一个提案 |
| G604 | 退出 | 退出码取 §13 封闭集合；非零必须带原因与证据；`0` 不得表示未知 |
| G605 | 放弃 | `abandoned` 必须写 `reason` 与操作人 |

与上游冲突时以上游为准：身份与版本（E1xx）、Run 与占用（E2xx）、交付与验收（E3xx）、迭代（I1xx）、自愈信号与作业（L2xx）、HTTP 幂等与错误码（H1xx）。

## 15. 字段对照（契约名 ↔ 代码符号／目标符号）

评审时按下表核对命名，禁止发明同义字段。标「PI-30x 新增」的是本契约定义、由对应卡落地的符号。

| 契约名 | 代码符号 / 位置 | 备注 |
| --- | --- | --- |
| 目标记录 `goal` | `internal/goal.Goal`、`internal/db/goal.go`（PI-301 新增） | 表名以 PI-301 为准，字段名必须与 §2 一致 |
| 目标版本 `goal_version` | `internal/goal.Version`（PI-301 新增） | 与任务 `version_no` 同构，不复用 |
| 计划修订 `plan_revision` | `internal/goal.Plan`（PI-301 新增） | 摘要算法见 §5.1 |
| 计划卡 | `blueprint.Task`（`internal/blueprint/blueprint.go`） | 字段名与蓝图 `[[task]]` 一致，`card_id`/`origin` 存 `Extra` |
| 任务映射 | `internal/goal.TaskLink`（PI-301 新增） | 对应 `goal_task_links` |
| 入队台账 | `internal/goal.EnqueueKey`（PI-301 新增） | 对应 `goal_enqueues`，状态 `appended`/`applied`/`failed` |
| 追加入口 | `app.AppendTasks`（`internal/app/blueprint_append.go:27`） | 只追加，`hasNonTaskTable` 拒绝整份计划 |
| 增量合入 | `app.PlanApply`（`internal/app/plan_cmd.go:50`） | 非 reset 路径；`approvals.plan=manual` 需 `--yes` |
| 身份规划 | `app.planTaskIdentity`（`internal/app/task_services.go:166`） | 新建／冻结／退役在这里发生 |
| 定义冻结 | `app.freezeIfDefinitionChanged`（`task_services.go:372`） | 定义变化追加冻结版本 |
| 退役 | `app.retireIdleRemoved`（`task_services.go:412`） | 蓝图外 idle 题退役，非 soft-cancel |
| 保留判定 | `machine.PreserveOnPlanApply`（`internal/machine/machine.go:14`） | `done`/`cancelled`/`blocked`/活跃 Owner 保留 |
| 激活 | `promoteReady`（`internal/app/helpers.go:256`）、`schedule.CanActivateShared`（`internal/schedule/schedule.go:58`） | 依赖 + 写盘 + shared_files 三重判定 |
| 蓝图校验 | `blueprint.Validate`（`internal/blueprint/blueprint.go:135`）、`ValidateLint`（`:248`） | 校验对象是完整文件 |
| 写盘重叠 | `scope.ScopesOverlap`（`internal/scope/scope.go:25`）、`matching.ReasonWriteConflict`（`internal/engine/matching/matching.go:30`） | 计划期用前者，运行期用后者 |
| 拆卡模板 | `app.GenerateTaskBlocks`（`internal/app/lead_split.go:48`） | 现状输出带 TODO 占位，入队前必须补齐（G214） |
| 计划事件 | `plan_applied`、`task_created`、`task_updated`、`task_plan_updated`（`plan_cmd.go:252` 等） | 台账与看板的时间线来源 |
| 交付证据 | `maw.delivery.manifest/v1` 的 `completion`／`verification`／`merge` | 单卡完成的唯一判据 |
| 用量与质量 | `maw.observability.timeline/v1` §4 | 预算的计量来源；`unknown ≠ 0` |
| 目标循环退出码 | `internal/goal.Exit*`（PI-304 新增） | §13 六值封闭集合 |

## 16. 默认开关与回退

| 开关 | 默认 | 打开后发生什么 | 回退 |
| --- | --- | --- | --- |
| `[goal] enabled` | `false` | 目标级 Lead 生效（规划／观察／返工） | 改回 false；已入队任务不受影响 |
| `auto_enqueue` | `false` | 计划校验通过后自动入队，仍需落策略审计 | 改回 false；未入队修订失效 |
| `approval_ttl_hours` | `24` | 批准后超时：审批 `expired`，修订 `invalidated` | 调整数值 |
| `max_plan_revisions` | `5` | 单目标修订上限 | 人可显式调高 |
| `max_rework_rounds` | `2` | 返工轮次上限 | 人可显式调高 |
| `max_cards_per_revision` | `30` | 单次压题上限 | 同上 |
| 预算缺省（§10） | 见 §10 | 目标未显式给预算时按缺省 | 目标级显式覆盖 |

任何打开动作都必须留下审计：`goal_state` 事件 + 修订的 `approval.policy_source`，不另起日志。默认全关是硬要求——目标级 Lead 直接决定写哪些卡、花多少钱，默认打开等于把审批通道变成装饰。

## 17. 验证剧本（本卡 Verify：人工核对）

本卡是文档交付，验证方式是按下面三个剧本**在文档里追踪状态与证据**，逐格对得上才算通过。命令名由本契约固定（PI-303／PI-304 实现），当前评审核对的是「按契约应当出现什么状态与证据」，不要求命令已存在。

三张计划卡，覆盖「无依赖卡」「有依赖卡」「范围内嵌 + 有依赖序」三种形态：

| card | id | deps | write_scope | 说明 |
| --- | --- | --- | --- | --- |
| `c01` | `GL-001` | `[]` | `docs/contracts/vnext/lead/pi-goal-workflow-v1.md` | 文档卡，可立即领 |
| `c02` | `GL-002` | `[GL-001]` | `internal/goal/**` | 代码卡，范围较宽 |
| `c03` | `GL-003` | `[GL-002]` | `internal/goal/lifecycle.go` | 范围包含于 `c02`，但有依赖序 → 合法（G213） |

反例：把 `c03` 的 `deps` 去掉后，`c02` 与 `c03` 范围重叠且无序 → 计划期必须拒绝（`write_overlap_unordered`），而不是等运行期撞 `write_conflict`。

### 17.1 剧本 A：三张卡从规划到目标完成

| 步 | 动作 | 期望状态 | 期望证据 |
| --- | --- | --- | --- |
| A1 | `maw lead goal plan --goal <目标> --cards <三张>` | 修订 `rev1` = `proposed`，目标 `draft` | 蓝图 sha256 与任务数**未变**；只有 `proposed` 记录与 `plan_digest` |
| A2 | `maw lead goal approve --rev 1` | `rev1` = `approved`，`approval.approver` 非空 | 批准记录含 digest 与 `policy_source=manual` |
| A3 | `maw lead goal enqueue --rev 1` | `rev1` = `applied`，目标 `active`；蓝图 `[[task]]` 数 +3 | 三条映射（`GL-001/002/003` → `ti_*`，`version_no=1`）；台账三键 `applied` |
| A4 | 重复 A3 | 返回 `replayed=true`，`rev1` 仍 `applied` | 蓝图任务数不变；台账时间戳不变 |
| A5 | 三次 `maw next --claim` | 依次 `GL-001 → GL-002 → GL-003` | 串行由 deps 与 `write_scope` 保证；无 `write_conflict` |
| A6 | 三卡 `submit` + `merge --yes` | 三张映射 `done` 且 `merged=true`；目标 `active → verifying` | 每条映射绑定一个 `delivery_id` 与合入提交 |
| A7 | `maw lead goal accept` | `AC-1`/`AC-2` 均 `pass`；目标 `done` | 退出码 `0`；证据绑定 `rev1` + 三个 `version_no` + 合入提交 |

### 17.2 剧本 B：一次并发冲突

现场：另一个 Agent 已在 `internal/goal/lifecycle.go` 处于 `in_progress`（人工题或另一目标），Lead 的 `rev2` 新增 `c04`，`write_scope=["internal/goal/**"]`，`deps=[]`。

| 步 | 动作 | 期望状态 | 期望证据 |
| --- | --- | --- | --- |
| B1 | `approve --rev 2` | `approved` | digest 固定 |
| B2 | `enqueue --rev 2` | `applied`（计划期不因运行期占用而拒绝） | 蓝图任务数 +1；`c04` 映射建立 |
| B3 | `maw next --claim` | `c04` 不可领，原因 `write_conflict` | `matching.ReasonWriteConflict`；`schedule.CanActivateShared=false`；既有 `in_progress` 卡状态未变 |
| B4 | Lead 重试 `enqueue --rev 2` | `replayed=true` | 不新增第二张 `GL-004`；台账仍一键 |
| B5 | 在飞卡 `merge` 后重领 | `c04` 可领 | 冲突消失，无人工改范围 |
| B6 | 反例：`c04` 的 `write_scope` 写成 `["**"]` | 计划期拒绝（`write_scope_invalid`） | 蓝图与任务均无变化；修订保持 `proposed` |

禁止的处置：为了让 `c04` 早点跑而把在飞卡改回 `ready`、改 `c04` 的 `deps` 造假、或绕过 `maw merge` 直接推主干。三者都会破坏 `write_scope` 的独占性与 G305。

### 17.3 剧本 C：一次集成失败与有限返工

现场：三张卡均 `done` 且已合入，但目标级 `AC-2`（`go test ./internal/goal -count=1`）在集成后 `fail`——单卡自测都过，组合起来不过。

| 步 | 动作 | 期望状态 | 期望证据 |
| --- | --- | --- | --- |
| C1 | `maw lead goal accept` | `AC-2` = `fail`；目标 `verifying → active`（返工未用尽） | `result.verdict=fail` + 失败输出 + `verification_id`；命令本身退出 0，`run` 在返工用尽时才退 5 |
| C2 | Lead 提返工修订 `rev3`（`kind=rework`，`c05`，`deps=[GL-002]`，`write_scope=["internal/goal/**"]`） | `proposed` | 卡带 `origin.evidence=[delivery_id, verification_id, AC-2]`；`rework_key` 唯一 |
| C3 | 同一失败事件重复投递 / 重启后重放 | 仍是同一个提案 | `rework_key` 相同，不产生第二张 `GL-005`（G603、G503） |
| C4 | `approve --rev 3` → `enqueue --rev 3` | `applied` | `GL-001..003` 的定义版本**不变**；仅新增 `GL-005`（G305） |
| C5 | `c05` 交付合入后重新 `accept` | `AC-2` = `pass`；目标 `done` | 退出码 `0`；证据绑定 `rev3` + `GL-005` 的合入提交 |
| C6 | 反例：`max_rework_rounds` 用尽仍 `fail` | 目标 `escalated` | 退出码 `5`；不再自动加卡，等人处置（G403） |

## 18. 本契约不做的事

- 不实现 `internal/goal`、`internal/db/goal.go`、追加入口与规划入口（PI-301／PI-302／PI-303／PI-304 分别落地）。
- 不定义 Pi 执行器的模式、消息与用量字段（`maw.runtime.state/v1` 与 PI-001 的执行契约负责）。
- 不定义交付 Manifest 的字段与完成判定（`maw.delivery.manifest/v1`），只引用它的结论。
- 不定义系统层信号、作业与自愈动作（`maw.lead.selfheal/v1`）。
- 不改 `maw next`／`maw submit`／`maw merge` 的现有语义与权限模型，不把任何开关默认打开。
- 不规定 Lead 的提示词、模型供应商与上下文裁剪；只规定它必须交出什么结构的提案、以及提案如何被校验、批准与入队。
- 不规定 `goal_id`／`enqueue_key` 的生成实现，只规定形态、确定性要求与不可复用性。
