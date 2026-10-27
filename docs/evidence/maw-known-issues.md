# maw 已知问题清单

建立于 2026-10-26，依据 [maw 运行日志分析](maw-log-analysis-2026-10-26.md)（数据源 `~/.maw/maw.db`，4701 条事件）。
编号供任务卡、提交说明与交接记录引用；**"证据"列是可复核的查询口径，不是主观判断**。

严重度：**P0** = 正在造成损失；**P1** = 已发生且会复现；**P2** = 数据卫生／观测盲区。

---

## K-01 门禁把环境性失败恒判为代码失败

- **严重度**：P0（阻断所有沙箱内 agent 的提交）
- **现象**：`gate_failed` 53 次，`env_failure` 字段 15 次为 `false`、38 次为空、**0 次为 `true`**。其中至少 10 次错误文本明确是 `fork/exec /bin/ps: operation not permitted`。
- **受影响用例**：`internal/app/managed_cmd_test.go:106`、`tests/managed/loop_test.go:238`、`tests/resilience/gates_test.go:183`；`internal/runner/process/process_test.go:162`（报「子进程 cwd = ""」，根因同为读不到 `ps`）。
- **根因**：`gitops.IsEnvFailure` 词表不含 `operation not permitted`，环境问题被计入 `attempts`。
- **证据**：`select json_extract(data_json,'$.env_failure'), count(*) from events where type='gate_failed' group by 1;`
- **修复入口**：扩 `IsEnvFailure` 词表；或给上述四处测试补 platform/沙箱跳过守卫（参考 `internal/runner/process/process_test.go` 已有的 `requireDarwinProcessTools`）。

## K-02 资源租约泄漏：已失效的 agent 占着槽位不释放

- **严重度**：P0（当前正在发生，已持续 8 小时）
- **现象**：`resource_leases` 中 `ls_0a15ugbgvbpz`（`node_slot node:manual-compat#1`）与 `ls_mok9sj1po9r0`（workspace）`released_at IS NULL`，归属 `run_3bno5tv83it4`，持有者 `qoder-companiosclassroomwebadmin-1`。
- **伴随状态**：`runs` 中该 run 停在 `phase=reserved`、`process_stop_confirmed=0`；持有者 `last_seen=09:24:48Z` 早于租约获取时间 `09:26:28Z`；`agents` 表仍标 `working`。
- **后果**：`node:manual-compat#1` 是槽位化唯一资源，泄漏会挡住后续任务抢槽。
- **证据**：`select lease_id,kind,resource_key,run_id,holder,acquired_at from resource_leases where released_at is null;`
- **修复入口**：租约过期回收（按 `acquired_at + ttl` 或 agent 心跳判活后强制释放）；`maw prune` 增加租约清理。

## K-03 `plan apply` 是破坏性全量语义，无 dry-run 阻断

- **严重度**：P1（已发生一次事故，需重建蓝图 + 重装 maw 才恢复）
- **现象**：2026-10-23T08:02:42Z 对只含单题的 `/tmp/rf-regfix.toml` 执行增量 apply，同一秒产生 `task_removed_from_plan` 30 + `task_retired` 30；`task_instances` 中 30 条 retired 的 `retired_reason` 全为 `removed_from_plan`。
- **另一次**：07:10:53Z 有 43 条 `task_removed_from_plan`。
- **语义现状**：计划文件 = 期望保留的空闲题**全集**，未列出的题会被退役。文档写了，但执行时没有二次确认或差异预览。
- **证据**：`select ts,actor,type,count(*) from events where type in ('task_removed_from_plan','task_retired') group by substr(ts,1,13),type;`
- **修复入口**：apply 前打印「将退役 N 道」并要求显式确认；提供 `--diff-only`；单题增量走独立命令而非全量 apply。

## K-04 并发 merge 制造假冲突

- **严重度**：P1（5 次假冲突 + 16 次 merging→conflict）
- **现象**：`merge_skipped` 7 次中 5 次报 `squash merge conflict for agent/<holder>/<TASK>: ... not something we can merge`，`reason=already_done`。
- **根因**：一次 `maw merge --yes` 会把**所有** `status=merging` 的题一起合入并删分支；后到者看到一批已删分支，被判冲突。
- **收尾代价**：仅 1 次由 orchestrator 手工补 `merge_reconciled`（RF-001）。按既往经验，DB 状态可能永久停在 `merging`，需重建分支 + 补真实提交才能落 done。
- **证据**：`select ts,task_id,data_json from events where type='merge_skipped';`
- **修复入口**：merge 只处理调用者持有的题；分支不存在且主干已含该题内容时直接判 `already_done` 并落 done，不计 conflict。

