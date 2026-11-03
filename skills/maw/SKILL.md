---
name: maw
description: >-
  maw multi-agent workflow router: init, task list, plan push, claim tasks,
  status attach, board. Use when the user mentions maw, multi-agent worktree,
  maw init, splitting tasks, maw next, maw-task-pull, status report, plan apply,
  maw board, or which maw-* skill to use.
---

# maw series (router)

> Chinese: [SKILL.zh-CN.md](SKILL.zh-CN.md)

Pick a skill by stage; **Read** that skill's `SKILL.md` for details.

| Stage | Skill | When |
|---|---|---|
| 1. Onboard repo | **[maw-init](../maw-init/SKILL.md)** | No `.multiagent/`, doctor not init, first board hookup |
| 2. Split tasks | **[maw-task-list](../maw-task-list/SKILL.md)** | Claimable cards, Surface/Deps/size |
| 3. Push plan | **[maw-plan-push](../maw-plan-push/SKILL.md)** | One-shot remaining tasks into blueprint / apply |
| 4. **Claim** | **[maw-task-pull](../maw-task-pull/SKILL.md)** | `maw next` in-repo or after board project pick |
| 4b. **Side attach** | **[maw-status-attach](../maw-status-attach/SKILL.md)** | Work not from next; `maw status report` to the hub |
| 5. Executing | (this cheat sheet) | progress / submit / merge / board |

Requires **maw CLI ≥ 0.4.0** (persistent worktree per agent; trunk from task list needs **≥ 0.3.9**). Install: repo `./install.sh` → `~/.local/bin/maw`.

## Recommended flow

```text
maw-init (main_branch is only a probe placeholder)
  → maw-task-list (state trunk at top + human task cards)
  → maw-plan-push (blueprint meta.main_branch → plan apply syncs config)
  → maw-task-pull (in-repo next, or board pick then next -C)
  → implement in worktree → submit → maw merge --yes
  → (if ready remains) maw next → … loop
  → maw board --serve
```

## Execution cheat sheet

```bash
maw register --json
export MAW_AGENT=<returned agent>
maw next --json
# or one step: maw next --json (auto-assigns without MAW_AGENT)

maw progress -m "milestone"
maw submit
maw merge --yes
maw next

# hub bookkeeping: list / remove project registrations (refuses while tasks or Agents are live)
maw project list
maw project rm <id|name>        # --force clears the live work too
```

**Hard rules**

- Only edit task `write_scope`; state lives in `~/.maw/maw.db`
- One persistent worktree per agent: `.worktrees/{agent}`
- **`git commit` before `maw submit`** (scope only)
- After submit you may `maw merge --yes`; never git merge/rebase/push trunk yourself
- Locale: `MAW_LANG=en|zh` (default en) for CLI / protocol inject / terminal board
- Manual claim keeps **instance / version / source** (`manual_next`); tokens never enter prompts, the repo, or events
- Bypass (`maw status report`) reports **own** observations only — never seize a **managed Run**
- Managed path: `maw managed start --task … --node …` (auto off by default); stale protocol → `maw init --upgrade`
- System agent (off by default): `[system_agent] enabled=true`, then `maw system watch|trigger|run|status`; `task_split` / `ops_run` on the same bus
