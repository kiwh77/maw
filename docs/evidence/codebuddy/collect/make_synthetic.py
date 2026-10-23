#!/usr/bin/env python3
"""VN-301 合成反例生成器。

为什么单独一个脚本
------------------
反例是**合成的**，不是采集来的。把它们和实测样本混在同一个目录、同一种命名下，
下游很容易把反例当成来源实测（正是 G04「退出码 0 或不完整输出导致假 done」的风险）。
因此本脚本只做三件事：

1. 从已冻结的实测样本派生反例（derived_from 指向真实样本 id）；
2. 每个反例 meta 显式写 kind=synthetic、defect、expected_interpretation、do_not_use_as；
3. 把登记写入 manifest.json 的 synthetic_samples，并记录文件哈希。

反例内容只做结构性破坏（截断、丢记录、空正文、未知字段），不新增任何「看似实测」的取值；
必须假定才能构造的字段写入 assumed_values。

用法：
    python3 collect/make_synthetic.py
"""

from __future__ import annotations

import hashlib
import json
import shutil
from pathlib import Path
from typing import Any, Dict, List

EVIDENCE_DIR = Path(__file__).resolve().parent.parent
SAMPLES_DIR = EVIDENCE_DIR / "samples"
SYNTH_DIR = SAMPLES_DIR / "synthetic"
MANIFEST = EVIDENCE_DIR / "manifest.json"

SCHEMA_VERSION = "maw.evidence.codebuddy/v1"


