import unittest

from maw.scope import path_in_scope, scopes_overlap


class ScopeTests(unittest.TestCase):
    def test_overlap_prefix(self):
        o = scopes_overlap(["src/orders/**"], ["src/orders/api.py"])
        self.assertTrue(o)

    def test_no_overlap(self):
        o = scopes_overlap(["src/a/**"], ["src/b/**"])
        self.assertEqual(o, [])

    def test_in_scope(self):
        self.assertTrue(path_in_scope("src/a/x.py", ["src/a/**"]))
        self.assertFalse(path_in_scope("src/b/x.py", ["src/a/**"]))


if __name__ == "__main__":
    unittest.main()
