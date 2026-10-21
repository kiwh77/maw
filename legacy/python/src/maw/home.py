"""Locate shared .multiagent home across worktrees."""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

from maw.errors import MawError

MULTIAGENT_DIR = ".multiagent"
DB_NAME = "maw.db"
CONFIG_NAME = "config.toml"
BLUEPRINT_NAME = "blueprint.toml"


def run_git(
    args: list[str], cwd: Path | None = None, check: bool = True
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args],
        cwd=cwd,
        text=True,
        capture_output=True,
        check=check,
    )


def repo_root(cwd: Path | None = None) -> Path:
    env_home = os.environ.get("MAW_HOME")
    if env_home:
        p = Path(env_home).expanduser().resolve()
        if p.name == MULTIAGENT_DIR:
            return p.parent
        # MAW_HOME may point at repo root
        if (p / MULTIAGENT_DIR).exists() or (p / ".git").exists():
            return p
    try:
        proc = run_git(["rev-parse", "--path-format=absolute", "--git-common-dir"], cwd=cwd)
    except (subprocess.CalledProcessError, FileNotFoundError) as exc:
        raise MawError(
            "not inside a git repository",
            next_hint="cd into a git repo, then run `maw init`",
        ) from exc
    common = Path(proc.stdout.strip())
    if common.name == ".git":
        return common.parent
    proc2 = run_git(
        ["rev-parse", "--path-format=absolute", "--show-toplevel"], cwd=cwd, check=False
    )
    if proc2.returncode == 0 and proc2.stdout.strip():
        return Path(proc2.stdout.strip())
    return common.parent


def maw_home(cwd: Path | None = None) -> Path:
    env = os.environ.get("MAW_HOME")
    if env:
        p = Path(env).expanduser().resolve()
        if p.name == MULTIAGENT_DIR:
            return p
        candidate = p / MULTIAGENT_DIR
        if candidate.exists() or not (p / DB_NAME).exists():
            return candidate
        return p
    return repo_root(cwd) / MULTIAGENT_DIR


def require_home(cwd: Path | None = None) -> Path:
    home = maw_home(cwd)
    if not home.exists():
        raise MawError(
            f"maw not initialized ({home} missing)",
            next_hint="run `maw init` in the repository root",
        )
    return home


def db_path(home: Path) -> Path:
    return home / DB_NAME


def config_path(home: Path) -> Path:
    return home / CONFIG_NAME


def blueprint_path(home: Path) -> Path:
    return home / BLUEPRINT_NAME


def worktree_root(cwd: Path | None = None) -> Path:
    proc = run_git(["rev-parse", "--path-format=absolute", "--show-toplevel"], cwd=cwd, check=False)
    if proc.returncode != 0:
        raise MawError("not inside a git worktree", next_hint="run from a git checkout")
    return Path(proc.stdout.strip())
