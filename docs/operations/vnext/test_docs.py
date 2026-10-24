"""doccheck 的四重自测。

1. 正例零违规：真实文档与 samples/valid 都必须干净。
2. 反例命中声明码：每个 samples/invalid 命中且只命中它声明的那一个码。
3. 每条规则都有反例：CODES 里的每个码都被至少一个样本覆盖。
4. 双向对齐：注册表与文档互为子集；平台声明与平台矩阵一致。

另外钉住两条「解析器不能悄悄失效」的断言：表格解析与注册表解析必须看到同样多的能力。
解析器静默返回空集合时，对齐检查会因为「两边都空」而通过——那是最危险的一种假绿。
"""

from __future__ import annotations

import unittest
from pathlib import Path

import doccheck

BASE = Path(__file__).resolve().parent

# 每个反例声明它要证明的那条规则；多的码出现说明规则之间耦合了，也要修。
SAMPLE_CASES: dict[str, tuple[str, set[str]]] = {
    "samples/invalid/01-guide-missing-section.md": ("guide", {"OPS001"}),
    "samples/invalid/02-guide-platform-overclaim.md": ("guide", {"OPS002"}),
    "samples/invalid/03-guide-unknown-level.md": ("guide", {"OPS003"}),
    "samples/invalid/04-guide-auto-on.md": ("guide", {"OPS004"}),
    "samples/invalid/05-upgrade-no-vacuum.md": ("upgrade", {"OPS005"}),
    "samples/invalid/06-upgrade-copy-db.md": ("upgrade", {"OPS006"}),
    "samples/invalid/07-guide-overclaim.md": ("guide", {"OPS007"}),
}


def codes_of(violations: list[doccheck.Violation]) -> set[str]:
    return {item.code for item in violations}


def read(relative: str) -> str:
    return (BASE / relative).read_text(encoding="utf-8")


class RealDocumentsTest(unittest.TestCase):
    """仓库里的真实文档必须零违规——这是本目录存在的意义。"""

    def test_guide_is_clean(self) -> None:
        self.assertEqual([], doccheck.check_document(read("operations-guide.zh-CN.md"), "guide"))

    def test_upgrade_is_clean(self) -> None:
        self.assertEqual([], doccheck.check_document(read("upgrade-notes.zh-CN.md"), "upgrade"))

    def test_registry_alignment_is_clean(self) -> None:
        violations = doccheck.check_registry_alignment(read("operations-guide.zh-CN.md"), doccheck.load_registry())
        self.assertEqual([], violations)

    def test_platform_alignment_is_clean(self) -> None:
        violations = doccheck.check_platform_alignment(
            read("operations-guide.zh-CN.md"), doccheck.load_platform_support()
        )
        self.assertEqual([], violations)

    def test_validate_all_is_clean(self) -> None:
        self.assertEqual([], doccheck.validate_all())


class ParsersSeeEverythingTest(unittest.TestCase):
    """解析器不能静默返回空集合，否则对齐检查会「两边都空」地通过。"""

    def test_guide_table_and_registry_agree(self) -> None:
        rows = doccheck._extension_rows(read("operations-guide.zh-CN.md"))
        registry = doccheck.load_registry()
        self.assertGreaterEqual(len(registry), 5, "注册表解析结果过少，解析规则可能已失效")
        self.assertEqual(len(registry), len(rows), "文档能力表与注册表行数不一致")
        self.assertIn("dispatch.model_invoke", registry, "不支持级能力也必须被解析到")

    def test_platform_matrix_has_three_systems(self) -> None:
        support = doccheck.load_platform_support()
        self.assertEqual({"darwin", "linux", "windows"}, set(support))

    def test_other_tables_are_not_read_as_capabilities(self) -> None:
        """原因码表的第一列也是反引号标识符，不能被当成能力。"""
        text = read("samples/valid/guide-min.md") + (
            "\n## 原因码\n\n"
            "| 原因码 | 意思 | 怎么办 |\n| --- | --- | --- |\n| `auto_disabled` | 关着 | 改配置 |\n"
        )
        rows = doccheck._extension_rows(text)
        self.assertEqual(["source.manual", "dispatch.auto"], [doccheck._row_id(row[0]) for row in rows])


