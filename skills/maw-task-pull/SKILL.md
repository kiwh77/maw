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
maw register --json          # or omit; next auto-assigns
export MAW_AGENT=<name>      # {app}-{project}-{n}
maw next --json
```

Work only in the printed worktree (`.worktrees/{agent}`). Stay inside `write_scope`. Commit before `maw submit`.

## Global board

```bash
maw board --json --status ready
maw next -C "<project_path>" --json
```

## After claim

```bash
maw progress -m "milestone"
maw submit
maw merge --yes
maw next   # if ready remains
```
