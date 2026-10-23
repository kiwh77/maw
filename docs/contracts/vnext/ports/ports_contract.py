"""maw 应用端口与接入器契约 v1 —— 纯标准库校验器。

契约正文见同目录 ``app-ports-contract-v1.md``。
本模块只做一件事：读一份「端口装配快照」JSON，返回违反不变量清单。

快照描述的是**接线**，不是运行态：哪些模块存在、谁 import 谁、七个端口各自绑给谁、
每次调用在不在事务里、声明了哪些错误。校验器不读 Go 源码、不连数据库、不读环境变量。

用法::

    from ports_contract import load, validate
    violations = validate(load("samples/valid/01-minimal-wiring.json"))
"""

from __future__ import annotations

import json
import re
from datetime import datetime
from pathlib import Path

CONTRACT_ID = "maw.app.ports/v1"

# 四层：用例 / 端口 / 适配器 / 基础设施。顺序即允许的依赖方向（只准向下）。
LAYERS = ("app", "port", "adapter", "infra")

# 七个端口的输入、输出、错误。正文 §2 的端口小节与本表一一对应。
PORTS = {
    "ClaimExecution": {
        "kind": "use_case",
        "inputs": ("project_id", "agent_id", "requested_task", "expected_revision",
                   "idempotency_key", "capacity"),
        "outputs": ("task_instance", "task_version", "run_id", "generation", "leases",
                    "operation_id"),
        "errors": ("no_ready", "conflict", "revision_mismatch", "capacity_exhausted",
                   "idempotent_replay"),
    },
    "Store": {
        "kind": "transaction",
        "inputs": ("ops", "expected_revision", "generation"),
        "outputs": ("cursor", "revision", "applied"),
        "errors": ("busy", "conflict", "invalid_transition", "unknown_identity"),
    },
    "Probe": {
        "kind": "protocol",
        "inputs": ("node_config",),
        "outputs": ("entry_state", "version", "readiness", "capabilities", "evidence_ref"),
        "errors": ("entry_missing", "version_unparsed", "probe_timeout", "platform_unsupported"),
    },
    "Launch": {
        "kind": "protocol",
        "inputs": ("run_spec",),
        "outputs": ("executable", "argv", "cwd", "stdin_mode", "env_allowlist",
                    "expected_protocol"),
        "errors": ("invalid_spec", "capability_unsupported", "entry_unavailable"),
    },
    "Decode": {
        "kind": "protocol",
        "inputs": ("raw_record", "cursor", "protocol"),
        "outputs": ("events", "raw_ref", "quality"),
        "errors": ("unparseable_record", "unknown_record_type", "version_unsupported"),
    },
    "Interpret": {
        "kind": "protocol",
        "inputs": ("final_result", "application_error", "exit_code", "timeout", "artifacts"),
        "outputs": ("outcome", "result_text", "artifact_candidates", "quality"),
        "errors": ("missing_result_record", "auth_required", "application_error",
                   "protocol_unknown"),
    },
    "ProcessController": {
        "kind": "process",
        "inputs": ("launch_spec", "stop_request"),
        "outputs": ("process_handle", "stop_evidence"),
        "errors": ("not_started", "already_stopped", "stop_unconfirmed", "platform_unsupported"),
    },
}

# 事务内禁止的副作用：碰外部世界的动作都不许进事务。
EXTERNAL_EFFECTS = ("process_spawn", "process_stop", "git_write", "network")
KNOWN_EFFECTS = ("db_write", "event_append", "task_status_write", "git_write",
                 "process_spawn", "process_stop", "network", "fs_write")

# adapter 不得反向依赖的包前缀（业务用例、存储、git、调度、状态机）。
ADAPTER_FORBIDDEN_IMPORTS = (
    "internal/app",
    "internal/db",
    "internal/gitops",
    "internal/schedule",
    "internal/machine",
    "internal/engine/claim",
    "internal/engine/dispatch",
    "internal/runner",
)

SEMVER_RE = re.compile(r"^\d+\.\d+\.\d+$")

# 每个错误码在正文「不变量登记表」中必须有同名行，由 test 强制对齐。
RULES = {
    "P100": "必填字段缺失或类型不符",
    "P101": "契约标识不受支持",
    "P110": "模块层不在 app/port/adapter/infra 内",
    "P111": "绑定了未登记的端口",
    "P112": "同一端口重复绑定",
    "P113": "端口声明了却没有绑定实现",
    "P120": "适配器反向依赖业务或基础设施",
    "P121": "端口层依赖了实现",
    "P122": "import 了未声明的模块",
    "P123": "模块依赖成环",
    "P130": "接入器直接写业务任务状态",
    "P131": "调用未声明可能错误",
    "P132": "事务内出现外部副作用",
    "P133": "事务外写业务状态",
    "P134": "领取调用缺幂等键或冲突检查",
    "P140": "适配器能力未绑定版本或证据",
    "P141": "把退出码 0 当作成功",
    "P142": "未知协议结果被标为成功",
}


