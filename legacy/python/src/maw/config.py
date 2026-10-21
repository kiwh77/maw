"""Load/save config.toml (stdlib tomllib)."""

from __future__ import annotations

import tomllib
from pathlib import Path
from typing import Any

from maw.errors import MawError

DEFAULT_CONFIG = """\
[project]
main_branch = "main"
merge_strategy = "squash"
max_parallel = 4
blueprint = ".multiagent/blueprint.toml"

[approvals]
plan = "manual"
merge = "manual"
teardown = "manual"
review = "auto"

[gates]
commands = {gates}
timeout_sec = 900

[locks]
ttl_sec = 1800
heartbeat_ttl_sec = 900

[launcher]
mode = "manual"

[identity]
workers = ["w1", "w2", "w3"]
reviewers = ["r1"]
integrator = "int"
"""


def detect_gates(repo: Path) -> list[str]:
    if (repo / "package.json").exists():
        cmds = []
        text = (repo / "package.json").read_text(encoding="utf-8")
        if '"lint"' in text:
            cmds.append("npm run lint")
        if '"typecheck"' in text:
            cmds.append("npm run typecheck")
        if '"test"' in text:
            cmds.append("npm test")
        return cmds or ["npm test"]
    if (repo / "go.mod").exists():
        return ["go test ./..."]
    if (repo / "pyproject.toml").exists() or (repo / "pytest.ini").exists():
        return ["pytest"]
    if (repo / "Cargo.toml").exists():
        return ["cargo test"]
    return []


def detect_main_branch(repo: Path) -> str:
    from maw.home import run_git

    proc = run_git(["symbolic-ref", "refs/remotes/origin/HEAD"], cwd=repo, check=False)
    if proc.returncode == 0 and proc.stdout.strip():
        ref = proc.stdout.strip().split("/")[-1]
        if ref:
            return ref
    for name in ("main", "master"):
        proc = run_git(["show-ref", "--verify", f"refs/heads/{name}"], cwd=repo, check=False)
        if proc.returncode == 0:
            return name
    return "main"


def render_default_config(repo: Path) -> str:
    gates = detect_gates(repo)
    if gates:
        gate_lit = "[" + ", ".join(f'"{g}"' for g in gates) + "]"
    else:
        gate_lit = "[]"
    text = DEFAULT_CONFIG.replace("{gates}", gate_lit)
    main = detect_main_branch(repo)
    return text.replace('main_branch = "main"', f'main_branch = "{main}"', 1)


def load_config(path: Path) -> dict[str, Any]:
    if not path.is_file():
        raise MawError(
            f"config not found: {path}",
            next_hint="run `maw init`",
        )
    with path.open("rb") as f:
        return tomllib.load(f)


def gates_commands(config: dict[str, Any]) -> list[str]:
    gates = config.get("gates") or {}
    return list(gates.get("commands") or [])
