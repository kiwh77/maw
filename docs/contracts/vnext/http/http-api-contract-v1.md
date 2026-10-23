# 本机接口与授权契约 v1（冻结）

契约标识：`maw.http.api/v1`
上游契约：`maw.task.instance/v1`（`../task/`）、`maw.runtime.state/v1`（`../execution/`）、`maw.delivery.manifest/v1`（`../delivery/`）、`maw.app.ports/v1`（`../ports/`）——身份、运行状态、交付清单与端口错误语义直接引用，不在这里重复定义。
状态：**冻结**。后续实现以本文件为唯一基线；改含义只能出 v2。

范围：`/api/v1` 的资源与动作、分页、202 异步操作、幂等键、revision、错误码、同源会话／CSRF／CLI 令牌，以及旧入口（`/api/*`、`/snapshot.json`）的适配与状态归属。**不实现服务端，不改 `internal/board`，不建表。**

## 0. 为什么需要这份契约

现状只有一组面向页面的私有接口（`internal/board/serve.go`），边界靠调用点自觉：

1. **读写不分。** `/api/board`、`/api/task`、`/api/feed` 是读，`/api/status` 是写；写接口只检查 `isLoopback`（`serve.go:107`），没有身份、没有 CSRF、没有 revision。任何本机进程都能改状态。
2. **没有版本条件。** `tasks.revision` 已经存在（`internal/db/db.go:51`），但 HTTP 层从不回传，也不接受 `If-Match`。两个人同时点「领取」，后到的覆盖先到的，界面上看不出谁赢了。
3. **长动作被伪装成立即完成。** 领取、提交、合入都要建 worktree、跑 git、等进程，现行接口却同步返回。客户端超时重试会重复触发，因为既没有 202，也没有幂等键。
4. **旧状态上报没有归属。** `/api/status` 直接写 `StatusReport`（`serve.go:14`），`task_id` 与 `status` 可以一起提交，来源侧等于能改业务状态——与 `maw.app.ports/v1` 的 P130 直接冲突。

本契约把这四件事拆成：资源与动作、异步操作、条件写入、授权边界。

## 1. 资源与动作

所有路径以 `/api/v1` 开头，只使用 HTTPS 之外的 loopback 也按同一规则。资源用复数名词，动作是资源下的子集合；不使用动词路径（`/doSomething`）。

| 方法与路径 | 语义 | 类别 |
| --- | --- | --- |
| `GET /api/v1/board` | 看板快照（project／status／agent 过滤） | read |
| `GET /api/v1/projects` | 工程列表 | read |
| `GET /api/v1/projects/{project_id}/tasks` | 任务列表，分页 | read |
| `GET /api/v1/tasks/{instance_id}` | 单任务详情（含事件摘要） | read |
| `GET /api/v1/projects/{project_id}/agents` | Agent 与节点占用 | read |
| `GET /api/v1/events` | 事件流，按 `cursor` 增量拉取 | read |
| `GET /api/v1/events/stream` | SSE 失效通知（只提示「有变化」，不承载事实） | read |
| `POST /api/v1/projects/{project_id}/claims` | 领取一道任务 | async |
| `POST /api/v1/tasks/{instance_id}/status-transitions` | 状态迁移（block／cancel 等） | async |
| `POST /api/v1/tasks/{instance_id}/submissions` | 提交交付 | async |
| `POST /api/v1/tasks/{instance_id}/merges` | 合入 | async |
| `POST /api/v1/status-reports` | 来源状态上报（不写任务状态） | write |
| `GET /api/v1/operations/{operation_id}` | 查询异步操作 | read |

三条命名规则：

- 路径参数 `{instance_id}` 必须是 `ti_` 形态（`maw.task.instance/v1` 的 E111），**不接受显示编号**。显示编号只出现在 `?display_id=VN-005` 这类查询参数里；命中多个实例返回 `409 ambiguous_display_id`，不允许取第一条（E153）。
- 写动作一律挂在具体资源下；`claims` 是工程级动作，因为领取前还不知道是哪道题。
- 响应统一包一层：成功 `{"ok": true, ...}`，失败 `{"ok": false, "error": {...}}`；列表必须带 `items` 与 `next_cursor`，不得只返回裸数组。

## 2. 分页

