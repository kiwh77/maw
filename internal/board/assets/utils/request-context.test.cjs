// VN-703 公共请求与页面上下文的验证。
//
// 覆盖任务卡 Acceptance：
//   1. CSRF、revision、202 轮询、结构化错误统一处理，且不把「受理」当「完成」。
//   2. 切换工程与并发请求不覆盖新上下文；未知状态与刷新恢复可见。
// 契约依据：docs/contracts/vnext/http/http-api-contract-v1.md §3–§7。
//
// 运行：node --test internal/board/assets/utils/request-context.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const RC_URL = pathToFileURL(path.join(__dirname, "request-context.mjs")).href;
const API_URL = pathToFileURL(path.join(__dirname, "../api/board-api.mjs")).href;

const load = () => import(RC_URL);
const loadApi = () => import(API_URL);

// ---------- 测试替身 ----------

function jsonResponse(status, body, headers = {}) {
  const normalized = {};
  for (const [k, v] of Object.entries(headers)) normalized[k.toLowerCase()] = String(v);
  return {
    status,
    headers: { get: name => normalized[String(name).toLowerCase()] ?? null },
    text: async () => (body === undefined ? "" : JSON.stringify(body)),
  };
}

function recordingFetch(handler) {
  const calls = [];
  const fn = async (url, init = {}) => {
    const call = {
      url,
      method: (init.method || "GET").toUpperCase(),
      headers: init.headers || {},
      body: init.body ? JSON.parse(init.body) : undefined,
    };
    calls.push(call);
    const response = await handler(call, calls.length - 1);
    if (response instanceof Error) throw response;
    return response;
  };
  fn.calls = calls;
  return fn;
}

function makeContext(fetchImpl, extra = {}) {
  const clock = { t: 0, sleeps: [] };
  const options = {
    fetchImpl,
    now: () => clock.t,
    sleep: async ms => {
      clock.sleeps.push(ms);
      clock.t += ms;
    },
    cookieReader: () => extra.cookie ?? "",
    ...extra,
  };
  delete options.cookie;
  return { clock, options };
}

const OP = (id, status, extra = {}) => ({ operation_id: id, status, ...extra });

// ---------- 1. 授权：Bearer 与 CSRF 双提交 ----------

test("读请求走 Bearer，不带 CSRF 头，并记录 revision", async () => {
  const { createRequestContext } = await load();
  const fetchImpl = recordingFetch(() =>
    jsonResponse(200, { ok: true, task: { id: "ti_a" }, revision: 7 }, { ETag: '"rev-7"' })
  );
  const { options } = makeContext(fetchImpl, { token: "maw_pat_abc" });
  const ctx = createRequestContext(options);

  const result = await ctx.read("/api/v1/tasks/ti_a");

  assert.equal(result.stale, false);
  assert.equal(result.revision, 7);
  assert.equal(fetchImpl.calls[0].headers.Authorization, "Bearer maw_pat_abc");
  assert.equal(fetchImpl.calls[0].headers["X-CSRF-Token"], undefined);
  assert.equal(ctx.revisionOf("/api/v1/tasks/ti_a"), 7);
});

test("会话写请求做 CSRF 双提交并带同源 Origin", async () => {
  const { createRequestContext } = await load();
  const fetchImpl = recordingFetch(() => jsonResponse(200, { ok: true, revision: 4 }));
  const { options } = makeContext(fetchImpl, {
    cookie: "maw_session=s1; maw_csrf=csrf-token-1",
    origin: "http://127.0.0.1:7420",
  });
  const ctx = createRequestContext(options);

  await ctx.write("POST", "/api/v1/status-reports", {
    revisionFree: true,
    body: { actor: "a1" },
  });

  const call = fetchImpl.calls[0];
  assert.equal(call.headers["X-CSRF-Token"], "csrf-token-1");
  assert.equal(call.headers.Origin, "http://127.0.0.1:7420");
  assert.equal(call.headers["If-Match"], undefined, "来源上报不带 revision 前置");
});

