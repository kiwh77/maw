# lead 角色与本地 CLI 节点／模型管理：功能规划与可执行任务清单

版本：v1.0，2026-11-04。主分支 `main`。迭代 `IT-LEAD-001`。

**依据**：`docs/maw_gap_可执行任务清单.md`、`docs/vnext-gap-analysis.zh-CN.md`、
`docs/maw_board_product_gaps_可执行任务清单.md`、当前 `main` 上的生产代码，以及
2026-11-04 14:48 主工作区未提交现场（下称「节点一期现场」）。

---

## 0. 结论：两个需求都不是从零开始，缺的是「身份层」和「目录层」

| 需求 | 已有零件（实测） | 真正缺的 |
| --- | --- | --- |
| lead 拆任务 | `maw system trigger --kind task_split`；`Effects.WriteSplitProposal` / `ApplyPlan`（`internal/systemagent/orchestrator.go:35,43`）；`appSystemEffects` 已有实现（`internal/app/system_cmd.go:617,621`） | 没有 `lead` 身份；拆题必须配 `llm_command` 且 `plan approval=manual` 才能落地；没有「一段需求 → 追加卡 → 校验」的直接入口 |
| lead 处理系统级阻塞 | `Watcher.ingestBlocked`（`internal/systemagent/watcher.go:93`）；`ClassifyBlocked`（`classifier.go:12`）；四种 kind（`classifier.go:68-71`） | 只认 task `blocked` 事件；`auto_kinds=["unblock_assist"]`、`enabled=false`；门禁环境失败／Agent 全 offline／`merging` 卡死／锁残留等**系统级**信号无人识别 |
| lead 调度 | `Dispatcher.DispatchReady`（`internal/systemagent/dispatch.go:119`）；`runDispatchJob`（`orchestrator.go:310`）；`Coordinator.Step` | `auto_enabled=false`；只能等 `system watch` 或显式 trigger，没有 lead 主动发起的一次性调度入口 |
| 节点配置体验 | 节点一期现场：编辑器 `NodeEditorModal.vue`、删除 `Nodes.Remove`（`internal/app/hub_nodes.go:29`）、自动探测 `node_probe.go`、CLI 预设 | 现场未提交、未走任务卡、未验证合入；需要先收口为可回归的基线 |
| 模型管理 | `default_model` 写入 meta 并在受管启动时注入 `--model`（`internal/app/runtime.go:317`）；按 flavor 分 `-m` / `--model` | 模型是**手填自由文本**；项目已有「按发行包实测登记的模型枚举」（`internal/adapters/workbuddy/distribution.go:62,79`），没有把这份证据接到节点配置上 |

**判据**：`docs/vnext-gap-analysis.zh-CN.md` §0 的教训是「按层拆卡、按层验 → 缺总装」。
所以本波不接受「只加字段／只加函数、没有端到端入口」的卡；每张卡必须给出一个能在 CLI 或看板上跑通的动作。

---

## 1. 项目特点：四条硬约束

1. **能力必须挂证据，不许拍脑袋列清单。**
   `internal/app/runtime.go` 的能力分级（verified / experimental / unsupported）与
   `internal/adapters/workbuddy/distribution.go` 的 `ModelIDs` 都是按实测登记、且明确
   「同版本不代表同能力、不可跨包继承」。因此模型目录按 **flavor + 证据来源** 提供；
   无证据的节点允许自由输入，但 UI 必须标「未标定」，不得冒充可用模型。
2. **默认关闭、可审计、可回退。**
   `[system_agent] enabled=false`、`[managed] auto_enabled=false`、`approvals.plan=manual`
   都是刻意的。lead 能力只能「显式开启 + 留审计」，不能因为加了角色就默认自动改状态。
   系统级阻塞默认**只入队观察**，自动处置需 `auto_kinds` 显式放行。
3. **写盘范围与串行热点。**
   `cmd/maw/main.go`、`internal/i18n/catalog_{zh,en}.go` 是全 CLI 卡共用入口；
   `internal/board/assets/dist/board-web.js` 是所有前端卡共用产物（历史 E2E-101…107 因此串成一条链）。
   本波把它们各自**归给唯一一张卡**，其余卡只提供函数，避免并行覆盖。
4. **验收以真实库／真实命令为准。**
   单测绿不算完成；须能在 `~/.maw/maw.db`、`maw ... --json` 或受控测试库看到结果。
   门禁 `go test ./...`（2026-11-04 15:15 实测全绿）为最低线。

---

## 2. 目标一：lead 角色

