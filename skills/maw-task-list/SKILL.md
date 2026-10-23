---
name: maw-task-list
description: >-
  Split requirements into claimable maw task cards (write_scope, deps, acceptance,
  lanes). Use when building an executable task list or preparing for maw-plan-push.
---

# maw-task-list

> Chinese: [SKILL.zh-CN.md](SKILL.zh-CN.md)

## Goal artifact

Prefer `docs/**executable-task-list*.md` (or user path). Each card must map to blueprint fields: `id / title / priority / deps / write_scope / acceptance`.

**State trunk at the top** of the list (branch for this wave). maw does not hard-code `main`.

## Split rules

1. **One responsibility per task** — one Goal sentence; clear Non-goals; Verify must be runnable.
2. **Surface = future write_scope** — narrow globs; shared hotspots own their own task or serial deps.
3. **Lane = context cluster** — same module/surface family; chain with deps; parallel across lanes.
4. **Sweet spot** — ~0.5–1 day to Verify; split if cross-module; merge if too tiny to verify alone.
5. **Deps only for real prerequisites** (and same-lane continuations for next affinity).
6. **Contract vs implementation** — contracts only touch docs; implementations Depend on contracts.

## Card template

```markdown
### T-0xx <short title>
- **Goal**: …
- **Non-goals**: …
- **Depends**: T-… | none
- **Surface**: `path/**`
- **Verify**: `go test ./…` / `npm run build`
- **Lane**: …
- **maw**: priority=10 · merge_order=10
```

## Next

**maw-plan-push** with matching `meta.main_branch`.