## K-05 心跳缺席导致题被抢

- **严重度**：P1（28% 的题领了完全不打点）
- **现象**：以题为单位统计 `claimed` 之后的 `progress`：94 道题 0 次、164 道 1 次、34 道 2–3 次、1 道 4 次以上。
- **判活参数**：`heartbeat_ttl_sec = 900`（15 分钟），`[locks] ttl_sec = 1800`。17 次 `agent_expired` 的 `ttl_sec` 全为 900，`last_seen` 均落后 15–16 分钟。
- **后果**：领题 → 埋头写 → 16 分钟被判离线 → 题被他人领走，本地改动作废。
- **证据**：`select case when p.n is null then '0' ... end from (claims left join progress);`
- **修复入口**：心跳与"正在执行"的事实解耦（用租约续期代替人工 `maw progress`）；或在 worktree 内有未提交改动时不判过期。

## K-06 审查全自动，质量闸门只剩 `go test ./...` 一道

- **严重度**：P1
- **现象**：`review_auto_approved` 325 次；`delivery_reviews` 17 条全部 `verdict=approved, reviewer=system`；`delivery_verifications`、`deliveries` 各 17 条一一对应，无一条来自人或独立 reviewer。
- **配置**：`[approvals] review = "auto"`、`merge = "manual"`。
- **后果**：叠加 K-01（门禁必红），实际质量闸门处于失效状态。
- **修复入口**：为关键车道配置 independent review；或在 review 中至少校验 write_scope 与验收条目覆盖。

## K-07 僵尸 agent 与失效项目记录

- **严重度**：P2（污染看板、误导调度）
- **现象**：
  - `agents` 共 68 条，`working` 仅 5 条。goal-agents 35 条全部陈旧（15 条仍挂 `idle`，最后活动 10-24T05:58）；NovelWriter 12 条（最后活动 10-21T15:46）。
  - `maw-i18n-repo`、`maw-i18n-zh`、`mawinit` 三个项目指向 `/private/tmp/*`，目录已不存在。
  - 一条 agent id 就是 `{`（`716c30878ba1b95f|{`），注册于 10-23T09:56:33Z、5 秒后注销。
- **修复入口**：注册时校验 agent 名合法字符；`maw prune` 清理离线超 N 天记录与路径不存在的项目。

## K-08 重复创建与蓝图重压非幂等

- **严重度**：P2
- **现象**：`task_created` 事件 441 条 vs 去重后 325 个任务，**116 次重复**（NW-380 / NW-390 各 4 次，一批 VN-\* 各 3 次）；`task_plan_updated` 433 次，NW 系列每道题被更新 5 次。
- **后果**：事件计数不能直接当任务量用；看板统计与复盘口径失真。
- **证据**：`select count(*), count(distinct project_id||'/'||task_id) from events where type='task_created';`
- **修复入口**：同一 task_id 在内容未变时不写 `task_created` / `task_plan_updated`。

## K-09 事件可追溯性缺失

- **严重度**：P2
- **现象**：
  - `status_changed` 1451 条里 296 条 `from` 为空（`|in_progress` 133、`|in_review` 133、`|ready` 24、`|blocked` 6），无法回溯来源。
  - 存在 `in_progress(w2) → in_progress(w1)`，worker 名混进了状态值。
  - `attempts` 与 `claimed` 次数不一致：9 道题 `attempts=2` 只领过 1 次，4 道题 `attempts=1` 领过 2 次。
- **修复入口**：状态机写事件时强制带 `from`；状态值域收紧为正枚举；`attempts` 与事件同源。

## K-10 消息、通知、用量三条通道从未启用

- **严重度**：P2
- **现象**：`messages`、`notification_deliveries`、`usage_records`、`shared_files`、`source_sessions`、`source_turns`、`run_source_links` 全部 0 行。
- **后果**：agent 之间交接只能塞进 `blocked_reason` 字符串；没有跨会话上下文、没有用量统计、没有通知投递记录。

