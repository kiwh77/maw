"""vNext 运维正文的机器校验器（纯标准库，无第三方依赖）。

为什么要有它：这两份文档最容易出的错不是写错字，而是**悄悄与代码不一致**——
注册表加了一个来源但文档没写、平台白名单改宽了但文档还写着「仅 macOS」、
升级步骤里把「VACUUM INTO 一致备份」换成了 `cp maw.db`。这些错读者看不出来，
但会在真出事那天变成事故扩音器。所以把「文档说了什么」变成可执行断言。

用法：

    python3 docs/operations/vnext/doccheck.py          # 校验真实文档
    python3 -m unittest discover -s docs/operations/vnext -p 'test_*.py'

规则码（封闭集合，新增规则必须同时补反例样本）：

    OPS001 缺必需章节
    OPS002 平台声明与平台矩阵不一致
    OPS003 能力级别与扩展注册表不一致
    OPS004 自动执行默认值声明错误
    OPS005 升级说明缺关键要素
    OPS006 出现危险的手工库操作
    OPS007 越界承诺（把「可运行」说成「全部能力通过」）
"""

from __future__ import annotations

import re
import sys
from dataclasses import dataclass
from pathlib import Path

BASE = Path(__file__).resolve().parent
REPO = BASE.parents[2]

GUIDE_PATH = BASE / "operations-guide.zh-CN.md"
UPGRADE_PATH = BASE / "upgrade-notes.zh-CN.md"
REGISTRY_PATH = REPO / "internal/app/runtime.go"
PLATFORM_MATRIX_PATH = REPO / "docs/verification/vnext/release/platform-matrix.zh-CN.md"

# 规则码与判据说明。测试会断言每个码都有反例样本。
CODES: dict[str, str] = {
    "OPS001": "缺必需章节：读者按标题找步骤，缺一节就是断一条路",
    "OPS002": "平台声明与平台矩阵不一致：受管支持范围不能有两处说法",
    "OPS003": "能力级别与扩展注册表不一致：文档与代码必须双向对齐",
    "OPS004": "自动执行默认值声明错误：默认关闭是安全底线，不能被写成开启或省略",
    "OPS005": "升级说明缺关键要素：停写／一致备份／恢复／unknown／降级任一缺失都不可执行",
    "OPS006": "出现危险的手工库操作：拷贝活动库文件或直改线上库都会制造静默损坏",
    "OPS007": "越界承诺：把「某个客户端能运行」说成「全部能力通过」",
}

# 章节清单。改标题等于改契约：读者与校验器都按标题定位。
GUIDE_SECTIONS = (
    "适用范围",
    "运行方式",
    "平台与来源支持分级",
    "日常操作",
    "首次接入",
    "排障",
    "安全边界",
)

UPGRADE_SECTIONS = (
    "版本兼容",
    "停旧写端",
    "一致备份",
    "升级步骤",
    "故障恢复",
    "unknown 处置",
    "回退与降级",
)

# 升级说明里每一步的「不可替代的一句话」。关键词必须出现在对应章节内。
UPGRADE_KEYWORDS: dict[str, tuple[str, ...]] = {
    "停旧写端": ("min_writer_version",),
    "一致备份": ("VACUUM INTO",),
    "故障恢复": ("-wal",),
    "unknown 处置": ("E232", "证据"),
    "回退与降级": ("ErrSchemaAhead",),
}

# 级别词汇表：左＝文档写法，右＝注册表常量。
LEVEL_WORDS = {"ExtVerified": "已验证", "ExtExperimental": "实验", "ExtUnsupported": "不支持"}
DOC_LEVELS = tuple(LEVEL_WORDS.values())

# 越界承诺的固定说法。允许的写法是「X 已验证、Y 不支持」，不是「全都通过」。
BANNED_PHRASES = (
    "全部能力已通过",
    "全部能力均已通过",
    "所有平台均已验证",
    "可以降级写",
    "直接降级写新库是安全的",
)

# 危险手工操作：拷活动库、直改线上库、粗暴删库目录。
DANGEROUS_PATTERNS: tuple[tuple[str, str], ...] = (
    (r"cp\s+[^\n]*maw\.db(?![.\w-])", "用 cp 拷贝活动库会丢掉未 checkpoint 的 WAL 帧"),
    (r"sqlite3\s+[^\n]*(?:INSERT|UPDATE|DELETE|ALTER|DROP)\b", "直改线上库绕过了写者闸门与引用一致性"),
    (r"rm\s+-rf\s+[^\n]*\.maw\b", "整目录删除会连备份一起删掉"),
)

CONTRACT_START = "<!-- ops-contract"
CONTRACT_END = "-->"


@dataclass(frozen=True)
class Violation:
    """一条违规：码 + 位置 + 具体差异。"""

    code: str
    where: str
    detail: str

    def __str__(self) -> str:  # pragma: no cover - 仅用于命令行可读输出
        return f"{self.code} {self.where}: {self.detail}"


