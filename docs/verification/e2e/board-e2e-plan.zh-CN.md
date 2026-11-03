# maw 看板端到端测试计划（E2E-731）

> 对象：`maw serve`（默认 `http://127.0.0.1:7420/`）——多 Agent 调度看板与 `/api/v1` 控制面。
> 依据：`docs/BOARD_DESIGN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`cmd/maw` 路由与 `internal/board`、`internal/httpapi/**` 实现。
> 版本：v1（2026-11-02）。

## 1. 目标

回答三个问题，并给出可复核的证据：

1. **功能是否按设定工作**：看板 13 个视图、筛选/搜索/详情抽屉/主题语言、SSE 实时刷新，是否都能在真实浏览器里跑通。
2. **接口是否符合契约**：`/api/*` 与 `/api/v1/*` 的状态码、字段、分页、鉴权、错误码是否与实现声明一致。
3. **是否安全**：所有写操作只发生在隔离副本上；真实状态库与线上实例在测试前后**逐字节无变化**。

不在本次范围：`maw next/claim/submit/merge` 等编排写链路（会真实领取任务，违反「不动活动数据」前提）、`clients/desktop` 打包产物、跨机网络访问。

## 2. 环境与数据隔离

| 项 | 真实环境 | 测试环境 |
| --- | --- | --- |
| 状态库 | `~/.maw/maw.db` | `/tmp/maw-e2e/global/maw.db`（`sqlite3 .backup` 一致性快照） |
| 端口 | `127.0.0.1:7420` | `127.0.0.1:7421` |
| 环境变量 | — | `MAW_GLOBAL=/tmp/maw-e2e/global` |
| 启动目录 | 工程根 | `/tmp/maw-e2e`（非 git 仓库，避免写工程 `.multiagent/board`） |

隔离依据：`home.GlobalDir()` 优先读 `MAW_GLOBAL`，令牌、会话密钥、看板快照全部落在临时目录；端口独立，互不干扰。

**写操作用例只打 7421**；7420 只做只读断言（`total`、`updated_at`、DB 文件 mtime/size）。

## 3. 用例矩阵

优先级：**P0** 阻断级（坏了等于不可用）／**P1** 主要功能／**P2** 边界与观感。

### 3.1 A 组：HTTP 契约（API）

| ID | 场景 | 期望 | 级别 |
| --- | --- | --- | --- |
| A-01 | `GET /` | 200、`text/html`、`no-store`、含 `#app` 与 `assets/board-web.js` | P0 |
| A-02 | `GET /assets/board-web.js` | 200、`no-store`、`javascript` | P0 |
| A-03 | `GET /assets/`、`/assets/nope.js` | 404（禁止目录列举） | P1 |
| A-04 | `GET /nope` | 404 | P2 |
| A-05 | `GET /snapshot.json` | 307 → `/api/board` | P1 |
| A-06 | `GET /api/board` 形状 | 必需字段齐全；`total==len(tasks)`；Σ`counts`==`total`；`done==counts.done`；`active==claimed+in_progress+self_check`；`percent` 与 `done/total` 一致 | P0 |
| A-07 | `project` 过滤 | 返回任务全属该工程；`filter_project` 回显 | P0 |
| A-08 | `agent` 过滤 | 返回任务 `owner` 全等于该值 | P1 |
| A-09 | `status` 过滤 | 返回任务状态全等于该值 | P1 |
| A-10 | `project`+`status` 组合 | 两个约束同时成立 | P1 |
| A-11 | 未知 `project` | 200，空集（不 5xx、不返回全量） | P1 |
| A-12 | `POST /api/board` | 405 + `Allow` | P2 |
| A-13 | `GET /api/task` 缺 `id` | 400 | P1 |
| A-14 | `GET /api/task?id=<未知>` | 404 | P1 |
| A-15 | `GET /api/task?id=<真实>` | 200，`task.id` 匹配、`events` 为数组、`project` 非空 | P0 |
| A-16 | `GET /api/feed?since=0` | 事件升序、`next_since`=末条 id、`has_more` 布尔 | P1 |
| A-17 | `GET /api/feed?since=<末条>` | 空数组、`next_since` 回显 | P1 |
| A-18 | `GET /api/events` SSE | 首帧 `event: ready`；数据变更后收到 `board-changed` | P0 |
| A-19 | `GET /api/runtime` | `ok=true`、`managed_via=cli_or_api_v1`、`static_export_managed=false`、`auto_execution` 三必需键 | P1 |
| A-20 | `GET /api/log` 缺参 | 400；有参且无日志 → `missing:true` 且 200 | P1 |
| A-21 | `POST /api/status` 无凭证 | 401 `unauthenticated` | P0 |
| A-22 | `POST /api/session` | 200，下发会话 Cookie 与 CSRF | P0 |
| A-23 | 会话读 `/api/v1/*` | 200 | P0 |
| A-24 | 写请求缺 CSRF / 缺 Origin | 403 `csrf_failed` / `origin_mismatch` | P0 |
| A-25 | 会话写 `/api/status` | 200；事件进入 `/api/feed`，看板 `updated_at` 前进 | P1 |
| A-26 | `/api/v1/board` 游标分页 | `limit=2` 翻完全集，无重复无遗漏；`next_cursor` 终止为 `null` | P0 |
| A-27 | `?offset=1` / `?page=1` | 422 `invalid_pagination` | P1 |
| A-28 | `?limit=abc` | 422 | P1 |
| A-29 | `/api/v1/projects` | 9 条，含 `project_id/name/path` | P0 |
| A-30 | 六类工程子资源 | `tasks/nodes/runs/operations/deliveries/instances` 均 200 且带 `items` | P1 |
| A-31 | `/api/v1/tasks/{未知}` | 404 | P1 |
| A-32 | 未知工程子资源 | 200 空集（不 5xx） | P1 |
| A-33 | 观测四接口 | `timeline/activity/sessions/analytics` 200 且含 `watermark` | P1 |
| A-34 | `capabilities` / `notifications` | 200，数组字段存在 | P2 |
| A-35 | 非法时间窗 | 4xx 且带错误码 | P2 |
| A-36 | v1 写路由在 hub 上是否可达 | 可达则 201；不可达记录为观察项（`/api/runtime` 自述 `cli_or_api_v1`） | P1 |
| A-37 | 上报缺 `Idempotency-Key` | 422 `invalid_idempotency_key` | P1 |
| A-38 | 上报 + 同 key 重放 | 首次 `accepted=1`，重放 `idempotent_replay=true` 且事件数不变 | P1 |
| A-39 | 令牌 actor 与身份不一致 | 403 `actor_mismatch` | P0 |
| A-40 | 会话身份上报 | 记录响应语义（`ok` 与 `accepted` 是否一致） | P2 |

### 3.2 B 组：浏览器真实渲染（Playwright，Chromium）

| ID | 场景 | 期望 | 级别 |
| --- | --- | --- | --- |
| B-01 | 无工程首屏 | 落到 `nodes`（`DEFAULT_VIEW_NO_PROJECT`），无 console error | P0 |
| B-02 | 侧栏导航 | 10 个视图项齐全，点击后 `aria-current="page"` 跟随 | P0 |
| B-03 | 13 个视图逐一切换 | 标题/眉标/描述非空，正文非空，无 console/page error | P0 |
| B-04 | 观测类视图无工程 | `timeline/activity/sessions/analytics` 给出「先选工程」而非空白或假成功 | P1 |
| B-05 | 工程下拉 | 含「全部工程」+9 工程；有任务工程带 `●`；切换写 URL `?project=` 且 hash 锚点兼容 | P0 |
| B-06 | 看板统计 | `Total` 等于接口 `total`，`Done` 等于 `done`，进度百分比一致 | P0 |
| B-07 | Agent 侧栏 | 只列 `working/busy`，数量与接口一致 | P1 |
| B-08 | Agent/状态筛选 | 改下拉即刷新列表，URL 带 `agent`/`status` | P1 |
| B-09 | 重置 | 清空 Agent/状态/搜索 | P1 |
| B-10 | `/` 快捷键 | 聚焦搜索框 | P1 |
| B-11 | 搜索过滤（tasks 视图） | 关键字命中行数下降；`q` 进 URL 且刷新后保留 | P1 |
| B-12 | 详情抽屉 | 点卡片开抽屉、URL 带 `id`、`Esc` 关闭、`aria-modal` | P0 |
| B-13 | 抽屉焦点 | 打开后焦点进入抽屉；关闭后焦点回到触发元素附近 | P1 |
| B-14 | 深链 | 直接访问 `?view=tasks&project=…&id=…` 能恢复视图与详情 | P1 |
| B-15 | 主题切换 | `Light/Dark/System` 即时生效，`localStorage.maw-theme` 持久化，刷新保持 | P1 |
| B-16 | 语言切换 | EN/中 切换文案与 `document.lang`，`maw-board-lang` 持久化 | P1 |
| B-17 | SSE/poll 实时 | 外部写库后 5s 内看板数字/任务列表更新 | P0 |
| B-18 | 断线降级 | 服务不可达时状态变 Offline/离线，保留上次数据；恢复后自动回正 | P1 |
| B-19 | 窄屏 390 | 无横向溢出（`scrollWidth<=clientWidth`），侧栏隐藏，主内容可读 | P1 |
| B-20 | 拓扑视图 | 单工程渲染节点>0；fit/zoom 控件可用；无 X6 报错 | P1 |
| B-21 | 全部工程 + 拓扑 | 拓扑入口被禁/回落 board（多工程不可读） | P2 |
| B-22 | 复制加入提示词 | 剪贴板含工程接入命令；按钮有可访问名称 | P2 |
| B-23 | 空状态 | 构造空结果时给空态文案，不留白屏 | P2 |
| B-24 | 键盘可达 | Tab 能到侧栏/筛选/卡片；卡片 Enter/Space 可开 | P2 |
| B-25 | 筛选与搜索刷新后保留 | URL 带 `agent/status/q`，刷新后回填 | P1 |
| B-26 | 泳道覆盖全部状态 | 各泳道计数之和 == `total`（含 backlog/cancelled） | P1 |

### 3.3 C 组：数据安全

| ID | 检查 | 期望 | 级别 |
| --- | --- | --- | --- |
| C-01 | 真实库 `tasks/events/agents` 计数 | 测试前后完全一致 | P0 |
| C-02 | 真实库文件 mtime/size | 不变 | P0 |
| C-03 | 7420 `total`/`updated_at` | 不变 | P0 |
| C-04 | 所有写用例落点 | 仅 `/tmp/maw-e2e/global/maw.db`（副本 `updated_at` 前进） | P0 |
| C-05 | 真实库写入痕迹 | `events/agents` 中 `e2e-%` 计数为 0 | P0 |

### 3.4 D 组：执行策略 / 自动化策略

| ID | 场景 | 期望 | 级别 |
| --- | --- | --- | --- |
| D-01 | `/api/runtime` 自动化默认值 | `enabled=false`、`managed_execution_limit≥1`、`config_version` 存在 | P0 |
| D-02 | 运行时视图泄密检查 | 递归遍历响应，无 token/secret/password 等键 | P0 |
| D-03 | 受管入口声明与读取幂等 | `managed_via=cli_or_api_v1`、`static_export_managed=false`，连读不改变状态 | P1 |
| D-04 | 平台支持声明 | `platform_supported` 必须是布尔；缺失即记录（前端会误当「支持」） | P1 |
| D-05 | 策略写入口 | 存在可保存端点；不存在则记录（页面却给可编辑表单） | P1 |
| U-D1 | 页面「计算上限」与服务端一致 | 显示 `1` 而不是 `—` | P1 |
| U-D2 | 平台支持缺失时的自动化态 | 显示「未就绪」，不是「关闭」 | P1 |
| U-D3 | 表单回填 | 输入框带当前值 | P1 |
| U-D4 | 点「保存设置」 | 不跳离策略页、不丢 `view/project` | P1 |
| U-D5 | 无秘密字段 | 页面无 password 输入，且有「凭据只在 CLI」声明 | P2 |
| U-D6 | 本地化 | 中文界面出现「自动模式/计算上限」 | P2 |

### 3.5 E 组：回顾数据（时间轴／热图／会话／统计）

| ID | 场景 | 期望 | 级别 |
| --- | --- | --- | --- |
| E-01 | 时间轴无投影 | `status=unknown` + `missing=projection_not_built`，`items` 不编造 | P0 |
| E-02 | 热图缺测 | `present_hours=0`、`hours=[]`、`missing=no_observed_buckets` | P1 |
| E-03 | 会话空态 | `count` 与 `items` 一致；空列表必须能区分「查过没有」 | P1 |
| E-04 | 统计交付数 | 与 `deliveries` 表计数一致，且投影未建时标 unknown | P1 |
| E-05 | 四接口自洽 | 有 `missing` 不得 `status=ready`；无 `missing` 不得 `unknown` | P0 |
| E-06 | 检查点在位 | 插入检查点后改为 `projection_not_rebuilt` 并回显水位；删除后回落 | P1 |
| U-E1 | 时间轴未知态文案 | 出现「incomplete/未知」，不出现「这段时间没有区间」 | P1 |
| U-E2 | 时间轴窗口标签 | 24h 窗口不能显示成 `16:18 – 16:18` 而不带日期 | P2 |
| U-E3 | 时间轴工具栏 | Previous/Next/Zoom 改变窗口或发请求 | P2 |
| U-E4 | 热图缺测标记 | 有 `--missing` 单元且文案说明「不是 0」 | P1 |
| U-E5 | 统计页数字 | 交付数与接口一致，`unknown` 有横幅 | P1 |
| U-E6 | 会话空态 | 给出「查过没有」的解释与完整性 | P2 |

### 3.6 F 组：空库冷启动 ／ G 组：实时推送压力

| ID | 场景 | 期望 | 级别 |
| --- | --- | --- | --- |
| F-01 | 空库 `/api/board` | `total=0`，`tasks/projects` 是 `[]` 而不是 `null` | P1 |
| F-02 | 空库首页与静态资源 | 200 可用 | P1 |
| F-03 | 空库 v1 | 未授权 401；授权后空集；未知任务 404 | P1 |
| F-04 | 空库运行时 | 自动化仍默认关闭 | P1 |
| F-05 | 空库任务详情 | 404 | P2 |
| U-F1 | 空库浏览器 | 首页不白屏，10 个视图都有内容 | P1 |
| U-F2 | 空库看板 | 无卡片、有空态文案、统计为 0 | P2 |
| G-01 | 事件积压 250 条 | 单页 200、`has_more=true`、翻页取全 250 不丢不重 | P1 |
| G-02 | SSE 心跳 | 安静流上 20s 内收到 `: keepalive` | P1 |
| G-03 | 写入推送 | 写库后收到 `board-changed` | P1 |

## 4. 通过准则

- P0 全绿才允许判定「可上线/可交接」；P1 允许有记录在案的问题，但必须逐条给出影响面与绕过方式。
- 涉及「回顾数据」的用例只断言**语义**（快照是否如实、缺失是否标明），不假设投影一定已建：投影是可选启用的事实流水线。
- 任何 FAIL 都必须附可复现的最小请求或操作步骤、实际值、期望值。
- 发现的问题按严重度归入报告，不直接改代码（本轮只测不改）。

## 5. 执行方式

```bash
# 1) 隔离副本 + 独立实例
mkdir -p /tmp/maw-e2e/global
sqlite3 ~/.maw/maw.db ".backup /tmp/maw-e2e/global/maw.db"
cd /tmp/maw-e2e && MAW_GLOBAL=/tmp/maw-e2e/global maw serve --addr 127.0.0.1:7421

# 2) API 契约套件（标准库，无第三方依赖）
python3 tests/e2e/api_e2e.py --base http://127.0.0.1:7421 --report /tmp/maw-e2e/api-report.json

# 3) 浏览器套件
E2E_BASE=http://127.0.0.1:7421 npx --prefix tests/browser playwright test specs/board-e2e.browser.spec.ts
```
