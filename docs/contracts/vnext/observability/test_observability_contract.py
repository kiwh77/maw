"""时间轴与计量契约测试（VN-006）。

强制正文 §7 的不变量登记表与 observability_contract.INVARIANTS 对齐，
并用快照样本验证：正例零违规，反例命中声明的码。
"""

import json
import re
import unittest
from pathlib import Path

import observability_contract

DIR = Path(__file__).resolve().parent
CONTRACT = DIR / "timeline-metering-contract-v1.md"
VALID = DIR / "samples" / "valid"
INVALID = DIR / "samples" / "invalid"
README = DIR / "README.md"

INVALID_EXPECTATIONS = {
    "01-duplicate-id.json": {"T110"},
    "02-bad-kind.json": {"T111"},
    "03-negative-duration.json": {"T112"},
    "04-overlap.json": {"T113"},
    "05-watermark-inverted.json": {"T120"},
    "06-gap-overlap.json": {"T120"},
    "07-estimated-no-basis.json": {"T121"},
    "08-day-splits-mismatch.json": {"T130"},
    "09-total-mismatch.json": {"T140"},
    "10-duplicate-turn.json": {"T110", "T143"},
    "11-unknown-token-zero.json": {"T142"},
    "12-shared-turn-double.json": {"T143", "T145"},
    "13-reset-epoch.json": {"T141"},
    "14-late-unmarked.json": {"T144"},
    "15-missing-field.json": {"T100"},
    "16-wrong-contract.json": {"T101"},
    "17-bad-phase.json": {"T114"},
}


def _rows(text):
    rows = {}
    section = text.split("## 7. 不变量登记表", 1)[-1].split("## 8.", 1)[0]
    for line in section.splitlines():
        match = re.match(r"^\|\s*(T\d{3})\s*\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|", line)
        if match:
            rows[match.group(1)] = match.group(3).strip()
    return rows


class ContractDocumentTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = CONTRACT.read_text(encoding="utf-8")

    def test_root_identity(self):
        self.assertIn("`maw.observability.timeline/v1`", self.text)
        self.assertIn("状态：**冻结**", self.text)

    def test_required_topics(self):
        for phrase in (
            "`task_phase`",
            "`cli_process`",
            "`turn`",
            "`explicit_wait`",
            "watermark",
            "completeness",
            "跨日",
            "乱序",
            "累计",
            "共享轮次",
            "未知",
            "并集",
            "去重",
        ):
            with self.subTest(phrase=phrase):
                self.assertIn(phrase, self.text)

    def test_double_counting_is_forbidden(self):
        self.assertIn("不得重复计量", self.text)
        self.assertIn("按轮次去重", self.text)
        self.assertIn("并集", self.text)

    def test_no_strong_isolation_note(self):
        self.assertIn("不实现采集器", self.text)

    def test_readme_exists(self):
        self.assertTrue(README.exists())


class InvariantAlignmentTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.rows = _rows(CONTRACT.read_text(encoding="utf-8"))

    def test_table_matches_code(self):
        self.assertEqual(set(self.rows), set(observability_contract.INVARIANTS), "不变量码不一致")

    def test_descriptions_match(self):
        for code, text in observability_contract.INVARIANTS.items():
            with self.subTest(code=code):
                self.assertEqual(self.rows.get(code), text)

    def test_every_code_has_counterexample(self):
        covered = set().union(*INVALID_EXPECTATIONS.values())
        self.assertEqual(set(observability_contract.INVARIANTS) - covered, set(), "有码没有反例背书")

    def test_invalid_samples_are_registered(self):
        files = {path.name for path in INVALID.glob("*.json")}
        self.assertEqual(files, set(INVALID_EXPECTATIONS), "反例文件与期望表不一致")


class SampleTest(unittest.TestCase):
    def test_valid_samples_trigger_nothing(self):
        files = sorted(VALID.glob("*.json"))
        self.assertEqual(len(files), 6)
        for path in files:
            with self.subTest(sample=path.name):
                doc = json.loads(path.read_text(encoding="utf-8"))
                self.assertEqual(observability_contract.validate(doc), [], f"{path.name} 触发了不应出现的码")

    def test_invalid_samples_trigger_declared_codes(self):
        for name, declared in INVALID_EXPECTATIONS.items():
            with self.subTest(sample=name):
                doc = json.loads((INVALID / name).read_text(encoding="utf-8"))
                found = set(observability_contract.validate(doc))
                self.assertTrue(declared <= found, f"{name} 声明 {sorted(declared)}，实际 {sorted(found)}")


if __name__ == "__main__":
    unittest.main()
