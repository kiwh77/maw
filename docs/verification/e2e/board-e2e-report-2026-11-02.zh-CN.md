# maw 看板端到端测试报告（2026-11-02）

> 计划：[board-e2e-plan.zh-CN.md](board-e2e-plan.zh-CN.md)｜套件：`tests/e2e/api_e2e.py`、`tests/browser/specs/board-e2e.browser.spec.ts`
> 被测：`maw serve`（`http://127.0.0.1:7420` 的真实实例 → 测试跑在隔离副本 `127.0.0.1:7421`）
> 证据：`api-report-2026-11-02.json`、`ui-run-2026-11-02.log`、`screenshots/`

## 结论

**能跑，但没跑全；第二轮补测后，问题比第一轮更多。** 后端契约 64 条里 63 条通过（唯一失败是空库返回 `null` 而非 `[]`）；前端 40 条里 28 条通过、12 条失败。失败项全部指向真实缺陷，不是测试写错。真实状态库与 7420 实例在全程未被改动（真实库里 `e2e-*` 痕迹为 0）。

第二轮的重点结论：**执行策略页是「只读摆设 + 死表单」**——计算上限显示「—」、输入框不回填、点保存会把用户 GET 跳走并丢掉当前工程；**回顾页的时间轴/热图永远没有数据**，因为投影从未构建（全库 `projection_state` 0 行），接口如实报 unknown，但工具栏按钮同样没接线，窗口标签还把 24 小时显示成 `16:18 – 16:18`。

- 后端：鉴权（会话 + CSRF + 同源 + 令牌 actor 绑定）、游标分页（420 条无重无漏）、时间窗校验、SSE 首帧与变更推送，全部符合实现声明。
- 前端：13 个视图、主题/语言持久化、拓扑渲染、窄屏、离线降级、抽屉开关、搜索过滤，均可用。
- 主要缺口集中在**看板本身**：状态覆盖不全、筛选不可分享、焦点不接管、观测视图把「没选工程」说成「没数据」。

## 测试环境与隔离（为什么可以确认「没动活动数据」）

| 项 | 真实 | 测试 |
| --- | --- | --- |
| 状态库 | `~/.maw/maw.db` | `/tmp/maw-e2e/global/maw.db`（`sqlite3 .backup` 快照：420 任务 / 94 Agent / 9 工程） |
| 端口 | 7420 | 7421（`MAW_GLOBAL` 指向临时目录） |
| 启动目录 | 工程根 | `/tmp/maw-e2e`（非 git 仓库，不写工程 `.multiagent/board`） |

数据安全 5 项全过：真实库表计数、文件大小/mtime 在单次运行窗口内不变；7420 `total` 不变；副本上有 `e2e-*` 写入、真实库里 `e2e-*` 事件与 Agent 计数为 **0**。

> 真实库是活动靶场（本轮期间其他 Agent 从 420 涨到 421 条任务），所以「不变」只按单次运行的前后快照比较；更强的证据是 C-05——真实库里查不到任何 `e2e-%` 写入痕迹。

## 结果明细

| 组 | 用例 | 通过 | 失败 | 备注 |
| --- | --- | --- | --- | --- |
| A 后端契约 | 40 | 40 | 0 | 含 3 条 WARN |
| D 执行策略 | 5 | 5 | 0 | 2 条 WARN（无写入口、平台未声明） |
| E 回顾数据 | 6 | 6 | 0 | 语义层全绿 |
| F 空库 | 5 | 4 | 1 | `tasks/projects` 返回 null |
| G 推送压力 | 3 | 3 | 0 | 积压 250 条分页不丢 |
| C 数据安全 | 5 | 5 | 0 | — |
| UI-B 看板 | 26 | 20 | 6 | 首轮 6 条缺陷 |
| UI-D/E/F 策略与回顾 | 14 | 8 | 6 | 本轮新增 6 条缺陷 |

## 缺陷清单

### P1-1 观测类四视图把「没选工程」渲染成「没有数据」

- **现象**：`?view=timeline`（无 project）显示「这个小时窗口里没有可显示的区间。换个小时或缩小缩放再试。」；activity 显示逐小时 `missing`；sessions 说「没有可观测会话」；analytics 说「本范围内暂无统计」。`data-status` 全是 `empty`。
- **根因**：`VnextHost.vue` 已按 `needsProject()` 注入 `NO_PROJECT_STATE(status:"no_project")`，但 `timeline/activity/sessions/analytics` 四个渲染器都没有 `no_project` 分支（`rg no_project` 命中 0），落到默认空态。
- **影响**：用户会以为「这段时间真的没活动」，而不是「你没选工程」。这正是 `view-context.mjs` 注释里禁止的「用空列表冒充空成功」。
- **证据**：`screenshots/08-no-project-timeline.png`；`ui-final.log` B-04。
- **修复入口**：四个 `*-view.mjs` 增加 `status === "no_project"` 分支，文案指向工程下拉；顺带补单测。

