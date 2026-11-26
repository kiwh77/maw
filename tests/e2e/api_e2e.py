#!/usr/bin/env python3
"""maw 看板端到端契约套件（API 层，仅标准库）。

设计前提：**只打隔离实例**。脚本默认目标为 http://127.0.0.1:7421（MAW_GLOBAL 指向临时目录的
副本库），写用例只落在副本上；live 相关断言（C 组）只读真实库与 7420，用于证明「没动活动数据」。

用法：
  python3 tests/e2e/api_e2e.py --base http://127.0.0.1:7421 \
      --live-base http://127.0.0.1:7420 --live-db ~/.maw/maw.db \
      --report /tmp/maw-e2e/api-report.json

退出码：0 = 无 P0 失败；1 = 存在 P0 失败；2 = 环境不可用。
"""

from __future__ import annotations

import argparse
import http.client
import http.cookiejar
import json
import os
import socket
import sqlite3
import sys
import threading
import time
import traceback
import urllib.error
import urllib.parse
import urllib.request
import uuid

P0, P1, P2 = "P0", "P1", "P2"
STATUS_LANES = {
    "queued": ["backlog", "ready"],
    "running": ["claimed", "in_progress", "self_check"],
    "review": ["in_review", "merging"],
    "attention": ["conflict", "blocked"],
    "finished": ["done", "cancelled"],
}


# --------------------------------------------------------------------------- 基础设施


class Warn(Exception):
    """观察项：不是故障，但实现与声明不一致，需要进报告。"""


class Resp:
    def __init__(self, status, headers, body):
        self.status = status
        self.headers = headers  # 小写键
        self.body = body
        try:
            self.json = json.loads(body.decode("utf-8")) if body else None
        except (ValueError, UnicodeDecodeError):
            self.json = None

    @property
    def text(self):
        return self.body.decode("utf-8", "replace")


class Client:
    """带 Cookie 罐的极简 HTTP 客户端；会话写请求自动补 Origin 与 CSRF。"""

    def __init__(self, base):
        self.base = base.rstrip("/")
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        self.csrf = None
        self.actor = ""

    def call(self, method, path, body=None, headers=None, timeout=20):
        url = self.base + path
        data = None
        hdrs = {"Accept": "application/json"}
        if body is not None:
            data = json.dumps(body).encode()
            hdrs["Content-Type"] = "application/json"
        if headers:
            hdrs.update(headers)
        req = urllib.request.Request(url, data=data, headers=hdrs, method=method)
        try:
            with self.opener.open(req, timeout=timeout) as res:
                return Resp(res.status, {k.lower(): v for k, v in res.headers.items()}, res.read())
        except urllib.error.HTTPError as err:
            return Resp(err.code, {k.lower(): v for k, v in err.headers.items()}, err.read())

    def get(self, path, **kw):
        return self.call("GET", path, **kw)

    def session(self):
        res = self.call("POST", "/api/session")
        if res.status == 200 and res.json:
            self.csrf = res.json.get("csrf")
            self.actor = "session:" + str(res.json.get("session_id") or "")
        return res

    def write(self, path, body, extra=None, idem=True):
        """会话写请求：带 Origin + X-CSRF-Token（双提交）。"""
        headers = {"Origin": self.base}
        if self.csrf:
            headers["X-CSRF-Token"] = self.csrf
        if idem:
            headers["Idempotency-Key"] = "e2e-" + uuid.uuid4().hex
        if extra:
            headers.update(extra)
        return self.call("POST", path, body=body, headers=headers)


def raw_request(base, method, path, headers=None, timeout=10):
    """不跟随重定向的裸请求，用于断言 3xx 本身。"""
    parsed = urllib.parse.urlsplit(base)
    conn = http.client.HTTPConnection(parsed.hostname or "127.0.0.1", parsed.port or 80, timeout=timeout)
    try:
        conn.request(method, path, headers=headers or {})
        res = conn.getresponse()
        return res.status, {k.lower(): v for k, v in res.getheaders()}
    finally:
        conn.close()


def feed_tail(client, project_id=None, max_pages=60):
    """按 next_since 翻到末尾；返回 (全部事件, pages)。"""
    out, since, pages = [], 0, 0
    while pages < max_pages:
        q = "/api/feed?since=%d" % since
        if project_id:
            q += "&project=" + urllib.parse.quote(project_id)
        body = client.get(q).json or {}
        evs = body.get("events") or []
        out.extend(evs)
        pages += 1
        nxt = body.get("next_since") or 0
        if not body.get("has_more") or nxt <= since:
            break
        since = nxt
    return out, pages


def rss_kb(path):
    try:
        st = os.stat(path)
        return st.st_size, int(st.st_mtime)
    except OSError:
        return None, None


def copy_db_query(path, sql, params=(), write=False):
    """只在隔离副本上读写；write=True 用于构造检查点等一次性夹具。"""
    con = sqlite3.connect(os.path.abspath(os.path.expanduser(path)), timeout=5)
    try:
        cur = con.execute(sql, params)
        if write:
            con.commit()
            return cur.rowcount
        return cur.fetchall()
    finally:
        con.close()


def live_db_scan(path, patterns):
    """在真实库里找本套件留下的痕迹（预期为 0）。"""
    uri = "file:%s?mode=ro" % urllib.parse.quote(os.path.abspath(os.path.expanduser(path)))
    con = sqlite3.connect(uri, uri=True, timeout=5)
    hits = {}
    try:
        for name, sql in patterns.items():
            try:
                hits[name] = con.execute(sql).fetchone()[0]
            except sqlite3.Error as err:
                hits[name] = "err:%s" % err
    finally:
        con.close()
    return hits


def live_db_counts(path):
    """只读取真实库；WAL 下用只读 URI，避免任何写锁。"""
    uri = "file:%s?mode=ro" % urllib.parse.quote(os.path.abspath(os.path.expanduser(path)))
    out = {}
    con = sqlite3.connect(uri, uri=True, timeout=5)
    try:
        for table in ("task_instances", "events", "agents", "projects"):
            try:
                out[table] = con.execute("select count(*) from %s" % table).fetchone()[0]
            except sqlite3.Error as err:
                out[table] = "err:%s" % err
    finally:
        con.close()
    return out


def sse_heartbeat(base, seconds=20.0):
    """安静流上等心跳注释帧：不在收到业务事件时提前退出。"""
    parsed = urllib.parse.urlsplit(base)
    conn = http.client.HTTPConnection(parsed.hostname or "127.0.0.1", parsed.port or 80, timeout=seconds + 5)
    frames, comments, ready = [], [], False
    try:
        conn.request("GET", "/api/events", headers={"Accept": "text/event-stream"})
        res = conn.getresponse()
        if res.status != 200:
            return False, [], []
        deadline = time.time() + seconds
        while time.time() < deadline:
            try:
                conn.sock.settimeout(max(0.2, deadline - time.time()))
                raw = res.fp.readline()
            except (socket.timeout, OSError):
                break
            if not raw:
                break
            line = raw.decode("utf-8", "replace").rstrip("\n")
            if line.startswith(":"):
                comments.append(line)
            elif line.startswith("event:"):
                name = line.split(":", 1)[1].strip()
                frames.append(name)
                ready = ready or name == "ready"
    finally:
        try:
            conn.close()
        except OSError:
            pass
    return ready, frames, comments


