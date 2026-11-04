# DeepSeek Harness「智能团队」对 maw 的可借鉴沉淀

日期：2026-11-04（Asia/Shanghai）。  
范围：对照 [DeepSeek Harness](https://www.deepseek.com/harness/) 实验包 Agent Teams（`dsh-experimental-agent-team` / `tool-agent-team`）与本仓 maw 任务中枢；本次**仅沉淀设计决议与后续拆包草案**，不改功能、不 `plan apply`。

参考材料：

- 产品页：`https://www.deepseek.com/harness/`
- 上游说明：`packages/experimental/agent-team`、`docs/subsystems/agent-team.md`
- 社区缺口讨论：[Discussion #10770](https://github.com/deepseek-ai/deepseek-harness/discussions/10770)（生命周期可见性、停滞信号、结构化 complete、预算等）

## 结论先行

**不对接、不仿造会话内 Team 运行时**；吸收其「多 Agent 可信协作」原语，补强 maw 已有中枢（全局库、强制 `write_scope`、worktree、submit/merge、看板）。

| 判断 | 项 | maw 落地方式 | 优先级 |
| --- | --- | --- | --- |
| 直接采用 | 任务变更 CAS / `revision` | 改描述、改派、释放、回板均带版本；过期写拒绝 | P0 |
| 直接采用 | 分型协作消息 | `progress` / `question` / `blocker` / `final` 扩展 status/report 与 feed | P0 |
| 直接采用 | 结构化完成物 | `submit` / 交付可选 `result`（摘要、产物路径、验收勾选） | P0 |
| 直接采用 | stalled vs idle | 心跳区分工作中 / 停滞 / 空闲；超时释放人工占用 | P0 |
| 改造后采用 | 事件等待 | `maw wait` / SSE：等 deps、状态变化或超时，替代盲轮询 `next` | P1 |
| 改造后采用 | 具名角色 + 定向改派 | Agent 标签/能力；中枢或 Lead 可 reassign（非会话 spawn） | P1 |
| 改造后采用 | 创建上下文 `fresh` / `fork` | 受管/系统拆题时选干净上下文或继承摘要 | P1 |
| 改造后采用 | 权限分层 | 仅中枢/Lead 可改派、中断、建成员；成员限 claim/上报/完成 | P1 |
| 改造后采用 | 活动任务默认视图 + 归档 | 看板默认 pending/in_progress；done/deleted 可切换；`archive` 中间态 | P2 |
| 有来源后采用 | 成员/任务预算 | token/时长上限与可见性；依赖真实计量后再开 | 后续 |
| **不采用** | 会话内共享 cwd 队友 | 与持久 worktree 冲突 | 不排期 |
| **不采用** | `write_scopes` 仅警告 | maw 强制拦越界是优势 | 不排期 |
| **不采用** | 不活动不释放 owner | 反面教材；对齐占用过期释放 | 不排期 |
| **不采用** | 扁平不可改名/不可删 roster | 运维痛苦 | 不排期 |
| **不采用** | 任务真相绑在会话日志 | 与 `~/.maw/maw.db` 中枢目标冲突 | 不排期 |
| **不采用** | 原生接收 Harness Team 任务板 | 无跨进程协议；不建桥除非另开产品题 | 不排期 |

## 定位对照（为何只借原语）

| 维度 | DeepSeek Agent Teams | maw |
| --- | --- | --- |
| 目标 | 一次会话里 Lead + 具名队友并行拆活 | 本机多工程：拆题、派发、隔离改动、合入、看板 |
| 任务存哪 | Lead 会话日志内的共享任务板 | 全局 `~/.maw/maw.db` + 蓝图 |
| 协作范围 | 单进程、共享 checkout；无远端成员/worktree/merge | 每 Agent 持久 worktree；submit 后 merge |
| 执行者 | Harness 内子 Agent（同套工具循环） | 外部 IDE/Agent；maw 管状态与流程 |
| write scope | 建议性路径提示，不锁 | 强制检查，越界提交拒绝 |
| 对外 | 实验包，无跨进程协调承诺 | CLI / HTTP / SSE 本机中枢 |

含义：两者都有「任务板 + owner + 依赖」味道，但一层是**会话内协作**，一层是**工程级流水线**。maw 应继续做后者，只把前者验证过的并发与信号设计搬过来。

## 我方已有、无需回退的能力

- 蓝图 `deps` + 写盘序位调度；重叠 scope 串行（强于纯警告）
- 持久 worktree、`submit` 前 dirty 检查、`merge --yes`
- `maw status report` / feed / SSE；system agent 对 `blocked` 的自愈入口
- 心跳与占用相关逻辑（含「过期占用须释放」类缺口，见 `docs/maw_gap_可执行任务清单.md`）

借鉴时应**叠加上去**，不要用「共享 cwd + 建议 scope」削弱现有约束。

## 采纳说明（P0）

### 1. 任务变更 CAS / `revision`

- **借鉴点**：每次任务快照带单调 `revision`；更新带期望版本，冲突则拒绝而非静默覆盖。
- **maw 动机**：看板、system agent、人工 CLI 可能并发改派/改描述/放回；缺乐观锁会丢决策。
- **边界**：领取（claim）事务可继续用现有引擎；本项聚焦**已存在任务的元数据与归属变更**。
- **非目标**：不做跨机器分布式锁；不做 DeepSeek 同款 TeamId 品牌类型。

### 2. 分型协作消息

- **借鉴点**：消息类型 `progress` / `question` / `blocker` / `final`，便于编排而非纯自然语言。
- **maw 落地**：扩展 `status report` 与 `/api/status`（或并列 `kind` 字段）；feed/看板按类型筛选与通知。
- **与现况关系**：现有 `phase` / `pct` / `working` 可映射到 `progress`；`blocked` 对齐 `blocker`。
- **非目标**：不做 Agent 间任意邮箱投递保证（ack/重试/TTL）的第一版；先中枢可见、可订阅。

### 3. 结构化完成物

- **借鉴点**：`complete` 携带 `result`，Lead 不必只靠读对话聚合。
- **maw 落地**：`submit`（及旁路收工 report）可选 JSON：摘要、产物路径列表、验收勾选；写入事件/交付清单供看板与 system agent 消费。
- **非目标**：不替代 git commit/merge；`result` 是交付元数据，不是代码本身。

### 4. stalled vs idle + 超时释放

- **借鉴点**：`inactive` 不等于完成；需 `lastActivityAt`、失败原因、超时与「无变化」区分。
- **maw 落地**：Agent/Run 展示态区分 working / stalled / idle；人工领取且无受管进程证据时，心跳过期必须释放任务回 `ready`（或合法 `blocked`），禁止长期 `occupancy_kept` 死锁。
- **对齐**：与 gap 清单中占用过期释放题合并推进，避免两套语义。

## 采纳说明（P1–P2，摘要）

| ID 草案 | 内容 | 备注 |
| --- | --- | --- |
| 事件等待 | deps 满足、任务/Agent 状态变化或超时唤醒 | 复用 SSE/feed；CLI 可 `maw wait` |
| 角色与改派 | Agent 标签；中枢 reassign | 不实现会话内 `spawn_teammate` |
| fresh/fork | 受管/系统起跑的上下文策略 | 与 runner launch_spec 扩展相关 |
| 权限分层 | 契约写明谁可 interrupt/reassign | 对齐 manual vs managed 边界 |
| 归档视图 | 默认活动项；archive 保留审计 | 可与看板 iteration 筛选同波 |

## 明确不排期

1. **接收 DeepSeek Harness 智能团队任务板** — 无稳定跨进程 API；Team 自身声明不支持跨进程协调。  
2. **在 maw 内嵌 Harness 式 Lead/Teammate 运行时** — 非目标见路线图「不替代各 IDE 内置 Agent 运行时」。  
3. **弱化 write_scope 为提示** — 禁止。  
4. **共享单一 checkout 并行写** — 禁止；并行必须走 worktree + scope。

## 可执行任务清单

人读题卡（含 L1/L2、Lane、Surface）：[maw_collab_primitives_可执行任务清单.md](maw_collab_primitives_可执行任务清单.md)。  
迭代 `IT-COL-001`；主分支 `main`；**已** `plan apply`，十题全部合入（`d23e87f` → `41f061b`）。

要点：`COL-CTR-001` 契约闸门 → 并行 CAS/MSG/STL/CTX/ARCH；MSG 后续 RES/WAIT；ROLE（含 main 串行）→ ACL。`COL-STL-001` 不重做已落地的 CQ-003 过期释放。

### 落地对照（2026-11-04 收尾）

| 优先级 | 项 | 状态 | 提交 |
| --- | --- | --- | --- |
| P0 | 任务变更 CAS / `revision` | 已落地 | `cae2daf`（`COL-CAS-001`） |
| P0 | 分型协作消息 kind | 已落地 | `5ddb87b`（`COL-MSG-001`） |
| P0 | 结构化完成物 `result` | 已落地 | `8264b2e`（`COL-RES-001`） |
| P0 | stalled vs idle | 已落地 | `4303ad6`（`COL-STL-001`） |
| P1 | 事件等待 `maw wait` | 已落地 | `9e84e33`（`COL-WAIT-001`） |
| P1 | 角色标签 + 定向改派 | 已落地 | `0e5f1fa`（`COL-ROLE-001`） |
| P1 | 创建上下文 fresh / fork | 已落地 | `a4d14f2`（`COL-CTX-001`） |
| P1 | 权限分层 | 已落地 | `41f061b`（`COL-ACL-001`） |
| P2 | 活动任务默认视图 + 归档 | 已落地 | `eceb7a2`（`COL-ARCH-001`） |
| 后续 | 成员/任务预算 | 未排期 | — |

契约正文：`docs/contracts/vnext/collaboration/`（`COL-CTR-001`，提交 `d23e87f`）。

## 与现有清单关系

- 看板/受管补齐：`docs/maw_board_product_gaps_可执行任务清单.md`（`IT-BP-001`）— **不阻塞**本沉淀；归档视图可与 BP 统计/筛选波次协调，勿重复拆同一 Surface。  
- 能力缺口：`docs/maw_gap_可执行任务清单.md` — **CQ-003 占用过期释放已落地**；`COL-STL-001` 只补 activity 三态，禁止两套心跳语义。  
- 路线图：见 [zh/ROADMAP.md](zh/ROADMAP.md)「协作原语（借鉴 DeepSeek Agent Teams）」条目。

## 修订

| 日期 | 说明 |
| --- | --- |
| 2026-11-04 | 初稿：对照 Harness 智能团队，冻结采纳/不采纳与拆包草案 |
| 2026-11-04 | 拆出可执行任务清单 `maw_collab_primitives_可执行任务清单.md`（v1.0） |
