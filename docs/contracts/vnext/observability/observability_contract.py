"""时间轴与计量契约的机检实现（VN-006）。

输入是一份完整快照；返回触发的 T 码列表。
契约正文见 timeline-metering-contract-v1.md。
"""

import re
from datetime import datetime, timezone

CONTRACT_ID = "maw.observability.timeline/v1"

INVARIANTS = {
    "T100": "必填字段存在且类型正确；时间可解析为 ISO8601",
    "T101": "`contract` 必须是 `maw.observability.timeline/v1`",
    "T110": "同快照内分别不得重复",
    "T111": "只能是 task_phase／cli_process／turn／explicit_wait",
    "T112": "`end_at >= start_at`；未结束只能 `null`；不得负时长",
    "T113": "同一身份同一类别不得重叠",
    "T114": "`run_id`／`generation` 必须成对出现；`phase` 属于封闭集合",
    "T120": "水位不后退；`complete_before <= cursor`；gaps 升序不重叠",
    "T121": "只能是 observed／estimated／unknown；estimated 必须给 basis",
    "T130": "分段时长之和等于区间时长；段不重叠且在区间内",
    "T140": "token 非负；`total = input + output`；缓存字段不并入 input",
    "T141": "下降只能由新 epoch 承担；不得出现负增量",
    "T142": "unknown 时 token 为 null，不得填 0 或估算",
    "T143": "`turn_id` 唯一；`turn_count` 按轮次去重",
    "T144": "允许回溯，但必须标记 `late_arrival` 且不回退水位",
    "T145": "时长按并集、用量按轮次去重；总数必须与明细重算一致",
}

KINDS = {"task_phase", "cli_process", "turn", "explicit_wait"}
PHASES = {
    "backlog",
    "ready",
    "claimed",
    "in_progress",
    "self_check",
    "in_review",
    "merging",
    "conflict",
    "blocked",
    "done",
    "cancelled",
}
QUALITY = {"observed", "estimated", "unknown"}
COMPLETENESS = {"complete", "partial", "unknown"}
WAIT_REASONS = {"blocked", "manual_pause", "external_dependency"}


def _parse(value):
    if not isinstance(value, str):
        return None
    text = value.replace("Z", "+00:00")
    try:
        parsed = datetime.fromisoformat(text)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        return parsed.replace(tzinfo=timezone.utc)
    return parsed


def _seconds(start, end):
    return (end - start).total_seconds()


def _union_seconds(intervals):
    total = 0.0
    for start, end in intervals:
        if end <= start:
            continue
        total += _seconds(start, end)
    return total


def _merge(ranges):
    merged = []
    for start, end in sorted(ranges):
        if merged and start <= merged[-1][1]:
            merged[-1][1] = max(merged[-1][1], end)
        else:
            merged.append([start, end])
    return merged


def validate(doc):
    found = []
    if not isinstance(doc, dict):
        return ["T100"]
    if doc.get("contract") != CONTRACT_ID:
        found.append("T101")
    for key in ("project_id", "generated_at", "timezone", "completeness", "watermark", "intervals", "turns", "totals"):
        if key not in doc:
            found.append("T100")
    if "T100" in found:
        return sorted(set(found + (["T101"] if "T101" in found else [])))

    if doc.get("completeness") not in COMPLETENESS:
        found.append("T121")

    intervals = doc.get("intervals")
    turns = doc.get("turns")
    totals = doc.get("totals")
    watermark = doc.get("watermark") or {}
    found.extend(_validate_watermark(watermark))
    if not isinstance(intervals, list) or not isinstance(turns, list) or not isinstance(totals, dict):
        return sorted(set(found + ["T100"]))

    interval_ids = set()
    turn_ids = set()
    parsed = []
    for item in intervals:
        if not isinstance(item, dict):
            found.append("T100")
            continue
        interval_id = item.get("interval_id")
        if not isinstance(interval_id, str) or not interval_id:
            found.append("T100")
            continue
        if interval_id in interval_ids:
            found.append("T110")
        interval_ids.add(interval_id)
        kind = item.get("kind")
        if kind not in KINDS:
            found.append("T111")
        quality = item.get("quality")
        if quality not in QUALITY:
            found.append("T121")
        if quality == "estimated" and not str(item.get("basis", "")).strip():
            found.append("T121")
        start = _parse(item.get("start_at"))
        end = _parse(item.get("end_at")) if item.get("end_at") is not None else None
        if start is None:
            found.append("T100")
            continue
        if end is not None and end < start:
            found.append("T112")
            continue
        run_id = item.get("run_id")
        generation = item.get("generation")
        if (run_id is None) != (generation is None):
            found.append("T114")
        if kind == "task_phase":
            if item.get("phase") not in PHASES:
                found.append("T114")
        if kind == "explicit_wait" and item.get("reason") not in WAIT_REASONS:
            found.append("T114")
        if end is not None:
            found.extend(_validate_day_splits(item, start, end))
        if kind == "turn" and item.get("late_arrival") is True:
            received = _parse(item.get("received_at"))
            if received is None or start >= received:
                found.append("T144")
        parsed.append((item, start, end))

    # T113：同身份同类别的重叠。
    groups = {}
    for item, start, end in parsed:
        if end is None:
            continue
        key = (item.get("instance_id"), item.get("run_id"), item.get("kind"))
        groups.setdefault(key, []).append((start, end))
    for ranges in groups.values():
        ordered = sorted(ranges)
        for previous, current in zip(ordered, ordered[1:]):
            if current[0] < previous[1]:
                found.append("T113")
                break

    # T143：轮次唯一。
    usage_total = 0.0
    unknown_usage = False
    for turn in turns:
        if not isinstance(turn, dict):
            found.append("T100")
            continue
        turn_id = turn.get("turn_id")
        if not isinstance(turn_id, str) or not turn_id:
            found.append("T100")
            continue
        if turn_id in turn_ids:
            found.append("T110")
        turn_ids.add(turn_id)
        found.extend(_validate_usage(turn))
        usage = turn.get("usage") or {}
        if isinstance(usage.get("input_tokens"), int) and isinstance(usage.get("output_tokens"), int):
            usage_total += usage["input_tokens"] + usage["output_tokens"]
        if usage.get("quality") == "unknown":
            unknown_usage = True
        if turn.get("late_arrival") is True:
            received = _parse(turn.get("received_at"))
            started = _parse(turn.get("started_at"))
            if received is None or started is None or started >= received:
                found.append("T144")

    found.extend(_validate_totals(totals, parsed, turn_ids, usage_total, unknown_usage))
    return sorted(set(found))


