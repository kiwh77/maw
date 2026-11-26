"""maw 运行状态与资源契约 v1 —— 纯标准库校验器。

契约正文见同目录 ``runtime-state-contract-v1.md``。
读一份「运行态快照」JSON，返回违反不变量清单。不连数据库、不读环境变量、不依赖厂商字段。

用法::

    from runtime_contract import load, validate
    validate(load("samples/valid/01-reserved-run.json"))
"""

from __future__ import annotations

import json
import re
from datetime import datetime
from pathlib import Path

CONTRACT_ID = "maw.runtime.state/v1"

RUN_ID_RE = re.compile(r"^run_[a-z0-9]{3,}$")
OPERATION_ID_RE = re.compile(r"^op_[a-z0-9]{3,}$")
LEASE_ID_RE = re.compile(r"^ls_[a-z0-9]{3,}$")
EVENT_ID_RE = re.compile(r"^evt_[a-z0-9]{3,}$")

# §7.2 主路径。终态不再出发任何转移。
TRANSITIONS = {
    "reserved": {"preparing", "stopping"},
    "preparing": {"launching", "stopping", "failed"},
    "launching": {"running", "stopping", "failed"},
    "running": {"waiting", "stopping", "completed", "failed"},
    "waiting": {"running", "stopping", "completed", "failed"},
    "stopping": {"cancelled", "timed_out"},
}
PHASES = set(TRANSITIONS) | {"completed", "failed", "cancelled", "timed_out"}
TERMINAL = {"completed", "failed", "cancelled", "timed_out"}
FINISHED = {"completed", "failed", "timed_out"}  # 有结果但不算 cancel
LEASE_KINDS = {"node_slot", "workspace", "merge"}
OP_KINDS = {"dispatch", "cancel", "verify"}

RULES = {
    "E200": "字段缺失或类型不符",
    "E201": "契约标识不受支持",
    "E210": "run_id 重复",
    "E211": "operation_id 重复",
    "E212": "event_id 重复",
    "E213": "事件游标非严格递增（游标是入库顺序，不是发生顺序）",
    "E214": "事件缺少 schema_version",
    "E220": "未知的运行阶段",
    "E221": "非法阶段转移（含从终态出发）",
    "E222": "阶段历史末项与当前阶段不一致",
    "E223": "时间倒序：结束早于开始，或阶段历史时间非递增",
    "E230": "失败／取消／超时终态缺少证据",
    "E231": "cancelled 却未确认进程已停止",
    "E232": "核实状态 unknown 却表现为终态",
    "E240": "同一任务实例存在多个有效占用",
    "E241": "进程仍在占用时释放了节点槽",
    "E242": "产物未冻结／未验收就释放了工作目录",
    "E243": "释放使用的代际与持有时不一致",
    "E250": "已成终态的运行被追改为 cancelled",
    "E251": "对已结束运行的取消操作未返回 already_finished",
    "E252": "幂等键相同但请求摘要不同",
    "E260": "当前阶段缺少同事务提交的对应事件",
    "E261": "占用已释放但缺少 lease.released 事件",
    "E262": "发生时间晚于接收时间却标记为 observed（时钟不可信未标注）",
    "E263": "事件引用了不存在的运行",
    "E270": "自动执行已开启但未配置受管执行上限",
}


class Violation:
    __slots__ = ("code", "path", "message")

    def __init__(self, code: str, path: str, message: str) -> None:
        self.code = code
        self.path = path
        self.message = message

    def to_dict(self) -> dict:
        return {"code": self.code, "path": self.path, "message": self.message}

    def __str__(self) -> str:
        return f"{self.code} @ {self.path}: {self.message}"

    def __repr__(self) -> str:  # pragma: no cover
        return f"Violation({self.code!r}, {self.path!r}, {self.message!r})"


def _add(out: list, code: str, path: str, message: str) -> None:
    out.append(Violation(code, path, message))


