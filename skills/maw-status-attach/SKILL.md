---
name: maw-status-attach
description: >-
  Attach ad-hoc / user-assigned work to the local maw hub with maw status report
  without claiming via maw next. Use for side-path status into the board.
---

# maw-status-attach

> Chinese: [SKILL.zh-CN.md](SKILL.zh-CN.md)

## When

Work is not from `maw next` (user-assigned, exploration) but should still show on the local hub / tray.

## Report

```bash
# hub must be up
maw serve   # or maw board --serve

maw status report -m "implementing X" --phase code --pct 40
# optional: --task T-xxx --agent <name> --project <id> --client cursor
```

Or:

```bash
curl -sX POST http://127.0.0.1:7420/api/status   -d '{"actor":"cursor-demo-1","message":"hi","phase":"code","pct":40}'
```

Keep reporting on milestones so the board/desktop tray stay current.

## Ownership boundary (hard)

- Bypass / observer reports **only your own** observations and heartbeats.
- They **must not** seize a **managed Run** (created by `maw managed start` / ClaimExecution): no flipping ownership, no pretending to be the run supervisor.
- Optional `--task T-xxx` associates a heartbeat only; it does **not** claim the task or change status to `claimed` / `in_progress`. Real claim → [maw-task-pull](../maw-task-pull/SKILL.md).
- **Never** put CLI tokens (`maw_pat_…`, Bearer) into `-m` text, curl bodies that get logged as events, prompts, or the repo. Use `Authorization` header or the local 0600 token file only.