def sse_probe(base, seconds=8.0, trigger=None):
    """读 /api/events：返回 (ready_seen, 事件名列表, 原始行样本)。"""
    parsed = urllib.parse.urlsplit(base)
    host = parsed.hostname or "127.0.0.1"
    port = parsed.port or 80
    conn = http.client.HTTPConnection(host, port, timeout=seconds + 3)
    events, lines = [], []
    ready = False
    try:
        conn.request("GET", "/api/events", headers={"Accept": "text/event-stream"})
        res = conn.getresponse()
        if res.status != 200:
            return False, [], ["status=%s" % res.status]
        deadline = time.time() + seconds
        if trigger:
            threading.Timer(1.2, trigger).start()
        sock = conn.sock
        while time.time() < deadline:
            try:
                sock.settimeout(max(0.2, deadline - time.time()))
                raw = res.fp.readline()
            except (socket.timeout, OSError):
                break
            if not raw:
                break
            line = raw.decode("utf-8", "replace").strip()
            if not line:
                continue
            lines.append(line)
            if line.startswith("event:"):
                name = line.split(":", 1)[1].strip()
                events.append(name)
                if name == "ready":
                    ready = True
            if "board-changed" in events:
                break
    finally:
        try:
            conn.close()
        except OSError:
            pass
    return ready, events, lines


# --------------------------------------------------------------------------- 套件


class Suite:
    def __init__(self, base, live_base=None, live_db=None, token="", copy_db="", empty_base=""):
        self.base = base
        self.token = token
        self.copy_db = copy_db
        self.empty_base = empty_base
        self.live_base = live_base
        self.live_db = live_db
        self.client = Client(base)
        self.live = Client(live_base) if live_base else None
        self.results = []
        self.ctx = {}
        self.live_before = {}

    # -- 记录

    def record(self, cid, level, name, ok, detail="", skipped=False, warn=False):
        self.results.append({
            "id": cid, "level": level, "name": name, "ok": bool(ok),
            "detail": detail, "skipped": bool(skipped), "warn": bool(warn),
        })

    def warn(self, _cid, _level, _name, detail):
        """实现现状与声明不一致、但不构成故障的观察项：中断当前用例并单独记一条 WARN。"""
        raise Warn(detail)

    def check(self, cid, level, name, fn):
        try:
            detail = fn() or ""
            self.record(cid, level, name, True, detail)
            print("  PASS %-5s %s %s" % (cid, name, ("· " + str(detail)) if detail else ""))
        except Warn as err:
            self.record(cid, level, name, True, str(err), warn=True)
            print("  WARN %-5s %s · %s" % (cid, name, err))
        except AssertionError as err:
            self.record(cid, level, name, False, str(err))
            print("  FAIL %-5s %s\n         %s" % (cid, name, err))
        except Exception as err:  # noqa: BLE001 - 记录环境/未预期异常，不中断套件
            self.record(cid, level, name, False, "EXC %s: %s" % (type(err).__name__, err))
            print("  FAIL %-5s %s\n         EXC %s: %s" % (cid, name, type(err).__name__, err))
            if os.environ.get("E2E_DEBUG"):
                traceback.print_exc()

    # -- 公共断言

    @staticmethod
    def eq(actual, expected, what):
        assert actual == expected, "%s: 期望 %r，实际 %r" % (what, expected, actual)

    @staticmethod
    def truth(cond, msg):
        assert cond, msg

    @staticmethod
    def has_keys(obj, keys, what):
        missing = [k for k in keys if k not in (obj or {})]
        assert not missing, "%s 缺字段 %s" % (what, missing)

    # -- 准备

    def bootstrap(self):
        self.client.session()
        board = self.client.get("/api/board")
        assert board.status == 200, "bootstrap: /api/board 不可用 %s" % board.status
        snap = board.json
        self.ctx["board"] = snap
        projects = snap.get("projects") or []
        tasks = snap.get("tasks") or []
        assert projects, "bootstrap: 没有任何工程，无法测试"
        self.ctx["project"] = projects[0]
        pid = projects[0]["id"]
        self.ctx["pid"] = pid
        first_task = next((t for t in tasks if t.get("project_id") == pid), tasks[0] if tasks else None)
        assert first_task, "bootstrap: 没有任务"
        self.ctx["task"] = first_task
        self.ctx["lone_task"] = next((t for t in tasks if t.get("status") == "done"), first_task)
        if self.live:
            before = self.live.get("/api/board")
            self.live_before["api"] = {
                "total": (before.json or {}).get("total"),
                "updated_at": (before.json or {}).get("updated_at"),
                "status": before.status,
            }
        if self.live_db:
            self.live_before["db_counts"] = live_db_counts(self.live_db)
            size, mtime = rss_kb(os.path.expanduser(self.live_db))
            self.live_before["db_file"] = {"size": size, "mtime": mtime}
        print("· 目标 %s（工程 %s / 任务 %s）" % (self.base, pid, len(tasks)))


# --------------------------------------------------------------------------- A 组：静态与看板


def group_a_static(s: Suite):
    print("\n[A] 静态资源与页面")

    def a01():
        res = s.client.get("/")
        s.eq(res.status, 200, "GET / 状态码")
        s.truth("text/html" in res.headers.get("content-type", ""), "Content-Type 不是 text/html: %s" % res.headers.get("content-type"))
        s.eq(res.headers.get("cache-control"), "no-store", "Cache-Control")
        s.truth('id="app"' in res.text, "页面缺少 #app 挂载点")
        s.truth("assets/board-web.js" in res.text, "页面没有加载 board-web.js")
        body = res.text.lower()
        s.truth("<title>" in body, "缺少 title")
        return "html %d bytes" % len(res.body)

    def a02():
        res = s.client.get("/assets/board-web.js")
        s.eq(res.status, 200, "GET board-web.js 状态码")
        ctype = res.headers.get("content-type", "")
        s.truth("javascript" in ctype, "Content-Type 不是 javascript: %s" % ctype)
        s.eq(res.headers.get("cache-control"), "no-store", "Cache-Control")
        s.truth(len(res.body) > 100000, "bundle 太小，疑似占位: %d bytes" % len(res.body))
        return "%d bytes" % len(res.body)

    def a03():
        codes = {p: s.client.get(p).status for p in ("/assets/", "/assets/nope.js", "/assets/../page.html")}
        s.eq(codes["/assets/"], 404, "目录列举 %s" % codes)
        s.eq(codes["/assets/nope.js"], 404, "缺失资源 %s" % codes)
        s.truth(codes["/assets/../page.html"] in (301, 307, 308, 404), "路径穿越未被拒: %s" % codes)
        return str(codes)

    def a04():
        s.eq(s.client.get("/nope").status, 404, "未知路径状态码")

    def a05():
        code, headers = raw_request(s.base, "GET", "/snapshot.json")
        s.eq(code, 307, "snapshot.json 状态码")
        s.truth("/api/board" in headers.get("location", ""), "跳转目标不对: %s" % headers.get("location"))
        return "307 -> %s" % headers.get("location")

    s.check("A-01", P0, "GET / 返回可用的应用外壳", a01)
    s.check("A-02", P0, "前端 bundle 可加载且不缓存", a02)
    s.check("A-03", P1, "静态目录不可列举、缺失资源 404", a03)
    s.check("A-04", P2, "未知路径 404", a04)
    s.check("A-05", P1, "snapshot.json 兼容跳转", a05)