| 项 | 规则 |
| --- | --- |
| 风格 | 游标分页；`offset`／`page` 参数一律拒绝（`422 invalid_pagination`） |
| `limit` | 省略时 `50`；允许 `1`–`200`；越界即 `422 invalid_pagination`，不静默截断 |
| `cursor` | 不透明字符串，由服务端生成；客户端只能原样回传，不得解析或自造 |
| 排序 | 稳定且与游标一致；任务按 `priority, merge_order, display_id`，事件按入库 `cursor` 严格递增 |
| 终止 | 最后一页 `next_cursor` 为 `null`；有下一页时必须非空 |
| 一致性 | 列表可带 `revision`（工程级聚合版本）；翻页期间数据变化不回退游标 |

事件流只能用 `cursor` 增量拉取，不允许「按时间重查全量」。迟到事件按 `maw.runtime.state/v1` §7 处理，不回头改游标。

## 3. 202 与异步操作

所有 `async` 动作立即返回 `202 Accepted`，并给出操作地址：

```http
HTTP/1.1 202 Accepted
Location: /api/v1/operations/op_7f3c9a
Content-Type: application/json

{
  "ok": true,
  "operation": {
    "operation_id": "op_7f3c9a",
    "status": "queued",
    "action": "claim",
    "resource": "/api/v1/projects/716c30878ba1b95f/claims",
    "created_at": "2026-10-23T03:10:00Z"
  },
  "revision": 42,
  "idempotent_replay": false
}
```

| 项 | 规则 |
| --- | --- |
| 状态集合 | `queued` → `running` → `succeeded`／`failed`；四值封闭，不得新增同义词 |
| 轮询 | 非终止态必须带 `retry_after_ms`（`200`–`5000`）与 `Location`；客户端按此退避 |
| 终止态 | `succeeded` 带 `result`；`failed` 带 `error`；两者都不再变化，重复查询返回同一结果 |
| 成功判据 | 只有 `succeeded` 才算完成；`202` 与 `running` 都不是成功，不得提前改看板状态 |
| 失败映射 | 异步内部失败用第 6 节的错误码，不新造 HTTP 状态；底层 `busy` 以 `503` + `Retry-After` 暴露 |
| 禁止 | 同步执行长动作后仍返回 `202`；`202` 响应体里宣称 `status: succeeded`；没有 `Location` |

同步只允许用于纯读与幂等的小写入（`status-reports`）。建 worktree、跑 git、起进程必须在 202 之后完成（对应 `maw.app.ports/v1` §3 的事务外边界）。

## 4. 幂等键

| 项 | 规则 |
| --- | --- |
| 适用范围 | 所有 `POST` 动作，包括异步与 `status-reports` |
| 载体 | 请求头 `Idempotency-Key`；不得放在 body 或 query |
| 形态 | `[A-Za-z0-9._:-]{16,128}`，建议 UUIDv4；不合法即 `422 invalid_idempotency_key` |
| 作用域 | `(project_id, actor, method, path)`；跨路径复用同一键互不影响 |
| 同键同请求 | 返回原 `operation_id`（`202`／`200`），`idempotent_replay: true`，不得再执行一次 |
| 同键不同请求 | `409 idempotency_key_reuse`，不得「以最新请求为准」 |
| 保留期 | 至少 24 小时；过期后同键视为新请求 |

客户端重试必须原样复用键与 body；服务端不得因为「内容看起来一样」就自动合并不同键的请求。

## 5. revision 与条件写入

`revision` 是任务与工程行的单调版本（`internal/db/db.go:51`）。HTTP 层必须暴露并强制它。

| 项 | 规则 |
| --- | --- |
| 读 | 单资源响应带 `revision`（整数）与 `ETag: "rev-<n>"`；列表带工程级 `revision` |
| 写 | 每个写动作必须带 `If-Match: "rev-<n>"` 或 body 里的 `expected_revision`；两者都有时必须相等 |
| 缺失 | `422 revision_required`，不得默认「以最新为准」 |
| 不匹配 | `409 revision_mismatch`；响应 `error.details.actual_revision` 给出当前值 |
| 成功 | 新 revision = 旧值 + 1（同一事务内）；响应带新 `revision` 与 `ETag` |
| 前置条件 | `If-None-Match` 可用于读缓存，返回 `304` 时不得带 body |

revision 与运行代际（`generation`）是两套条件：revision 管「这行数据有没有被别人改」，generation 管「这个 Run 的写入是否已过期」。写动作两者都带时必须同时满足。

## 6. 状态码与错误体

