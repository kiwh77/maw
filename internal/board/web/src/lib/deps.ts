/**
 * 依赖可读性纯函数：上游解析、chips 着色分组、等待标记。
 *
 * 只做计算，不碰 store：BoardView 的 chips、等待行与 TaskDrawer 的
 * 依赖面板共用同一份规则，避免同一依赖在两处显示不同状态。
 */

export type DepTask = {
  id: unknown
  project_id?: unknown
  title?: unknown
  status?: unknown
  deps?: unknown
}

export type DepRow = {
  id: string
  pid: string
  title: string
  status: string
}

/** 卡片 deps 规整为字符串数组：非数组、空串、占位一律丢弃。 */
export function taskDeps(task: { deps?: unknown } | null | undefined): string[] {
  if (!task || !Array.isArray(task.deps)) return []
  return task.deps.map((d) => String(d ?? '').trim()).filter(Boolean)
}

/**
 * 按 id 找上游任务。
 *
 * 任务 ID 只在工程内唯一：优先取同工程命中，其次取任意命中，
 * 都没有就是快照里看不到的上游（返回 null，调用方按未知状态降级）。
 */
export function findUpstream(
  depId: string,
  task: { project_id?: unknown } | null | undefined,
  tasks: DepTask[],
): DepTask | null {
  const pid = String(task?.project_id ?? '')
  let fallback: DepTask | null = null
  for (const t of tasks) {
    if (String(t?.id ?? '') !== depId) continue
    if (pid && String(t?.project_id ?? '') === pid) return t
    if (!fallback) fallback = t
  }
  return fallback
}

export type DepTone = 'ok' | 'active' | 'waiting' | 'danger'

/**
 * 上游状态 → chips 底色分组。
 *
 * 复用页面既有状态色语义：done=完成色（--ok），claimed/in_progress 等
 * 活跃色（--warn），blocked/conflict 受阻色（--danger），
 * backlog/ready 及未知状态归等待色（--muted）。
 */
export function depTone(status: unknown): DepTone {
  const s = String(status ?? '')
  if (s === 'done' || s === 'cancelled') return 'ok'
  if (s === 'blocked' || s === 'conflict') return 'danger'
  if (s === 'claimed' || s === 'in_progress' || s === 'self_check' || s === 'in_review' || s === 'merging') {
    return 'active'
  }
  return 'waiting'
}

/** 上游是否已落定（done / cancelled 都算完成，不再阻塞下游）。 */
export function depSatisfied(status: unknown): boolean {
  const s = String(status ?? '')
  return s === 'done' || s === 'cancelled'
}

/** deps 规整为可渲染行：上游在快照中就用它的工程与状态，否则按未知降级。 */
export function depRows(
  task: { deps?: unknown; project_id?: unknown } | null | undefined,
  tasks: DepTask[],
): DepRow[] {
  return taskDeps(task).map((id) => {
    const up = findUpstream(id, task, tasks)
    return {
      id,
      pid: String(up?.project_id ?? task?.project_id ?? ''),
      title: String(up?.title ?? ''),
      status: String(up?.status ?? ''),
    }
  })
}

/** deps 中未完成的上游（含快照里找不到的）：等待标记只数这些。 */
export function unsatisfiedDeps(
  task: { deps?: unknown; project_id?: unknown } | null | undefined,
  tasks: DepTask[],
): DepRow[] {
  return depRows(task, tasks).filter((row) => !depSatisfied(row.status))
}
