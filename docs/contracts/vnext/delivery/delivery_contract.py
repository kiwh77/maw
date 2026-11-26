"""maw 交付与验收契约 v1 —— 纯标准库校验器。

契约正文见同目录 ``delivery-manifest-contract-v1.md``。
读一份「交付快照」JSON，返回违反不变量清单。不连 Git、不跑验证命令、不读环境变量。

用法::

    from delivery_contract import load, validate
    validate(load("samples/valid/01-code-delivery-done.json"))
"""

from __future__ import annotations

import json
import re
from datetime import datetime
from pathlib import Path

CONTRACT_ID = "maw.delivery.manifest/v1"

DELIVERY_ID_RE = re.compile(r"^dlv_[a-z0-9]{3,}$")
ARTIFACT_ID_RE = re.compile(r"^art_[a-z0-9]{3,}$")
VERIFICATION_ID_RE = re.compile(r"^vfy_[a-z0-9]{3,}$")
COMMIT_RE = re.compile(r"^[0-9a-f]{7,40}$")
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")

MODES = {"code", "non_code"}
ARTIFACT_KINDS = {"commit", "file", "content_hash"}
DECLARERS = {"cli", "manual"}
RESULTS = {"pass", "fail", "manual", "pending", "stale"}
VERDICTS = {"approved", "changes_requested", "not_required"}
DECISIONS = {"done", "pending", "rejected"}

# 只有这些前缀算「结果类证据」；退出码本身不算。
NON_RESULT_EVIDENCE = ("exit_code:",)