def load(path) -> object:
    return json.loads(Path(path).read_text(encoding="utf-8"))


def _time(raw, path: str, out: list):
    if not isinstance(raw, str) or not raw.strip():
        _add(out, "E200", path, "时间必须是非空 ISO8601 字符串")
        return None
    try:
        return datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        _add(out, "E200", path, f"无法解析的时间 {raw!r}")
        return None


def _need(mapping, key, path, out, types=None, required=True):
    if not isinstance(mapping, dict) or key not in mapping:
        if required:
            _add(out, "E200", f"{path}.{key}", "必填字段缺失")
        return None
    value = mapping[key]
    if types is not None and not isinstance(value, types):
        _add(out, "E200", f"{path}.{key}", f"类型错误，期望 {types}")
        return None
    return value


def _occupies(run: dict) -> bool:
    """该运行是否仍计入占用。

    §10.2：未到终态、unknown、以及「已失败但进程停止未确认」都继续占用。
    """
    phase = run.get("phase")
    if run.get("reconciliation_state") == "unknown":
        return True
    if phase not in TERMINAL:
        return True
    if phase == "failed" and not run.get("process_stop_confirmed"):
        return True
    if phase == "cancelled" and not run.get("process_stop_confirmed"):
        return True
    return False


def _validate_run(run, path, out) -> None:
    rid = _need(run, "run_id", path, out, str)
    if isinstance(rid, str) and not RUN_ID_RE.match(rid):
        _add(out, "E200", f"{path}.run_id", "必须形如 run_<小写字母数字>")
    _need(run, "task_instance", path, out, str)
    _need(run, "task_version", path, out, int)
    generation = _need(run, "generation", path, out, int)
    if isinstance(generation, int) and generation < 1:
        _add(out, "E200", f"{path}.generation", "代际从 1 开始")
    phase = _need(run, "phase", path, out, str)
    if isinstance(phase, str) and phase not in PHASES:
        _add(out, "E220", f"{path}.phase", f"未知阶段 {phase!r}")
    recon = run.get("reconciliation_state", "confirmed")
    if recon not in ("confirmed", "unknown"):
        _add(out, "E200", f"{path}.reconciliation_state", "只能是 confirmed 或 unknown")

    history = run.get("phase_history")
    if not isinstance(history, list) or not history:
        _add(out, "E200", f"{path}.phase_history", "必须记录阶段历史")
        history = []
    prev_phase = None
    prev_at = None
    for idx, step in enumerate(history):
        sp = f"{path}.phase_history[{idx}]"
        if not isinstance(step, dict):
            _add(out, "E200", sp, "阶段记录必须是对象")
            continue
        ph = _need(step, "phase", sp, out, str)
        at = _time(step.get("at"), f"{sp}.at", out)
        if isinstance(ph, str):
            if ph not in PHASES:
                _add(out, "E220", f"{sp}.phase", f"未知阶段 {ph!r}")
            elif prev_phase is None:
                if ph != "reserved":
                    _add(out, "E221", f"{sp}.phase", "运行必须从 reserved 开始")
            elif ph not in TRANSITIONS.get(prev_phase, set()):
                _add(out, "E221", f"{sp}.phase", f"非法转移 {prev_phase} -> {ph}")
        if at is not None and prev_at is not None and at <= prev_at:
            _add(out, "E223", f"{sp}.at", "阶段时间必须严格递增")
        if at is not None:
            prev_at = at
        prev_phase = ph
    if history and isinstance(phase, str) and prev_phase != phase:
        _add(out, "E222", f"{path}.phase", f"历史末项是 {prev_phase!r}，当前是 {phase!r}")

    started = _time(run.get("started_at"), f"{path}.started_at", out) if run.get("started_at") else None
    finished = _time(run.get("finished_at"), f"{path}.finished_at", out) if run.get("finished_at") else None
    if started and finished and finished < started:
        _add(out, "E223", f"{path}.finished_at", "结束时间早于开始时间（不得静默产生负时长）")

    if isinstance(phase, str) and phase in ("failed", "cancelled", "timed_out"):
        evidence = run.get("evidence")
        if not isinstance(evidence, list) or not evidence:
            _add(out, "E230", f"{path}.evidence", f"{phase} 必须带证据")
    if phase == "cancelled" and not run.get("process_stop_confirmed"):
        _add(out, "E231", f"{path}.process_stop_confirmed", "取消必须先确认进程已停止")
    if recon == "unknown" and isinstance(phase, str) and phase in TERMINAL:
        _add(out, "E232", f"{path}.phase", "unknown 只表明观测缺失，不得直接落终态")

    seq = [s.get("phase") for s in history if isinstance(s, dict)]
    if "cancelled" in seq:
        for prior in seq[: seq.index("cancelled")]:
            if prior in FINISHED:
                _add(out, "E250", f"{path}.phase_history", f"已确认 {prior} 之后不得再追改成 cancelled")
                break


