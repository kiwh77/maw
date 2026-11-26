# maw Desktop（Tauri）

> 语言：[English](README.md) | 简体中文

把 `maw serve`（即 `maw board --serve`）的看板页打成桌面窗口，并用**系统托盘**做实时通知。

## 行为

1. 启动时先打开本地 bootstrap 页，并探测 `http://127.0.0.1:7420/api/board`
2. 若未就绪，解析 `MAW_BIN` / `PATH` / `~/.local/bin/maw` 等常见路径后执行 `maw serve --addr 127.0.0.1:7420`（macOS 从 Dock 启动时系统 PATH 不含 `~/.local/bin`）
3. 中枢就绪后 WebView 导航到 `http://127.0.0.1:7420/`（与浏览器版同一套 HTML）；托盘「显示看板」会再次尝试拉起
4. 订阅 `/api/events`：收到 `event` / `board-changed` 时更新托盘标题；`event` 可弹出系统通知

## 状态与边界

- 仅在 **macOS** 上构建与实测；对 Linux / Windows 没有原生构建或行为承诺。
- 前置条件：本机能找到 `maw`（`MAW_BIN` / `PATH` / 常见安装目录）。桌面壳不持有库、不持有任务队列，
  只负责起中枢并渲染它的页面。
- 桌面壳**不**发起受管执行（`maw managed start` 是命令行动作）、**不**改变能力支持范围、
  也**不**把静态导出变成可写（导出页面是只读的）。

能力分级、受管执行上限与升级规则见
[docs/operations/vnext/operations-guide.zh-CN.md](../../docs/operations/vnext/operations-guide.zh-CN.md)。

## 开发

前置：Rust、[Tauri CLI](https://v2.tauri.app/start/)、本机已安装 `maw`。

```bash
# 终端 1（可选，桌面端也会尝试拉起）
maw serve

# 终端 2
cd clients/desktop
cargo tauri dev
```

打包：

```bash
cargo tauri build
```

## 目录

- `brand/` — 图标母版（1024、透明版、状态栏 Template）
- `src-tauri/icons/` — 打包用 `icns` / `ico` / PNG + 托盘图标
- `ui/` — 启动 bootstrap（中枢未就绪时的等待／失败提示）
- `src-tauri/` — Rust / Tauri 壳（窗口、托盘、拉起 `maw serve`）
- 前端：不自建业务 UI，中枢就绪后导航到本机中枢 URL

## 图标

已接入 `brand/maw-icon-1024.png` → `src-tauri/icons/`，并在 `tauri.conf.json` 的 `bundle.icon` 与托盘中引用。  
文档站可用：[docs/images/maw-icon.png](../../docs/images/maw-icon.png)。

## 状态

**macOS MVP 已交付**（VN-902）：拉起本机中枢、复用看板页、托盘菜单、SSE／feed 补拉后的系统通知、导航白名单与令牌脱敏。图标与托盘资源已接入。

仍不宣称的范围：Linux／Windows 原生构建；按事件类型细分文案／声音与多显示器窗口记忆（见 ROADMAP「托盘通知细化」）。完整 `cargo tauri build` 需本机安装 Tauri CLI；CI 只跑桌面前置检查，不跑完整打包。
