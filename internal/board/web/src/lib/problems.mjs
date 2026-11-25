// 问题审查视图的纯数据层（BUX-FE-002）。
//
// 只消费 snapshot.problems（后端 BuildProblems 的 Problem[] 形状），
// 不拉第二份数据、不判断「问题该怎么处理」——这一页是只读审查。
// 纯 ESM：Vite 打进 IIFE，node --test 也能直接 import 做契约校验。

/** 分组顺序：先看严重度，同档内再看 kind；未知值落 other，绝不丢题。 */
export const SEVERITY_ORDER = Object.freeze(['high', 'medium', 'low', 'other'])
export const KIND_ORDER = Object.freeze([
  'router-idle', 'blocked', 'conflict', 'retry', 'question', 'stalled', 'dep-wait', 'blocker', 'router',
])

const SEVERITY_LABELS = Object.freeze({
  zh: { high: '高优先级', medium: '中优先级', low: '低优先级', other: '未分级' },
  en: { high: 'High', medium: 'Medium', low: 'Low', other: 'Unrated' },
})

const KIND_LABELS = Object.freeze({
  zh: {
    blocked: '阻塞', conflict: '冲突', retry: '重试', question: '提问',
    stalled: '停滞', 'dep-wait': '依赖等待', blocker: '阻塞事件',
    'router-idle': '无人接管', router: 'Router 介入',
  },
  en: {
    blocked: 'Blocked', conflict: 'Conflict', retry: 'Retry', question: 'Question',
    stalled: 'Stalled', 'dep-wait': 'Dependency wait', blocker: 'Blocker',
    'router-idle': 'Unattended', router: 'Router tasked',
  },
})

function str(value) {
  return value == null ? '' : String(value).trim()
}

function langKey(lang) {
  return lang === 'en' ? 'en' : 'zh'
}

export function severityLabel(severity, lang = 'zh') {
  const key = str(severity).toLowerCase()
  const table = SEVERITY_LABELS[langKey(lang)]
  return table[key] || table.other
}

export function kindLabel(kind, lang = 'zh') {
  const key = str(kind).toLowerCase()
  return KIND_LABELS[langKey(lang)][key] || key || '—'
}

/** 把后端载荷归一化成稳定形状；缺字段留空串，null/非数组一律当成「没问题」。 */
export function normalizeProblems(raw) {
  if (!Array.isArray(raw)) return []
  return raw
    .filter((x) => x && typeof x === 'object')
    .map((x) => ({
      project_id: str(x.project_id),
      project_name: str(x.project_name),
      task_id: str(x.task_id),
      kind: str(x.kind).toLowerCase() || 'unknown',
      severity: str(x.severity).toLowerCase() || 'other',
      message: str(x.message),
      ts: str(x.ts),
    }))
    .filter((x) => x.task_id || x.message || x.kind !== 'unknown')
}

function severityBucket(severity) {
  return SEVERITY_ORDER.includes(severity) ? severity : 'other'
}

function kindRank(kind) {
  const i = KIND_ORDER.indexOf(kind)
  return i < 0 ? KIND_ORDER.length : i
}

function compareProblem(a, b) {
  return (
    a.project_id.localeCompare(b.project_id) ||
    a.task_id.localeCompare(b.task_id) ||
    a.ts.localeCompare(b.ts)
  )
}

/**
 * 行点击的抽屉目标。没有 task_id 的问题（例如纯 Agent 信号）不可点，
 * 返回 null 让视图渲染成静态行，避免打开一个空抽屉。
 */
export function drawerTarget(problem) {
  const tid = str(problem && problem.task_id)
  if (!tid) return null
  return { tid, pid: str(problem && problem.project_id) }
}

/** 空态必须说明「当前筛选范围内」，否则用户会以为全局没问题。 */
export function emptyMessage(lang = 'zh', project = '') {
  const scope = str(project)
  if (langKey(lang) === 'en') {
    return scope
      ? `No runtime problems in the selected project (${scope}).`
      : 'No runtime problems in the current view.'
  }
  return scope
    ? `当前筛选范围内（工程 ${scope}）没有运行问题。`
    : '当前筛选范围内没有运行问题。'
}

/**
 * 按 severity → kind 两层分组，并按当前工程筛选联动。
 * 返回 { groups, total, allCount, project, counts }：
 * total 是筛选后条数，allCount 是快照原始条数，两者不等说明筛选真的生效了。
 */
export function buildProblemGroups(raw, options = {}) {
  const lang = langKey(options.lang)
  const project = str(options.project)
  const all = normalizeProblems(raw)
  const scoped = project
    ? all.filter((p) => p.project_id === project || p.project_name === project)
    : all

  const buckets = new Map()
  const counts = { high: 0, medium: 0, low: 0, other: 0 }
  for (const problem of scoped) {
    const sev = severityBucket(problem.severity)
    counts[sev] += 1
    if (!buckets.has(sev)) buckets.set(sev, new Map())
    const byKind = buckets.get(sev)
    if (!byKind.has(problem.kind)) byKind.set(problem.kind, [])
    byKind.get(problem.kind).push(problem)
  }

  const groups = []
  for (const sev of SEVERITY_ORDER) {
    const byKind = buckets.get(sev)
    if (!byKind || !byKind.size) continue
    const names = [...byKind.keys()].sort(
      (a, b) => kindRank(a) - kindRank(b) || a.localeCompare(b),
    )
    const kinds = names.map((kind) => ({
      kind,
      label: kindLabel(kind, lang),
      items: byKind.get(kind).slice().sort(compareProblem),
    }))
    groups.push({
      severity: sev,
      label: severityLabel(sev, lang),
      count: kinds.reduce((n, k) => n + k.items.length, 0),
      kinds,
    })
  }
  return { groups, total: scoped.length, allCount: all.length, project, counts }
}
