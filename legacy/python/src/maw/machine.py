"""Task state machine."""

from __future__ import annotations

from maw.errors import MawError

# Full graph; M1 path: ready -> in_progress -> in_review -> merging -> done
TRANSITIONS: dict[str, set[str]] = {
    "backlog": {"ready", "cancelled", "blocked"},
    "ready": {"claimed", "in_progress", "cancelled", "blocked"},
    "claimed": {"in_progress", "ready", "cancelled", "blocked"},
    "in_progress": {"self_check", "in_review", "blocked", "cancelled", "ready"},
    "self_check": {"in_review", "in_progress", "blocked", "cancelled"},
    "in_review": {"merging", "in_progress", "blocked", "cancelled"},
    "merging": {"done", "in_progress", "conflict", "blocked", "cancelled"},
    "conflict": {"in_progress", "ready", "cancelled", "blocked"},
    "blocked": set(),  # restored via prev_status
    "done": set(),
    "cancelled": set(),
}

ACTIVE_OWNER_STATUSES = {
    "claimed",
    "in_progress",
    "self_check",
    "in_review",
    "merging",
    "conflict",
}


def can_transition(src: str, dst: str) -> bool:
    if src == "blocked":
        return True  # unblock restores prev
    return dst in TRANSITIONS.get(src, set())


def assert_transition(src: str, dst: str) -> None:
    if src == dst:
        return
    if src == "blocked":
        return
    if not can_transition(src, dst):
        raise MawError(
            f"illegal status transition {src} -> {dst}",
            next_hint="run `maw board` to inspect task status",
        )
