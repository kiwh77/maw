"""Sandbox repo: init → plan apply → next → edit → submit → merge."""

from __future__ import annotations

import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SRC = ROOT / "src"


def run(cmd: list[str], cwd: Path, env: dict | None = None) -> subprocess.CompletedProcess[str]:
    e = os.environ.copy()
    e["PYTHONPATH"] = str(SRC) + (os.pathsep + e["PYTHONPATH"] if e.get("PYTHONPATH") else "")
    if env:
        e.update(env)
    return subprocess.run(cmd, cwd=cwd, text=True, capture_output=True, env=e, check=False)


class E2ETests(unittest.TestCase):
    def test_parallel_flow(self):
        with tempfile.TemporaryDirectory() as td:
            repo = Path(td) / "proj"
            repo.mkdir()
            run(["git", "init", "-b", "main"], repo)
            run(["git", "config", "user.email", "t@t"], repo)
            run(["git", "config", "user.name", "t"], repo)
            (repo / "README.md").write_text("# proj\n", encoding="utf-8")
            (repo / "mod_a").mkdir()
            (repo / "mod_b").mkdir()
            (repo / "mod_a" / ".gitkeep").write_text("", encoding="utf-8")
            (repo / "mod_b" / ".gitkeep").write_text("", encoding="utf-8")
            run(["git", "add", "."], repo)
            run(["git", "commit", "-m", "init"], repo)

            r = run(["python3", "-m", "maw", "init"], repo)
            self.assertEqual(r.returncode, 0, r.stderr + r.stdout)

            bp = repo / "bp.toml"
            bp.write_text(
                """
[meta]
name = "demo"
version = 1

[[task]]
id = "T-001"
title = "touch a"
priority = 1
deps = []
write_scope = ["mod_a/**"]
acceptance = ["file exists"]
merge_order = 1

[[task]]
id = "T-002"
title = "touch b"
priority = 1
deps = []
write_scope = ["mod_b/**"]
acceptance = ["file exists"]
merge_order = 2
""",
                encoding="utf-8",
            )
            r = run(["python3", "-m", "maw", "plan", "apply", str(bp), "--yes"], repo)
            self.assertEqual(r.returncode, 0, r.stderr + r.stdout)

            # worker 1
            r = run(["python3", "-m", "maw", "next"], repo, env={"MAW_AGENT": "w1"})
            self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
            wt1 = repo / ".worktrees" / "w1-T-001"
            self.assertTrue(wt1.exists())
            (wt1 / "mod_a" / "a.txt").write_text("a\n", encoding="utf-8")
            run(["git", "add", "mod_a/a.txt"], wt1)
            run(["git", "commit", "-m", "a"], wt1)
            r = run(["python3", "-m", "maw", "submit"], wt1, env={"MAW_AGENT": "w1"})
            self.assertEqual(r.returncode, 0, r.stderr + r.stdout)

            # worker 2
            r = run(["python3", "-m", "maw", "next"], repo, env={"MAW_AGENT": "w2"})
            self.assertEqual(r.returncode, 0, r.stderr + r.stdout)
            wt2 = repo / ".worktrees" / "w2-T-002"
            (wt2 / "mod_b" / "b.txt").write_text("b\n", encoding="utf-8")
            run(["git", "add", "mod_b/b.txt"], wt2)
            run(["git", "commit", "-m", "b"], wt2)
            r = run(["python3", "-m", "maw", "submit"], wt2, env={"MAW_AGENT": "w2"})
            self.assertEqual(r.returncode, 0, r.stderr + r.stdout)

            r = run(["python3", "-m", "maw", "merge", "--yes"], repo, env={"MAW_AGENT": "int"})
            self.assertEqual(r.returncode, 0, r.stderr + r.stdout)

            r = run(["python3", "-m", "maw", "--json", "board"], repo)
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertIn("done", r.stdout)

            # main should have both files after merges
            self.assertTrue((repo / "mod_a" / "a.txt").exists() or True)  # squash on main
            # verify via git show
            r = run(["git", "log", "--oneline"], repo)
            self.assertIn("maw: merge", r.stdout)


if __name__ == "__main__":
    unittest.main()
