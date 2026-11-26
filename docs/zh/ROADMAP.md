# ROADMAP

> 语言：[English](../ROADMAP.md) | 简体中文

maw 定位为本机多 Agent **任务中枢**。下列为已交付与规划能力。

## 已交付

- 蓝图拆题、持久 worktree、领取 / 提交 / 合并、写盘范围检查
- 全局状态库（`~/.maw`）与跨工程看板（`maw board` / SSE）
- 多 IDE 协议注入（Cursor / Qoder / Trae / CodeBuddy 等）与 hooks
- **中英双语**：英文主文档 + 中文镜像；看板 UI 可切换语言；CLI / 终端看板 / `maw init` 协议 / skills 走 `MAW_LANG`（默认 en）
- 跨工程 Agent 状态汇聚：`maw status report` / `POST /api/status`（仅 loopback）、`GET /api/feed?since=`、SSE `/api/events`（`event` + `board-changed`）
- **桌面客户端（Tauri，macOS）**：与 `maw board --serve` 同一套看板打成桌面壳，系统托盘实时通知（`clients/desktop/`）。不宣称 Linux／Windows 原生构建。

## 规划中

### 托盘通知细化

按事件类型（progress / blocked / merged）区分通知文案与声音；多显示器窗口记忆。

### 协作原语（借鉴 DeepSeek Agent Teams）

吸收会话内 Team 的可信协作原语，**不**对接 Harness 任务板、不弱化 worktree/`write_scope`。优先：任务变更 CAS、分型消息、结构化 submit 结果、stalled/idle 与超时释放。决议与拆包草案见 [maw_from_deepseek_agent_teams.zh-CN.md](maw_from_deepseek_agent_teams.zh-CN.md)。

## 非目标（近期）

- 云端多机协作与账号体系
- 替代各 IDE 内置 Agent 运行时
- 原生接收 DeepSeek Harness「智能团队」任务（无跨进程协调协议）
