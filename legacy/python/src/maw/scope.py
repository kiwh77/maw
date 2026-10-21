"""Write-scope expansion and overlap / out-of-scope checks."""

from __future__ import annotations

import fnmatch
from pathlib import Path, PurePosixPath

from maw.errors import MawError


def _norm(path: str) -> str:
    p = path.replace("\\", "/").lstrip("./")
    return str(PurePosixPath(p))


def expand_globs(patterns: list[str], root: Path) -> set[str]:
    """Expand globs against existing files; also keep directory-prefix patterns."""
    matched: set[str] = set()
    root = root.resolve()
    for pat in patterns:
        pat_n = _norm(pat)
        if pat_n.endswith("/**"):
            prefix = pat_n[:-3]
            if (root / prefix).exists():
                for f in (root / prefix).rglob("*"):
                    if f.is_file():
                        matched.add(_norm(str(f.relative_to(root))))
            matched.add(prefix.rstrip("/") + "/")
            continue
        # literal or simple glob
        if any(ch in pat_n for ch in "*?[]"):
            for f in root.rglob("*"):
                if not f.is_file():
                    continue
                rel = _norm(str(f.relative_to(root)))
                if fnmatch.fnmatch(rel, pat_n):
                    matched.add(rel)
        else:
            matched.add(pat_n)
            p = root / pat_n
            if p.is_dir():
                for f in p.rglob("*"):
                    if f.is_file():
                        matched.add(_norm(str(f.relative_to(root))))
    return matched


def scopes_overlap(a: list[str], b: list[str]) -> list[str]:
    """Detect overlapping write scopes via prefix / glob heuristics (no FS required)."""
    conflicts: list[str] = []
    for x in a:
        xn = _norm(x)
        for y in b:
            yn = _norm(y)
            if xn == yn:
                conflicts.append(xn)
                continue
            # directory prefix containment
            x_pref = xn[:-3] if xn.endswith("/**") else xn.rstrip("*").rstrip("/")
            y_pref = yn[:-3] if yn.endswith("/**") else yn.rstrip("*").rstrip("/")
            if not x_pref or not y_pref:
                continue
            if x_pref == y_pref or x_pref.startswith(y_pref + "/") or y_pref.startswith(x_pref + "/"):
                conflicts.append(f"{xn} ∩ {yn}")
            elif fnmatch.fnmatch(xn, yn) or fnmatch.fnmatch(yn, xn):
                conflicts.append(f"{xn} ∩ {yn}")
    return sorted(set(conflicts))


def path_in_scope(path: str, patterns: list[str]) -> bool:
    rel = _norm(path)
    for pat in patterns:
        pn = _norm(pat)
        if pn.endswith("/**"):
            prefix = pn[:-3].rstrip("/")
            if rel == prefix or rel.startswith(prefix + "/"):
                return True
            continue
        if any(ch in pn for ch in "*?[]"):
            if fnmatch.fnmatch(rel, pn):
                return True
        elif rel == pn or rel.startswith(pn.rstrip("/") + "/"):
            return True
    return False


def assert_files_in_scope(files: list[str], patterns: list[str], *, task_id: str) -> None:
    bad = [f for f in files if not path_in_scope(f, patterns)]
    if bad:
        raise MawError(
            f"task {task_id} out-of-scope files: {', '.join(bad[:8])}"
            + ("..." if len(bad) > 8 else ""),
            next_hint="run `maw request-change` or narrow the diff to write_scope",
        )
