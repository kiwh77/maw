# 页面上下文与挂载契约 v1（冻结）

契约标识：`maw.ui.data/v1`
上游契约：`maw.http.api/v1`（`../http/`）、`maw.observability.timeline/v1`（`../observability/`）、`maw.task.instance/v1`（`../task/`）、UI 视觉契约（`../ui/`）。
状态：**冻结**。新增视图与页面一律按本契约挂载；改含义只能出 v2。

范围：`ViewContext`、公共请求客户端、视图局部双语字典、视图注册表、URL 与旧锚点兼容。**不改 `internal/board/page.html`，不实现框架，不引入构建链。**

## 0. 为什么需要这份契约

现在每个页面各自处理状态：

1. **上下文靠全局变量。** `filters`、`selectedPid`、`boardView` 都是脚本级 `let`（`page.html:876`），任何函数都能改；新增视图只能复制这套写法，测试也无从注入。
2. **请求各写各的。** 只有 `fetch(url, {cache:"no-store"})`（`page.html:1322`），失败后换备用 URL。没有 CSRF、没有幂等键、没有 `If-Match`，与刚冻结的 `/api/v1` 的形状对不上。
3. **文案是全局字典。** `t(key)` 读一张大表（`page.html:734`）；新增域只能往里塞，删一个 key 不知道谁在用。
4. **锚点含义不明确。** `#` 后直接放工程 ID（`page.html:882`），没有视图段；以后加时间轴、交付物视图必然抢同一个锚点。

本契约把「上下文」「请求」「文案」「路由」四处收口，视图只拿到一个只读上下文和一个受控客户端。

## 1. ViewContext

视图挂载时收到的唯一入口。**只读**：视图不得改写其中任何字段；需要变化时调用 `ctx.navigate(patch)`，拿到新的上下文并重新挂载。

```jsonc
{
  "api_version": "maw.ui.data/v1",
  "view_id": "board",
  "lang": "zh",
  "theme": "system",
  "project_id": "",
  "filters": { "agent": "", "status": "", "q": "" },
  "selection": { "instance_id": "", "project_id": "" },
  "cursor": "",
  "capabilities": { "write": false, "admin": false },
  "frozen": true,
  "request": "<RequestClient>",
  "i18n": "<ViewI18n>",
  "navigate": "<(patch) => ViewContext>"
}
```

| 字段 | 规则 |
| --- | --- |
| `api_version` | 固定 `maw.ui.data/v1`；不匹配拒绝挂载 |
| `view_id` | 必须是注册表内已登记的视图 |
| `lang` | `zh`／`en`；默认 `zh`，持久化键 `maw-board-lang` |
| `theme` | `system`／`light`／`dark`；持久化键 `maw-theme` |
| `filters` | 只允许 `agent`、`status`、`q`；新增筛选项须先改契约 |
| `selection` | 当前打开的任务；用 `instance_id`，不存显示编号 |
| `cursor` | 事件增量位置；不解释、不解析 |
| `capabilities` | 只读能力声明；视图按此隐藏写操作，**不能**当作授权（授权在服务端） |
| `request` / `i18n` / `navigate` | 由宿主注入；视图不得自行 `fetch`、读 `localStorage` 或改 URL |

约束：

- 上下文必须冻结（`frozen: true`），对它的写入在开发态抛错、生产态忽略；不得靠「大家都自觉」。
- `navigate(patch)` 接受 `{view_id?, project_id?, filters?, selection?, lang?, theme?}` 子集，返回**新**上下文；不得原地修改旧对象。
- 视图不得直接读写 `location`、`history`、`localStorage`；这些只属于宿主。
- 视图多实例并存时各自持有独立上下文，不得共享可变引用。

## 2. 公共请求客户端

`ctx.request(method, path, options)` 是唯一的网络入口。返回统一结果，不抛 HTTP 错误：

```jsonc
{
  "status": 202,
  "ok": true,
  "data": { "operation": { "operation_id": "op_7f3c9a", "status": "queued" }, "revision": 42 },
  "revision": 42,
  "etag": "\"rev-42\"",
  "operation_id": "op_7f3c9a",
  "error": null,
  "attempts": 1
}
```

