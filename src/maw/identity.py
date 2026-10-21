"""Caller identity and role checks."""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

from maw.errors import MawError
from maw.home import worktree_root


def resolve_agent(cwd: Path | None = None, explicit: str | None = None) -> str:
    if explicit:
        return explicit
    env = os.environ.get("MAW_AGENT")
    if env:
        return env.strip()
    try:
        root = worktree_root(cwd)
    except MawError:
        root = Path.cwd() if cwd is None else cwd
    marker = root / ".maw" / "agent"
    if marker.is_file():
        return marker.read_text(encoding="utf-8").strip()
    raise MawError(
        "cannot determine caller identity",
        next_hint="export MAW_AGENT=w1 (or integrator), or run `maw next` first",
    )


def role_of(agent: str, config: dict[str, Any]) -> str:
    ident = config.get("identity") or {}
    if agent == ident.get("integrator", "int"):
        return "integrator"
    if agent in (ident.get("reviewers") or []):
        return "reviewer"
    if agent in (ident.get("workers") or []):
        return "worker"
    # unknown agents treated as workers for M1 flexibility
    return "worker"


def require_integrator(agent: str, config: dict[str, Any]) -> None:
    if role_of(agent, config) != "integrator":
        raise MawError(
            f"agent {agent!r} is not integrator; merge denied",
            next_hint=f"export MAW_AGENT={config.get('identity', {}).get('integrator', 'int')}",
        )


def require_owner(agent: str, task: dict[str, Any]) -> None:
    owner = task.get("owner")
    if owner and owner != agent:
        raise MawError(
            f"task {task['id']} is owned by {owner}, not {agent}",
            next_hint="use the owning agent identity, or `maw handoff`",
        )
