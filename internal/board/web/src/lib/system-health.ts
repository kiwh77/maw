/**
 * 看板「自愈统计」视图的数据适配（SH-503）。
 *
 * 数据源：`GET /api/v1/system/health`（全部工程）与
 * `GET /api/v1/projects/{id}/system/health`（单工程），由 SH-502 落地。
 * 指标口径见 `docs/contracts/vnext/lead/lead-selfheal-contract-v1.md` §6：
 * **null ≠ 0**，分母为 0 或样本不足时显示「未知」，绝不把未知画成 0。
 *
 * 本模块只做「载荷 → 视图模型」的纯变换与文案，不发请求、不读全局状态，
 * 便于在 node 下单独校验。
 */

export type Quality = 'observed' | 'inferred' | 'unknown'

export type RatioMetric = {
  numerator: number
  denominator: number
  ratio: number | null
  quality: Quality
  note?: string
}

export type DurationMetric = {
  sample: number
  p50_seconds: number | null
  p95_seconds: number | null
  mean_seconds: number | null
  quality: Quality
  note?: string
}

export type JobsHealth = {
  total: number | null
  by_kind: Record<string, number>
  by_status: Record<string, number>
  success_rate: RatioMetric
  success_rate_strict: RatioMetric
  backlog: number
  backlog_oldest_seconds: number | null
  retry_rate: RatioMetric
  lease_reclaim_rate: RatioMetric
  failure_reasons: Record<string, number>
  unspecified_failures: number
}

export type ReconcileHealth = {
  release: number
  keep: number
  escalate: number
  unwired: number
  rate: RatioMetric
  mttr: DurationMetric
}

export type DispatchHealth = {
  queue_wait: DurationMetric
  claim_to_start: DurationMetric
}

export type AgentHealth = {
  heartbeat_freshness: RatioMetric
  node_success_rate: RatioMetric
}

export type SystemHealthReport = {
  scope?: { project_id?: string; start?: string; end?: string; timezone?: string }
  generated_at?: string
  jobs?: Partial<JobsHealth>
  reconcile?: Partial<ReconcileHealth>
  dispatch?: Partial<DispatchHealth>
  agents?: Partial<AgentHealth>
  job_rows?: number
  run_rows?: number
  agent_rows?: number
}

export type WindowInfo = {
  start?: string
  end?: string
  timezone?: string
  hours?: number
  defaulted?: boolean
}

export type MissingItem = { section?: string; reason?: string; detail?: string }
export type UnsupportedItem = { capability?: string; reason?: string; detail?: string }

export type MetricCard = {
  key: string
  label: string
  value: string
  unit: string
  detail: string
  /** 该指标无法计量（分母为 0、样本不足或维度缺失）。 */
  unknown: boolean
  tone: 'ok' | 'warn' | 'danger' | 'muted'
}

export type HealthRow = { key: string; label: string; code: string; count: number }

export type HealthTable = {
  key: string
  title: string
  basis: string
  rows: HealthRow[]
}

export type HealthView =
  | { state: 'error'; message: string }
  | { state: 'unsupported'; message: string }
  | {
      state: 'ready'
      cards: MetricCard[]
      tables: HealthTable[]
      windowText: string
      generatedText: string
      evidenceText: string
      unknownNotes: string[]
    }

/** 观测资源地址：有工程走工程作用域，没有工程走全局聚合。 */
export function systemHealthPath(projectId: string): string {
  const id = String(projectId || '').trim()
  if (!id) return '/api/v1/system/health'
  return `/api/v1/projects/${encodeURIComponent(id)}/system/health`
}

type Copy = {
  title: string
  subtitle: string
  allProjects: string
  projectScope: (id: string) => string
  loading: string
  retry: string
  errorUnauthenticated: string
  errorNetwork: string
  errorGeneric: string
  unsupported: string
  partial: string
  noRows: string
  nameColumn: string
  countColumn: string
  unknown: string
  noSample: string
  sampleShort: (n: number) => string
  ratioDetail: (n: number, d: number) => string
  strict: (value: string) => string
  oldest: (value: string) => string
  window: (start: string, end: string, tz: string) => string
  generated: (value: string) => string
  evidence: (jobs: number, runs: number, agents: number) => string
  card: Record<string, string>
  basis: Record<string, string>
  kind: Record<string, string>
  status: Record<string, string>
  table: { kinds: string; statuses: string; reasons: string; reconcile: string; reconcileBasis: string; rowsBasis: string; resultBasis: string }
}

