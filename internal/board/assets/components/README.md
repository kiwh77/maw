# Workbench 共享主题组件（vnext / UI）

本目录是 VN-702 建立的共享视觉层：**token 是唯一视觉源**，组件只消费 token，不再出现色值字面量。
契约来源：`docs/contracts/vnext/ui/WORKBENCH_UI_CONTRACT.md`（§2 颜色、§3 形状与字级、§4 状态词汇、§6–§11 组件、§13 断点）。

## 目录

| 文件 | 职责 |
| --- | --- |
| `../styles/tokens.css` | 颜色、字级、间距、密度、动效、断点、字体族。深色默认，`data-theme="light"` 覆盖，未指定时跟随系统。 |
| `badge.css` | 11 个固定状态徽标（`.badge--<state>`）与来源标记。颜色只表达状态，文本标签必须同时出现。 |
| `table.css` | 表格：表头 12px/muted、单元格 13px/text、行高 ≥36px、无斑马纹、内容区横向滚动。 |
| `form.css` | 表单：标签 12px、控件 32px/6px、主次操作、只读态用 muted、提交中不重复提交。 |
| `empty.css` | 空态：`1px dashed --line`、`24px 16px`、最多一句说明 + 一个可选主操作；错误态用 `--danger`。 |
| `drawer.css` | 抽屉：`min(500px, 94vw)`、`--panel` 底、左边界 `--line-strong`、遮罩 `--scrim`、220ms 位移。 |
| `components.css` | 入口层：`@import` 各组件 + 基础层（字体、`:focus-visible`、`prefers-reduced-motion`、断点）。 |
| `../legacy/style.css` | 视图样式入口：只装配 `tokens.css` + `components.css`，不写规则。 |

## 不变量（由 `components.test.cjs` 强制）

1. `tokens.css` 的 15 个契约颜色 token 与契约表**逐条等值**（深色/浅色两套）。
2. 组件层零色值字面量：不得出现 `#hex` / `rgb()` / `hsl()`，颜色只能 `var(--token)`。
3. 状态徽标覆盖契约 §4 的 11 个状态键，且每个状态的 `color` 必须等于契约规定的 token 语义。
4. 徽标不得把文本压成 0 或隐藏——颜色不是唯一信息载体。
5. `:focus-visible` 固定 `2px solid var(--accent)` + `outline-offset: 3px`；不得无替代地 `outline: none`。
6. `prefers-reduced-motion: reduce` 下 `animation` 与 `transition` 全部 `none !important`。
7. 断点只允许 `1500 / 1100 / 700`，不得新增。
8. `components.css` 的 `@import` 与实际组件文件一一对应；`legacy/style.css` 只装配不写规则。

## 验证

```sh
node --test internal/board/assets/components/components.test.cjs
npm --prefix tests/browser test -- --grep 'VN-702'
```

浏览器用例把 tokens 与组件原样注入独立页面，覆盖：深浅主题换色、系统偏好跟随、11 个徽标文本、
键盘焦点环、抽屉 500/94vw 两档宽度、减弱动画、700px 无页面级横向滚动、长中文不撑破 32px 控件。

## 与既有 Board 的关系（重要）

- `legacy/board.css` 是既有 Board 的高密度实现，由 `internal/board/board.go` 以 `go:embed` 注入 `{{MAW_STYLES}}`（颜色／字体族）。
- **生产接线**：`internal/board/web/src/main.ts` 的 `injectStyles` 按
  `tokens.css → components.css → shell.css → 视图 CSS` 注入 `#maw-vnext-styles`。
  `legacy/style.css` 仍作测试／手工装配入口，顺序与上一致。
- 不得保留互相漂移的两套 token：`board.css` 的 `:root` 颜色须与 `tokens.css` 同值；
  字级／密度／动效以 `tokens.css` 为唯一源（契约 §14）。
- 抽屉的 `role="dialog"`、`aria-modal`、背景 `inert` 与焦点返回属于页面侧行为，本包只冻结外观与动效。