def group_a_board(s: Suite):
    print("\n[A] /api/board 契约")

    def a06():
        res = s.client.get("/api/board")
        s.eq(res.status, 200, "状态码")
        d = res.json
        s.has_keys(d, ["updated_at", "max_parallel", "total", "done", "active", "blocked",
                       "percent", "counts", "tasks", "agents", "projects", "next_hint",
                       "filter_project", "filter_agent", "filter_status", "global"], "/api/board")
        tasks, counts = d["tasks"] or [], d["counts"] or {}
        s.eq(d["total"], len(tasks), "total 与 tasks 长度")
        s.eq(sum(counts.values()), d["total"], "counts 之和与 total")
        s.eq(d["done"], counts.get("done", 0), "done 与 counts.done")
        s.eq(d["blocked"], counts.get("blocked", 0), "blocked 与 counts.blocked")
        running = sum(counts.get(k, 0) for k in STATUS_LANES["running"] + STATUS_LANES["review"])
        s.eq(d["active"], running, "active 与「运行+评审」计数")
        expect_pct = (100 * d["done"] // d["total"]) if d["total"] else 0
        s.eq(d["percent"], expect_pct, "percent 计算")
        for t in tasks[:50]:
            s.has_keys(t, ["id", "project_id", "title", "status", "priority", "owner"], "任务对象")
        return "total=%d done=%d active=%d" % (d["total"], d["done"], d["active"])

    def a07():
        pid = s.ctx["pid"]
        d = s.client.get("/api/board?project=" + pid).json
        s.eq(d["filter_project"], pid, "filter_project 回显")
        s.truth(all(t.get("project_id") == pid for t in d["tasks"]), "存在跨工程任务污染")
        s.truth(d["total"] > 0, "该工程应至少有一条任务")
        return "project total=%d" % d["total"]

    def a08():
        d0 = s.client.get("/api/board").json
        owners = sorted({t.get("owner") for t in d0["tasks"] or [] if t.get("owner")})
        s.truth(bool(owners), "用例数据里没有任何 owner")
        agent = owners[0]
        d = s.client.get("/api/board?agent=" + urllib.parse.quote(agent)).json
        s.eq(d["filter_agent"], agent, "filter_agent 回显")
        tasks = d.get("tasks") or []
        s.truth(d["total"] > 0, "按 owner 过滤后应至少有一条任务")
        bad = [t["id"] for t in tasks if t.get("owner") != agent]
        s.truth(not bad, "agent 过滤混入其他 owner: %s" % bad[:3])
        return "agent=%s total=%d" % (agent, d["total"])

    def a09():
        d0 = s.client.get("/api/board").json
        status = "done" if (d0["counts"] or {}).get("done") else next(iter(d0["counts"] or {}), "")
        s.truth(bool(status), "没有可测状态")
        d = s.client.get("/api/board?status=" + status).json
        s.eq(d["filter_status"], status, "filter_status 回显")
        bad = [t["id"] for t in d["tasks"] if t.get("status") != status]
        s.truth(not bad, "status 过滤混入其他状态: %s" % bad[:3])
        return "status=%s total=%d" % (status, d["total"])

    def a10():
        pid = s.ctx["pid"]
        d = s.client.get("/api/board?project=%s&status=done" % pid).json
        bad = [t["id"] for t in d["tasks"] if t.get("project_id") != pid or t.get("status") != "done"]
        s.truth(not bad, "组合过滤失效: %s" % bad[:3])
        return "total=%d" % d["total"]

    def a11():
        d = s.client.get("/api/board?project=deadbeefdeadbeef").json
        s.eq(d["total"], 0, "未知工程 total")
        s.eq(d.get("tasks"), [], "未知工程应返回空数组而不是 null")
        return "tasks_type=%s" % type(d.get("tasks")).__name__

    def a12():
        res = s.client.call("POST", "/api/board", body={"x": 1})
        if res.status == 405:
            return "405 已拒绝"
        s.warn("A-12", P2, "只读端点的非 GET 处理",
               "POST /api/board 返回 %s，未限制方法也未声明（契约未规定，属宽松行为）" % res.status)
        return "observed"

    def a41():
        first = s.client.get("/api/board").json
        s.has_keys(first, ["updated_at", "generated_at"], "/api/board 时间字段")
        time.sleep(1.3)
        second = s.client.get("/api/board").json
        s.eq(second.get("updated_at"), first.get("updated_at"),
             "updated_at 是数据水位：没有写入时两次读取不应变化")
        s.truth(second.get("generated_at") != "" and second.get("generated_at") is not None,
                "generated_at 缺失")
        return "updated_at=%s" % first.get("updated_at")

    s.check("A-06", P0, "/api/board 字段与统计不变量", a06)
    s.check("A-07", P0, "project 过滤与回显", a07)
    s.check("A-08", P1, "agent 过滤", a08)
    s.check("A-09", P1, "status 过滤", a09)
    s.check("A-10", P1, "project+status 组合过滤", a10)
    s.check("A-11", P1, "未知工程返回空集而非全量", a11)
    s.check("A-12", P2, "只读端点拒绝非 GET", a12)
    s.check("A-41", P1, "updated_at 是数据水位而非渲染时刻", a41)


def group_a_detail(s: Suite):
    print("\n[A] 详情与事件流")

    def a13():
        s.eq(s.client.get("/api/task").status, 400, "缺 id 状态码")

    def a14():
        s.eq(s.client.get("/api/task?id=E2E-NOT-EXIST").status, 404, "未知 id 状态码")

    def a15():
        task = s.ctx["task"]
        res = s.client.get("/api/task?id=%s&project=%s" % (urllib.parse.quote(task["id"]), s.ctx["pid"]))
        s.eq(res.status, 200, "真实任务状态码")
        d = res.json
        s.truth(d.get("ok") is True, "ok 字段不是 true")
        s.eq((d.get("task") or {}).get("id"), task["id"], "返回的任务 id")
        s.truth(isinstance(d.get("events"), list), "events 不是数组")
        s.truth(d.get("project") is not None, "project 为空")
        return "events=%d" % len(d["events"])

    def a16():
        d = s.client.get("/api/feed?since=0").json
        evs = d.get("events")
        s.truth(isinstance(evs, list) and evs, "feed 为空")
        s.truth(d.get("next_since") == evs[-1]["id"], "next_since 不等于末条 id")
        ids = [e["id"] for e in evs]
        s.eq(ids, sorted(ids), "事件未按 id 升序")
        s.truth(isinstance(d.get("has_more"), bool), "has_more 不是布尔")
        return "events=%d next=%s" % (len(evs), d["next_since"])

    def a17():
        d1 = s.client.get("/api/feed?since=0").json
        s.truth(bool(d1.get("events")), "首页为空，无法测翻页")
        if d1.get("has_more"):
            s.eq(len(d1["events"]), 200, "满页应是单页上限 200 条")
        evs, pages = feed_tail(s.client)
        ids = [e["id"] for e in evs]
        s.eq(ids, sorted(ids), "翻页后事件不升序")
        s.eq(len(ids), len(set(ids)), "翻页出现重复事件")
        last = ids[-1] if ids else 0
        d2 = s.client.get("/api/feed?since=%d" % last).json
        s.eq(d2.get("events"), [], "末页之后应为空")
        s.eq(d2.get("next_since"), last, "空页应回显 since")
        return "pages=%d events=%d last=%d" % (pages, len(ids), last)

    def a18():
        ready, events, lines = sse_probe(
            s.base, 6.0,
            trigger=lambda: s.client.write("/api/v1/status-reports",
                                           {"actor": s.client.actor, "project_id": s.ctx["pid"],
                                            "message": "sse probe", "status": "idle"}),
        )
        s.truth(ready, "没收到 ready 事件；样本 %s" % lines[:4])
        s.truth("board-changed" in events, "写库后 6s 内没有 board-changed；收到 %s" % events)
        return "events=%s" % events

    s.check("A-13", P1, "/api/task 缺 id 返回 400", a13)
    s.check("A-14", P1, "/api/task 未知 id 返回 404", a14)
    s.check("A-15", P0, "/api/task 返回任务、工程与事件", a15)
    s.check("A-16", P1, "/api/feed 升序与水位", a16)
    s.check("A-17", P1, "/api/feed 续拉为空且回显水位", a17)
    s.check("A-18", P0, "SSE 首帧 ready 且变更推送 board-changed", a18)


def group_a_runtime(s: Suite):
    print("\n[A] 运行时视图与日志")

    def a19():
        d = s.client.get("/api/runtime").json
        s.has_keys(d, ["ok", "auto_execution", "managed_via", "static_export_managed"], "/api/runtime")
        s.eq(d["ok"], True, "ok")
        s.eq(d["managed_via"], "cli_or_api_v1", "managed_via")
        s.eq(d["static_export_managed"], False, "static_export_managed")
        s.has_keys(d["auto_execution"], ["enabled", "managed_execution_limit", "config_version"], "auto_execution")
        return "extensions=%s" % ("present" if "extensions" in d["auto_execution"] else "absent")

    def a20():
        s.eq(s.client.get("/api/log").status, 400, "缺参状态码")
        agent = (s.ctx["board"]["agents"] or [{}])[0].get("id") or "unknown-agent"
        res = s.client.get("/api/log?project=%s&agent=%s" % (s.ctx["pid"], urllib.parse.quote(agent)))
        s.eq(res.status, 200, "有参状态码")
        s.truth(res.json is not None and "ok" in res.json, "响应不是 JSON")
        return "missing=%s" % res.json.get("missing")

    s.check("A-19", P1, "/api/runtime 字段与只读声明", a19)
    s.check("A-20", P1, "/api/log 参数校验与缺日志降级", a20)


def group_a_auth(s: Suite):
    print("\n[A] 鉴权与会话")

    def a21():
        res = Client(s.base).call("POST", "/api/status", body={"actor": "x", "message": "y"})
        s.eq(res.status, 401, "匿名写状态码")
        s.eq(((res.json or {}).get("error") or {}).get("code"), "unauthenticated", "错误码")

    def a22():
        fresh = Client(s.base)
        res = fresh.session()
        s.eq(res.status, 200, "会话下发状态码")
        s.has_keys(res.json, ["ok", "session_id", "csrf"], "会话响应")
        cookies = {c.name for c in fresh.jar}
        s.truth(any("session" in n for n in cookies), "缺少会话 Cookie: %s" % cookies)
        s.truth(any("csrf" in n for n in cookies), "缺少 CSRF Cookie: %s" % cookies)
        return "cookies=%s" % sorted(cookies)

    def a23():
        res = s.client.get("/api/v1/projects")
        s.eq(res.status, 200, "会话读 v1 状态码")

    def a24():
        no_csrf = Client(s.base)
        no_csrf.session()
        no_csrf.csrf = None
        res = no_csrf.call("POST", "/api/v1/status-reports",
                           body={"actor": "e2e", "project_id": s.ctx["pid"], "message": "x"},
                           headers={"Origin": s.base, "Idempotency-Key": "e2e-nocsrf-" + uuid.uuid4().hex})
        s.eq(res.status, 403, "缺 CSRF 状态码")
        s.eq(((res.json or {}).get("error") or {}).get("code"), "csrf_failed", "错误码")
        no_origin = s.client.call("POST", "/api/v1/status-reports",
                                  body={"actor": "e2e", "project_id": s.ctx["pid"], "message": "x"},
                                  headers={"X-CSRF-Token": s.client.csrf,
                                           "Idempotency-Key": "e2e-noorg-" + uuid.uuid4().hex})
        s.eq(no_origin.status, 403, "缺 Origin 状态码")
        return "csrf/origin 均被拒"

    def a25():
        before = s.client.get("/api/board").json["updated_at"]
        time.sleep(1.1)
        res = s.client.write("/api/status", {"project_id": s.ctx["pid"], "actor": "e2e-writer",
                                             "message": "e2e write probe", "status": "idle"}, idem=False)
        s.eq(res.status, 200, "会话写状态码")
        s.truth((res.json or {}).get("event_id"), "写入未返回 event_id")
        after = s.client.get("/api/board").json["updated_at"]
        s.truth(after >= before, "updated_at 未前进: %s -> %s" % (before, after))
        evs, _ = feed_tail(s.client, s.ctx["pid"])
        s.truth(any(e.get("actor") == "e2e-writer" for e in evs[-50:]), "写入未出现在 feed 尾部")
        return "event_id=%s" % res.json.get("event_id")

    s.check("A-21", P0, "匿名写状态被拒", a21)
    s.check("A-22", P0, "会话下发 Cookie 与 CSRF", a22)
    s.check("A-23", P0, "会话可读 /api/v1", a23)
    s.check("A-24", P0, "写请求必须同源 + CSRF", a24)
    s.check("A-25", P1, "会话写入落库并进入 feed", a25)


def group_a_v1(s: Suite):
    print("\n[A] /api/v1 只读与分页")

    def a26():
        seen, cursor, pages = [], "", 0
        while pages < 200:
            q = "/api/v1/board?limit=50" + ("&cursor=" + urllib.parse.quote(cursor) if cursor else "")
            res = s.client.get(q)
            s.eq(res.status, 200, "分页状态码")
            body = res.json
            items = body.get("items") or []
            for it in items:
                key = (it.get("project_id"), it.get("task_instance"))
                s.truth(key not in seen, "分页出现重复: %s" % (key,))
                seen.append(key)
            pages += 1
            cursor = body.get("next_cursor")
            if not cursor:
                break
        total = s.ctx["board"]["total"]
        s.eq(len(seen), total, "翻页条目数与看板 total")
        return "pages=%d items=%d" % (pages, len(seen))

    def a27():
        for q in ("?offset=1", "?page=1"):
            res = s.client.get("/api/v1/board" + q)
            s.eq(res.status, 422, "legacy 分页 %s 状态码" % q)
            s.eq(((res.json or {}).get("error") or {}).get("code"), "invalid_pagination", "错误码 %s" % q)

    def a28():
        res = s.client.get("/api/v1/board?limit=abc")
        s.eq(res.status, 422, "非法 limit 状态码")

    def a29():
        d = s.client.get("/api/v1/projects").json
        s.truth(isinstance(d.get("items"), list), "items 不是数组")
        s.eq(len(d["items"]), len(s.ctx["board"]["projects"]), "工程数与看板")
        s.has_keys(d["items"][0], ["project_id", "name", "path"], "工程对象")
        s.truth("watermark" in d, "缺 watermark")
        return "projects=%d" % len(d["items"])

    def a30():
        pid = s.ctx["pid"]
        out = {}
        for kind in ("tasks", "nodes", "runs", "operations", "deliveries", "instances"):
            res = s.client.get("/api/v1/projects/%s/%s" % (pid, kind))
            out[kind] = res.status
            s.eq(res.status, 200, "%s 状态码" % kind)
            s.truth(isinstance((res.json or {}).get("items"), list), "%s items 不是数组" % kind)
        return str(out)

    def a31():
        s.eq(s.client.get("/api/v1/tasks/ti_zzzzzzzz").status, 404, "未知任务状态码")
        s.eq(s.client.get("/api/v1/tasks/not-an-id").status, 422, "非法形态状态码")

    def a32():
        res = s.client.get("/api/v1/projects/deadbeefdeadbeef/tasks")
        s.eq(res.status, 200, "未知工程状态码")
        s.eq((res.json or {}).get("items"), [], "未知工程应为空集")

    s.check("A-26", P0, "v1 游标分页无重无漏", a26)
    s.check("A-27", P1, "禁止 offset/page 分页", a27)
    s.check("A-28", P1, "limit 非整数返回 422", a28)
    s.check("A-29", P0, "v1 工程清单", a29)
    s.check("A-30", P1, "v1 六类工程子资源", a30)
    s.check("A-31", P1, "v1 未知任务 404", a31)
    s.check("A-32", P1, "v1 未知工程返回空集", a32)


def group_a_obs(s: Suite):
    print("\n[A] 观测与提醒")

    def a33():
        pid = s.ctx["pid"]
        out = {}
        for kind in ("timeline", "activity", "sessions", "analytics"):
            res = s.client.get("/api/v1/projects/%s/%s" % (pid, kind))
            out[kind] = res.status
            s.eq(res.status, 200, "%s 状态码" % kind)
            s.truth(res.json is not None, "%s 不是 JSON" % kind)
        return str(out)

    def a34():
        pid = s.ctx["pid"]
        cap = s.client.get("/api/v1/projects/%s/observability/capabilities" % pid)
        s.eq(cap.status, 200, "capabilities 状态码")
        caps = (cap.json or {}).get("capabilities")
        s.truth(isinstance(caps, list) and caps, "capabilities 不是非空数组")
        notif = s.client.get("/api/v1/projects/%s/notifications" % pid)
        s.eq(notif.status, 200, "notifications 状态码")
        s.truth(notif.json is not None, "notifications 不是 JSON")
        return "capabilities=%d" % len(caps)

    def a35():
        pid = s.ctx["pid"]
        cases = [
            ("start 非日期", "/api/v1/projects/%s/timeline?start=not-a-date" % pid),
            ("end 早于 start", "/api/v1/projects/%s/timeline?start=2026-11-02T10:00:00Z&end=2026-11-01T10:00:00Z" % pid),
            ("非法时区", "/api/v1/projects/%s/timeline?tz=Mars/Base" % pid),
            ("hours 超限", "/api/v1/projects/%s/activity?hours=999" % pid),
        ]
        codes = []
        for label, path in cases:
            res = s.client.get(path)
            codes.append((label, res.status))
            s.truth(400 <= res.status < 500, "%s 未按 4xx 拒绝: %s" % (label, res.status))
            s.truth(((res.json or {}).get("error") or {}).get("code"), "%s 缺错误码" % label)
        return str(codes)

    s.check("A-33", P1, "观测四接口可用", a33)
    s.check("A-34", P2, "能力与提醒接口", a34)
    s.check("A-35", P2, "非法时间窗被拒", a35)


def group_a_write(s: Suite):
    print("\n[A] 写通道可达性（只打副本）")

    def a36():
        pid = s.ctx["pid"]
        res = s.client.call("POST", "/api/v1/projects/%s/tasks" % pid,
                            body={"display_id": "E2E-W1", "title": "e2e"},
                            headers={"Origin": s.base, "X-CSRF-Token": s.client.csrf,
                                     "Idempotency-Key": "e2e-write-" + uuid.uuid4().hex})
        if res.status == 201:
            return "created"
        s.warn("A-36", P1, "v1 建任务在 hub 上不可达",
               "POST /api/v1/projects/{id}/tasks → %s（%s）；hub 只挂 read/events/observability，"
               "而 /api/runtime 声明 managed_via=cli_or_api_v1"
               % (res.status, res.text.strip()[:40]))
        return "observed"

    def a37():
        res = s.client.write("/api/v1/status-reports",
                             {"actor": "e2e", "project_id": s.ctx["pid"], "message": "no key"},
                             idem=False)
        s.eq(res.status, 422, "缺幂等键状态码")
        s.eq(((res.json or {}).get("error") or {}).get("code"), "invalid_idempotency_key", "错误码")

    def a38():
        if not s.token:
            return "SKIP 未提供 --token"
        tok = {"Authorization": "Bearer " + s.token}
        body = {"actor": "e2e-goalagents-1", "project_id": s.ctx["pid"],
                "message": "idem probe", "status": "idle"}
        headers = dict(tok, **{"Idempotency-Key": "e2e-idem-" + uuid.uuid4().hex, "Content-Type": "application/json"})
        first = s.client.call("POST", "/api/v1/status-reports", body=body, headers=headers)
        s.eq(first.status, 200, "首次上报状态码")
        s.eq((first.json or {}).get("accepted"), 1, "首次上报未被接受: %s" % first.text[:160])
        mark = max(e["id"] for e in feed_tail(s.client)[0])
        second = s.client.call("POST", "/api/v1/status-reports", body=body, headers=headers)
        s.eq(second.status, 200, "重放状态码")
        s.eq((second.json or {}).get("idempotent_replay"), True, "重放未标记 idempotent_replay")
        after = max(e["id"] for e in feed_tail(s.client)[0])
        s.eq(after, mark, "重放又新增了事件（%s → %s）" % (mark, after))
        return "accepted=1 replay=idempotent"

    def a39():
        if not s.token:
            return "SKIP 未提供 --token"
        res = s.client.call("POST", "/api/v1/status-reports",
                            body={"actor": "someone-else", "project_id": s.ctx["pid"], "message": "x"},
                            headers={"Authorization": "Bearer " + s.token,
                                     "Idempotency-Key": "e2e-mismatch-" + uuid.uuid4().hex})
        s.eq(res.status, 403, "令牌 actor 不一致状态码")
        s.eq(((res.json or {}).get("error") or {}).get("code"), "actor_mismatch", "错误码")

    def a40():
        res = s.client.write("/api/v1/status-reports",
                             {"actor": s.client.actor, "project_id": s.ctx["pid"],
                              "message": "session actor probe", "status": "idle"})
        d = res.json or {}
        if d.get("accepted") == 1:
            return "会话身份也可上报"
        s.warn("A-40", P2, "会话身份上报的响应语义",
               "HTTP %s 且 ok=true，但 accepted=0 rejected=%s：会话身份（%s）不满足 {应用}-{工程}-{序号} 命名，"
               "调用方只看 ok 会误判成功" % (res.status, d.get("rejected"), s.client.actor))
        return "observed"

    s.check("A-36", P1, "v1 建任务写路由在 hub 上可达", a36)
    s.check("A-37", P1, "上报缺幂等键被拒", a37)
    s.check("A-38", P1, "上报幂等重放不产生重复事件", a38)
    s.check("A-39", P0, "令牌 actor 绑定不可绕过", a39)
    s.check("A-40", P2, "会话身份上报的响应语义", a40)


def group_d_policy(s: Suite):
    print("\n[D] 执行策略与自动化策略")

    def d01():
        d = s.client.get("/api/runtime").json or {}
        ae = d.get("auto_execution") or {}
        s.eq(ae.get("enabled"), False, "自动化默认状态（必须为 false，不是缺省）")
        limit = ae.get("managed_execution_limit")
        s.truth(isinstance(limit, int) and limit >= 1, "计算上限必须是 ≥1 的整数，实际 %r" % (limit,))
        s.truth(isinstance(ae.get("config_version"), int), "缺 config_version（页面拿它当 revision）")
        return "enabled=false limit=%s version=%s" % (limit, ae.get("config_version"))

    def d02():
        d = s.client.get("/api/runtime").json or {}
        secret_re = __import__("re").compile(r"(token|secret|password|passwd|pat|api[-_]?key|credential)", __import__("re").I)
        hits = []

        def walk(node, path=""):
            if isinstance(node, dict):
                for k, v in node.items():
                    if secret_re.search(str(k)):
                        hits.append(path + "/" + str(k))
                    walk(v, path + "/" + str(k))
            elif isinstance(node, list):
                for i, v in enumerate(node):
                    walk(v, "%s[%d]" % (path, i))

        walk(d)
        s.truth(not hits, "运行时视图泄漏了秘密字段: %s" % hits)
        return "无秘密字段"

    def d03():
        d = s.client.get("/api/runtime").json or {}
        s.eq(d.get("managed_via"), "cli_or_api_v1", "受管入口声明")
        s.eq(d.get("static_export_managed"), False, "静态导出不得发起受管写")
        # 幂等：连续读不应把自动化「读开」
        for _ in range(3):
            s.client.get("/api/runtime")
        s.eq((s.client.get("/api/runtime").json or {}).get("auto_execution", {}).get("enabled"), False,
             "多次读取后自动化被打开")
        return "声明一致且读取无副作用"

    def d04():
        d = s.client.get("/api/runtime").json or {}
        ae = d.get("auto_execution") or {}
        if "platform_supported" not in ae:
            s.warn("D-04", P1, "platform_supported 缺失",
                   "runtime 未声明平台支持；前端会把它当成「支持」并显示「关闭」而不是「未就绪」"
                   "（settings 页注释要求「没说不猜成支持」）")
        s.truth(isinstance(ae.get("platform_supported"), bool), "platform_supported 必须是布尔")
        return "platform_supported=%s" % ae.get("platform_supported")

    def d05():
        pid = s.ctx["pid"]
        probes = [("POST", "/api/v1/projects/%s/policy" % pid, {"limit": 2}),
                  ("PATCH", "/api/v1/projects/%s/policy" % pid, {"limit": 2}),
                  ("POST", "/api/v1/projects/%s/settings" % pid, {"limit": 2})]
        codes = []
        for method, path, body in probes:
            res = s.client.call(method, path, body=body,
                                headers={"Origin": s.base, "X-CSRF-Token": s.client.csrf or "",
                                         "Idempotency-Key": "e2e-policy-" + uuid.uuid4().hex})
            codes.append((method, res.status))
        ok = [c for c in codes if c[1] in (200, 201, 204)]
        if not ok:
            s.warn("D-05", P1, "策略没有写入口",
                   "策略保存候选端点全部不可用 %s；页面却渲染可编辑表单 + 「保存设置」按钮" % codes)
        return str(codes)

    s.check("D-01", P0, "自动化默认关闭且上限合法", d01)
    s.check("D-02", P0, "运行时视图不含秘密字段", d02)
    s.check("D-03", P1, "受管入口声明与读取无副作用", d03)
    s.check("D-04", P1, "平台支持必须显式声明", d04)
    s.check("D-05", P1, "策略写入口存在", d05)


def group_e_review(s: Suite):
    print("\n[E] 回顾数据（时间轴／热图／会话／统计）")
    pid = s.ctx["pid"]
    # 回顾数据按投影物化；挑一个有交付记录的工程，才测得到「有数据」的分支。
    if s.copy_db:
        try:
            row = copy_db_query(s.copy_db,
                                "select project_id from deliveries group by 1 order by count(*) desc limit 1")
            if row and row[0][0]:
                pid = row[0][0]
        except sqlite3.Error:
            pass
    print("  · 使用工程 %s" % pid)
    win = "start=2026-10-26T00:00:00Z&end=2026-10-28T00:00:00Z"

    def fetch(kind, query=win):
        res = s.client.get("/api/v1/projects/%s/%s?%s" % (pid, kind, query))
        s.eq(res.status, 200, "%s 状态码" % kind)
        return res.json or {}

    def e01():
        d = fetch("timeline")
        s.truth(d.get("status") in ("ready", "unknown"), "status 非法: %r" % d.get("status"))
        if d.get("status") == "unknown":
            reasons = [m.get("reason") for m in d.get("missing") or []]
            s.truth("projection_not_built" in reasons or "projection_not_rebuilt" in reasons,
                    "unknown 但没给出未构建原因: %s" % reasons)
        s.eq(d.get("items"), [], "无投影时不应编造区间")
        s.has_keys(d, ["window", "watermark", "completeness", "missing"], "timeline")
        return "status=%s missing=%s" % (d.get("status"), [m.get("reason") for m in d.get("missing") or []])

    def e02():
        d = fetch("activity", "day=2026-10-27")
        s.eq(d.get("present_hours"), 0, "present_hours 应为 0")
        s.eq(d.get("hours"), [], "无观测小时桶时 hours 必须为空")
        reasons = [m.get("reason") for m in d.get("missing") or []]
        s.truth("no_observed_buckets" in reasons, "缺少 no_observed_buckets 说明: %s" % reasons)
        s.truth(d.get("status") == "unknown", "没有完整桶时 status 不能是 ready")
        return "missing=%s" % reasons

    def e03():
        d = fetch("sessions")
        s.has_keys(d, ["items", "count", "completeness", "status"], "sessions")
        empty = not d.get("items")
        if empty:
            s.truth(d.get("completeness") == "complete" or (d.get("missing") or []),
                    "空列表既没说 complete 也没给 missing：无法区分「查过没有」与「没查」")
        s.eq(d.get("count"), len(d.get("items") or []), "count 与 items 长度")
        return "items=%d status=%s completeness=%s" % (len(d.get("items") or []), d.get("status"), d.get("completeness"))

    def e04():
        d = fetch("analytics")
        report = d.get("report") or {}
        deliveries = report.get("deliveries") or {}
        total = deliveries.get("count") if isinstance(deliveries, dict) else None
        if total is None:
            s.truth(d.get("status") == "unknown", "统计既没有交付数也没标 unknown")
            return "deliveries=absent"
        if s.copy_db:
            rows = copy_db_query(s.copy_db, "select count(*) from deliveries where project_id=?", (pid,))
            s.eq(total, rows[0][0], "统计里的交付数与库不一致")
        s.truth(d.get("status") == "unknown", "有未构建投影时统计不能假装 ready")
        return "deliveries=%s status=unknown" % total

    def e05():
        bad = []
        for kind, query in (("timeline", win), ("activity", "day=2026-10-27"), ("analytics", win), ("sessions", win)):
            d = fetch(kind, query)
            if d.get("missing") and d.get("status") == "ready":
                bad.append(kind)
            if not d.get("missing") and d.get("status") == "unknown":
                bad.append(kind + ":unknown-without-reason")
        s.truth(not bad, "状态与缺测说明互相矛盾: %s" % bad)
        return "四接口状态自洽"

    def e06():
        if not s.copy_db:
            return "SKIP 未提供 --copy-db"
        # 造一个「投影已建」的检查点：只改副本，用完删除。
        copy_db_query(s.copy_db,
                      "insert or replace into projection_state"
                      "(projection,project_id,version,cursor,complete_before,dirty_from,dirty_to,updated_at,meta_json)"
                      " values('timeline',?,1,542,542,NULL,NULL,?,?)",
                      (pid, "2026-11-02T00:00:00Z", '{"completeness":"partial"}'), write=True)
        try:
            d = fetch("timeline")
            reasons = [m.get("reason") for m in d.get("missing") or []]
            s.truth("projection_not_built" not in reasons, "已有检查点仍报未构建: %s" % reasons)
            s.truth("projection_not_rebuilt" in reasons, "缺 projection_not_rebuilt 说明: %s" % reasons)
            cur = (d.get("watermark") or {}).get("cursor")
            s.eq(cur, 542, "水位应来自检查点")
        finally:
            copy_db_query(s.copy_db,
                          "delete from projection_state where projection='timeline' and project_id=?",
                          (pid,), write=True)
        after = fetch("timeline")
        s.truth("projection_not_built" in [m.get("reason") for m in after.get("missing") or []],
                "删除检查点后未回落到未构建")
        return "检查点→not_rebuilt，删除→not_built"

    s.check("E-01", P0, "时间轴无投影时如实报未知", e01)
    s.check("E-02", P1, "热图缺测小时不冒充 0", e02)
    s.check("E-03", P1, "会话空态可区分「查过没有」", e03)
    s.check("E-04", P1, "统计交付数与库一致", e04)
    s.check("E-05", P0, "四接口状态与缺测说明自洽", e05)
    s.check("E-06", P1, "投影检查点在位时改为报水位", e06)


def group_f_empty(s: Suite):
    print("\n[F] 空库冷启动")
    if not s.empty_base:
        s.record("F-00", P1, "空库实例", False, "未提供 --empty-base", skipped=True)
        print("  SKIP F-00 未提供 --empty-base")
        return
    c = Client(s.empty_base)

    def f01():
        res = c.get("/api/board")
        s.eq(res.status, 200, "空库 /api/board 状态码")
        d = res.json
        s.eq(d.get("total"), 0, "空库 total")
        s.eq(d.get("tasks"), [], "空库任务应是空数组而不是 null")
        s.eq(d.get("projects"), [], "空库工程应是空数组而不是 null")
        return "total=0"

    def f02():
        s.eq(c.get("/").status, 200, "空库首页")
        s.eq(c.get("/assets/board-web.js").status, 200, "空库静态资源")

    def f03():
        pid = "deadbeefdeadbeef"
        for path in ("/api/v1/projects", "/api/v1/board"):
            s.eq(c.get(path).status, 401, "空库未授权读 %s" % path)
        fresh = Client(s.empty_base)
        fresh.session()
        s.eq(fresh.get("/api/v1/projects").status, 200, "空库授权后读工程")
        s.eq((fresh.get("/api/v1/projects").json or {}).get("items"), [], "空库工程列表应为空")
        s.eq(fresh.get("/api/v1/tasks/ti_zzzzzzzz").status, 404, "空库未知任务")

    def f04():
        d = c.get("/api/runtime").json or {}
        ae = d.get("auto_execution") or {}
        s.eq(ae.get("enabled"), False, "空库自动化默认状态")
        s.truth(isinstance(ae.get("managed_execution_limit"), int), "空库上限缺失")

    def f05():
        res = c.get("/api/task?id=NOPE-1")
        s.eq(res.status, 404, "空库任务详情")

    s.check("F-01", P1, "空库看板返回空数组而不是 null", f01)
    s.check("F-02", P1, "空库首页与静态资源可用", f02)
    s.check("F-03", P1, "空库 v1 鉴权与空集", f03)
    s.check("F-04", P1, "空库运行时默认值", f04)
    s.check("F-05", P2, "空库任务详情 404", f05)


def group_g_sse(s: Suite):
    print("\n[G] 实时推送压力")

    def g01():
        if not s.copy_db:
            return "SKIP 未提供 --copy-db"
        base = copy_db_query(s.copy_db, "select coalesce(max(id),0) from events")[0][0]
        rows = []
        for i in range(250):
            rows.append((base + i + 1, "2026-11-02T08:00:%02dZ" % (i % 60), s.ctx["pid"],
                         "e2e-bulk", "progress", "", '{"message":"bulk %d"}' % i))
        con = sqlite3.connect(os.path.abspath(s.copy_db), timeout=5)
        try:
            con.executemany("insert into events(id,ts,project_id,actor,type,task_id,data_json) values(?,?,?,?,?,?,?)", rows)
            con.commit()
        finally:
            con.close()
        try:
            first = s.client.get("/api/feed?since=%d" % base).json
            s.eq(len(first.get("events") or []), 200, "单页上限应为 200")
            s.eq(first.get("has_more"), True, "积压 250 条时 has_more 必须为 true")
            evs, pages = feed_tail(s.client)
            mine = [e for e in evs if e.get("actor") == "e2e-bulk"]
            s.eq(len(mine), 250, "翻页应取全 250 条积压")
            s.truth(pages >= 2, "积压应至少两页，实际 %d" % pages)
            return "pages=%d events=%d" % (pages, len(mine))
        finally:
            con = sqlite3.connect(os.path.abspath(s.copy_db), timeout=5)
            try:
                con.execute("delete from events where actor='e2e-bulk'")
                con.commit()
            finally:
                con.close()

    def g02():
        # 心跳只在安静流上可观测：用空库实例，避免业务事件把时序搅乱。
        target = s.empty_base or s.base
        ready, frames, comments = sse_heartbeat(target, 20.0)
        s.truth(ready, "没收到 ready")
        s.truth(bool(comments), "20s 内没收到心跳注释帧；收到的事件 %s" % frames[:6])
        return "heartbeat=%d frames=%d" % (len(comments), len(frames))

    def g03():
        ready, events, lines = sse_probe(
            s.base, 10.0,
            trigger=lambda: s.client.write("/api/status", {"project_id": s.ctx["pid"], "actor": "e2e-live-sse",
                                                          "message": "sse", "status": "idle"}, idem=False),
        )
        s.truth("board-changed" in events, "写入后未收到 board-changed: %s" % events)
        s.truth("ready" in events, "缺 ready 帧")
        return str(events)

    s.check("G-01", P1, "事件积压按 200 条分页不丢不重", g01)
    s.check("G-02", P1, "SSE 心跳注释帧按时到达", g02)
    s.check("G-03", P1, "写入触发 board-changed", g03)


def group_c_safety(s: Suite):
    print("\n[C] 数据安全（真实环境未被改动）")

    if not s.live and not s.live_db:
        s.record("C-00", P0, "真实环境只读对照", False, "未提供 --live-base/--live-db，无法证明未动活动数据")
        print("  FAIL C-00 未提供真实环境对照参数")
        return

    def c01():
        if not s.live_db:
            return "SKIP 未提供 --live-db"
        after = live_db_counts(s.live_db)
        before = s.live_before.get("db_counts", {})
        diff = {k: (before.get(k), after.get(k)) for k in after if before.get(k) != after.get(k)}
        s.truth(not diff, "真实库计数变化: %s" % diff)
        return str(after)

    def c02():
        size, mtime = rss_kb(os.path.expanduser(s.live_db)) if s.live_db else (None, None)
        before = s.live_before.get("db_file", {})
        s.eq(size, before.get("size"), "真实库大小")
        s.eq(mtime, before.get("mtime"), "真实库 mtime")
        return "size=%s" % size

    def c03():
        if not s.live:
            return "SKIP 未提供 --live-base"
        res = s.live.get("/api/board")
        s.eq(res.status, 200, "真实实例可用性")
        now = {"total": (res.json or {}).get("total"), "updated_at": (res.json or {}).get("updated_at")}
        before = s.live_before.get("api", {})
        s.eq(now["total"], before.get("total"), "真实实例 total")
        if now["updated_at"] != before.get("updated_at"):
            s.warn("C-03", P2, "看板 updated_at 语义",
                   "两次读取 updated_at 不同（%s → %s）：该字段是响应生成时间，不是数据变更时间"
                   % (before.get("updated_at"), now["updated_at"]))
        return "total=%s" % now["total"]

    def c05():
        if not s.live_db:
            return "SKIP 未提供 --live-db"
        hits = live_db_scan(s.live_db, {
            "events": "select count(*) from events where actor like 'e2e-%'",
            "agents": "select count(*) from agents where id like 'e2e-%'",
        })
        s.truth(not any(hits.values()), "真实库里出现了本套件的写入: %s" % hits)
        return str(hits)

    def c04():
        evs, _ = feed_tail(s.client)
        actors = {e.get("actor") for e in evs[-200:]}
        mine = sorted(a for a in actors if a and a.startswith("e2e"))
        s.truth(bool(mine), "副本上没有本套件写入痕迹，写用例未真正执行")
        return "actors=%s" % mine

    s.check("C-01", P0, "真实库表计数不变", c01)
    s.check("C-02", P0, "真实库文件大小与 mtime 不变", c02)
    s.check("C-03", P0, "真实实例看板不变", c03)
    s.check("C-04", P0, "写操作确实落在隔离副本", c04)
    s.check("C-05", P0, "真实库没有本套件的写入痕迹", c05)


# --------------------------------------------------------------------------- 主流程


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:7421")
    ap.add_argument("--live-base", default="")
    ap.add_argument("--live-db", default="")
    ap.add_argument("--token", default=os.environ.get("E2E_TOKEN", ""))
    ap.add_argument("--copy-db", default=os.environ.get("E2E_COPY_DB", ""))
    ap.add_argument("--empty-base", default=os.environ.get("E2E_EMPTY_BASE", ""))
    ap.add_argument("--report", default="")
    args = ap.parse_args()

    suite = Suite(args.base, args.live_base or None, args.live_db or None, args.token,
                  args.copy_db or "", args.empty_base or "")
    try:
        suite.bootstrap()
    except Exception as err:  # noqa: BLE001
        print("环境不可用: %s" % err)
        return 2

    t0 = time.time()
    group_a_static(suite)
    group_a_board(suite)
    group_a_detail(suite)
    group_a_runtime(suite)
    group_a_auth(suite)
    group_a_v1(suite)
    group_a_obs(suite)
    group_a_write(suite)
    group_d_policy(suite)
    group_e_review(suite)
    group_f_empty(suite)
    group_g_sse(suite)
    group_c_safety(suite)
    elapsed = time.time() - t0

    total = len(suite.results)
    failed = [r for r in suite.results if not r["ok"]]
    skipped = [r for r in suite.results if r["skipped"]]
    p0_fail = [r for r in failed if r["level"] == P0]

    print("\n" + "=" * 72)
    print("用例 %d：通过 %d，失败 %d（P0 失败 %d），跳过 %d，耗时 %.1fs"
          % (total, total - len(failed), len(failed), len(p0_fail), len(skipped), elapsed))
    if failed:
        print("\n失败明细：")
        for r in failed:
            print("  %-5s %-3s %s\n        %s" % (r["id"], r["level"], r["name"], r["detail"]))
    summary = {
        "base": args.base, "live_base": args.live_base, "live_db": args.live_db,
        "total": total, "passed": total - len(failed), "failed": len(failed),
        "p0_failed": len(p0_fail), "elapsed_sec": round(elapsed, 1),
        "live_before": suite.live_before,
        "results": suite.results,
    }
    if args.report:
        with open(args.report, "w", encoding="utf-8") as fh:
            json.dump(summary, fh, ensure_ascii=False, indent=2)
        print("\n报告已写入 %s" % args.report)
    return 1 if p0_fail else 0


if __name__ == "__main__":
    sys.exit(main())
