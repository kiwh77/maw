# Board Web（拓扑前端）

Board 拓扑页面的前端工程：Vite + TypeScript + [`@antv/x6`](https://x6.antv.antgroup.com/)。

- 入口：`src/main.ts`，导出 `mountBoard` 占位 API 与 X6 的 `Graph`。
- 产物：`internal/board/assets/dist/board-web.js`（IIFE，全局名 `MawBoard`）。
- 视觉 token 仍定义在 `internal/board/page.html` 的 `:root`，本包不写死颜色。
- 本包**不参与 Go 侧装配**：`PageHTML`／`serve.go` 的引用由 VN-707 接入，拓扑交互由 VN-708 实现。

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