### 2.1 角色模型：本机能力标记，不是新角色体系

`internal/identity/identity.go:36-46` 的 `Role()` 只返回 `worker` / `integrator` / `reviewer`，
配置在 `[identity] reviewers / integrator`。架构 §13 明确「不新增云端账户或角色组织体系」，
`ARCHITECTURE_VNEXT.zh-CN.md:440` 也把复用边界定在现有 CLI 入口。因此：

- **不改 `Role()` 的返回值、不改 agents 表结构**（`internal/db/db.go:57` 的 `role` 仍是那三类）。
  `lead` 实现为**本机能力标记**：`[identity] leads = []` + `IsLead(agent, cfg)` / `RequireLead(actor, cfg)`。
- lead 是「同一个 worker 多一项授权」，不参与领题、不占 worker 槽位、不改 Worker 命名格式。
- 权限门：只有 lead 能调用 `maw lead *` 与改 `auto_kinds`；其余角色返回明确错误码，不静默降级。
- 看板通过 `maw agents --json` 的 meta 能力标记可见，不新增云端身份。

**不做**：不引入账号体系／组织层级、不给 lead 绕过 `write_scope`、不改 DB schema。

### 2.1.1 装配层是共同根因（实测）

`internal/app/board_cmd.go:152-160` 未注入 `Tasks` / `Probes` 端口，导致两件事同时发生：

- `POST /api/v1/projects/{id}/tasks`（`internal/httpapi/resources/handler.go:169-219`
  的 `handleCreateTask`）代码在但恒 501；
- `POST /api/v1/nodes/{id}/probes`（`internal/httpapi/resources/handler.go:268-414`）代码在但恒 501。

也就是说「lead 在 maw 里拆任务」和「节点探测回显」缺的是**同一个装配动作**，不是两套功能。
`LD-004` 收口卡必须把这两个端口一并注入，并补 `catalog_{zh,en}.go:84-85` 漏掉的
`dispatch_ready` kind 文案。

**更关键的实测结论**：当前 `.multiagent/config.toml` 下这三件事**端到端全跑不通，不是「缺功能」而是「开关全关」**：

| 开关 | 现值 | 后果 |
| --- | --- | --- |
| `[system_agent] enabled` | `false` | 自愈循环根本没启动 |
| `auto_kinds` | `["unblock_assist"]` | `task_split` / `ops_run` / `dispatch_ready` 只能显式 trigger |
| `llm_command` | `[]` | `buildOrchestrator` 退化为 `escalate_human`，LLM 决策路径不可用（`system_cmd.go:241-244`） |
| `[approvals] plan` | `manual` | `PlanApprovalAuto` 恒 false（`system_cmd.go:631-641`），拆解结果只写 `handoffs/system-split-*.toml`，永不自动进蓝图 |
| `worker_node` | `""` | `RelaunchWorker` 报「未配置」，错误只记 result |
| `[managed] auto_enabled` | `false` | leader.Dispatch 不跑 |

因此本波**不追求把所有开关默认打开**（那会破坏「默认关闭、可审计」的架构约束），
而是补上「lead 显式发起 → 有校验 → 留审计 → 可人工确认」的完整可达路径。

### 2.2 拆解任务（`maw lead split`）

流程固定为「生成 → 校验 → 人工确认」，**不绕过** `approvals.plan=manual`：

```text
需求文本/文件 → 生成 [[task]] 草稿（追加，不覆盖）
  → maw plan check（deps / write_scope / 标题 / 依赖深度）
  → 默认落盘为待审提案 + 打印 diff；--apply 且确认后才写 blueprint.toml
  → plan apply 仍由人执行
```

- 生成规则按仓库既有拆卡约定：`id` 唯一、`deps` 字段名（不是 `depends`）、
  每条卡带 `goal` / `write_scope` / `read_scope` / `acceptance` / `verify` / `not_doing`。
- **必须追加到蓝图末尾并保留既有题全集**：`STATUS.md` 记过一次事故——单题临时 toml
  做增量 `plan apply` 把 30 道 idle 题标成 `removed_from_plan`。生成器禁止产出「单题计划文件」。
- 不接 LLM 也能用：内置模板化拆卡（按 Lane/文件归属分组），`--from` 读需求文档，
  `--dry-run` 只打印；接了 `llm_command` 才走 `task_split` 的 LLM 路径。

### 2.3 系统级阻塞（新增 signal，不新增自动动作）

`system_signals` 识别四类现有系统事实，命中即入 `system_jobs`（kind 复用，不新增 kind）：

