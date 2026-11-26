# 时间轴与计量契约 v1（冻结）

契约标识：`maw.observability.timeline/v1`
上游契约：`maw.task.instance/v1`（`../task/`）、`maw.runtime.state/v1`（`../execution/`）、`maw.app.ports/v1`（`../ports/`）。
状态：**冻结**。后续看板、报表与计量实现以此为准；改含义只能出 v2。

范围：`task_phase`、`cli_process`、`turn`、`explicit_wait` 四类区间，游标水位与完整性字段，以及按轮次去重的用量计量。**不实现采集器，不改 `internal/db`，不建表，不写查询。**

## 0. 为什么需要这份契约

现在的时间与用量是「按事件顺手累加」：

1. **四种时间混在一起。** 任务在 `in_progress` 多久、CLI 进程活了多久、模型轮次花了多久、显式等待多久，各自回答不同问题；混成一个「耗时」后，等待被算成执行，重试被算成进展。
2. **跨日与乱序没有规则。** 事件按接收顺序入库（`maw.runtime.state/v1` §7），按发生时间统计就会跨日漂移；迟到的记录到底算哪一天，没有定义。
3. **累计用量会重置。** 来源给的 `usage` 可能是累计值，重启／换会话后归零。直接做差会出现负数，直接相加会重复计量。
4. **共享轮次被重复计数。** 一轮对话同时服务两道题时，按任务汇总会把同一份 token 加两次，总量对不上。

本契约把「时间区间」「游标水位」「计量」拆开，并规定聚合只能取并集与去重。

## 1. 四类区间

| 类别 | 回答的问题 | 起止 | 典型来源 |
| --- | --- | --- | --- |
| `task_phase` | 任务处于某阶段多久 | 状态迁移事件 | `status-transitions`、事件流 |
| `cli_process` | 进程实际存活多久 | 进程启动／停止确认 | `ProcessController`、`StopEvidence` |
| `turn` | 一次模型请求到结果返回多久 | 请求发出／结果落盘 | 适配器 Decode／Interpret |
| `explicit_wait` | 明确在等人或等外部条件多久 | 等待声明／解除 | 阻塞原因、人工暂停 |

规则：

- 四类**并行存在**，不得互相替代。`task_phase` 覆盖 `cli_process` 是正常的（进程可能在阶段内重启），`explicit_wait` 也可以覆盖 `task_phase`；只有**同一身份、同一类别**的区间禁止重叠。
- `task_phase` 的 `phase` 必须取 `maw.task.instance/v1` 的 11 个任务状态封闭集合；不认识就拒绝，不猜。
- `turn` 必须绑定 `run_id` 与 `generation`；跨代际的轮次不得合并成一段。
- `explicit_wait` 必须写 `reason`（`blocked`／`manual_pause`／`external_dependency`），没有原因的空等不计入等待。
- 区间身份 `interval_id` 全局唯一；`start_at` 必填，`end_at` 可为 `null` 表示仍未结束；未结束区间不参与「已完成时长」，只计入「进行中」。

## 2. 游标水位与完整性

| 字段 | 含义 |
| --- | --- |
| `watermark.cursor` | 已消费到的最大事件入库序号 |
| `watermark.complete_before` | 该序号之前的区间与轮次视为完整；之后可能仍会补 |
| `watermark.received_at` | 水位生成时间 |
| `gaps[]` | `{from, to, reason}`，采集器离线或事件丢失的区段；必须升序、互不重叠 |
| `covered_from` | 本快照统计覆盖的最早时间 |
| `covered_to` | 覆盖的最晚时间（通常等于水位生成时间） |

- `complete_before <= cursor`，且 `gaps` 必须落在 `[0, complete_before]` 内。
- 水位只前进不后退；不能用「重算一次」把 `cursor` 调小。
- 落在 `gaps` 内的时长与轮次标记为 `unknown`，**不得按均值补齐**，也不得从分母里悄悄剔除。
- 快照必须显式写 `completeness`：`complete`／`partial`／`unknown`；有 gap 或水位之后仍有未消费事件时不得写 `complete`。

## 3. 时间语义：跨日、乱序、时区