def _validate_watermark(watermark):
    found = []
    cursor = watermark.get("cursor")
    complete_before = watermark.get("complete_before")
    if not isinstance(cursor, int) or not isinstance(complete_before, int):
        return ["T100"]
    if complete_before > cursor:
        found.append("T120")
    gaps = watermark.get("gaps")
    if gaps is None:
        return found
    if not isinstance(gaps, list):
        return found + ["T100"]
    previous_to = None
    for gap in gaps:
        if not isinstance(gap, dict):
            found.append("T100")
            continue
        start, end = gap.get("from"), gap.get("to")
        if not isinstance(start, int) or not isinstance(end, int):
            found.append("T100")
            continue
        if end < start or end > complete_before:
            found.append("T120")
        if previous_to is not None and start <= previous_to:
            found.append("T120")
        previous_to = end
    return found


def _validate_day_splits(item, start, end):
    found = []
    splits = item.get("day_splits")
    if splits is None:
        return found
    if not isinstance(splits, list) or not splits:
        return ["T100"]
    total = 0.0
    ordered = []
    for split in splits:
        split_start = _parse(split.get("start_at"))
        split_end = _parse(split.get("end_at"))
        if split_start is None or split_end is None:
            found.append("T100")
            continue
        if split_end < split_start or split_start < start or split_end > end:
            found.append("T130")
        total += _seconds(split_start, split_end)
        ordered.append((split_start, split_end))
    if any(current[0] < previous[1] for previous, current in zip(sorted(ordered), sorted(ordered)[1:])):
        found.append("T130")
    if abs(total - _seconds(start, end)) > 1:
        found.append("T130")
    return found


def _validate_usage(turn):
    found = []
    usage = turn.get("usage")
    if not isinstance(usage, dict):
        return ["T100"]
    quality = usage.get("quality")
    if quality not in QUALITY:
        return ["T121"]
    input_tokens = usage.get("input_tokens")
    output_tokens = usage.get("output_tokens")
    if quality == "unknown":
        if input_tokens is not None or output_tokens is not None:
            found.append("T142")
    else:
        if not isinstance(input_tokens, int) or not isinstance(output_tokens, int):
            found.append("T140")
        elif input_tokens < 0 or output_tokens < 0:
            found.append("T140")
        elif isinstance(usage.get("total_tokens"), int) and usage["total_tokens"] != input_tokens + output_tokens:
            found.append("T140")
    for key in ("cache_read_tokens", "cache_write_tokens", "total_tokens"):
        value = usage.get(key)
        if value is not None and (not isinstance(value, int) or value < 0):
            found.append("T140")
    epoch = usage.get("epoch")
    if not isinstance(epoch, int) or epoch < 1:
        found.append("T141")
    elif usage.get("reset_observed") is True and epoch < 2:
        found.append("T141")
    return found


def _validate_totals(totals, parsed, turn_ids, usage_total, unknown_usage):
    found = []
    per_kind = {"task_phase": [], "cli_process": [], "explicit_wait": []}
    engineering = {}
    for item, start, end in parsed:
        if end is None:
            continue
        kind = item.get("kind")
        key = (item.get("instance_id"), item.get("run_id"))
        if kind in per_kind:
            per_kind[kind].append((key, start, end))
        if kind in ("task_phase", "cli_process"):
            engineering.setdefault(key, []).append((start, end))

    computed = {
        "task_phase_seconds": _sum_by_key(per_kind["task_phase"]),
        "cli_process_seconds": _sum_by_key(per_kind["cli_process"]),
        "engineering_seconds": sum(_union_seconds(_merge(ranges)) for ranges in engineering.values()),
    }
    for key, value in computed.items():
        reported = totals.get(key)
        if not isinstance(reported, (int, float)):
            found.append("T100")
        elif abs(float(reported) - value) > 1:
            found.append("T145")

    turn_count = totals.get("turn_count")
    if turn_count != len(turn_ids):
        found.append("T143")
    reported_usage = totals.get("usage_total_tokens")
    if unknown_usage:
        if totals.get("usage_unknown") is not True:
            found.append("T142")
        if isinstance(reported_usage, (int, float)) and reported_usage != usage_total:
            found.append("T145")
    else:
        if totals.get("usage_unknown") is not False:
            found.append("T142")
        if not isinstance(reported_usage, (int, float)) or abs(float(reported_usage) - usage_total) > 0.5:
            found.append("T145")
    return found


def _sum_by_key(items):
    grouped = {}
    for key, start, end in items:
        grouped.setdefault(key, []).append((start, end))
    return sum(_union_seconds(_merge(ranges)) for ranges in grouped.values())
