# 平台支持矩阵与发布检查（VN-985）

日期：2026-10-24　基线：`58bf3d8`　机检：`CGO_ENABLED=0 go test ./tests/platform -count=1`

## 1. 一句话结论

**受管执行只在 macOS 上被声称支持**；其余平台「能编译、能人工用、但不承诺受管」。
本文是这句话的唯一出处，`tests/platform` 会校验它没有被悄悄改宽。

## 2. 支持矩阵

| 平台 | 架构 | 构建 | 受管执行 | 人工接入 | 观测／回顾 | 依据 |
| --- | --- | --- | --- | --- | --- | --- |
| darwin | amd64 | 交叉编译 + 原生构建 | 支持 | 支持 | 支持 | 原生进程实测：启动、停止、回收、worktree 复用 |
| darwin | arm64 | 交叉编译 + 原生构建 | 支持 | 支持 | 支持 | 同上（本机为 arm64） |
| linux | amd64 | 交叉编译 | 不支持 | 支持 | 支持 | 无原生进程实测记录 |
| linux | arm64 | 交叉编译 | 不支持 | 支持 | 支持 | 无原生进程实测记录 |
| windows | amd64 | 交叉编译 | 不支持 | 支持 | 支持 | 无原生进程实测记录 |
| windows | arm64 | 交叉编译 | 不支持 | 支持 | 支持 | 无原生进程实测记录 |

读法：

- **交叉编译 ≠ 支持受管**。能编出来只说明代码没有平台专属语法问题，
  受管执行还要能起子进程、能读回进程状态、能在退出后回收目录——这些只在 macOS 实测过。
- 「人工接入」指 Agent 自己跑 `maw next`／`maw submit`，不依赖中枢起进程，
  因此在任何能跑 CLI 的平台上都成立。
- 非 macOS 上 `AssertPlatform()` 会明确报错，扩展注册表里 `dispatch.auto`
  的平台白名单也只有 `darwin`；这不是遗漏，是证据不足时的正确取值。

## 3. 跨编译与原生实测的边界

| 检查 | 覆盖什么 | 不覆盖什么 |
| --- | --- | --- |
| `go build GOOS=…`（`tests/platform` 交叉编译用例） | 六个发布目标都能编出非空二进制 | 能否起进程、能否停进程、目录回收 |
| 原生进程实测（仅 macOS） | 启动、停止、回收、worktree 复用、卡死超时 | 其它平台的一切 |
| `tests/managed`、`tests/resilience` | 受管闭环与门禁行为（在跑测试的平台上） | 跨平台差异 |

因此 CI 里「交叉编译绿」不能当作「平台支持」的证据，两条结论分开写。

## 4. CI 覆盖

`.github/workflows/ci.yml` 的任务划分：

| 任务 | 跑什么 | 为什么单独一件 |
| --- | --- | --- |
| `go-core` | `CGO_ENABLED=0 go test ./...` + 覆盖率下限 | 主干正确性；覆盖率下限只许上调 |
| `go-build` | `CGO_ENABLED=0 go build ./cmd/maw` | 编译失败要单独可见，不能混在测试里 |
| `frontend-unit` | `node --test internal/board/page.test.cjs` + 各视图／组件的 `*.test.cjs` | 页面装配与视图单测，秒级反馈 |
| `frontend-browser` | `npm --prefix tests/browser test` | 真实浏览器行为；装 chromium 后跑 |
| `platform-matrix` | `CGO_ENABLED=0 go test ./tests/platform -count=1` | 交叉编译与平台声明 |
| `static-export` | `go test ./internal/board -run 'Assets\|Static\|Snapshot\|PageHTML\|Runtime'` | 导出页只读、资源随包发布、`/api/runtime` 如实报 `static_export_managed=false` |
| `desktop-prereq` | 桌面壳前置条件（Cargo／capabilities／桥接源） | 不跑完整 tauri 构建，但缺件要早失败 |

覆盖率下限保持现状（`>= 35`），**不因为新增前端与平台用例而下调**：
下调会把已有的失败藏起来。

### 已知红（不掩盖）

`tests/browser/specs/vn-981.browser.spec.ts` 的「keyboard focus across search and views」
在干净基线上就失败：未选工程时 `#view-topo` 是 `disabled`，按 Enter 不会有反应。
根因在用例期望与既有行为不一致，修法是该用例在未选工程时断言「禁用且有说明」。
该文件不在本卡写盘范围，见 `docs/verification/vnext/frontend/VN-984-visual-consistency.zh-CN.md` §4.4。
CI **不加** `--grep-invert` 绕过它——绕过等于把一条真实失败藏进绿灯里。

## 5. 发布前手工确认项

1. `npm --prefix internal/board/web run build` 重建 `assets/dist/board-web.js`，
   否则发布页面仍是旧注册表（当前基线处在这个状态，见 VN-984 报告 §4.3）。
2. macOS 上跑一次受管闭环（`maw next` → `maw submit` → `maw merge --yes`），
   确认进程启动、停止与 worktree 回收都正常。
3. 检查 `clients/desktop/src-tauri/capabilities/default.json` 的权限没有回涨
   （只留实际用到的项）。

## 6. 本卡验收执行记录

在 macOS（arm64）上按卡片 Verify 逐条跑，结果与预期一致的那几条连同证据一起记在这里：

| 检查 | 结果 |
| --- | --- |
| `CGO_ENABLED=0 go test ./...` | 通过（35 个包 ok，0 红） |
| `node --test internal/board/page.test.cjs` | 通过（31/31） |
| `npm --prefix tests/browser test` | 107 通过 / 1 失败，唯一失败是 §4「已知红」的 `vn-981` |
| `CGO_ENABLED=0 go build -o /tmp/maw-vnext-release-check ./cmd/maw` | 通过（19.5 MB） |
| `CGO_ENABLED=0 go test ./tests/platform -count=1` | 通过（含 6 个交叉编译子用例，2.6 s） |

浏览器套件那 1 条红不是本卡引入的：在干净基线 `d71c4b1` 上 stash 掉全部改动后同样失败。
它所在文件不在本卡写盘范围，所以这里只登记、不改写；CI 也不给它开后门。

