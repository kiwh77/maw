"""Blueprint TOML parse and validation."""

from __future__ import annotations

import tomllib
from pathlib import Path
from typing import Any

from maw.errors import MawError
from maw.scope import scopes_overlap


def load_blueprint(path: Path) -> dict[str, Any]:
    if not path.is_file():
        raise MawError(f"blueprint not found: {path}", next_hint="pass a blueprint.toml path")
    with path.open("rb") as f:
        data = tomllib.load(f)
    tasks = data.get("task") or data.get("tasks") or []
    if not isinstance(tasks, list):
        raise MawError("blueprint [[task]] must be an array of tables")
    data["_tasks"] = tasks
    data["_shared"] = data.get("shared_files") or []
    return data


def _deps_cycle(tasks: list[dict[str, Any]]) -> list[str] | None:
    ids = {t["id"] for t in tasks}
    graph = {t["id"]: list(t.get("deps") or []) for t in tasks}
    for deps in graph.values():
        for d in deps:
            if d not in ids:
                raise MawError(
                    f"unknown dependency {d!r}",
                    next_hint="fix blueprint deps to reference existing task ids",
                )
    visiting: set[str] = set()
    visited: set[str] = set()
    stack: list[str] = []

    def dfs(n: str) -> list[str] | None:
        if n in visited:
            return None
        if n in visiting:
            return stack[stack.index(n) :] + [n]
        visiting.add(n)
        stack.append(n)
        for d in graph.get(n, []):
            cyc = dfs(d)
            if cyc:
                return cyc
        stack.pop()
        visiting.remove(n)
        visited.add(n)
        return None

    for tid in graph:
        cyc = dfs(tid)
        if cyc:
            return cyc
    return None


def validate_blueprint(data: dict[str, Any]) -> list[str]:
    """Return list of error strings (empty = ok)."""
    errors: list[str] = []
    tasks: list[dict[str, Any]] = data.get("_tasks") or []
    if not tasks:
        errors.append("blueprint has no [[task]] entries")
        return errors

    ids: set[str] = set()
    for t in tasks:
        tid = t.get("id")
        if not tid:
            errors.append("task missing id")
            continue
        if tid in ids:
            errors.append(f"duplicate task id {tid}")
        ids.add(tid)
        if not t.get("write_scope"):
            errors.append(f"{tid}: write_scope required")
        if not t.get("title"):
            errors.append(f"{tid}: title required")

    try:
        cyc = _deps_cycle(tasks)
        if cyc:
            errors.append("dependency cycle: " + " -> ".join(cyc))
    except MawError as exc:
        errors.append(exc.message)

    # pairwise scope overlap
    for i, a in enumerate(tasks):
        for b in tasks[i + 1 :]:
            overlaps = scopes_overlap(a.get("write_scope") or [], b.get("write_scope") or [])
            if overlaps:
                errors.append(
                    f"write_scope overlap {a['id']} vs {b['id']}: {', '.join(overlaps[:5])}"
                )

    shared = data.get("_shared") or []
    task_ids = {t["id"] for t in tasks if t.get("id")}
    for sf in shared:
        path = sf.get("path")
        owner = sf.get("owner_task")
        if not path or not owner:
            errors.append("shared_files entry needs path and owner_task")
            continue
        if owner not in task_ids:
            errors.append(f"shared_files {path}: owner_task {owner} unknown")
        # ensure only owner lists that path in write_scope
        for t in tasks:
            if t["id"] == owner:
                continue
            for pat in t.get("write_scope") or []:
                if path == pat or path.startswith(str(pat).rstrip("*").rstrip("/") + "/"):
                    # soft: other tasks claiming the shared file
                    if path in (t.get("write_scope") or []) or any(
                        path == p for p in (t.get("write_scope") or [])
                    ):
                        errors.append(
                            f"shared file {path} also in write_scope of {t['id']} "
                            f"(only owner_task {owner} may write it)"
                        )

    return errors


def tasks_for_db(data: dict[str, Any]) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for t in data.get("_tasks") or []:
        deps = list(t.get("deps") or [])
        status = "ready" if not deps else "backlog"
        out.append(
            {
                "id": t["id"],
                "title": t.get("title") or t["id"],
                "status": status,
                "priority": int(t.get("priority") or 100),
                "merge_order": int(t.get("merge_order") or t.get("priority") or 100),
                "owner": None,
                "deps": deps,
                "write_scope": list(t.get("write_scope") or []),
                "read_scope": list(t.get("read_scope") or []),
                "acceptance": list(t.get("acceptance") or []),
                "branch": None,
                "worktree": None,
                "attempts": 0,
                "blocked_reason": None,
                "heartbeat": None,
                "prev_status": None,
                "revision": 0,
                "extra": {},
            }
        )
    return out


EXAMPLE_BLUEPRINT = """\
[meta]
name = "example"
version = 1

[[task]]
id = "T-001"
title = "Example shared setup"
priority = 1
deps = []
write_scope = ["README.md"]
acceptance = ["README updated"]
merge_order = 1

[[task]]
id = "T-002"
title = "Example feature A"
priority = 2
deps = ["T-001"]
write_scope = ["src/a/**"]
read_scope = ["src/common/**"]
acceptance = ["feature A works"]
merge_order = 2
"""