def sha256_of(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def load_events(sample: str) -> List[Dict[str, Any]]:
    path = SAMPLES_DIR / sample / "events.jsonl"
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def load_records(sample: str) -> List[Dict[str, Any]]:
    path = SAMPLES_DIR / sample / "records.json"
    return json.loads(path.read_text(encoding="utf-8"))


def write_synthetic(sample_id: str, slug: str, meta: Dict[str, Any], files: Dict[str, str]) -> Dict[str, Any]:
    target = SYNTH_DIR / f"{sample_id}-{slug}"
    if target.exists():
        shutil.rmtree(target)
    target.mkdir(parents=True)
    digests = {}
    for name, content in files.items():
        (target / name).write_text(content, encoding="utf-8")
        digests[name] = sha256_of(target / name)
    meta = dict(meta)
    meta["files"] = digests
    (target / "meta.json").write_text(
        json.dumps(meta, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    meta["files"]["meta.json"] = sha256_of(target / "meta.json")
    return {
        "sample_id": sample_id,
        "dir": f"samples/synthetic/{sample_id}-{slug}",
        "kind": "synthetic",
        "defect": meta["defect"],
        "derived_from": meta["derived_from"],
        "files": meta["files"],
    }


def build() -> List[Dict[str, Any]]:
    base_events = load_events("VN301-S03-print-stream-json")
    base_records = load_records("VN301-S04-print-json")
    registered: List[Dict[str, Any]] = []

    # X01：事件流中间一行被截断，形成非法 JSON。
    lines = [json.dumps(e, ensure_ascii=False, sort_keys=True) for e in base_events]
    corrupted = lines[: len(lines) // 2] + ['{"type":"assistant","message":{"content":[{"type":"text","text":"MAW-OK"']
    registered.append(write_synthetic(
        "VN301-X01", "corrupted-line",
        {
            "schema": SCHEMA_VERSION, "sample_id": "VN301-X01", "kind": "synthetic",
            "defect": "corrupted_json_line",
            "derived_from": "VN301-S03",
            "expected_interpretation": "无法解析的记录只能保留为证据，不得推断文本内容，也不得据此生成 done",
            "do_not_use_as": "来源实测；本样本不可用于声称 CLI 会输出坏行",
            "assumed_values": [],
            "origin": "由 VN301-S03 事件流截断一行构造",
        },
        {"events.jsonl": "\n".join(corrupted) + "\n"},
    ))

    # X02：结果记录缺失（进程被中断或输出被截断）。
    without_result = [e for e in base_events if e.get("type") != "result"]
    registered.append(write_synthetic(
        "VN301-X02", "missing-result",
        {
            "schema": SCHEMA_VERSION, "sample_id": "VN301-X02", "kind": "synthetic",
            "defect": "missing_final_result",
            "derived_from": "VN301-S03",
            "expected_interpretation": "缺少 result 记录时运行只能判为不完整／待核实，不得判 done",
            "do_not_use_as": "来源实测；本样本不可用于声称 CLI 会丢结果",
            "assumed_values": [],
            "origin": "由 VN301-S03 删除 result 记录构造",
        },
        {"events.jsonl": "".join(json.dumps(e, ensure_ascii=False, sort_keys=True) + "\n" for e in without_result)},
    ))

    # X03：退出码 0、subtype=success，但正文为空且用量为 0（最容易被误判成成功）。
    empty_records = json.loads(json.dumps(base_records))
    for record in empty_records:
        if record.get("type") == "result":
            record["result"] = ""
            record["num_turns"] = 0
            record["usage"] = {k: (0 if isinstance(v, int) else v) for k, v in record.get("usage", {}).items()}
        if record.get("type") == "message" and record.get("role") == "assistant":
            record["content"] = [{"type": "output_text", "text": ""}]
    registered.append(write_synthetic(
        "VN301-X03", "success-without-content",
        {
            "schema": SCHEMA_VERSION, "sample_id": "VN301-X03", "kind": "synthetic",
            "defect": "success_subtype_without_content",
            "derived_from": "VN301-S04",
            "expected_interpretation": "subtype=success 不足以判定交付；空正文与零用量必须降级为待核实",
            "do_not_use_as": "来源实测；本样本不可用于声称 CLI 会返回空成功",
            "assumed_values": [
                "subtype=success 的空正文形态未实测，此处按结果记录结构构造",
                "num_turns=0 与 usage 全 0 的取值未实测，仅作解析器边界",
            ],
            "origin": "由 VN301-S04 清空正文与用量构造",
        },
        {"records.json": json.dumps(empty_records, ensure_ascii=False, indent=2, sort_keys=True) + "\n"},
    ))

    # X04：未知事件类型 + 未知字段 + 未知版本（版本化协议的向前兼容边界）。
    future = json.loads(json.dumps(base_events))
    future.append({
        "type": "system", "subtype": "hook",
        "session_id": "<SESSION_ID>", "uuid": "<UUID>",
        "hook": {"name": "<HOOK_NAME>", "status": "completed", "future_field": {"unknown_key": "<VALUE>"}},
    })
    for record in future:
        if record.get("type") == "result":
            record["cli_version"] = "9.9.9-future"
            record["future_usage_field"] = {"unit": "<UNKNOWN>", "value": 0}
    registered.append(write_synthetic(
        "VN301-X04", "unknown-event-and-version",
        {
            "schema": SCHEMA_VERSION, "sample_id": "VN301-X04", "kind": "synthetic",
            "defect": "unknown_event_type_and_fields",
            "derived_from": "VN301-S03",
            "expected_interpretation": "未识别事件与字段只保留证据，不得映射为状态迁移，也不得按旧版本口径解释用量",
            "do_not_use_as": "来源实测；本样本不可用于声称 CLI 2.173.0 会输出 hook 事件",
            "assumed_values": [
                "system/hook 事件与 cli_version 字段均未实测，按未知结构构造",
            ],
            "origin": "由 VN301-S03 追加未知事件与未知字段构造",
        },
        {"events.jsonl": "".join(json.dumps(e, ensure_ascii=False, sort_keys=True) + "\n" for e in future)},
    ))
    return registered


def main() -> int:
    if not MANIFEST.exists():
        raise SystemExit("manifest.json 不存在；请先执行 collect/collect_probe.py collect")
    manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
    synthetic = build()
    manifest["synthetic_samples"] = synthetic
    MANIFEST.write_text(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    for entry in synthetic:
        print(f"{entry['sample_id']} {entry['defect']} <- {entry['derived_from']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