- 所有时间存 UTC ISO8601（带 `Z`）；展示层再按 `timezone` 换算。日切按 `timezone` 的本地零点，默认 `Asia/Shanghai`。
- 跨日区间必须给 `day_splits[]`：`{day, start_at, end_at}`；各段时长之和必须等于原区间时长，段之间不得重叠，且不得超出原区间。
- 事件按**发生时间**归入时间桶，按**接收游标**保证增量不丢。迟到记录必须带 `late_arrival: true` 与 `received_at`，允许回溯修正当天统计，但**不得回退游标**，也不得产生负时长。
- 乱序到达不能重排已发出的报表；报表必须带 `watermark` 与 `generated_at`，读者据此知道数据截止点。

## 4. 用量计量

| 字段 | 规则 |
| --- | --- |
| `turn_id` | 全局唯一；计量主键 |
| `input_tokens` / `output_tokens` | 非负整数；未知时为 `null`，不得填 0 |
| `cache_read_tokens` / `cache_write_tokens` | **单独字段**，不得并入 `input_tokens`（来源的 `input_tokens` 已含缓存分解） |
| `total_tokens` | 若出现，必须等于 `input + output`；不含缓存字段 |
| `quality` | `observed`／`estimated`／`unknown`；`estimated` 必须给 `basis` |
| `epoch` | 累计计数的第几代；从 1 开始 |

### 4.1 累计值重置

来源给的是累计值时，同一 `turn_id` 的前后快照可能变小：

- 数值下降即判定**新代际开始**：`epoch + 1`，并写 `reset_observed: true`；增量按 `max(0, current - previous)` 计算。
- 禁止把下降当负数累加，也禁止跨代际直接相减。
- 每个 epoch 的增量各自记账；`epoch` 变化不影响 `turn_id` 唯一性与去重。

### 4.2 共享轮次

一轮同时服务多道题时（`shared_with[]` 非空）：

- 用量只按 `turn_id` 记一次；任务视图可以分摊，但各任务分摊之和必须等于该轮总量，不得放大。
- `turn_count` 按去重后的 `turn_id` 计数，不按「任务×轮次」计数。
- 共享轮次不得拆成多条 `turn` 记录来「方便查询」。

### 4.3 未知用量

`quality = unknown` 时必须：

- `input_tokens`／`output_tokens` 为 `null`，不得用 0 冒充；
- 该轮仍计入 `turn_count`，但不计入 token 总量；
- 快照 `usage_unknown: true`，报表必须显示「部分未知」，不得只给一个看起来完整的数字。

## 5. 聚合规则

- 时长按「同一身份 + 同一类别」的**并集**计算，不是各区间相加；重叠部分只算一次。
- 跨身份（不同任务或不同 Run）的区间可以重叠，聚合时按身份分别求并集后再相加。
- token 按 `(turn_id, epoch)` 去重后相加；共享轮次只加一次。
- 同一份时间或同一份用量不得重复计量；去重后仍无法解释的差异必须如实报出，不得抹平。
- 报表必须同时给出 `watermark`、`completeness`、`quality` 与 `gaps`；缺任一字段的报表视为不可比较。

## 6. 快照结构

```jsonc
{
  "contract": "maw.observability.timeline/v1",
  "project_id": "716c30878ba1b95f",
  "generated_at": "2026-10-23T03:40:00Z",
  "timezone": "Asia/Shanghai",
  "completeness": "partial",
  "watermark": {
    "cursor": 128,
    "complete_before": 120,
    "received_at": "2026-10-23T03:39:58Z",
    "gaps": [{ "from": 90, "to": 95, "reason": "collector_offline" }]
  },
  "covered_from": "2026-10-22T16:00:00Z",
  "covered_to": "2026-10-23T03:40:00Z",
  "intervals": [
    {
      "interval_id": "iv_0001",
      "kind": "task_phase",
      "instance_id": "ti_vn006a",
      "run_id": "run_0001",
      "generation": 1,
      "phase": "in_progress",
      "start_at": "2026-10-23T02:00:00Z",
      "end_at": "2026-10-23T04:00:00Z",
      "quality": "observed",
      "evidence_ref": "event:118",
      "day_splits": [
        { "day": "2026-10-23", "start_at": "2026-10-23T02:00:00Z", "end_at": "2026-10-23T04:00:00Z" }
      ]
    }
  ],
  "turns": [
    {
      "turn_id": "tu_0001",
      "instance_id": "ti_vn006a",
      "run_id": "run_0001",
      "generation": 1,
      "started_at": "2026-10-23T02:10:00Z",
      "ended_at": "2026-10-23T02:11:30Z",
      "usage": {
        "input_tokens": 1200,
        "output_tokens": 300,
        "cache_read_tokens": 800,
        "cache_write_tokens": 100,
        "total_tokens": 1500,
        "quality": "observed",
        "epoch": 1,
        "reset_observed": false
      },
      "shared_with": [],
      "late_arrival": false
    }
  ],
  "totals": {
    "task_phase_seconds": 7200,
    "cli_process_seconds": 0,
    "engineering_seconds": 0,
    "turn_count": 1,
    "usage_total_tokens": 1500,
    "usage_unknown": false
  }
}
```

