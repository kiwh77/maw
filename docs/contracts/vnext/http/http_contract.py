"""本机 HTTP /api/v1 契约的机检实现。

校验对象是「一次 HTTP 交换」样本；返回触发的 H 码列表。
契约正文见 http-api-contract-v1.md。
"""

import json
import re
from pathlib import Path

CONTRACT_ID = "maw.http.api/v1"

# 与契约 §8 的不变量登记表逐条对齐；测试会强制两边一致。
INVARIANTS = {
    "H100": "必填字段存在且类型正确",
    "H101": "`contract` 必须是 `maw.http.api/v1`",
    "H110": "2xx 必须已认证；旧 `/api/status` 仅 loopback 无凭据例外",
    "H111": "必须带 Origin 同源且 `X-CSRF-Token` 等于 `maw_csrf`",
    "H112": "只能走 `Authorization: Bearer`；query 不得出现令牌；响应不得回显",
    "H113": "无凭据只能 `401`，有凭据被拒只能 `403`",
    "H120": "`limit` 在 1–200；列表有 `items` 与 `next_cursor`；拒绝 offset",
    "H121": "非法枚举与非法分页必须是 `422`，不得 `400`／`200`",
    "H130": "返回 `202` + `Location: /api/v1/operations/{id}` + 非终止 `status`",
    "H131": "非终止态带 `retry_after_ms`；终止态不再变化；未知操作 `404`",
    "H140": "所有 POST 必带合法键；同键同请求复用操作；同键异请求 `409`",
    "H150": "写动作必须带 revision 前置；成功回新 revision；不匹配 `409`",
    "H160": "`error.code` 与 HTTP 状态必须符合第 6 节映射",
    "H170": "仅登记路径可用；响应带旧版标记与后继链接；旧写仅 status-reports",
    "H171": "上报不得改任务状态；`actor` 必须存在且与 owner 一致",
}

ERROR_STATUS = {
    "malformed_json": 400,
    "missing_field": 400,
    "unauthenticated": 401,
    "csrf_failed": 403,
    "origin_mismatch": 403,
    "insufficient_scope": 403,
    "loopback_required": 403,
    "not_found": 404,
    "method_not_allowed": 405,
    "revision_mismatch": 409,
    "idempotency_key_reuse": 409,
    "ambiguous_display_id": 409,
    "not_owner": 409,
    "write_conflict": 409,
    "invalid_pagination": 422,
    "invalid_idempotency_key": 422,
    "revision_required": 422,
    "unknown_enum": 422,
    "invalid_transition": 422,
    "dependency_retired": 422,
    "unknown_actor": 422,
    "busy": 503,
}

LEGACY_PATHS = {
    ("GET", "/api/board"),
    ("GET", "/api/task"),
    ("GET", "/api/feed"),
    ("GET", "/api/events"),
    ("GET", "/snapshot.json"),
    ("POST", "/api/status"),
}

