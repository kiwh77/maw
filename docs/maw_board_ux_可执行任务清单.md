# maw 看板 UX（依赖可读性 + 问题审查）可执行任务清单

**主分支：`main`**
**迭代：`IT-BUX-001`**；蓝图 v16（在 v15 末尾追加，无 `--reset`）。

版本：拆包稿 v1.0，2026-11-04。

> **状态（2026-11-04 收尾）：三题已全部 `plan apply` 并合入 `main`，本文件转为设计归档，不再是待办。**
>
> | 题 | 合入提交 |
> | --- | --- |
> | `BUX-BE-001` 运行问题聚合 | `64d2a04` |
> | `BUX-FE-001` 看板依赖可读性 | `3c1cc3d` |
> | `BUX-FE-002` 问题审查视图 | `912b2b0` |
>
> 实测补充：`BUX-FE-002` 落地时发现注册表里 `problems` 视图 id 已存在且旧 `renderProblems`
> 仍被 `page.test.cjs` 断言，因此**没有改注册表**（也不在写域），改为在 Vue 壳里让同一路由
> 走新的 `ProblemsView`；旧挂载层映射原样保留。

## 背景（分析结论）

1. **依赖可读性**：泳道卡片不含 deps；被依赖卡住的题与普通 backlog 无差别；拓扑强制先选工程；抽屉无上下游。数据（deps/status）快照里都有，纯前端可解。
2. **问题审查**：blocked/conflict 仅 risks 字符串；attempts≥2、Agent 停滞、question/blocker 事件（COL-MSG-001 铺路）无聚合承载面。需后端聚合一处 + 前端一屏审查。

## Lane 与题 ID

| Lane | 题 ID | 依赖 | 说明 |
|---|---|---|---|
| BE | `BUX-BE-001` | — | Snapshot.problems 问题聚合 |
| FE | `BUX-FE-001` | — | 卡片 deps chips/等待标记 + 抽屉依赖面板 + 全局拓扑 |
| FE | `BUX-FE-002` | `BUX-BE-001` `BUX-FE-001` | 问题审查视图 |

并行入口：`BUX-BE-001` ‖ `BUX-FE-001`；`BUX-FE-002` 在两题合入后领取。

## 写盘冲突说明

- 避开在途 COL 题：不改 `internal/board/serve.go`、`internal/i18n/*`、`internal/app/status_cmd.go`（COL-MSG-001）、`internal/db/tasks_archive.go`（COL-ARCH-001）。
- `internal/board/board.go`（Snapshot 结构）与 backlog 题 COL-ARCH-001 有重叠，靠本波高优先级先行降低撞车概率；如撞车按 maw 冲突流程处理。
- 两道 FE 题共享 `internal/board/web/**`，已用 deps 串行。

## 任务卡

### BUX-BE-001 运行问题聚合（Snapshot.problems）

- **Goal**：现状：问题散落在 blocked_reason/conflict/事件流/attempts，看板无法一屏审查。目标：`Snapshot` 新增 `problems` 字段，聚合六类：①blocked（原因+持续时长）②conflict ③attempts≥2（重试痕迹）④最近 kind=question/blocker 事件（COL-MSG-001 落地后自动可见，事件缺 kind 时兼容跳过）⑤Agent 停滞（working 但 last_seen 超过 30 分钟）⑥依赖等待（非 done 题引用了未完成 deps）。每条含 project_id/project_name/task_id/kind/severity/message/ts。
- **Surface**：`internal/board/problems.go`（新）、`internal/board/problems_test.go`（新）、`internal/board/board.go`（Snapshot 字段+Normalize）、`internal/app/helpers.go`（装配接线）、`internal/app/problems_test.go`（新）
- **Contract**：无问题时 `problems=[]`（非 null）；severity 排序稳定（blocked/conflict > 停滞/重试 > 提问/等待）；`WithIteration` 收窄后 problems 跟随该批任务重算。
- **Acceptance**：单测六类至少覆盖五类各一条；空库 problems=[]；JSON 序列化含 problems。
- **Verify**：`CGO_ENABLED=0 go test ./internal/board ./internal/app -count=1 -run 'Problem|Snapshot|Board'`
- **Not doing**：不改 serve.go 路由；不做问题详情 API；不落库新表；不做前端。

### BUX-FE-001 看板依赖可读性

- **Goal**：①泳道卡片为含 deps 的任务渲染 chips（上游 ID + 状态色，点击开抽屉）②deps 未满足的 backlog/ready 卡片显示「等待：<上游>(<状态>)」③TaskDrawer 增加依赖面板（上游/下游 + 状态，可点击跳转）④拓扑视图未选工程时按多工程 band 全局渲染，不再要求先选工程。
- **Surface**：`internal/board/web/**`；`internal/board/assets/dist/board-web.js`；`internal/board/web/README.md`
- **Acceptance**：有 deps 的卡片渲染 chips 且状态色正确；等待标记可区分 backlog 卡片；抽屉显示上下游且可跳转；全局拓扑可用；web 构建通过且 dist 已更新。
- **Verify**：`npm run build --prefix internal/board/web && node --test internal/board/page.test.cjs && CGO_ENABLED=0 go test ./internal/board -count=1`
- **Not doing**：不改后端 API；不改泳道状态分组；不拖拽编辑 deps；不引入第二套主题色。

### BUX-FE-002 问题审查视图

- **Goal**：新增「问题」视图消费 `snapshot.problems`：按 severity/kind 分组列表（阻塞/冲突/重试/提问/停滞/依赖等待），行点击开对应任务抽屉，工程筛选联动；空态说明「当前筛选范围内无运行问题」。
- **Surface**：`internal/board/web/**`；`internal/board/assets/dist/board-web.js`
- **Acceptance**：有 problems 时按组渲染且可跳抽屉；空态可读；构建通过。
- **Verify**：`npm run build --prefix internal/board/web && node --test internal/board/page.test.cjs`
- **Not doing**：不做问题处理动作（只读审查）；不改后端。

## 压入说明

- 蓝图在 v15 末尾追加 `[[iteration]] IT-BUX-001` + 三题 → `maw plan check` → `maw plan apply --yes`；优先级 1/2/3 使其成为 next 候选头部。
- 看板筛看本波：`?iteration=IT-BUX-001`；`maw iteration show IT-BUX-001`。
