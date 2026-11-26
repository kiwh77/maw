#!/usr/bin/env python3
"""VN-301 证据包的验收测试。

运行：
    python3 -m unittest discover -s docs/evidence/codebuddy -p 'test_*.py'

这些测试只读样本，不联网、不调用 CLI。它们要回答的是四件事：

1. 每个样本的「来源」是不是真的（哈希、版本、时间、退出码、参数）；
2. 实测与合成有没有被混写（合成必须有缺陷声明与派生来源）；
3. 脱敏有没有漏（路径、标识、凭据、CLI 注入上下文）；
4. 文档里的事实有没有样本支撑（事件顺序、退出码 0 不等于成功、用量口径）。
"""

from __future__ import annotations

import hashlib
import json
import re
import unittest
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

EVIDENCE_DIR = Path(__file__).resolve().parent
SAMPLES_DIR = EVIDENCE_DIR / "samples"
MANIFEST_PATH = EVIDENCE_DIR / "manifest.json"
MATRIX_PATH = EVIDENCE_DIR / "capability-matrix.json"
INVENTORY_PATH = EVIDENCE_DIR / "schema" / "event-inventory.json"

SEMVER_RE = re.compile(r"^\d+\.\d+\.\d+$")
ISO_UTC_RE = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")
STATE_VALUES = {"observed", "unobserved", "unknown", "unsupported"}

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


def load_json(path: Path) -> Any:
    return json.loads(path.read_text(encoding="utf-8"))