const KIND_ZH: Record<string, string> = {
  unblock_assist: '解阻协助',
  task_split: '拆题提案',
  ops_run: '白名单运维',
  dispatch_ready: '派发选题',
  run_reconcile: '核实收尾',
}

const KIND_EN: Record<string, string> = {
  unblock_assist: 'Unblock assist',
  task_split: 'Task split',
  ops_run: 'Ops run',
  dispatch_ready: 'Dispatch ready',
  run_reconcile: 'Run reconcile',
}

const STATUS_ZH: Record<string, string> = {
  queued: '排队中',
  running: '运行中',
  done: '已完成',
  failed: '失败',
  escalated: '升级人工',
  cancelled: '已取消',
}

const STATUS_EN: Record<string, string> = {
  queued: 'Queued',
  running: 'Running',
  done: 'Done',
  failed: 'Failed',
  escalated: 'Escalated',
  cancelled: 'Cancelled',
}

const ZH: Copy = {
  title: '自愈统计',
  subtitle: '系统作业、Run 核实与派发耗时；未知值保持未知，不显示为 0。',
  allProjects: '全部工程',
  projectScope: (id) => `当前工程：${id}`,
  loading: '正在读取自愈统计…',
  retry: '重试',
  errorUnauthenticated: '未登录，无法查看自愈统计。刷新页面或重新建立本机会话后重试。',
  errorNetwork: '连不上本机中枢，自愈统计读取失败。',
  errorGeneric: '自愈统计读取失败。',
  unsupported: '本机未配置系统健康数据源，暂时无法展示自愈统计。',
  partial: '部分指标未知',
  noRows: '这个范围里没有记录（不是 0，是没采集到）。',
  nameColumn: '项目',
  countColumn: '数量',
  unknown: '未知',
  noSample: '没有样本',
  sampleShort: (n) => `样本 ${n} < 3，只给均值`,
  ratioDetail: (n, d) => `${n} / ${d}`,
  strict: (value) => `严格口径（不含升级）：${value}`,
  oldest: (value) => `最久排队：${value}`,
  window: (start, end, tz) => `窗口 ${start} → ${end}（${tz}）`,
  generated: (value) => `生成于 ${value}`,
  evidence: (jobs, runs, agents) => `证据：作业 ${jobs} 行 · Run ${runs} 行 · Agent ${agents} 行`,
  card: {
    total: '自愈作业总数',
    success: '作业成功率',
    failures: '失败结果',
    backlog: '积压作业',
    retry: '重试率',
    reclaim: '租约回收率',
    reconcile: 'Run 释放率',
    mttr: '核实 MTTR',
    queue: '派发排队',
    claim: '领取到启动',
    heartbeat: 'Agent 心跳新鲜度',
    node: '节点成功率',
  },
  basis: {
    total: '口径：窗口内 system_jobs 计数，按 kind 分桶',
    success: '口径：done / (done + failed + escalated)，不含 cancelled',
    failures: '口径：failed + escalated 的失败原因分布',
    backlog: '口径：queued 计数与最久排队时长',
    retry: '口径：attempts > 1 / attempts > 0',
    reclaim: '口径：result.reaped_from=running / 曾领取作业',
    reconcile: '口径：release / (release + keep + escalate)',
    mttr: '口径：release 的 finished_at − created_at',
    queue: '口径：run.created_at − job.created_at',
    claim: '口径：run.started_at − run.created_at',
    heartbeat: '口径：15 分钟内 last_seen / 已登记 Agent',
    node: '口径：runs 表没有节点归属字段，v1 不计量',
  },
  kind: KIND_ZH,
  status: STATUS_ZH,
  table: {
    kinds: '作业类型',
    statuses: '状态分布',
    reasons: '结果（失败原因）',
    reconcile: 'Run 核实结论',
    reconcileBasis: '口径：run_reconcile 作业的释放判据分布',
    rowsBasis: '口径：窗口内 system_jobs 按维度计数',
    resultBasis: '口径：failed + escalated 的 result.reason（缺失时取 decision.action）',
  },
}

