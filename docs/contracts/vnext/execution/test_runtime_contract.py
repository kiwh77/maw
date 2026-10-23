"""VN-002 验证：契约正文、规范样本与校验器三者一致。

运行：python3 -m unittest discover -s docs/contracts/vnext/execution -p 'test_*.py'
只用标准库。
"""

from __future__ import annotations

import json
import re
import unittest
from pathlib import Path

import runtime_contract as rc

HERE = Path(__file__).resolve().parent
VALID_DIR = HERE / "samples" / "valid"
INVALID_DIR = HERE / "samples" / "invalid"
CONTRACT_DOC = HERE / "runtime-state-contract-v1.md"

TERMINAL_PHASES = {"completed", "failed", "cancelled", "timed_out"}


def _samples(folder: Path) -> list:
    return sorted(folder.glob("*.json"))


class SampleLayoutTest(unittest.TestCase):
    def test_directories_are_populated(self):
        self.assertGreaterEqual(len(_samples(VALID_DIR)), 5, "正例至少要覆盖五类现场")
        self.assertGreaterEqual(len(_samples(INVALID_DIR)), 20, "反例也要自成体系")

    def test_every_sample_is_json_object_with_contract_id(self):
        for path in _samples(VALID_DIR):
            with self.subTest(sample=path.name):
                doc = json.loads(path.read_text(encoding="utf-8"))
                self.assertIsInstance(doc, dict)
                self.assertEqual(doc.get("contract"), rc.CONTRACT_ID)
                self.assertTrue(doc.get("project_id"))
                self.assertIn("auto_execution", doc, "自动执行配置必须显式出现")

    def test_invalid_samples_keep_contract_namespace(self):
        """反例可以故意写错版本号（E201），但必须留在同一契约命名空间内。"""
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                doc = json.loads(path.read_text(encoding="utf-8"))
                self.assertIsInstance(doc, dict)
                self.assertTrue(str(doc.get("contract", "")).startswith("maw.runtime.state/"))
                self.assertTrue(doc.get("project_id"))
                self.assertIn("auto_execution", doc, "自动执行配置必须显式出现")


class ValidSampleTest(unittest.TestCase):
    def test_valid_samples_pass(self):
        for path in _samples(VALID_DIR):
            with self.subTest(sample=path.name):
                found = rc.validate(rc.load(path))
                self.assertEqual([str(v) for v in found], [], f"{path.name} 应通过校验")


class InvalidSampleTest(unittest.TestCase):
    def test_invalid_samples_raise_declared_code(self):
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                doc = rc.load(path)
                expected = doc.get("expect_error")
                self.assertIn(expected, rc.RULES, f"{path.name} 的错误码未登记")
                codes = [v.code for v in rc.validate(doc)]
                self.assertIn(expected, codes, f"{path.name} 未触发 {expected}，实际 {codes}")

    def test_invalid_samples_have_reason(self):
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                self.assertTrue(str(rc.load(path).get("note", "")).strip(), f"{path.name} 缺少反例说明")

    def test_every_rule_has_a_sample(self):
        """没有样本背书的不变量等于没写，防止判定表里塞死规则。"""
        exercised = set()
        for path in _samples(INVALID_DIR):
            exercised.update(v.code for v in rc.validate(rc.load(path)))
        missing = sorted(set(rc.RULES) - exercised)
        self.assertEqual(missing, [], f"以下不变量没有任何反例触发：{missing}")


class TransitionTableTest(unittest.TestCase):
    def _doc_transitions(self) -> dict:
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        block = re.search(r"```text\ntransitions\n(.*?)```", text, re.S)
        self.assertIsNotNone(block, "正文必须给出 transitions 代码块")
        table = {}
        for line in block.group(1).strip().splitlines():
            src, dst = line.split("->")
            targets = {t.strip() for t in dst.split(",") if t.strip()}
            table[src.strip()] = targets
        return table

    def test_doc_transitions_match_code(self):
        documented = self._doc_transitions()
        self.assertEqual(set(documented), set(rc.TRANSITIONS), "阶段集合与代码不一致")
        for phase, targets in rc.TRANSITIONS.items():
            with self.subTest(phase=phase):
                self.assertEqual(documented[phase], targets, f"{phase} 的去向与代码不一致")

    def test_terminal_phases_have_no_outgoing(self):
        documented = self._doc_transitions()
        for phase in TERMINAL_PHASES:
            with self.subTest(phase=phase):
                self.assertNotIn(phase, documented, f"{phase} 是终态，不得有出边")

    def test_no_transition_into_reserved(self):
        documented = self._doc_transitions()
        for phase, targets in documented.items():
            with self.subTest(phase=phase):
                self.assertNotIn("reserved", targets, "任何阶段都不得回到 reserved")


class DocCodeAlignmentTest(unittest.TestCase):
    def _documented_codes(self) -> set:
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        return set(re.findall(r"^\|\s*(E\d{3})\s*\|", text, re.MULTILINE))

    def test_doc_declares_same_contract_id(self):
        self.assertIn(rc.CONTRACT_ID, CONTRACT_DOC.read_text(encoding="utf-8"))

    def test_every_rule_is_documented(self):
        missing = sorted(set(rc.RULES) - self._documented_codes())
        self.assertEqual(missing, [], f"正文未登记的不变量：{missing}")

    def test_no_orphan_codes_in_doc(self):
        orphan = sorted(self._documented_codes() - set(rc.RULES))
        self.assertEqual(orphan, [], f"正文登记了代码里不存在的错误码：{orphan}")

    def test_doc_cites_upstream_contract(self):
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        self.assertIn("maw.task.instance/v1", text, "必须声明所依赖的下游契约")

    def test_doc_declares_verify_command(self):
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        self.assertIn("python3 -m unittest discover -s docs/contracts/vnext/execution", text)


if __name__ == "__main__":
    unittest.main()
