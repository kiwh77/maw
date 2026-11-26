# maw Desktop (Tauri)

> Languages: **English** | [简体中文](README.zh-CN.md)

Wraps the `maw serve` (`maw board --serve`) board page in a desktop window and uses the **system tray** for live notifications.

## Behavior

1. On start, show a local bootstrap page and probe `http://127.0.0.1:7420/api/board`
2. If not ready, resolve `maw` via `MAW_BIN` / `PATH` / `~/.local/bin/maw` (and common prefixes), then run `maw serve --addr 127.0.0.1:7420` — macOS Dock launches get a minimal PATH without `~/.local/bin`
3. When the hub is ready, the WebView navigates to `http://127.0.0.1:7420/` (same HTML as the browser board); tray “Show board” retries spawn
4. Subscribe to `/api/events`: update tray title on `event` / `board-changed`; `event` may show a system notification

Tray menu language follows `MAW_LANG` / system locale (`en` / `zh`).

## Status and scope

- Built and exercised **on macOS only**. No native build or behavior claim for Linux / Windows.
- Precondition: a local `maw` binary reachable via `MAW_BIN`, `PATH`, or the common install prefixes.
  The shell does not own the DB or the task queue — it only starts a hub and renders its page.
- The shell does **not** start managed runs (`maw managed start` is a CLI action), does not change capability
  support, and does not make a static export writable (exported pages are read-only).

Capability tiers, managed-execution limits, and upgrade rules:
[docs/operations/vnext/operations-guide.zh-CN.md](../../docs/operations/vnext/operations-guide.zh-CN.md).

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
- `ui/` — startup bootstrap (wait / failure UI while the hub is down)
- `src-tauri/icons/` — bundle icons + tray
- `src-tauri/` — Rust / Tauri shell
- Front-end: no board UI of our own; navigates to the local hub URL once ready

## Status

**macOS MVP shipped** (VN-902): spawn local hub, reuse the board page, tray menu, system notifications via SSE / feed catch-up, navigation allowlist and token redaction. Icons and tray assets are wired.

Still out of claim: native Linux / Windows builds; per-event-type copy / sounds and multi-display window memory (see ROADMAP “Tray notification detail”). Full `cargo tauri build` needs a local Tauri CLI; CI only runs desktop prerequisites, not a full bundle.
