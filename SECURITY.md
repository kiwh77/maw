# Security Policy

> Languages: **English** | [简体中文](SECURITY.zh-CN.md)

## Supported versions

Please use the latest released `maw` (or build from `main`). Older versions usually do not receive separate security fixes.

## Trust boundary

`maw` is a **local, single-user** tool. The hub listens on `127.0.0.1` and write operations require local
authentication. Do not expose it on `0.0.0.0`, a public interface, or a shared/mounted volume: the state DB holds
task content, agent names, and the event stream, and there is no multi-user authorization model.

Static exports (`file://` or an exported directory) are **read-only**; `/api/runtime` reports
`static_export_managed: false` and write controls are expected to be disabled or degraded there.

## Managed execution

`maw managed start` launches child processes under your OS user, with the same filesystem permissions you have.
Two boundaries follow from that:

- It is only claimed to work on macOS, where native start / stop / reclaim has been observed. Linux and Windows
  build but are not supported for managed execution.
- Automatic dispatch (`[managed] auto_enabled`) is **off by default**. Turning it on lets the hub pick tasks and
  start processes without you in the loop; treat that as a privilege escalation for the project, not a convenience toggle.

## Data integrity

- **Back up before upgrading.** Upgrades go through a consistent `VACUUM INTO` snapshot plus verification.
  Copying a live `maw.db` with `cp` silently loses un-checkpointed WAL frames.
- **Never write to the DB with `sqlite3`.** Direct writes bypass the writer gate and referential checks; a newer
  schema written by an older binary corrupts the DB while looking successful.
- Backup files contain the same sensitive data as the DB. Keep them out of Git and out of shared storage.

Upgrade procedure, recovery steps, and `unknown` resolution rules:
[docs/operations/vnext/upgrade-notes.zh-CN.md](docs/operations/vnext/upgrade-notes.zh-CN.md).
Operational scope and platform support: [docs/operations/vnext/operations-guide.zh-CN.md](docs/operations/vnext/operations-guide.zh-CN.md).

## Reporting a vulnerability

Do **not** disclose exploit details in public Issues.

Report privately via GitHub Security Advisories (repository Security page) or contact a maintainer. Include:

- Affected version / commit
- Reproduction steps or PoC (private channel)
- Expected impact

We will acknowledge receipt and prioritize a fix.
