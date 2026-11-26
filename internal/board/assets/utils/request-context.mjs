// 公共请求与页面上下文（vnext / UI）。
//
// 对应契约：
//   docs/contracts/vnext/http/http-api-contract-v1.md（maw.http.api/v1）
//     §3 202 与异步操作 · §4 幂等键 · §5 revision 与条件写入
//     §6 状态码与错误体 · §7 授权：同源会话／CSRF／CLI 令牌
//
// 本模块只做客户端：不发路由、不算业务统计、不把令牌落到页面状态之外。
//
// 三条硬规则：
//   1. 202 不是完成。只有操作进入 succeeded 才算成功，running／queued 一律不算。
//   2. 写动作没带 revision 前置就本地拒绝（422 revision_required），不默认「以最新为准」。
//   3. 过期上下文不覆盖新上下文：切工程或换筛选时旧 epoch 的响应一律标记 stale。

export const CONTRACT = "maw.http.api/v1";

/** 幂等键形态：`[A-Za-z0-9._:-]{16,128}`（契约 §4）。 */
export const IDEMPOTENCY_KEY_PATTERN = /^[A-Za-z0-9._:-]{16,128}$/;

/** 异步操作状态封闭四值（契约 §3），不得新增同义词。 */
export const OPERATION_STATUSES = Object.freeze(["queued", "running", "succeeded", "failed"]);
export const TERMINAL_OPERATION_STATUSES = Object.freeze(["succeeded", "failed"]);

/** 轮询退避区间（契约 §3：retry_after_ms 必须在 200–5000）。 */
export const RETRY_AFTER_MIN_MS = 200;
export const RETRY_AFTER_MAX_MS = 5000;

/** 连接状态。unknown 表示尚未取得可信证据，不得渲染成「正常」。 */
export const CONNECTION_STATES = Object.freeze(["unknown", "online", "degraded", "offline"]);

/** 结构化错误（契约 §6）：`{ok:false, error:{code, message, retryable, details}}`。 */
export class ApiError extends Error {
  constructor({ status, code, message, retryable = false, details = null, path = null }) {
    super(message || code || "request failed");
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.retryable = retryable;
    this.details = details;
    this.path = path;
  }
}

/**
 * 操作仍在进行（未到终态）时抛出。它既不是成功，也不是业务失败，
 * 调用方必须把界面留在「进行中」，不能提前把看板改成完成。
 */
export class OperationPendingError extends Error {
  constructor(operation) {
    super("operation still pending: " + (operation?.operation_id || "unknown"));
    this.name = "OperationPendingError";
    this.operation = operation || null;
    this.retryable = true;
  }
}

const SAFE_METHODS = new Set(["GET", "HEAD"]);

function isSafe(method) {
  return SAFE_METHODS.has(String(method || "GET").toUpperCase());
}

function readCookie(cookieString, name) {
  if (!cookieString) return null;
  for (const part of String(cookieString).split(";")) {
    const i = part.indexOf("=");
    if (i < 0) continue;
    if (part.slice(0, i).trim() === name) return decodeURIComponent(part.slice(i + 1).trim());
  }
  return null;
}

function headerGet(headers, name) {
  if (!headers) return null;
  if (typeof headers.get === "function") return headers.get(name);
  const lower = String(name).toLowerCase();
  for (const key of Object.keys(headers)) {
    if (key.toLowerCase() === lower) return headers[key];
  }
  return null;
}

function parseETagRevision(etag) {
  const m = String(etag || "").match(/^"?rev-(\d+)"?$/);
  return m ? Number(m[1]) : null;
}

function clampRetryAfter(ms) {
  const n = Number(ms);
  if (!Number.isFinite(n)) return RETRY_AFTER_MIN_MS;
  return Math.min(RETRY_AFTER_MAX_MS, Math.max(RETRY_AFTER_MIN_MS, n));
}

function randomId() {
  const c = globalThis.crypto;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  return "op-" + Math.random().toString(16).slice(2) + Date.now().toString(16);
}