def sha256_of(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def type_name(value: Any) -> str:
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


def fields_of(node: Any, prefix: str = "") -> Dict[str, str]:
    out: Dict[str, str] = {}
    for key, value in node.items():
        path = f"{prefix}{key}"
        out[path] = type_name(value)
        if isinstance(value, dict) and value:
            out.update(fields_of(value, f"{path}."))
    return out


def sample_dirs() -> List[Path]:
    return sorted(p for p in SAMPLES_DIR.glob("VN301-*") if p.is_dir()) + \
        sorted(p for p in (SAMPLES_DIR / "synthetic").glob("VN301-*") if p.is_dir())


def records_of(sample_dir: Path) -> Tuple[str, List[Dict[str, Any]]]:
    """返回 (output_format, 记录列表)。"""
    meta = load_json(sample_dir / "meta.json")
    fmt = meta.get("output_format")
    events = sample_dir / "events.jsonl"
    if events.exists():
        return fmt, [json.loads(line) for line in events.read_text(encoding="utf-8").splitlines() if line.strip()]
    records = sample_dir / "records.json"
    if records.exists():
        doc = load_json(records)
        return fmt, doc if isinstance(doc, list) else [doc]
    return fmt, []


def sample_by_id(sample_id: str) -> Path:
    for candidate in sample_dirs():
        meta = load_json(candidate / "meta.json")
        if meta.get("sample_id") == sample_id:
            return candidate
    raise AssertionError(f"样本不存在: {sample_id}")


class ManifestIntegrityTest(unittest.TestCase):
    def setUp(self) -> None:
        self.manifest = load_json(MANIFEST_PATH)

    def test_manifest_declares_schema_and_environment(self) -> None:
        self.assertTrue(self.manifest["schema"].startswith("maw.evidence.codebuddy/"))
        env = self.manifest["environment"]
        self.assertEqual(env["cli"], "codebuddy")
        self.assertRegex(env["cli_version"], SEMVER_RE)
        self.assertTrue(env["cli_version_provenance"].startswith("measured:"))
        self.assertIn("PATH", env["env_whitelist"])
        self.assertNotIn("CODEBUDDY_CONFIG_DIR", env["env_whitelist"])

    def test_manifest_covers_every_sample_dir(self) -> None:
        declared = {e["dir"] for e in self.manifest["samples"]} | \
                   {e["dir"] for e in self.manifest["synthetic_samples"]}
        on_disk = {str(p.relative_to(EVIDENCE_DIR)) for p in sample_dirs()}
        self.assertEqual(declared, on_disk)

    def test_sample_ids_unique_and_prefixed(self) -> None:
        ids = [e["sample_id"] for e in self.manifest["samples"]] + \
              [e["sample_id"] for e in self.manifest["synthetic_samples"]]
        self.assertEqual(len(ids), len(set(ids)))
        for sample_id in ids:
            self.assertRegex(sample_id, r"^VN301-[SX]\d{2}$")

    def test_file_hashes_match_declared(self) -> None:
        for entry in self.manifest["samples"] + self.manifest["synthetic_samples"]:
            sample_dir = EVIDENCE_DIR / entry["dir"]
            declared = set(entry["files"])
            actual = {p.name for p in sample_dir.iterdir() if p.is_file()}
            self.assertEqual(declared, actual, f"{entry['sample_id']} 文件清单与磁盘不一致")
            for name, digest in entry["files"].items():
                self.assertEqual(digest, sha256_of(sample_dir / name),
                                 f"{entry['sample_id']}/{name} 内容已变更，哈希不匹配")


class MeasuredSampleTest(unittest.TestCase):
    def setUp(self) -> None:
        self.manifest = load_json(MANIFEST_PATH)
        self.measured = [EVIDENCE_DIR / e["dir"] for e in self.manifest["samples"]]

    def test_every_measured_sample_declares_source(self) -> None:
        for sample_dir in self.measured:
            meta = load_json(sample_dir / "meta.json")
            self.assertEqual(meta["kind"], "measured", sample_dir.name)
            source = meta["source"]
            for key in ("cli", "cli_entry", "cli_version", "cli_version_source", "platform",
                        "cwd_template", "argv_template", "env_whitelist", "captured_at", "collector"):
                self.assertIn(key, source, f"{sample_dir.name} 缺 source.{key}")
            self.assertRegex(source["cli_version"], SEMVER_RE)
            self.assertRegex(source["captured_at"], ISO_UTC_RE)
            self.assertEqual(source["cli_version"], self.manifest["environment"]["cli_version"])
            self.assertTrue(meta["redactions"], f"{sample_dir.name} 未声明脱敏规则")

    def test_argv_template_has_no_absolute_path(self) -> None:
        for sample_dir in self.measured:
            source = load_json(sample_dir / "meta.json")["source"]
            joined = " ".join(source["argv_template"])
            self.assertNotIn("/Users/", joined, f"{sample_dir.name} 参数模板泄漏绝对路径")
            self.assertIn("<CLI_PATH>", source["argv_template"][0])

    def test_outcome_fields_are_typed(self) -> None:
        for sample_dir in self.measured:
            outcome = load_json(sample_dir / "meta.json")["outcome"]
            for key in ("exit_code", "stdout_bytes", "stderr_bytes", "duration_ms",
                        "protocol_records", "result_record_present", "success", "success_basis"):
                self.assertIn(key, outcome, f"{sample_dir.name} 缺 outcome.{key}")
            self.assertIsInstance(outcome["success"], bool)
            self.assertIsInstance(outcome["protocol_records"], int)
            if outcome["exit_code"] is not None:
                self.assertIsInstance(outcome["exit_code"], int)

    def test_success_flag_follows_declared_basis(self) -> None:
        for sample_dir in self.measured:
            meta = load_json(sample_dir / "meta.json")
            outcome = meta["outcome"]
            if outcome["success_basis"] == "exit_code_and_result_record":
                expected = (outcome["exit_code"] == 0 and outcome["result_record_present"]
                            and outcome["is_error"] is False and outcome["result_subtype"] == "success")
            else:
                self.assertEqual(outcome["success_basis"], "exit_code_and_nonempty_stdout")
                expected = outcome["exit_code"] == 0 and outcome["stdout_bytes"] > 0
            self.assertEqual(outcome["success"], bool(expected), f"{sample_dir.name} success 与证据不一致")


class ProvenanceHonestyTest(unittest.TestCase):
    def setUp(self) -> None:
        self.manifest = load_json(MANIFEST_PATH)

    def test_kind_values_are_declared(self) -> None:
        for entry in self.manifest["samples"]:
            self.assertEqual(entry["kind"], "measured")
        for entry in self.manifest["synthetic_samples"]:
            self.assertEqual(entry["kind"], "synthetic")

    def test_synthetic_samples_declare_defect_and_origin(self) -> None:
        measured_ids = {e["sample_id"] for e in self.manifest["samples"]}
        for entry in self.manifest["synthetic_samples"]:
            meta = load_json(EVIDENCE_DIR / entry["dir"] / "meta.json")
            self.assertEqual(meta["kind"], "synthetic")
            self.assertTrue(meta["defect"], f"{entry['sample_id']} 缺 defect")
            self.assertIn(meta["derived_from"], measured_ids,
                          f"{entry['sample_id']} 的派生来源不是实测样本")
            self.assertIn("do_not_use_as", meta)
            self.assertIn("来源实测", meta["do_not_use_as"])
            self.assertTrue(meta["expected_interpretation"])

    def test_structural_counterexamples_add_no_values(self) -> None:
        for sample_id in ("VN301-X01", "VN301-X02"):
            meta = load_json(sample_by_id(sample_id) / "meta.json")
            self.assertEqual(meta["assumed_values"], [],
                             f"{sample_id} 只应做结构性破坏，不得引入新取值")

    def test_value_level_counterexamples_declare_assumptions(self) -> None:
        for sample_id in ("VN301-X03", "VN301-X04"):
            meta = load_json(sample_by_id(sample_id) / "meta.json")
            self.assertTrue(meta["assumed_values"], f"{sample_id} 含未实测取值，必须声明 assumed_values")

    def test_synthetic_not_listed_as_measured(self) -> None:
        measured_dirs = {e["dir"] for e in self.manifest["samples"]}
        for entry in self.manifest["synthetic_samples"]:
            self.assertNotIn(entry["dir"], measured_dirs)
            self.assertTrue(entry["dir"].startswith("samples/synthetic/"))


class RedactionTest(unittest.TestCase):
    def test_no_sensitive_patterns_in_samples(self) -> None:
        for path in sorted(SAMPLES_DIR.rglob("*")):
            if not path.is_file():
                continue
            text = path.read_text(encoding="utf-8", errors="replace")
            for pattern, label in SENSITIVE_PATTERNS:
                match = pattern.search(text)
                self.assertIsNone(match, f"{path.relative_to(EVIDENCE_DIR)} 残留{label}: {match.group(0) if match else ''}")

    def test_injected_context_is_redacted(self) -> None:
        sample_dir = sample_by_id("VN301-S04")
        _, records = records_of(sample_dir)
        user_messages = [r for r in records if r.get("type") == "message" and r.get("role") == "user"]
        self.assertTrue(user_messages, "json 模式应含 user 记录")
        parts = [c for c in user_messages[0]["content"] if isinstance(c, dict)]
        self.assertIn("<CLI_INJECTED_CONTEXT>", [c.get("text") for c in parts])
        for part in parts:
            text = part.get("text") or ""
            self.assertLess(len(text), 512, "长上下文未被脱敏")

    def test_manifest_declares_redaction_rules(self) -> None:
        rules = load_json(MANIFEST_PATH)["redaction_rules"]
        self.assertGreaterEqual(len(rules), 10)
        for rule in rules:
            self.assertRegex(rule["id"], r"^R\d{2}$")
            self.assertTrue(rule["detail"])


class ProtocolFactsTest(unittest.TestCase):
    """把文档里的断言钉在样本上，防止文档与证据脱节。"""

    def test_version_probe_stdout_is_semver(self) -> None:
        stdout = (sample_by_id("VN301-S01") / "stdout.txt").read_text(encoding="utf-8")
        self.assertRegex(stdout.strip(), SEMVER_RE)
        meta = load_json(sample_by_id("VN301-S01") / "meta.json")
        self.assertEqual(meta["outcome"]["exit_code"], 0)

    def test_stream_json_sequence_has_single_trailing_result(self) -> None:
        _, records = records_of(sample_by_id("VN301-S03"))
        self.assertEqual(records[0]["type"], "system")
        self.assertEqual(records[0]["subtype"], "init")
        results = [r for r in records if r["type"] == "result"]
        self.assertEqual(len(results), 1, "一次回合只应有一条 result 记录")
        self.assertEqual(records[-1]["type"], "result")
        self.assertEqual(results[0]["subtype"], "success")
        self.assertIs(results[0]["is_error"], False)
        self.assertEqual(results[0]["result"], "MAW-OK")

    def test_stream_json_carries_partial_events(self) -> None:
        _, records = records_of(sample_by_id("VN301-S03"))
        stream_events = [r for r in records if r["type"] == "stream_event"]
        self.assertGreater(len(stream_events), 5)
        self.assertTrue(all("event" in r for r in stream_events))

    def test_assistant_record_repeated_for_same_turn(self) -> None:
        _, records = records_of(sample_by_id("VN301-S03"))
        assistants = [r for r in records if r["type"] == "assistant"]
        self.assertEqual(len(assistants), 2, "实测同回合出现 thinking 与 text 两条 assistant 记录")
        kinds = [c["type"] for r in assistants for c in r["message"]["content"]]
        self.assertIn("thinking", kinds)
        self.assertIn("text", kinds)

    def test_auth_failure_exits_zero_but_is_error(self) -> None:
        meta = load_json(sample_by_id("VN301-S08") / "meta.json")
        outcome = meta["outcome"]
        self.assertEqual(outcome["exit_code"], 0, "实测：认证失败仍以退出码 0 结束")
        self.assertTrue(outcome["result_record_present"])
        self.assertIs(outcome["is_error"], True)
        self.assertEqual(outcome["result_subtype"], "error_during_execution")
        self.assertFalse(outcome["success"])
        self.assertEqual(outcome["error_class"], "authentication_required")

    def test_auth_error_text_leaks_into_assistant_content(self) -> None:
        _, records = records_of(sample_by_id("VN301-S08"))
        assistants = [r for r in records if r["type"] == "assistant"]
        self.assertTrue(assistants)
        text = assistants[0]["message"]["content"][0]["text"]
        self.assertTrue(text.startswith("Authentication required"),
                        "失败文本出现在 assistant 正文里，直接取正文会假 done")

    def test_port_conflict_fails_launch_with_exit_one(self) -> None:
        sample_dir = sample_by_id("VN301-S06")
        meta = load_json(sample_dir / "meta.json")
        self.assertEqual(meta["outcome"]["exit_code"], 1)
        self.assertEqual(meta["outcome"]["stdout_bytes"], 0)
        stderr = (sample_dir / "stderr.txt").read_text(encoding="utf-8")
        self.assertIn("EADDRINUSE", stderr)
        self.assertIn("<PORT>", stderr, "端口号应已脱敏")

    def test_invalid_output_format_is_not_rejected(self) -> None:
        bogus = sample_by_id("VN301-S07")
        text_mode = sample_by_id("VN301-S05")
        self.assertEqual(load_json(bogus / "meta.json")["outcome"]["exit_code"], 0)
        self.assertEqual((bogus / "stdout.txt").read_text(encoding="utf-8"),
                         (text_mode / "stdout.txt").read_text(encoding="utf-8"),
                         "非法枚举值静默回退为 text 模式")

    def test_json_mode_is_array_of_transcript_records(self) -> None:
        doc = load_json(sample_by_id("VN301-S04") / "records.json")
        self.assertIsInstance(doc, list)
        self.assertGreaterEqual(len(doc), 2)
        self.assertEqual(doc[-1]["type"], "result")
        self.assertEqual(doc[-1]["subtype"], "success")

    def test_usage_input_equals_cache_breakdown(self) -> None:
        for sample_id in ("VN301-S03", "VN301-S04"):
            _, records = records_of(sample_by_id(sample_id))
            result = [r for r in records if r["type"] == "result"][-1]
            usage = result["usage"]
            self.assertEqual(
                usage["input_tokens"],
                usage["cache_creation_input_tokens"] + usage["cache_read_input_tokens"],
                f"{sample_id}: input_tokens 已包含缓存分解，不能与 cache_* 相加",
            )

    def test_num_turns_is_not_user_turn_count(self) -> None:
        _, records = records_of(sample_by_id("VN301-S03"))
        result = [r for r in records if r["type"] == "result"][-1]
        self.assertEqual(result["num_turns"], 3, "单轮文本回复的 num_turns 实测为 3")
        self.assertNotEqual(result["num_turns"], 1)


class EventInventoryTest(unittest.TestCase):
    def setUp(self) -> None:
        self.inventory = load_json(INVENTORY_PATH)

    def test_inventory_is_version_bound(self) -> None:
        manifest = load_json(MANIFEST_PATH)
        self.assertEqual(self.inventory["cli_version"], manifest["environment"]["cli_version"])
        self.assertIn("measured:", self.inventory["provenance"])

    def test_every_inventory_field_exists_in_samples(self) -> None:
        for entry in self.inventory["records"]:
            fmt = entry["output_format"]
            found: Dict[str, str] = {}
            for sample_dir in sample_dirs():
                meta = load_json(sample_dir / "meta.json")
                if meta.get("kind") != "measured" or meta.get("output_format") != fmt:
                    continue
                _, records = records_of(sample_dir)
                for record in records:
                    if str(record.get("type")) != entry["type"]:
                        continue
                    if str(record.get("subtype")) != entry["subtype"]:
                        continue
                    found.update(fields_of(record))
            self.assertTrue(found, f"清单登记的 {fmt}/{entry['type']}/{entry['subtype']} 在样本中不存在")
            for path, declared in entry["fields"].items():
                self.assertIn(path, found, f"{fmt}/{entry['type']}/{entry['subtype']} 缺字段 {path}")
                self.assertEqual(found[path], declared["type"], f"{path} 类型与清单不符")

    def test_samples_have_no_unregistered_fields(self) -> None:
        registered = {
            (e["output_format"], e["type"], e["subtype"]): set(e["fields"])
            for e in self.inventory["records"]
        }
        for sample_dir in sample_dirs():
            meta = load_json(sample_dir / "meta.json")
            if meta.get("kind") != "measured":
                continue
            fmt = meta.get("output_format")
            _, records = records_of(sample_dir)
            for record in records:
                key = (fmt, str(record.get("type")), str(record.get("subtype")))
                self.assertIn(key, registered, f"{sample_dir.name} 出现未登记的记录类型 {key}")
                extra = set(fields_of(record)) - registered[key]
                self.assertFalse(extra, f"{sample_dir.name} 有未登记字段: {sorted(extra)}")


class CapabilityMatrixTest(unittest.TestCase):
    def setUp(self) -> None:
        self.matrix = load_json(MATRIX_PATH)
        self.manifest = load_json(MANIFEST_PATH)
        self.capabilities = {c["id"]: c for c in self.matrix["capabilities"]}

    def test_matrix_is_version_bound_to_measured_version(self) -> None:
        self.assertEqual(self.matrix["source"]["cli_version"], self.manifest["environment"]["cli_version"])
        self.assertIn(self.matrix["source"]["cli_version"], self.matrix["version_binding"]["valid_for_versions"])
        self.assertIn("cli_version_change", self.matrix["version_binding"]["invalidated_by"])

    def test_states_are_valid(self) -> None:
        for cap in self.matrix["capabilities"]:
            self.assertIn(cap["state"], STATE_VALUES, f"{cap['id']} 状态非法")
            self.assertTrue(cap["note"], f"{cap['id']} 缺说明")

    def test_observed_rows_point_to_existing_samples(self) -> None:
        measured_ids = {e["sample_id"] for e in self.manifest["samples"]}
        for cap in self.matrix["capabilities"]:
            if cap["state"] != "observed":
                continue
            self.assertTrue(cap["samples"], f"{cap['id']} 标为 observed 却无样本")
            for sample_id in cap["samples"]:
                self.assertIn(sample_id, measured_ids, f"{cap['id']} 引用了不存在的样本 {sample_id}")

    def test_non_observed_rows_have_no_samples(self) -> None:
        for cap in self.matrix["capabilities"]:
            if cap["state"] in ("unknown", "unobserved", "unsupported"):
                self.assertEqual(cap["samples"], [],
                                 f"{cap['id']} 标为 {cap['state']} 却挂样本，等于把未验证写成已验证")

    def test_related_samples_of_unverified_rows_exist(self) -> None:
        measured_ids = {e["sample_id"] for e in self.manifest["samples"]}
        for cap in self.matrix["capabilities"]:
            if cap["state"] == "observed":
                continue
            for sample_id in cap.get("observed_in", []):
                self.assertIn(sample_id, measured_ids,
                              f"{cap['id']} 的 observed_in 指向不存在的样本 {sample_id}")

    def test_unverified_capabilities_stay_unverified(self) -> None:
        for cap_id in ("C23", "C24", "C25", "C26", "C27"):
            self.assertNotEqual(self.capabilities[cap_id]["state"], "observed",
                                f"{cap_id} 未经实测，不得标为 observed")

    def test_gates_reference_real_samples(self) -> None:
        measured_ids = {e["sample_id"] for e in self.manifest["samples"]}
        for gate in ("G04", "G07"):
            entry = self.matrix["gates"][gate]
            self.assertIn("status", entry)
            self.assertTrue(entry["evidence"])
            for ref in entry["evidence"]:
                if ref.startswith("VN301-"):
                    self.assertIn(ref, measured_ids)


class SyntheticDefectTest(unittest.TestCase):
    def test_corrupted_line_is_invalid_json(self) -> None:
        path = sample_by_id("VN301-X01") / "events.jsonl"
        lines = [line for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
        with self.assertRaises(json.JSONDecodeError):
            json.loads(lines[-1])

    def test_missing_result_has_no_result_record(self) -> None:
        _, records = records_of(sample_by_id("VN301-X02"))
        self.assertTrue(records)
        self.assertFalse([r for r in records if r.get("type") == "result"])

    def test_success_without_content_is_degraded(self) -> None:
        doc = load_json(sample_by_id("VN301-X03") / "records.json")
        result = [r for r in doc if r.get("type") == "result"][-1]
        self.assertEqual(result["subtype"], "success")
        self.assertEqual(result["result"], "")
        self.assertEqual(result["usage"]["output_tokens"], 0)

    def test_unknown_event_and_version_are_present(self) -> None:
        _, records = records_of(sample_by_id("VN301-X04"))
        self.assertTrue([r for r in records if r.get("type") == "system" and r.get("subtype") == "hook"])
        result = [r for r in records if r.get("type") == "result"][-1]
        self.assertEqual(result.get("cli_version"), "9.9.9-future")
        self.assertIn("future_usage_field", result)


if __name__ == "__main__":
    unittest.main()