| 状态 | 何时 | `error.code` 示例 |
| --- | --- | --- |
| `200` | 同步成功、操作查询成功 | — |
| `202` | 异步动作已受理 | — |
| `304` | `If-None-Match` 命中 | — |
| `400` | JSON 解析失败、必填字段缺失 | `malformed_json`、`missing_field` |
| `401` | 无凭据、凭据无效或过期 | `unauthenticated` |
| `403` | 凭据有效但无权：CSRF／Origin／scope／loopback | `csrf_failed`、`origin_mismatch`、`insufficient_scope`、`loopback_required` |
| `404` | 资源或操作不存在 | `not_found` |
| `405` | 方法不允许；必须带 `Allow` | `method_not_allowed` |
| `409` | 版本、归属、幂等键、歧义编号、写盘冲突 | `revision_mismatch`、`idempotency_key_reuse`、`ambiguous_display_id`、`not_owner`、`write_conflict` |
| `422` | 语法正确但语义不合法 | `invalid_pagination`、`invalid_idempotency_key`、`revision_required`、`unknown_enum`、`invalid_transition`、`dependency_retired`、`unknown_actor` |
| `503` | 数据库忙或依赖暂不可用；带 `Retry-After` | `busy` |

错误体固定形状：

```json
{
  "ok": false,
  "error": {
    "code": "revision_mismatch",
    "message": "task revision changed",
    "retryable": false,
    "details": { "actual_revision": 43 }
  }
}
```

`401` 与 `403` 不得混用：没有凭据是 `401`，有凭据但被拒是 `403`。`422` 不得用来表达冲突，`409` 不得用来表达字段格式错误。

## 7. 授权：同源会话、CSRF、CLI 令牌

### 7.1 浏览器同源会话

| 项 | 规则 |
| --- | --- |
| Cookie | `maw_session`（HttpOnly、SameSite=Lax、仅 loopback 主机下发） |
| CSRF | 双提交：`maw_csrf` 可读 Cookie + 请求头 `X-CSRF-Token`，两者必须相等 |
| Origin | 所有非安全方法必须带 `Origin`，且与 `Host` 同源；缺失或跨源 → `403 origin_mismatch` |
| 只读豁免 | `GET`／`HEAD` 不要求 CSRF 与 Origin，但仍要求会话 |

### 7.2 CLI 令牌

| 项 | 规则 |
| --- | --- |
| 载体 | `Authorization: Bearer maw_pat_<...>`；**禁止** query／body 传令牌 |
| 作用域 | `read`、`write`、`admin`；缺 scope → `403 insufficient_scope` |
| 生命周期 | 可撤销；撤销后立即 `401 unauthenticated` |
| 日志 | 令牌不得出现在响应、错误体、访问日志与看板事件里；回显即视为泄漏 |

### 7.3 旧入口

| 旧入口 | 适配 | 写入 |
| --- | --- | --- |
| `GET /api/board` | `GET /api/v1/board` 的投影 | 否 |
| `GET /api/task?id=` | `GET /api/v1/tasks/{instance}?display_id=` | 否 |
| `GET /api/feed?since=` | `GET /api/v1/events?cursor=` | 否 |
| `/api/events`（SSE） | `GET /api/v1/events/stream` | 否 |
| `GET /snapshot.json` | `308` 到 `/api/v1/board` | 否 |
| `POST /api/status` | `POST /api/v1/status-reports`，仅 loopback | **不写任务状态** |

旧响应必须带 `X-Maw-Legacy: 1` 与 `Link: </api/v1/...>; rel="successor-version"`。旧写入口保留的唯一理由是兼容既有脚本，且只允许「来源上报」语义。

### 7.4 旧状态归属

`POST /api/status` 与 `/api/v1/status-reports` 都是**来源上报**，不是业务状态写入（对应 `maw.app.ports/v1` P130）：

- `actor` 必须是本工程已注册的 Agent，否则 `422 unknown_actor`；非 Agent 的外部来源必须显式写 `attribution: "external"` 并带 `client`。
- 带 `task_id` 时，只允许记录「该 Agent 对这道题的进度／心跳」；`actor` 与任务当前 owner 不一致 → `409 not_owner`。
- 允许写入的字段只有 `reported_status ∈ {idle, working, blocked, offline}`、`message`、`pct`、`phase`、`heartbeat`。**禁止**通过上报把任务写成 `in_progress`／`done`；任务状态只能走 `status-transitions`／`submissions`／`merges`。
- 上报成功体必须显式声明 `changed_task_status: false`，否则视为越权。
- loopback 只是部署约束，不是安全边界；脚本仍须带 `actor`。