test("缺 CSRF Cookie 的写请求本地拒绝 403，且不发请求", async () => {
  const { createRequestContext, ApiError } = await load();
  const fetchImpl = recordingFetch(() => jsonResponse(200, { ok: true }));
  const { options } = makeContext(fetchImpl, { cookie: "maw_session=s1" });
  const ctx = createRequestContext(options);

  await assert.rejects(
    () => ctx.write("POST", "/api/v1/status-reports", { revisionFree: true, body: {} }),
    err => {
      assert.ok(err instanceof ApiError);
      assert.equal(err.status, 403);
      assert.equal(err.code, "csrf_failed");
      return true;
    }
  );
  assert.equal(fetchImpl.calls.length, 0);
});

// ---------- 2. revision 与条件写入 ----------

test("写动作缺 revision 前置时本地 422，不默认以最新为准", async () => {
  const { createRequestContext, ApiError } = await load();
  const fetchImpl = recordingFetch(() => jsonResponse(200, { ok: true }));
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext(options);

  await assert.rejects(
    () => ctx.write("PATCH", "/api/v1/tasks/ti_a", { body: { title: "x" } }),
    err => {
      assert.equal(err.status, 422);
      assert.equal(err.code, "revision_required");
      return true;
    }
  );
  assert.equal(fetchImpl.calls.length, 0);
});

test("写动作带 If-Match，成功后回填新 revision", async () => {
  const { createRequestContext } = await load();
  const fetchImpl = recordingFetch((call, i) =>
    i === 0
      ? jsonResponse(200, { ok: true, revision: 3 })
      : jsonResponse(200, { ok: true, revision: 4 })
  );
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext(options);

  await ctx.read("/api/v1/tasks/ti_a");
  await ctx.write("PATCH", "/api/v1/tasks/ti_a", { body: { title: "x" } });

  assert.equal(fetchImpl.calls[1].headers["If-Match"], '"rev-3"');
  assert.equal(ctx.revisionOf("/api/v1/tasks/ti_a"), 4);

  await ctx.write("PATCH", "/api/v1/tasks/ti_a", { body: { title: "y" } });
  assert.equal(fetchImpl.calls[2].headers["If-Match"], '"rev-4"');
});

test("409 revision_mismatch 抛出当前 revision 并同步本地版本", async () => {
  const { createRequestContext } = await load();
  const fetchImpl = recordingFetch((call, i) =>
    i === 0
      ? jsonResponse(200, { ok: true, revision: 42 })
      : jsonResponse(409, {
          ok: false,
          error: {
            code: "revision_mismatch",
            message: "task revision changed",
            retryable: false,
            details: { actual_revision: 43 },
          },
        })
  );

  const { createRequestContext: make } = await load();
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = make(options);

  await ctx.read("/api/v1/tasks/ti_a");
  await assert.rejects(
    () => ctx.write("PATCH", "/api/v1/tasks/ti_a", { body: {} }),
    err => {
      assert.equal(err.status, 409);
      assert.equal(err.code, "revision_mismatch");
      assert.equal(err.details.actual_revision, 43);
      return true;
    }
  );
  assert.equal(ctx.revisionOf("/api/v1/tasks/ti_a"), 43, "冲突后本地版本应跟上实际值");
});

// ---------- 3. 幂等键 ----------

test("POST 自动生成合法幂等键，重试复用同一个键", async () => {
  const { createRequestContext, IDEMPOTENCY_KEY_PATTERN } = await load();
  const fetchImpl = recordingFetch(() => jsonResponse(200, { ok: true, revision: 1 }));
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext(options);

  await ctx.write("POST", "/api/v1/status-reports", { revisionFree: true, actionKey: "report:1", body: {} });
  await ctx.write("POST", "/api/v1/status-reports", { revisionFree: true, actionKey: "report:1", body: {} });
  await ctx.write("POST", "/api/v1/status-reports", { revisionFree: true, actionKey: "report:2", body: {} });

  const [a, b, c] = fetchImpl.calls.map(call => call.headers["Idempotency-Key"]);
  assert.match(a, IDEMPOTENCY_KEY_PATTERN);
  assert.equal(a, b, "同一动作重试必须原样复用键");
  assert.notEqual(a, c, "不同动作不得共用一个键");
});

