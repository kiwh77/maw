# Changelog

> Languages: **English** | [简体中文](CHANGELOG.zh-CN.md)

Format follows [Keep a Changelog](https://keepachangelog.com/). Versions follow SemVer.

## [Unreleased]

### Added

- `maw task cancel <id> [--reason] [--superseded-by]` with audit events; clears `blocked_reason` / branch / worktree into `extra`
- `maw request-change [--add path/**] [-m …]` expands the current task write_scope (auto-detects out-of-scope diffs)
- `maw prune` reports `kept_reasons`; prunes idle (>48h) / offline agent worktrees; strips `TASK.md` / `.maw/` before remove
- `plan apply` returns `stale_blocked` when blocked tasks remain outside the blueprint
- Agent expiry renews from recent task heartbeat (`agent_heartbeat_renewed`) instead of false-expiring long coding sessions
- System agent layer: `[system_agent]` config, `maw system status|trigger|run|watch`, Raise+enqueue on `blocked`, `task_split` / `ops_run` on the same bus, HTTP system-jobs, and `__runner` wiring via `launch_spec.json` → `session.Runner`. See `docs/operations/vnext/system-agent.zh-CN.md`.
- English / Chinese i18n: docs mirrors, board UI language switch, CLI / terminal board / protocol / skills via `MAW_LANG` (default `en`)
- Remove a project from the hub: board "Projects" view gets a two-step removal button, plus `maw project list` / `maw project rm <id|name>` and `DELETE /api/v1/projects/{project_id}`. Live work is protected: unfinished tasks or online Agents reject removal unless `--force` / `?force=1`; events, frozen task versions and usage records are kept as ledgers.

### Changed

- Board: `cancelled` is its own lane (not under Done); terminal cards hide `blocked_reason`; activity view hides cancelled by default
- `register` rejects JSON-dump agent ids
- Merge into `done` uses the same terminal field finalizer as cancel
- Submit re-checks a clean tree after freeze/gates (no auto-approve then dirty merge)
- Merge dirty trees become recoverable `blocked` (not conflict); ignore tracked `TASK.md` / `.maw/`
- Default `heartbeat_ttl_sec` 900→1800; `block` on scope messages attaches `suggested_followups`

## [0.4.0] - 2026-10-22

### Added

- Persistent worktree per agent (protocol v7); `maw prune`
- Global board SSE, light/dark themes and filters
- Agent Skills (`skills/maw*`) and board screenshot
- GitHub Releases: multi-platform binaries (tag `v*`)

### Changed

- Public brand and Go module unified as `github.com/kiwh77/maw` (formerly `goal-agents`)
- Positioning as local multi-agent task hub; README / ROADMAP aligned with status aggregation and status-bar roadmap
- Neutral example agent name `cursor-demo-1`
- `maw next` no longer limited by `max_parallel`; context-affinity claiming

## [0.3.x]

- Global multi-project DB, incremental `plan apply`, unified worker naming, priority scheduling, and more