### 7.5 不承诺强隔离

同一操作系统用户下的其他进程可以读同一份令牌文件、连同一个 loopback 端口、伪造 `Host` 与 `Origin` 头。因此：

- 本契约只保证「浏览器跨站请求不能借会话改状态」与「无令牌的远端进程不能调用」。
- **不**承诺同用户进程之间互相隔离；需要强隔离时使用独立系统用户、独立数据目录或容器，这不在本契约范围。
- 令牌文件权限必须 `0600`，路径不写进 HTTP 响应。

## 8. 不变量登记表

机检主体是 `http_contract.py`；本表由 `test_http_contract.py` 强制与代码对齐（多一条、少一条都失败），每条至少有 `samples/invalid/` 反例背书。

| 码 | 判定对象 | 不变量 |
| --- | --- | --- |
| H100 | 全文档 | 必填字段存在且类型正确 |
| H101 | 根节点 | `contract` 必须是 `maw.http.api/v1` |
| H110 | 请求／响应 | 2xx 必须已认证；旧 `/api/status` 仅 loopback 无凭据例外 |
| H111 | 会话写请求 | 必须带 Origin 同源且 `X-CSRF-Token` 等于 `maw_csrf` |
| H112 | CLI 令牌 | 只能走 `Authorization: Bearer`；query 不得出现令牌；响应不得回显 |
| H113 | 状态码 | 无凭据只能 `401`，有凭据被拒只能 `403` |
| H120 | 分页 | `limit` 在 1–200；列表有 `items` 与 `next_cursor`；拒绝 offset |
| H121 | 语义校验 | 非法枚举与非法分页必须是 `422`，不得 `400`／`200` |
| H130 | 异步动作 | 返回 `202` + `Location: /api/v1/operations/{id}` + 非终止 `status` |
| H131 | 操作查询 | 非终止态带 `retry_after_ms`；终止态不再变化；未知操作 `404` |
| H140 | 幂等键 | 所有 POST 必带合法键；同键同请求复用操作；同键异请求 `409` |
| H150 | 条件写入 | 写动作必须带 revision 前置；成功回新 revision；不匹配 `409` |
| H160 | 错误映射 | `error.code` 与 HTTP 状态必须符合第 6 节映射 |
| H170 | 旧入口 | 仅登记路径可用；响应带旧版标记与后继链接；旧写仅 status-reports |
| H171 | 状态归属 | 上报不得改任务状态；`actor` 必须存在且与 owner 一致 |

## 9. 正反例

样本是「一次 HTTP 交换」的 JSON：`request{method,path,query,headers,body,loopback}`、`response{status,headers,body}`、`expect{valid,codes}`。

正例（`samples/valid/`）：

| 文件 | 覆盖现场 |
| --- | --- |
| `01-list-with-token.json` | Bearer 读列表，分页与 ETag 正确 |
| `02-claim-async.json` | 领取返回 202、Location、幂等键与 revision |
| `03-operation-poll.json` | 查询运行中的操作，带 `retry_after_ms` |
| `04-session-csrf-write.json` | 同源会话 + CSRF 双提交成功 |
| `05-legacy-status-report.json` | loopback 旧上报，归属正确且不改任务状态 |
| `06-revision-conflict.json` | 合法地返回 409 `revision_mismatch` 与当前 revision |

反例（`samples/invalid/`，用 `expect_error` 声明应触发的码）：无凭据 200、会话写缺 CSRF、令牌放 query、有凭据却 401、`limit=500` 仍 200、异步动作 200、操作查询缺 `retry_after_ms`、POST 缺幂等键、同键异请求 202、写缺 revision、错误码与状态不符、旧写改任务状态、actor 与 owner 不一致。

验证命令（本卡 Verify）：

```sh
python3 -m unittest discover -s docs/contracts/vnext/http -p 'test_*.py'
```

## 10. 本契约不做的事

- 不实现 Go 服务，不改 `internal/board`、`internal/db`、`internal/app`；不建表、不加中间件。
- 不定义节点登记、启动规格、事件解析、交付物校验（分别属 VN-302／VN-303／VN-304／VN-306）。
- 不规定 TLS、反向代理与多机部署；默认只监听 loopback。
- 不承诺同用户进程之间的强隔离（见 §7.5）。
