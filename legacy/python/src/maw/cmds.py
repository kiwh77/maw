"""Command implementations for maw M1."""

from __future__ import annotations

import json
import shutil
from pathlib import Path
from typing import Any

from maw import PROTOCOL_VERSION, __version__
from maw.blueprint import (
    EXAMPLE_BLUEPRINT,
    load_blueprint,
    tasks_for_db,
    validate_blueprint,
)
from maw.board.render import render_terminal, write_board
from maw.config import gates_commands, load_config, render_default_config
from maw.db import (
    append_event,
    connect,
    get_meta,
    get_task,
    init_db,
    list_tasks,
    set_meta,
    transaction,
    upsert_task,
    utcnow,
)
from maw.errors import MawError
from maw.gitops import (
    changed_files,
    create_worktree,
    current_branch,
    ensure_branch_base,
    rebase_onto_main,
    run_gates,
    squash_merge,
)
from maw.home import (
    MULTIAGENT_DIR,
    blueprint_path,
    config_path,
    db_path,
    maw_home,
    repo_root,
    require_home,
    run_git,
    worktree_root,
)
from maw.hooks_install import ensure_gitignore, install_hooks
from maw.identity import require_integrator, require_owner, resolve_agent, role_of
from maw.machine import ACTIVE_OWNER_STATUSES, assert_transition
from maw.protocol import install_protocol_files, read_protocol_version
from maw.scope import assert_files_in_scope, path_in_scope


def _refresh_board(home: Path) -> Path:
    cfg = load_config(config_path(home))
    with connect(db_path(home)) as conn:
        tasks = list_tasks(conn)
    return write_board(home, tasks, cfg)


