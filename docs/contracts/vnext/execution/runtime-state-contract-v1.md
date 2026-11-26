# 运行状态与资源契约 v1（冻结）

契约标识：`maw.runtime.state/v1`
上游契约：`maw.task.instance/v1`（`../task/task-instance-contract-v1.md`）——本文件只谈「跑起来之后」，任务身份不在这里重复定义。
状态：**冻结**。VN-003 起以它为引用基线；改含义只能出 v2。

范围：Run、Operation、资源占用、心跳核实、标准事件之间的一致性规则。**不建表、不调厂商 CLI、不改现有代码。**

## 0. 为什么需要这份契约

现有实现把「跑没跑起来」压在一个字段里：`tasks.status` 走到 `in_progress` 就算在跑，`heartbeat` 过期就算掉线（`internal/app/worker_cmd.go:66`、`_ = expireStaleAgents(tx, pc)`）。这带来三个说不清的现场：

1. **心跳丢了，到底是没在跑还是没上报？** 现有代码只能二选一：继续当它在跑，或者释放掉。而「不知道」是第三种状态，必须能表达。
2. **取消和正常完成同时发生，谁说了算？** 没有顺序规则时，后到的写覆盖先到的写，同一个 Run 一会儿 completed 一会儿 cancelled。
3. **节点槽和目录是两种资源，却常常一起放。** 进程停了槽就该空出来，但目录里还躺着待验收的分支，提前放掉下一题就把它冲了。

契约把这三件事拆开：**运行阶段、核实状态、资源占用各自独立**，用代际（generation）做条件写入。

## 1. 三类状态，三套语义

| 层 | 载体 | 说明什么 | 谁可以改 |
| --- | --- | --- | --- |
| 业务任务状态 | `tasks.status`（backlog…done/cancelled） | 这道题对计划意味着什么 | 调度与完成契约 |
| 运行状态 | `runs.phase` + `reconciliation_state` | 这一次执行到哪了、我们有多确定 | Supervisor / Runner |
| 交付验收状态 | 产物候选 → 验证 → 合入 | 结果够不够格算完成 | 验收与合入链路（VN-003） |

三者不互相推导：Run `completed` 不等于任务 `done`，Run `failed` 也不必然把任务打成 `failed`（任务按策略进 `blocked` 或重新 `ready`）。

**`completed` 只说明运行协议走完并产出了可判定结果**，不代表验收通过。

## 2. 运行阶段是封闭集合

```text
transitions
reserved -> preparing, stopping
preparing -> launching, stopping, failed
launching -> running, stopping, failed
running -> waiting, stopping, completed, failed
waiting -> running, stopping, completed, failed
stopping -> cancelled, timed_out
```

- 十个阶段：`reserved`、`preparing`、`launching`、`running`、`waiting`、`stopping`、`completed`、`failed`、`cancelled`、`timed_out`。
- 终态四个：`completed`、`failed`、`cancelled`、`timed_out`，**不再出发任何转移**。重跑一律开新 Run（新代际），不复活旧 Run。
- 任何阶段都不得回到 `reserved`；`reserved` 只能作为起点。
- `phase_history` 必须逐条记录，时间严格递增，末项等于当前 `phase`。历史是重建时间轴的唯一依据。
- `waiting` 必须带可验证原因（等输入、等授权、等外部服务），**不因为「没有输出」就自动判成等待**。

## 3. 核实状态：不知道就说不知道

`reconciliation_state ∈ {confirmed, unknown}`。

- 心跳丢失、中枢重启后查不清 → `unknown`。**`unknown` 不得直接落终态**（E232）：观测缺失不是停止证据。
- `unknown` 期间占用继续保留，节点槽不释放、目录不重分配。
- `unknown` 解除时按证据回落：能确认结束就补录结果，查不清就继续挂着等人处置。

配套两个字段一起用：

| 字段 | 含义 |
| --- | --- |
| `desired_action` | 期望动作（`none` / `stop`），表达意图，不代表已发生 |
| `process_stop_confirmed` | 进程确实已停止的证据位；**只有它为真才算释放资格** |

## 4. 占用：三种资源分别释放