### P1-2 看板泳道漏掉 backlog / cancelled，卡片数与统计对不上

- **现象**：全局看板统计 `Total 420`，泳道卡片合计 **383**，差 37 条——`counts` 里 `backlog: 37` 无处可去。
- **根因**：`BoardView.vue` 的 `LANES` 只有 ready/active/review/attention/done；`BOARD_DESIGN.md` 的状态泳道表写的是「Queued = backlog + ready」「Finished = done + cancelled」。
- **影响**：看板不是「任务去哪了」的可靠视图；新登记但未派发的题（backlog）完全不可见。
- **证据**：`ui-final.log` B-26「泳道合计 383 与接口 total 420 不一致」。
- **修复入口**：`LANES` 的 ready 并入 backlog、done 并入 cancelled（或单开 Cancelled 泳道）。

### P1-3 选「全部工程」会被踢出看板

- **现象**：在看板把工程下拉切到「全部工程」，页面立刻变成 `?view=nodes`（执行节点），看板不可见。
- **根因**：`stores/board.ts` `setProject('')` 里 `if (!id && def.legacy) patch.view = 'nodes'`。
- **影响**：与 `BOARD_DESIGN.md`「选 All projects 强制看板并禁用拓扑」相反；跨工程视图只能靠手拼 `?view=board` 进入。
- **证据**：`ui-final.log` B-05；手工复现：`/?view=board&project=<id>` → 选「全部工程」→ URL 变 `?view=nodes`。
- **修复入口**：`setProject` 的空值分支改为保留/强制 `board`，由 ViewToggle 负责禁用 topo。

### P1-4 筛选与搜索不进地址栏，刷新即丢

- **现象**：看板选「done」+ 搜索框输入后，URL 始终是 `/#<projectId>`；刷新回到未筛选状态。
- **根因**：`router.mjs` `serializeQuery()` 对 `view === DEFAULT_VIEW('board')` 直接返回空串（为保持旧链接形态），把 `agent/status/q` 一起丢了。
- **影响**：看板是唯一提供筛选器的入口，等于筛选条件完全不可持久、不可分享。
- **证据**：`ui-final.log` B-25「Received: http://127.0.0.1:7421/#7b24d390e1ee056d」。
- **修复入口**：board 视图只在「无筛选且无视图参数」时省略查询串，不要再无条件清空。

### P1-5 任务抽屉不接管焦点

- **现象**：点开抽屉后 `document.activeElement` 仍在背后的卡片上；Tab 可继续走到页面背景。
- **根因**：`TaskDrawer.vue` 只设置 `aria-modal` 与 `inert`，打开时没有 `focus()` 首个可聚焦元素，也没有焦点环回。
- **影响**：键盘/读屏用户的实际操作路径仍停留在列表；`BOARD_DESIGN.md` §Interaction 明确写「Drawer traps focus」。
- **证据**：`ui-final.log` B-13。
- **修复入口**：打开时聚焦 `#d-close` 或首个操作按钮，`keydown` 里做 Tab 环回，关闭时把焦点还给触发卡片。

### P2-1 `/` 快捷键未实现

- **现象**：焦点在页面空白处按 `/`，搜索框不会获得焦点。
- **根因**：全站没有 `/` 的 keydown 处理（`rg "key === '/'"` 命中 0，只有 Escape / Enter / 空格 / 方向键）。
- **影响**：`BOARD_DESIGN.md` §Interaction 承诺「`/` focuses search」；页面上还印着 `/` 提示符，属于「提示了但没做」。
- **证据**：`ui-final.log` B-10。

### P2-2 顶栏 `Updated` 是请求时间，不是数据时间

- **现象**：轮询下 `Updated` 每 3 秒跳一次；接口 `updated_at` 也随之变化。
- **根因**：`board.BuildSnapshot` 里 `UpdatedAt: time.Now()`，每次请求重新生成（指纹计算时被显式清空，说明作者知道它不是状态）。
- **影响**：用户与外部监控都无法从该字段判断「数据是不是新的」；数据停更时界面依然显示「刚刚更新」。
- **证据**：`api-report-2026-11-02.json` C-03 WARN（两次读取不同）。

### P2-3 hub 上 v1 写路由不可达，且错误是纯文本 405

