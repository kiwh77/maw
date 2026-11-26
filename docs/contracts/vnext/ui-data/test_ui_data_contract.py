"""页面上下文与挂载契约测试（VN-022）。

强制契约正文的不变量登记表与 ui_data_contract.INVARIANTS 对齐，
并用正反例样本验证机检语义：正例不得触发任何码，反例必须触发声明的码。
"""

import json
import re
import unittest
from pathlib import Path

import ui_data_contract

DIR = Path(__file__).resolve().parent
CONTRACT = DIR / "view-context-contract-v1.md"
VALID = DIR / "samples" / "valid"
INVALID = DIR / "samples" / "invalid"
README = DIR / "README.md"


def _table_rows(contract_text):
    rows = {}
    section = contract_text.split("## 7. 不变量登记表", 1)[-1].split("## 8.", 1)[0]
    for line in section.splitlines():
        match = re.match(r"^\|\s*(V\d{3})\s*\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|", line)
        if match:
            rows[match.group(1)] = match.group(3).strip()
    return rows


class ContractDocumentTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = CONTRACT.read_text(encoding="utf-8")

    def test_root_identity(self):
        self.assertIn("`maw.ui.data/v1`", self.text)
        self.assertIn("状态：**冻结**", self.text)

    def test_required_topics(self):
        for phrase in (
            "ViewContext",
            "frozen: true",
            "navigate(patch)",
            "RequestClient",
            "Idempotency-Key",
            "If-Match",
            "X-CSRF-Token",
            "credentials:",
            "same-origin",
            "view.<view_id>.<key>",
            "#/v/<view_id>",
            "replaceState",
            "pushState",
            "未登记视图",
        ):
            with self.subTest(phrase=phrase):
                self.assertIn(phrase, self.text)

    def test_readme_exists(self):
        self.assertTrue(README.exists())


class InvariantAlignmentTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.rows = _table_rows(CONTRACT.read_text(encoding="utf-8"))

    def test_table_matches_code(self):
        self.assertEqual(set(self.rows), set(ui_data_contract.INVARIANTS), "不变量码不一致")

    def test_descriptions_match(self):
        for code, text in ui_data_contract.INVARIANTS.items():
            with self.subTest(code=code):
                self.assertEqual(self.rows.get(code), text)

    def test_every_code_has_counterexample(self):
        covered = set()
        for path in INVALID.glob("*.json"):
            doc = json.loads(path.read_text(encoding="utf-8"))
            covered.update((doc.get("expect") or {}).get("codes", []))
        self.assertEqual(set(ui_data_contract.INVARIANTS) - covered, set(), "有码没有反例背书")


class SampleTest(unittest.TestCase):
    def test_valid_samples_trigger_nothing(self):
        files = sorted(VALID.glob("*.json"))
        self.assertTrue(files, "缺少正例样本")
        for path in files:
            with self.subTest(sample=path.name):
                doc = ui_data_contract.load(path)
                self.assertEqual(
                    ui_data_contract.validate(doc),
                    [],
                    f"{path.name} 触发了不应出现的码",
                )

    def test_invalid_samples_trigger_declared_codes(self):
        files = sorted(INVALID.glob("*.json"))
        self.assertTrue(files, "缺少反例样本")
        for path in files:
            with self.subTest(sample=path.name):
                doc = ui_data_contract.load(path)
                found = ui_data_contract.validate(doc)
                declared = set((doc.get("expect") or {}).get("codes", []))
                self.assertTrue(declared, f"{path.name} 未声明期望错误码")
                self.assertTrue(
                    declared <= set(found),
                    f"{path.name} 声明 {sorted(declared)}，实际 {found}",
                )


if __name__ == "__main__":
    unittest.main()