def _sections(text: str) -> dict[str, str]:
    """把正文按二级标题切成 {标题: 正文}，标题去掉 `## ` 与首尾空白。"""
    out: dict[str, str] = {}
    current = ""
    buf: list[str] = []
    for line in text.splitlines():
        if line.startswith("## "):
            if current:
                out[current] = "\n".join(buf)
            current = line[3:].strip()
            buf = []
        elif current:
            buf.append(line)
    if current:
        out[current] = "\n".join(buf)
    return out


def _contract(text: str) -> dict[str, str]:
    """解析 ops-contract 声明块；没有块时返回空 dict。"""
    start = text.find(CONTRACT_START)
    if start < 0:
        return {}
    end = text.find(CONTRACT_END, start)
    if end < 0:
        return {}
    body = text[start + len(CONTRACT_START) : end]
    out: dict[str, str] = {}
    for line in body.splitlines():
        if ":" not in line:
            continue
        key, _, value = line.partition(":")
        out[key.strip()] = value.strip()
    return out


def _extension_rows(text: str) -> list[list[str]]:
    """抓取能力表的数据行。

    只认「表头同时含『能力』与『级别』」的那张表：正文里还有原因码表、排障表，
    它们的第一列同样用反引号包着标识符，按行扫会把 `auto_disabled` 当成能力 ID。
    """
    rows: list[list[str]] = []
    in_table = False
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped.startswith("|"):
            if in_table:
                break
            continue
        cells = [cell.strip() for cell in stripped.strip("|").split("|")]
        if not in_table:
            if "能力" in cells[0] and "级别" in cells:
                in_table = True
            continue
        if len(cells) < 5 or set(stripped) <= set("|-: "):
            continue
        rows.append(cells)
    return rows


def _row_id(cell: str) -> str:
    match = re.match(r"`([^`]+)`", cell)
    return match.group(1) if match else ""


def check_document(text: str, kind: str) -> list[Violation]:
    """按文档类型跑「单篇自洽」规则：OPS001/004/005/006/007，以及表的格式与词汇。"""
    where = "guide" if kind == "guide" else "upgrade"
    out: list[Violation] = []

    required = GUIDE_SECTIONS if kind == "guide" else UPGRADE_SECTIONS
    sections = _sections(text)
    missing = [name for name in required if name not in sections]
    if missing:
        out.append(Violation("OPS001", where, "缺章节：" + "、".join(missing)))

    contract = _contract(text)
    if not contract:
        # 自动执行默认值是操作指南的核心承诺，必须有一处机器可读的声明；
        # 升级说明没有这份承诺，缺块不算违规。
        if kind == "guide":
            out.append(Violation("OPS004", where, "缺 ops-contract 声明块，自动执行默认值无处可核"))
    else:
        automatic = contract.get("automatic_default", "")
        if automatic != "off":
            out.append(
                Violation("OPS004", where, f"automatic_default 必须是 off，实际 {automatic or '未声明'}")
            )
        scope = contract.get("codebuddy_scope", "")
        if scope != "manual_loop_only":
            out.append(
                Violation("OPS007", where, f"codebuddy_scope 必须是 manual_loop_only，实际 {scope or '未声明'}")
            )
        platforms = {p.strip() for p in contract.get("managed_platforms", "").split(",") if p.strip()}
        if platforms != {"darwin"}:
            out.append(
                Violation("OPS002", where, f"managed_platforms 只能是 darwin，实际 {sorted(platforms) or '未声明'}")
            )

    if kind == "guide":
        rows = _extension_rows(text)
        if not rows:
            out.append(Violation("OPS003", where, "缺能力表：来源与级别必须逐条列出"))
        for cells in rows:
            level = cells[2]
            if level not in DOC_LEVELS:
                out.append(
                    Violation("OPS003", where, f"能力 {_row_id(cells[0]) or cells[0]} 的级别 {level} 不在词汇表内")
                )
            if _row_id(cells[0]) == "dispatch.auto" and "darwin" not in cells[3]:
                out.append(Violation("OPS002", where, "dispatch.auto 的启用条件必须写清仅 darwin"))
    else:
        for section, keywords in UPGRADE_KEYWORDS.items():
            body = sections.get(section, "")
            for keyword in keywords:
                if keyword not in body:
                    out.append(Violation("OPS005", where, f"「{section}」缺少关键要素 {keyword}"))

    for pattern, reason in DANGEROUS_PATTERNS:
        for match in re.finditer(pattern, text):
            line = text[text.rfind("\n", 0, match.start()) + 1 : text.find("\n", match.end())]
            # 正文里「不要用 cp 备份」这类禁令本身就是正确写法，命中的是它举的反例；
            # 同一行出现了否定词就当作在告诫，不判违规。
            if any(neg in line for neg in ("不要", "不能", "禁止", "别用", "不许")):
                continue
            out.append(Violation("OPS006", where, f"{reason}：{match.group(0).strip()[:60]}"))
            break

    for phrase in BANNED_PHRASES:
        if phrase in text:
            out.append(Violation("OPS007", where, f"出现越界承诺：{phrase}"))

    return out


