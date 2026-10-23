"""Workbench vnext UI 契约测试（VN-021）。

校验 WORKBENCH_UI_CONTRACT.md 冻结的 token、字级、间距与状态词汇，
并确认这些冻结值仍与 internal/board/page.html 的既有实现一致。
"""

import re
import unittest
from pathlib import Path

UI_DIR = Path(__file__).resolve().parent
REPO_ROOT = UI_DIR.parents[3]
CONTRACT = UI_DIR / "WORKBENCH_UI_CONTRACT.md"
PAGE = REPO_ROOT / "internal" / "board" / "page.html"

DARK_TOKENS = {
    "bg": "#141618",
    "panel": "#191c1f",
    "card": "#202427",
    "line": "#2d3338",
    "line-strong": "#414a51",
    "text": "#e6eaed",
    "muted": "#9ba5ad",
    "faint": "#7d8992",
    "accent": "#9cdbb0",
    "warn": "#e5bc78",
    "danger": "#ed9797",
    "ok": "#9cdbb0",
    "offline": "#68747c",
}

LIGHT_TOKENS = {
    "bg": "#f7f8f9",
    "panel": "#eef1f3",
    "card": "#ffffff",
    "line": "#dce1e5",
    "line-strong": "#c1cbd2",
    "text": "#20282f",
    "muted": "#5b6871",
    "faint": "#6b7882",
    "accent": "#267044",
    "warn": "#8a601f",
    "danger": "#b53e45",
    "ok": "#267044",
    "offline": "#89959d",
}

REQUIRED_SECTIONS = [
    "不变量",
    "颜色 token",
    "形状",
    "状态词汇",
    "颜色职责",
    "表格",
    "表单",
    "空态",
    "抽屉",
    "焦点与键盘",
    "动效与减弱动画",
    "中英双语",
    "窄屏",
    "验收与变更",
]


def _section(text, keyword):
    """返回以 keyword 所在的二级标题开始、到下一个二级标题为止的片段。"""
    pattern = re.compile(r"^## .*" + re.escape(keyword) + r".*$", re.M)
    match = pattern.search(text)
    if not match:
        return ""
    rest = text[match.end():]
    following = re.search(r"^## ", rest, re.M)
    return rest[: following.start()] if following else rest


def _css_block(text, selector):
    match = re.search(re.escape(selector) + r"\s*\{(.*?)\}", text, re.S)
    if not match:
        return ""
    return match.group(1)


def _hex_tokens(block):
    found = {}
    for name, value in re.findall(r"--([a-z-]+):\s*(#[0-9a-fA-F]{3,8})", block):
        found[name] = value.lower()
    return found


class ContractDocumentTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.contract = CONTRACT.read_text(encoding="utf-8")
        cls.page = PAGE.read_text(encoding="utf-8")

    def test_contract_has_all_sections(self):
        headings = re.findall(r"^## .+$", self.contract, re.M)
        for keyword in REQUIRED_SECTIONS:
            with self.subTest(section=keyword):
                self.assertTrue(
                    any(keyword in line for line in headings),
                    f"缺少小节：{keyword}",
                )

    def test_frozen_numbers_are_written_down(self):
        for literal in ("6px", "22px", "18px", "13px", "12px", "28px", "16px"):
            with self.subTest(literal=literal):
                self.assertIn(literal, self.contract)

    def test_no_new_page_skin_or_remote_assets(self):
        self.assertIn("不新设页面皮肤", self.contract)
        self.assertIn("不引入字体／图标 CDN", self.contract)

    def test_every_frozen_color_is_documented(self):
        for name, dark_hex in DARK_TOKENS.items():
            light_hex = LIGHT_TOKENS[name]
            with self.subTest(token=name):
                row = re.search(r"^\|\s*`--" + re.escape(name) + r"`\s*\|(.+)$", self.contract, re.M)
                self.assertIsNotNone(row, f"契约缺少 --{name} 行")
                cells = [cell.strip().strip("`").lower() for cell in row.group(1).split("|")]
                self.assertEqual(cells[0], dark_hex)
                self.assertEqual(cells[1], light_hex)

    def test_source_color_rule_is_explicit(self):
        section = _section(self.contract, "颜色职责")
        self.assertIn("不分配色相", section)
        self.assertIn("禁止为不同工程／应用生成专属色板或随机色", section)


class ContractStatusTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.contract = CONTRACT.read_text(encoding="utf-8")
        cls.page = PAGE.read_text(encoding="utf-8")

    def _contract_statuses(self):
        section = _section(self.contract, "状态词汇")
        rows = {}
        for line in section.splitlines():
            match = re.match(r"^\|\s*`([a-z_]+)`\s*\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|", line)
            if match:
                rows[match.group(1)] = (match.group(2).strip(), match.group(3).strip())
        return rows

    def _page_columns(self):
        block = re.search(r"const COLUMNS = \[(.*?)\];", self.page, re.S).group(1)
        return dict(re.findall(r'\["([a-z_]+)","([^"]+)"\]', block))

    def _page_lanes(self):
        block = re.search(r"const LANES = \[(.*?)\];", self.page, re.S).group(1)
        lanes = {}
        for lane_id, label, statuses in re.findall(
            r'id: "(\w+)",\s*label: "([^"]+)",\s*statuses: \[([^\]]+)\]', block
        ):
            lanes[lane_id] = (label, re.findall(r'"([a-z_]+)"', statuses))
        return lanes

    def test_status_vocabulary_matches_board(self):
        contract_rows = self._contract_statuses()
        page_columns = self._page_columns()
        self.assertEqual(set(contract_rows), set(page_columns), "契约与 Board 状态集不一致")
        self.assertEqual(len(contract_rows), 11)
        for status, (label, _lane) in contract_rows.items():
            with self.subTest(status=status):
                self.assertEqual(label, page_columns[status])

    def test_lane_mapping_matches_board(self):
        contract_rows = self._contract_statuses()
        lanes = self._page_lanes()
        self.assertEqual(len(lanes), 5)
        expected = {}
        for lane_id, (label, statuses) in lanes.items():
            for status in statuses:
                expected[status] = label
        for status, (_label, lane_label) in contract_rows.items():
            with self.subTest(status=status):
                self.assertEqual(lane_label, expected[status])

    def test_no_extra_or_missing_status(self):
        contract_rows = self._contract_statuses()
        self.assertEqual(
            contract_rows.keys() - self._page_columns().keys(),
            set(),
            "契约里出现了 Board 不认识的状态",
        )


class BoardImplementationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.page = PAGE.read_text(encoding="utf-8")
        cls.dark = _css_block(cls.page, ":root")
        cls.light = _css_block(cls.page, ':root[data-theme="light"]')

    def test_dark_palette_is_frozen(self):
        tokens = _hex_tokens(self.dark)
        for name, value in DARK_TOKENS.items():
            with self.subTest(token=name):
                self.assertEqual(tokens.get(name), value)

    def test_light_palette_is_frozen(self):
        tokens = _hex_tokens(self.light)
        for name, value in LIGHT_TOKENS.items():
            with self.subTest(token=name):
                self.assertEqual(tokens.get(name), value)

    def test_radius_type_scale_and_spacing_are_frozen(self):
        patterns = {
            "半径 6px": r"--radius:\s*6px",
            "正文 13px": r"body\s*\{[^}]*font:\s*13px",
            "标题 22px": r"h1\s*\{[^}]*font-size:\s*22px",
            "抽屉标题 18px": r"\.drawer-head h2 \{[^}]*font-size:\s*18px",
            "卡片标题 12px": r"\.card \.task-title \{[^}]*font-size:\s*12px",
            "页面边距 28px": r"\.content \{[^}]*padding:\s*18px 28px 0",
            "区块间距 16px": r"\.drawer-section \{[^}]*padding-top:\s*16px",
        }
        for label, pattern in patterns.items():
            with self.subTest(label=label):
                self.assertRegex(self.page, pattern)

    def test_focus_and_reduced_motion_rules_exist(self):
        self.assertRegex(self.page, r":focus-visible \{[^}]*outline:\s*2px solid var\(--accent\)")
        self.assertIn("prefers-reduced-motion: reduce", self.page)
        self.assertIn("transition: none !important", self.page)


if __name__ == "__main__":
    unittest.main()
