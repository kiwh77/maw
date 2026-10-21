"""maw CLI entrypoint."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any, Callable

from maw import __version__
from maw import cmds
from maw.errors import MawError


def _print_result(data: dict[str, Any], *, as_json: bool) -> None:
    if as_json:
        print(json.dumps(data, ensure_ascii=False, indent=2))
        return
    if "terminal" in data:
        print(data["terminal"])
        if data.get("html"):
            print(f"\nHTML: {data['html']}")
        if data.get("next"):
            print(f"Next: {data['next']}")
        return
    # human summary
    if data.get("ok") is False and data.get("issues"):
        print("Doctor: FAIL")
        for i in data["issues"]:
            print(f"  - {i}")
    elif data.get("ok"):
        print("OK")
    for key in ("home", "worktree", "branch", "task_id", "status", "board", "html"):
        if key in data and key != "task":
            print(f"{key}: {data[key]}")
    if "task" in data and isinstance(data["task"], dict):
        t = data["task"]
        print(f"task: {t.get('id')} [{t.get('status')}] {t.get('title')}")
        if t.get("write_scope"):
            print("write_scope:", ", ".join(t["write_scope"]))
    if "results" in data:
        print("results:", json.dumps(data["results"], ensure_ascii=False))
    if "errors" in data and data["errors"]:
        for e in data["errors"]:
            print(f"  - {e}")
    if "branch_protection_advice" in data:
        print("Branch protection advice:")
        for a in data["branch_protection_advice"]:
            print(f"  - {a}")
    if data.get("next"):
        print(f"Next: {data['next']}")


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="maw", description="Multi-agent worktree orchestrator")
    p.add_argument("--version", action="version", version=f"maw {__version__}")
    p.add_argument("--json", action="store_true", help="JSON output")
    p.add_argument("--dry-run", action="store_true", help="Preview without writing")
    p.add_argument("-C", "--cwd", type=Path, default=None, help="Working directory")

    sub = p.add_subparsers(dest="cmd", required=True)

    init = sub.add_parser("init", help="Initialize .multiagent environment")
    init.add_argument("--upgrade", action="store_true")

    plan = sub.add_parser("plan", help="Blueprint commands")
    plan_sub = plan.add_subparsers(dest="plan_cmd", required=True)
    pa = plan_sub.add_parser("apply", help="Validate and apply blueprint")
    pa.add_argument("blueprint", type=Path)
    pa.add_argument("--yes", action="store_true")
    pc = plan_sub.add_parser("check", help="Validate blueprint only")
    pc.add_argument("blueprint", type=Path)

    nxt = sub.add_parser("next", help="Claim next ready task + worktree")
    nxt.add_argument("--agent", default=None)

    prog = sub.add_parser("progress", help="Heartbeat / milestone")
    prog.add_argument("-m", "--message", required=True)
    prog.add_argument("--task", default=None)

    subm = sub.add_parser("submit", help="Scope-check and enter review/merge queue")
    subm.add_argument("--task", default=None)

    rev = sub.add_parser("review", help="Approve or request changes")
    rev.add_argument("task_id")
    g = rev.add_mutually_exclusive_group(required=True)
    g.add_argument("--approve", action="store_true")
    g.add_argument("--request-changes", action="store_true")
    rev.add_argument("-m", "--message", default="")

    mg = sub.add_parser("merge", help="Serial merge queue (integrator)")
    mg.add_argument("--yes", action="store_true")

    sub.add_parser("board", help="Show board + write HTML")
    sub.add_parser("doctor", help="Self-check")

    blk = sub.add_parser("block", help="Block current task")
    blk.add_argument("-m", "--message", required=True)
    blk.add_argument("--task", default=None)

    task = sub.add_parser("task", help="Task utilities")
    task_sub = task.add_subparsers(dest="task_cmd", required=True)
    task_sub.add_parser("list")
    ts = task_sub.add_parser("show")
    ts.add_argument("task_id")
    tu = task_sub.add_parser("unblock")
    tu.add_argument("task_id")

    hook = sub.add_parser("hook", help="Internal git hook entrypoints")
    hook.add_argument("hook_name", choices=["pre-commit", "pre-push"])

    return p


def dispatch(args: argparse.Namespace) -> dict[str, Any]:
    cwd = args.cwd
    dry = bool(args.dry_run)

    if args.cmd == "init":
        return cmds.cmd_init(cwd=cwd, dry_run=dry, upgrade=bool(args.upgrade))
    if args.cmd == "plan":
        if args.plan_cmd == "apply":
            return cmds.cmd_plan_apply(
                path=args.blueprint, cwd=cwd, yes=bool(args.yes), dry_run=dry
            )
        return cmds.cmd_plan_check(path=args.blueprint, cwd=cwd)
    if args.cmd == "next":
        return cmds.cmd_next(cwd=cwd, agent=args.agent, dry_run=dry)
    if args.cmd == "progress":
        return cmds.cmd_progress(cwd=cwd, message=args.message, task_id=args.task)
    if args.cmd == "submit":
        return cmds.cmd_submit(cwd=cwd, task_id=args.task, dry_run=dry)
    if args.cmd == "review":
        return cmds.cmd_review(
            cwd=cwd,
            task_id=args.task_id,
            approve=bool(args.approve),
            message=args.message,
        )
    if args.cmd == "merge":
        return cmds.cmd_merge(cwd=cwd, yes=bool(args.yes), dry_run=dry)
    if args.cmd == "board":
        return cmds.cmd_board(cwd=cwd)
    if args.cmd == "doctor":
        return cmds.cmd_doctor(cwd=cwd)
    if args.cmd == "block":
        return cmds.cmd_block(cwd=cwd, message=args.message, task_id=args.task)
    if args.cmd == "task":
        if args.task_cmd == "list":
            return cmds.cmd_task_list(cwd=cwd)
        if args.task_cmd == "show":
            return cmds.cmd_task_show(cwd=cwd, task_id=args.task_id)
        if args.task_cmd == "unblock":
            return cmds.cmd_unblock(cwd=cwd, task_id=args.task_id)
    if args.cmd == "hook":
        if args.hook_name == "pre-commit":
            return cmds.cmd_hook_pre_commit(cwd=cwd)
        return cmds.cmd_hook_pre_push(cwd=cwd)
    raise MawError(f"unknown command {args.cmd}")


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        result = dispatch(args)
        _print_result(result, as_json=bool(args.json))
        return 0 if result.get("ok", True) else 1
    except MawError as exc:
        if args.json:
            print(
                json.dumps(
                    {"ok": False, "error": exc.message, "next": exc.next_hint},
                    ensure_ascii=False,
                    indent=2,
                )
            )
        else:
            print(exc.format(), file=sys.stderr)
        return exc.code
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