test("非法幂等键本地 422 invalid_idempotency_key", async () => {
  const { createRequestContext } = await load();
  const fetchImpl = recordingFetch(() => jsonResponse(200, { ok: true }));
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext(options);

  await assert.rejects(
    () => ctx.write("POST", "/api/v1/status-reports", { idempotencyKey: "short", revisionFree: true, body: {} }),
    err => {
      assert.equal(err.status, 422);
      assert.equal(err.code, "invalid_idempotency_key");
      return true;
    }
  );
  assert.equal(fetchImpl.calls.length, 0);
});

// ---------- 4. 202 与异步操作 ----------

test("202 只算受理：轮询到 succeeded 才返回结果", async () => {
  const { createRequestContext } = await load();
  const queue = [
    jsonResponse(202, {
      ok: true,
      operation: OP("op_1", "queued", { retry_after_ms: 300 }),
      revision: 42,
      idempotent_replay: false,
    }, { Location: "/api/v1/operations/op_1" }),
    jsonResponse(200, { ok: true, operation: OP("op_1", "running"), retry_after_ms: 200 }),
    jsonResponse(200, { ok: true, operation: OP("op_1", "running"), retry_after_ms: 200 }),
    jsonResponse(200, { ok: true, operation: OP("op_1", "succeeded"), result: { task_id: "ti_a" } }),
  ];
  const fetchImpl = recordingFetch(() => queue.shift());
  const { options, clock } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext({ ...options, maxPollMs: 10_000 });

  // 领取前必须知道工程级 revision（契约 §5），否则本地就会 422。
  ctx.setRevision("/api/v1/projects/p1/claims", 42);
  const done = await ctx.action("/api/v1/projects/p1/claims", { body: { task_id: "ti_a" } });

  assert.equal(fetchImpl.calls[0].headers["If-Match"], '"rev-42"');
  assert.equal(done.operation.status, "succeeded");
  assert.deepEqual(done.result, { task_id: "ti_a" });
  assert.equal(fetchImpl.calls[0].method, "POST");
  assert.equal(fetchImpl.calls[1].url, "/api/v1/operations/op_1", "按 Location 轮询");
  assert.deepEqual(clock.sleeps, [300, 200, 200], "按 retry_after_ms 逐次退避");
});

test("202 响应里直接宣称终态属于违约，按协议错误抛出", async () => {
  const { createRequestContext } = await load();
  const fetchImpl = recordingFetch(() =>
    jsonResponse(202, { ok: true, operation: OP("op_2", "succeeded"), revision: 1 })
  );
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext(options);
  ctx.setRevision("/api/v1/projects/p1/claims", 1);

  await assert.rejects(
    () => ctx.action("/api/v1/projects/p1/claims", { body: {} }),
    err => {
      assert.equal(err.code, "premature_terminal_operation");
      return true;
    }
  );
});

test("操作 failed 时抛出服务端错误码，不当作成功", async () => {
  const { createRequestContext } = await load();
  const queue = [
    jsonResponse(202, { ok: true, operation: OP("op_3", "queued", { retry_after_ms: 200 }) },
      { Location: "/api/v1/operations/op_3" }),
    jsonResponse(200, {
      ok: true,
      operation: OP("op_3", "failed", {
        error: { code: "write_conflict", message: "worktree dirty", retryable: false, details: { path: "x" } },
      }),
    }),
  ];
  const fetchImpl = recordingFetch(() => queue.shift());
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext({ ...options, maxPollMs: 10_000 });
  ctx.setRevision("/api/v1/projects/p1/claims", 1);

  await assert.rejects(
    () => ctx.action("/api/v1/projects/p1/claims", { body: {} }),
    err => {
      assert.equal(err.code, "write_conflict");
      assert.equal(err.status, 200, "异步失败不改写成 HTTP 状态以外的语义");
      assert.equal(err.details.path, "x");
      return true;
    }
  );
});