const EN: Copy = {
  title: 'Self-heal statistics',
  subtitle: 'System jobs, run reconcile and dispatch latency; unknown stays unknown, never shown as 0.',
  allProjects: 'All projects',
  projectScope: (id) => `Project: ${id}`,
  loading: 'Loading self-heal statistics…',
  retry: 'Retry',
  errorUnauthenticated: 'Not signed in, cannot read self-heal statistics. Refresh and retry.',
  errorNetwork: 'Cannot reach the local hub; self-heal statistics failed to load.',
  errorGeneric: 'Self-heal statistics failed to load.',
  unsupported: 'No system health source is configured on this hub.',
  partial: 'Some metrics unknown',
  noRows: 'No records in this range (missing, not zero).',
  nameColumn: 'Item',
  countColumn: 'Count',
  unknown: 'Unknown',
  noSample: 'No samples',
  sampleShort: (n) => `Sample ${n} < 3; mean only`,
  ratioDetail: (n, d) => `${n} / ${d}`,
  strict: (value) => `Strict basis (excluding escalations): ${value}`,
  oldest: (value) => `Oldest queued: ${value}`,
  window: (start, end, tz) => `Window ${start} → ${end} (${tz})`,
  generated: (value) => `Generated at ${value}`,
  evidence: (jobs, runs, agents) => `Evidence: ${jobs} job rows · ${runs} run rows · ${agents} agent rows`,
  card: {
    total: 'System jobs',
    success: 'Success rate',
    failures: 'Failures',
    backlog: 'Backlog',
    retry: 'Retry rate',
    reclaim: 'Lease reclaim rate',
    reconcile: 'Run release rate',
    mttr: 'Reconcile MTTR',
    queue: 'Dispatch queue',
    claim: 'Claim to start',
    heartbeat: 'Agent heartbeat freshness',
    node: 'Node success rate',
  },
  basis: {
    total: 'Basis: system_jobs count in window, bucketed by kind',
    success: 'Basis: done / (done + failed + escalated), cancelled excluded',
    failures: 'Basis: failure reason distribution of failed + escalated jobs',
    backlog: 'Basis: queued count and oldest queue age',
    retry: 'Basis: attempts > 1 / attempts > 0',
    reclaim: 'Basis: result.reaped_from=running / claimed jobs',
    reconcile: 'Basis: release / (release + keep + escalate)',
    mttr: 'Basis: finished_at − created_at for released runs',
    queue: 'Basis: run.created_at − job.created_at',
    claim: 'Basis: run.started_at − run.created_at',
    heartbeat: 'Basis: last_seen within 15m / registered agents',
    node: 'Basis: runs carry no node attribution; not measured in v1',
  },
  kind: KIND_EN,
  status: STATUS_EN,
  table: {
    kinds: 'Job kinds',
    statuses: 'Status distribution',
    reasons: 'Result (failure reasons)',
    reconcile: 'Run reconcile verdicts',
    reconcileBasis: 'Basis: release verdict distribution of run_reconcile jobs',
    rowsBasis: 'Basis: system_jobs in window, counted per dimension',
    resultBasis: 'Basis: result.reason of failed + escalated jobs (falls back to decision.action)',
  },
}

export function copyFor(lang: string): Copy {
  return lang === 'en' ? EN : ZH
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' ? (value as Record<string, unknown>) : {}
}

function num(value: unknown): number {
  const n = Number(value)
  return Number.isFinite(n) ? n : 0
}

function maybeNum(value: unknown): number | null {
  if (value === null || value === undefined) return null
  const n = Number(value)
  return Number.isFinite(n) ? n : null
}

function ratioMetric(value: unknown): RatioMetric {
  const raw = asRecord(value)
  return {
    numerator: num(raw.numerator),
    denominator: num(raw.denominator),
    ratio: maybeNum(raw.ratio),
    quality: (raw.quality === 'observed' || raw.quality === 'inferred' ? raw.quality : 'unknown') as Quality,
    note: typeof raw.note === 'string' ? raw.note : '',
  }
}

function durationMetric(value: unknown): DurationMetric {
  const raw = asRecord(value)
  return {
    sample: num(raw.sample),
    p50_seconds: maybeNum(raw.p50_seconds),
    p95_seconds: maybeNum(raw.p95_seconds),
    mean_seconds: maybeNum(raw.mean_seconds),
    quality: (raw.quality === 'observed' || raw.quality === 'inferred' ? raw.quality : 'unknown') as Quality,
    note: typeof raw.note === 'string' ? raw.note : '',
  }
}