| 信号 | 判据（真实库） | 默认动作 |
| --- | --- | --- |
| 门禁环境失败 | 最近 submit 的 gate 结果含环境失败词表（`gitops.IsEnvFailure`） | 入队 `unblock_assist`，标记 `needs_human` |
| Agent 全 offline | 工程内无 `last_seen` 在心跳窗口内的非 offline Agent | 入队 `dispatch_ready`，只观察 |
| merging 卡死 | 任务 `status=merging` 且超过阈值未推进 | 入队 `ops_run`，命令须在 `ops_allowlist` 内 |
| 锁／租约残留 | `maw prune` 能列出但受 unknown 占用保护 | 只上报，不自动 prune |

- 冷却与次数沿用 `cooldown_sec` / `max_attempts`；`needs_human` / `auth_required`
  继续走 `escalate_patterns`，不自动处置。
- 审计沿用 `internal/systemagent/audit.go` 的 sidecar，不另起一套日志。

### 2.4 主动调度（`maw lead dispatch`）：不是缺触发，是「规范已设计、生产未落地」

**生产层事实（只读复核结论）**：`dispatch_ready` 只有两个显式入队口——
`internal/app/system_cmd.go:111`（`maw system trigger --kind dispatch_ready`）与
`internal/httpapi/systemjobs/handler.go:115`（POST system-jobs）。
`Watcher.PollOnce` 只产 `unblock_assist`（`watcher.go:130-193`，:169 kind 恒为 `SystemKindUnblock`）。
`leader.drain` 只反复调 `Coordinator.Step`，而 `Step` 从 `queue.Pending()` 读**已存在**的 operation
（`engine/dispatch/queue.go:81-85`），**不选题、不造 job、不造 operation**；pending dispatch operation
实际由 `claimManaged`（`engine/claim/claim.go:210-245`）或 `POST /api/v1/projects/{id}/dispatches`
（`httpapi/commands/handler.go:138-170`）产生。

完整链条：显式 job → `Orchestrator.runDispatchJob` → `Dispatcher.DispatchReady` →
`matching.Match` → `claim.New().Claim` → `claimManaged` 建 Run + Operation → `Advance.Advance` → launch_spec。
这段**代码全在**，缺的是「谁来入第一队」。全队统一措辞：**触发源显式入队，消费与选题全自动**。
serve leader 的 System 循环确实挂了 `Dispatcher` + `systemDispatchProjection` + `matching.Match`
（`system_cmd.go:285-287,700,712-715`），但那由已有 `dispatch_ready` job 触发，不是 Dispatch 循环自动选题。
交叉验证：`BuildCoordinator`（`runtime.go:481-491`）三处生产装配全传 `dispatch.NewDBQueue`，
Queue 只有 DBQueue 一个生产实现、Preparer 未接线；`INSERT INTO operations` 唯一路径在 Claim 事务内
（`db/runtime.go:302`、`claim.go:275`）；`matching.Match` 生产调用点唯一且为纯函数
（`systemagent/dispatch.go:143`）。所以：

- `maw lead dispatch` 是**新建 CLI**（`cmd/maw/main.go` 无脚手架），一次动作 =
  lead 身份校验 → 显式入队 `dispatch_ready` → `SystemRun` 消费 → 输出 `--json` 含匹配理由
  与 rejected 原因（复用 `RenderQueueReasons`）。
- `system_signals` / auto 选题循环是**规范已设计、生产未落地**的设计缺口：
  `docs/maw_gap_可执行任务清单.md:226` 的 SA-002 目标写了「处理 dispatch_ready（及 auto 循环）」，
  但全仓 grep 无 `system_signals`、无自动入队实现。**LD-003 的验收点必须写明「自动入队」是新增能力，
  不能假设已有**。
- **不**把 `auto_enabled` 默认改 true；常驻循环仍归 `maw system watch`。
- 系统级阻塞处置若要加 retry／改派／放依赖，直接复用既有判定口径：
  `engine/dispatch/intent.go:14-64` 单向 Progress、`coordinator.go:476-509` 的
  `ErrLaunchUncertain` 不重发、`reconcile.go:136-140` 心跳丢失默认 unknown 保留占用、
  `runner/recovery/reconcile.go:55-59` 仅确定结束／从未启动才放资源。

---

## 3. 目标二：设置-节点与模型管理

### 3.1 先收口节点一期现场