RULES = {
    "E300": "字段缺失或类型不符",
    "E301": "契约标识不受支持",
    "E310": "交付未绑定任务实例",
    "E311": "交付未绑定任务版本",
    "E312": "交付未绑定运行",
    "E313": "提交引用格式非法或缺失",
    "E320": "产物引用指向不存在的实体",
    "E321": "产物落在写盘范围之外",
    "E330": "验证目标版本与交付任务版本不一致",
    "E331": "验证针对旧提交（与候选提交不一致）",
    "E332": "判为通过却没有证据",
    "E333": "只凭退出码 0 判为通过",
    "E334": "产物已变更，旧验证对当前产物无效",
    "E340": "声称完成但没有一条有效通过的验证",
    "E341": "声称完成却没有任何产物",
    "E342": "代码任务未合入却声称完成",
    "E343": "代码任务未通过评审却声称完成",
    "E350": "非代码任务缺人工或机器验收结论",
    "E351": "完成方式与任务模式不符（非代码任务记录合入）",
    "E360": "判为拒绝却没有失败原因",
    "E361": "声称完成却存在未处理的失败验证",
    "E370": "已被后续交付取代却仍标为完成",
    "E371": "取代关系指向不存在或自身的交付",
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
        _add(out, "E300", path, "时间必须是非空 ISO8601 字符串")
        return None
    try:
        return datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        _add(out, "E300", path, f"无法解析的时间 {raw!r}")
        return None


def _need(mapping, key, path, out, types=None):
    if not isinstance(mapping, dict) or key not in mapping or mapping[key] is None:
        _add(out, "E300", f"{path}.{key}", "必填字段缺失")
        return None
    value = mapping[key]
    if types is not None and not isinstance(value, types):
        _add(out, "E300", f"{path}.{key}", f"类型错误，期望 {types}")
        return None
    return value


def _result_evidence(evidence) -> list:
    """过滤出「结果类」证据：退出码不算。"""
    if not isinstance(evidence, list):
        return []
    return [e for e in evidence if isinstance(e, str) and not e.startswith(NON_RESULT_EVIDENCE)]


def _verification_is_effective(ver: dict, delivery: dict, artifacts: dict) -> bool:
    """这条验证结论是否能支撑「当前交付」完成。"""
    mode = delivery.get("mode")
    ok_results = {"pass"} | ({"manual"} if mode == "non_code" else set())
    if ver.get("result") not in ok_results:
        return False
    if ver.get("target_task_version") != delivery.get("task_version"):
        return False
    if delivery.get("mode") == "code" and ver.get("target_commit") != delivery.get("candidate_commit"):
        return False
    if not _result_evidence(ver.get("evidence")):
        return False
    art_id = ver.get("target_artifact_id")
    if art_id is not None:
        art = artifacts.get(art_id)
        if art is None or art.get("sha256") != ver.get("target_artifact_sha256"):
            return False
    return True


def _validate_artifacts(delivery: dict, path: str, out: list) -> dict:
    artifacts = delivery.get("artifacts")
    indexed: dict = {}
    if not isinstance(artifacts, list):
        _add(out, "E300", f"{path}.artifacts", "artifacts 必须是数组")
        return indexed
    for idx, art in enumerate(artifacts):
        ap = f"{path}.artifacts[{idx}]"
        if not isinstance(art, dict):
            _add(out, "E300", ap, "产物必须是对象")
            continue
        aid = _need(art, "artifact_id", ap, out, str)
        if isinstance(aid, str):
            if not ARTIFACT_ID_RE.match(aid):
                _add(out, "E300", f"{ap}.artifact_id", "必须形如 art_<小写字母数字>")
            indexed[aid] = art
        kind = _need(art, "kind", ap, out, str)
        if isinstance(kind, str) and kind not in ARTIFACT_KINDS:
            _add(out, "E300", f"{ap}.kind", f"kind 只能是 {sorted(ARTIFACT_KINDS)}")
        _need(art, "ref", ap, out, str)
        declared_by = _need(art, "declared_by", ap, out, str)
        if isinstance(declared_by, str) and declared_by not in DECLARERS:
            _add(out, "E300", f"{ap}.declared_by", f"declared_by 只能是 {sorted(DECLARERS)}")
        exists = art.get("exists")
        if not isinstance(exists, bool):
            _add(out, "E300", f"{ap}.exists", "必须显式给出产物实体是否存在")
        elif exists is False:
            _add(out, "E320", f"{ap}.exists", "声明了产物却查不到实体，不能计入交付")
        in_scope = art.get("in_write_scope")
        if not isinstance(in_scope, bool):
            _add(out, "E300", f"{ap}.in_write_scope", "必须显式给出是否在写盘范围内")
        elif in_scope is False:
            _add(out, "E321", f"{ap}.in_write_scope", "产物越出本卡写盘范围")
        if kind in ("file", "content_hash"):
            sha = art.get("sha256")
            if not isinstance(sha, str) or not SHA256_RE.match(sha):
                _add(out, "E300", f"{ap}.sha256", "文件类产物必须给出 64 位内容哈希")
    return indexed


def _validate_verifications(delivery: dict, path: str, artifacts: dict, out: list) -> list:
    rows = delivery.get("verification")
    if not isinstance(rows, list):
        _add(out, "E300", f"{path}.verification", "verification 必须是数组")
        return []
    for idx, ver in enumerate(rows):
        vp = f"{path}.verification[{idx}]"
        if not isinstance(ver, dict):
            _add(out, "E300", vp, "验证记录必须是对象")
            continue
        vid = _need(ver, "verification_id", vp, out, str)
        if isinstance(vid, str) and not VERIFICATION_ID_RE.match(vid):
            _add(out, "E300", f"{vp}.verification_id", "必须形如 vfy_<小写字母数字>")
        _need(ver, "command", vp, out, str)
        _time(ver.get("ran_at"), f"{vp}.ran_at", out)
        target_version = _need(ver, "target_task_version", vp, out, int)
        if isinstance(target_version, int) and target_version != delivery.get("task_version"):
            _add(out, "E330", f"{vp}.target_task_version", "验证必须针对本次交付的任务版本")
        result = _need(ver, "result", vp, out, str)
        if isinstance(result, str) and result not in RESULTS:
            _add(out, "E300", f"{vp}.result", f"result 只能是 {sorted(RESULTS)}")
        evidence = ver.get("evidence")
        if evidence is None:
            evidence = []
        if not isinstance(evidence, list):
            _add(out, "E300", f"{vp}.evidence", "evidence 必须是数组")
            evidence = []
        if delivery.get("mode") == "code":
            commit = ver.get("target_commit")
            if not isinstance(commit, str) or not COMMIT_RE.match(commit):
                _add(out, "E313", f"{vp}.target_commit", "代码验证必须记录被测提交")
            elif result == "pass" and commit != delivery.get("candidate_commit"):
                _add(out, "E331", f"{vp}.target_commit", "验证的是旧提交，不代表当前候选")
        if result == "pass":
            if not evidence:
                _add(out, "E332", f"{vp}.evidence", "判为通过必须带证据")
            elif not _result_evidence(evidence):
                _add(out, "E333", f"{vp}.evidence", "只有退出码，没有结果类证据")
        art_id = ver.get("target_artifact_id")
        if art_id is not None:
            art = artifacts.get(art_id)
            if art is None:
                _add(out, "E300", f"{vp}.target_artifact_id", f"产物 {art_id!r} 不在本次交付内")
            elif art.get("sha256") != ver.get("target_artifact_sha256") and result in ("pass", "manual"):
                _add(out, "E334", f"{vp}.target_artifact_sha256", "产物已变更，该验证对当前产物无效")
    return rows


def _validate_delivery(dlv: dict, path: str, by_id: dict, out: list) -> None:
    did = _need(dlv, "delivery_id", path, out, str)
    if isinstance(did, str) and not DELIVERY_ID_RE.match(did):
        _add(out, "E300", f"{path}.delivery_id", "必须形如 dlv_<小写字母数字>")
    ti = dlv.get("task_instance")
    if not isinstance(ti, str) or not ti.strip():
        _add(out, "E310", f"{path}.task_instance", "交付必须绑定任务实例")
    tv = dlv.get("task_version")
    if not isinstance(tv, int) or isinstance(tv, bool) or tv < 1:
        _add(out, "E311", f"{path}.task_version", "交付必须绑定任务定义版本")
    run_id = dlv.get("run_id")
    if not isinstance(run_id, str) or not run_id.startswith("run_"):
        _add(out, "E312", f"{path}.run_id", "交付必须绑定产生它的运行")
    mode = _need(dlv, "mode", path, out, str)
    if isinstance(mode, str) and mode not in MODES:
        _add(out, "E300", f"{path}.mode", f"mode 只能是 {sorted(MODES)}")
    _time(dlv.get("delivered_at"), f"{path}.delivered_at", out)

    baseline = dlv.get("baseline_commit")
    if not isinstance(baseline, str) or not COMMIT_RE.match(baseline):
        _add(out, "E313", f"{path}.baseline_commit", "必须记录基线提交")
    candidate = dlv.get("candidate_commit")
    if mode == "code":
        if not isinstance(candidate, str) or not COMMIT_RE.match(candidate):
            _add(out, "E313", f"{path}.candidate_commit", "代码交付必须记录候选提交")

    review = dlv.get("review") if isinstance(dlv.get("review"), dict) else {}
    merge = dlv.get("merge") if isinstance(dlv.get("merge"), dict) else {}
    completion = dlv.get("completion") if isinstance(dlv.get("completion"), dict) else {}
    decision = completion.get("decision")
    if decision is not None and decision not in DECISIONS:
        _add(out, "E300", f"{path}.completion.decision", f"decision 只能是 {sorted(DECISIONS)}")
    _time(completion.get("decided_at"), f"{path}.completion.decided_at", out)

    artifacts = _validate_artifacts(dlv, path, out)
    verifications = _validate_verifications(dlv, path, artifacts, out)

    if decision == "done":
        if mode == "code":
            if merge.get("merged") is not True:
                _add(out, "E342", f"{path}.merge.merged", "代码任务以合入完成作为 done 条件")
            if merge.get("required") is False:
                _add(out, "E351", f"{path}.merge.required", "代码任务不得声明合入不适用")
            if review.get("required") is True and review.get("verdict") != "approved":
                _add(out, "E343", f"{path}.review.verdict", "评审未通过不得 done")
            if not artifacts:
                _add(out, "E341", f"{path}.artifacts", "代码交付必须至少有一个产物引用")
        else:
            if review.get("verdict") != "approved":
                _add(out, "E350", f"{path}.review.verdict", "非代码任务需要人工或机器验收通过")
            if merge.get("merged") is True:
                _add(out, "E351", f"{path}.merge.merged", "非代码任务不应记录合入")
        if not any(_verification_is_effective(v, dlv, artifacts) for v in verifications if isinstance(v, dict)):
            _add(out, "E340", f"{path}.completion.decision", "没有一条有效通过的验证支撑完成")
        for idx, ver in enumerate(verifications):
            if isinstance(ver, dict) and ver.get("result") == "fail":
                _add(out, "E361", f"{path}.verification[{idx}]", "仍存在失败验证却声称完成")
        if isinstance(dlv.get("superseded_by"), str):
            _add(out, "E370", f"{path}.superseded_by", "已被后续交付取代，旧结论不得保留 done")

    if decision == "rejected":
        reason = completion.get("reason")
        if not isinstance(reason, str) or not reason.strip():
            _add(out, "E360", f"{path}.completion.reason", "拒绝必须写明原因")

    supersedes = dlv.get("supersedes")
    if supersedes is not None:
        if not isinstance(supersedes, str):
            _add(out, "E300", f"{path}.supersedes", "supersedes 必须是交付 ID 或 null")
        elif supersedes == did:
            _add(out, "E371", f"{path}.supersedes", "交付不得取代自身")
        elif supersedes not in by_id:
            _add(out, "E371", f"{path}.supersedes", f"被取代的交付 {supersedes!r} 不在快照内")


def validate(doc) -> list:
    out: list = []
    if not isinstance(doc, dict):
        _add(out, "E300", "$", "快照根节点必须是对象")
        return out
    if doc.get("contract") != CONTRACT_ID:
        _add(out, "E301", "$.contract", f"必须是 {CONTRACT_ID!r}")
    _need(doc, "project_id", "$", out, str)

    deliveries = doc.get("deliveries")
    if not isinstance(deliveries, list):
        _add(out, "E300", "$.deliveries", "deliveries 必须是数组")
        return out

    by_id: dict = {}
    for idx, dlv in enumerate(deliveries):
        if not isinstance(dlv, dict):
            continue
        did = dlv.get("delivery_id")
        if isinstance(did, str):
            if did in by_id:
                _add(out, "E300", f"$.deliveries[{idx}].delivery_id", "delivery_id 重复")
            else:
                by_id[did] = dlv

    for idx, dlv in enumerate(deliveries):
        path = f"$.deliveries[{idx}]"
        if not isinstance(dlv, dict):
            _add(out, "E300", path, "交付必须是对象")
            continue
        _validate_delivery(dlv, path, by_id, out)

    return out


if __name__ == "__main__":  # pragma: no cover
    import sys

    target = sys.argv[1] if len(sys.argv) > 1 else "samples/valid/01-code-delivery-done.json"
    found = validate(load(target))
    for item in found:
        print(item)
    print(f"{len(found)} violation(s) in {target}")
