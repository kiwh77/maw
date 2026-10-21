"""Git worktree / merge helpers. No force push / hard reset."""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

from maw.errors import MawError
from maw.home import run_git

# Set while maw merge commits to main so hooks allow the integrator path.
MERGE_ENV = "MAW_INTERNAL_MERGE"


def current_branch(cwd: Path) -> str:
    proc = run_git(["rev-parse", "--abbrev-ref", "HEAD"], cwd=cwd, check=False)
    if proc.returncode != 0:
        raise MawError("cannot determine current branch")
    return proc.stdout.strip()


def ensure_branch_base(repo: Path, main_branch: str) -> str:
    """Return ref to base off (prefer origin/main)."""
    for ref in (f"origin/{main_branch}", main_branch):
        proc = run_git(["rev-parse", "--verify", ref], cwd=repo, check=False)
        if proc.returncode == 0:
            return ref
    raise MawError(
        f"main branch {main_branch!r} not found",
        next_hint="create the branch or set project.main_branch in config.toml",
    )


def create_worktree(
    repo: Path,
    *,
    worktree: Path,
    branch: str,
    base: str,
) -> None:
    worktree.parent.mkdir(parents=True, exist_ok=True)
    if worktree.exists():
        return
    # delete stale branch if exists without worktree
    run_git(["show-ref", "--verify", f"refs/heads/{branch}"], cwd=repo, check=False)
    proc = run_git(
        ["worktree", "add", "-b", branch, str(worktree), base],
        cwd=repo,
        check=False,
    )
    if proc.returncode != 0:
        # branch may already exist
        proc2 = run_git(
            ["worktree", "add", str(worktree), branch],
            cwd=repo,
            check=False,
        )
        if proc2.returncode != 0:
            raise MawError(
                f"failed to create worktree: {proc.stderr or proc2.stderr}",
                next_hint="run `git worktree list` and clean stale entries",
            )


def changed_files(cwd: Path, main_branch: str) -> list[str]:
    base = ensure_branch_base(cwd, main_branch)
    # merge-base diff
    mb = run_git(["merge-base", base, "HEAD"], cwd=cwd, check=False)
    if mb.returncode != 0:
        base_ref = base
    else:
        base_ref = mb.stdout.strip()
    proc = run_git(["diff", "--name-only", f"{base_ref}...HEAD"], cwd=cwd, check=False)
    files = [ln.strip() for ln in proc.stdout.splitlines() if ln.strip()]
    # include unstaged/staged
    proc2 = run_git(["status", "--porcelain"], cwd=cwd, check=False)
    for ln in proc2.stdout.splitlines():
        if len(ln) < 4:
            continue
        path = ln[3:].strip()
        if " -> " in path:
            path = path.split(" -> ", 1)[1]
        if path and path not in files:
            files.append(path)
    return files


def squash_merge(repo: Path, *, branch: str, main_branch: str, message: str) -> None:
    run_git(["checkout", main_branch], cwd=repo)
    proc = run_git(
        ["merge", "--squash", branch],
        cwd=repo,
        check=False,
    )
    if proc.returncode != 0:
        run_git(["merge", "--abort"], cwd=repo, check=False)
        raise MawError(
            f"squash merge conflict for {branch}: {proc.stderr}",
            next_hint="inspect conflict files; maw will create a conflict task",
        )
    # commit if there is something staged
    staged = run_git(["diff", "--cached", "--quiet"], cwd=repo, check=False)
    if staged.returncode != 0:
        env = os.environ.copy()
        env[MERGE_ENV] = "1"
        proc = subprocess.run(
            ["git", "commit", "-m", message],
            cwd=repo,
            text=True,
            capture_output=True,
            env=env,
            check=False,
        )
        if proc.returncode != 0:
            raise MawError(
                f"merge commit failed: {proc.stderr or proc.stdout}",
                next_hint="inspect git status on main; ensure hooks allow MAW_INTERNAL_MERGE",
            )


def rebase_onto_main(worktree: Path, main_branch: str) -> None:
    base = ensure_branch_base(worktree, main_branch)
    proc = run_git(["rebase", base], cwd=worktree, check=False)
    if proc.returncode != 0:
        run_git(["rebase", "--abort"], cwd=worktree, check=False)
        raise MawError(
            f"rebase failed: {proc.stderr or proc.stdout}",
            next_hint="resolve conflicts in a conflict task; do not merge manually",
        )


def run_gates(commands: list[str], cwd: Path, timeout: int = 900) -> None:
    for cmd in commands:
        try:
            proc = subprocess.run(
                cmd,
                shell=True,
                cwd=cwd,
                text=True,
                capture_output=True,
                timeout=timeout,
                check=False,
            )
        except subprocess.TimeoutExpired as exc:
            raise MawError(f"gate timed out: {cmd}", next_hint="raise gates.timeout_sec or fix tests") from exc
        if proc.returncode != 0:
            raise MawError(
                f"gate failed: {cmd}\n{proc.stdout}\n{proc.stderr}",
                next_hint="fix failures then `maw submit` again",
            )