节点一期现场（27 改 + 3 新增，约 1605 行）已实现：节点编辑弹窗（Claude/Codex/自定义预设、
入口路径、槽数、自动探测、心跳间隔、默认模型）、删除、自动探测循环、`default_model` 注入。
它原先**未提交、未走任务卡、未验证合入**，必须先成为干净基线，否则后续卡读不到。

**已收口（2026-11-04 15:2x，commit `d54e063`）**：提交前完整备份至 `/tmp/maw-wip-backup-20261104`
（patch + 3 个未跟踪文件 + 原 HEAD）；验证 `go test ./...` 全绿、
`node --test page/settings/projects` 80/80、`internal/app|httpapi/resources|nodes|board` 单测通过。
提交只做「把既有成果落主干」，未覆盖、未代删除任何现场内容，可用
`git reset --soft 1a44fea` 回到未提交状态。

### 3.2 实测缺口清单（2026-11-04 只读调研，均为 `main` + 现场上的事实）

| 缺口 | 证据 | 影响 |
| --- | --- | --- |
| 编辑无 PATCH，提交恒走 POST register | `NodeEditorModal.vue:75`；`sources.ts` 无 `updateNode` | 编辑会新建／覆盖，无 revision/If-Match |
| 探测端点恒 501 | `httpapi/resources/handler.go:268-414` 有 `POST /nodes/{id}/probes`，但 `board_cmd.go:152-156` 未注入 `Probes` | 现场 `node_probe.go` 的自动探测只有后台循环，前端点「探测」拿不到回显 |
| `nodes.Service` 未接线、两套模型并存 | `internal/nodes/service.go:54-333` 仅测试用；HTTP 直写 `db`，`NodeConfig.Validate` 未被 hub 调用 | `mode/platform/timeout/accept_new` 被丢弃，登记校验形同虚设 |
| 无启停／接收策略 UI | `service.go:197 SetAdmission` 无路由；`nodes-view.mjs:323-338` 只有 edit/remove | 节点只能登记/删除，不能暂停接单 |
| CLI 无模型/探测/编辑参数 | `cmd/maw/main.go:444-482` 只有 id/label/project/slots/cmd/system | `--cmd` 手写 JSON，无预设向导 |
| `cliFlavorFromID` 只认 claude/codex | `hub_nodes.go:140-150`；而 `identity/name.go:21-31` 能识别 7 种 CLI | Cursor/Qoder/Trae/ZCode/CodeBuddy 落到默认 flavor |
| 模型枚举未接后端 | `docs/evidence/workbuddy/capability-matrix.json:39-63,167-177`（CN 9 个 / intl 4 个）；`distribution.go:62,79` 有 `ModelIDs` | 前端只有 `sonnet` / `gpt-5` 两个硬编码默认值 |
| API 双通道不一致 | `board-api.mjs:33-131` 无 node 方法，`sources.ts` 直接 raw fetch | 后续加端点要同时维护两处 |
| i18n 三处并存 | `board-i18n.mjs`、`nodes-view.mjs` 局部字典、`NodeEditorModal.vue` 内联双语 | 多 Agent 并行必冲突 |

**结论**：`NM-*` 的价值不是「再加一个输入框」，而是把**已实测的模型枚举**接成目录、
把**已挂载但未注入的探测端点**接通、把**已存在但未接线的 Service/Validate** 补上校验。
`dist/board-web.js`（1.5MB 单行、`go:embed`）是最强冲突点，只允许单一 owner 最后重建一次。

### 3.3 模型管理：从「自由文本」到「有证据的目录」

分层设计，每一层都能单独验收：

1. **目录层**：`internal/nodes/catalog.go` 汇总已登记的模型枚举。
   来源只取 `internal/adapters/*/distribution.go` 的实测 `ModelIDs`（含 CN/INTL 差异），
   按 `cli_flavor` 分组，返回 `{id, source, verified}`；无来源的返回空列表而不是猜。
2. **API / CLI 层**：`GET /api/v1/nodes/models?flavor=` 与 `maw node models --flavor claude`。
   与现有 `node register / rm` 同族；只读，不写状态。
3. **配置层**：节点 `default_model` 增加「目录内 / 未标定」标记，保存时不再无脑接受任意字符串；
   未标定值仍允许（自定义 CLI），但列表与详情显示「未标定」。
4. **UI 层**：编辑器模型字段从纯 input 改为「可选目录下拉 + 自由输入」，
   显示来源与是否实测；保持中英双语与窄屏。