test("轮询超出预算抛 OperationPendingError，界面上仍是进行中", async () => {
  const { createRequestContext, OperationPendingError } = await load();
  const queue = [
    jsonResponse(202, { ok: true, operation: OP("op_4", "queued", { retry_after_ms: 200 }) },
      { Location: "/api/v1/operations/op_4" }),
    jsonResponse(200, { ok: true, operation: OP("op_4", "running"), retry_after_ms: 5000 }),
  ];
  const fetchImpl = recordingFetch(() => queue.shift());
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext({ ...options, maxPollMs: 1000 });
  ctx.setRevision("/api/v1/projects/p1/claims", 1);

  await assert.rejects(
    () => ctx.action("/api/v1/projects/p1/claims", { body: {} }),
    err => {
      assert.ok(err instanceof OperationPendingError);
      assert.equal(err.operation.operation_id, "op_4");
      return true;
    }
  );
  assert.equal(ctx.pendingOperations().length, 1, "未完成的操作要保持可见");
});

test("retry_after_ms 被夹到契约区间 200–5000", async () => {
  const { createRequestContext } = await load();
  const queue = [
    jsonResponse(202, { ok: true, operation: OP("op_5", "queued") }, { Location: "/api/v1/operations/op_5" }),
    jsonResponse(200, { ok: true, operation: OP("op_5", "running"), retry_after_ms: 1 }),
    jsonResponse(200, { ok: true, operation: OP("op_5", "running"), retry_after_ms: 999999 }),
    jsonResponse(200, { ok: true, operation: OP("op_5", "succeeded"), result: {} }),
  ];
  const fetchImpl = recordingFetch(() => queue.shift());
  const { options, clock } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext({ ...options, maxPollMs: 60_000 });
  ctx.setRevision("/api/v1/projects/p1/claims", 1);

  await ctx.action("/api/v1/projects/p1/claims", { body: {} });
  // 202 未给 retry_after_ms → 用下限 200；1 → 200；999999 → 上限 5000。
  assert.deepEqual(clock.sleeps, [200, 200, 5000]);
});

// ---------- 5. 结构化错误：401 与 403 不混用 ----------

test("无凭据 401、有凭据被拒 403，错误码按契约映射", async () => {
  const { createRequestContext } = await load();
  const queue = [
    jsonResponse(401, { ok: false, error: { code: "unauthenticated", message: "no token" } }),
    jsonResponse(403, { ok: false, error: { code: "origin_mismatch", message: "cross origin" } }),
  ];
  const fetchImpl = recordingFetch(() => queue.shift());
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext(options);

  await assert.rejects(() => ctx.read("/api/v1/board"), err => {
    assert.equal(err.status, 401);
    assert.equal(err.code, "unauthenticated");
    return true;
  });
  await assert.rejects(() => ctx.read("/api/v1/board"), err => {
    assert.equal(err.status, 403);
    assert.equal(err.code, "origin_mismatch");
    return true;
  });
});

// ---------- 6. 上下文：并发与切换工程 ----------

test("切工程后旧 epoch 的响应被标记 stale 且不污染上下文", async () => {
  const { createRequestContext } = await load();
  let release;
  const fetchImpl = () => new Promise(resolve => { release = resolve; });
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext(options);

  const pending = ctx.read("/api/v1/board");
  const newEpoch = ctx.setProject("p2");
  assert.equal(newEpoch, ctx.epoch);

  release(jsonResponse(200, { ok: true, revision: 5 }));
  const result = await pending;

  assert.equal(result.stale, true, "旧工程的响应必须可识别为过期");
  assert.equal(result.data, null);
  assert.equal(ctx.revisionOf("/api/v1/board"), null, "过期响应不得写回 revision");
  assert.equal(ctx.projectId, "p2");
});