def check_registry_alignment(text: str, registry: dict[str, str]) -> list[Violation]:
    """双向对齐：注册表里的能力都要在文档里，文档里的能力都要在注册表里，级别一致。"""
    out: list[Violation] = []
    documented: dict[str, str] = {}
    for cells in _extension_rows(text):
        ext_id = _row_id(cells[0])
        if ext_id:
            documented[ext_id] = cells[2]

    for ext_id, doc_level in sorted(documented.items()):
        if ext_id not in registry:
            out.append(Violation("OPS003", "guide", f"文档登记了注册表里没有的能力 {ext_id}"))
            continue
        expected = LEVEL_WORDS.get(registry[ext_id], registry[ext_id])
        if doc_level != expected:
            out.append(
                Violation("OPS003", "guide", f"能力 {ext_id} 级别写成 {doc_level}，注册表是 {expected}")
            )

    for ext_id, level in sorted(registry.items()):
        if ext_id not in documented:
            out.append(
                Violation("OPS003", "guide", f"注册表能力 {ext_id}（{LEVEL_WORDS.get(level, level)}）未在文档登记")
            )
    return out


def check_platform_alignment(text: str, support: dict[str, bool]) -> list[Violation]:
    """受管平台声明必须与平台矩阵文档一致：两边都只允许 darwin。"""
    out: list[Violation] = []
    contract = _contract(text)
    declared = {p.strip() for p in contract.get("managed_platforms", "").split(",") if p.strip()}
    evidenced = {os_name for os_name, supported in support.items() if supported}
    if declared != evidenced:
        out.append(
            Violation(
                "OPS002",
                "guide",
                f"文档声明受管平台 {sorted(declared)}，平台矩阵实测支持 {sorted(evidenced)}",
            )
        )
    for os_name, supported in sorted(support.items()):
        if supported and os_name != "darwin":
            out.append(Violation("OPS002", "guide", f"平台矩阵把 {os_name} 标成受管支持，但只有 darwin 有原生证据"))
    return out


def load_registry(path: Path = REGISTRY_PATH) -> dict[str, str]:
    """从 internal/app/runtime.go 读扩展注册表：{能力 ID: Ext 级别常量}。"""
    text = path.read_text(encoding="utf-8")
    out: dict[str, str] = {}
    for match in re.finditer(r'ID:\s*"([^"]+)"', text):
        ext_id = match.group(1)
        window = text[match.end() : match.end() + 400]
        level = re.search(r"Level:\s*(Ext[A-Za-z]+)", window)
        if level:
            out[ext_id] = level.group(1)
    return out


def load_platform_support(path: Path = PLATFORM_MATRIX_PATH) -> dict[str, bool]:
    """从平台矩阵文档读「受管执行」列：{系统: 是否声称支持}。同一系统多架构取并集。"""
    text = path.read_text(encoding="utf-8")
    out: dict[str, bool] = {}
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped.startswith("|"):
            continue
        cells = [cell.strip() for cell in stripped.strip("|").split("|")]
        if len(cells) < 4:
            continue
        os_name = cells[0]
        if os_name not in {"darwin", "linux", "windows"}:
            continue
        out[os_name] = out.get(os_name, False) or cells[3] == "支持"
    return out


def validate_all() -> list[Violation]:
    """校验仓库里的真实文档与真实代码是否一致。"""
    violations: list[Violation] = []
    guide = GUIDE_PATH.read_text(encoding="utf-8")
    upgrade = UPGRADE_PATH.read_text(encoding="utf-8")

    violations += check_document(guide, "guide")
    violations += check_document(upgrade, "upgrade")
    violations += check_registry_alignment(guide, load_registry())
    violations += check_platform_alignment(guide, load_platform_support())
    return violations


def main(argv: list[str]) -> int:
    files = {"guide": GUIDE_PATH, "upgrade": UPGRADE_PATH}
    violations: list[Violation] = []
    if len(argv) > 1:
        # 允许校验指定文件，方便 CI 在单文件改动时快速给出结论。
        for raw in argv[1:]:
            path = Path(raw)
            kind = "upgrade" if "upgrade" in path.name else "guide"
            violations += check_document(path.read_text(encoding="utf-8"), kind)
    else:
        violations = validate_all()

    for item in violations:
        print(item)
    if violations:
        print(f"\n{len(violations)} 条违规；规则码说明见 doccheck.CODES", file=sys.stderr)
        return 1
    print(f"PASS {files['guide'].name} / {files['upgrade'].name}")
    return 0


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(main(sys.argv))
