# ROADMAP

maw 定位为本机多 Agent **任务中枢**。下列为已交付与规划能力。

## 已交付

- 蓝图拆题、持久 worktree、领取 / 提交 / 合并、写盘范围检查
- 全局状态库（`~/.maw`）与跨工程看板（`maw board` / SSE）
- 多 IDE 协议注入（Cursor / Qoder / Trae / CodeBuddy 等）与 hooks

## 进行中 / 已部分交付

### 跨工程 Agent 状态汇聚

- CLI：`maw status report`（也可 `POST http://127.0.0.1:7420/api/status`，仅 loopback）
- 增量：`GET /api/feed?since=`
- SSE：`/api/events` 除 `board-changed` 外推送 `event` 帧

### 桌面客户端（Tauri）

把 `maw board --serve` 同一套看板打成桌面壳，系统托盘做实时通知。见 `clients/desktop/`。

## 规划中

### 托盘通知细化

按事件类型（progress / blocked / merged）区分通知文案与声音；多显示器窗口记忆。

## 非目标（近期）

- 云端多机协作与账号体系
- 替代各 IDE 内置 Agent 运行时
