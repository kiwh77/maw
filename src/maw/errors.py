"""CLI errors with Next-step hints for LLM callers."""

from __future__ import annotations


class MawError(Exception):
    def __init__(self, message: str, *, next_hint: str | None = None, code: int = 1):
        super().__init__(message)
        self.message = message
        self.next_hint = next_hint
        self.code = code

    def format(self) -> str:
        lines = [f"Error: {self.message}"]
        if self.next_hint:
            lines.append(f"Next: {self.next_hint}")
        return "\n".join(lines)
