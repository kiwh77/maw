"""maw 任务实例与版本契约 v1 —— 纯标准库校验器。

契约正文见同目录 ``task-instance-contract-v1.md``。
本模块只做一件事：读一份「任务身份快照」JSON，返回违反不变量清单。
不使用第三方依赖，不连数据库，不读环境变量。

用法::

    from task_contract import load, validate
    violations = validate(load("samples/valid/01-minimal.json"))
"""

from __future__ import annotations

import json
import re
from datetime import datetime
from pathlib import Path

CONTRACT_ID = "maw.task.instance/v1"

# 实例身份与旧显示编号属于两个命名空间：前者永不复用，后者可被新实例重新占用。
INSTANCE_ID_RE = re.compile(r"^ti_[a-z0-9]{6,}$")
DISPLAY_ID_RE = re.compile(r"^[A-Z][A-Z0-9]*-[0-9]+$")

STATE_ACTIVE = "active"
STATE_RETIRED = "retired"
STATES = (STATE_ACTIVE, STATE_RETIRED)

# 每个错误码在正文「不变量登记表」中必须有同名行，由 test 强制对齐。
RULES = {
    "E100": "字段缺失或类型不符",
    "E101": "契约标识不受支持",
    "E110": "instance_id 重复",
    "E111": "instance_id 命名空间非法",
    "E112": "display_id 格式非法",
    "E120": "同一工程内活跃别名冲突",
    "E130": "活跃版本指针缺失或不指向已冻结版本",
    "E131": "版本号必须从 1 连续递增",
    "E132": "活跃版本不得回退到非最大版本号",
    "E133": "版本冻结时间必须严格递增",
    "E140": "退役实例的退役事实不完整",
    "E142": "退役后不得再冻结新版本",
    "E150": "依赖目标在本工程内不存在",
    "E151": "依赖指向退役或不可满足的实例",
    "E152": "依赖自引用",
    "E153": "旧编号依赖存在多个候选，必须改用 instance 形式消歧",
    "E160": "旧主键不完整或与 (project_id, display_id) 不一致",
    "E161": "歧义历史不得占用当前活跃别名",
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
    """读一份快照 JSON。"""
    return json.loads(Path(path).read_text(encoding="utf-8"))


def _parse_time(raw, path: str, out: list):
    """解析 ISO8601 Z 时间；失败返回 None 并记 E100。"""
    if not isinstance(raw, str) or not raw.strip():
        _add(out, "E100", path, "时间必须是非空 ISO8601 字符串")
        return None
    try:
        return datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        _add(out, "E100", path, f"无法解析的时间 {raw!r}")
        return None


def _require(mapping: dict, key: str, path: str, out: list, types=None):
    if not isinstance(mapping, dict) or key not in mapping:
        _add(out, "E100", f"{path}.{key}", "必填字段缺失")
        return None
    value = mapping[key]
    if types is not None and not isinstance(value, types):
        _add(out, "E100", f"{path}.{key}", f"类型错误，期望 {types}")
        return None
    return value


def _validate_legacy(inst: dict, path: str, project_id: str, out: list) -> dict:
    """校验并返回 legacy 块；缺失即 E160，不返回 None 之外的兜底值。"""
    legacy = inst.get("legacy")
    if not isinstance(legacy, dict):
        _add(out, "E160", f"{path}.legacy", "每个实例都必须携带旧主键别名块")
        return {}
    lp = f"{path}.legacy"
    display_id = inst.get("display_id")
    _require(legacy, "project_id", lp, out, str)
    _require(legacy, "display_id", lp, out, str)
    _require(legacy, "primary_key", lp, out, str)
    if legacy.get("project_id") != project_id:
        _add(out, "E160", f"{lp}.project_id", "旧主键的工程必须与快照工程一致")
    if legacy.get("display_id") != display_id:
        _add(out, "E160", f"{lp}.display_id", "旧主键的显示编号必须与实例一致")
    expected_pk = f"{project_id}/{display_id}"
    if legacy.get("primary_key") != expected_pk:
        _add(out, "E160", f"{lp}.primary_key", f"旧物理主键必须是 {expected_pk!r}")
    if not isinstance(legacy.get("ambiguous", False), bool):
        _add(out, "E100", f"{lp}.ambiguous", "ambiguous 必须是布尔值")
    return legacy


def _validate_versions(inst: dict, path: str, out: list) -> list:
    versions = inst.get("versions")
    if not isinstance(versions, list) or not versions:
        _add(out, "E130", f"{path}.versions", "实例必须至少有一个冻结版本")
        return []
    last_no = 0
    last_frozen = None
    for idx, ver in enumerate(versions):
        vp = f"{path}.versions[{idx}]"
        if not isinstance(ver, dict):
            _add(out, "E100", vp, "版本必须是对象")
            continue
        no = _require(ver, "version_no", vp, out, int)
        frozen = _parse_time(ver.get("frozen_at"), f"{vp}.frozen_at", out)
        if isinstance(no, int) and not isinstance(no, bool):
            if no != last_no + 1:
                _add(out, "E131", f"{vp}.version_no", f"期望 {last_no + 1}，实际 {no}")
            last_no = no
        if frozen is not None and last_frozen is not None and frozen <= last_frozen:
            _add(out, "E133", f"{vp}.frozen_at", "冻结时间必须严格晚于上一版本")
        if frozen is not None:
            last_frozen = frozen
        deps = ver.get("deps", [])
        if not isinstance(deps, list):
            _add(out, "E100", f"{vp}.deps", "deps 必须是数组")
    return versions


def _validate_deps(inst: dict, path: str, versions: list, by_instance: dict, by_display: dict, out: list) -> None:
    own = inst.get("instance_id")
    for idx, ver in enumerate(versions):
        if not isinstance(ver, dict):
            continue
        vp = f"{path}.versions[{idx}]"
        for jdx, dep in enumerate(ver.get("deps") or []):
            dp = f"{vp}.deps[{jdx}]"
            if not isinstance(dep, dict):
                _add(out, "E100", dp, "依赖项必须是对象")
                continue
            kind = dep.get("kind")
            target = dep.get("target")
            if kind not in ("instance", "legacy"):
                _add(out, "E100", f"{dp}.kind", "kind 只能是 instance 或 legacy")
                continue
            if not isinstance(target, str) or not target:
                _add(out, "E100", f"{dp}.target", "target 必须是非空字符串")
                continue
            if kind == "instance":
                if target == own:
                    _add(out, "E152", dp, "版本不得依赖自身实例")
                    continue
                hit = by_instance.get(target)
                if hit is None:
                    _add(out, "E150", dp, f"实例 {target!r} 不在本工程快照内")
                elif hit.get("state") == STATE_RETIRED:
                    _add(out, "E151", dp, f"实例 {target!r} 已退役，依赖不可满足")
            else:
                hits = by_display.get(target, [])
                if not hits:
                    _add(out, "E150", dp, f"显示编号 {target!r} 无任何实例承载")
                elif len(hits) > 1:
                    _add(out, "E153", dp, f"显示编号 {target!r} 对应 {len(hits)} 个实例，须改用 instance 形式")
                else:
                    hit = hits[0]
                    if hit.get("instance_id") == own:
                        _add(out, "E152", dp, "版本不得依赖自身实例")
                    elif hit.get("state") == STATE_RETIRED:
                        _add(out, "E151", dp, f"实例 {hit.get('instance_id')!r} 已退役，依赖不可满足")


def validate(doc) -> list:
    """校验整份快照，返回 Violation 列表；空列表表示通过。"""
    out: list = []
    if not isinstance(doc, dict):
        _add(out, "E100", "$", "快照根节点必须是对象")
        return out
    if doc.get("contract") != CONTRACT_ID:
        _add(out, "E101", "$.contract", f"必须是 {CONTRACT_ID!r}")
    project_id = _require(doc, "project_id", "$", out, str)
    instances = doc.get("instances")
    if not isinstance(instances, list):
        _add(out, "E100", "$.instances", "instances 必须是数组")
        return out

    by_instance: dict = {}
    by_display: dict = {}
    active_alias: dict = {}
    for inst in instances:
        if not isinstance(inst, dict):
            continue
        iid = inst.get("instance_id")
        if isinstance(iid, str):
            if iid in by_instance:
                _add(out, "E110", f"instance:{iid}", "instance_id 在同一快照内重复")
            else:
                by_instance[iid] = inst
        did = inst.get("display_id")
        if isinstance(did, str):
            by_display.setdefault(did, []).append(inst)
            if inst.get("state") == STATE_ACTIVE:
                if did in active_alias:
                    _add(out, "E120", f"instance:{iid}", f"显示编号 {did!r} 已被另一活跃实例占用")
                else:
                    active_alias[did] = inst

    for idx, inst in enumerate(instances):
        if not isinstance(inst, dict):
            _add(out, "E100", f"$.instances[{idx}]", "实例必须是对象")
            continue
        path = f"$.instances[{idx}]"
        iid = _require(inst, "instance_id", path, out, str)
        if isinstance(iid, str) and not INSTANCE_ID_RE.match(iid):
            _add(out, "E111", f"{path}.instance_id", "必须形如 ti_<小写字母数字，至少 6 位>")
        did = _require(inst, "display_id", path, out, str)
        if isinstance(did, str) and not DISPLAY_ID_RE.match(did):
            _add(out, "E112", f"{path}.display_id", "必须形如 VN-001 的大写前缀加数字")
        state = _require(inst, "state", path, out, str)
        if isinstance(state, str) and state not in STATES:
            _add(out, "E100", f"{path}.state", f"state 只能是 {STATES}")
        _parse_time(inst.get("created_at"), f"{path}.created_at", out)
        legacy = _validate_legacy(inst, path, project_id or "", out)
        versions = _validate_versions(inst, path, out)

        if state == STATE_ACTIVE:
            active = inst.get("active_version")
            if not isinstance(active, int) or isinstance(active, bool):
                _add(out, "E130", f"{path}.active_version", "活跃实例必须指定整数活跃版本号")
            elif versions:
                numbers = [v.get("version_no") for v in versions if isinstance(v, dict)]
                valid_numbers = [n for n in numbers if isinstance(n, int) and not isinstance(n, bool)]
                if active not in valid_numbers:
                    _add(out, "E130", f"{path}.active_version", f"版本 {active} 未冻结")
                elif valid_numbers and active != max(valid_numbers):
                    _add(out, "E132", f"{path}.active_version", f"活跃版本必须是最新版本 {max(valid_numbers)}")
        elif state == STATE_RETIRED:
            retired_at = _parse_time(inst.get("retired_at"), f"{path}.retired_at", out)
            reason = inst.get("retired_reason")
            if not isinstance(reason, str) or not reason.strip():
                _add(out, "E140", f"{path}.retired_reason", "退役必须记录原因")
            if inst.get("active_version") is not None:
                _add(out, "E140", f"{path}.active_version", "退役实例不得保留活跃版本指针")
            if retired_at is not None:
                for idx_v, ver in enumerate(versions):
                    if not isinstance(ver, dict):
                        continue
                    frozen = _parse_time(ver.get("frozen_at"), f"{path}.versions[{idx_v}].frozen_at", [])
                    if frozen is not None and frozen > retired_at:
                        _add(out, "E142", f"{path}.versions[{idx_v}]", "退役之后不得再冻结新版本")

        if legacy.get("ambiguous") is True:
            if state != STATE_RETIRED:
                _add(out, "E161", f"{path}.legacy", "歧义历史不得作为当前活跃别名")
            note = legacy.get("ambiguity_note")
            if not isinstance(note, str) or not note.strip():
                _add(out, "E161", f"{path}.legacy.ambiguity_note", "歧义历史必须写明不可归属的原因")

        _validate_deps(inst, path, versions, by_instance, by_display, out)

    return out


if __name__ == "__main__":  # pragma: no cover - 手工排查入口
    import sys

    target = sys.argv[1] if len(sys.argv) > 1 else "samples/valid/01-minimal.json"
    found = validate(load(target))
    for item in found:
        print(item)
    print(f"{len(found)} violation(s) in {target}")
