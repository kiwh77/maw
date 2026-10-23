"""VN-004 验证：契约正文、规范样本与校验器三者一致。

运行：python3 -m unittest discover -s docs/contracts/vnext/ports -p 'test_*.py'
只用标准库。
"""

from __future__ import annotations

import json
import re
import unittest
from pathlib import Path

import ports_contract as pc

HERE = Path(__file__).resolve().parent
VALID_DIR = HERE / "samples" / "valid"
INVALID_DIR = HERE / "samples" / "invalid"
CONTRACT_DOC = HERE / "app-ports-contract-v1.md"


def _samples(folder: Path) -> list:
    return sorted(folder.glob("*.json"))


class SampleLayoutTest(unittest.TestCase):
    def test_directories_are_populated(self):
        self.assertGreaterEqual(len(_samples(VALID_DIR)), 6, "正例要覆盖接线、事务边界、多适配器")
        self.assertGreaterEqual(len(_samples(INVALID_DIR)), 18, "反例要覆盖每条不变量")

    def test_valid_samples_declare_contract_id(self):
        for path in _samples(VALID_DIR):
            with self.subTest(sample=path.name):
                doc = json.loads(path.read_text(encoding="utf-8"))
                self.assertEqual(doc.get("contract"), pc.CONTRACT_ID)
                self.assertTrue(doc.get("project_id"))
                self.assertTrue(doc.get("modules"))
                self.assertTrue(doc.get("bindings"))
                self.assertIsInstance(doc.get("calls"), list)

    def test_invalid_samples_keep_contract_namespace(self):
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                doc = json.loads(path.read_text(encoding="utf-8"))
                self.assertTrue(str(doc.get("contract", "")).startswith("maw.app.ports/"))
                self.assertTrue(doc.get("project_id"))


class ValidSampleTest(unittest.TestCase):
    def test_valid_samples_pass(self):
        for path in _samples(VALID_DIR):
            with self.subTest(sample=path.name):
                found = pc.validate(pc.load(path))
                self.assertEqual([str(v) for v in found], [], f"{path.name} 应通过校验")

    def test_valid_samples_bind_all_ports(self):
        for path in _samples(VALID_DIR):
            with self.subTest(sample=path.name):
                doc = pc.load(path)
                bound = {b.get("port") for b in doc.get("bindings", [])}
                self.assertEqual(bound, set(pc.PORTS), f"{path.name} 未绑全七个端口")


class InvalidSampleTest(unittest.TestCase):
    def test_invalid_samples_raise_declared_code(self):
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                doc = pc.load(path)
                expected = doc.get("expect_error")
                self.assertIn(expected, pc.RULES, f"{path.name} 的错误码未登记")
                codes = [v.code for v in pc.validate(doc)]
                self.assertIn(expected, codes, f"{path.name} 未触发 {expected}，实际 {codes}")

    def test_invalid_samples_have_reason(self):
        for path in _samples(INVALID_DIR):
            with self.subTest(sample=path.name):
                doc = pc.load(path)
                self.assertTrue(str(doc.get("note", "")).strip(), f"{path.name} 缺少反例说明")

    def test_every_rule_has_a_sample(self):
        covered = set()
        for path in _samples(INVALID_DIR):
            covered.update(v.code for v in pc.validate(pc.load(path)))
        missing = sorted(set(pc.RULES) - covered)
        self.assertEqual(missing, [], f"没有反例背书的不变量：{missing}")


class DocCodeAlignmentTest(unittest.TestCase):
    """正文登记的不变量必须与代码里的判定表一一对齐，防止文档漂移。"""

    def _documented_codes(self) -> set:
        self.assertTrue(CONTRACT_DOC.exists(), "缺少契约正文")
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        return set(re.findall(r"^\|\s*(P\d{3})\s*\|", text, re.MULTILINE))

    def test_doc_declares_contract_id(self):
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        self.assertIn(pc.CONTRACT_ID, text, "正文必须写出契约标识")

    def test_every_rule_is_documented(self):
        documented = self._documented_codes()
        missing = sorted(set(pc.RULES) - documented)
        self.assertEqual(missing, [], f"正文未登记的不变量：{missing}")

    def test_no_orphan_codes_in_doc(self):
        orphan = sorted(self._documented_codes() - set(pc.RULES))
        self.assertEqual(orphan, [], f"正文登记了代码里不存在的错误码：{orphan}")

    def test_every_rule_has_description(self):
        for code, desc in pc.RULES.items():
            with self.subTest(code=code):
                self.assertTrue(desc.strip(), f"{code} 缺少说明")

    def test_doc_declares_verify_command(self):
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        self.assertIn("python3 -m unittest discover -s docs/contracts/vnext/ports", text)


class PortTableAlignmentTest(unittest.TestCase):
    """正文的端口小节必须与 PORTS 表一一对齐，输入/输出/错误不许缺项。"""

    def _port_sections(self) -> dict:
        text = CONTRACT_DOC.read_text(encoding="utf-8")
        sections = {}
        for chunk in re.split(r"^### ", text, flags=re.MULTILINE):
            head = chunk.split("\n", 1)[0].strip()
            match = re.match(r"^(\d+\.\d+)\s+(\w+)", head)
            if match:
                sections[match.group(2)] = chunk
        return sections

    def test_doc_lists_same_ports(self):
        self.assertEqual(set(self._port_sections()), set(pc.PORTS))

    def test_doc_states_each_port_error(self):
        sections = self._port_sections()
        for port, spec in pc.PORTS.items():
            with self.subTest(port=port):
                self.assertIn(port, sections, f"正文缺端口小节 {port}")
                body = sections[port]
                for error in spec["errors"]:
                    self.assertIn(error, body, f"{port} 的错误 {error!r} 未在正文出现")

    def test_ports_declare_inputs_outputs_errors(self):
        for port, spec in pc.PORTS.items():
            with self.subTest(port=port):
                self.assertTrue(spec["inputs"], f"{port} 缺输入")
                self.assertTrue(spec["outputs"], f"{port} 缺输出")
                self.assertTrue(spec["errors"], f"{port} 缺错误")
                self.assertIn(spec["kind"], ("use_case", "transaction", "protocol", "process"))


if __name__ == "__main__":
    unittest.main()
