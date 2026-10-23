# maw design notes

> Languages: **English** | [简体中文](zh/DESIGN.md)

**maw** (Multi-Agent Workspace) is a **local multi-agent task hub** CLI (single Go binary): orchestrate parallel development and aggregate status across projects. Client status-bar ideas: [ROADMAP.md](ROADMAP.md).

- Language: Go, `CGO_ENABLED=0` + modernc.org/sqlite
- **Global state**: `~/.maw/maw.db` (override with `MAW_GLOBAL`), tables `projects` / `tasks` / `agents` (all with `project_id`)
- **Per project**: `.multiagent/` holds `config.toml`, `blueprint.toml`, `project.json` (legacy local `maw.db` migrates once on init)
- Blueprint: `blueprint.toml` → `maw plan apply` (**incremental merge from 0.3.2**; `--reset` clears and reloads)
- **Trunk branch (0.3.9)**: not hard-coded to `main`; `init` only probes a placeholder. Task lists / blueprints set `[meta] main_branch`; `plan apply` syncs `.multiagent/config.toml` and global `projects.main_branch`. Missing meta keeps existing config
- **Merge cleanup (0.3.10)**: after successful `maw merge`, best-effort delete matching `agent/*` local branches
- **Persistent worktree per agent (0.4.0 / protocol v7)**: `.worktrees/{agent}`; `next` resets to trunk tip and `checkout -B` a new task branch; successful `merge` **keeps** the worktree and only deletes the task branch. Use `maw prune` for leftovers
- Protocol: `AGENTS.md` fallback + Cursor/Qoder/Trae/CodeBuddy rules + git hooks (v7: worker name `{app}-{project}-{n}`, agents may merge, then continue with `next`). Language follows `MAW_LANG` / system locale at `maw init`
- **Global board**: `maw board --serve`, HTTP `/api/board?project=&agent=&status=`, in-page filters; UI language switchable (en/zh)
- **Scheduling (0.3.1)**: `internal/schedule` — overlapping `write_scope` may coexist; `promoteReady` / `maw next` activate only when deps are satisfied and write-scope order is first (`priority` → `merge_order` → `id`)
- **Context affinity (0.3.5)**: for the same agent, prefer affinity to recent finished work (deps chain > write-scope overlap > module path prefix ≥2 segments)
- **No parallel slot block (0.3.6)**: `maw next` is not refused for `max_parallel`; write-scope overlap still serializes; `max_parallel` is board reference only

Authoritative product spec is design doc v0.2; multi-project is the 0.3.x increment.
