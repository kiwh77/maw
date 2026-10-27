# maw 运行日志分析（2026-10-21 ~ 10-26）

数据源：`~/.maw/maw.db`（无独立日志文件）。采集时刻 2026-10-26 17:34（Asia/Shanghai）。
库是活的，采集期间事件数从 4662 涨到 4701，属正常写入。

**问题编号已归档到 [maw 已知问题清单](maw-known-issues.md)（K-01 ~ K-13）。** 本文是分析与证据，清单是可被任务卡和交接记录引用的版本。

## 0. 观测口径

| 维度 | 事实 |
|---|---|
| 事件总数 | 4701 条，跨度 10-21T02:18Z ~ 10-26T09:34Z（10-25 全天断档） |
| 项目 | 7 个注册，其中 3 个是 `/private/tmp/*`（目录已被删） |
| 任务实例 | 355 个：325 活跃 + 30 已退役 |
| 任务状态 | done 288 / backlog 27 / in_progress 5 / ready 5 |
| 独立日志文件 | 无。events / run_events / messages 三张表即全部日志 |
| maw 版本 | 0.4.0 |

`messages`、`notification_deliveries`、`usage_records`、`shared_files`、`source_sessions` 全是 0 行 —— 消息通道、通知、用量统计从未启用过。

## 1. 门禁把环境性失败恒判为代码失败（P0，正在造成损失）

`.multiagent/config.toml` 的 gate 是 `go test ./...`，`maw submit` 与 `maw merge` 都会跑。

- 53 次 `gate_failed`，其中 `env_failure` 字段：**15 次为 false，38 次为空，0 次为 true**。
- 但失败内容里至少 10 次明确是 `fork/exec /bin/ps: operation not permitted`：
  - `internal/app/managed_cmd_test.go:106`
  - `tests/managed/loop_test.go:238`
  - `tests/resilience/gates_test.go:183`
- 二次伤害同样被算成代码错：`internal/runner/process/process_test.go:162` 报「子进程 cwd = ""」，根因也是读不到 `ps`。

后果：WorkBuddy 会话内（沙箱禁 `ps`）任何 agent 都过不了门禁，`attempts` 被白烧，`gitops.IsEnvFailure` 词表里没有这条错误串。最后一次 goal-agents 的 `gate_failed` 在 10-24T00:40，之后该项目再无事件。

## 2. 资源租约泄漏：死 agent 占着槽不放（P0，当前正在发生）

`resource_leases` 38 条，2 条从未释放：

```
ls_0a15ugbgvbpz  node_slot  node:manual-compat#1  run_3bno5tv83it4  qoder-companiosclassroomwebadmin-1  09:26:28Z  released_at=NULL
ls_mok9sj1po9r0  workspace  ws:.../.worktrees/qoder-companiosclassroomwebadmin-1  run_3bno5tv83it4  同上
```

对应 `runs` 表 `run_3bno5tv83it4` 卡在 `phase=reserved`、`process_stop_confirmed=0`，创建于 09:26:28Z 至今（约 8 小时）无推进。
该 agent 的 `last_seen` 是 09:24:48Z —— 比租约获取还早，即拿完租约就再没心跳。`agents` 表里它仍是 `working`。

`node:manual-compat#1` 是唯一槽位化资源，泄漏会挡住后续任务抢槽。

## 3. `plan apply` 是破坏性全量语义（P1，已发生过一次事故）

事件表硬证据：

```
2026-10-23T07:10:53Z  orchestrator  task_removed_from_plan  43
2026-10-23T08:02:42Z  orchestrator  task_removed_from_plan  30
2026-10-23T08:02:42Z  orchestrator  task_retired            30
```

`task_instances` 里 30 条 retired 的 `retired_reason` 全是 `removed_from_plan`。
`.multiagent/STATUS.md` 记录：对只含 RF-REGFIX 的单题 `/tmp/rf-regfix.toml` 做增量 apply，把蓝图外 30 道 idle VN-\* 全部退役；恢复需要从库里冻结的 `task_versions` 重建蓝图，并重装 maw 才能绕过历史编号校验。

语义本身没写错（「计划文件 = 期望保留的空闲题全集」），但**误用的代价是批量退役在跑/待跑的题**，且没有 dry-run 阻断。

## 4. 并发 merge 产生假冲突（P1，重复出现）

`merge_skipped` 7 次，5 次报同一个错误：

