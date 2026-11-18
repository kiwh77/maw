// 用量归属（PI-203）：按来源／节点／模型／用途查看用量与采集完整度。
//
// 数据来自 PI-201／PI-202 已交付的 `/api/v1[/projects/{id}]/analytics?dimension=…`，
// 这里只做三件事：拼地址、取数、把响应归一成视图认识的状态。
//
// 三条口径（契约 maw.observability.timeline/v1 §4–§5、maw.pi.execution/v1 §8–§11）：
//   1. 缺 token 就是 null，不当 0：未知用量计入轮次，但不进 token 合计；
//   2. 每个比率都带分子分母，分母为 0 时比率是 null，显示「无分母」而不是 0%；
//   3. call 级与 run 级是两类事实：同一行只标它实际用到的口径（scopes），
//      旧 CLI 只有运行级汇总时仍按原粒度展示，不伪造成逐次调用。

/** 归属维度：与 HTTP 层的封闭集合一一对应。 */
export const ATTRIBUTION_DIMENSIONS = Object.freeze(["source", "node", "model", "purpose"]);

/** 维度 → i18n key；视图负责翻译，模块只搬运。 */
const DIMENSION_LABEL_KEYS = Object.freeze({
  source: "attr.dimension.source",
  node: "attr.dimension.node",
  model: "attr.dimension.model",
  purpose: "attr.dimension.purpose",
});

export function dimensionLabelKey(dimension) {
  return DIMENSION_LABEL_KEYS[dimension] || DIMENSION_LABEL_KEYS.source;
}

/** 归属查询地址：有工程走工程路由，没有工程走全局聚合路由。 */
export function attributionPath(projectId) {
  const id = String(projectId || "").trim();
  return id
    ? `/api/v1/projects/${encodeURIComponent(id)}/analytics`
    : "/api/v1/analytics";
}

/**
 * 构造一次维度查询。
 *
 * 不传窗口参数：统计接口的默认窗口是最近 7 天，主报表（/analytics 无参数）用的是
 * 同一默认值；这里显式只加 dimension，避免两处窗口口径悄悄分叉。
 */
export function attributionUrl(projectId, dimension) {
  const dim = ATTRIBUTION_DIMENSIONS.includes(dimension) ? dimension : "source";
  const params = new URLSearchParams();
  params.set("dimension", dim);
  return `${attributionPath(projectId)}?${params.toString()}`;
}

/** 运行列表地址：汇总的下钻落点（Run 级归属只到工程粒度，见 §attr.run.grain）。 */
export function runDrillHref(projectId) {
  const id = String(projectId || "").trim();
  const params = new URLSearchParams();
  params.set("view", "runs");
  if (id) params.set("project", id);
  return `?${params.toString()}${id ? `#${encodeURIComponent(id)}` : ""}`;
}

/** null／undefined 是「未观测」，不是 0。 */
export function numOrNull(value) {
  if (value == null) return null;
  const n = Number(value);
  return Number.isFinite(n) && n >= 0 ? n : null;
}

/** 比率原样搬运：分母为 0 时 ratio 必须是 null，不在这里补 0。 */
export function ratioOrNull(raw) {
  if (!raw || typeof raw !== "object") return null;
  const numerator = numOrNull(raw.numerator);
  const denominator = numOrNull(raw.denominator);
  if (numerator == null || denominator == null) return null;
  return {
    numerator,
    denominator,
    ratio: typeof raw.ratio === "number" && Number.isFinite(raw.ratio) ? raw.ratio : null,
  };
}

function tokensOf(raw) {
  const tokens = raw && typeof raw === "object" ? raw : {};
  return {
    input: numOrNull(tokens.input_tokens),
    output: numOrNull(tokens.output_tokens),
    total: numOrNull(tokens.total_tokens),
    cache_read: numOrNull(tokens.cache_read_tokens),
    cache_write: numOrNull(tokens.cache_write_tokens),
  };
}

/**
 * 把一条归集行归一成渲染模型。
 *
 * `value` 保持字符串原样：空串表示「来源未声明该字段」，视图据此写「未声明」，
 * 而不是显示成空白或一个假值。
 */
export function normalizeRollup(raw = {}) {
  return {
    dimension: ATTRIBUTION_DIMENSIONS.includes(raw.dimension) ? raw.dimension : "source",
    value: typeof raw.value === "string" ? raw.value : "",
    scopes: Array.isArray(raw.scopes) ? raw.scopes.map(String) : [],
    turns: numOrNull(raw.turns),
    tokens: tokensOf(raw.tokens),
    usageUnknown: raw.usage_unknown === true,
    estimatedTurns: numOrNull(raw.estimated_turns),
    cacheNormalized: numOrNull(raw.cache_normalized),
    cacheUnverified: numOrNull(raw.cache_unverified),
    observed: ratioOrNull(raw.usage_observed),
    evidence: Array.isArray(raw.evidence) ? raw.evidence.map(String) : [],
  };
}

