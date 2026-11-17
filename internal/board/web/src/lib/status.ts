export const STATUS_LABELS: Record<string, Record<string, string>> = {
  zh: {
    backlog: '待排期',
    ready: '可领取',
    claimed: '已领取',
    in_progress: '进行中',
    self_check: '自检中',
    in_review: '评审中',
    merging: '合并中',
    conflict: '冲突',
    blocked: '阻塞',
    done: '已完成',
    cancelled: '已取消',
  },
  en: {
    backlog: 'Backlog',
    ready: 'Ready',
    claimed: 'Claimed',
    in_progress: 'In progress',
    self_check: 'Self-check',
    in_review: 'In review',
    merging: 'Merging',
    conflict: 'Conflict',
    blocked: 'Blocked',
    done: 'Done',
    cancelled: 'Cancelled',
  },
}


export function statusLabel(status: unknown, lang: string): string {
  const key = String(status || '')
  return STATUS_LABELS[lang === 'zh' ? 'zh' : 'en'][key] || key || '—'
}