test("切换工程清空上一工程的 revision 与幂等键", async () => {
  const { createRequestContext } = await load();
  const fetchImpl = recordingFetch(() => jsonResponse(200, { ok: true, revision: 9 }));
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext(options);

  await ctx.read("/api/v1/tasks/ti_a");
  ctx.nextIdempotencyKey("claim:p1");
  assert.equal(ctx.revisionOf("/api/v1/tasks/ti_a"), 9);

  ctx.setProject("p2");
  assert.equal(ctx.revisionOf("/api/v1/tasks/ti_a"), null);
  assert.equal(ctx.idempotencyKeyOf("claim:p1"), null);
});

// ---------- 7. 连接状态与刷新恢复 ----------

test("连接状态从 unknown 起，按证据在 online / degraded / offline 间迁移", async () => {
  const { createRequestContext } = await load();
  let mode = "ok";
  const fetchImpl = recordingFetch(() => {
    if (mode === "boom") return new Error("ECONNREFUSED");
    return jsonResponse(200, { ok: true, revision: 1 });
  });
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const ctx = createRequestContext(options);

  assert.equal(ctx.connection, "unknown", "未取得证据前不得显示为正常");

  await ctx.read("/api/v1/board");
  assert.equal(ctx.connection, "online");

  mode = "boom";
  await assert.rejects(() => ctx.read("/api/v1/board"), err => err.code === "network_error");
  assert.equal(ctx.connection, "degraded", "已有数据时降级，不是直接离线");

  mode = "ok";
  await ctx.read("/api/v1/board");
  assert.equal(ctx.connection, "online");
});

test("刷新后恢复工程与已知 revision，但不把旧结论当在线", async () => {
  const { createRequestContext } = await load();
  const store = new Map();
  const storage = {
    getItem: k => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, v),
  };
  const fetchImpl = recordingFetch(() => jsonResponse(200, { ok: true, revision: 12 }));
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x", storage });
  const first = createRequestContext(options);
  first.setProject("p9");
  await first.read("/api/v1/projects/p9/tasks");

  const second = createRequestContext(options);
  const restored = second.restore();

  assert.equal(restored.restored, true);
  assert.equal(second.projectId, "p9");
  assert.equal(second.revisionOf("/api/v1/projects/p9/tasks"), 12);
  assert.notEqual(second.connection, "online", "刷新后必须先重新取证据");
});

// ---------- 8. 资源客户端 ----------

test("显示编号走 display_id 查询参数，实例 ID 走路径", async () => {
  const { createRequestContext } = await load();
  const { createBoardApi } = await loadApi();
  const fetchImpl = recordingFetch(() => jsonResponse(200, { ok: true, revision: 2 }));
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const api = createBoardApi(createRequestContext(options));

  await api.getTask("ti_vn005a");
  await api.getTask("VN-005");

  assert.equal(fetchImpl.calls[0].url, "/api/v1/tasks/ti_vn005a");
  assert.equal(fetchImpl.calls[1].url, "/api/v1/tasks/VN-005?display_id=VN-005");
});

test("来源上报不带 revision，且必须声明未改任务状态", async () => {
  const { createRequestContext } = await load();
  const { createBoardApi } = await loadApi();
  const fetchImpl = recordingFetch(() =>
    jsonResponse(200, { ok: true, changed_task_status: false })
  );
  const { options } = makeContext(fetchImpl, { token: "maw_pat_x" });
  const api = createBoardApi(createRequestContext(options));

  await api.reportStatus({ actor: "a1", reported_status: "working" }, { idempotencyKey: "0123456789abcdef" });

  const call = fetchImpl.calls[0];
  assert.equal(call.url, "/api/v1/status-reports");
  assert.equal(call.headers["If-Match"], undefined);
  assert.equal(call.body.reported_status, "working");
});
