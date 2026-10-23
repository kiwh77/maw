# 公共请求与页面上下文（vnext / UI）

VN-703 建立的客户端请求层：把授权、幂等、revision、202 轮询与结构化错误收在一处，
供 VN-704 之后的新视图共用。契约：`docs/contracts/vnext/http/http-api-contract-v1.md`（`maw.http.api/v1`）。

## 文件

| 文件 | 职责 |
| --- | --- |
| `../utils/request-context.mjs` | 请求上下文：授权头、幂等键、revision 前置、202 轮询、错误映射、epoch 与连接状态。 |
| `board-api.mjs` | `/api/v1` 资源方法：board／projects／tasks／agents／events／claims／transitions／submissions／merges／status-reports／operations。 |
| `../utils/request-context.test.cjs` | 20 条用例覆盖上述全部行为（含过期响应与刷新恢复）。 |

## 用法

```js
import { createRequestContext } from "../utils/request-context.mjs";
import { createBoardApi } from "./board-api.mjs";

const ctx = createRequestContext({ storage: localStorage });
ctx.restore();                       // 刷新后恢复工程与已知 revision
const api = createBoardApi(ctx);

const board = await api.getBoard({ status: "ready" });   // 纯读
if (!board.stale) render(board.data);                    // 过期响应直接丢弃

const claim = await api.claim("p1", { task_id: "ti_x" });  // 异步动作
// claim 只在 operation.status === "succeeded" 时才有 result
```

## 硬规则

1. **202 不是完成。** 只有 `succeeded` 算成功；`queued`/`running` 一律不算，
   超预算抛 `OperationPendingError`，界面保持「进行中」，不得提前改看板状态。
2. **写动作必须有 revision 前置。** 没有 `expectedRevision` 也没有已知 revision 时本地 `422 revision_required`，
   不会发请求，更不会默认「以最新为准」。`409 revision_mismatch` 会把 `details.actual_revision` 同步回本地。
3. **CSRF 双提交。** 会话模式下写请求读 `maw_csrf` Cookie 并放 `X-CSRF-Token`；浏览器自动带 `Origin`，
   非浏览器环境才允许显式注入。CLI 令牌只走 `Authorization: Bearer`，不进 query／body。
4. **幂等键按动作复用。** 同一 `actionKey` 的重试复用同一个 `Idempotency-Key`；确认动作作废时才 `resetIdempotencyKey`。
5. **过期上下文不覆盖新上下文。** `setProject` 递增 epoch 并清空上一工程的 revision 与幂等键；
   epoch 变化的响应 `stale: true` 且不写回上下文。
6. **来源上报不改任务状态。** `reportStatus` 不带 `If-Match`，服务端必须回 `changed_task_status: false`，否则本地报错。
7. **状态未知就是未知。** `connection` 初值 `unknown`，取得证据才转 `online`；已有数据时网络失败为 `degraded`，
   从未成功过才是 `offline`。`restore()` 恢复上下文但不会把状态恢复成 `online`。

## 验证

```sh
node --test internal/board/assets/utils/request-context.test.cjs
```
