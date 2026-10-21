import tempfile
import unittest
from pathlib import Path

from maw.blueprint import load_blueprint, validate_blueprint


class BlueprintTests(unittest.TestCase):
    def test_cycle(self):
        text = """
[[task]]
id = "A"
title = "a"
deps = ["B"]
write_scope = ["a/**"]

[[task]]
id = "B"
title = "b"
deps = ["A"]
write_scope = ["b/**"]
"""
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "bp.toml"
            p.write_text(text, encoding="utf-8")
            data = load_blueprint(p)
            errs = validate_blueprint(data)
            self.assertTrue(any("cycle" in e for e in errs))

    def test_overlap(self):
        text = """
[[task]]
id = "A"
title = "a"
write_scope = ["src/**"]

[[task]]
id = "B"
title = "b"
write_scope = ["src/foo/**"]
"""
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "bp.toml"
            p.write_text(text, encoding="utf-8")
            data = load_blueprint(p)
            errs = validate_blueprint(data)
            self.assertTrue(any("overlap" in e for e in errs))


if __name__ == "__main__":
    unittest.main()
