"""本机 HTTP 契约测试（VN-005）。

强制契约正文的不变量登记表与 http_contract.INVARIANTS 对齐，
并用正反例样本验证机检语义：正例不得触发任何码，反例必须触发声明的码。
"""

import json
import re
import unittest
from pathlib import Path

import http_contract

DIR = Path(__file__).resolve().parent
CONTRACT = DIR / "http-api-contract-v1.md"
VALID = DIR / "samples" / "valid"
INVALID = DIR / "samples" / "invalid"
README = DIR / "README.md"


def _table_rows(contract_text):
    rows = {}
    section = contract_text.split("## 8. 不变量登记表", 1)[-1].split("## 9.", 1)[0]
    for line in section.splitlines():
        match = re.match(r"^\|\s*(H\d{3})\s*\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|", line)
        if match:
            rows[match.group(1)] = match.group(3).strip()
    return rows


class ContractDocumentTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = CONTRACT.read_text(encoding="utf-8")

    def test_root_identity(self):
        self.assertIn("`maw.http.api/v1`", self.text)
        self.assertIn("状态：**冻结**", self.text)

    def test_required_topics(self):
        for phrase in (
            "/api/v1",
            "202",
            "Idempotency-Key",
            "revision",
            "401",
            "409",
            "422",
            "旧入口",
            "CSRF",
            "CLI 令牌",
            "status-reports",
            "不承诺同用户进程之间的强隔离",
        ):
            with self.subTest(phrase=phrase):
                self.assertIn(phrase, self.text)

    def test_resources_and_actions_are_listed(self):
        for path in (
            "GET /api/v1/board",
            "GET /api/v1/projects/{project_id}/tasks",
            "GET /api/v1/tasks/{instance_id}",
            "GET /api/v1/events",
            "POST /api/v1/projects/{project_id}/claims",
            "POST /api/v1/tasks/{instance_id}/status-transitions",
            "POST /api/v1/tasks/{instance_id}/submissions",
            "POST /api/v1/tasks/{instance_id}/merges",
            "GET /api/v1/operations/{operation_id}",
        ):
            with self.subTest(path=path):
                self.assertIn(path, self.text)

    def test_pagination_limits_are_frozen(self):
        self.assertIn("`50`", self.text)
        self.assertIn("`1`–`200`", self.text)
        self.assertIn("`next_cursor`", self.text)
        self.assertIn("offset", self.text)

    def test_no_strong_isolation_is_stated(self):
        self.assertIn("不承诺同用户进程之间的强隔离", self.text)
        self.assertIn("loopback 只是部署约束，不是安全边界", self.text)

    def test_readme_exists(self):
        self.assertTrue(README.exists())


class InvariantAlignmentTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.rows = _table_rows(CONTRACT.read_text(encoding="utf-8"))

    def test_table_matches_code(self):
        self.assertEqual(set(self.rows), set(http_contract.INVARIANTS), "不变量码不一致")

    def test_descriptions_match(self):
        for code, text in http_contract.INVARIANTS.items():
            with self.subTest(code=code):
                self.assertEqual(self.rows.get(code), text)

    def test_every_code_has_counterexample(self):
        covered = set()
        for path in INVALID.glob("*.json"):
            doc = json.loads(path.read_text(encoding="utf-8"))
            for case in doc.get("cases", []):
                covered.update((case.get("expect") or {}).get("codes", []))
            covered.update((doc.get("expect") or {}).get("codes", []))
        self.assertEqual(set(http_contract.INVARIANTS) - covered, set(), "有码没有反例背书")


class SampleTest(unittest.TestCase):
    def test_valid_samples_trigger_nothing(self):
        files = sorted(VALID.glob("*.json"))
        self.assertTrue(files, "缺少正例样本")
        for path in files:
            with self.subTest(sample=path.name):
                doc = http_contract.load(path)
                self.assertEqual(
                    http_contract.validate(doc),
                    [],
                    f"{path.name} 触发了不应出现的码",
                )

    def test_invalid_samples_trigger_declared_codes(self):
        files = sorted(INVALID.glob("*.json"))
        self.assertTrue(files, "缺少反例样本")
        for path in files:
            with self.subTest(sample=path.name):
                doc = http_contract.load(path)
                found = http_contract.validate(doc)
                declared = set()
                for case in doc.get("cases", []):
                    expect = case.get("expect") or {}
                    declared.update(expect.get("codes") or expect.get("expect_error") or [])
                self.assertTrue(declared, f"{path.name} 未声明期望错误码")
                self.assertTrue(
                    declared <= set(found),
                    f"{path.name} 声明 {sorted(declared)}，实际 {found}",
                )


if __name__ == "__main__":
    unittest.main()
