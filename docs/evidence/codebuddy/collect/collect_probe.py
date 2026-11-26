#!/usr/bin/env python3
"""VN-301 CodeBuddy 版本化协议证据采集器（脱敏）。

用途
----
在临时目录对已安装的 CodeBuddy CLI 做固定文本的最小探测，保存「退出码 / stdout /
stderr / 事件流 / 耗时」的来源证据，并按固定规则脱敏后写入本目录的 samples/。

设计约束（与 docs/evidence/codebuddy/README.md 对应）
--------------------------------------------------
1. 只做本机可观测动作：--version、--help、-p 三种输出格式、端口占用、非法枚举值、
   隔离配置目录下的认证缺失。不登录、不切换账号、不安装工具、不修改项目。
2. 未观测到的能力一律不写进证据；本脚本不产生任何「成功回合」的伪造样本。
3. 所有样本落盘前必须过脱敏规则（见 REDACTION_RULES）：凭据、绝对路径、会话标识、
   CLI 注入的上下文一律替换为占位符。

用法
----
    python3 collect/collect_probe.py collect            # 重新采集并覆盖 samples/
    python3 collect/collect_probe.py collect --dry-run  # 只打印将要执行的命令
    python3 collect/collect_probe.py verify-redaction   # 扫描现有样本是否残留敏感模式

重新采集会改变 captured_at / duration_ms / 退出码，manifest.json 的哈希随之一并重写；
capability-matrix.json 与 README 为人工冻结内容，不自动更新。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any, Dict, List, Tuple

EVIDENCE_DIR = Path(__file__).resolve().parent.parent
SAMPLES_DIR = EVIDENCE_DIR / "samples"
SCHEMA_DIR = EVIDENCE_DIR / "schema"
MANIFEST = EVIDENCE_DIR / "manifest.json"

SCHEMA_VERSION = "maw.evidence.codebuddy/v1"
COLLECTOR = "collect_probe.py v1"

# 采集时继承的环境变量白名单。受管启动同样只应继承白名单内的变量：
# 继承宿主应用的私有变量（例如指向另一份配置目录的 CODEBUDDY_CONFIG_DIR）会改变
# 认证与配置解析结果，S08 就是该差异的对照样本。
ENV_WHITELIST = ("PATH", "HOME", "LANG", "LC_ALL", "TERM", "TMPDIR", "SHELL")

FIXED_PROMPT = "Reply with exactly: MAW-OK"
INJECTED_CONTEXT_MIN_LEN = 512

REDACTION_RULES = [
    {"id": "R01", "rule": "home_dir", "detail": "$HOME 及其真实用户名替换为 <HOME>"},
    {"id": "R02", "rule": "temp_path", "detail": "临时工作目录 / TMPDIR / 会话临时目录替换为 <WORKDIR> 或 <TMP_PATH>"},
    {"id": "R03", "rule": "uuid", "detail": "UUID 形式的消息/会话标识替换为 <UUID>/<SESSION_ID>"},
    {"id": "R04", "rule": "hex32", "detail": "32 位十六进制请求标识替换为 <REQUEST_ID>"},
    {"id": "R05", "rule": "trace_context", "detail": "traceparent/baggage/traceId 替换为 <TRACEPARENT>/<BAGGAGE>/<REQUEST_ID>"},
    {"id": "R06", "rule": "credential_like", "detail": "sk-*/Bearer/Authorization 等凭据形态替换为 <TOKEN>"},
    {"id": "R07", "rule": "email", "detail": "邮箱替换为 <EMAIL>"},
    {"id": "R08", "rule": "tool_names", "detail": "tools/slash_commands 列表保留长度，元素替换为 <TOOL_n>/<SLASH_COMMAND_n>"},
    {"id": "R09", "rule": "mcp_names", "detail": "mcp_servers 保留 status，name 替换为 <MCP_SERVER_n>"},
    {"id": "R10", "rule": "api_key_source", "detail": "apiKeySource 取值替换为 <REDACTED>"},
    {"id": "R11", "rule": "loopback_port", "detail": "127.0.0.1:<port> 端口号替换为 <PORT>"},
    {"id": "R12", "rule": "cli_path", "detail": "CLI 绝对入口替换为 <CLI_PATH>"},
    {"id": "R13", "rule": "injected_context", "detail": "CLI 注入的 system-reminder/记忆提示等长文本替换为 <CLI_INJECTED_CONTEXT>"},
    {"id": "R14", "rule": "message_ids", "detail": "_messageId/messageId/parentId 替换为 <MESSAGE_ID>/<UUID>"},
]

UUID_RE = re.compile(r"\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b")
HEX32_RE = re.compile(r"\b[0-9a-f]{32}\b")
EMAIL_RE = re.compile(r"[\w.+-]+@[\w-]+\.[\w.-]+")
TOKEN_RE = re.compile(r"(sk-[A-Za-z0-9_\-]{8,}|Bearer\s+[A-Za-z0-9._\-]{8,})")
LOOPBACK_RE = re.compile(r"127\.0\.0\.1:(\d{2,5})")
TMP_FRAGMENT_RE = re.compile(r"(?:private-)?var-folders-[\w\-]+")
TMP_PATH_RE = re.compile(r"(?:/private)?/var/folders/[\w/\-.]+")
COLLECT_DIR_RE = re.compile(r"maw-vn301-[\w\-]+")


def _type_name(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, int):
        return "integer"
    if isinstance(value, float):
        return "number"
    if isinstance(value, str):
        return "string"
    if isinstance(value, list):
        return "array"
    if isinstance(value, dict):
        return "object"
    return type(value).__name__


class Redactor:
    def __init__(self, home: str, workdir: str, cli_path: str, tmpdir: str):
        self.home = home
        self.workdir = workdir
        self.cli_path = cli_path
        self.tmpdir = tmpdir

    def text(self, value: str) -> str:
        if not value:
            return value
        out = value
        for raw in (self.workdir, os.path.realpath(self.workdir) if self.workdir else ""):
            if raw:
                out = out.replace(raw, "<WORKDIR>")
        if self.cli_path:
            out = out.replace(self.cli_path, "<CLI_PATH>")
            out = out.replace(os.path.realpath(self.cli_path), "<CLI_PATH>")
        if self.tmpdir:
            out = out.replace(os.path.realpath(self.tmpdir), "<TMP_PATH>")
            out = out.replace(self.tmpdir, "<TMP_PATH>")
        if self.home:
            out = out.replace(self.home, "<HOME>")
        out = TMP_PATH_RE.sub("<TMP_PATH>", out)
        out = TMP_FRAGMENT_RE.sub("<TMP_PATH>", out)
        out = COLLECT_DIR_RE.sub("<COLLECT_DIR>", out)
        out = LOOPBACK_RE.sub("127.0.0.1:<PORT>", out)
        out = UUID_RE.sub("<UUID>", out)
        out = HEX32_RE.sub("<REQUEST_ID>", out)
        out = EMAIL_RE.sub("<EMAIL>", out)
        out = TOKEN_RE.sub("<TOKEN>", out)
        return out

    def event(self, event: Dict[str, Any]) -> Dict[str, Any]:
        def walk(node: Any, key: str = "", parent: str = "", role: str = "") -> Any:
            if isinstance(node, dict):
                current_role = node.get("role", role) if isinstance(node.get("role"), str) else role
                return {k: walk(v, k, key, current_role) for k, v in node.items()}
            if isinstance(node, list):
                if key == "tools":
                    return [f"<TOOL_{i + 1}>" for i in range(len(node))]
                if key == "slash_commands":
                    return [f"<SLASH_COMMAND_{i + 1}>" for i in range(len(node))]
                if key == "mcp_servers":
                    out = []
                    for i, item in enumerate(node):
                        if isinstance(item, dict):
                            replaced = dict(item)
                            if "name" in replaced:
                                replaced["name"] = f"<MCP_SERVER_{i + 1}>"
                            out.append(replaced)
                        else:
                            out.append(item)
                    return out
                if key == "content" and role == "user":
                    out = []
                    for item in node:
                        if isinstance(item, dict) and isinstance(item.get("text"), str) \
                                and len(item["text"]) >= INJECTED_CONTEXT_MIN_LEN:
                            replaced = dict(item)
                            replaced["text"] = "<CLI_INJECTED_CONTEXT>"
                            out.append(replaced)
                        else:
                            out.append(walk(item, key, parent, role))
                    return out
                return [walk(v, key, parent, role) for v in node]
            if isinstance(node, str):
                if key == "session_id":
                    return "<SESSION_ID>"
                if key == "sessionId":
                    return "<SESSION_ID>"
                if key == "uuid":
                    return "<UUID>"
                if key == "_requestId":
                    return "<REQUEST_ID>"
                if key == "_messageId":
                    return "<MESSAGE_ID>"
                if key == "messageId":
                    return "<UUID>"
                if key == "parentId":
                    return "<UUID>"
                if key == "cwd":
                    return "<WORKDIR>"
                if key == "apiKeySource":
                    return "<REDACTED>"
                if key == "id" and parent in ("message", ""):
                    return "<UUID>"
                if key == "conversationRequestId" or key == "traceId":
                    return "<REQUEST_ID>"
                if parent == "_meta":
                    if key == "traceparent":
                        return "<TRACEPARENT>"
                    if key == "baggage":
                        return "<BAGGAGE>"
                    if "equestId" in key:
                        return "<REQUEST_ID>"
                    if "essageId" in key:
                        return "<UUID>"
                    return "<REDACTED>"
                return self.text(node)
            return node

        return walk(event)


def run_probe(argv: List[str], cwd: str, env: Dict[str, str], timeout: float) -> Dict[str, Any]:
    started = time.monotonic()
    try:
        proc = subprocess.run(
            argv,
            cwd=cwd,
            env=env,
            capture_output=True,
            text=True,
            errors="replace",
            timeout=timeout,
            stdin=subprocess.DEVNULL,
        )
        code, out, err = proc.returncode, proc.stdout, proc.stderr
        timed_out = False
    except subprocess.TimeoutExpired as exc:
        code, timed_out = None, True
        out = exc.stdout if isinstance(exc.stdout, str) else ""
        err = exc.stderr if isinstance(exc.stderr, str) else ""
    return {
        "exit_code": code,
        "stdout": out,
        "stderr": err,
        "duration_ms": int((time.monotonic() - started) * 1000),
        "timed_out": timed_out,
    }


def parse_records(stdout: str, fmt: str) -> Tuple[List[Dict[str, Any]], int]:
    """按输出格式取记录：stream-json 为 JSONL，json 为单个 JSON 数组或对象，text 无记录。"""
    if fmt == "text" or not stdout.strip():
        return [], 0
    if fmt == "json":
        try:
            doc = json.loads(stdout)
        except json.JSONDecodeError:
            return [], 1
        if isinstance(doc, list):
            return [d for d in doc if isinstance(d, dict)], 0
        if isinstance(doc, dict):
            return [doc], 0
        return [], 1
    events, bad = [], 0
    for line in stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            item = json.loads(line)
        except json.JSONDecodeError:
            bad += 1
            continue
        if isinstance(item, dict):
            events.append(item)
    return events, bad


def outcome_of(probe: Dict[str, Any], result: Dict[str, Any], records: List[Dict[str, Any]], bad: int) -> Dict[str, Any]:
    final = next((r for r in reversed(records) if r.get("type") == "result"), None)
    errors = [] if final is None else [str(e) for e in (final.get("errors") or [])]
    outcome: Dict[str, Any] = {
        "exit_code": result["exit_code"],
        "timed_out": result["timed_out"],
        "stdout_bytes": len(result["stdout"].encode("utf-8")),
        "stderr_bytes": len(result["stderr"].encode("utf-8")),
        "duration_ms": result["duration_ms"],
        "protocol_records": len(records),
        "unparseable_stdout_lines": bad,
        "result_record_present": final is not None,
        "result_subtype": None if final is None else final.get("subtype"),
        "is_error": None if final is None else bool(final.get("is_error")),
        "errors": errors,
    }
    if probe["probe"] == "launch.port_conflict":
        outcome["error_class"] = "launch_port_conflict" if "EADDRINUSE" in result["stderr"] else "unknown"
    elif errors and any("Authentication required" in e for e in errors):
        outcome["error_class"] = "authentication_required"
    elif final is not None and final.get("is_error"):
        outcome["error_class"] = "application_error"
    elif probe["fmt"] == "text":
        outcome["error_class"] = None if result["stdout"].strip() else "empty_stdout"
    elif final is None:
        outcome["error_class"] = "missing_result_record"
    else:
        outcome["error_class"] = None
    if probe["fmt"] == "text":
        # 文本模式没有结果记录，只能以退出码 + 非空正文判定，不能声称有结束证据。
        outcome["success"] = bool(result["exit_code"] == 0 and result["stdout"].strip())
        outcome["success_basis"] = "exit_code_and_nonempty_stdout"
    else:
        outcome["success"] = bool(
            result["exit_code"] == 0 and final is not None
            and not final.get("is_error") and final.get("subtype") == "success"
        )
        outcome["success_basis"] = "exit_code_and_result_record"
    return outcome


def build_probes(cli_path: str) -> List[Dict[str, Any]]:
    """探测清单。port: none=不涉及服务，auto=自动分配，busy=刻意占用。"""
    return [
        {"id": "VN301-S01", "slug": "version-probe", "probe": "version", "fmt": "text",
         "argv": [cli_path, "--version"], "port": "none", "timeout": 60,
         "expect": "exit 0 且 stdout 为版本号；不登录、不发模型任务"},
        {"id": "VN301-S02", "slug": "help-probe", "probe": "help", "fmt": "text",
         "argv": [cli_path, "--help"], "port": "none", "timeout": 60,
         "expect": "exit 0 且输出用法清单；用于启动规格的参数枚举"},
        {"id": "VN301-S03", "slug": "print-stream-json", "probe": "print.stream_json", "fmt": "stream-json",
         "argv": [cli_path, "-p", FIXED_PROMPT, "--output-format", "stream-json",
                  "--include-partial-messages", "--port", "0"], "port": "auto", "timeout": 240,
         "expect": "逐行 JSON 事件；result 记录给出结束语义与用量"},
        {"id": "VN301-S04", "slug": "print-json", "probe": "print.json", "fmt": "json",
         "argv": [cli_path, "-p", FIXED_PROMPT, "--output-format", "json", "--port", "0"],
         "port": "auto", "timeout": 240,
         "expect": "单条 JSON 文档；记录形态是否与 stream-json 一致需实测"},
        {"id": "VN301-S05", "slug": "print-text", "probe": "print.text", "fmt": "text",
         "argv": [cli_path, "-p", FIXED_PROMPT, "--port", "0"], "port": "auto", "timeout": 240,
         "expect": "默认文本输出；无结构化结束证据"},
        {"id": "VN301-S06", "slug": "port-conflict", "probe": "launch.port_conflict", "fmt": "stream-json",
         "argv": [cli_path, "-p", FIXED_PROMPT, "--output-format", "stream-json", "--port", "{busy_port}"],
         "port": "busy", "timeout": 120,
         "expect": "端口被占用时启动失败；用于确认必须显式指定端口"},
        {"id": "VN301-S07", "slug": "invalid-output-format", "probe": "print.invalid_output_format", "fmt": "text",
         "argv": [cli_path, "-p", FIXED_PROMPT, "--output-format", "bogus", "--port", "0"],
         "port": "auto", "timeout": 240,
         "expect": "非法枚举值是否被拒绝需实测，不能按帮助文本假定"},
        {"id": "VN301-S08", "slug": "print-stream-json-no-credentials", "probe": "print.stream_json_no_credentials",
         "fmt": "stream-json",
         "argv": [cli_path, "-p", FIXED_PROMPT, "--output-format", "stream-json", "--port", "0"],
         "port": "auto", "timeout": 240,
         "env_extra": {"HOME": "{empty_home}"},
         "expect": "隔离空 HOME 复现未登录：用于确认退出码 0 不等于成功"},
    ]


def sha256_of(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_sample(sample_dir: Path, files: Dict[str, str]) -> Dict[str, str]:
    sample_dir.mkdir(parents=True, exist_ok=True)
    for stale in sample_dir.iterdir():
        if stale.is_file() and stale.name not in files:
            stale.unlink()
    digests = {}
    for name, content in files.items():
        target = sample_dir / name
        target.write_text(content, encoding="utf-8")
        digests[name] = sha256_of(target)
    return digests


def fields_of(node: Any, prefix: str = "") -> Dict[str, Dict[str, Any]]:
    out: Dict[str, Dict[str, Any]] = {}
    for key, value in node.items():
        path = f"{prefix}{key}"
        out[path] = {"type": _type_name(value)}
        if isinstance(value, dict) and value:
            out.update(fields_of(value, f"{path}."))
    return out


def collect(dry_run: bool) -> int:
    cli = shutil_which("codebuddy")
    if not cli:
        print("ERROR: 未找到 codebuddy 入口；本卡不安装工具，请先确认已安装。", file=sys.stderr)
        return 2
    cli_path = os.path.realpath(cli)
    home = os.environ.get("HOME", "")
    tmpdir = os.environ.get("TMPDIR", tempfile.gettempdir())
    probes = build_probes(cli_path)

    if dry_run:
        for p in probes:
            print(f"[{p['id']}] {p['probe']}: {' '.join(p['argv'])}  (cwd=<临时目录>, port={p['port']})")
        return 0

    base_env = {k: os.environ[k] for k in ENV_WHITELIST if k in os.environ}
    base_env.setdefault("TERM", "dumb")
    base_env["NO_COLOR"] = "1"

    workroot = tempfile.mkdtemp(prefix="maw-vn301-")
    samples, inventories = [], {}
    version = _detect_version(cli_path, base_env)

    for probe in probes:
        workdir = os.path.join(workroot, probe["slug"])
        os.makedirs(workdir, exist_ok=True)
        env = dict(base_env)
        env_note: Dict[str, str] = {}
        argv = list(probe["argv"])
        sock = None
        if probe.get("env_extra"):
            for key, value in probe["env_extra"].items():
                if value == "{empty_home}":
                    empty_home = os.path.join(workroot, f"empty-home-{probe['slug']}")
                    os.makedirs(empty_home, exist_ok=True)
                    env[key] = empty_home
                    env_note[key] = "<EMPTY_HOME>"
                else:
                    env[key] = value
                    env_note[key] = value
        if probe["port"] == "busy":
            sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            sock.bind(("127.0.0.1", 0))
            sock.listen(1)
            argv = [a.replace("{busy_port}", str(sock.getsockname()[1])) for a in argv]
        try:
            result = run_probe(argv, workdir, env, probe["timeout"])
        finally:
            if sock is not None:
                sock.close()

        records, bad = parse_records(result["stdout"], probe["fmt"])
        outcome = outcome_of(probe, result, records, bad)
        redactor = Redactor(home, workdir, cli_path, tmpdir)

        files: Dict[str, str] = {
            "stdout.txt": redactor.text(result["stdout"]),
            "stderr.txt": redactor.text(result["stderr"]),
        }
        if records:
            redacted = [redactor.event(r) for r in records]
            if probe["fmt"] == "stream-json":
                files["events.jsonl"] = "".join(
                    json.dumps(r, ensure_ascii=False, sort_keys=True) + "\n" for r in redacted)
            else:
                files["records.json"] = json.dumps(redacted, ensure_ascii=False, indent=2, sort_keys=True) + "\n"

        meta = {
            "schema": SCHEMA_VERSION,
            "sample_id": probe["id"],
            "kind": "measured",
            "probe": probe["probe"],
            "output_format": probe["fmt"],
            "expectation": probe["expect"],
            "source": {
                "cli": "codebuddy",
                "cli_entry": "<CLI_PATH>",
                "cli_version": version,
                "cli_version_source": "measured:VN301-S01",
                "platform": sys.platform,
                "cwd_template": "<WORKDIR>",
                "argv_template": [redactor.text(a) for a in argv],
                "env_whitelist": sorted(env.keys()),
                "env_overrides": env_note,
                "fixed_prompt": FIXED_PROMPT,
                "captured_at": _utc_now(),
                "collector": COLLECTOR,
            },
            "outcome": outcome,
            "redactions": [r["id"] for r in REDACTION_RULES],
        }
        files["meta.json"] = json.dumps(meta, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
        digests = write_sample(SAMPLES_DIR / f"{probe['id']}-{probe['slug']}", files)
        samples.append({
            "sample_id": probe["id"],
            "dir": f"samples/{probe['id']}-{probe['slug']}",
            "kind": "measured",
            "probe": probe["probe"],
            "output_format": probe["fmt"],
            "files": digests,
        })
        for record in records:
            key = (probe["fmt"], str(record.get("type")), str(record.get("subtype")))
            entry = inventories.setdefault(key, {"output_format": key[0], "type": key[1],
                                                "subtype": key[2], "fields": {}})
            for path, info in fields_of(record).items():
                entry["fields"][path] = info
        print(f"[{probe['id']}] exit={outcome['exit_code']} dur={outcome['duration_ms']}ms "
              f"records={outcome['protocol_records']} error_class={outcome['error_class']} "
              f"subtype={outcome['result_subtype']} success={outcome['success']}")

    manifest = {
        "schema": SCHEMA_VERSION,
        "generated_by": COLLECTOR,
        "generated_at": _utc_now(),
        "environment": {
            "platform": sys.platform,
            "cli": "codebuddy",
            "cli_version": version,
            "cli_version_provenance": "measured:VN301-S01",
            "python": sys.version.split()[0],
            "env_whitelist": sorted(base_env.keys()),
        },
        "redaction_rules": REDACTION_RULES,
        "samples": samples,
        "synthetic_samples": _existing_synthetic(),
    }
    MANIFEST.write_text(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")

    SCHEMA_DIR.mkdir(parents=True, exist_ok=True)
    inventory = {
        "schema": "maw.evidence.codebuddy/event-inventory/v1",
        "cli_version": version,
        "provenance": "measured:VN301-S03/S04/S08（字段名与类型级；不含取值）",
        "note": "只登记实测出现过的字段。未出现的字段一律视为未知，不得由其他来源补齐。",
        "records": [
            {"output_format": v["output_format"], "type": v["type"], "subtype": v["subtype"],
             "fields": {p: v["fields"][p] for p in sorted(v["fields"])}}
            for _, v in sorted(inventories.items())
        ],
    }
    (SCHEMA_DIR / "event-inventory.json").write_text(
        json.dumps(inventory, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"wrote {MANIFEST.name} 与 schema/event-inventory.json")
    return 0


def _existing_synthetic() -> List[Dict[str, Any]]:
    if not MANIFEST.exists():
        return []
    try:
        data = json.loads(MANIFEST.read_text(encoding="utf-8"))
    except json.JSONDecodeError:
        return []
    return data.get("synthetic_samples", [])


def _detect_version(cli_path: str, env: Dict[str, str]) -> str:
    try:
        proc = subprocess.run([cli_path, "--version"], capture_output=True, text=True,
                              timeout=60, env=env, stdin=subprocess.DEVNULL)
        return proc.stdout.strip().splitlines()[0].strip() if proc.stdout.strip() else "unknown"
    except (OSError, subprocess.SubprocessError):
        return "unknown"


def _utc_now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def shutil_which(name: str) -> str:
    for entry in os.environ.get("PATH", "").split(os.pathsep):
        candidate = os.path.join(entry, name)
        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate
    return ""


SENSITIVE_PATTERNS = [
    (re.compile(r"/Users/[^/\s\"']+"), "绝对家目录路径"),
    (re.compile(r"(?:/private)?/var/folders/[^\s\"']+"), "绝对临时路径"),
    (re.compile(r"var-folders-[\w\-]+"), "临时目录片段"),
    (re.compile(r"/home/[^/\s\"']+"), "绝对家目录路径"),
    (re.compile(r"sk-[A-Za-z0-9_\-]{12,}"), "疑似 API Key"),
    (re.compile(r"Bearer\s+[A-Za-z0-9._\-]{12,}"), "疑似 Bearer Token"),
    (re.compile(r"[\w.+-]+@[\w-]+\.[\w.-]+"), "邮箱"),
    (re.compile(r"\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b"), "UUID 标识"),
    (re.compile(r"\b[0-9a-f]{32}\b"), "32 位十六进制请求标识"),
    (re.compile(r"system-reminder data-role=\"memory\""), "CLI 注入的记忆上下文"),
]


def verify_redaction() -> int:
    hits = 0
    for path in sorted(SAMPLES_DIR.rglob("*")):
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        for pattern, label in SENSITIVE_PATTERNS:
            for match in pattern.finditer(text):
                hits += 1
                print(f"{path.relative_to(EVIDENCE_DIR)}: {label}: {match.group(0)[:40]}")
    print("redaction clean" if hits == 0 else f"{hits} 处敏感模式命中")
    return 0 if hits == 0 else 1


def main() -> int:
    parser = argparse.ArgumentParser(description="VN-301 CodeBuddy 协议证据采集器")
    sub = parser.add_subparsers(dest="cmd", required=True)
    p_collect = sub.add_parser("collect", help="执行探测并写入脱敏样本")
    p_collect.add_argument("--dry-run", action="store_true")
    sub.add_parser("verify-redaction", help="扫描样本是否残留敏感模式")
    args = parser.parse_args()
    if args.cmd == "collect":
        return collect(args.dry_run)
    return verify_redaction()


if __name__ == "__main__":
    raise SystemExit(main())
