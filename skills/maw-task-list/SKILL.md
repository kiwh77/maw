---
name: maw-task-list
description: >-
  Split requirements into claimable maw task cards (write_scope, deps, acceptance,
  verify, goal, lanes). Use when building an executable task list or preparing for maw-plan-push.
---

# maw-task-list

> Chinese: [SKILL.zh-CN.md](SKILL.zh-CN.md)

## Goal artifact

Prefer `docs/**executable-task-list*.md` (or user path). Each card must map to blueprint **L1**: `id / title / goal / write_scope / acceptance / verify`. Also L2: `deps` / `read_scope` / `not_doing` / `priority` / `merge_order`. Missing `verify` or empty `acceptance` → `maw plan apply` rejects.

**State trunk at the top** of the list (branch for this wave). maw does not hard-code `main`.

## Split rules

1. **One responsibility per task** — Goal = `现状：…. 目标：….`; Non-goals → `not_doing`; Verify is a separate required field (command or `manual: …`).
2. **Surface = future write_scope** — narrow globs; shared hotspots own their own task or serial deps.
3. **Lane = context cluster** — same module/surface family; chain with deps; parallel across lanes.
4. **Sweet spot** — ~0.5–1 day to Verify; split if cross-module; merge if too tiny to verify alone.
5. **Deps only for real prerequisites** (and same-lane continuations for next affinity). Field name must be `deps` — typos like `depends` are silently dropped.
6. **Contract vs implementation** — contracts only touch docs; implementations Depend on contracts and **must** set `read_scope` to the contract file(s). Steps/Contract prose stay in md.

## Card template

```markdown
### T-0xx <short title>
- **Goal**: 现状：…. 目标：….
- **Non-goals**: …          → not_doing
- **Depends**: T-… | none
- **Surface**: `path/**`
- **Contract**: see `docs/contracts/….md`
- **Read-scope**: `docs/contracts/….md`  (≥1; → read_scope)
- **Steps**: …
- **Acceptance**: ≥1 decidable assertion  → acceptance
- **Verify**: command or `manual: …`     → verify (never merge into Acceptance)
- **Lane**: …
- **maw**: priority=10 · merge_order=10
```

## Self-check (human; machines are weak)

- ≥1 zero-deps entry; after `maw plan check`, `deps_depth` / `layer_widths` match the Lane table
- Check does **not** catch: `works` acceptance, same-file writes without deps, typo’d `depends`, or overlapping `a/**` vs `a/foo.go` (theory parallel ignores path overlap)

## Next

**maw-plan-push** with matching `meta.main_branch`. Field map: [task-card-map.md](task-card-map.md).
