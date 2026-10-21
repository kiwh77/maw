import unittest

from maw.errors import MawError
from maw.machine import assert_transition, can_transition


class MachineTests(unittest.TestCase):
    def test_happy_path(self):
        self.assertTrue(can_transition("ready", "in_progress"))
        self.assertTrue(can_transition("in_progress", "in_review"))
        self.assertTrue(can_transition("in_review", "merging"))
        self.assertTrue(can_transition("merging", "done"))

    def test_illegal(self):
        with self.assertRaises(MawError):
            assert_transition("done", "ready")


if __name__ == "__main__":
    unittest.main()