class Violation:
    """一条不变量违反记录。"""

    __slots__ = ("code", "path", "message")

    def __init__(self, code: str, path: str, message: str) -> None:
        self.code = code
        self.path = path
        self.message = message

    def to_dict(self) -> dict:
        return {"code": self.code, "path": self.path, "message": self.message}

    def __str__(self) -> str:
        return f"{self.code} @ {self.path}: {self.message}"

    def __repr__(self) -> str:  # pragma: no cover - 调试用
        return f"Violation({self.code!r}, {self.path!r}, {self.message!r})"


def _add(out: list, code: str, path: str, message: str) -> None:
    out.append(Violation(code, path, message))


def load(path) -> object:
    """读一份装配快照 JSON。"""
    return json.loads(Path(path).read_text(encoding="utf-8"))


def _require(mapping, key: str, path: str, out: list, types=None):
    if not isinstance(mapping, dict) or key not in mapping:
        _add(out, "P100", f"{path}.{key}", "必填字段缺失")
        return None
    value = mapping[key]
    if types is not None and not isinstance(value, types):
        _add(out, "P100", f"{path}.{key}", f"类型错误，期望 {types}")
        return None
    return value


def _parse_time(raw, path: str, out: list):
    if not isinstance(raw, str) or not raw.strip():
        _add(out, "P100", path, "时间必须是非空 ISO8601 字符串")
        return None
    try:
        return datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        _add(out, "P100", path, f"无法解析的时间 {raw!r}")
        return None


def _check_modules(doc: dict, out: list) -> dict:
    """校验模块层与依赖方向，返回 path -> module 映射。"""
    modules = doc.get("modules")
    if not isinstance(modules, list) or not modules:
        _add(out, "P100", "$.modules", "modules 必须是非空数组")
        return {}
    by_path: dict = {}
    for idx, mod in enumerate(modules):
        path = f"$.modules[{idx}]"
        if not isinstance(mod, dict):
            _add(out, "P100", path, "模块必须是对象")
            continue
        mod_path = _require(mod, "path", path, out, str)
        layer = _require(mod, "layer", path, out, str)
        imports = _require(mod, "imports", path, out, list)
        if isinstance(layer, str) and layer not in LAYERS:
            _add(out, "P110", f"{path}.layer", f"layer 只能是 {LAYERS}")
        if isinstance(mod_path, str):
            if mod_path in by_path:
                _add(out, "P100", f"{path}.path", "模块路径重复")
            else:
                by_path[mod_path] = mod
        if isinstance(imports, list) and not all(isinstance(i, str) for i in imports):
            _add(out, "P100", f"{path}.imports", "imports 必须是字符串数组")
    return by_path


def _check_import_edges(doc: dict, by_path: dict, out: list) -> None:
    """依赖方向、未声明模块、环。"""
    declared = set(by_path)
    for mod in by_path.values():
        mod_path = mod.get("path")
        layer = mod.get("layer")
        imports = [i for i in (mod.get("imports") or []) if isinstance(i, str)]
        if layer == "port" and imports:
            _add(out, "P121", f"module:{mod_path}", "端口层不得 import 任何实现")
        for target in imports:
            if target not in declared:
                _add(out, "P122", f"module:{mod_path}", f"import {target!r} 未在 modules 中声明")
            if layer == "adapter" and target.startswith(ADAPTER_FORBIDDEN_IMPORTS):
                _add(out, "P120", f"module:{mod_path}",
                     f"适配器不得依赖 {target!r}（只能依赖 port 与 model）")

    # 环检测：白灰黑染色，发现回边即报。
    state: dict = {}

    def visit(node: str, stack: list) -> None:
        if state.get(node) == "black":
            return
        if state.get(node) == "grey":
            cycle = " -> ".join(stack + [node])
            _add(out, "P123", f"module:{node}", f"依赖成环：{cycle}")
            return
        state[node] = "grey"
        for target in by_path.get(node, {}).get("imports") or []:
            if isinstance(target, str) and target in by_path:
                visit(target, stack + [node])
        state[node] = "black"

    for mod_path in by_path:
        visit(mod_path, [])


