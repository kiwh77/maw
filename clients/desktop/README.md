# maw Desktop（Tauri）

把 `maw serve`（即 `maw board --serve`）的看板页打成桌面窗口，并用**系统托盘**做实时通知。

## 行为

1. 启动时探测 `http://127.0.0.1:7420/api/board`
2. 若未就绪，尝试在 PATH 中执行 `maw serve --addr 127.0.0.1:7420`
3. WebView 加载 `http://127.0.0.1:7420/`（与浏览器版同一套 HTML）
4. 订阅 `/api/events`：收到 `event` / `board-changed` 时更新托盘标题；`event` 可弹出系统通知

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
- `src-tauri/` — Rust / Tauri 壳（窗口、托盘、拉起 `maw serve`）
- 前端：不自建业务 UI，直接导航到本机中枢 URL

## 图标

已接入 `brand/maw-icon-1024.png` → `src-tauri/icons/`，并在 `tauri.conf.json` 的 `bundle.icon` 与托盘中引用。  
文档站可用：[docs/images/maw-icon.png](../../docs/images/maw-icon.png)。

## 状态

图标与托盘资源已接入；`cargo tauri build` 前需本机安装 Tauri CLI。