function countMap(value: unknown): Record<string, number> {
  const out: Record<string, number> = {}
  for (const [key, raw] of Object.entries(asRecord(value))) {
    const n = Number(raw)
    if (Number.isFinite(n) && n > 0) out[key] = n
  }
  return out
}

function seconds(value: number | null): string {
  if (value === null) return ''
  if (value >= 60) {
    const minutes = value / 60
    return `${minutes >= 10 ? Math.round(minutes) : minutes.toFixed(1)}m`
  }
  return `${value >= 10 ? Math.round(value) : value.toFixed(1)}s`
}

function percent(value: number): string {
  return `${(value * 100).toFixed(1)}%`
}

/** 比率卡：分母为 0 或 quality=unknown 时显示「未知」，并保留分子分母供核对。 */
function ratioCard(
  key: string,
  label: string,
  basis: string,
  metric: RatioMetric,
  copy: Copy,
  tone: MetricCard['tone'] = 'muted',
  extraDetail?: string,
): MetricCard {
  const unknown = metric.ratio === null || metric.quality === 'unknown'
  const detailParts = [copy.ratioDetail(metric.numerator, metric.denominator)]
  if (extraDetail) detailParts.push(extraDetail)
  if (metric.note) detailParts.push(metric.note)
  return {
    key,
    label,
    value: unknown ? copy.unknown : percent(metric.ratio as number),
    unit: '',
    detail: detailParts.join(' · ') + ` · ${basis}`,
    unknown,
    tone: unknown ? 'muted' : tone,
  }
}

/** 耗时卡：样本为 0 时「未知」；样本 < 3 时 quality=unknown，只展示均值并标注。 */
function durationCard(
  key: string,
  label: string,
  basis: string,
  metric: DurationMetric,
  copy: Copy,
): MetricCard {
  if (metric.sample <= 0) {
    return {
      key,
      label,
      value: copy.unknown,
      unit: '',
      detail: `${copy.noSample} · ${basis}`,
      unknown: true,
      tone: 'muted',
    }
  }
  const lowSample = metric.quality === 'unknown'
  if (lowSample) {
    return {
      key,
      label,
      value: metric.mean_seconds === null ? copy.unknown : seconds(metric.mean_seconds),
      unit: '',
      detail: `${copy.sampleShort(metric.sample)} · ${basis}`,
      unknown: true,
      tone: 'muted',
    }
  }
  return {
    key,
    label,
    value: seconds(metric.p50_seconds),
    unit: '',
    detail: `p50 / p95：${seconds(metric.p50_seconds)} / ${seconds(metric.p95_seconds)} · 样本 ${metric.sample} · ${basis}`,
    unknown: false,
    tone: 'muted',
  }
}

function tableRows(map: Record<string, number>, labels: Record<string, string>): HealthRow[] {
  return Object.entries(map)
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([code, count]) => ({
      key: code,
      // 未知枚举保留原始 code，绝不折叠成「其他」——那样会把新 kind 藏起来。
      label: labels[code] || code,
      code,
      count,
    }))
}

function normalizeReport(raw: unknown): SystemHealthReport {
  const report = asRecord(raw)
  const jobs = asRecord(report.jobs)
  const reconcile = asRecord(report.reconcile)
  const dispatch = asRecord(report.dispatch)
  const agents = asRecord(report.agents)
  return {
    scope: asRecord(report.scope) as SystemHealthReport['scope'],
    generated_at: typeof report.generated_at === 'string' ? report.generated_at : '',
    jobs: {
      total: maybeNum(jobs.total),
      by_kind: countMap(jobs.by_kind),
      by_status: countMap(jobs.by_status),
      success_rate: ratioMetric(jobs.success_rate),
      success_rate_strict: ratioMetric(jobs.success_rate_strict),
      backlog: num(jobs.backlog),
      backlog_oldest_seconds: maybeNum(jobs.backlog_oldest_seconds),
      retry_rate: ratioMetric(jobs.retry_rate),
      lease_reclaim_rate: ratioMetric(jobs.lease_reclaim_rate),
      failure_reasons: countMap(jobs.failure_reasons),
      unspecified_failures: num(jobs.unspecified_failures),
    },
    reconcile: {
      release: num(reconcile.release),
      keep: num(reconcile.keep),
      escalate: num(reconcile.escalate),
      unwired: num(reconcile.unwired),
      rate: ratioMetric(reconcile.rate),
      mttr: durationMetric(reconcile.mttr),
    },
    dispatch: {
      queue_wait: durationMetric(dispatch.queue_wait),
      claim_to_start: durationMetric(dispatch.claim_to_start),
    },
    agents: {
      heartbeat_freshness: ratioMetric(agents.heartbeat_freshness),
      node_success_rate: ratioMetric(agents.node_success_rate),
    },
    job_rows: num(report.job_rows),
    run_rows: num(report.run_rows),
    agent_rows: num(report.agent_rows),
  }
}

