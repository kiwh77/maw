"""Install git hooks that call back into maw."""

from __future__ import annotations

import os
import stat
from pathlib import Path

from maw.home import run_git


PRE_COMMIT = """#!/bin/sh
# maw pre-commit — scope check
if command -v maw >/dev/null 2>&1; then
  exec maw hook pre-commit
fi
# fallback: run via python module if maw not on PATH
ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
if [ -n "$ROOT" ] && [ -f "$ROOT/.multiagent/maw_pythonpath" ]; then
  export PYTHONPATH="$(cat "$ROOT/.multiagent/maw_pythonpath"):$PYTHONPATH"
  exec python3 -m maw hook pre-commit
fi
exit 0
"""

PRE_PUSH = """#!/bin/sh
# maw pre-push — block main / force
if command -v maw >/dev/null 2>&1; then
  exec maw hook pre-push
fi
ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
if [ -n "$ROOT" ] && [ -f "$ROOT/.multiagent/maw_pythonpath" ]; then
  export PYTHONPATH="$(cat "$ROOT/.multiagent/maw_pythonpath"):$PYTHONPATH"
  exec python3 -m maw hook pre-push
fi
exit 0
"""


def hooks_dir(repo: Path) -> Path:
    proc = run_git(["rev-parse", "--git-path", "hooks"], cwd=repo)
    return Path(proc.stdout.strip())


def _write_hook(path: Path, content: str) -> None:
    path.write_text(content, encoding="utf-8")
    path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


def install_hooks(repo: Path, *, pythonpath: Path) -> list[str]:
    hdir = hooks_dir(repo)
    hdir.mkdir(parents=True, exist_ok=True)
    written = []
    for name, content in (("pre-commit", PRE_COMMIT), ("pre-push", PRE_PUSH)):
        target = hdir / name
        _write_hook(target, content)
        written.append(str(target))
    # remember PYTHONPATH for fallback
    home = repo / ".multiagent"
    home.mkdir(parents=True, exist_ok=True)
    (home / "maw_pythonpath").write_text(str(pythonpath.resolve()), encoding="utf-8")
    # also copy into .multiagent/hooks for reference
    src_hooks = home / "hooks"
    src_hooks.mkdir(parents=True, exist_ok=True)
    (src_hooks / "pre-commit").write_text(PRE_COMMIT, encoding="utf-8")
    (src_hooks / "pre-push").write_text(PRE_PUSH, encoding="utf-8")
    return written


def ensure_gitignore(repo: Path) -> bool:
    gi = repo / ".gitignore"
    entries = [".multiagent/", ".worktrees/"]
    existing = gi.read_text(encoding="utf-8") if gi.exists() else ""
    lines = existing.splitlines()
    changed = False
    for e in entries:
        if e not in lines and e.rstrip("/") not in lines:
            lines.append(e)
            changed = True
    if changed:
        text = "\n".join(lines)
        if text and not text.endswith("\n"):
            text += "\n"
        gi.write_text(text, encoding="utf-8")
    return changed