- **现象**：`POST /api/v1/projects/{id}/tasks` 返回 `405 Method Not Allowed`（纯文本），而 `/api/runtime` 声明 `managed_via: "cli_or_api_v1"`。
- **根因**：`internal/app/board_cmd.go` 的 v1 分流只挂 read / events / observability；`internal/httpapi/resources`（写树：建任务、claim、merge、retry…）没有被装配进 hub。
- **影响**：按 `/api/runtime` 的自述去调 v1 写接口会拿到非 JSON 错误，排障成本高；文档/桌面端若据此实现写操作会失败。
- **证据**：`api-report-2026-11-02.json` A-36 WARN。

### P2-4 会话身份上报 status-reports：200 + ok:true 但一条没落

- **现象**：会话 Cookie 身份（`session:<id>`）提交上报，HTTP 200、`ok:true`，但 `accepted:0 / rejected:1`，理由是 actor 不符合 `{应用}-{工程}-{序号}`。
- **根因**：鉴权要求 `actor == identity.Actor`，而会话身份天然是 `session:...`，随即被业务命名规则拒绝；两者互斥。
- **影响**：只看 `ok` 的调用方会以为写成功了；实测 `maw` CLI 令牌路径（actor 合法）才返回 `accepted:1`。
- **证据**：`api-report-2026-11-02.json` A-40 WARN。

### P2-5 `/api/runtime` 形状依赖服务进程 cwd

- **现象**：同一二进制，从工程目录启动返回 `extensions / extended_enabled / platform_supported`；从非工程目录启动只返回 3 个字段。
- **根因**：`runtimeView` 里 `resolveProject(cwd)` 失败就整体降级。
- **影响**：桌面端从 App bundle 目录拉起 `maw serve` 时，「执行策略」页看不到能力矩阵与平台支持；文档里的 `jq '.auto_execution.extensions'` 会得到 null。
- **证据**：7421（cwd=/tmp）与 7420（cwd=工程）对比。

### P3 契约宽松项（不影响功能，但会让客户端猜）

| 现象 | 证据 | 建议 |
| --- | --- | --- |
| `?project=<不存在>` 返回 `tasks: null` 而非 `[]` | A-11 | 统一空数组 |
| `risks` 常为 `null`（类型声明为数组） | `/api/board` 响应 | 统一 `[]` |
| `POST /api/board` 返回 200 而非 405 | A-12 WARN | 加方法校验 |

## 第二轮新增缺陷（执行策略 / 回顾）

### P1-6 「保存设置」按钮没接线，点击会把用户带离策略页

- **现象**：填完值点保存，浏览器发起 **GET 原生表单提交**，URL 变成
  `/?limit=&capacity=&retentionDays=&retentionRuns=`——`view=settings` 与 `project=` 一起丢掉，页面跳回默认视图，输入作废，也没有任何保存请求。
- **根因**：`settings-view.mjs` 只产出静态表单（无 `action`/`method`、无提交处理），`VnextHost.vue` 也未挂任何保存逻辑；`validateDraft()` 这套校验器根本没有调用方。
- **影响**：策略页写着「保存设置」，实际是不可用的死表单。
- **证据**：`screenshots/11-policy-after-save.png`；`ui-pr1.log` D-04；手工复现 URL 如上。

### P1-7 「计算上限」显示「—」，与 `/api/runtime` 的 1 不一致

- **现象**：概念表里 Auto mode 显示 Off，但 Execution limit / Node capacity / max_parallel 全是「—」，而接口明确返回 `managed_execution_limit: 1`。
- **根因**：`settingsStateFrom()` 只把 `enabled→auto_enabled`、`config_version→revision` 对齐，`managed_execution_limit` 没对齐；`stores/board.ts` 调 `stateForKind('settings', payload, { readOnly })` 时也没注入 `normalizePolicy`，于是 `policy.limit` 永远是 undefined。
- **证据**：`ui-pr1.log` D-01「Expected "1", Received "—"」。

### P1-8 策略表单不回填当前值

- **现象**：四个输入框（limit/capacity/retentionDays/retentionRuns）初始全空。
- **根因**：视图是「渲染草稿」模型，但页面没有持有 draft 状态，也没有回填动作。
- **影响**：用户既看不到现状，也无从判断该改哪个值；配合 P1-6，这一页目前只能看概念说明。

### P2-6 平台支持未声明，自动化却显示成「关闭」

- **现象**：`/api/runtime` 不含 `platform_supported`（非工程 cwd 启动时如此），页面显示 **Off**。
- **矛盾点**：`view-context.mjs` 的注释写着「没说不猜成「支持」」，但 `settings-view.mjs` 的 `autoModeOf()` 把 `undefined` 当成支持（只有显式 `false` 才判未就绪）。
- **影响**：用户会以为「手动开一下就行」，而真实情况是平台能力未知。

### P2-7 策略没有任何写入口