| 项 | 规则 |
| --- | --- |
| 基址 | 同源相对路径 `/api/v1/...`；不得跨域、不得写死 host |
| 凭据 | `credentials: "same-origin"`；浏览器端**不**使用 Bearer，令牌只给 CLI |
| CSRF | 非安全方法从 `maw_csrf` Cookie 取值，写入 `X-CSRF-Token`；同时带 `Origin`（浏览器自动） |
| 幂等 | 所有写请求自动生成 `Idempotency-Key`（UUIDv4）；**重试必须复用同一个键与 body** |
| 条件写 | 传 `expectedRevision` 时同时带 `If-Match: "rev-<n>"`；收到 `409 revision_mismatch` 不自动重试，交视图提示 |
| 重试 | 只对 `GET` 的 `503`／网络错误重试，最多 3 次，尊重 `Retry-After`；`4xx` 一律不重试；写请求不自动重试 |
| 取消 | `AbortSignal` 透传；组件卸载必须 abort 未完成请求 |
| 归一化 | HTTP 错误映射为 `{ok:false, status, error:{code,message,retryable}}`；`202` 返回 `operation_id` 与轮询地址 |
| 禁止 | 令牌／幂等键放 query；回显 Cookie；把 `202` 当成功结果直接改本地事实 |
| 缓存 | 默认 `cache: "no-store"`；`304` 由 `If-None-Match` 触发，命中时返回 `status:304, data:null` |

`request` 不解释业务：拿到 `202` 后由视图轮询 `GET /api/v1/operations/{id}`（见 `maw.http.api/v1` §3）。

## 3. 局部双语字典

每个视图自带一份字典，不写进全局表。

```jsonc
{
  "view.board.title": { "zh": "任务看板", "en": "Board" },
  "view.board.empty": { "zh": "暂无任务", "en": "No tasks" }
}
```

规则：

- 键必须是 `view.<view_id>.<key>`，只含小写字母、数字与点；全局键只允许 `board.*`。不得使用裸 key。
- 中英必须成对：同一 `view_id` 下 `zh` 与 `en` 的键集合完全一致；缺一侧视为未完成，不得上线。
- 解析顺序：当前视图当前语言 → 当前视图 `zh` → 全局同 key → 原样返回 key（并只警告一次）。**不得返回空字符串**。
- 变量插值只允许 `{name}` 形式；插值值必须转义，不得直接拼 HTML。
- 字典在挂载时注册、卸载时撤销；同名视图重挂载覆盖旧字典，不叠加。
- 语言切换不重新加载页面；视图重新挂载并取新语言。

## 4. 视图注册表

```jsonc
{
  "id": "timeline",
  "title_key": "view.timeline.title",
  "order": 30,
  "match": ["#/v/timeline"],
  "mount": "function (el, ctx) { ... }",
  "unmount": "function () { ... }"
}
```

| 项 | 规则 |
| --- | --- |
| `id` | 小写字母／数字／连字符；全表唯一；重复注册直接拒绝（后注册者失败） |
| `title_key` | 必须存在于该视图字典的 `zh` 与 `en` |
| `order` | 正整数，决定导航顺序；相同 order 按 id 排序 |
| `mount(el, ctx)` | 同步或返回 Promise；必须只使用 `el` 与 `ctx`，不查询其他视图 DOM |
| `unmount()` | 必须清理定时器、监听器、订阅与未完成请求；宿主在切换视图、换语言、卸载页面时调用 |
| 失败处理 | `mount` 抛错时卸载该视图并显示错误视图，不得留下半挂载 DOM |
| 单活 | 同一时刻只有一个活动视图；切换时先 `unmount` 旧视图再 `mount` 新视图 |

注册表本身不可由视图修改；新增视图必须在启动时登记。

## 5. URL 与旧锚点兼容

| 形态 | 含义 | 处理 |
| --- | --- | --- |
| `#/v/<view_id>` | 规范视图路由 | 直接解析；未知视图回落 `board` |
| `#/v/<view_id>?project=<id>&status=<s>&agent=<a>&instance=<ti>` | 规范筛选与选中 | 写入上下文；`instance` 用实例 ID |
| `#<project_id>` | 旧锚点（工程过滤） | 映射到 `board`，`filters.project = <project_id>`，用 `replaceState` 改写为规范形态 |
| `?lang=en` | 旧语言参数 | 读取后仍以持久化值为准；首次访问采用参数并清理 |
| `#/v/board#<project_id>` | 旧锚点叠加 | 取工程段，其余同上 |
| 未知 `#/v/xxx` | 未登记视图 | 回落 `board`，保留原 hash 供开发排查，不报 500 |

规则：

