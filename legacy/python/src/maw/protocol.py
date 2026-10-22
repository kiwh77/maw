"""AGENTS.md / CLAUDE.md / cursor rules protocol segments."""

from __future__ import annotations

import re
from pathlib import Path

from maw import PROTOCOL_VERSION

BEGIN = f"<!-- maw:begin v{PROTOCOL_VERSION} -->"
END = "<!-- maw:end -->"

PROTOCOL_BODY = f"""\
## 多 Agent 协议（由 maw 管理，请勿手改）
1. 开始工作前运行 `maw next` 领取任务，只在它创建的 worktree 中工作。
2. 只修改任务卡 write_scope 内的文件；需要越界时运行 `maw request-change`。
3. 里程碑运行 `maw progress`，完成运行 `maw submit`，无法继续运行 `maw block`。
4. 换人或中断前运行 `maw handoff`，交接包必须自包含。
5. 完成 submit 后可自行执行 `maw merge --yes` 合入；禁止直接用 git merge/rebase/push 主干。
6. 合入后若仍有 ready 任务，继续 `maw next` 领取并执行下一题，循环直至无题可领或用户叫停。
7. 出错时先读 maw 输出里的 Next 提示，不要绕过。
"""

SEGMENT = f"{BEGIN}\n{PROTOCOL_BODY}{END}\n"

_BLOCK_RE = re.compile(
    r"<!--\s*maw:begin\s+v(\d+)\s*-->.*?<!--\s*maw:end\s*-->",
    re.DOTALL,
)


def upsert_protocol(path: Path) -> str:
    """Insert or replace protocol block. Returns action: created|updated|unchanged."""
    path.parent.mkdir(parents=True, exist_ok=True)
    if not path.exists():
        path.write_text(SEGMENT, encoding="utf-8")
        return "created"
    text = path.read_text(encoding="utf-8")
    m = _BLOCK_RE.search(text)
    if not m:
        if text and not text.endswith("\n"):
            text += "\n"
        path.write_text(text + "\n" + SEGMENT, encoding="utf-8")
        return "updated"
    if m.group(0) == SEGMENT.strip() or BEGIN in m.group(0) and f"v{PROTOCOL_VERSION}" in m.group(0):
        new_text = _BLOCK_RE.sub(SEGMENT.rstrip(), text, count=1)
        if new_text != text:
            path.write_text(new_text if new_text.endswith("\n") else new_text + "\n", encoding="utf-8")
            return "updated"
        return "unchanged"
    new_text = _BLOCK_RE.sub(SEGMENT.rstrip(), text, count=1)
    path.write_text(new_text if new_text.endswith("\n") else new_text + "\n", encoding="utf-8")
    return "updated"


def read_protocol_version(path: Path) -> int | None:
    if not path.is_file():
        return None
    m = _BLOCK_RE.search(path.read_text(encoding="utf-8"))
    if not m:
        return None
    return int(m.group(1))


def install_protocol_files(repo: Path) -> dict[str, str]:
    results = {}
    results["AGENTS.md"] = upsert_protocol(repo / "AGENTS.md")
    results["CLAUDE.md"] = upsert_protocol(repo / "CLAUDE.md")
    cursor = repo / ".cursor" / "rules" / "maw.mdc"
    # mdc with frontmatter
    if not cursor.exists():
        cursor.parent.mkdir(parents=True, exist_ok=True)
        cursor.write_text(
            "---\ndescription: maw multi-agent protocol\nglobs:\nalwaysApply: true\n---\n\n"
            + SEGMENT,
            encoding="utf-8",
        )
        results[".cursor/rules/maw.mdc"] = "created"
    else:
        results[".cursor/rules/maw.mdc"] = upsert_protocol(cursor)
    return results
