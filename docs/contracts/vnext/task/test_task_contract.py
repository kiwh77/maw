"""VN-001 验证：契约正文、规范样本与校验器三者一致。

运行：python3 -m unittest discover -s docs/contracts/vnext/task -p 'test_*.py'
只用标准库。
"""

from __future__ import annotations

import json
import re
import unittest
from pathlib import Path

import task_contract as tc

HERE = Path(__file__).resolve().parent
VALID_DIR = HERE / "samples" / "valid"
INVALID_DIR = HERE / "samples" / "invalid"
CONTRACT_DOC = HERE / "task-instance-contract-v1.md"


def _samples(folder: Path) -> list:
    return sorted(folder.glob("*.json"))


class SampleLayoutTest(unittest.TestCase):
    def test_directories_are_populated(self):
        self.assertTrue(_samples(VALID_DIR), "至少要有规范正例")
        self.assertGreaterEqual(len(_samples(INVALID_DIR)), 10, "反例覆盖面不足")

    def test_every_sample_is_json_object_with_contract_id(self):
        for path in _samples(VALID_DIR) + _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                doc = json.loads(path.read_text(encoding="utf-8"))
                self.assertIsInstance(doc, dict)
                self.assertEqual(doc.get("contract"), tc.CONTRACT_ID)
                self.assertTrue(doc.get("project_id"))


class ValidSampleTest(unittest.TestCase):
    def test_valid_samples_pass(self):
        for path in _samples(VALID_DIR):
            with self.subTest(sample=path.name):
                found = tc.validate(tc.load(path))
                self.assertEqual(
                    [str(v) for v in found], [], f"{path.name} 应通过校验"
                )


class InvalidSampleTest(unittest.TestCase):
    def test_invalid_samples_raise_declared_code(self):
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                doc = tc.load(path)
                expected = doc.get("expect_error")
                self.assertIsNotNone(expected, f"{path.name} 必须声明 expect_error")
                self.assertIn(expected, tc.RULES, f"{path.name} 的错误码未登记")
                codes = [v.code for v in tc.validate(doc)]
                self.assertIn(expected, codes, f"{path.name} 未触发 {expected}，实际 {codes}")

    def test_invalid_samples_have_reason(self):
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                doc = tc.load(path)
                self.assertTrue(str(doc.get("note", "")).strip(), f"{path.name} 缺少反例说明")


class DocCodeAlignmentTest(unittest.TestCase):
    """正文登记的不变量必须与代码里的判定表一一对齐，防止文档漂移。"""

    def _documented_codes(self) -> set:
        self.assertTrue(CONTRACT_DOC.exists(), "缺少契约正文")
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        return set(re.findall(r"^\|\s*(E\d{3})\s*\|", text, re.MULTILINE))

    def test_doc_declares_same_contract_id(self):
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        self.assertIn(tc.CONTRACT_ID, text, "正文必须写出契约标识")

    def test_every_rule_is_documented(self):
        documented = self._documented_codes()
        missing = sorted(set(tc.RULES) - documented)
        self.assertEqual(missing, [], f"正文未登记的不变量：{missing}")

    def test_no_orphan_codes_in_doc(self):
        orphan = sorted(self._documented_codes() - set(tc.RULES))
        self.assertEqual(orphan, [], f"正文登记了代码里不存在的错误码：{orphan}")

    def test_every_rule_has_description(self):
        for code, desc in tc.RULES.items():
            with self.subTest(code=code):
                self.assertTrue(desc.strip(), f"{code} 缺少说明")

    def test_doc_declares_verify_command(self):
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        self.assertIn("python3 -m unittest discover -s docs/contracts/vnext/task", text)


if __name__ == "__main__":
    unittest.main()
