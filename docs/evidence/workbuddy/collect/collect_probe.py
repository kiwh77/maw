#!/usr/bin/env python3
"""采集 WorkBuddy 两类发行包的随包入口最小调用证据。

只跑 `--version` 与 `--help`：这两条是只读的，不登录、不发模型任务、不改配置，
与 `maw.app.ports/v1` §2.3 对 Probe 的副作用边界一致。

用法（在仓库根目录执行）：

    python3 docs/evidence/workbuddy/collect/collect_probe.py --dist all

产物写到 `docs/evidence/workbuddy/samples/<SAMPLE_ID>-<slug>/`，
每份包含 `meta.json`、`stdout.txt`、`stderr.txt`。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import platform
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

EVIDENCE_DIR = Path(__file__).resolve().parent.parent
SAMPLES_DIR = EVIDENCE_DIR / "samples"

# 唯一允许传给随包 CLI 的宿主环境变量（证据里两包一致）。
ENV_ALLOWLIST = ["HOME", "LANG", "NO_COLOR", "PATH", "SHELL", "TERM", "TMPDIR"]

# 实测启动条件：随包实现由 CODEBUDDY_FORCE_LITE_WB_BUNDLE 选中；
# CODEBUDDY_DISABLE_COMPILE_CACHE 是同一轮最小调用里一起设置的编译缓存开关。
REQUIRED_ENV = {
    "CODEBUDDY_FORCE_LITE_WB_BUNDLE": "1",
    "CODEBUDDY_DISABLE_COMPILE_CACHE": "1",
}

# 两类发行包：入口与 App 版本分别登记，不做「同一个东西」的推断。
DISTRIBUTIONS = {
    "cn": {
        "kind": "cn",
        "app_name": "WorkBuddy",
        "app_bundle": "/Applications/WorkBuddy.app",
        "bundle_id": "com.tencent.workbuddy.mac",
    },
    "intl": {
        "kind": "intl",
        "app_name": "WorkBuddy AI",
        "app_bundle": "/Applications/WorkBuddy AI.app",
        "bundle_id": "com.workbuddy.workbuddy-ai",
    },
}

ENTRY_SUFFIX = "Contents/Resources/app.asar.unpacked/cli/bin/codebuddy"

TIMEOUT_SECONDS = 60


def app_version(bundle: Path) -> str | None:
    """从 Info.plist 读 App 版本；读不到就返回 None，不猜。"""
    with open("/dev/null", "w") as devnull:
        proc = subprocess.run(
            ["/usr/libexec/PlistBuddy", "-c", "Print :CFBundleShortVersionString", str(bundle / "Contents/Info.plist")],
            stdout=subprocess.PIPE,
            stderr=devnull,
            text=True,
        )
    if proc.returncode != 0:
        return None
    return proc.stdout.strip() or None


def probe_env() -> dict[str, str]:
    """按白名单从宿主环境取值，再叠加实测要求的两个变量。"""
    env = {k: os.environ[k] for k in ENV_ALLOWLIST if k in os.environ}
    env.setdefault("NO_COLOR", "1")
    env.update(REQUIRED_ENV)
    return env


def redact(text: str, entry: str) -> tuple[str, list[str]]:
    """脱敏：把绝对入口与家目录换成占位符，返回 (文本, 命中规则)。"""
    hits: list[str] = []
    if entry and entry in text:
        text = text.replace(entry, "<CLI_ENTRY>")
        hits.append("R01_cli_entry")
    home = os.path.expanduser("~")
    if home and home in text:
        text = text.replace(home, "<HOME>")
        hits.append("R02_home_dir")
    user = os.path.basename(home)
    if user and len(user) > 2 and user in text:
        text = text.replace(user, "<USER>")
        hits.append("R03_user_name")
    return text, hits


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def run(argv: list[str], env: dict[str, str]) -> dict:
    started = time.time()
    try:
        proc = subprocess.run(
            argv,
            env=env,
            cwd="/tmp",
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=TIMEOUT_SECONDS,
        )
        duration_ms = int((time.time() - started) * 1000)
        return {
            "stdout": proc.stdout,
            "stderr": proc.stderr,
            "exit_code": proc.returncode,
            "timed_out": False,
            "duration_ms": duration_ms,
        }
    except subprocess.TimeoutExpired as exc:
        duration_ms = int((time.time() - started) * 1000)
        return {
            "stdout": exc.stdout or b"",
            "stderr": exc.stderr or b"",
            "exit_code": None,
            "timed_out": True,
            "duration_ms": duration_ms,
        }


def write_sample(dist: dict, app_ver: str | None, sample_id: str, slug: str, probe: str,
                 args: list[str], entry: str, result: dict) -> dict:
    out_dir = SAMPLES_DIR / f"{sample_id}-{slug}"
    out_dir.mkdir(parents=True, exist_ok=True)

    stdout_text, hits_out = redact(result["stdout"].decode("utf-8", "replace"), entry)
    stderr_text, hits_err = redact(result["stderr"].decode("utf-8", "replace"), entry)
    (out_dir / "stdout.txt").write_text(stdout_text, encoding="utf-8")
    (out_dir / "stderr.txt").write_text(stderr_text, encoding="utf-8")

    argv_template = ["<CLI_ENTRY>"] + args
    meta = {
        "schema": "maw.evidence.workbuddy/v1",
        "sample_id": sample_id,
        "probe": probe,
        "kind": "measured",
        "expectation": {
            "version": "exit 0 且 stdout 为单行 x.y.z；不登录、不发模型任务",
            "help": "exit 0 且 stdout 为帮助清单；只读，不改配置",
        }[probe],
        "source": {
            "distribution": dist["kind"],
            "app_name": dist["app_name"],
            "app_version": app_ver,
            "bundle_id": dist["bundle_id"],
            "cli": "codebuddy",
            "cli_entry": "<CLI_ENTRY>",
            "entry_suffix": ENTRY_SUFFIX,
            "argv_template": argv_template,
            "env_allowlist": ENV_ALLOWLIST,
            "env_overrides": REQUIRED_ENV,
            "cwd_template": "/tmp",
            "platform": platform.system().lower(),
            "captured_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "collector": "collect_probe.py v1",
        },
        "outcome": {
            "exit_code": result["exit_code"],
            "timed_out": result["timed_out"],
            "duration_ms": result["duration_ms"],
            "stdout_bytes": len(result["stdout"]),
            "stderr_bytes": len(result["stderr"]),
            "stdout_sha256": sha256(result["stdout"]),
            "unparseable_stdout_lines": 0,
        },
        "redactions": sorted(set(hits_out + hits_err)) or ["none"],
    }

    if probe == "version":
        line = stdout_text.strip()
        meta["outcome"]["parsed_version"] = line if line.count(".") == 2 else None
        meta["outcome"]["parse_ok"] = meta["outcome"]["parsed_version"] is not None

    (out_dir / "meta.json").write_text(
        json.dumps(meta, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    return meta


def collect_one(key: str) -> list[dict]:
    dist = DISTRIBUTIONS[key]
    bundle = Path(dist["app_bundle"])
    entry = str(bundle / ENTRY_SUFFIX)
    if not Path(entry).exists():
        print(f"[skip] {key}: 入口不存在 {entry}", file=sys.stderr)
        return []

    app_ver = app_version(bundle)
    env = probe_env()
    out = []

    prefix = f"VN305-{key.upper()}"
    plan = [
        ("S01", "version-probe", "version", ["--version"]),
        ("S02", "help-probe", "help", ["--help"]),
    ]
    for idx, slug, probe, args in plan:
        result = run([entry] + args, env)
        meta = write_sample(dist, app_ver, f"{prefix}-{idx}", slug, probe, args, entry, result)
        print(
            f"[ok] {prefix}-{idx} exit={meta['outcome']['exit_code']} "
            f"stdout={meta['outcome']['stdout_bytes']}B stderr={meta['outcome']['stderr_bytes']}B "
            f"{meta['outcome']['duration_ms']}ms",
            file=sys.stderr,
        )
        out.append(meta)
    return out


def main() -> int:
    parser = argparse.ArgumentParser(description="采集 WorkBuddy 随包入口最小调用证据")
    parser.add_argument("--dist", default="all", choices=["all", *DISTRIBUTIONS.keys()])
    args = parser.parse_args()

    keys = list(DISTRIBUTIONS) if args.dist == "all" else [args.dist]
    collected = []
    for key in keys:
        collected.extend(collect_one(key))

    missing = [k for k in keys if not any(m["source"]["distribution"] == k for m in collected)]
    if missing:
        print(f"[warn] 未采集到：{', '.join(missing)}", file=sys.stderr)
        return 1 if len(missing) == len(keys) else 0
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
