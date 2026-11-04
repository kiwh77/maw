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