- **现象**：`POST/PATCH /api/v1/projects/{id}/policy`、`POST …/settings` 全部 404。
- **影响**：与 P1-6 合起来，等于「策略页不可写」；若这是有意的只读设计，页面就不该渲染可编辑输入框与保存按钮。

### P2-8 时间轴工具栏（上一段/下一段/缩放）没有接线

- **现象**：点 `Previous` 无请求、窗口标签不变；`Next` / `Zoom in` / `Zoom out` 同样无反应。
- **证据**：`ui-pr1.log` E-03「既没发请求也没改变窗口」。

### P2-9 时间轴窗口标签只印时分，24 小时窗口看起来像 0 长度

- **现象**：标签为 `Hour window: 16:18 – 16:18 (24h · Asia/Shanghai)`。
- **根因**：`formatAxis()` 只输出 `HH:MM`，跨天窗口首尾同形。

### 回顾数据的事实（不是缺陷，但决定这一块能测到什么）

- 全库 **`projection_state` 0 行、`source_sessions` 0 行**（真实库与副本一致），即时间轴投影从未构建；
  代码注释也写明「重建需要事实流水线，那条链路不归本卡接线」。
- 因此 `timeline/activity/analytics` 恒为 `status=unknown` + `missing.reason=projection_not_built`，
  `sessions` 为 `ready/complete` 但 0 条。
- **能验的**：语义诚实性（不把未知说成 0）、统计页交付数与 `deliveries` 表一致（43 条）、插入检查点后改为报水位。
- **不能验的**：区间渲染、热图颜色分布、会话轮次详情——这些数据在系统里尚不存在。

## 通过项（抽样）

- 鉴权：匿名写 401、缺 CSRF/Origin 403、令牌 actor 不匹配 403、会话读 v1 200。
- 分页：`limit=50` 翻 9 页拿全 420 条，无重复无遗漏；`offset/page` 与非法 `limit` 均 422。
- 观测：非法 `start/tz/hours` 四种都 422 且带错误码。
- SSE：首帧 `ready`，写库后 `board-changed` 在 6s 内到达。
- 界面：13 视图无 console 错误；主题/语言刷新后保持；拓扑 41 个节点可交互；390px 无横向溢出；断网显示离线并保留旧卡片；复制接入提示词进剪贴板。

## 复跑方式

```bash
# 1) 隔离副本 + 独立实例
mkdir -p /tmp/maw-e2e/global
sqlite3 ~/.maw/maw.db ".backup /tmp/maw-e2e/global/maw.db"
cd /tmp/maw-e2e && MAW_GLOBAL=/tmp/maw-e2e/global maw serve --addr 127.0.0.1:7421

# 1b) 空库实例（F 组用）
mkdir -p /tmp/maw-e2e/empty
cd /tmp/maw-e2e && MAW_GLOBAL=/tmp/maw-e2e/empty maw serve --addr 127.0.0.1:7422

# 2) 后端契约（64 条，约 25s，含 20s 心跳）
python3 tests/e2e/api_e2e.py --base http://127.0.0.1:7421 \
  --live-base http://127.0.0.1:7420 --live-db ~/.maw/maw.db \
  --token "$(cat /tmp/maw-e2e/token.txt)" \
  --copy-db /tmp/maw-e2e/global/maw.db --empty-base http://127.0.0.1:7422 \
  --report /tmp/maw-e2e/api-report.json

# 3) 浏览器（26 + 14 条，约 2min）
E2E_BASE=http://127.0.0.1:7421 E2E_ALLOW_WRITE=1 \
  npx --prefix tests/browser playwright test specs/board-e2e.browser.spec.ts
E2E_BASE=http://127.0.0.1:7421 E2E_REVIEW_PID=931269ebc68abb88 \
  E2E_EMPTY_BASE=http://127.0.0.1:7422 \
  npx --prefix tests/browser playwright test specs/board-policy-review.browser.spec.ts
```

> `A-38/A-39` 需要一枚写作用域令牌。当前构建没有签发命令，脚本说明里给的方法是在**副本** `auth/tokens.json` 里预置一枚（`hash = base64url(sha256(明文))`、`actor=e2e-goalagents-1`），重启实例生效。

## 待决

1. 5 个 P1 与 1 个 P2（`/` 快捷键）是否开卡修——都可以测试先行，现有 6 条失败用例直接就是回归门槛。
2. `P2-3` 需要先定语义：hub 是「只读看板 + 旁路上报」，还是「也要承载 v1 写」？前者改 `/api/runtime` 的 `managed_via` 措辞，后者补装配。
3. 本轮未覆盖：`maw next/claim/submit/merge` 编排链路（会真实领题）、桌面端打包、跨机访问。