def _validate_lease(lease, path, runs: dict, out) -> None:
    lid = _need(lease, "lease_id", path, out, str)
    if isinstance(lid, str) and not LEASE_ID_RE.match(lid):
        _add(out, "E200", f"{path}.lease_id", "必须形如 ls_<小写字母数字>")
    kind = _need(lease, "kind", path, out, str)
    if isinstance(kind, str) and kind not in LEASE_KINDS:
        _add(out, "E200", f"{path}.kind", f"kind 只能是 {sorted(LEASE_KINDS)}")
    _need(lease, "resource_id", path, out, str)
    holder = _need(lease, "holder_run", path, out, str)
    generation = _need(lease, "generation", path, out, int)
    released_at = lease.get("released_at")
    if released_at is not None:
        _time(released_at, f"{path}.released_at", out)
        release_gen = lease.get("release_generation")
        if not isinstance(release_gen, int):
            _add(out, "E200", f"{path}.release_generation", "释放必须记录释放代际")
        elif isinstance(generation, int) and release_gen != generation:
            _add(out, "E243", f"{path}.release_generation", f"释放代际 {release_gen} != 持有代际 {generation}")
        run = runs.get(holder) if isinstance(holder, str) else None
        if run is not None:
            if kind == "node_slot" and _occupies(run):
                _add(out, "E241", f"{path}.released_at", "运行仍计入占用时不得释放节点槽")
            if kind == "workspace" and not run.get("delivery_frozen"):
                _add(out, "E242", f"{path}.released_at", "产物未冻结时不得释放工作目录")


def _validate_operation(op, path, runs: dict, out) -> None:
    oid = _need(op, "operation_id", path, out, str)
    if isinstance(oid, str) and not OPERATION_ID_RE.match(oid):
        _add(out, "E200", f"{path}.operation_id", "必须形如 op_<小写字母数字>")
    kind = _need(op, "kind", path, out, str)
    if isinstance(kind, str) and kind not in OP_KINDS:
        _add(out, "E200", f"{path}.kind", f"kind 只能是 {sorted(OP_KINDS)}")
    _need(op, "state", path, out, str)
    _need(op, "idempotency_key", path, out, str)
    _need(op, "request_digest", path, out, str)
    target = op.get("target_run")
    if target is not None and target not in runs:
        _add(out, "E200", f"{path}.target_run", f"目标运行 {target!r} 不在快照内")
    if kind == "cancel" and op.get("state") == "done":
        result = op.get("result")
        if result not in ("applied", "already_finished"):
            _add(out, "E200", f"{path}.result", "取消完成必须给出 applied 或 already_finished")
        run = runs.get(target) if isinstance(target, str) else None
        if run is not None:
            phase = run.get("phase")
            if phase in FINISHED and result != "already_finished":
                _add(out, "E251", f"{path}.result", f"运行已是 {phase}，取消只能返回 already_finished")