```
squash merge conflict for agent/xxx: merge: agent/xxx - not something we can merge  (reason=already_done)
```

根因是多个 agent 同时 `maw merge --yes`：一次调用会把所有 `merging` 状态的题一起合入并删分支，后到者看到的是一批已删分支。
`merging → conflict` 共 16 次；只有 1 次被 orchestrator 手工补 `merge_reconciled` 收尾（RF-001，`already_merged_on_main`）。
16 道冲突题最终 14 道落 done，2 道走了重建分支 + 补提交的路子。

## 5. 心跳缺失：28% 的题领了不打点（P1，抢题根源）

以「题」为单位统计 `claimed` 之后的 `progress` 次数：

| 打点情况 | 题数 |
|---|---|
| 完全没打 progress | 94（28%） |
| 只打 1 次 | 164（49%） |
| 2-3 次 | 34 |
| 4 次以上 | 1 |

`heartbeat_ttl_sec = 900`（15 分钟），而 `locked` 的 TTL 是 1800。`agent_expired` 17 次，全部 `ttl_sec=900`，`last_seen` 均落后约 15-16 分钟。
即：领题 → 埋头写 → 16 分钟后被判离线 → 题被别的 agent 领走。10-26 当天领取 21 次、合并 17 次，`progress` 只有 2 次，说明新版流程已在用租约替代心跳，但租约不回收（见 §2）。

## 6. 数据卫生：僵尸记录与重复写入（P2）

**僵尸 agent**：`agents` 表 68 条，`working` 只有 5 条。goal-agents 35 条全部陈旧（15 条还挂 `idle`，最后活动 10-24T05:58）；NovelWriter 12 条（最后活动 10-21T15:46）。

**失效项目**：`maw-i18n-repo`、`maw-i18n-zh`、`mawinit` 三个项目指向 `/private/tmp/*`，目录已不存在，记录还在库里。

**脏 agent id**：`716c30878ba1b95f|{|worker` —— 注册于 10-23T09:56:33Z，5 秒后注销。畸形 `MAW_AGENT` 未做校验。

**重复创建**：`task_created` 事件 441 条 vs 去重后 325 个任务，116 次重复（NW-380 / NW-390 各创建 4 次，VN-\* 一批各 3 次）。`task_plan_updated` 433 次，NW 系列每道题被更新 5 次。蓝图反复重压同一批题，apply 不是幂等的观测视图。

**事件不可追溯**：`status_changed` 1451 条里有 296 条 `from` 为空（`|in_progress` 133、`|in_review` 133、`|ready` 24、`|blocked` 6），无法回溯来源状态。另有一条 `in_progress(w2) → in_progress(w1)`，状态里混入了 worker 后缀。

**计数口径不一致**：9 个任务 `attempts=2` 但只 `claimed` 过 1 次；4 个任务 `attempts=1` 但 `claimed` 过 2 次。

## 7. 审查全自动，实质无审查（P2）

`review_auto_approved` 325 次；`delivery_reviews` 17 条全部 `verdict=approved, reviewer=system`。
交付评审、验证、审查三张表各 17 条，一一对应，没有任何一条来自人或其他 reviewer。
config 里 `[approvals] review = "auto"`、`merge = "manual"`，配合并发 merge 的假冲突，等于质量闸门只有 `go test ./...` 这一道 —— 而它当前必红（§1）。

## 8. 当前队列现状（非缺陷，供决策）

- goal-agents：92 道全 done，**0 道可领**，蓝图已跑空。
- niuma-buddy：ready 5 / in_progress 5 / backlog 27。backlog 里 T-783、T-786 的 `deps` 为空却没被推到 ready（其余 backlog 都在等上游），队列有停滞点。
- companios-classroom-web-admin：1 道 in_progress，被 §2 的死 agent 占着。
- 调度参数：所有项目 `max_parallel = 0`（代码注释明确 0 = 不设建议槽位，领取不卡槽），`[managed] auto_enabled=false, limit=1`。

## 附：分析过程中遇到的干扰（非 maw 缺陷）

用 `sqlite3` CLI 直连时出现 1 次 `database is locked`。maw 自身写池设了 `busy_timeout=30000`、`_txlock=immediate`、`max_open_conns=1`（`internal/db/store.go:263-305`），读池 10000 + `query_only`；CLI 的 `busy_timeout` 默认为 0，撞上写锁即失败。这是排查工具的问题，不是 maw 的问题。
