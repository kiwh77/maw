"""SQLite WAL state store."""

from __future__ import annotations

import json
import sqlite3
from contextlib import contextmanager
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterator

from maw import SCHEMA_VERSION

SCHEMA = """
CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    status TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 100,
    merge_order INTEGER NOT NULL DEFAULT 100,
    owner TEXT,
    deps_json TEXT NOT NULL DEFAULT '[]',
    write_scope_json TEXT NOT NULL DEFAULT '[]',
    read_scope_json TEXT NOT NULL DEFAULT '[]',
    acceptance_json TEXT NOT NULL DEFAULT '[]',
    branch TEXT,
    worktree TEXT,
    attempts INTEGER NOT NULL DEFAULT 0,
    blocked_reason TEXT,
    heartbeat TEXT,
    prev_status TEXT,
    revision INTEGER NOT NULL DEFAULT 0,
    extra_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts TEXT NOT NULL,
    actor TEXT NOT NULL,
    type TEXT NOT NULL,
    task_id TEXT,
    data_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS locks (
    path TEXT PRIMARY KEY,
    owner TEXT NOT NULL,
    task_id TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts TEXT NOT NULL,
    sender TEXT NOT NULL,
    recipient TEXT NOT NULL,
    type TEXT NOT NULL,
    task_id TEXT,
    body_json TEXT NOT NULL DEFAULT '{}',
    read INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS shared_files (
    path TEXT PRIMARY KEY,
    owner_task TEXT NOT NULL
);
"""


def utcnow() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def connect(db_file: Path) -> sqlite3.Connection:
    db_file.parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(str(db_file), timeout=30)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("PRAGMA foreign_keys=ON")
    return conn


def init_db(db_file: Path) -> None:
    conn = connect(db_file)
    try:
        conn.executescript(SCHEMA)
        conn.execute(
            "INSERT OR REPLACE INTO meta(key, value) VALUES('schema_version', ?)",
            (str(SCHEMA_VERSION),),
        )
        conn.commit()
    finally:
        conn.close()


@contextmanager
def transaction(db_file: Path) -> Iterator[sqlite3.Connection]:
    conn = connect(db_file)
    try:
        conn.execute("BEGIN IMMEDIATE")
        yield conn
        conn.commit()
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()


def row_to_task(row: sqlite3.Row) -> dict[str, Any]:
    return {
        "id": row["id"],
        "title": row["title"],
        "status": row["status"],
        "priority": row["priority"],
        "merge_order": row["merge_order"],
        "owner": row["owner"],
        "deps": json.loads(row["deps_json"]),
        "write_scope": json.loads(row["write_scope_json"]),
        "read_scope": json.loads(row["read_scope_json"]),
        "acceptance": json.loads(row["acceptance_json"]),
        "branch": row["branch"],
        "worktree": row["worktree"],
        "attempts": row["attempts"],
        "blocked_reason": row["blocked_reason"],
        "heartbeat": row["heartbeat"],
        "prev_status": row["prev_status"],
        "revision": row["revision"],
        "extra": json.loads(row["extra_json"]),
    }


def get_task(conn: sqlite3.Connection, task_id: str) -> dict[str, Any] | None:
    row = conn.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
    return row_to_task(row) if row else None


def list_tasks(conn: sqlite3.Connection) -> list[dict[str, Any]]:
    rows = conn.execute(
        "SELECT * FROM tasks ORDER BY priority ASC, merge_order ASC, id ASC"
    ).fetchall()
    return [row_to_task(r) for r in rows]


def upsert_task(conn: sqlite3.Connection, task: dict[str, Any]) -> None:
    conn.execute(
        """
        INSERT INTO tasks(
            id, title, status, priority, merge_order, owner,
            deps_json, write_scope_json, read_scope_json, acceptance_json,
            branch, worktree, attempts, blocked_reason, heartbeat, prev_status,
            revision, extra_json
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            title=excluded.title,
            status=excluded.status,
            priority=excluded.priority,
            merge_order=excluded.merge_order,
            owner=excluded.owner,
            deps_json=excluded.deps_json,
            write_scope_json=excluded.write_scope_json,
            read_scope_json=excluded.read_scope_json,
            acceptance_json=excluded.acceptance_json,
            branch=excluded.branch,
            worktree=excluded.worktree,
            attempts=excluded.attempts,
            blocked_reason=excluded.blocked_reason,
            heartbeat=excluded.heartbeat,
            prev_status=excluded.prev_status,
            revision=excluded.revision,
            extra_json=excluded.extra_json
        """,
        (
            task["id"],
            task["title"],
            task["status"],
            task.get("priority", 100),
            task.get("merge_order", 100),
            task.get("owner"),
            json.dumps(task.get("deps") or []),
            json.dumps(task.get("write_scope") or []),
            json.dumps(task.get("read_scope") or []),
            json.dumps(task.get("acceptance") or []),
            task.get("branch"),
            task.get("worktree"),
            task.get("attempts", 0),
            task.get("blocked_reason"),
            task.get("heartbeat"),
            task.get("prev_status"),
            task.get("revision", 0),
            json.dumps(task.get("extra") or {}),
        ),
    )


def append_event(
    conn: sqlite3.Connection,
    *,
    actor: str,
    type_: str,
    task_id: str | None = None,
    data: dict[str, Any] | None = None,
) -> None:
    conn.execute(
        "INSERT INTO events(ts, actor, type, task_id, data_json) VALUES (?, ?, ?, ?, ?)",
        (utcnow(), actor, type_, task_id, json.dumps(data or {})),
    )


def set_meta(conn: sqlite3.Connection, key: str, value: str) -> None:
    conn.execute(
        "INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)",
        (key, value),
    )


def get_meta(conn: sqlite3.Connection, key: str, default: str | None = None) -> str | None:
    row = conn.execute("SELECT value FROM meta WHERE key = ?", (key,)).fetchone()
    return row["value"] if row else default