def _validate_event(ev, path, runs: dict, out) -> None:
    eid = _need(ev, "event_id", path, out, str)
    if isinstance(eid, str) and not EVENT_ID_RE.match(eid):
        _add(out, "E200", f"{path}.event_id", "必须形如 evt_<小写字母数字>")
    _need(ev, "cursor", path, out, int)
    sv = ev.get("schema_version")
    if not isinstance(sv, int) or isinstance(sv, bool) or sv < 1:
        _add(out, "E214", f"{path}.schema_version", "事件必须携带整数契约版本")
    _need(ev, "type", path, out, str)
    rid = ev.get("run_id")
    if rid is not None and rid not in runs:
        _add(out, "E263", f"{path}.run_id", f"事件引用了不存在的运行 {rid!r}")
    occurred = _time(ev.get("occurred_at"), f"{path}.occurred_at", out)
    received = _time(ev.get("received_at"), f"{path}.received_at", out)
    quality = ev.get("quality", "observed")
    if quality not in ("observed", "estimated", "unknown"):
        _add(out, "E200", f"{path}.quality", "quality 只能是 observed/estimated/unknown")
    if occurred and received and occurred > received and quality == "observed":
        _add(out, "E262", f"{path}.quality", "发生晚于接收说明时钟不可信，必须降级为 estimated/unknown")