- URL 只承载可分享状态：视图、工程、筛选、选中实例、语言。**不得**出现令牌、幂等键、内部路径或用户隐私。
- 筛选用 `replaceState`，避免每次输入都压历史；显式切换视图用 `pushState`。
- 深链接必须可还原：解析 → 建上下文 → 挂载 → 再序列化，结果与输入等价（编码规范化允许）。
- 非法参数（未知 status、非 `ti_` 的 instance）忽略并回落默认值，不阻塞挂载。
- 旧锚点在兼容期内只做**读映射**，不再生成；写只写规范形态。

## 6. 启动顺序

1. 读 URL → 解析 `{view_id, project_id, filters, selection, lang}`。
2. 合并持久化偏好（`maw-board-lang`、`maw-theme`），URL 显式值优先于持久化，持久化优先于默认。
3. 建只读上下文，注入 `request`／`i18n`／`navigate`。
4. 解析视图；未登记则回落 `board` 并 `replaceState` 规范地址。
5. `unmount` 旧视图 → `mount` 新视图；失败进入错误视图。
6. 注册 `hashchange` 与 `popstate`，只由宿主响应。

## 7. 不变量登记表

机检主体是 `ui_data_contract.py`；本表由 `test_ui_data_contract.py` 强制对齐，每条至少一个 `samples/invalid/` 反例。

| 码 | 判定对象 | 不变量 |
| --- | --- | --- |
| V100 | 全文档 | 必填字段存在且类型正确 |
| V101 | 根节点 | `contract` 必须是 `maw.ui.data/v1` |
| V110 | 上下文 | 必须冻结；`navigate` 返回新上下文，旧对象不变 |
| V111 | 上下文 | 必填字段齐全，`lang`／`theme`／`filters` 取值合法 |
| V120 | 请求 | 非安全方法必须带 CSRF 双提交与同源 Origin，浏览器端不得用 Bearer |
| V121 | 请求 | 写请求必须带合法幂等键；重试复用同一键；键不得进 query |
| V122 | 请求 | 条件写必须带 `expected_revision`；`409` 不得自动重试 |
| V123 | 请求 | 只有 GET 的 503／网络错误可重试，最多 3 次，4xx 不重试 |
| V124 | 请求 | 返回统一结构；`202` 暴露操作地址；错误归一化不抛 HTTP 异常 |
| V130 | 字典 | 键必须命名空间化；缺失回落顺序正确；不得返回空串 |
| V131 | 字典 | 同视图 zh／en 键集合一致 |
| V140 | 注册表 | 视图 id 唯一；mount／unmount 成对存在；切换先卸载再挂载 |
| V150 | 路由 | 规范路由可解析；旧锚点只读映射；未知视图回落；URL 无凭据 |
| V151 | 路由 | 解析→序列化往返稳定；非法参数回落默认值 |
| V152 | 路由 | 筛选变化用 replaceState，视图切换用 pushState |

## 8. 正反例

样本是一份页面装配快照（`context`／`requests`／`dicts`／`registry`／`navigation` 的任意子集）；反例期望码登记在测试里。

正例（`samples/valid/`）：

| 文件 | 覆盖现场 |
| --- | --- |
| `01-context.json` | 只读上下文 + navigate 返回新对象 |
| `02-write-request.json` | 写请求带 CSRF、幂等键、If-Match，返回 202 |
| `03-read-retry.json` | GET 503 重试三次并遵守 Retry-After |
| `04-dicts.json` | 视图字典命名空间化，中英键一致，缺失回落 key |
| `05-registry.json` | 两个视图注册、唯一 id、先卸载再挂载 |
| `06-navigation.json` | 规范路由、旧锚点、未知视图、replace／push |

反例（`samples/invalid/`）：上下文可写、缺字段、写请求缺 CSRF、写请求缺幂等键、幂等键进 query、带 Bearer、409 自动重试、410 重试、错误未归一化、裸字典键、中英键不一致、缺失返回空串、重复视图 id、mount 无 unmount、URL 含令牌、未知视图报错、非法参数阻塞挂载、筛选用 pushState。

验证命令（本卡 Verify）：

```sh
python3 -m unittest discover -s docs/contracts/vnext/ui-data -p 'test_*.py'
```

## 9. 本契约不做的事

- 不实现视图框架、路由库或请求库；不引入打包器、框架依赖或远程字体。
- 不改 `internal/board/page.html` 与 `page.test.cjs`；迁移现有页面是后续任务。
- 不定义时间轴／交付物视图的业务字段（见 `../observability/`、`../delivery/`）。
