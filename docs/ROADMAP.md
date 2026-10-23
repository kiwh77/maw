# ROADMAP

> Languages: **English** | [简体中文](zh/ROADMAP.md)

maw is a local multi-agent **task hub**. Shipped and planned capabilities:

## Shipped

- Blueprint tasks, persistent worktrees, claim / submit / merge, write-scope checks
- Global state (`~/.maw`) and cross-project board (`maw board` / SSE)
- Multi-IDE protocol injection (Cursor / Qoder / Trae / CodeBuddy, …) and hooks
- **i18n (en/zh)**: English-primary docs with Chinese mirrors; board UI language switch; CLI / terminal board / `maw init` protocol / skills catalogs via `MAW_LANG` (default en)

## In progress / partially shipped

### Cross-project agent status aggregation

- CLI: `maw status report` (also `POST http://127.0.0.1:7420/api/status`, loopback only)
- Incremental: `GET /api/feed?since=`
- SSE: `/api/events` pushes `event` frames in addition to `board-changed`

### Desktop client (Tauri)

Package the same board as `maw board --serve` into a desktop shell with tray notifications. See `clients/desktop/`.

## Planned

### Tray notification detail

Distinct copy and sounds per event type (progress / blocked / merged); multi-display window memory.

## Non-goals (near term)

- Cloud multi-machine collaboration and accounts
- Replacing each IDE’s built-in agent runtime
