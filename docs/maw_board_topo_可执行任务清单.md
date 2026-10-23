# Board 拓扑工程化（Vite + X6）可执行任务清单

**主分支：`main`**

版本：拆包稿 v1.0，2026-10-23。状态：已纳入 maw 增量压入（蓝图 v5：`VN-706`–`VN-709`）。

依据：Board 拓扑依赖难读 → 按波次自由画布；用户确认走前端工程化并安装 `@antv/x6`。与 [maw vNext 清单](maw_vnext_可执行任务清单.md) 交叉分析后插入 **FE-SHELL 续链**，不重置既有队列。

## 1. 与现有队列交叉影响

### 1.1 结论（先看）

| 判断 | 说明 |
|---|---|
| **不改 VN-701 目标** | VN-701 `not_doing` 含「不换框架」；仍只做保行为资源拆分。工程化另开 `VN-706+`。 |
| **硬前置** | `VN-706`→依赖 `VN-701`；挂载/改 legacy 拓扑依赖 `VN-701R`（先修 `t` 遮蔽）。 |
| **同盘串行** | `VN-707` 与 VN-701／VN-955 共享 `board.go`／`serve.go`／`page.html`；靠 deps + 更优 priority，在 701 之后、955 之前落地 embed。 |
| **与 VN-702 正交** | 702 写 `assets/styles|components`；本波写 `web/**` + `assets/dist/**`，不抢 token 组件盘。 |
| **与 VN-981** | 981「不引入生产框架」约束的是 **tests/browser**；X6 进 Board 生产资源。981 仍测真实 embed 产物，dist 合入后基座自动覆盖。 |
| **与 FE-PAGES / 契约** | 不碰 `assets/views/**`；不改调度 API。视觉增量写 BOARD_DESIGN（VN-709），不重开 VN-021 冻结值（颜色／字级不变）。 |
| **何时 ready** | 整条链 backlog，直到 `VN-701`（及挂载所需的 `VN-701R`）done；**不与当前 ENGINE／RUNNER／DELIVERY 抢盘**。 |

### 1.2 写盘冲突矩阵

| 现有题 | 状态（压入时） | 重叠路径 | 处理 |
|---|---|---|---|
| VN-021／022 | ready／backlog | 仅 read 本波文档 | 无写冲突；拓扑文案不改冻结 token |
| VN-701 | backlog | `page.html` `board.go` `serve.go` `page.test` `legacy/**` | **VN-707／708 必须 deps VN-701**；706 仅新盘 `web/**`+`dist/**`，deps VN-701 保证拆分完成后再引入构建树 |
| VN-701R | backlog | `legacy/**` | **VN-708 deps VN-701R**，避免与 i18n 热修同改 `board-app.js` |
| VN-702–705 | backlog | styles/components/api/shell | 无重叠；702 勿假定「永远零 npm」——README／壳注释由 706 说明 dist 构建 |
| VN-721–744 | backlog | `assets/views/**` | 无重叠 |
| VN-955 | backlog | `serve.go` 等 | **蓝图为 VN-955 增加 deps `VN-707`**，避免覆盖 `/assets/*` 静态挂载 |
| VN-981 | backlog | `tests/browser/**` | 无重叠；可选后续加 topo smoke（非本波） |

### 1.3 调度示意

```text
VN-021 ‖ …其它 Lane…
    └─ VN-701（拆分，不换框架）
          ├─ VN-701R（legacy t 热修）──┐
          └─ VN-706（Vite+X6 脚手架）─┼─ VN-707（embed.FS + /assets）─ VN-708（X6 波次拓扑）─ VN-709（设计文档）
                                       │
                              VN-955 deps 增加 VN-707
```

### 1.4 Lane 表

| Lane | 题 ID | Surface 根 |
|---|---|---|
| FE-SHELL（续） | VN-706 → VN-707 → VN-708 → VN-709 | `internal/board/web/**`、`assets/dist/**`；707 起含 `board.go`／`serve.go`／`page.html` |

---

## 2. 任务卡

### VN-706 建立 Board 前端工程化与 X6 依赖

- **Goal**：在 `internal/board/web` 建立 Vite+TypeScript 工程，安装 `@antv/x6`，构建产物落到 `assets/dist`，仓库可提交 dist。
- **Non-goals**：不改 `serve.go`／`page.html` 装配；不实现拓扑业务；不改 legacy 看板；不换 Vue/React 整页框架。
- **Depends**：`VN-701`
- **Surface**：`internal/board/web/**`；`internal/board/assets/dist/**`
- **Contract**：沿用 VN-021 视觉 token；构建为库/入口 bundle，供后续 embed。
- **Steps**：
  1. `package.json`／`vite.config.ts`／tsconfig；依赖 `@antv/x6`。
  2. 最小 `src/main.ts` 导出挂载 API 占位；`npm run build` → `assets/dist`。
  3. 写清 rebuild 说明（web/README）；构建脚本放在 web 包内，避免无主 Makefile 时无入口。
