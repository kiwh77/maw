# 系统 Agent 操作说明

默认关闭。打开后，maw 可在任务 `blocked` 时自动入队并编排诊断 / 拆题 / 运维，无需人工逐个通知 Cursor／WorkBuddy。

## 它和「自动派发」不是同一个开关

配置里还有 `[managed] auto_enabled`。两个开关驱动两条不同的循环，**互不推导**：

| 你想解决的问题 | 该开哪个 | 另一个不因此生效 |
| --- | --- | --- |
| 任务卡住了要自动诊断／拆题／跑运维 | `[system_agent] enabled = true` | 不会让任务被自动派发出去 |
| 可领取的任务要自动派给受管执行 | `[managed] auto_enabled = true`（另需 `limit >= 1` 与 macOS） | 不会让卡住的任务自愈 |

两个都关（默认）时完全不自动跑。两条循环共用同一个 leader 与同一把
`coordinator.lock`，但开关互不影响。完整表格、`maw system status` 的字段与
`serve` 启动日志见[操作指南的「两个开关」](operations-guide.zh-CN.md#两个开关自动派发与系统-agent)，
那里是唯一权威口径；看板设置页只读展示同一份状态。

## 配置

`.multiagent/config.toml`：

```toml
[system_agent]
config_version = 1
enabled = true
poll_interval_sec = 5
max_concurrent = 1
llm_node = ""
worker_node = "codebuddy-lab"   # relaunch_worker 时用
llm_command = ["your-llm-cli", "--print"]  # stdin=prompt，stdout=JSON 决策
auto_kinds = ["unblock_assist"]
escalate_patterns = ["needs_human", "auth_required"]
cooldown_sec = 300
max_attempts = 3
ops_allowlist = ["maw next", "maw merge --yes", "maw prune", "maw task unblock"]
```

未配置 `llm_command` 时，诊断默认 `escalate_human`（安全降级）。

## CLI

```bash
maw system status
maw system trigger --kind unblock_assist --task T-1 -m "scope too large"
maw system trigger --kind task_split -m "把需求拆成可领题"
maw system trigger --kind ops_run -m "maw prune"
maw system run                 # 消费队列一步
maw system run --job sjob_…
maw system watch               # 常驻（与 dispatch 共用 coordinator.lock）
maw system watch --once
```

`maw board --serve` / `maw serve` 在 `enabled=true` 时会同进程拉起 watch。

## HTTP

- `GET /api/v1/system-jobs?status=&project_id=`
- `POST /api/v1/projects/{id}/system-jobs` JSON: `{kind,task_id,reason,message}`

鉴权同看板 localauth（需 write）。

## 决策 JSON

```json
{"action":"escalate_human|relaunch_worker|split_tasks|unblock_and_retry|ops_run|notify_only","reason":"...","blueprint":"...","ops_command":"..."}
```

- `split_tasks`：蓝图片段写入 `.multiagent/handoffs/system-split-*.toml`；仅当 `[approvals].plan = "auto"` 时自动 `plan apply`。
- `ops_run`：命令必须命中 `ops_allowlist`。

## Runner 接线

`maw __runner` 若在 `ops/<operation_id>/launch_spec.json` 发现启动规格，会接入 `session.Runner`（passthrough Decode/Interpret）。可选 `run_meta.json` 提供 `run_id` / `global_db` 等。

## 自动执行开关（工作台）

工具栏「接入」左侧的开关是**一个**开关：打开它 = 让中枢用所选执行节点自动领题并执行。

它一次写齐四个字段，因为只写 `auto_enabled` 只会推进派发队列，而真正「领题」的那一步是
`auto_select` 产出的 `dispatch_ready`：

```toml
[managed]
auto_enabled = true          # 推进派发队列
auto_select = true           # 生产 dispatch_ready（真正领题）
limit = 1
allowed_nodes = ["dsh-local"]
[system_agent]
worker_node = "dsh-local"    # 自愈重跑时复用同一节点
```

规则：

- **节点接不了活时拒绝开启**，返回稳定原因码（`node_stale`／`node_not_ready`／`node_disabled`／
  `no_candidate_node`／`platform_unsupported`／`managed_execution_limit`）。槽位占满
  （`node_occupied`）不算拒绝理由——那是「忙」，空槽后会自然接单。
- 打开前会**当场探活**一次刷新 `observed_at`：匹配层只认 5 分钟内的观测，
  否则「刚登记但还没跑过 serve」的节点永远开不起来。
- `applied=false` + `reason=serve_not_running` 表示配置已保存、但中枢没在跑：
  先 `maw serve`。界面会如实显示「已保存，中枢启动后生效」。
- **热生效**：`maw serve` 内的 leader 每轮对比 `config.toml` 的 mtime/size，变化即就地重算
  三个开关与回调，不重启、不重新抢锁。变化会落一条 `lead_switches_changed` 事件。

## Router 介入

「Router」指系统 Agent 层。它要真的能解决问题，三件事缺一不可：

1. **信号产作业**：五类队列卡死信号按 `auto_kinds` 产作业（默认只放行 `unblock_assist`；
   放行 `task_split` 后依赖死锁优先拆题）。`stale_lock` 属「占用者不明」，永远只上报。
2. **决策可诊断**：`llm_command` 必须配置（stdin=prompt，stdout=JSON 决策）。没配时
   Router 只有一种结局——升级人工。用 `maw system status` 看 `llm_ready`。
3. **动作有边界**：`ops_run` 只执行 `ops_allowlist` 前缀匹配的命令，且结果里必须留下
   `ops_command`／`ops_allowlist_rule`／`ops_exit_code`，命令回显可见。

```bash
# 配置（也可用 PUT /api/v1/projects/{id}/system-agent）
llm_command = ["/Users/you/.local/bin/claude", "-p", "--output-format", "text"]
auto_kinds = ["unblock_assist", "task_split", "ops_run"]
ops_allowlist = ["maw next", "maw merge --yes", "maw prune", "maw task unblock"]
```

看板「问题审查」里的两类条目说明 Router 有没有接管：

| kind | 含义 |
| --- | --- |
| `router` | 已有作业：`done` 会写决策动作，`escalated` 写升级原因，`queued/running` 写第几次尝试 |
| `router-idle` | 这条 blocked 没有任何相关作业：`system_agent` 没开，或该处置类型没在 `auto_kinds` 放行 |
