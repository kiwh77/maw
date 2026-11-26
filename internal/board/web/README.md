# Board Web（拓扑前端）

Board 拓扑页面的前端工程：Vite + TypeScript + [`@antv/x6`](https://x6.antv.antgroup.com/)。

- 入口：`src/main.ts`，导出 `mountBoard` 占位 API 与 X6 的 `Graph`。
- 产物：`internal/board/assets/dist/board-web.js`（IIFE，全局名 `MawBoard`）。
- 视觉 token 来自 `assets/styles/tokens.css`，由 `src/main.ts` `injectStyles` 打进产物；
  本包不写死颜色。Go 侧 `{{MAW_STYLES}}` 仍嵌 `legacy/board.css`（高密度壳）。
- `PageHTML`／`serve.go` 经 `/assets/board-web.js` 加载本产物。

## 重建

```sh
npm ci --prefix internal/board/web          # 首次或依赖变更后
npm run build --prefix internal/board/web   # tsc --noEmit && vite build
test -f internal/board/assets/dist/board-web.js
```

等价于进入包目录执行：

```sh
cd internal/board/web
npm ci
npm run build
```

开发期持续重建用 `npm run watch`（`vite build --watch`），只做类型检查用 `npm run typecheck`。

## 产物入库说明

根 `.gitignore` 里的 `dist/` 会匹配到 `internal/board/assets/dist/`，因此产物需要显式加入版本库：

```sh
git add -f internal/board/assets/dist
```

`node_modules/` 由本目录的 `.gitignore` 忽略，不入库；依赖版本以 `package-lock.json` 为准（`npm ci` 可复现）。
构建产物不含 sourcemap，`vite.config.ts` 通过 `build.emptyOutDir` 清空旧产物。

## 目录

```text
internal/board/web
├── package.json        # scripts: build / typecheck / watch
├── package-lock.json   # 锁定 @antv/x6、tslib、vite、typescript
├── tsconfig.json       # strict，仅做类型检查（noEmit）
├── vite.config.ts      # lib/IIFE → ../assets/dist/board-web.js
└── src/main.ts         # 挂载 API 占位 + X6 运行时导出
```

## 注意

`@antv/x6` 的 ESM 产物引用了 `tslib` 但未在其 `dependencies` 中声明，因此在本包显式依赖 `tslib`，否则构建会报 `Rolldown failed to resolve import "tslib"`。

## 依赖可读性（BUX-FE-001）

- **卡片依赖 chips**（`src/views/BoardView.vue`）：含 deps 的卡片在标题下渲染上游任务
  chips，底色按上游当前状态着色（done/cancelled 完成色、claimed/in_progress 等活跃色、
  blocked/conflict 受阻色、backlog/ready 等待色）；点击 chip 直接打开上游任务抽屉。
- **等待标记**：deps 未全部完成的 backlog/ready 卡片显示
  「等待：<上游ID>(<状态>)」（英文 `Waiting: ...`），纯前端按快照计算。
- **抽屉依赖面板**（`src/components/TaskDrawer.vue`）：新增「依赖」区块，上游（本卡
  deps）与下游（快照中 deps 引用本卡的任务）两个列表，每行 ID／状态／标题，行点击
  跳转对应任务抽屉。
- **全局拓扑**（`src/views/TopoView.vue`）：未选工程时用全部快照任务按多工程 band
  渲染全局拓扑，视图切换按钮与路由不再禁用拓扑。

共享规则在 `src/lib/deps.ts`：`taskDeps` / `findUpstream` / `depTone` /
`depRows` / `unsatisfiedDeps` 都是纯函数，看板与抽屉复用同一份状态语义，
颜色只消费既有 token（chips 底色由状态色低透明度派生，不引入第二套主题色）。

## Receiving yard (RF-002)

The Board view switch includes a live Three.js receiving yard (`src/factory`).
It consumes the same filtered snapshot/search as Board and topology and opens the
same task drawer. No separate port, CDN, external model or simulation is required.
An existing project is a truck; task count chooses its size. This is explicitly a
project visualization until the backend supplies a distinct ware identity.

`done` alone maps to warehouse storage. Review/merging remain at inspection;
cancelled, conflict/blocked and unknown each have distinct cargo states. A filtered
subset must never be interpreted as whole-project completion. The canvas shows at
most six projects and 72 boxes each; the keyboard-accessible list retains every
matching task. Select a project for a closer view. Workers represent actual task
owners; idle registered agents have no invented assignment. Motion is decorative
and never writes to task state. The canvas stops rendering when its view is hidden.
WebGL failures retain a live task list. System reduced-motion disables idle motions.

### Yard motion (RF-003)

- Trucks enter along the road with the nose leading, then reverse so the tail stops
  over the unloading dock; waiting cargo is stacked in truck space, so it arrives and
  parks with the vehicle instead of staying behind.
- An owner holding executing work gets one carrier: empty walk to the tail, load,
  carry to the bench, a visible working phase, then the walk back. Other states wait
  at the dock, the inspection station or the blocked bay.
- Nameplates render the complete Agent id and are re-projected every frame, so they
  follow both the worker and the camera.
- Pose and carry helpers live in `src/factory/model.mjs` and are pure functions of
  elapsed time and progress, so polling never restarts a walk-in or a carry cycle:
  `update()` only rebuilds when `sceneSignature()` changes, and rebuilds keep truck
  progress, worker position and cycle clock.

Checks: `node --test src/factory/model.test.cjs`, `node --test src/factory/yard.test.cjs`,
`npm run build`, `node --test ../page.test.cjs`, `go test ./internal/board` from
repository root.
