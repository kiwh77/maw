---
name: maw-task-pull
description: >-
  Claim a maw task with maw next in this repo or via global board -C.
  Use when pulling/claiming ready work for an agent session.
---

# maw-task-pull

> Chinese: [SKILL.zh-CN.md](SKILL.zh-CN.md)

## In this repo

```bash
maw register --json          # or omit; --claim auto-assigns
export MAW_AGENT=<name>      # {app}-{project}-{n}

maw next --json              # 1) query: preview only (no state change, no Run, no worktree)
maw next --claim --json      # 2) claim explicitly once the preview looks right
```

`maw next` without `--claim` is read-only: the candidate stays `ready`, no Run or lease is
created and no worktree is written. Only `maw next --claim` claims it. `--dry-run` remains a
pure preview alias.

Work only in the printed worktree (`.worktrees/{agent}`). Stay inside `write_scope`. Commit before `maw submit`.

### Claim context (keep these fields)

After `maw next --claim --json`, preserve in your working notes / TASK context:

| Field | From JSON | Notes |
|---|---|---|
| task id | `task.id` | display id |
| instance | `task.instance_id` | `ti_…` — stable identity |
| version / revision | task revision fields | definition version |
| source | treat as `manual_next` | not `managed_cli` / not status-attach |

**Never** paste CLI tokens (`maw_pat_…`, `Authorization: Bearer …`) into prompts, TASK.md, commits, or status events. Tokens stay in the local auth file / HTTP header only.

Managed runs are a separate path: `maw managed start --task <ID> --node <node>` (auto-dispatch off by default). Do not pretend a manual next owns a managed Run.

## Global board

```bash
maw board --json --status ready
maw next -C "<project_path>" --json                 # preview the candidate first
maw next -C "<project_path>" --claim --json         # then claim it
```

## After claim

```bash
maw progress -m "milestone"
maw submit
maw merge --yes
maw next --claim   # if ready remains: preview, then claim the next one
```