## K-11 过宽的 write_scope 让整个 integration 域退化为单线程

- **严重度**：P1（队列看起来空了，实际有 5 道能跑）
- **现象**：niuma-buddy 的 `ready` 队列为 0，但 backlog 里有 5 道题的依赖已全部 `done`（`T-751`、`T-781`、`T-782`、`T-784`、`T-785`）。它们不是依赖未满足，是被写盘范围串行化挡住了。
- **实测判定**（`internal/scope.ScopesOverlap`，用库里真实字符串跑）：

  | 组合 | 是否重叠 |
  | --- | --- |
  | `T-781 integration/**` vs `T-786 integration/connection/**` | 是 |
  | `T-781 integration/**` vs `T-782 integration/mapping/**` | 是 |
  | `T-781 integration/**` vs `T-784 integration/ci/**` | 是 |
  | `T-781 integration/**` vs `T-785 integration/issue/**` | 是 |
  | `T-751 integration/**/interfaces/**…或…changerequest/**` vs `T-786` | 否（脏 scope 串） |

- **机制**：`schedule.CanActivateShared` 在 scope 重叠时要求「本任务排序更靠前」才能激活，否则返回 false。`T-786`（priority 40）排在 `T-781`（50）前面，`T-781` 又排在 `T-782/784/785`（60）前面 → 整条 integration 域串成单线程。`T-786` 的执行者一死，后面 5 道全部停摆。
- **附带脏数据**：`T-751` 的 write_scope 是 `backend/internal/integration/**/interfaces/**` 或 `backend/internal/**/changerequest/**`——把中文说明与反引号写进了 scope 串，不是合法路径模式。
- **修复入口**：拆分 `integration/**` 这类覆盖全域的 scope；或在 scope 声明时对「覆盖整个子域通配」给出警告。

## K-12 `maw next --dry-run` 会真实写库

- **严重度**：P1（预览动作改变了状态）
- **现象**：2026-10-26T14:58:45Z 执行 `maw next -C <niuma-buddy> --dry-run`，库中随即新增 `agent_expired` 与 `status_changed T-786`（`in_progress → ready`，`owner` 清空，`revision` 3→4）。
- **根因**：`internal/app/worker_cmd.go:30` 的 `Next` 先调用 `prepareWorker`（内含 `db.WithTx` → `expireStaleAgents` + `promoteReady`），到第 68 行才判断 `dryRun`。维护性写入已经落库。
- **后果**：以为安全的预览会回收 agent、改任务状态；在排查现场时尤其危险（会抹掉「当前卡在哪」的证据）。
- **修复入口**：`prepareWorker` 的维护性写入也纳入 dry-run 判定，或在 dry-run 下用只读事务计算预览结果。

## K-13 过期回收没有后台巡检，全靠「有人来领题」触发

- **严重度**：P1（死 agent 能永久占着任务）
- **现象**：`expireStaleAgents` 唯一的调用点是 `prepareWorker`（`worker_cmd.go:180`），即 `maw register` / `maw next` 路径。没有任何后台循环或独立命令扫描过期。
- **实证**：`zcode-niumabuddy-2` 自 2026-10-26T10:01:53Z 起再无事件，`heartbeat_ttl_sec=900`（15 分钟），但 `T-786` 一直停在 `in_progress`、agent 一直标 `working`，**持续 4 小时 57 分钟**，直到上一条命令改写它。
- **后果**：队列死锁的解除依赖「恰好有人来领题」。若 `ready` 已为 0，首个来领题的人会得到「无题可领」，但同一次调用已经顺手做了回收——第二次才领得到。表现为「刚才明明没题，再跑一次就有了」。
- **修复入口**：`maw agents` / `maw doctor` / 看板轮询都接一次回收；或加独立 `maw reconcile`。

---

## 附：非缺陷说明

用 `sqlite3` CLI 直连时偶发 `database is locked`。maw 自身写池为 `busy_timeout=30000` + `_txlock=immediate` + `max_open_conns=1`，读池 `10000` + `query_only`（见 `internal/db/store.go:263-305`）；CLI 默认 `busy_timeout=0`，撞上写锁即失败。属排查工具问题，不计入缺陷。