- **Verify**：
  ```sh
  npm ci --prefix internal/board/web
  npm run build --prefix internal/board/web
  test -f internal/board/assets/dist/board-web.js
  ```
- **Deliverables**：可重复构建的 web 包与已生成 dist。
- **Lane**：FE-SHELL；**maw**：priority=14 · merge_order=14

### VN-707 嵌入 dist 并挂载 /assets 静态资源

- **Goal**：Go embed `assets/dist`，HTTP 提供 `/assets/*`，`PageHTML`／静态 `Write` 引用构建产物，旧看板脚本仍可用。
- **Non-goals**：不实现 X6 拓扑交互；不改业务 API；不扩大到 `internal/app/**`（留给 VN-955）。
- **Depends**：`VN-701`、`VN-706`
- **Surface**：`internal/board/board.go`；`internal/board/serve.go`；`internal/board/page.html`；`internal/board/page.test.cjs`；`internal/board/snapshot_test.go`
- **Contract**：旧 `/` HTML 与 `/api/board` 兼容；静态导出目录含 dist 文件且相对路径可离线打开。
- **Steps**：
  1. `embed.FS` 发布 dist；`serve.go` 增加 `/assets/` 文件服务。
  2. `page.html` 增加 script/link 引用；保留 legacy 内联直至 VN-708 切换拓扑。
  3. 更新 PageHTML／snapshot 测试断言。
- **Verify**：
  ```sh
  node --test internal/board/page.test.cjs
  CGO_ENABLED=0 go test ./internal/board -count=1
  ```
- **Deliverables**：embed 路径与静态导出一致。
- **Lane**：FE-SHELL；**maw**：priority=15 · merge_order=15

### VN-708 用 X6 实现按波次依赖拓扑画布

- **Goal**：拓扑视图按依赖深度显示「波次 N」列，ER 正交边、平移缩放、适应画布／重布局、悬停高亮相邻依赖路线；节点点击仍开抽屉。
- **Non-goals**：不拖拽改 deps；不新增任务 `stage` 字段；不重写看板泳道；不引入第二套主题色。
- **Depends**：`VN-701R`、`VN-707`
- **Surface**：`internal/board/web/src/**`；`internal/board/assets/dist/**`；`internal/board/assets/legacy/board-app.js`；`internal/board/assets/legacy/board.css`
- **Contract**：波次 = 依赖深度；筛选外依赖 stub＋虚线；F06／BOARD 视图语义保持。
- **Steps**：
  1. 移植 niuma `TaskTopologyGraph` 布局语义为 TS（无 Vue）。
  2. `#topology` 挂载 Graph；工具条；主题适配深浅色 CSS 变量。
  3. legacy `renderTopology` 改为调用 web 挂载 API；保留空态／多工程 band。
- **Verify**：
  ```sh
  npm run build --prefix internal/board/web
  node --check internal/board/assets/legacy/board-app.js
  node --test internal/board/page.test.cjs
  CGO_ENABLED=0 go test ./internal/board -count=1
  ```
- **Deliverables**：可交互波次拓扑；布局纯函数可单测（web 内）。
- **Lane**：FE-SHELL；**maw**：priority=16 · merge_order=16

### VN-709 更新 Board 设计说明中的拓扑／工程化约定

- **Goal**：人读文档写明波次列、X6 画布、构建与 embed 约定。
- **Non-goals**：不改契约冻结色值；不改代码。
- **Depends**：`VN-708`
- **Surface**：`docs/zh/BOARD_DESIGN.md`；`docs/BOARD_DESIGN.md`；`docs/maw_board_topo_可执行任务清单.md`
- **Steps**：更新「视图」段；英文镜像补一句；本清单标记完成状态。
- **Verify**：文档含「波次」「X6」或「Vite」及 `/assets` 说明；中英文视图段一致。
- **Deliverables**：设计文档与实现一致。
- **Lane**：FE-SHELL；**maw**：priority=17 · merge_order=17

---

## 3. 压入说明

- 增量 `maw plan apply`（**无 `--reset`**）；蓝图 version **5**。
- 同步修改蓝图中 **VN-955** 的 `deps`，追加 `VN-707`。
- 本波 **不** 把实现提前在主干手工落地；由 worker `maw next` 领取 VN-706+（ready 取决于 VN-701／701R）。
