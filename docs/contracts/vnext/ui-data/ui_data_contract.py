"""页面上下文与挂载契约的机检实现。

校验对象是「页面装配快照」；返回触发的 V 码列表。
契约正文见 view-context-contract-v1.md。
"""

import json
import re
from pathlib import Path

CONTRACT_ID = "maw.ui.data/v1"

# 与契约 §7 的不变量登记表逐条对齐；测试会强制两边一致。
INVARIANTS = {
    "V100": "必填字段存在且类型正确",
    "V101": "`contract` 必须是 `maw.ui.data/v1`",
    "V110": "必须冻结；`navigate` 返回新上下文，旧对象不变",
    "V111": "必填字段齐全，`lang`／`theme`／`filters` 取值合法",
    "V120": "非安全方法必须带 CSRF 双提交与同源 Origin，浏览器端不得用 Bearer",
    "V121": "写请求必须带合法幂等键；重试复用同一键；键不得进 query",
    "V122": "条件写必须带 `expected_revision`；`409` 不得自动重试",
    "V123": "只有 GET 的 503／网络错误可重试，最多 3 次，4xx 不重试",
    "V124": "返回统一结构；`202` 暴露操作地址；错误归一化不抛 HTTP 异常",
    "V130": "键必须命名空间化；缺失回落顺序正确；不得返回空串",
    "V131": "同视图 zh／en 键集合一致",
    "V140": "视图 id 唯一；mount／unmount 成对存在；切换先卸载再挂载",
    "V150": "规范路由可解析；旧锚点只读映射；未知视图回落；URL 无凭据",
    "V151": "解析→序列化往返稳定；非法参数回落默认值",
    "V152": "筛选变化用 replaceState，视图切换用 pushState",
}

SAFE_METHODS = {"GET", "HEAD", "OPTIONS"}
VIEW_ID = re.compile(r"^[a-z0-9-]+$")
DICT_KEY = re.compile(r"^view\.([a-z0-9-]+)\.([a-z0-9.]+)$")
IDEMPOTENCY = re.compile(r"^[A-Za-z0-9._:-]{16,128}$")
LOCATION = re.compile(r"^/api/v1/operations/[A-Za-z0-9_-]+$")
CREDENTIAL_IN_URL = re.compile(
    r"(token|access_token|api_key|idempotency|maw_session|maw_csrf)", re.I
)
CONTEXT_REQUIRED = (
    "api_version",
    "view_id",
    "lang",
    "theme",
    "project_id",
    "filters",
    "selection",
    "cursor",
    "capabilities",
    "frozen",
)


def load(path):
    return json.loads(Path(path).read_text(encoding="utf-8"))


def validate(doc):
    if not isinstance(doc, dict):
        return ["V100"]
    found = []
    if doc.get("contract") != CONTRACT_ID:
        found.append("V101")

    sections = (
        ("context", dict, _validate_context),
        ("requests", list, _validate_request),
        ("dicts", dict, _validate_dicts),
        ("registry", dict, _validate_registry),
        ("navigation", dict, _validate_navigation),
    )
    dicts = doc.get("dicts") if isinstance(doc.get("dicts"), dict) else {}
    registry = doc.get("registry") if isinstance(doc.get("registry"), dict) else {}
    for name, kind, func in sections:
        if name not in doc:
            continue
        value = doc[name]
        if not isinstance(value, kind):
            found.append("V100")
            continue
        if name == "requests":
            for item in value:
                found.extend(func(item))
        elif name == "registry":
            found.extend(func(value, dicts))
        elif name == "navigation":
            found.extend(func(value, registry))
        else:
            found.extend(func(value))
    return sorted(set(found))