/**
 * 页面请求上下文。
 *
 * @param {object} [options]
 * @param {string} [options.baseUrl=""]               API 前缀，默认同源
 * @param {Function} [options.fetchImpl]              注入 fetch（测试用）
 * @param {Function} [options.now]                    时钟
 * @param {Function} [options.sleep]                  退避等待
 * @param {Function} [options.cookieReader]           读 Cookie（默认 document.cookie）
 * @param {string|null} [options.token]               CLI 令牌 `maw_pat_*`；浏览器默认 null
 * @param {string|null} [options.origin]              非浏览器环境显式 Origin（浏览器自动带，禁止手写）
 * @param {object|null} [options.storage]             localStorage 兼容对象，用于刷新恢复
 * @param {number} [options.maxPollMs=8000]           单次异步操作最长轮询预算
 * @param {string|null} [options.projectId]           初始工程
 */
export function createRequestContext(options = {}) {
  const {
    baseUrl = "",
    fetchImpl = globalThis.fetch,
    now = () => Date.now(),
    sleep = ms => new Promise(resolve => setTimeout(resolve, ms)),
    cookieReader = () => (typeof document === "undefined" ? "" : document.cookie),
    token = null,
    origin = null,
    storage = null,
    maxPollMs = 8000,
    projectId = null,
  } = options;

  if (typeof fetchImpl !== "function") {
    throw new Error("createRequestContext: fetch 不可用，需要注入 fetchImpl");
  }

  const STORAGE_KEY = "maw.request-context.v1";

  let currentProjectId = projectId;
  let epoch = 0;
  let connection = "unknown";
  let lastSyncAt = null;
  let lastError = null;

  /** 资源路径 → revision，用于自动带 If-Match（契约 §5）。 */
  const revisions = new Map();
  /** actionKey → 幂等键，重试必须原样复用（契约 §4）。 */
  const idempotencyKeys = new Map();
  /** 已受理未完成的操作，供界面显示「进行中」。 */
  const pendingOperations = new Map();

  function persist() {
    if (!storage || typeof storage.setItem !== "function") return;
    try {
      storage.setItem(
        STORAGE_KEY,
        JSON.stringify({
          contract: CONTRACT,
          projectId: currentProjectId,
          revisions: [...revisions.entries()],
          lastSyncAt,
        })
      );
    } catch (_) {
      /* 存储不可用不影响主流程 */
    }
  }

  function markOnline() {
    connection = "online";
    lastError = null;
    lastSyncAt = new Date(now()).toISOString();
    persist();
  }

  function markTransportFailure(err) {
    // 网络层失败不等于服务不可用：已有数据仍在，只能标为 degraded。
    connection = lastSyncAt ? "degraded" : "offline";
    lastError = err;
    persist();
  }

  function buildUrl(path, query) {
    const base = /^https?:/.test(path) ? path : baseUrl.replace(/\/$/, "") + path;
    if (!query) return base;
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) {
      if (value === undefined || value === null || value === "") continue;
      params.set(key, String(value));
    }
    const qs = params.toString();
    if (!qs) return base;
    return (base.includes("?") ? base + "&" : base + "?") + qs;
  }

  function buildHeaders(method, { body, ifMatch, idempotencyKey } = {}) {
    const headers = { Accept: "application/json" };
    if (body !== undefined) headers["Content-Type"] = "application/json";

    if (token) {
      // 契约 §7.2：只走 Authorization: Bearer，禁止 query/body 传令牌。
      headers.Authorization = "Bearer " + token;
    } else if (!isSafe(method)) {
      // 契约 §7.1：会话写请求必须 CSRF 双提交。
      const csrf = readCookie(cookieReader(), "maw_csrf");
      if (!csrf) {
        throw new ApiError({
          status: 403,
          code: "csrf_failed",
          message: "缺少 maw_csrf Cookie，写请求被拒绝",
        });
      }
      headers["X-CSRF-Token"] = csrf;
    }

    // 浏览器会自动带 Origin 且禁止脚本改写；非浏览器环境（CLI/测试）显式注入。
    if (origin && !isSafe(method)) headers.Origin = origin;

    if (ifMatch !== undefined && ifMatch !== null) {
      headers["If-Match"] = typeof ifMatch === "number" ? `"rev-${ifMatch}"` : ifMatch;
    }
    if (idempotencyKey) headers["Idempotency-Key"] = idempotencyKey;
    return headers;
  }

  function errorFromResponse(status, data, path) {
    const payload = data && data.ok === false ? data.error : null;
    if (payload && payload.code) {
      return new ApiError({
        status,
        code: payload.code,
        message: payload.message,
        retryable: payload.retryable === true,
        details: payload.details ?? null,
        path,
      });
    }
    const fallback = {
      400: "malformed_json",
      401: "unauthenticated",
      403: "forbidden",
      404: "not_found",
      405: "method_not_allowed",
      409: "conflict",
      422: "invalid_request",
      503: "busy",
    }[status] || "unknown_error";
    return new ApiError({ status, code: fallback, message: "HTTP " + status, path });
  }

  async function send(method, path, opts = {}) {
    const url = buildUrl(path, opts.query);
    const headers = buildHeaders(method, opts);
    const startedEpoch = epoch;

    let res;
    try {
      res = await fetchImpl(url, {
        method,
        headers,
        body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
        credentials: opts.credentials || "same-origin",
        signal: opts.signal,
      });
    } catch (err) {
      markTransportFailure(err);
      throw new ApiError({
        status: 0,
        code: "network_error",
        message: err?.message || "网络不可用",
        retryable: true,
        path,
      });
    }

    let data = null;
    if (res.status !== 304 && res.status !== 204) {
      const text = typeof res.text === "function" ? await res.text() : "";
      if (text) {
        try {
          data = JSON.parse(text);
        } catch (_) {
          throw new ApiError({
            status: res.status,
            code: "malformed_json",
            message: "响应不是合法 JSON",
            path,
          });
        }
      }
    }

    if (res.status >= 400) {
      const err = errorFromResponse(res.status, data, path);
      if (err.status === 409 && err.code === "revision_mismatch") {
        const actual = err.details?.actual_revision;
        if (typeof actual === "number") revisions.set(scopeKey(path), actual);
      }
      markOnline();
      throw err;
    }

    // 过期响应只用于判活：不写回 revision，不参与上下文状态（契约：不覆盖新上下文）。
    const stale = startedEpoch !== epoch;
    if (!stale) {
      const revision = typeof data?.revision === "number"
        ? data.revision
        : parseETagRevision(headerGet(res.headers, "ETag"));
      if (typeof revision === "number") revisions.set(scopeKey(path), revision);
      markOnline();
      return { data, status: res.status, headers: res.headers, revision, stale: false, epoch: startedEpoch };
    }
    return { data, status: res.status, headers: res.headers, revision: null, stale: true, epoch: startedEpoch };
  }

  /** 资源键：去掉 query 与末段操作 id，保证「同一资源共用同一 revision」。 */
  function scopeKey(path) {
    return String(path).split("?")[0];
  }

  /**
   * 读请求。过期 epoch 的响应返回 stale=true 且不参与上下文状态更新，
   * 由调用方直接丢弃（契约：切工程与并发请求不会覆盖新上下文）。
   */
  async function read(path, opts = {}) {
    const result = await send("GET", path, opts);
    if (result.stale) return { stale: true, epoch: result.epoch, data: null };
    return { stale: false, epoch: result.epoch, data: result.data, revision: result.revision };
  }

  /**
   * 同步写（只用于幂等小写入，如 status-reports）。
   * 必须带 revision 前置或显式 expectedRevision，否则本地 422。
   * 例外：来源上报（`revisionFree: true`）不写业务行，因此不带 If-Match
   * ——对应契约 §3「同步只允许纯读与幂等小写入」与样本 05。
   */
  async function write(method, path, opts = {}) {
    const expected = opts.revisionFree ? null : resolveExpectedRevision(path, opts);
    const key = opts.idempotencyKey ?? (method.toUpperCase() === "POST" ? nextIdempotencyKey(opts.actionKey || path) : null);
    assertIdempotencyKey(key, method);
    const result = await send(method, path, { ...opts, ifMatch: expected, idempotencyKey: key });
    if (result.stale) return { stale: true, epoch: result.epoch, data: null };
    return { stale: false, epoch: result.epoch, data: result.data, revision: result.revision };
  }

  function resolveExpectedRevision(path, opts) {
    const explicit = opts.expectedRevision ?? opts.body?.expected_revision;
    if (explicit !== undefined && explicit !== null) return explicit;
    const known = revisions.get(scopeKey(opts.revisionPath || path));
    if (known === undefined) {
      throw new ApiError({
        status: 422,
        code: "revision_required",
        message: "写动作必须带 If-Match 或 expected_revision，不得默认以最新为准",
        path,
      });
    }
    return known;
  }

  function assertIdempotencyKey(key, method) {
    if (String(method).toUpperCase() !== "POST") return;
    if (!IDEMPOTENCY_KEY_PATTERN.test(String(key || ""))) {
      throw new ApiError({
        status: 422,
        code: "invalid_idempotency_key",
        message: "幂等键形态必须是 [A-Za-z0-9._:-]{16,128}",
      });
    }
  }

  function nextIdempotencyKey(actionKey) {
    const existing = idempotencyKeys.get(actionKey);
    if (existing) return existing; // 重试原样复用
    const key = randomId();
    idempotencyKeys.set(actionKey, key);
    return key;
  }

  /** 主动换键：只有确认上一次动作作废（例如用户改了参数）时才允许。 */
  function resetIdempotencyKey(actionKey) {
    idempotencyKeys.delete(actionKey);
  }

  /**
   * 异步动作（契约 §3）：POST → 202 → 轮询到终态才算成功。
   * 返回 { stale, operation, result, revision }；非终态抛 OperationPendingError。
   */
  async function action(path, opts = {}) {
    const expected = resolveExpectedRevision(path, { ...opts, revisionPath: opts.revisionPath });
    const key = opts.idempotencyKey ?? nextIdempotencyKey(opts.actionKey || path);
    assertIdempotencyKey(key, "POST");

    const accepted = await send("POST", path, { ...opts, ifMatch: expected, idempotencyKey: key });
    if (accepted.stale) return { stale: true, epoch: accepted.epoch };

    const operation = accepted.data?.operation || null;
    if (accepted.status !== 202) {
      // 同键重放可能直接返回 200；此时也算完成，但仍以响应的 revision 为准。
      return {
        stale: false,
        epoch: accepted.epoch,
        status: accepted.status,
        operation,
        result: accepted.data?.result ?? null,
        revision: accepted.revision,
      };
    }
    if (!operation || !operation.operation_id) {
      throw new ApiError({
        status: 502,
        code: "missing_operation",
        message: "202 响应缺少 operation_id",
        path,
      });
    }
    if (TERMINAL_OPERATION_STATUSES.includes(operation.status)) {
      // 契约 §3 明确禁止：202 里不得宣称已成功。
      throw new ApiError({
        status: 502,
        code: "premature_terminal_operation",
        message: "202 响应不得直接返回终态",
        path,
      });
    }

    pendingOperations.set(operation.operation_id, operation);
    const settled = await pollOperation(operation.operation_id, {
      location: headerGet(accepted.headers, "Location"),
      retryAfterMs: operation.retry_after_ms,
      operation,
      deadline: now() + (opts.maxPollMs ?? maxPollMs),
      signal: opts.signal,
      epochAtStart: accepted.epoch,
    });
    return {
      stale: false,
      epoch: accepted.epoch,
      operation: settled.operation,
      result: settled.result,
      revision: revisions.get(scopeKey(path)) ?? null,
    };
  }

  /**
   * 轮询操作（契约 §3）：非终止态必须带 retry_after_ms，客户端按此退避后才发现变化。
   * 第一次轮询同样要等：202 里的 retry_after_ms 不是装饰，跳过它等于空转打服务端。
   * 超预算抛 OperationPendingError —— 「未完成」既不是成功也不是失败。
   */
  async function pollOperation(operationId, opts = {}) {
    const path = opts.location || "/api/v1/operations/" + encodeURIComponent(operationId);
    const deadline = opts.deadline ?? now() + maxPollMs;
    let operation = opts.operation || null;
    let delay = clampRetryAfter(opts.retryAfterMs ?? RETRY_AFTER_MIN_MS);

    for (;;) {
      if (now() + delay > deadline) throw new OperationPendingError(operation);
      await sleep(delay);

      const res = await send("GET", path, { signal: opts.signal });
      if (res.stale) return { stale: true, operation: null, result: null };

      operation = res.data?.operation || null;
      const status = operation?.status;

      if (status === "succeeded") {
        pendingOperations.delete(operationId);
        return { stale: false, operation, result: res.data?.result ?? operation.result ?? null };
      }
      if (status === "failed") {
        pendingOperations.delete(operationId);
        const payload = operation.error || res.data?.error;
        throw new ApiError({
          status: 200,
          code: payload?.code || "operation_failed",
          message: payload?.message || "异步操作失败",
          retryable: payload?.retryable === true,
          details: payload?.details ?? null,
          path,
        });
      }
      if (!status) {
        throw new ApiError({
          status: 502,
          code: "missing_operation",
          message: "操作查询缺少 status",
          path,
        });
      }
      // 未知状态按「未完成」继续等，不猜成成功（契约 §3 状态集合封闭）。

      pendingOperations.set(operationId, operation);
      delay = clampRetryAfter(
        operation?.retry_after_ms ?? res.data?.retry_after_ms ?? Number(headerGet(res.headers, "Retry-After")) * 1000
      );
    }
  }

  function setProject(nextProjectId) {
    const normalized = nextProjectId || null;
    currentProjectId = normalized;
    epoch += 1;
    // 换工程后旧工程的行版本不能再用于新工程的写动作。
    revisions.clear();
    idempotencyKeys.clear();
    persist();
    return epoch;
  }

  /** 从 storage 恢复：只恢复视图上下文与已知 revision，不恢复任何「已完成」结论。 */
  function restore() {
    if (!storage || typeof storage.getItem !== "function") return { restored: false };
    try {
      const raw = storage.getItem(STORAGE_KEY);
      if (!raw) return { restored: false };
      const saved = JSON.parse(raw);
      if (!saved || saved.contract !== CONTRACT) return { restored: false };
      if (saved.projectId) currentProjectId = saved.projectId;
      if (Array.isArray(saved.revisions)) {
        revisions.clear();
        for (const [key, value] of saved.revisions) {
          if (typeof value === "number") revisions.set(key, value);
        }
      }
      lastSyncAt = saved.lastSyncAt || null;
      connection = lastSyncAt ? "degraded" : "unknown";
      return { restored: true, projectId: currentProjectId, lastSyncAt, revisions: new Map(revisions) };
    } catch (_) {
      return { restored: false };
    }
  }

  function snapshotState() {
    return {
      contract: CONTRACT,
      projectId: currentProjectId,
      epoch,
      connection,
      lastSyncAt,
      lastError: lastError ? { code: lastError.code, message: lastError.message } : null,
      pendingOperations: [...pendingOperations.values()],
      revisions: new Map(revisions),
    };
  }

  return {
    // 状态
    get projectId() { return currentProjectId; },
    get epoch() { return epoch; },
    get connection() { return connection; },
    get lastSyncAt() { return lastSyncAt; },
    isCurrent(capturedEpoch) { return capturedEpoch === epoch; },
    snapshotState,
    revisionOf(path) { return revisions.get(scopeKey(path)) ?? null; },
    setRevision(path, revision) { revisions.set(scopeKey(path), revision); },
    pendingOperations() { return [...pendingOperations.values()]; },
    idempotencyKeyOf(actionKey) { return idempotencyKeys.get(actionKey) ?? null; },
    // 上下文
    setProject,
    restore,
    // 请求
    read,
    write,
    action,
    pollOperation,
    request: send,
    // 幂等键
    nextIdempotencyKey,
    resetIdempotencyKey,
  };
}