def _check_bindings(doc: dict, out: list) -> dict:
    """端口绑定：登记、唯一、齐全、适配器版本。返回 port -> binding。"""
    bindings = doc.get("bindings")
    if not isinstance(bindings, list) or not bindings:
        _add(out, "P100", "$.bindings", "bindings 必须是非空数组")
        return {}
    by_port: dict = {}
    for idx, bind in enumerate(bindings):
        path = f"$.bindings[{idx}]"
        if not isinstance(bind, dict):
            _add(out, "P100", path, "绑定必须是对象")
            continue
        port = _require(bind, "port", path, out, str)
        impl = _require(bind, "impl", path, out, str)
        _require(bind, "kind", path, out, str)
        if isinstance(port, str):
            if port not in PORTS:
                _add(out, "P111", f"{path}.port", f"{port!r} 不是登记过的端口")
                continue
            if port in by_port:
                _add(out, "P112", f"{path}.port", f"端口 {port!r} 重复绑定")
                continue
            by_port[port] = bind
        is_adapter = isinstance(impl, str) and impl.startswith("internal/adapters/")
        is_adapter_router = isinstance(impl, str) and impl.rstrip("/") == "internal/adapters"
        if is_adapter or is_adapter_router:
            providers = bind.get("providers")
            if providers is not None:
                # 多接入器：一个端口由注册表分派，但每个来源必须各自绑版本与证据。
                if not isinstance(providers, list) or not providers:
                    _add(out, "P140", f"{path}.providers", "providers 必须是非空数组")
                else:
                    for pidx, provider in enumerate(providers):
                        ppath = f"{path}.providers[{pidx}]"
                        if not isinstance(provider, dict):
                            _add(out, "P100", ppath, "provider 必须是对象")
                            continue
                        _require(provider, "name", ppath, out, str)
                        version = provider.get("capability_version")
                        evidence = provider.get("evidence")
                        if not isinstance(version, str) or not SEMVER_RE.match(version):
                            _add(out, "P140", f"{ppath}.capability_version",
                                 "每个来源都必须绑定语义化版本")
                        if not isinstance(evidence, str) or not evidence.strip():
                            _add(out, "P140", f"{ppath}.evidence", "每个来源都必须挂证据引用")
            else:
                version = bind.get("capability_version")
                evidence = bind.get("evidence")
                if not isinstance(version, str) or not SEMVER_RE.match(version):
                    _add(out, "P140", f"{path}.capability_version", "适配器绑定必须写语义化版本")
                if not isinstance(evidence, str) or not evidence.strip():
                    _add(out, "P140", f"{path}.evidence", "适配器绑定必须挂证据引用")
    missing = sorted(set(PORTS) - set(by_port))
    for port in missing:
        _add(out, "P113", "$.bindings", f"端口 {port!r} 没有绑定实现")
    return by_port


def _check_calls(doc: dict, out: list) -> None:
    calls = doc.get("calls")
    if not isinstance(calls, list):
        _add(out, "P100", "$.calls", "calls 必须是数组")
        return
    for idx, call in enumerate(calls):
        path = f"$.calls[{idx}]"
        if not isinstance(call, dict):
            _add(out, "P100", path, "调用必须是对象")
            continue
        port = _require(call, "port", path, out, str)
        caller = _require(call, "caller", path, out, str)
        in_tx = _require(call, "in_tx", path, out, bool)
        effects = _require(call, "effects", path, out, list)
        errors = _require(call, "errors", path, out, list)
        if isinstance(port, str) and port not in PORTS:
            _add(out, "P111", f"{path}.port", f"{port!r} 不是登记过的端口")
        if isinstance(errors, list) and not errors:
            _add(out, "P131", f"{path}.errors", "调用必须声明可能错误，不得留空")
        if isinstance(effects, list):
            bad = [e for e in effects if e not in KNOWN_EFFECTS]
            if bad:
                _add(out, "P100", f"{path}.effects", f"未知副作用 {bad}")
            if in_tx is True:
                external = [e for e in effects if e in EXTERNAL_EFFECTS]
                if external:
                    _add(out, "P132", f"{path}.effects", f"事务内出现外部副作用 {external}")
            if in_tx is False and "task_status_write" in effects:
                _add(out, "P133", f"{path}.effects", "业务状态只能在事务内写")
            if "task_status_write" in effects and isinstance(caller, str) \
                    and caller.startswith("internal/adapters/"):
                _add(out, "P130", f"{path}.effects", "接入器不得直接写业务任务状态")
        if port == "ClaimExecution":
            if call.get("idempotency_key") is not True:
                _add(out, "P134", f"{path}.idempotency_key", "领取必须带幂等键")
            if call.get("conflict_check") is not True:
                _add(out, "P134", f"{path}.conflict_check", "领取必须做写盘／占用冲突检查")
        if call.get("exit_code_only_success") is True:
            _add(out, "P141", f"{path}.exit_code_only_success",
                 "退出码 0 不能作为成功依据，必须有结果记录与证据")
        if call.get("unknown_as_success") is True:
            _add(out, "P142", f"{path}.unknown_as_success",
                 "未知协议结果必须保留为 unknown，不得标为成功")


def validate(doc) -> list:
    """校验整份装配快照，返回 Violation 列表；空列表表示通过。"""
    out: list = []
    if not isinstance(doc, dict):
        _add(out, "P100", "$", "快照根节点必须是对象")
        return out
    if doc.get("contract") != CONTRACT_ID:
        _add(out, "P101", "$.contract", f"必须是 {CONTRACT_ID!r}")
    _require(doc, "project_id", "$", out, str)
    _parse_time(doc.get("generated_at"), "$.generated_at", out)

    by_path = _check_modules(doc, out)
    _check_import_edges(doc, by_path, out)
    _check_bindings(doc, out)
    _check_calls(doc, out)
    return out


if __name__ == "__main__":  # pragma: no cover - 手工排查入口
    import sys

    target = sys.argv[1] if len(sys.argv) > 1 else "samples/valid/01-minimal-wiring.json"
    found = validate(load(target))
    for item in found:
        print(item)
    print(f"{len(found)} violation(s) in {target}")