def _validate_context(ctx):
    found = []
    if [key for key in CONTEXT_REQUIRED if key not in ctx]:
        found.append("V111")
    if ctx.get("api_version") != CONTRACT_ID:
        found.append("V111")
    if ctx.get("frozen") is not True:
        found.append("V110")
    if ctx.get("navigate_returns_new") is False:
        found.append("V110")
    if ctx.get("navigate_mutated_original") is True:
        found.append("V110")

    if ctx.get("lang") not in {"zh", "en"}:
        found.append("V111")
    if ctx.get("theme") not in {"system", "light", "dark"}:
        found.append("V111")

    filters = ctx.get("filters")
    if not isinstance(filters, dict):
        found.append("V111")
    else:
        if set(filters) - {"agent", "status", "q"}:
            found.append("V111")
        if any(not isinstance(value, str) for value in filters.values()):
            found.append("V111")

    selection = ctx.get("selection")
    if not isinstance(selection, dict):
        found.append("V111")
    else:
        if set(selection) - {"instance_id", "project_id"}:
            found.append("V111")
        if any(not isinstance(value, str) for value in selection.values()):
            found.append("V111")

    capabilities = ctx.get("capabilities")
    if not isinstance(capabilities, dict):
        found.append("V111")
    elif not isinstance(capabilities.get("write"), bool) or not isinstance(
        capabilities.get("admin"), bool
    ):
        found.append("V111")
    return found


def _validate_request(req):
    found = []
    method = str(req.get("method", "")).upper()
    query = req.get("query") or {}
    headers = {str(k).lower(): str(v) for k, v in (req.get("headers") or {}).items()}
    body = req.get("body") or {}
    result = req.get("result")
    attempts = req.get("attempts", 1)

    if not isinstance(req.get("path"), str) or not req.get("path"):
        found.append("V100")
    if not isinstance(attempts, int) or attempts < 1:
        found.append("V100")

    # V120：浏览器端永不用 Bearer；写请求必须有 CSRF 双提交与同源 Origin。
    if headers.get("authorization", "").lower().startswith("bearer ") or req.get(
        "bearer"
    ) is True:
        found.append("V120")
    if method not in SAFE_METHODS:
        csrf = re.search(r"maw_csrf=([^;]+)", headers.get("cookie", ""))
        if not csrf or headers.get("x-csrf-token") != csrf.group(1):
            found.append("V120")
        origin = headers.get("origin", "")
        host = headers.get("host", "")
        if not origin or not host or origin.split("//")[-1] != host:
            found.append("V120")

    # V121：写请求必须有合法幂等键；重试复用同一键；键不得进 query。
    if method not in SAFE_METHODS and not IDEMPOTENCY.match(
        headers.get("idempotency-key", "")
    ):
        found.append("V121")
    if attempts > 1 and method not in SAFE_METHODS:
        if req.get("reused_idempotency_key") is not True:
            found.append("V121")
    if any(str(key).lower().replace("_", "-") == "idempotency-key" for key in query):
        found.append("V121")

    # V122：条件写必须带匹配的 If-Match；409 不得自动重试。
    expected = body.get("expected_revision")
    if expected is not None:
        if not isinstance(expected, int) or headers.get("if-match") != f'"rev-{expected}"':
            found.append("V122")
    if (
        req.get("auto_retry") is True
        and isinstance(result, dict)
        and result.get("status") == 409
    ):
        found.append("V122")

    # V123：只有 GET 的 503／网络错误可重试，最多 3 次。
    status = result.get("status") if isinstance(result, dict) else None
    if attempts > 3:
        found.append("V123")
    if attempts > 1:
        retryable = method == "GET" and (status == 503 or req.get("network_error") is True)
        if not retryable:
            found.append("V123")
    if req.get("auto_retry") is True and method not in SAFE_METHODS:
        found.append("V123")

    # V124：统一返回结构，错误归一化，202 暴露操作地址。
    if not isinstance(result, dict):
        found.append("V124")
        return found
    status = result.get("status")
    ok = result.get("ok")
    if not isinstance(status, int) or not isinstance(ok, bool):
        found.append("V124")
    elif ok != (200 <= status < 300):
        found.append("V124")
    if "data" not in result or "error" not in result:
        found.append("V124")
    if result.get("threw") is True:
        found.append("V124")
    if ok is False:
        error = result.get("error")
        if not isinstance(error, dict) or not {"code", "message", "retryable"} <= set(
            error
        ):
            found.append("V124")
    if status == 202:
        operation = str(result.get("operation_id") or "")
        if not operation or not LOCATION.match(str(result.get("location", ""))):
            found.append("V124")
    return found


