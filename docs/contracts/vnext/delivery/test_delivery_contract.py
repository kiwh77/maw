"""VN-003 验证：契约正文、规范样本与校验器三者一致。

运行：python3 -m unittest discover -s docs/contracts/vnext/delivery -p 'test_*.py'
只用标准库。
"""

from __future__ import annotations

import json
import re
import unittest
from pathlib import Path

import delivery_contract as dc

HERE = Path(__file__).resolve().parent
VALID_DIR = HERE / "samples" / "valid"
INVALID_DIR = HERE / "samples" / "invalid"
CONTRACT_DOC = HERE / "delivery-manifest-contract-v1.md"


def _samples(folder: Path) -> list:
    return sorted(folder.glob("*.json"))


class SampleLayoutTest(unittest.TestCase):
    def test_directories_are_populated(self):
        self.assertGreaterEqual(len(_samples(VALID_DIR)), 6, "正例要覆盖代码/非代码/重复交付")
        self.assertGreaterEqual(len(_samples(INVALID_DIR)), 20, "反例也要自成体系")

    def test_valid_samples_declare_contract_id(self):
        for path in _samples(VALID_DIR):
            with self.subTest(sample=path.name):
                doc = json.loads(path.read_text(encoding="utf-8"))
                self.assertEqual(doc.get("contract"), dc.CONTRACT_ID)
                self.assertTrue(doc.get("project_id"))
                self.assertTrue(doc.get("deliveries"))

    def test_invalid_samples_keep_contract_namespace(self):
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                doc = json.loads(path.read_text(encoding="utf-8"))
                self.assertTrue(str(doc.get("contract", "")).startswith("maw.delivery.manifest/"))
                self.assertTrue(doc.get("project_id"))


class ValidSampleTest(unittest.TestCase):
    def test_valid_samples_pass(self):
        for path in _samples(VALID_DIR):
            with self.subTest(sample=path.name):
                found = dc.validate(dc.load(path))
                self.assertEqual([str(v) for v in found], [], f"{path.name} 应通过校验")


class InvalidSampleTest(unittest.TestCase):
    def test_invalid_samples_raise_declared_code(self):
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                doc = dc.load(path)
                expected = doc.get("expect_error")
                self.assertIn(expected, dc.RULES, f"{path.name} 的错误码未登记")
                codes = [v.code for v in dc.validate(doc)]
                self.assertIn(expected, codes, f"{path.name} 未触发 {expected}，实际 {codes}")

    def test_invalid_samples_have_reason(self):
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                self.assertTrue(str(dc.load(path).get("note", "")).strip(), f"{path.name} 缺少反例说明")

    def test_every_rule_has_a_sample(self):
        exercised = set()
        for path in _samples(INVALID_DIR):
            exercised.update(v.code for v in dc.validate(dc.load(path)))
        missing = sorted(set(dc.RULES) - exercised)
        self.assertEqual(missing, [], f"以下不变量没有任何反例触发：{missing}")


class AcceptanceTest(unittest.TestCase):
    """直接对着本卡的验收条款做断言，避免规则漂移后验收悄悄不成立。"""

    def test_exit_code_alone_never_yields_done(self):
        path = VALID_DIR / "06-exit-code-only-pending.json"
        doc = dc.load(path)
        self.assertEqual(doc["deliveries"][0]["completion"]["decision"], "pending")
        self.assertEqual(dc.validate(doc), [])

    def test_missing_artifact_blocks_done(self):
        doc = dc.load(INVALID_DIR / "12-done-without-artifact.json")
        self.assertIn("E341", [v.code for v in dc.validate(doc)])

    def test_stale_commit_verification_blocks_done(self):
        doc = dc.load(INVALID_DIR / "07-stale-commit-verified.json")
        self.assertIn("E331", [v.code for v in dc.validate(doc)])

    def test_manifest_carries_version_run_and_commits(self):
        doc = dc.load(VALID_DIR / "01-code-delivery-done.json")
        dlv = doc["deliveries"][0]
        for key in ("task_instance", "task_version", "run_id", "baseline_commit", "candidate_commit"):
            with self.subTest(field=key):
                self.assertTrue(dlv.get(key), f"Manifest 缺 {key}")

    def test_code_and_non_code_completion_differ(self):
        code = dc.load(VALID_DIR / "01-code-delivery-done.json")["deliveries"][0]
        non_code = dc.load(VALID_DIR / "02-non-code-manual-acceptance.json")["deliveries"][0]
        self.assertTrue(code["merge"]["merged"], "代码任务完成需要合入")
        self.assertFalse(non_code["merge"]["merged"], "非代码任务不走合入")
        self.assertEqual(non_code["verification"][0]["result"], "manual", "非代码任务靠人工验收")


class DocCodeAlignmentTest(unittest.TestCase):
    def _documented_codes(self) -> set:
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        return set(re.findall(r"^\|\s*(E\d{3})\s*\|", text, re.MULTILINE))

    def test_doc_declares_same_contract_id(self):
        self.assertIn(dc.CONTRACT_ID, CONTRACT_DOC.read_text(encoding="utf-8"))

    def test_every_rule_is_documented(self):
        missing = sorted(set(dc.RULES) - self._documented_codes())
        self.assertEqual(missing, [], f"正文未登记的不变量：{missing}")

    def test_no_orphan_codes_in_doc(self):
        orphan = sorted(self._documented_codes() - set(dc.RULES))
        self.assertEqual(orphan, [], f"正文登记了代码里不存在的错误码：{orphan}")

    def test_doc_cites_upstream_contracts(self):
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        self.assertIn("maw.task.instance/v1", text)
        self.assertIn("maw.runtime.state/v1", text)

    def test_doc_declares_verify_command(self):
        self.assertIn(
            "python3 -m unittest discover -s docs/contracts/vnext/delivery",
            CONTRACT_DOC.read_text(encoding="utf-8"),
        )


if __name__ == "__main__":
    unittest.main()