function formatWindow(window: WindowInfo, copy: Copy): string {
  const start = window.start || '—'
  const end = window.end || '—'
  const tz = window.timezone || 'UTC'
  return copy.window(start, end, tz)
}

/**
 * 载荷 → 视图模型。
 *
 * 失败形态分四种：会话失效（401）、连不上（status=0）、接口错误码、
 * 能力未配置（unsupported）。它们不能都折叠成一句「加载失败」。
 */
export function buildHealthView(payload: unknown, lang: string): HealthView {
  const copy = copyFor(lang)
  const body = asRecord(payload)

  if (body.unavailable === true) {
    const status = num(body.status)
    if (status === 401 || body.unauthenticated === true) {
      return { state: 'error', message: copy.errorUnauthenticated }
    }
    if (status === 0) return { state: 'error', message: copy.errorNetwork }
    return { state: 'error', message: copy.errorGeneric }
  }

  const error = asRecord(body.error)
  if (body.error && typeof body.error === 'object') {
    const code = typeof error.code === 'string' ? error.code : ''
    return {
      state: 'error',
      message: code === 'unauthenticated' ? copy.errorUnauthenticated : copy.errorGeneric,
    }
  }

  const unsupported = Array.isArray(body.unsupported) ? (body.unsupported as UnsupportedItem[]) : []
  if (!body.health || typeof body.health !== 'object') {
    if (unsupported.length) {
      const detail = unsupported
        .map((item) => item.detail || item.reason || '')
        .filter(Boolean)
        .join('；')
      return {
        state: 'unsupported',
        message: detail ? `${copy.unsupported}（${detail}）` : copy.unsupported,
      }
    }
    return { state: 'error', message: copy.errorGeneric }
  }

  const report = normalizeReport(body.health)
  const jobs = report.jobs as JobsHealth
  const reconcile = report.reconcile as ReconcileHealth
  const dispatch = report.dispatch as DispatchHealth
  const agents = report.agents as AgentHealth
  const window = asRecord(body.window) as WindowInfo
  const reasonLabels: Record<string, string> = lang === 'zh'
    ? { unspecified: '未标注原因' }
    : { unspecified: 'Unspecified' }

  const cards: MetricCard[] = []

  // 1. 作业总数：null 表示未采集，显示「未知」而不是 0。
  cards.push({
    key: 'total',
    label: copy.card.total,
    value: jobs.total === null ? copy.unknown : String(jobs.total),
    unit: '',
    detail:
      (jobs.total === null
        ? copy.noRows
        : Object.entries(jobs.by_kind)
            .sort((a, b) => b[1] - a[1])
            .map(([kind, n]) => `${copy.kind[kind] || kind} ${n}`)
            .join(' · ')) + ` · ${copy.basis.total}`,
    unknown: jobs.total === null,
    tone: 'muted',
  })

  // 严格口径（不含 escalated）作为补充证据，避免第二张几乎相同的卡占位。
  const strictDetail =
    jobs.success_rate_strict.ratio !== null && jobs.success_rate_strict.denominator > 0
      ? copy.strict(percent(jobs.success_rate_strict.ratio))
      : ''
  cards.push(
    ratioCard('success', copy.card.success, copy.basis.success, jobs.success_rate, copy, 'ok', strictDetail),
  )

  // 失败结果分布（契约指标 failure_reason_distribution）：数字是「结果」，明细在下方列表。
  const failureTotal =
    Object.values(jobs.failure_reasons).reduce((sum, count) => sum + count, 0) +
    jobs.unspecified_failures
  const topReason = Object.entries(jobs.failure_reasons).sort((a, b) => b[1] - a[1])[0]
  cards.push({
    key: 'failures',
    label: copy.card.failures,
    value: jobs.total === null ? copy.unknown : String(failureTotal),
    unit: '',
    detail:
      topReason
        ? `${topReason[0]} × ${topReason[1]} · ${copy.basis.failures}`
        : `${copy.noSample} · ${copy.basis.failures}`,
    unknown: jobs.total === null,
    tone: failureTotal > 0 ? 'warn' : 'muted',
  })

  cards.push({
    key: 'backlog',
    label: copy.card.backlog,
    value: String(jobs.backlog),
    unit: '',
    detail: jobs.backlog_oldest_seconds === null
      ? `${copy.oldest(copy.unknown)} · ${copy.basis.backlog}`
      : `${copy.oldest(seconds(jobs.backlog_oldest_seconds))} · ${copy.basis.backlog}`,
    unknown: false,
    tone: jobs.backlog > 0 ? 'warn' : 'muted',
  })

  cards.push(ratioCard('retry', copy.card.retry, copy.basis.retry, jobs.retry_rate, copy))
  cards.push(
    ratioCard('reclaim', copy.card.reclaim, copy.basis.reclaim, jobs.lease_reclaim_rate, copy, 'warn'),
  )
  cards.push(ratioCard('reconcile', copy.card.reconcile, copy.basis.reconcile, reconcile.rate, copy, 'ok'))
  cards.push(durationCard('mttr', copy.card.mttr, copy.basis.mttr, reconcile.mttr, copy))
  cards.push(durationCard('queue', copy.card.queue, copy.basis.queue, dispatch.queue_wait, copy))
  cards.push(durationCard('claim', copy.card.claim, copy.basis.claim, dispatch.claim_to_start, copy))
  cards.push(
    ratioCard('heartbeat', copy.card.heartbeat, copy.basis.heartbeat, agents.heartbeat_freshness, copy, 'ok'),
  )
  // 节点成功率在 v1 恒为 unknown：维度缺失要写在卡面上，不能静默留空。
  cards.push({
    key: 'node',
    label: copy.card.node,
    value: copy.unknown,
    unit: '',
    detail: `${agents.node_success_rate.note || copy.basis.node} · ${copy.basis.node}`,
    unknown: true,
    tone: 'muted',
  })

  const tables: HealthTable[] = [
    {
      key: 'kinds',
      title: copy.table.kinds,
      basis: copy.table.rowsBasis,
      rows: tableRows(jobs.by_kind, copy.kind),
    },
    {
      key: 'statuses',
      title: copy.table.statuses,
      basis: copy.table.rowsBasis,
      rows: tableRows(jobs.by_status, copy.status),
    },
    {
      key: 'reasons',
      title: copy.table.reasons,
      basis: copy.table.resultBasis,
      rows: tableRows(
        {
          ...jobs.failure_reasons,
          ...(jobs.unspecified_failures > 0 ? { unspecified: jobs.unspecified_failures } : {}),
        },
        reasonLabels,
      ),
    },
    {
      key: 'reconcile',
      title: copy.table.reconcile,
      basis: copy.table.reconcileBasis,
      rows: Object.entries({
        release: reconcile.release,
        keep: reconcile.keep,
        escalate: reconcile.escalate,
        unwired: reconcile.unwired,
      })
        .filter(([, count]) => count > 0)
        .map(([code, count]) => ({
          key: code,
          label:
            lang === 'zh'
              ? { release: '释放', keep: '保留', escalate: '升级', unwired: '未接线' }[code] || code
              : { release: 'Release', keep: 'Keep', escalate: 'Escalate', unwired: 'Unwired' }[code] || code,
          code,
          count,
        })),
    },
  ]

  const unknownNotes: string[] = []
  const missing = Array.isArray(body.missing) ? (body.missing as MissingItem[]) : []
  for (const item of missing) {
    const text = item.detail || item.section || item.reason || ''
    if (text) unknownNotes.push(text)
  }
  if (jobs.total === null) unknownNotes.push(copy.noRows)

  return {
    state: 'ready',
    cards,
    tables,
    windowText: formatWindow(window, copy),
    generatedText: report.generated_at ? copy.generated(report.generated_at) : copy.generated('—'),
    evidenceText: copy.evidence(report.job_rows || 0, report.run_rows || 0, report.agent_rows || 0),
    unknownNotes,
  }
}

export function scopeText(lang: string, projectId: string): string {
  const copy = copyFor(lang)
  const id = String(projectId || '').trim()
  return id ? copy.projectScope(id) : copy.allProjects
}