def validate(doc) -> list:
    out: list = []
    if not isinstance(doc, dict):
        _add(out, "E200", "$", "快照根节点必须是对象")
        return out
    if doc.get("contract") != CONTRACT_ID:
        _add(out, "E201", "$.contract", f"必须是 {CONTRACT_ID!r}")
    _need(doc, "project_id", "$", out, str)

    auto = doc.get("auto_execution")
    if not isinstance(auto, dict):
        _add(out, "E200", "$.auto_execution", "必须显式给出自动执行配置（默认关闭也要写）")
    else:
        enabled = auto.get("enabled")
        if not isinstance(enabled, bool):
            _add(out, "E200", "$.auto_execution.enabled", "必须显式布尔值")
        limit = auto.get("managed_execution_limit")
        if enabled is True:
            if not isinstance(limit, int) or isinstance(limit, bool) or limit < 1:
                _add(out, "E270", "$.auto_execution.managed_execution_limit", "开启自动执行必须给出正整数上限")
        elif isinstance(limit, int) and limit < 0:
            _add(out, "E200", "$.auto_execution.managed_execution_limit", "上限不得为负")

    runs_list = doc.get("runs")
    ops_list = doc.get("operations") or []
    leases_list = doc.get("leases") or []
    events_list = doc.get("events") or []
    for name, value in (("runs", runs_list), ("operations", ops_list), ("leases", leases_list), ("events", events_list)):
        if not isinstance(value, list):
            _add(out, "E200", f"$.{name}", "必须是数组")
            return out

    runs: dict = {}
    for idx, run in enumerate(runs_list):
        path = f"$.runs[{idx}]"
        if not isinstance(run, dict):
            _add(out, "E200", path, "运行必须是对象")
            continue
        rid = run.get("run_id")
        if isinstance(rid, str):
            if rid in runs:
                _add(out, "E210", f"{path}.run_id", "run_id 重复")
            else:
                runs[rid] = run
        _validate_run(run, path, out)

    # E240：同一任务实例至多一个有效占用
    by_instance: dict = {}
    for idx, run in enumerate(runs_list):
        if not isinstance(run, dict):
            continue
        ti = run.get("task_instance")
        if isinstance(ti, str) and _occupies(run):
            by_instance.setdefault(ti, []).append(f"$.runs[{idx}]")
    for ti, paths in by_instance.items():
        if len(paths) > 1:
            for p in paths:
                _add(out, "E240", p, f"任务实例 {ti!r} 同时有 {len(paths)} 个有效占用")

    seen_ops: dict = {}
    for idx, op in enumerate(ops_list):
        path = f"$.operations[{idx}]"
        if not isinstance(op, dict):
            _add(out, "E200", path, "操作必须是对象")
            continue
        oid = op.get("operation_id")
        if isinstance(oid, str):
            if oid in seen_ops:
                _add(out, "E211", f"{path}.operation_id", "operation_id 重复")
            else:
                seen_ops[oid] = op
        _validate_operation(op, path, runs, out)

    seen_keys: dict = {}
    for idx, op in enumerate(ops_list):
        if not isinstance(op, dict):
            continue
        key = op.get("idempotency_key")
        digest = op.get("request_digest")
        if not isinstance(key, str):
            continue
        if key in seen_keys and seen_keys[key] != digest:
            _add(out, "E252", f"$.operations[{idx}].idempotency_key", "同键不同内容必须返回冲突，不得复用")
        seen_keys.setdefault(key, digest)

    seen_leases: set = set()
    released_leases: set = set()
    for idx, lease in enumerate(leases_list):
        path = f"$.leases[{idx}]"
        if not isinstance(lease, dict):
            _add(out, "E200", path, "占用必须是对象")
            continue
        lid = lease.get("lease_id")
        if isinstance(lid, str):
            if lid in seen_leases:
                _add(out, "E200", f"{path}.lease_id", "lease_id 重复")
            seen_leases.add(lid)
            if lease.get("released_at") is not None:
                released_leases.add(lid)
        _validate_lease(lease, path, runs, out)

    seen_events: dict = {}
    last_cursor = None
    for idx, ev in enumerate(events_list):
        path = f"$.events[{idx}]"
        if not isinstance(ev, dict):
            _add(out, "E200", path, "事件必须是对象")
            continue
        eid = ev.get("event_id")
        if isinstance(eid, str):
            if eid in seen_events:
                _add(out, "E212", f"{path}.event_id", "event_id 重复")
            else:
                seen_events[eid] = ev
        cursor = ev.get("cursor")
        if isinstance(cursor, int) and not isinstance(cursor, bool):
            if last_cursor is not None and cursor <= last_cursor:
                _add(out, "E213", f"{path}.cursor", f"游标必须严格递增，{cursor} 接在 {last_cursor} 之后")
            last_cursor = cursor
        _validate_event(ev, path, runs, out)

    # E260：每个运行当前阶段都要有同事务提交的事件
    phases_by_run: dict = {}
    released_events: set = set()
    for ev in events_list:
        if not isinstance(ev, dict):
            continue
        if ev.get("type") == "run.phase":
            phases_by_run.setdefault(ev.get("run_id"), set()).add(ev.get("to_phase"))
        if ev.get("type") == "lease.released":
            released_events.add(ev.get("lease_id"))
    for idx, run in enumerate(runs_list):
        if not isinstance(run, dict):
            continue
        phase = run.get("phase")
        rid = run.get("run_id")
        if isinstance(rid, str) and isinstance(phase, str):
            if phase not in phases_by_run.get(rid, set()):
                _add(out, "E260", f"$.runs[{idx}].phase", f"缺少 {rid} 进入 {phase} 的事件")
    for idx, lease in enumerate(leases_list):
        if not isinstance(lease, dict):
            continue
        lid = lease.get("lease_id")
        if isinstance(lid, str) and lease.get("released_at") is not None and lid not in released_events:
            _add(out, "E261", f"$.leases[{idx}].released_at", "释放必须留下 lease.released 事件")

    return out


if __name__ == "__main__":  # pragma: no cover
    import sys

    target = sys.argv[1] if len(sys.argv) > 1 else "samples/valid/01-reserved-run.json"
    found = validate(load(target))
    for item in found:
        print(item)
    print(f"{len(found)} violation(s) in {target}")