class SamplesTest(unittest.TestCase):
    """正例干净、反例只命中声明码、每个码都有反例。"""

    def test_valid_samples_are_clean(self) -> None:
        self.assertEqual([], doccheck.check_document(read("samples/valid/guide-min.md"), "guide"))
        self.assertEqual([], doccheck.check_document(read("samples/valid/upgrade-min.md"), "upgrade"))

    def test_invalid_samples_hit_declared_code(self) -> None:
        for relative, (kind, expected) in SAMPLE_CASES.items():
            with self.subTest(sample=relative):
                violations = doccheck.check_document(read(relative), kind)
                self.assertEqual(
                    expected,
                    codes_of(violations),
                    f"{relative} 应只命中 {expected}，实际 {[(v.code, v.detail) for v in violations]}",
                )

    def test_every_code_has_a_counterexample(self) -> None:
        covered: set[str] = set()
        for _, expected in SAMPLE_CASES.values():
            covered |= expected
        self.assertEqual(set(doccheck.CODES), covered, "有规则没有反例样本，等于没验证过")


class RuleDetailTest(unittest.TestCase):
    """规则的判据本身也要有正反两面，否则改坏了没人知道。"""

    def test_registry_missing_capability_is_reported(self) -> None:
        guide = read("samples/valid/guide-min.md")
        registry = doccheck.load_registry()
        registry.pop("source.trae")
        codes = codes_of(doccheck.check_registry_alignment(guide, registry))
        self.assertIn("OPS003", codes)

    def test_registry_extra_capability_is_reported(self) -> None:
        guide = read("samples/valid/guide-min.md")
        registry = {key: value for key, value in doccheck.load_registry().items() if key in {"source.manual", "dispatch.auto"}}
        registry["source.brandnew"] = "ExtExperimental"
        details = " ".join(item.detail for item in doccheck.check_registry_alignment(guide, registry))
        self.assertIn("source.brandnew", details)

    def test_registry_level_drift_is_reported(self) -> None:
        guide = read("samples/valid/guide-min.md")
        registry = {key: value for key, value in doccheck.load_registry().items() if key in {"source.manual", "dispatch.auto"}}
        registry["source.manual"] = "ExtExperimental"
        codes = codes_of(doccheck.check_registry_alignment(guide, registry))
        self.assertIn("OPS003", codes)

    def test_platform_overclaim_is_reported(self) -> None:
        guide = read("samples/valid/guide-min.md")
        codes = codes_of(doccheck.check_platform_alignment(guide, {"darwin": True, "linux": True}))
        self.assertEqual({"OPS002"}, codes)

    def test_negated_warning_is_not_a_violation(self) -> None:
        """正文里「不要用 cp 备份」是在告诫，不能被判成违规。"""
        text = read("samples/valid/upgrade-min.md").replace(
            "用 `VACUUM INTO` 拿一致快照，不要拷活动库文件。",
            "不要用 `cp ~/.maw/maw.db` 备份，改用 `VACUUM INTO`。",
        )
        self.assertEqual([], doccheck.check_document(text, "upgrade"))

    def test_direct_sql_write_is_reported(self) -> None:
        text = read("samples/valid/upgrade-min.md").replace(
            "`maw init --upgrade`，失败会自动回滚。",
            "遇到卡住直接跑 sqlite3 改库：sqlite3 ~/.maw/maw.db \"UPDATE tasks SET status='done'\"。",
        )
        self.assertIn("OPS006", codes_of(doccheck.check_document(text, "upgrade")))

    def test_missing_contract_block_is_reported_for_guide(self) -> None:
        text = read("samples/valid/guide-min.md")
        start = text.index("<!-- ops-contract")
        end = text.index("-->", start) + len("-->")
        self.assertIn("OPS004", codes_of(doccheck.check_document(text[:start] + text[end:], "guide")))

    def test_contract_is_optional_for_upgrade(self) -> None:
        """升级说明不承诺自动执行默认值，所以缺块不算违规。"""
        self.assertEqual([], doccheck.check_document(read("samples/valid/upgrade-min.md"), "upgrade"))


if __name__ == "__main__":  # pragma: no cover
    unittest.main()
