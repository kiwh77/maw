# maw vNext 拆包静态审查

生成日期：2026-10-23。主分支：`main`。仅规划，不代表任何任务已实施。

| 检查 | 结果 |
|---|---|
| 任务／Lane | 81／15 |
| 重复 ID／缺失依赖／依赖环 | 0／0／0 |
| 写盘交叠／未串行交叠 | 37／0 |
| 实现归属覆盖 | F01–F48，48／48 |
| 门槛归属覆盖 | G01–G08 ＋ UI-C，9／9 |
| 首闭环任务闭包 | 56，无 O2／X3 前置 |
| 首波独立 Lane／规划 worker | 3／3；未启动 |

## Surface 交叠的显式顺序

路径检查覆盖明确文件及本清单使用的目录 `/**`。先后依据完整依赖图；仅相同 priority 或口头约定不视为已串行。

| 先题 | 后题 | 交叠路径（列举） |
|---|---|---|
| VN-101 | VN-102 | `internal/db/**` |
| VN-101 | VN-103 | `internal/db/**` |
| VN-101 | VN-104 | `internal/db/**` |
| VN-101 | VN-105 | `internal/db/**` |
| VN-101 | VN-106 | `internal/db/**` |
| VN-101 | VN-107 | `internal/db/**` |
| VN-102 | VN-103 | `internal/db/**` |
| VN-102 | VN-104 | `internal/db/**` |
| VN-102 | VN-105 | `internal/db/**` |
| VN-102 | VN-106 | `internal/db/**` |
| VN-102 | VN-107 | `internal/db/**` |
| VN-103 | VN-104 | `internal/db/**` |
| VN-103 | VN-105 | `internal/db/**` |
| VN-103 | VN-106 | `internal/db/**` |
| VN-103 | VN-107 | `internal/db/**` |
| VN-104 | VN-105 | `internal/db/**` |
| VN-104 | VN-106 | `internal/db/**` |
| VN-104 | VN-107 | `internal/db/**` |
| VN-105 | VN-106 | `internal/db/**` |
| VN-105 | VN-107 | `internal/db/**` |
| VN-106 | VN-107 | `internal/db/**` |
| VN-301 | VN-308 | `docs/evidence/codebuddy/**` |
| VN-303 | VN-304 | `internal/adapters/codebuddy/**` |
| VN-303 | VN-308 | `internal/adapters/codebuddy/**` |
| VN-304 | VN-308 | `internal/adapters/codebuddy/**` |
| VN-402 | VN-403 | `internal/runner/process/**` |
| VN-501 | VN-504 | `internal/workspace/**`、`internal/workspace/nongit/**` |
| VN-503 | VN-505 | `internal/verification/**`、`internal/verification/nongit/**` |
| VN-701 | VN-702 | `internal/board/assets/legacy/**`、`internal/board/assets/legacy/style.css` |
| VN-701 | VN-704 | `internal/board/assets/bootstrap/**`、`internal/board/page.html`、`internal/board/page.test.cjs` |
| VN-701 | VN-705 | `internal/board/assets/bootstrap/**`、`internal/board/page.html`、`internal/board/page.test.cjs` |
| VN-701 | VN-955 | `internal/board/serve.go` |
| VN-704 | VN-705 | `internal/board/assets/bootstrap/**`、`internal/board/assets/i18n/**`、`internal/board/assets/shell/**`、`internal/board/page.html`、`internal/board/page.test.cjs` |
| VN-952 | VN-953 | `internal/app/worker_cmd.go` |
| VN-954 | VN-956 | `cmd/maw/main.go`、`internal/app/managed_cmd.go`、`internal/app/runtime.go` |
| VN-955 | VN-956 | `internal/app/board_cmd.go` |
| VN-981 | VN-984 | `tests/browser/**`、`tests/browser/audit/**` |

## 验证边界

本轮校验的是任务清单结构与规划冲突，不是产品测试。没有执行卡内 Verify，也没有迁移、启动 Runner、调用厂商模型或创建蓝图。

执行前仍需固定当前未提交工作区基线、确认测试基座的运行前提；真实厂商能力、macOS 停止证据及跨平台支持必须由对应任务交付。

全仓 `git diff --check` 发现既有 `README.md:82` 行尾空格；该文件本轮未修改。本轮新增三份规划文件的行尾空格与文件结束换行检查通过。
