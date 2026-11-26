---
name: maw-init
description: >-
  Attach a git repo to local maw (maw init / project add, out-of-tree config, doctor).
  Use when doctor says not wired, first board hookup, or user asks to init/attach maw.
---

# maw-init

> Chinese: [SKILL.zh-CN.md](SKILL.zh-CN.md)

## When

- First time registering a repo on the local board
- `maw doctor` says not attached
- Optional: refresh out-of-tree defaults with `maw init --upgrade`

`register` / `next` / `board` also auto-`EnsureAttached` — explicit init is often unnecessary.

## Steps

```bash
cd /path/to/git-repo
maw init                 # or: maw project add
# maw init --upgrade     # rewrite out-of-tree config.toml defaults
maw doctor
```

Writes **only** under `~/.maw/` (`maw.db` + `projects/<id>/config.toml` + example blueprint).  
Does **not** create in-repo `.multiagent/`, protocol files, git hooks, or `.gitignore` changes.  
In-repo legacy files are **ignored** at runtime.

`main_branch` from init is only a probe placeholder — set the real trunk in the task list / blueprint `[meta] main_branch` before `plan apply`.

## Next

[maw-task-list](../maw-task-list/SKILL.md) → [maw-plan-push](../maw-plan-push/SKILL.md)
