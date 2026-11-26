# 直修交接（2026-11-02 16:40）

> BOSS 指示「直接修，不进 maw」。以下改动**已完成源码编辑，但尚未构建与运行测试**：
> 本轮执行环境的命令沙箱整段故障（`SandboxError 0xFFFFF826`，连 `echo` 都起不来），
> 无法 `npm build` / `go build` / `go test`。**结论：代码已改，验证未做。**

## 1. 改了哪些文件

| 缺陷 | 文件 | 改动 |
| --- | --- | --- |
| 快照空值 null | `internal/board/board.go` | `tasks/risks` 为空时补成 `[]`，不再编码成 `null` |
| `updated_at` 语义 | `internal/board/board.go`、`internal/app/helpers.go` | 新增 `generated_at`；`updated_at` 改为最近事件时间（数据水位），无事件时留空 |
| 顶栏「更新时间」 | `internal/board/web/src/stores/board.ts` | 无事件时显示「尚无事件 / No events yet」，不再拿请求时刻冒充 |
| 策略值显示「—」 | `internal/board/assets/shell/view-context.mjs` | 对齐 `managed_execution_limit → limit`、`node_capacity`、`max_parallel_reference` |
| 平台未声明却显示「关闭」 | `internal/board/assets/views/settings/settings-view.mjs` | `autoModeOf` 只认显式 `true`；`autoReasonOf` 缺失时给平台原因 |
| 死表单 / 点保存跳走 | 同上 + `VnextHost.vue` | 表单 `onsubmit="return false"`、`@submit.prevent`；无写入口时整体只读（`data-editable="0"`、按钮 disabled、说明原因）；输入框回填服务端值 |
| 观测四视图 no_project | `assets/views/{timeline,activity,sessions,analytics}/*-view.mjs` | 各加 `no_project` 分支与中英文案，不再落到 empty |
| 看板泳道漏状态 | `internal/board/web/src/views/BoardView.vue` | Queued 含 backlog、Done 含 cancelled；契约外状态单列 Other |
| 选「全部工程」被踢走 | `internal/board/web/src/stores/board.ts` | 清空工程保持当前视图，只有 topo 回落 board |
| 筛选不进 URL | `internal/board/assets/shell/router.mjs` | board 视图仍省略 `view`，但把 `agent/status/q` 写进查询串 |
| 抽屉不接管焦点 | `internal/board/web/src/components/TaskDrawer.vue` | 打开聚焦关闭键、Tab 环回、关闭还原焦点 |
| `/` 快捷键缺失 | `internal/board/web/src/components/AppChrome.vue` | 非输入态聚焦搜索；输入态与修饰键放行；抽屉打开时不抢焦点 |
| 时间轴工具栏无反应 | `VnextHost.vue` | `data-action` → 换窗口（shift/zoom）并带 `start/end` 重取 |
| 时间轴窗口标签无日期 | `assets/views/timeline/timeline-view.mjs` | 新增 `formatStamp`（带月日），24h 窗口不再显示成 16:18 – 16:18 |

新增/更新测试：

- `internal/board/assets/views/settings/settings.test.cjs`：3 条（平台缺失=未就绪、只读表单、可写恢复）
- `internal/board/assets/views/no-project.test.cjs`：6 条（四视图 no_project + unknown 不受影响 + 窗口标签带日期）
- `internal/board/snapshot_test.go`：`TestBuildSnapshotEmptyCollectionsEncodeAsArrays`
- `tests/e2e/api_e2e.py`：A-11 收紧为必须 `[]`；新增 A-41（`updated_at` 两次读取不变）

## 2. 必须补做的验证（沙箱恢复后按序执行）

```bash
# 1) 前端产物必须重建，否则页面还是旧 bundle（源码改动不会自动生效）
npm --prefix internal/board/web run build
# 若被 safe-delete shim 拦在清空 dist：
#   npx --prefix internal/board/web vite build --outDir /tmp/board-dist
#   cp /tmp/board-dist/board-web.js internal/board/assets/dist/board-web.js

# 2) 单元测试
node --test internal/board/assets/views/settings/settings.test.cjs
node --test internal/board/assets/views/no-project.test.cjs
node --test internal/board/assets/views/timeline/timeline.test.cjs   # 标签改动影响
CGO_ENABLED=0 go test ./internal/board ./internal/app -count=1
CGO_ENABLED=0 go build ./cmd/maw

# 3) 端到端（隔离副本，别打 7420）
mkdir -p /tmp/maw-e2e/global && sqlite3 ~/.maw/maw.db ".backup /tmp/maw-e2e/global/maw.db"
cd /tmp/maw-e2e && MAW_GLOBAL=/tmp/maw-e2e/global maw serve --addr 127.0.0.1:7421
python3 tests/e2e/api_e2e.py --base http://127.0.0.1:7421 --copy-db /tmp/maw-e2e/global/maw.db \
  --live-base http://127.0.0.1:7420 --live-db ~/.maw/maw.db --token "$(cat /tmp/maw-e2e/token.txt)"
E2E_BASE=http://127.0.0.1:7421 npx --prefix tests/browser playwright test specs/board-e2e.browser.spec.ts
E2E_BASE=http://127.0.0.1:7421 E2E_REVIEW_PID=931269ebc68abb88 \
  npx --prefix tests/browser playwright test specs/board-policy-review.browser.spec.ts
```

## 3. 已知风险

1. **dist 未重建**：`internal/board/assets/dist/board-web.js` 仍是旧包，页面行为不会变；静态导出断言只检查 `var MawBoard=`，所以 `go test ./internal/board` 可能照样绿——别把绿灯当验证。
2. **未编译**：Go 与 TS 均未过编译器；本轮改动含函数签名新增（`GeneratedAt`、`editable`），若有拼写/类型问题会在构建时暴露。
3. **并发编辑**：`VnextHost.vue` 本轮同时被另一个 Agent 改（工程项目移除），我的改动与其字段（`canRemove/pendingRemove`）共存；合并后需人工过一眼。
4. **maw 队列里的 8 张卡（E2E-101…108）仍在**：本轮已直修，若别的 Agent 再领会重复劳动。建议 `maw plan apply` 前把 E2E-* 从蓝图移除使其退役，或由 BOSS 决定保留哪几张。
5. **E2E-108 未修**：hub 是否承载 v1 写路由属于设计决策，不在直修范围。