`engineering_seconds` 指 `explicit_wait` 之外的净执行时长（`task_phase` 与 `cli_process` 的并集），与等待分开报，不合并成一个数字。

## 7. 不变量登记表

机检主体是 `observability_contract.py`；本表由 `test_observability_contract.py` 强制对齐，每条至少一个 `samples/invalid/` 反例。

| 码 | 判定对象 | 不变量 |
| --- | --- | --- |
| T100 | 全文档 | 必填字段存在且类型正确；时间可解析为 ISO8601 |
| T101 | 根节点 | `contract` 必须是 `maw.observability.timeline/v1` |
| T110 | `interval_id` / `turn_id` | 同快照内分别不得重复 |
| T111 | `intervals[].kind` | 只能是 task_phase／cli_process／turn／explicit_wait |
| T112 | 区间 | `end_at >= start_at`；未结束只能 `null`；不得负时长 |
| T113 | 区间 | 同一身份同一类别不得重叠 |
| T114 | 区间 | `run_id`／`generation` 必须成对出现；`phase` 属于封闭集合 |
| T120 | `watermark` | 水位不后退；`complete_before <= cursor`；gaps 升序不重叠 |
| T121 | `quality` | 只能是 observed／estimated／unknown；estimated 必须给 basis |
| T130 | `day_splits` | 分段时长之和等于区间时长；段不重叠且在区间内 |
| T140 | `usage` | token 非负；`total = input + output`；缓存字段不并入 input |
| T141 | 累计重置 | 下降只能由新 epoch 承担；不得出现负增量 |
| T142 | 未知用量 | unknown 时 token 为 null，不得填 0 或估算 |
| T143 | 共享轮次 | `turn_id` 唯一；`turn_count` 按轮次去重 |
| T144 | 迟到事件 | 允许回溯，但必须标记 `late_arrival` 且不回退水位 |
| T145 | 聚合 | 时长按并集、用量按轮次去重；总数必须与明细重算一致 |

## 8. 正反例

样本是一份完整快照；反例期望码登记在 `test_observability_contract.py` 的 `INVALID_EXPECTATIONS`。

正例（`samples/valid/`）：

| 文件 | 覆盖现场 |
| --- | --- |
| `01-cross-day.json` | 跨本地零点的 task_phase，day_splits 与并集时长一致 |
| `02-out-of-order.json` | 迟到事件回溯当天，水位不回退，completeness=partial |
| `03-cumulative-reset.json` | 累计用量跨 epoch 重置，增量为正 |
| `04-shared-turn.json` | 一轮共享两题，token 只计一次 |
| `05-unknown-token.json` | 未知用量为 null，turn_count 仍计入 |
| `06-explicit-wait.json` | 等待与执行并行，工程时长取并集 |

反例（`samples/invalid/`）：重复 id、非法类别、负时长、同身份重叠、水位倒挂、gaps 重叠、estimated 缺依据、day_splits 不闭合、total 不等于 input+output、缓存并入 input、未知用量填 0、共享轮次重复计量、累计重置跨代相减为负、迟到未标记。

验证命令（本卡 Verify）：

```sh
python3 -m unittest discover -s docs/contracts/vnext/observability -p 'test_*.py'
```

## 9. 本契约不做的事

- 不实现采集器、存储与查询，不改 `internal/db`、`internal/board`、`internal/runner`。
- 不定义告警阈值、SLO 与报表 UI；不改 `max_parallel` 与自动执行开关。
- 不规定 token 的计价与账单；只规定去重、未知与重置的计量语义。
