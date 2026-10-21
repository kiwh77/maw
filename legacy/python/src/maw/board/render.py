"""Terminal and HTML board renderers."""

from __future__ import annotations

import html
import json
from collections import Counter
from pathlib import Path
from typing import Any


COLUMNS = [
    ("backlog", "BACKLOG"),
    ("ready", "READY"),
    ("claimed", "CLAIMED"),
    ("in_progress", "IN PROGRESS"),
    ("self_check", "SELF CHECK"),
    ("in_review", "IN REVIEW"),
    ("merging", "MERGING"),
    ("conflict", "CONFLICT"),
    ("done", "DONE"),
]


def _by_status(tasks: list[dict[str, Any]]) -> dict[str, list[dict[str, Any]]]:
    d: dict[str, list[dict[str, Any]]] = {}
    for t in tasks:
        d.setdefault(t["status"], []).append(t)
    return d


def render_terminal(tasks: list[dict[str, Any]], *, max_parallel: int = 4) -> str:
    by = _by_status(tasks)
    done = len(by.get("done", []))
    total = len(tasks) or 1
    pct = int(100 * done / total)
    bar_len = 12
    filled = int(bar_len * done / total)
    bar = "█" * filled + "░" * (bar_len - filled)
    active = sum(
        len(by.get(s, []))
        for s in ("claimed", "in_progress", "self_check", "in_review", "merging")
    )
    blocked = by.get("blocked", [])
    lines = [
        f"项目进度 {bar} {done}/{len(tasks)} ({pct}%)   并行 {active}/{max_parallel}   "
        f"阻塞 {len(blocked)}",
        "",
    ]
    # compact columns
    col_parts = []
    for key, label in COLUMNS:
        items = by.get(key, [])
        if key in ("claimed", "self_check") and not items:
            continue
        if key == "cancelled":
            continue
        col_parts.append(f"{label}({len(items)})")
    lines.append("  ".join(col_parts))
    for key, label in (
        ("ready", "READY"),
        ("in_progress", "IN PROGRESS"),
        ("in_review", "IN REVIEW"),
        ("merging", "MERGING"),
        ("done", "DONE"),
    ):
        items = by.get(key, [])
        if not items and key == "done":
            continue
        for t in items[:6]:
            owner = t.get("owner") or "-"
            hb = t.get("heartbeat") or ""
            lines.append(f"  {t['id']}  {t['title'][:40]}  {owner}  {hb}")
    if blocked:
        lines.append("BLOCKED:")
        for t in blocked:
            lines.append(f"  {t['id']} ({t.get('blocked_reason') or 'no reason'})")
    conflicts = by.get("conflict", [])
    if conflicts:
        lines.append("CONFLICT:")
        for t in conflicts:
            lines.append(f"  {t['id']} — needs author fix then resubmit")
    # NEXT
    merging = by.get("merging", []) or by.get("in_review", [])
    if merging:
        lines.append(f"NEXT: run `maw merge` (or `maw review --approve {merging[0]['id']}`)")
    elif by.get("ready"):
        lines.append("NEXT: workers run `maw next`")
    else:
        lines.append("NEXT: apply a blueprint or wait for deps")
    return "\n".join(lines)


def render_html(tasks: list[dict[str, Any]], *, max_parallel: int = 4) -> str:
    by = _by_status(tasks)
    counts = Counter(t["status"] for t in tasks)
    done = counts.get("done", 0)
    total = len(tasks)
    data_json = html.escape(json.dumps(tasks, ensure_ascii=False), quote=True)
    cards = []
    for key, label in COLUMNS:
        if key in ("claimed", "self_check", "cancelled") and not by.get(key):
            continue
        items = by.get(key, [])
        body = "".join(
            f'<div class="card" data-id="{html.escape(t["id"])}">'
            f'<strong>{html.escape(t["id"])}</strong> {html.escape(t["title"])}'
            f'<div class="meta">owner={html.escape(str(t.get("owner") or "-"))} '
            f'♥ {html.escape(str(t.get("heartbeat") or "-"))}</div>'
            f'<button onclick="navigator.clipboard.writeText('
            f"'maw task show {t['id']}')\">复制 show</button>"
            f"</div>"
            for t in items
        )
        cards.append(f'<section><h2>{label} ({len(items)})</h2>{body or "<p class=empty>—</p>"}</section>')
    blocked = by.get("blocked", [])
    risk = []
    for t in by.get("conflict", []):
        risk.append(f"{t['id']} conflict")
    for t in blocked:
        risk.append(f"{t['id']} blocked: {t.get('blocked_reason')}")
    return f"""<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8"/>
<title>maw board</title>
<style>
body {{ font-family: ui-sans-serif, system-ui, sans-serif; margin: 1.5rem; background: #0f1419; color: #e7ecf1; }}
h1 {{ font-size: 1.25rem; }}
.grid {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 1rem; }}
section {{ background: #1a2332; border-radius: 8px; padding: .75rem; }}
.card {{ background: #243044; margin: .4rem 0; padding: .5rem; border-radius: 6px; font-size: .85rem; }}
.meta {{ opacity: .7; font-size: .75rem; margin: .25rem 0; }}
button {{ font-size: .7rem; cursor: pointer; }}
.risk {{ background: #3a2218; padding: .75rem; border-radius: 8px; margin-bottom: 1rem; }}
</style>
</head>
<body>
<h1>maw board — {done}/{total} done · parallel cap {max_parallel}</h1>
<div class="risk"><strong>RISK / NEXT</strong><ul>
{''.join(f'<li>{html.escape(r)}</li>' for r in risk) or '<li>无阻塞</li>'}
<li>合入：export MAW_AGENT=int && maw merge</li>
</ul></div>
<div class="grid">
{''.join(cards)}
</div>
<!-- data: {data_json} -->
</body>
</html>
"""


def write_board(home: Path, tasks: list[dict[str, Any]], config: dict[str, Any]) -> Path:
    max_p = int((config.get("project") or {}).get("max_parallel") or 4)
    board_dir = home / "board"
    board_dir.mkdir(parents=True, exist_ok=True)
    html_path = board_dir / "index.html"
    html_path.write_text(render_html(tasks, max_parallel=max_p), encoding="utf-8")
    md_path = board_dir / "BOARD.md"
    md_path.write_text("```\n" + render_terminal(tasks, max_parallel=max_p) + "\n```\n", encoding="utf-8")
    return html_path