def _validate_dicts(dicts):
    found = []
    for view_id, block in dicts.items():
        if not isinstance(view_id, str) or not VIEW_ID.match(view_id):
            found.append("V100")
            continue
        if not isinstance(block, dict):
            found.append("V100")
            continue
        zh = block.get("zh")
        en = block.get("en")
        if not isinstance(zh, dict) or not isinstance(en, dict):
            found.append("V100")
            continue
        for table in (zh, en):
            for key, value in table.items():
                match = DICT_KEY.match(str(key))
                if not match or match.group(1) != view_id:
                    found.append("V130")
                if not isinstance(value, str) or not value.strip():
                    found.append("V130")
        if set(zh) != set(en):
            found.append("V131")
        fallback = block.get("fallback")
        if fallback is not None and list(fallback) != ["current", "zh", "global", "key"]:
            found.append("V130")
        for item in block.get("resolution") or []:
            if isinstance(item, dict):
                value = item.get("value")
                if not isinstance(value, str) or not value.strip():
                    found.append("V130")
    return found


def _validate_registry(registry, dicts):
    found = []
    views = registry.get("views")
    if not isinstance(views, list) or not views:
        return ["V100"]
    seen = set()
    for view in views:
        if not isinstance(view, dict):
            found.append("V100")
            continue
        view_id = str(view.get("id", ""))
        if not VIEW_ID.match(view_id):
            found.append("V100")
            continue
        if view_id in seen:
            found.append("V140")
        seen.add(view_id)
        if view.get("has_mount") is not True or view.get("has_unmount") is not True:
            found.append("V140")
        order = view.get("order")
        if not isinstance(order, int) or order <= 0:
            found.append("V100")
        title = str(view.get("title_key", ""))
        if not DICT_KEY.match(title) or not title.startswith(f"view.{view_id}."):
            found.append("V140")
        elif view_id in dicts:
            block = dicts[view_id]
            if not isinstance(block, dict) or title not in (
                block.get("zh") or {}
            ) or title not in (block.get("en") or {}):
                found.append("V140")
    if registry.get("single_active") is not True:
        found.append("V140")
    for transition in registry.get("transitions") or []:
        if not isinstance(transition, dict) or transition.get("unmounted_first") is not True:
            found.append("V140")
    return found


def _validate_navigation(nav, registry):
    found = []
    if nav.get("roundtrip") is not True:
        found.append("V151")
    if nav.get("invalid_params") != "ignore":
        found.append("V151")
    urls = nav.get("urls")
    if not isinstance(urls, list):
        return sorted(set(found + ["V100"]))

    registered = set()
    for view in registry.get("views") or []:
        if isinstance(view, dict) and view.get("id"):
            registered.add(str(view["id"]))

    for item in urls:
        if not isinstance(item, dict):
            found.append("V100")
            continue
        url = str(item.get("url", ""))
        resolved = item.get("resolved")
        if CREDENTIAL_IN_URL.search(url):
            found.append("V150")
        if not isinstance(resolved, dict) or "view_id" not in resolved:
            found.append("V100")
            continue
        change = item.get("change")
        history = item.get("history")
        if change not in {"filter", "view", "legacy", "fallback"}:
            found.append("V100")
        if resolved.get("blocked") is True:
            found.append("V151")

        if url.startswith("#/v/"):
            requested = url[len("#/v/") :].split("?")[0].split("#")[0]
            if registered and requested not in registered:
                if resolved.get("view_id") != "board" or resolved.get("error") is not False:
                    found.append("V150")
            if resolved.get("error") is True:
                found.append("V150")
            if resolved.get("legacy") is True:
                found.append("V150")
        elif url.startswith("#"):
            if resolved.get("legacy") is not True:
                found.append("V150")
            if resolved.get("view_id") != "board":
                found.append("V150")

        expected = "push" if change == "view" else "replace"
        if history != expected:
            found.append("V152")
    return found