ASYNC_SUFFIXES = ("/claims", "/status-transitions", "/submissions", "/merges")
IDEMPOTENCY_KEY = re.compile(r"^[A-Za-z0-9._:-]{16,128}$")
REVISION_ETAG = re.compile(r'^"rev-(\d+)"$')
LOCATION = re.compile(r"^/api/v1/operations/[A-Za-z0-9_-]+$")
MUTATING = {"POST", "PUT", "PATCH", "DELETE"}
REPORTED_STATUS = {"idle", "working", "blocked", "offline"}
TASK_STATUS = {
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


def load(path):
    return json.loads(Path(path).read_text(encoding="utf-8"))


def _headers(part):
    return {str(k).lower(): str(v) for k, v in (part.get("headers") or {}).items()}


def _auth(request):
    headers = _headers(request)
    authorization = headers.get("authorization", "")
    if authorization.lower().startswith("bearer "):
        return "bearer", authorization[7:].strip()
    cookies = headers.get("cookie", "")
    if "maw_session=" in cookies:
        return "session", None
    return None, None


def _is_async(request):
    return request.get("method") == "POST" and str(request.get("path", "")).endswith(ASYNC_SUFFIXES)


def _is_legacy(request):
    path = request.get("path", "")
    return path.startswith("/api/") and not path.startswith("/api/v1") or path == "/snapshot.json"


def _is_status_report(request):
    return request.get("path") in {"/api/status", "/api/v1/status-reports"}


def validate(doc):
    found = []
    if not isinstance(doc, dict):
        return ["H100"]
    if doc.get("contract") != CONTRACT_ID:
        found.append("H101")
    cases = doc.get("cases")
    if not isinstance(cases, list) or not cases:
        return sorted(set(found + ["H100"]))
    for case in cases:
        found.extend(validate_case(case))
    return sorted(set(found))


def validate_case(case):
    found = []
    request = case.get("request") or {}
    response = case.get("response") or {}
    for key, kind in (("request", dict), ("response", dict), ("expect", dict)):
        if not isinstance(case.get(key), kind):
            found.append("H100")
    if "H100" in found:
        return found
    method = str(request.get("method", "")).upper()
    path = str(request.get("path", ""))
    query = {str(k): str(v) for k, v in (request.get("query") or {}).items()}
    request_headers = _headers(request)
    response_headers = _headers(response)
    status = response.get("status")
    body = response.get("body") or {}
    request_body = request.get("body") or {}
    auth_kind, token = _auth(request)

    # H110：2xx 必须已认证（旧 loopback 上报例外）。
    if isinstance(status, int) and 200 <= status < 300:
        loopback_report = _is_status_report(request) and request.get("loopback") is True
        if auth_kind is None and not loopback_report:
            found.append("H110")

    # H111：会话写请求的 CSRF 与 Origin。
    if auth_kind == "session" and method in MUTATING and path != "/api/status":
        cookie_csrf = ""
        match = re.search(r"maw_csrf=([^;]+)", request_headers.get("cookie", ""))
        if match:
            cookie_csrf = match.group(1)
        origin = request_headers.get("origin", "")
        host = request_headers.get("host", "")
        same_origin = bool(origin) and origin.split("//")[-1] == host
        if not same_origin or not cookie_csrf or request_headers.get("x-csrf-token") != cookie_csrf:
            found.append("H111")

    # H112：令牌只能走 Authorization，不得出现在 query 或响应中。
    if request_headers.get("authorization") and auth_kind != "bearer":
        found.append("H112")
    if any(key in query for key in ("token", "access_token", "api_key", "authorization")):
        found.append("H112")
    if token and token in json.dumps(body, ensure_ascii=False):
        found.append("H112")

    # H113：401 / 403 不得混用。
    if status == 403 and auth_kind is None:
        found.append("H113")
    if status == 401 and auth_kind is not None:
        found.append("H113")

    # H120 / H121：分页与枚举。
    if method == "GET" and "offset" in query:
        if status != 422:
            found.append("H120")
    if "limit" in query:
        try:
            limit = int(query["limit"])
        except ValueError:
            limit = -1
        if not 1 <= limit <= 200:
            if status != 422:
                found.append("H120")
    if method == "GET" and path.endswith("/tasks") and isinstance(status, int) and status < 300:
        if not isinstance(body.get("items"), list) or "next_cursor" not in body:
            found.append("H120")
    if isinstance(request_body.get("status"), str):
        allowed = REPORTED_STATUS | TASK_STATUS
        if request_body["status"] not in allowed and status != 422:
            found.append("H121")

    # H130：异步动作成功受理必须是 202 且带操作地址；条件失败（4xx）不在此列。
    if _is_async(request) and isinstance(status, int) and 200 <= status < 300:
        operation = (body or {}).get("operation") or {}
        if status != 202 or not LOCATION.match(response_headers.get("location", "")):
            found.append("H130")
        if operation.get("status") not in {"queued", "running"}:
            found.append("H130")
    elif (
        method in MUTATING
        and isinstance(status, int)
        and 200 <= status < 300
        and "operation" in body
        and not _is_async(request)
    ):
        found.append("H130")

    # H131：操作查询形态。
    if method == "GET" and path.startswith("/api/v1/operations/"):
        operation = body.get("operation") or {}
        state = operation.get("status")
        if state in {"queued", "running"}:
            retry = body.get("retry_after_ms")
            if not isinstance(retry, int) or not 200 <= retry <= 5000:
                found.append("H131")
        elif state == "succeeded":
            if "result" not in body or "retry_after_ms" in body:
                found.append("H131")
        elif state == "failed":
            if "error" not in body or "retry_after_ms" in body:
                found.append("H131")
        elif status == 200:
            found.append("H131")

    # H140：幂等键。
    key = request_headers.get("idempotency-key", "")
    if method == "POST" and path != "/api/status":  # 旧 loopback 上报保留无键兼容
        if not IDEMPOTENCY_KEY.match(key):
            found.append("H140")
        if body.get("idempotent_replay") is True and not key:
            found.append("H140")
    error_code = ((body.get("error") or {}) or {}).get("code")
    if error_code == "idempotency_key_reuse" and status != 409:
        found.append("H140")

    # H150：条件写入。
    if method == "POST" and (_is_async(request) or path.endswith("/status-transitions")):
        expected = request_body.get("expected_revision")
        if_match = response_headers.get("if-match", "")  # 兼容样本把前置头记录在任一位置
        if_match = request_headers.get("if-match", if_match)
        if not isinstance(expected, int) and not REVISION_ETAG.match(if_match):
            if status not in (409, 422):
                found.append("H150")
        if isinstance(status, int) and 200 <= status < 300 and "revision" not in body:
            found.append("H150")
        if error_code == "revision_mismatch" and status != 409:
            found.append("H150")

    # H160：错误码与状态映射。
    if error_code:
        if ERROR_STATUS.get(error_code) != status:
            found.append("H160")

    # H170：旧入口适配。
    if _is_legacy(request):
        if (method, path) not in LEGACY_PATHS:
            found.append("H170")
        if method in MUTATING and path != "/api/status":
            found.append("H170")
        if isinstance(status, int) and 200 <= status < 300:
            if response_headers.get("x-maw-legacy") != "1":
                found.append("H170")
            if not re.search(r'rel="successor-version"', response_headers.get("link", "")):
                found.append("H170")

    # H171：状态归属。
    if _is_status_report(request):
        if body.get("changed_task_status") is True:
            found.append("H171")
        if request_body.get("task_id") and not str(request_body.get("actor", "")).strip():
            found.append("H171")
        if error_code == "not_owner" and status != 409:
            found.append("H171")

    return found
