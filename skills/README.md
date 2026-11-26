# Agent Skills (maw workflow)

> Languages: **English** | [简体中文](README.zh-CN.md)

For clients that support [Agent Skills](https://docs.cursor.com/context/skills) (Cursor, Claude Code, …): stage-by-stage guidance for using maw correctly.

Each skill directory has **`SKILL.md` (English)** and **`SKILL.zh-CN.md` (Chinese)**.

| Skill | Role |
| --- | --- |
| [maw](maw/SKILL.md) | Series router and execution cheat sheet |
| [maw-init](maw-init/SKILL.md) | Project init / protocol upgrade |
| [maw-task-list](maw-task-list/SKILL.md) | Split claimable task cards |
| [maw-plan-push](maw-plan-push/SKILL.md) | Write blueprint and `plan apply` |
| [maw-task-pull](maw-task-pull/SKILL.md) | `maw next` in-repo or cross-repo |
| [maw-status-attach](maw-status-attach/SKILL.md) | Attach non-claimed work; `status report` to the hub |

## Install locally

Copy or symlink each skill directory into your client's skills path, for example:

```bash
ln -s "$(pwd)/skills/maw" ~/.cursor/skills/maw
ln -s "$(pwd)/skills/maw-init" ~/.cursor/skills/maw-init
ln -s "$(pwd)/skills/maw-task-list" ~/.cursor/skills/maw-task-list
ln -s "$(pwd)/skills/maw-plan-push" ~/.cursor/skills/maw-plan-push
ln -s "$(pwd)/skills/maw-task-pull" ~/.cursor/skills/maw-task-pull
ln -s "$(pwd)/skills/maw-status-attach" ~/.cursor/skills/maw-status-attach
```

If you use a central library (e.g. `~/.agents/skills`), link there and let tools share it.
