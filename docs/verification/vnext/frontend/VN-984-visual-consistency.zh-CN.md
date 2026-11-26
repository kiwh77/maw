# VN-984 全页面视觉与交互一致性验收

日期：2026-10-24　车道：UI-C　任务：VN-984　基线：`58bf3d8`（已合入 VN-705）

## 1. 结论

**10 条审计用例全绿**，覆盖桌面／窄屏、深／浅主题、中英、长中文、四类非成功状态、
断线与乱序响应、键盘与焦点、减弱动画，以及「页面不另造全局样式」。截图与断言同源，
先断言数据与投影状态、再截图。

**同时登记 4 条漂移，均不在本卡写盘范围**，见第 4 节；其中第一条需要产品决策。

## 2. 怎么跑

```sh
npm --prefix tests/browser test -- --grep 'VN-984'
```

- 用例：`tests/browser/audit/workbench-audit.browser.spec.ts`
- 基座：`tests/browser/audit/harness.ts`（按需把 `internal/board/assets` 下的源模块
  和全部样式表从磁盘喂给浏览器，页面仍是生产 `page.html`）
- 截图：`docs/verification/vnext/frontend/screenshots/VN-984-0*.png`

为什么不打包后再审：`assets/dist/board-web.js` 是 `internal/board/web` 的产物，
源模块改完未重建时会审到旧代码（本次基线正好处在这种状态，见 4.3）。

## 3. 覆盖矩阵

| 维度 | 取值 | 落点 |
| --- | --- | --- |
| 宽度 | 1440／1280／1024／390 | 01、03、04 |
| 主题 | 深（`data-theme=dark`）、浅（`light`）、系统默认 | 01、02 |
| 语言 | zh、en | 01、02 |
| 文本 | 常长、超长中文（工程名／标题／负责人各重复 6 次） | 04 |
| 状态 | loading、empty、error(unavailable)、unknown | 05 |
| 网络 | 断线（源抛错）、乱序（慢请求后到） | 06、07 |
| 交互 | Enter 激活导航、Escape 关闭详情、焦点归还 | 08 |
| 可访问性 | 徽标文本承载状态、焦点轮廓非 `none`、reduced-motion 关动画 | 01、08、09 |
| 样式纪律 | 无硬编码色值、`!important` 白名单、视图无全局选择器 | 10 |

截图前都先断言数据：行数、页面标题、`data-status`、徽标文本；不存在「先截图再解释」。

## 4. 发现的漂移（按严重度排序，均需另开卡）

### 4.1 ≤700px 隐藏侧栏，工作台导航随之不可达（需决策）

契约 §13 明确要求「`≤700px` 隐藏侧栏，工程改用筛选器选择」，实现照做
（`internal/board/assets/legacy/board.css` 的 `.sidebar{display:none}`）。
副作用是：**导航整块在侧栏里，窄屏上无法切换视图**——手机上只能停在当前页。
契约只交代了工程怎么选，没交代视图怎么换。要么给导航一个窄屏入口（顶部下拉或横向导航条），
要么修契约。涉及 `internal/board/assets/legacy/board.css` 与页面骨架，需单独立卡。

本卡不擅自改动：那条规则是契约条款，不是缺陷。

### 4.2 `components.css` 自带基础层，与页面内联样式重复

`internal/board/assets/components/components.css` 第 21–40 行重新声明了
`*, *::before, *::after { box-sizing }`、`html { color-scheme: dark }`、
`body { margin/background/color/font }`、`h1/h2` 字级。
契约 §1 要求「不引入第二套 token 体系」，颜色走 token 没问题，但：

- `html { color-scheme: dark }` 是写死的深色，浅色主题下 `--bg` 是纸白而
  `color-scheme` 仍是 dark，滚动条与原生控件会呈深色；
- 同一批属性在 `page.html` 的内联样式里已有一份，两处改动不同步就会漂移。

审计用例把「全局选择器」的合法范围钉在 `components/` 一处（视图层出现即失败），
因此这条不会再扩大；是否把基础层收回 `page.html` 需要单独评估影响面。

### 4.3 `assets/dist/board-web.js` 未随 VN-705 重建

发布页面加载的是打包产物，而 VN-705 的写盘范围不含 `internal/board/assets/dist/**`
（VN-704 当时是含的）。结果：源码里已有 13 个视图，发布页面上仍是旧的 7 个。
重建只需 `npm --prefix internal/board/web run build`，但该路径需要一张卡授权。

### 4.4 `tests/browser/specs/vn-981.browser.spec.ts` 键盘用例在干净基线上红

192 行那条断言 `#view-topo` 按 Enter 后 `aria-pressed=true`，实际该按钮在未选工程时
是 `disabled`（旧看板的既有行为），按 Enter 不会有反应。已在 `d71c4b1` 上复现，
与 VN-705／VN-984 无关；属既有用例与既有行为的冲突，建议由该 spec 的归属方修断言
（未选工程时断言「禁用且有说明」）。

### 4.5 英文界面下旧看板统计条仍是中文

`?lang=en` 直达时，`vnext` 视图是英文，但顶部统计条（`#stats`）与 Agents 栏仍是中文：
挂载层解析出语言后只把它交给 vnext 视图，旧看板的 `setLang` 要等用户手动切换才被调用。
契约 §12 要求「同一页面只出现一种语言」。修法是在挂载时把语言同步给旧看板
（`internal/board/assets/shell/board-shell.mjs`，VN-705 的写盘范围）。
审计用例 2 里有一条断言把现状钉住（断言 `#stats` 仍含中文），修好后它会转红，
提醒同步更新本节。

### 4.5 英文界面下旧看板统计条仍是中文

`?lang=en` 直达时，`vnext` 视图是英文，但顶部统计条（`#stats`）与 Agents 栏仍是中文：
挂载层解析出语言后只把它交给 vnext 视图，旧看板的 `setLang` 要等用户手动切换才被调用。
契约 §12 要求「同一页面只出现一种语言」。修法是在挂载时把语言同步给旧看板
（`internal/board/assets/shell/board-shell.mjs`，VN-705 的写盘范围）。
审计用例 2 里有一条断言把现状钉住（断言 `#stats` 仍含中文），修好后它会转红，
提醒同步更新本节。

## 5. 未覆盖与后续

- 真实 5 个工程的全量数据、真实 SSE 断线重连后的补拉：属集成验收（VN-985）。
- 触摸目标 ≥32px 的实测：本卡用 390px 视口验证了不溢出与容器滚动，未逐控件量测。
- 深浅主题的像素级对比：本卡断言 token 生效（背景不是深色底）与结构不变，
  不做图像差分——契约不要求像素基线。
