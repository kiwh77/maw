# Changelog

> Languages: **English** | [简体中文](CHANGELOG.zh-CN.md)

Format follows [Keep a Changelog](https://keepachangelog.com/). Versions follow SemVer.

## [Unreleased]

### Added

- System agent layer: `[system_agent]` config, `maw system status|trigger|run|watch`, Raise+enqueue on `blocked`, `task_split` / `ops_run` on the same bus, HTTP system-jobs, and `__runner` wiring via `launch_spec.json` → `session.Runner`. See `docs/operations/vnext/system-agent.zh-CN.md`.
- English / Chinese i18n: docs mirrors, board UI language switch, CLI / terminal board / protocol / skills via `MAW_LANG` (default `en`)
- Remove a project from the hub: board "Projects" view gets a two-step removal button, plus `maw project list` / `maw project rm <id|name>` and `DELETE /api/v1/projects/{project_id}`. Live work is protected: unfinished tasks or online Agents reject removal unless `--force` / `?force=1`; events, frozen task versions and usage records are kept as ledgers.

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