5. **执行层一致性**：`withDefaultModelArgs`（`internal/app/runtime.go:317`）按 flavor 选
   `-m` / `--model` 的逻辑保持不变，新增单测覆盖「目录内模型 → 注入旗标正确」。

**不做**：不实现模型下载／鉴权／计费；不把某发行包的模型枚举硬编码成通用常量；
不因为模型不在目录里就拒绝启动（`unsupported` 语义是「无证据」，不是「不可用」）。

---

## 4. 任务卡与 write_scope（IT-LEAD-001，同迭代）

| 卡 | Lane | write_scope（独占） | deps | 验收要点 |
| --- | --- | --- | --- | --- |
| `NODE-001` | NODE-BASE | 节点一期现场全部 30 个文件 | — | 现场成果落主干；`go test ./...` + `node --test` + build 全绿 |
| `LD-001` | LEAD-ID | `internal/identity/**`、`internal/config/config.go`、`internal/app/lead_auth.go`(新)、`lead_auth_test.go`(新) | — | `lead` 角色可配置；非 lead 调 lead 命令被拒；角色不影响 worker 领题 |
| `LD-002` | LEAD-SPLIT | `internal/app/lead_split.go`(新)、`blueprint_append.go`(新)、`lead_split_test.go`(新) | `NODE-001` | 追加式生成、`plan check` 通过、绝不产出单题计划文件、既有题不丢 |
| `LD-003` | LEAD-SIGNAL | `internal/systemagent/signals.go`(新)、`signals_test.go`(新)、`watcher.go`、`classifier.go` | `LD-001` | 四类信号各有一条测试；默认只入队；`needs_human` 升级路径保留 |
| `NM-001` | NODE-MODEL | `internal/nodes/catalog.go`(新)、`catalog_test.go`(新)、`internal/app/node_models.go`(新)、`node_models_test.go`(新) | `NODE-001` | 目录只来自已登记枚举；无来源返回空；CLI `maw node models` 可跑 |
| `NM-002` | NODE-UI | `internal/board/web/src/components/NodeEditorModal.vue`、`internal/httpapi/resources/**`、`internal/board/assets/dist/board-web.js`、对应测试 | `NM-001` | 中英下拉 + 未标定标记；API 只读；build 后 dist 含新逻辑 |
| `LD-004` | CLI-WIRE | `cmd/maw/main.go`、`internal/i18n/catalog_{zh,en}.go`、`internal/app/lead_cmd.go`(新)、`docs/operations/vnext/**` | `LD-001`,`LD-002`,`LD-003`,`NM-001` | 统一注册 `maw lead split/dispatch/status`、`maw node models`；中英帮助一致；运维文档可复跑 |

**并行入口**：`NODE-001` 必须先落地（现场基线）。之后 `LD-001` ‖ `LD-002` ‖ `LD-003` ‖ `NM-001`。
`NM-002` ← `NM-001`；`LD-004` 是收口卡，最后串行装配（独占 main.go / i18n / dist）。

**冲突约束**：
- `cmd/maw/main.go`、`internal/i18n/**` 只归 `LD-004`；其余卡不得注册命令。
- `internal/board/assets/dist/board-web.js` 只归 `NODE-001` 与 `NM-002`，且两者串行。
- `internal/app/runtime.go`、`hub_nodes.go`、`system_cmd.go` 已被节点一期现场占用，
  `LD-*` 不碰；需要接线时通过新文件 + `LD-004` 装配。

---

## 5. 复跑与验收

```bash
# 门禁
CGO_ENABLED=0 go test ./...
# 前端
node --test internal/board/page.test.cjs
npm --prefix internal/board/web run build
# 角色与拆卡
maw lead status --json
maw lead split --from docs/xxx.md --dry-run --json
maw plan check .multiagent/blueprint.toml --json
# 系统信号与调度
maw system status --json
maw lead dispatch --json
# 模型目录
maw node models --flavor claude --json
```

**总验收**：`maw task list --json` 里本迭代七张卡全部 `done`；
`maw plan check` 无 error；`go test ./...` 全绿；看板设置-节点页能编辑并看到模型来源标记。

---

## 6. 现场处置约定（2026-11-04 15:12 实测）

主工作区节点一期现场的 mtime 为 14:48:46（dist 14:53:34），至 15:14 未再变化，
对应 `cursor-goalagents-1` 的 `last_seen=14:48:46`，状态 `idle`、无在跑任务。
处置顺序：**完整备份 → 收口卡在 worktree 内承接 → 主干落地 → 不覆盖、不代删除**。
备份位置与 HEAD 记在当日工作日志，随时可回溯。