/** 整份归属报表归一；缺字段保持 null／空数组，不构造全零报表。 */
export function normalizeAttribution(raw = {}) {
  const totals = raw.totals && typeof raw.totals === "object" ? raw.totals : {};
  const cost = totals.estimated_cost && typeof totals.estimated_cost === "object" ? totals.estimated_cost : {};
  const scope = raw.scope && typeof raw.scope === "object" ? raw.scope : {};
  return {
    dimension: ATTRIBUTION_DIMENSIONS.includes(raw.dimension) ? raw.dimension : "source",
    scope: {
      projectId: typeof scope.project_id === "string" ? scope.project_id : "",
      start: typeof scope.start === "string" ? scope.start : "",
      end: typeof scope.end === "string" ? scope.end : "",
      timezone: typeof scope.timezone === "string" ? scope.timezone : "",
    },
    completeness: typeof raw.completeness === "string" ? raw.completeness : "unknown",
    generatedAt: typeof raw.generated_at === "string" ? raw.generated_at : "",
    watermark: raw.watermark && typeof raw.watermark === "object" ? raw.watermark : null,
    rollups: Array.isArray(raw.rollups) ? raw.rollups.map(normalizeRollup) : [],
    totals: {
      tokens: tokensOf(totals.tokens),
      calls: ratioOrNull(totals.calls_with_usage),
      turns: ratioOrNull(totals.turns_with_usage),
      runSuccess: ratioOrNull(totals.run_success),
      deliveryPass: ratioOrNull(totals.delivery_pass),
      usageUnknown: totals.usage_unknown === true,
      cost: {
        amountUSD: typeof cost.amount_usd === "number" && Number.isFinite(cost.amount_usd)
          ? cost.amount_usd
          : null,
        basis: typeof cost.basis === "string" ? cost.basis : "",
      },
    },
    gaps: Array.isArray(raw.gaps)
      ? raw.gaps.map(gap => ({
        field: typeof gap.field === "string" ? gap.field : "",
        reason: typeof gap.reason === "string" ? gap.reason : "",
      }))
      : [],
  };
}

/**
 * 粒度分类：call／run／mixed／unknown。
 *
 * 旧 CLI（例如 CodeBuddy 的整次运行汇总）只有 run 级；Pi 给逐次调用。
 * 两者同时出现时标 mixed——混在一起说「逐次调用」就是伪造证据。
 */
export function granularityOf(rollup = {}) {
  const scopes = Array.isArray(rollup.scopes) ? rollup.scopes : [];
  const hasCall = scopes.includes("call");
  const hasRun = scopes.includes("run");
  if (hasCall && hasRun) return "mixed";
  if (hasCall) return "call";
  if (hasRun) return "run";
  return "unknown";
}

/**
 * 取齐四个维度。
 *
 * 每个维度独立成败：某一个维度 4xx／500 不该把整块打成错误——其余维度仍然是
 * 「已观测」的事实。全部失败才收口成 error；全部 401 收口成 unauthenticated。
 */
export async function fetchAttribution(projectId, options = {}) {
  const fetchImpl = options.fetchImpl || (typeof fetch === "function" ? fetch : null);
  const requested = Array.isArray(options.dimensions)
    ? options.dimensions.filter(d => ATTRIBUTION_DIMENSIONS.includes(d))
    : [];
  // 非法／空的维度清单回落到四个标准维度：不把「参数写错」装成「未登录」。
  const dimensions = requested.length ? requested : ATTRIBUTION_DIMENSIONS;
  if (!fetchImpl) {
    return { status: "error", reason: "fetch_unavailable", reports: {}, errors: {} };
  }
  const settled = await Promise.all(dimensions.map(async dimension => {
    try {
      const res = await fetchImpl(attributionUrl(projectId, dimension), {
        headers: { Accept: "application/json" },
        credentials: "same-origin",
      });
      if (res && res.status === 401) return { dimension, status: "unauthenticated" };
      if (!res || !res.ok) return { dimension, status: "error", reason: `http_${(res && res.status) || 0}` };
      const payload = await res.json();
      if (payload && payload.unauthenticated === true) return { dimension, status: "unauthenticated" };
      if (payload && (payload.status === "unsupported" ||
        (Array.isArray(payload.unsupported) && payload.unsupported.length))) {
        return { dimension, status: "unsupported" };
      }
      if (!payload || !payload.attribution || typeof payload.attribution !== "object") {
        // 字段缺席是「这次查询没有归集」，不是「窗口内用量为零」：按不支持处理。
        return { dimension, status: "unsupported", reason: "no_attribution" };
      }
      return {
        dimension,
        status: "ready",
        report: normalizeAttribution(payload.attribution),
        completeness: typeof payload.completeness === "string" ? payload.completeness : "",
      };
    } catch (_) {
      return { dimension, status: "error", reason: "network" };
    }
  }));

  const reports = {};
  const errors = {};
  let ready = 0;
  let unauthenticated = 0;
  let unsupported = 0;
  for (const item of settled) {
    if (item.status === "ready") {
      ready += 1;
      reports[item.dimension] = item.report || normalizeAttribution({ dimension: item.dimension });
      if (item.completeness) reports[item.dimension].completeness = item.completeness;
    } else {
      if (item.status === "unauthenticated") unauthenticated += 1;
      if (item.status === "unsupported") unsupported += 1;
      errors[item.dimension] = item.status === "unsupported" || item.status === "unauthenticated"
        ? item.status
        : (item.reason || "error");
    }
  }
  let status = "ready";
  if (ready === 0) {
    if (unauthenticated === dimensions.length) status = "unauthenticated";
    else if (unsupported === dimensions.length) status = "unsupported";
    else status = "error";
  }
  return { status, reports, errors, dimensions: [...dimensions] };
}
