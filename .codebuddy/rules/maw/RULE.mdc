---
description: maw multi-agent protocol
alwaysApply: true
---

<!-- maw:begin v9 -->
## Multi-agent protocol (managed by maw; do not edit by hand)
1. Global state lives in `~/.maw/maw.db`; this repo's `.multiagent/` holds config and blueprints only. One board covers all projects.
2. Set identity and register: worker names are `{app}-{project}-{n}` (project slug lowercased without hyphens, e.g. `cursor-demo-1`). `export MAW_AGENT=<name>` then `maw register`; if unset, `maw register` / `maw next --claim` auto-assign for the current app. Override the app with `MAW_APP`.
3. Query first with `maw next` — by default it only previews the next candidate: no state change, no Run, no worktree write. Claim explicitly with `maw next --claim`, then work only in that agent's persistent worktree (`.worktrees/{agent}`). maw records which task you hold. Manual claim remains first-class: claim context must keep instance id, definition version, and source (`manual_next`).
4. Edit only files inside the task `write_scope`; to go outside it run `maw request-change`.
5. Milestones: `maw progress`. Non-orchestrated progress can use `maw status report` into the local hub. Finish with `maw submit`; if stuck run `maw block`.
6. Before handoff or interrupt run `maw handoff`; when this session has no more claimable tasks run `maw unregister`.
7. After submit you may `maw merge --yes`; a successful merge deletes the agent worktree and the task branch. Do not git merge/rebase/push trunk yourself.
8. After merge, if ready tasks remain, keep querying with `maw next` and claiming with `maw next --claim` (recreate `.worktrees/{agent}` at trunk tip, new task branch) until none left or the user stops you.
9. Global board: `maw board --serve` with project/Agent/status filters; prune leftovers with `maw prune` (keeps only working / in-progress task holds). The board is observation-only; it does not auto-dispatch.
10. On errors, read maw's Next hint first; do not bypass it.
11. Local CLI/session tokens may appear only in the Authorization header or a 0600 auth file — never in prompts, repo files, blueprints, URLs, logs, or board events.
12. Bypass clients (`maw status report` / observers) may publish their own observations and heartbeats only; they must not flip a task to claimed/in_progress. Real claims still require `maw next --claim`.
13. Protocol blocks are maintained by `maw init` / `maw init --upgrade`; if the file version lags, upgrade first — do not hand-edit the `maw:begin` block.
<!-- maw:end -->