| 资源 | 何时可释放 | 释放前置 |
| --- | --- | --- |
| `node_slot` | 进程停止且已确认 | `process_stop_confirmed = true`，且运行不再计入占用 |
| `workspace` | 产物冻结、验收与合入结束 | `delivery_frozen = true`（E242） |
| `merge` | 合入完成 | 按 VN-003 交付契约 |

- 释放是条件写入：`release_generation` 必须等于 `generation`（E243），否则拒绝——用旧代际去释放新代际持有的资源，等于把别人手里的东西放掉。
- 每次释放必须留下 `lease.released` 事件（E261），否则时间轴上看不出是谁、什么时候放的。

### 4.1 什么算「仍然占用」

一个 Run 只要满足任一条，就继续计入占用（E240 的判定基准）：

1. 阶段不是终态；
2. `reconciliation_state = unknown`；
3. 阶段是 `failed`/`cancelled` 但 `process_stop_confirmed = false`。

第 3 条是现状最容易漏的：进程退出码拿到了、状态写了 `failed`，但进程本身可能还没退干净（`internal/app/worker_cmd.go` 目前只按状态字符串判断可重派）。同一任务实例出现两个有效占用就是重复派发，两份进程会覆盖同一份产物。

## 5. 取消与完成的竞争

- 取消先写持久 Operation 和目标状态，由 Runner 执行停止并留下证据；**取消是意图，`cancelled` 是事实**，中间隔着 `process_stop_confirmed`（E231）。
- **结束事实先到，取消就只能认输**：运行已是 `completed`/`failed`/`timed_out` 时，取消操作必须返回 `already_finished` 及原结果，不得把阶段追改成 `cancelled`（E250、E251）。
- 顺序按持久事务的确认顺序裁定，不按请求到达顺序。
- 「停止本次运行」与「取消业务任务」是两个操作：前者保留任务与已有结果，后者要先让 Run 停稳并把资源核实清楚。

## 6. 代际（generation）

- 每个 Run 有单调递增的 `generation`，从 1 开始；同一任务实例上新一代开始，旧代际的写入立即失效。
- 代际失效只拦住**过期状态的提交**，拦不住旧进程直接写文件；因此无法证明旧进程停止时，目录保持隔离、不可重新分配。
- 事件、占用释放、操作完成都必须携带代际，条件不匹配就拒绝，而不是「以最新为准」。

## 7. 标准事件：身份、时间、同事务

| 字段组 | 规则 |
| --- | --- |
| 身份 | `event_id` 全局唯一（E212）；`cursor` 是**入库顺序**，严格递增（E213） |
| 版本 | 必须带 `schema_version` 整数（E214）；不认识就拒绝，不按最新版猜 |
| 时间 | `occurred_at`（发生）与 `received_at`（接收）分别保存；**游标按接收排序，不按发生排序** |
| 质量 | `quality ∈ {observed, estimated, unknown}`；发生晚于接收说明来源时钟不可信，必须降级（E262） |
| 关联 | `run_id` 等引用必须指向本地已存在的身份（E263）；未归属的来源记录保留引用即可，不硬塞 |
| 同事务 | 状态变更必须与对应事件同批提交：当前 `phase` 必须有 `run.phase` 事件（E260），释放必须有 `lease.released` 事件（E261） |

迟到事件按发生时间重算受影响的时间桶，但**不回头改游标**。出现时间倒置、边界缺失时标质量，不静默产出负时长（E223），也不修正成「看起来很完整」。

## 8. 自动执行默认关闭

`auto_execution` 必须显式出现，默认关闭：

```jsonc
"auto_execution": { "enabled": false, "managed_execution_limit": 0 }
```

- `enabled = true` 时必须给出正整数 `managed_execution_limit`（E270）——声明自动派发却没有并发护栏，等于放弃唯一的全局刹车。
- `max_parallel` 保持「看板参考」语义，不因本契约改变。真正的上限是 `managed_execution_limit`（全局）与节点 `slot_limit`（单节点），两者只约束受管执行，不给人手动 `next` 追加计算配额。

## 9. 不变量登记表

机检主体是 `runtime_contract.py`；本表由 `test_runtime_contract.py` 强制对齐，并且**每条都必须有反例触发**（无样本背书的规则等于没写）。