def _write_task_card(wt: Path, task: dict[str, Any], agent: str) -> None:
    maw_dir = wt / ".maw"
    maw_dir.mkdir(parents=True, exist_ok=True)
    (maw_dir / "agent").write_text(agent + "\n", encoding="utf-8")
    (maw_dir / "task.json").write_text(
        json.dumps(task, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    acceptance = "\n".join(f"- {a}" for a in task.get("acceptance") or [])
    scope = "\n".join(f"- `{s}`" for s in task.get("write_scope") or [])
    (wt / "TASK.md").write_text(
        f"""# {task['id']}: {task['title']}

## Owner
`{agent}`

## Write scope
{scope}

## Acceptance
{acceptance or '- (none)'}

## Protocol
Run `maw progress -m "..."`, then `maw submit`. Do not merge to main.
""",
        encoding="utf-8",
    )


def _infer_task_id(cwd: Path | None, home: Path) -> str:
    try:
        root = worktree_root(cwd)
    except MawError:
        root = Path.cwd() if cwd is None else cwd
    marker = root / ".maw" / "task.json"
    if marker.is_file():
        data = json.loads(marker.read_text(encoding="utf-8"))
        return data["id"]
    raise MawError(
        "cannot infer task from worktree",
        next_hint="cd into the task worktree from `maw next`, or pass --task",
    )


def cmd_init(*, cwd: Path | None, dry_run: bool, upgrade: bool) -> dict[str, Any]:
    repo = repo_root(cwd)
    home = repo / MULTIAGENT_DIR
    src_root = Path(__file__).resolve().parent.parent  # .../src
    plan = {
        "repo": str(repo),
        "home": str(home),
        "create": [
            str(home / "maw.db"),
            str(home / "config.toml"),
            str(home / "blueprint.toml"),
            str(home / "board"),
            str(home / "handoffs"),
            "AGENTS.md protocol",
            "git hooks pre-commit/pre-push",
            ".gitignore entries",
        ],
    }
    if dry_run:
        plan["dry_run"] = True
        return plan

    home.mkdir(parents=True, exist_ok=True)
    (home / "board").mkdir(exist_ok=True)
    (home / "handoffs").mkdir(exist_ok=True)
    (home / "DECISIONS.md").write_text("# Decisions\n", encoding="utf-8")

    cfg_file = config_path(home)
    if not cfg_file.exists() or upgrade:
        cfg_file.write_text(render_default_config(repo), encoding="utf-8")

    bp = blueprint_path(home)
    if not bp.exists():
        bp.write_text(EXAMPLE_BLUEPRINT, encoding="utf-8")

    init_db(db_path(home))
    with transaction(db_path(home)) as conn:
        set_meta(conn, "protocol_version", str(PROTOCOL_VERSION))
        set_meta(conn, "maw_version", __version__)

    proto = install_protocol_files(repo)
    ensure_gitignore(repo)
    hooks = install_hooks(repo, pythonpath=src_root)

    advice = [
        "Protect main branch: require PR, disallow force push",
        "Add CI job running project gates from config.toml",
        "Do not grant workers push access to main",
    ]
    doctor = cmd_doctor(cwd=cwd)
    return {
        "ok": True,
        "home": str(home),
        "protocol": proto,
        "hooks": hooks,
        "branch_protection_advice": advice,
        "doctor": doctor,
        "next": "edit .multiagent/blueprint.toml then `maw plan apply .multiagent/blueprint.toml --yes`",
    }


def cmd_doctor(*, cwd: Path | None) -> dict[str, Any]:
    repo = repo_root(cwd)
    home = maw_home(cwd)
    issues: list[str] = []
    ok = True
    if not home.exists():
        return {"ok": False, "issues": ["not initialized"], "next": "maw init"}
    if not db_path(home).exists():
        issues.append("maw.db missing")
        ok = False
    else:
        with connect(db_path(home)) as conn:
            pv = get_meta(conn, "protocol_version")
            if pv and int(pv) != PROTOCOL_VERSION:
                issues.append(f"db protocol {pv} != code {PROTOCOL_VERSION}")
                ok = False
    file_ver = read_protocol_version(repo / "AGENTS.md")
    if file_ver is None:
        issues.append("AGENTS.md protocol segment missing")
        ok = False
    elif file_ver != PROTOCOL_VERSION:
        issues.append(f"AGENTS.md protocol v{file_ver} != v{PROTOCOL_VERSION}")
        ok = False
    hdir = Path(run_git(["rev-parse", "--git-path", "hooks"], cwd=repo).stdout.strip())
    for name in ("pre-commit", "pre-push"):
        if not (hdir / name).exists():
            issues.append(f"hook missing: {name}")
            ok = False
    return {
        "ok": ok,
        "issues": issues,
        "maw_version": __version__,
        "protocol_version": PROTOCOL_VERSION,
        "next": "maw init --upgrade" if not ok else "maw board",
    }


def cmd_plan_check(*, path: Path, cwd: Path | None) -> dict[str, Any]:
    data = load_blueprint(path)
    errors = validate_blueprint(data)
    return {
        "ok": not errors,
        "errors": errors,
        "task_count": len(data.get("_tasks") or []),
        "next": "fix blueprint errors" if errors else f"maw plan apply {path} --yes",
    }


def cmd_plan_apply(
    *, path: Path, cwd: Path | None, yes: bool, dry_run: bool
) -> dict[str, Any]:
    home = require_home(cwd)
    cfg = load_config(config_path(home))
    data = load_blueprint(path)
    errors = validate_blueprint(data)
    if errors:
        raise MawError(
            "blueprint validation failed:\n- " + "\n- ".join(errors),
            next_hint="fix write_scope overlaps / deps, then re-run plan apply",
        )
    tasks = tasks_for_db(data)
    if dry_run:
        return {"ok": True, "dry_run": True, "tasks": tasks}
    approvals = (cfg.get("approvals") or {}).get("plan", "manual")
    if approvals == "manual" and not yes:
        raise MawError(
            "plan apply requires confirmation",
            next_hint=f"review tasks then re-run with --yes: maw plan apply {path} --yes",
        )
    # copy blueprint into home
    dest = blueprint_path(home)
    if path.resolve() != dest.resolve():
        shutil.copy2(path, dest)
    agent = "orchestrator"
    try:
        agent = resolve_agent(cwd)
    except MawError:
        pass
    with transaction(db_path(home)) as conn:
        # clear and reload tasks for M1 simplicity
        conn.execute("DELETE FROM tasks")
        conn.execute("DELETE FROM shared_files")
        for t in tasks:
            # deps not done -> backlog; promote ready if deps empty OR all deps done
            upsert_task(conn, t)
            append_event(conn, actor=agent, type_="task_created", task_id=t["id"], data={"status": t["status"]})
        for sf in data.get("_shared") or []:
            conn.execute(
                "INSERT OR REPLACE INTO shared_files(path, owner_task) VALUES (?, ?)",
                (sf["path"], sf["owner_task"]),
            )
        _promote_ready(conn)
        append_event(conn, actor=agent, type_="plan_applied", data={"path": str(path)})
        tasks_out = list_tasks(conn)
    html = _refresh_board(home)
    return {
        "ok": True,
        "tasks": tasks_out,
        "board": str(html),
        "next": "export MAW_AGENT=w1 && maw next",
    }


def _promote_ready(conn) -> None:
    tasks = list_tasks(conn)
    done = {t["id"] for t in tasks if t["status"] == "done"}
    for t in tasks:
        if t["status"] not in ("backlog", "ready"):
            continue
        deps = t.get("deps") or []
        if all(d in done for d in deps):
            if t["status"] != "ready":
                assert_transition(t["status"], "ready")
                t["status"] = "ready"
                t["revision"] = t.get("revision", 0) + 1
                upsert_task(conn, t)


def cmd_next(*, cwd: Path | None, agent: str | None, dry_run: bool) -> dict[str, Any]:
    home = require_home(cwd)
    repo = repo_root(cwd)
    cfg = load_config(config_path(home))
    who = agent or resolve_agent(cwd)
    max_p = int((cfg.get("project") or {}).get("max_parallel") or 4)
    main_branch = (cfg.get("project") or {}).get("main_branch") or "main"

    with transaction(db_path(home)) as conn:
        # idempotent: return existing active task for this agent
        for t in list_tasks(conn):
            if t.get("owner") == who and t["status"] in ACTIVE_OWNER_STATUSES:
                return {
                    "ok": True,
                    "idempotent": True,
                    "task": t,
                    "worktree": t.get("worktree"),
                    "next": f"cd {t.get('worktree')} && continue work; maw progress -m '...'; maw submit",
                }
        active = sum(1 for t in list_tasks(conn) if t["status"] in ACTIVE_OWNER_STATUSES)
        if active >= max_p:
            raise MawError(
                f"max_parallel={max_p} reached",
                next_hint="wait for submit/merge, or raise project.max_parallel",
            )
        row = conn.execute(
            """
            SELECT id FROM tasks
            WHERE status = 'ready'
            ORDER BY priority ASC, merge_order ASC, id ASC
            LIMIT 1
            """
        ).fetchone()
        if not row:
            raise MawError(
                "no ready tasks",
                next_hint="run `maw board`; finish deps or `maw plan apply`",
            )
        task = get_task(conn, row["id"])
        assert task
        if dry_run:
            return {"ok": True, "dry_run": True, "task": task}

        assert_transition(task["status"], "in_progress")
        append_event(conn, actor=who, type_="claimed", task_id=task["id"])
        branch = f"agent/{who}/{task['id']}"
        wt_rel = f".worktrees/{who}-{task['id']}"
        wt = repo / wt_rel
        base = ensure_branch_base(repo, main_branch)
        create_worktree(repo, worktree=wt, branch=branch, base=base)
        task["status"] = "in_progress"
        task["owner"] = who
        task["branch"] = branch
        task["worktree"] = wt_rel
        task["heartbeat"] = utcnow()
        task["revision"] = task.get("revision", 0) + 1
        task["attempts"] = task.get("attempts", 0) + 1
        upsert_task(conn, task)
        append_event(
            conn,
            actor=who,
            type_="status_changed",
            task_id=task["id"],
            data={"to": "in_progress"},
        )
        _write_task_card(wt, task, who)

    _refresh_board(home)
    return {
        "ok": True,
        "task": task,
        "worktree": str(repo / wt_rel),
        "branch": branch,
        "env": {"MAW_AGENT": who, "MAW_TASK": task["id"]},
        "next": f"cd {repo / wt_rel} && implement within write_scope; maw progress -m '...'; maw submit",
    }


def cmd_progress(*, cwd: Path | None, message: str, task_id: str | None) -> dict[str, Any]:
    home = require_home(cwd)
    who = resolve_agent(cwd)
    tid = task_id or _infer_task_id(cwd, home)
    with transaction(db_path(home)) as conn:
        task = get_task(conn, tid)
        if not task:
            raise MawError(f"unknown task {tid}")
        require_owner(who, task)
        task["heartbeat"] = utcnow()
        task["revision"] = task.get("revision", 0) + 1
        upsert_task(conn, task)
        append_event(
            conn,
            actor=who,
            type_="progress",
            task_id=tid,
            data={"message": message},
        )
    return {"ok": True, "task_id": tid, "heartbeat": task["heartbeat"], "next": "continue or `maw submit`"}


def cmd_submit(*, cwd: Path | None, task_id: str | None, dry_run: bool) -> dict[str, Any]:
    home = require_home(cwd)
    repo = repo_root(cwd)
    cfg = load_config(config_path(home))
    who = resolve_agent(cwd)
    tid = task_id or _infer_task_id(cwd, home)
    main_branch = (cfg.get("project") or {}).get("main_branch") or "main"
    try:
        wt = worktree_root(cwd)
    except MawError:
        wt = repo

    with transaction(db_path(home)) as conn:
        task = get_task(conn, tid)
        if not task:
            raise MawError(f"unknown task {tid}")
        require_owner(who, task)
        if task["status"] not in ("in_progress", "self_check", "conflict"):
            raise MawError(
                f"task {tid} status {task['status']} cannot submit",
                next_hint="only in_progress tasks can be submitted",
            )
        files = [
            f
            for f in changed_files(wt, main_branch)
            if f not in ("TASK.md",) and not f.startswith(".maw/")
        ]
        assert_files_in_scope(files, task.get("write_scope") or [], task_id=tid)
        if dry_run:
            return {"ok": True, "dry_run": True, "files": files, "task": task}

        review_mode = (cfg.get("approvals") or {}).get("review", "auto")
        assert_transition(task["status"], "in_review")
        task["status"] = "in_review"
        task["heartbeat"] = utcnow()
        task["revision"] = task.get("revision", 0) + 1
        upsert_task(conn, task)
        append_event(conn, actor=who, type_="status_changed", task_id=tid, data={"to": "in_review", "files": files})
        if review_mode == "auto":
            assert_transition("in_review", "merging")
            task["status"] = "merging"
            task["revision"] += 1
            upsert_task(conn, task)
            append_event(conn, actor="system", type_="review_auto_approved", task_id=tid)

    _refresh_board(home)
    nxt = "export MAW_AGENT=int && maw merge" if review_mode == "auto" else f"maw review --approve {tid}"
    return {"ok": True, "task_id": tid, "files": files, "status": task["status"], "next": nxt}


def cmd_review(
    *, cwd: Path | None, task_id: str, approve: bool, message: str
) -> dict[str, Any]:
    home = require_home(cwd)
    cfg = load_config(config_path(home))
    who = resolve_agent(cwd)
    with transaction(db_path(home)) as conn:
        task = get_task(conn, tid := task_id)
        if not task:
            raise MawError(f"unknown task {tid}")
        if task.get("owner") == who:
            raise MawError("author cannot review own task", next_hint="use another MAW_AGENT")
        if task["status"] != "in_review":
            raise MawError(f"task not in_review ({task['status']})")
        if approve:
            assert_transition("in_review", "merging")
            task["status"] = "merging"
            append_event(conn, actor=who, type_="review_result", task_id=tid, data={"result": "approve", "message": message})
        else:
            assert_transition("in_review", "in_progress")
            task["status"] = "in_progress"
            append_event(
                conn,
                actor=who,
                type_="review_result",
                task_id=tid,
                data={"result": "request_changes", "message": message},
            )
        task["revision"] = task.get("revision", 0) + 1
        upsert_task(conn, task)
    _refresh_board(home)
    return {
        "ok": True,
        "task_id": tid,
        "status": task["status"],
        "next": "maw merge" if approve else "author: fix and maw submit",
    }


def cmd_merge(*, cwd: Path | None, yes: bool, dry_run: bool) -> dict[str, Any]:
    home = require_home(cwd)
    repo = repo_root(cwd)
    cfg = load_config(config_path(home))
    who = resolve_agent(cwd)
    require_integrator(who, cfg)
    approvals = (cfg.get("approvals") or {}).get("merge", "manual")
    if approvals == "manual" and not yes:
        raise MawError(
            "merge requires confirmation",
            next_hint="run `maw merge --yes`",
        )
    main_branch = (cfg.get("project") or {}).get("main_branch") or "main"
    gates = gates_commands(cfg)
    timeout = int((cfg.get("gates") or {}).get("timeout_sec") or 900)
    results: list[dict[str, Any]] = []

    with transaction(db_path(home)) as conn:
        queue = [
            t
            for t in list_tasks(conn)
            if t["status"] == "merging"
        ]
        queue.sort(key=lambda t: (t.get("merge_order", 100), t.get("priority", 100), t["id"]))
        if not queue:
            raise MawError("merge queue empty", next_hint="wait for submit/review")
        if dry_run:
            return {"ok": True, "dry_run": True, "queue": [t["id"] for t in queue]}

    for task in queue:
        tid = task["id"]
        wt = repo / (task.get("worktree") or "")
        branch = task.get("branch") or ""
        try:
            if wt.exists():
                rebase_onto_main(wt, main_branch)
            # run gates in worktree if present else repo
            gate_cwd = wt if wt.exists() else repo
            if gates:
                run_gates(gates, gate_cwd, timeout=timeout)
            # merge on main checkout (repo root must be main worktree)
            # Prefer operating from repo root
            cur = current_branch(repo)
            if cur != main_branch:
                run_git(["checkout", main_branch], cwd=repo, check=False)
            squash_merge(
                repo,
                branch=branch,
                main_branch=main_branch,
                message=f"maw: merge {tid} {task.get('title')}",
            )
            with transaction(db_path(home)) as conn:
                t = get_task(conn, tid)
                assert t
                assert_transition(t["status"], "done")
                t["status"] = "done"
                t["revision"] = t.get("revision", 0) + 1
                upsert_task(conn, t)
                append_event(conn, actor=who, type_="merged", task_id=tid)
                append_event(conn, actor=who, type_="gate_passed", task_id=tid)
                _promote_ready(conn)
            results.append({"id": tid, "status": "done"})
        except MawError as exc:
            with transaction(db_path(home)) as conn:
                t = get_task(conn, tid)
                assert t
                t["status"] = "conflict"
                t["blocked_reason"] = exc.message[:500]
                t["attempts"] = t.get("attempts", 0) + 1
                t["revision"] = t.get("revision", 0) + 1
                upsert_task(conn, t)
                append_event(
                    conn,
                    actor=who,
                    type_="gate_failed",
                    task_id=tid,
                    data={"error": exc.message},
                )
                # create conflict follow-up note in handoffs
                note = home / "handoffs" / f"{tid}-conflict.md"
                note.write_text(
                    f"# Conflict: {tid}\n\n{exc.message}\n\nNext: owner fixes in worktree, then `maw submit`\n",
                    encoding="utf-8",
                )
            results.append({"id": tid, "status": "conflict", "error": exc.message})
            break  # serial queue stops on first failure

    html = _refresh_board(home)
    return {
        "ok": all(r.get("status") == "done" for r in results),
        "results": results,
        "board": str(html),
        "next": "maw board" if results else "nothing to merge",
    }


def cmd_board(*, cwd: Path | None, html_only: bool = False) -> dict[str, Any]:
    home = require_home(cwd)
    cfg = load_config(config_path(home))
    with connect(db_path(home)) as conn:
        tasks = list_tasks(conn)
    html_path = write_board(home, tasks, cfg)
    max_p = int((cfg.get("project") or {}).get("max_parallel") or 4)
    term = render_terminal(tasks, max_parallel=max_p)
    return {
        "ok": True,
        "terminal": term,
        "html": str(html_path),
        "counts": {s: sum(1 for t in tasks if t["status"] == s) for s in {t["status"] for t in tasks}},
        "next": "open HTML board or run workers / merge as indicated",
    }


def cmd_task_list(*, cwd: Path | None) -> dict[str, Any]:
    home = require_home(cwd)
    with connect(db_path(home)) as conn:
        return {"ok": True, "tasks": list_tasks(conn)}


def cmd_task_show(*, cwd: Path | None, task_id: str) -> dict[str, Any]:
    home = require_home(cwd)
    with connect(db_path(home)) as conn:
        t = get_task(conn, task_id)
    if not t:
        raise MawError(f"unknown task {task_id}")
    return {"ok": True, "task": t}


def cmd_block(*, cwd: Path | None, message: str, task_id: str | None) -> dict[str, Any]:
    home = require_home(cwd)
    who = resolve_agent(cwd)
    tid = task_id or _infer_task_id(cwd, home)
    with transaction(db_path(home)) as conn:
        task = get_task(conn, tid)
        if not task:
            raise MawError(f"unknown task {tid}")
        require_owner(who, task)
        task["prev_status"] = task["status"]
        task["status"] = "blocked"
        task["blocked_reason"] = message
        task["revision"] = task.get("revision", 0) + 1
        upsert_task(conn, task)
        append_event(conn, actor=who, type_="status_changed", task_id=tid, data={"to": "blocked", "reason": message})
    _refresh_board(home)
    return {"ok": True, "task_id": tid, "next": "orchestrator: maw task unblock " + tid}


def cmd_unblock(*, cwd: Path | None, task_id: str) -> dict[str, Any]:
    home = require_home(cwd)
    with transaction(db_path(home)) as conn:
        task = get_task(conn, task_id)
        if not task:
            raise MawError(f"unknown task {task_id}")
        prev = task.get("prev_status") or "ready"
        task["status"] = prev
        task["blocked_reason"] = None
        task["prev_status"] = None
        task["revision"] = task.get("revision", 0) + 1
        upsert_task(conn, task)
        append_event(conn, actor="orchestrator", type_="status_changed", task_id=task_id, data={"to": prev})
    _refresh_board(home)
    return {"ok": True, "task_id": task_id, "status": prev}


def cmd_hook_pre_commit(*, cwd: Path | None) -> dict[str, Any]:
    """Enforce write_scope on staged files when inside a task worktree."""
    import os

    if os.environ.get("MAW_INTERNAL_MERGE") == "1":
        return {"ok": True, "internal_merge": True}
    try:
        root = worktree_root(cwd)
    except MawError:
        return {"ok": True, "skipped": True}
    marker = root / ".maw" / "task.json"
    if not marker.is_file():
        # not a maw worktree
        home = maw_home(cwd)
        if home.exists():
            cfg = load_config(config_path(home))
            main = (cfg.get("project") or {}).get("main_branch") or "main"
            if current_branch(root) == main:
                raise MawError(
                    "commits on main branch are blocked by maw hook",
                    next_hint="run `maw next` and work in the task worktree",
                )
        return {"ok": True, "skipped": True}
    task = json.loads(marker.read_text(encoding="utf-8"))
    proc = run_git(["diff", "--cached", "--name-only"], cwd=root, check=False)
    files = [
        ln.strip()
        for ln in proc.stdout.splitlines()
        if ln.strip() and ln.strip() not in ("TASK.md",) and not ln.strip().startswith(".maw/")
    ]
    if not files:
        return {"ok": True}
    bad = [f for f in files if not path_in_scope(f, task.get("write_scope") or [])]
    if bad:
        raise MawError(
            "pre-commit: out of write_scope: " + ", ".join(bad),
            next_hint="maw request-change or unstage those files",
        )
    return {"ok": True, "checked": files}


def cmd_hook_pre_push(*, cwd: Path | None) -> dict[str, Any]:
    home = require_home(cwd)
    cfg = load_config(config_path(home))
    main = (cfg.get("project") or {}).get("main_branch") or "main"
    # pre-push receives refs on stdin; also check env
    import sys

    payload = sys.stdin.read() if not sys.stdin.isatty() else ""
    for line in payload.splitlines():
        parts = line.split()
        if len(parts) < 4:
            continue
        local_ref, _local_sha, remote_ref, _remote_sha = parts[:4]
        if remote_ref in (f"refs/heads/{main}", f"refs/heads/master") or remote_ref.endswith(f"/{main}"):
            raise MawError(
                f"pre-push: refusing to push {remote_ref}",
                next_hint="only maw merge (integrator) updates main; push agent/* branches only",
            )
        if local_ref == "(delete)":
            continue
        # detect force by missing left sha zeros? skip — block force via remote config advice
        if not remote_ref.startswith("refs/heads/agent/") and remote_ref.startswith("refs/heads/"):
            # allow only agent/* from workers
            try:
                who = resolve_agent(cwd)
            except MawError:
                who = ""
            if role_of(who, cfg) != "integrator":
                raise MawError(
                    f"pre-push: only agent/* branches allowed, got {remote_ref}",
                    next_hint="push refs/heads/agent/<worker>/<task>",
                )
    return {"ok": True}
