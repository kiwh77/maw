# maw Desktop (Tauri)

> Languages: **English** | [简体中文](README.zh-CN.md)

Wraps the `maw serve` (`maw board --serve`) board page in a desktop window and uses the **system tray** for live notifications.

## Behavior

1. On start, probe `http://127.0.0.1:7420/api/board`
2. If not ready, try `maw serve --addr 127.0.0.1:7420` from `PATH`
3. WebView loads `http://127.0.0.1:7420/` (same HTML as the browser board)
4. Subscribe to `/api/events`: update tray title on `event` / `board-changed`; `event` may show a system notification

Tray menu language follows `MAW_LANG` / system locale (`en` / `zh`).

## Development

Requires Rust, [Tauri CLI](https://v2.tauri.app/start/), and a local `maw` install.

```bash
maw serve
cd clients/desktop
cargo tauri dev
```

Build: `cargo tauri build`

## Layout

- `brand/` — icon masters
- `src-tauri/icons/` — bundle icons + tray
- `src-tauri/` — Rust / Tauri shell
- Front-end: no separate business UI; navigates to the local hub URL