| 码 | 判定对象 | 不变量 |
| --- | --- | --- |
| E200 | 全文档 | 必填字段存在且类型正确；时间可解析为 ISO8601 |
| E201 | 根节点 | `contract` 必须是 `maw.runtime.state/v1` |
| E210 | `run_id` | 同一快照内不得重复 |
| E211 | `operation_id` | 同一快照内不得重复 |
| E212 | `event_id` | 标准事实身份不得重复 |
| E213 | `cursor` | 严格递增（表达入库顺序，不是发生顺序） |
| E214 | `schema_version` | 事件必须携带正整数契约版本 |
| E220 | `phase` | 只能取封闭集合内的十个阶段 |
| E221 | `phase_history` | 转移必须合法；终态不得再出发；起点必须是 `reserved` |
| E222 | `phase` | 阶段历史末项必须等于当前阶段 |
| E223 | 时间 | 阶段时间严格递增；结束不得早于开始 |
| E230 | 终态证据 | `failed`/`cancelled`/`timed_out` 必须带证据 |
| E231 | `cancelled` | 必须先确认进程已停止 |
| E232 | `unknown` | 核实状态未知时不得落终态 |
| E240 | 占用 | 同一任务实例至多一个有效占用 |
| E241 | `node_slot` | 运行仍计入占用时不得释放节点槽 |
| E242 | `workspace` | 产物未冻结时不得释放工作目录 |
| E243 | 释放 | 释放代际必须等于持有代际 |
| E250 | 阶段历史 | 已确认终态之后不得再追改成 `cancelled` |
| E251 | 取消操作 | 目标已结束时必须返回 `already_finished` |
| E252 | 幂等键 | 同键不同请求摘要必须判冲突，不得复用旧结果 |
| E260 | 运行阶段 | 当前阶段必须有同事务提交的 `run.phase` 事件 |
| E261 | 占用释放 | 释放必须留下 `lease.released` 事件 |
| E262 | 事件质量 | 发生晚于接收不得标为 `observed` |
| E263 | 事件关联 | 不得引用不存在的运行 |
| E270 | 自动执行 | 开启时必须配置正整数受管执行上限 |

## 10. 正反例

正例（`samples/valid/`）：

| 文件 | 覆盖现场 |
| --- | --- |
| `01-reserved-run.json` | 刚领取、未启动：双占用就位，派发操作仍在排队 |
| `02-running-unknown.json` | 心跳丢失 → `unknown`，占用保留，不伪造终态 |
| `03-cancel-race-finished-wins.json` | 完成先到：取消返回 `already_finished`，槽与目录按各自条件释放 |
| `04-failed-stop-unconfirmed-still-occupies.json` | `failed` 但进程未确认停止 → 仍占用，下一次派发只能排队 |
| `05-late-and-clock-skew.json` | 迟到事件按接收排游标；时钟不可信降级为 `estimated` |
| `06-generation-relay.json` | 旧代际停稳后退役，新代际同实例重跑；旧代际迟到提交只能被拒 |

反例（`samples/invalid/`，每个文件用 `expect_error` 声明应触发的码）：非法转移、终态重启、`unknown` 落终态、进程未停就放槽、产物未冻结就放目录、代际不匹配释放、同实例双占用、`cancelled` 未确认停止、取消追改已完成、状态缺事件、事件身份重复、游标重复、负时长、自动执行无上限、时钟倒置标 observed，以及缺字段、错契约版本、运行/操作身份重复、缺 `schema_version`、未知阶段、历史末项不符、终态无证据、取消未报 `already_finished`、幂等冲突、释放无事件、事件挂到不存在的运行。

验证命令（本卡 Verify）：

```sh
python3 -m unittest discover -s docs/contracts/vnext/execution -p 'test_*.py'
```

## 11. 本契约不做的事

- 不新增数据库表，不改 `internal/db`、`internal/app`、`internal/schedule`。
- 不定义产物候选、Manifest 与验收结论（VN-003）。
- 不定义接入器字段映射与厂商事件解析（VN-301 起）。
- 不规定停止进程的具体手段；只规定「停止必须有证据、证据缺失就保持占用」。
