# Changelog

> Languages: **English** | [简体中文](CHANGELOG.zh-CN.md)

Format follows [Keep a Changelog](https://keepachangelog.com/). Versions follow SemVer.

## [Unreleased]

### Changed

- **Out-of-tree-only attach (breaking)**: runtime config/blueprint live under `~/.maw/projects/<id>/`; in-repo `.multiagent/` is ignored; `maw init` no longer injects protocol files or git hooks. `register`/`next`/`board` auto-`EnsureAttached`. Copy legacy in-repo config by hand if needed.
- **Remove worktree after merge (protocol v8 / CLI 0.4.1)**: successful `maw merge --yes` deletes `.worktrees/{agent}` and the task branch; the next `maw next` recreates the same path. `maw prune` no longer keeps idle trees warm for 48h — only `working` / in-progress task holds remain.

### Added

- `maw project add`: register the current git repo (writes only under `~/.maw`, zero repo tree writes)
- **Block-feedback iteration**: `maw block` records `block_category` + followups; board problems show category; `request-change` refreshes `.maw/task.json`/`TASK.md`; blueprint `write_scope` strips Chinese parenthetical fragments; claim-time `required_paths` / acceptance `required:` baseline check; unrelated repo gates become recoverable `blocked` (category gate); queue-stall signal cooldown 30m; `maw prune --history [--yes]` exports then deletes terminal-task →blocked events and stale `unblock_assist` jobs
- **Workbench "auto run" toggle**: one switch left of "Join" in the toolbar. Enabling writes `managed.auto_enabled` + `managed.auto_select` + `allowed_nodes` + `system_agent.worker_node` atomically and probes the node on the spot; a node that cannot accept work is **rejected with a reason code** instead of producing a switch that silently does nothing. `GET/PUT /api/v1/projects/{id}/auto-execution`.
- **Hot-applied switches**: the `maw serve` leader re-reads `config.toml` (mtime+size) every loop, so toggles take effect without a restart; the three-switch snapshot is recorded as `lead_switches_changed`.
- **Router intervention loop**: the five queue-stall signals (`dispatch_op_stalled`／`claimed_not_started`／`dep_deadlock`／`no_capable_node`／`repeated_failure`) now enqueue jobs according to `auto_kinds` instead of reporting only; `GET/PUT /api/v1/projects/{id}/system-agent` writes `llm_command`／`auto_kinds`／`ops_allowlist` and `llm_ready` states whether the Router can actually diagnose; `ops_run` results keep the command text, the matched allowlist rule and the exit code.
- **Router visibility**: the problems view gains `router` (tasked／escalated／running) and `router-idle` (unattended) rows reconciled against blocked tasks.

- `maw task cancel <id> [--reason] [--superseded-by]` with audit events; clears `blocked_reason` / branch / worktree into `extra`
- `maw request-change [--add path/**] [-m …]` expands the current task write_scope (auto-detects out-of-scope diffs)
- `maw prune` reports `kept_reasons`; prunes idle / offline agent worktrees (no post-merge warm keep); strips `TASK.md` / `.maw/` before remove
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
