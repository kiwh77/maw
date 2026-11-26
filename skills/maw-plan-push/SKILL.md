---
name: maw-plan-push
description: >-
  Write remaining tasks into .multiagent/blueprint.toml and maw plan apply.
  Use when pushing/refilling the maw queue after a round of agents.
---

# maw-plan-push

> Chinese: [SKILL.zh-CN.md](SKILL.zh-CN.md)

## Prerequisites

- Repo already `maw init`
- Task list from **maw-task-list**
- maw ≥ 0.4.0 (L1: `goal` / `acceptance` / `verify` required); prefer ≥ 0.3.9 for `meta.main_branch`

## Flow

```bash
maw doctor
maw board --json
# edit .multiagent/blueprint.toml with remaining (not done) tasks
maw plan check .multiagent/blueprint.toml
maw plan apply .multiagent/blueprint.toml --yes   # add --reset only for full wipe
maw board
```

## Blueprint sketch

```toml
[meta]
name = "wave-name"
version = 1
main_branch = "<trunk>"

[[task]]
id = "T-xxx"
title = "verb-led deliverable"
goal = "现状：…. 目标：…."
priority = 10
deps = []
write_scope = ["path/**"]
read_scope = ["docs/contracts/….md"]
acceptance = ["decidable assertion with observation point"]
verify = ["go test ./… -count=1"]   # or "manual: …"
not_doing = ["do not change …"]
merge_order = 10
```

Default apply is **incremental**. Do not re-list done tasks unless using `--reset`.  
After check: confirm `deps_depth` / `layer_widths` / zero-deps entries. Typos like `depends` are silently dropped.

## Next

**maw-task-pull** / `maw next`. Field details: [blueprint-spec.md](blueprint-spec.md).
