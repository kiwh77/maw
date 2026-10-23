---
name: maw-init
description: >-
  Initialize or upgrade maw multi-agent setup in a git repo (maw init, protocol,
  config). Use when there is no .multiagent/, doctor says not init, or first board hookup.
---

# maw-init

> Chinese: [SKILL.zh-CN.md](SKILL.zh-CN.md)

## When

- Repo has no `.multiagent/` or `maw doctor` says not initialized
- Need protocol / config upgrade (`maw init --upgrade`)

## Steps

```bash
cd /path/to/git-repo
maw init                 # or: maw init --upgrade
maw doctor
```

Writes `.multiagent/config.toml`, protocol into `AGENTS.md` / IDE rules (language from `MAW_LANG` / system locale, default **en**), and registers the project in `~/.maw/maw.db`.

`main_branch` from init is only a probe placeholder — set the real trunk in the task list / blueprint `[meta] main_branch` before `plan apply`.

## Next

Use **maw-task-list** then **maw-plan-push**.
