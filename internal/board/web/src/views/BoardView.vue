<script setup lang="ts">
import { computed } from 'vue'
import { statusLabel } from '../lib/status'
import { useBoard } from '../stores/board'
import { depRows, depTone, taskDeps, unsatisfiedDeps } from '../lib/deps'

const board = useBoard()

const LANES = [
  // 泳道必须覆盖全部状态：backlog 在 Queued；cancelled 单独一列，
  // 避免与 done 混在「已完成」里仍挂 blocked_reason 造成误解。
  { id: 'queued', label: 'Queued', statuses: ['backlog', 'ready'], symbol: '▷', empty: '—' },
  { id: 'active', label: 'Active', statuses: ['claimed', 'in_progress', 'self_check'], symbol: '◉', empty: '—' },
  { id: 'review', label: 'Review', statuses: ['in_review', 'merging'], symbol: '◎', empty: '—' },
  { id: 'attention', label: 'Attention', statuses: ['blocked', 'conflict'], symbol: '!', empty: '—' },
  { id: 'done', label: 'Done', statuses: ['done'], symbol: '✓', empty: '—' },
  { id: 'cancelled', label: 'Cancelled', statuses: ['cancelled'], symbol: '×', empty: '—' },
]

const tasks = computed(() => {
  const all = board.state.snapshot?.tasks || []
  const q = String(board.state.route.search || '').trim().toLowerCase()
  if (!q) return all
  return all.filter((t) =>
    ['id', 'title', 'owner', 'project_name', 'project_id'].some((k) =>
      String(t[k] ?? '').toLowerCase().includes(q),
    ),
  )
})

/** 契约外的状态不丢：给它们单独一列，而不是静默从看板上消失。 */
const extraLane = computed(() => {
  const known = new Set(LANES.flatMap((lane) => lane.statuses))
  // 按「不属于任何已知泳道」判定，而不是按状态字符串集合：
  // 状态缺失（空串）的任务同样要落位，否则泳道计数之和会少于任务总数。
  const items = tasks.value.filter((t) => !known.has(String(t.status || '')))
  if (!items.length) return null
  const labels = [...new Set(items.map((t) => String(t.status || '') || '—'))].sort()
  return {
    id: 'other',
    label: 'Other',
    statuses: labels,
    symbol: '?',
    empty: '—',
    items,
  }
})

const lanes = computed(() => {
  const base = LANES.map((lane) => ({
    ...lane,
    items: tasks.value.filter((t) => lane.statuses.includes(String(t.status))),
  }))
  if (extraLane.value) base.push(extraLane.value)
  return base.filter((lane) => {
    if (lane.id === 'attention' || lane.id === 'cancelled') {
      return lane.items.length > 0
    }
    return true
  })
})

/** 泳道计数之和：必须等于落在看板上的任务数，否则有卡片在泳道外丢了。 */
const laneTotal = computed(() => lanes.value.reduce((n, lane) => n + lane.items.length, 0))

const drawer = computed(() => board.state.drawer)

function openTask(task: Record<string, unknown>) {
  board.openTaskDrawer(String(task.id || ''), String(task.project_id || ''))
}

function onCardKey(event: KeyboardEvent, task: Record<string, unknown>) {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    openTask(task)
  }
}

function isActive(task: Record<string, unknown>) {
  const d = drawer.value
  if (!d) return false
  return String(task.id) === d.tid && String(task.project_id || '') === String(d.pid || '')
}

function allTasks(): Array<Record<string, unknown>> {
  return (board.state.snapshot?.tasks || []) as Array<Record<string, unknown>>
}

/** 卡片依赖 chips：上游 ID + 状态色，点击开上游抽屉。 */
function depChips(task: Record<string, unknown>) {
  return depRows(task, allTasks()).map((row) => ({
    ...row,
    tone: depTone(row.status),
  }))
}

function openDep(row: { id: string; pid: string }) {
  board.openTaskDrawer(row.id, row.pid)
}

function onChipKey(event: KeyboardEvent, row: { id: string; pid: string }) {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    event.stopPropagation()
    openDep(row)
  }
}

/**
 * 等待标记：仅 backlog/ready 卡片，列出尚未完成的上游（含快照外未知状态）。
 * 活跃泳道的卡片靠 chips 表达同样的信息，不再重复一行。
 */
function waitingText(task: Record<string, unknown>) {
  const status = String(task.status || '')
  if (status !== 'backlog' && status !== 'ready') return ''
  const list = unsatisfiedDeps(task, allTasks())
  if (!list.length) return ''
  const head = board.state.lang === 'zh' ? '等待' : 'Waiting'
  const body = list
    .map((row) => `${row.id}(${row.status || (board.state.lang === 'zh' ? '未知' : 'unknown')})`)
    .join(', ')
  return `${head}: ${body}`
}
</script>

<template>
  <div
    class="board"
    id="columns"
    aria-label="Task flow"
    :data-total="tasks.length"
    :data-lane-total="laneTotal"
  >
    <section
      v-for="(lane, i) in lanes"
      :key="lane.id"
      class="col"
      :data-lane="lane.id"
      :aria-label="lane.label"
    >
      <h3>
        <span class="lane-index">{{ String(i + 1).padStart(2, '0') }}</span>
        {{ board.state.lang === 'zh' ? ({ queued: '待处理', active: '进行中', review: '评审中', attention: '需关注', done: '已完成', cancelled: '已取消', other: '其他' } as Record<string, string>)[lane.id] : lane.label }}
        <span class="count">{{ lane.items.length }}</span>

      </h3>
      <div class="col-body">
        <template v-if="lane.items.length">
          <article
            v-for="task in lane.items"
            :key="String(task.id) + String(task.project_id || '')"
            class="card clickable"
            :class="{ active: isActive(task) }"
            tabindex="0"
            role="button"
            :data-status="String(task.status || '')"
            :data-tid="String(task.id)"
            :data-pid="String(task.project_id || '')"
            @click="openTask(task)"
            @keydown="onCardKey($event, task)"
          >
            <div class="card-top">
              <span class="id">{{ task.id }}</span>
              <span class="badge" :class="'badge--' + task.status">{{ statusLabel(task.status, board.state.lang) }}</span>
            </div>
            <h4 class="task-title">{{ task.title }}</h4>
            <div class="card-project">{{ task.project_name || task.project_id || '—' }}</div>
            <div
              v-if="task.blocked_reason && task.status !== 'done' && task.status !== 'cancelled'"
              class="task-reason"
            >{{ task.blocked_reason }}</div>
            <div v-if="taskDeps(task).length" class="card-deps" :data-deps="taskDeps(task).length">
              <button
                v-for="chip in depChips(task)"
                :key="chip.id"
                type="button"
                class="dep-chip"
                :data-dep-tone="chip.tone"
                :data-dep="chip.id"
                :title="chip.status || (board.state.lang === 'zh' ? '快照中未见该上游' : 'upstream not in snapshot')"
                @click.stop="openDep(chip)"
                @keydown="onChipKey($event, chip)"
              >
                {{ chip.id }}
              </button>
            </div>
            <div
              v-if="waitingText(task)"
              class="task-waiting"
              data-waiting="true"
            >{{ waitingText(task) }}</div>
            <div class="card-bottom">
              <span class="owner">{{ task.owner || (board.state.lang === 'zh' ? '未指派' : 'unassigned') }}</span>
              <span class="priority">P{{ task.priority ?? '—' }}</span>
            </div>
          </article>
        </template>
        <div v-else class="lane-empty">
          {{ board.state.lang === 'zh' ? '暂无任务' : 'No tasks' }}
        </div>
      </div>
    </section>
  </div>
  <div class="hint-center" id="hint-center">
    <div class="hint-next" id="next">
      <b>NEXT</b>
      <span id="next-hint-text">{{ board.state.snapshot?.next_hint || '—' }}</span>
    </div>
    <div
      class="hint-risks"
      :class="{ empty: !(board.state.snapshot?.risks || []).length }"
      id="risks"
    >
      <div v-for="(r, i) in board.state.snapshot?.risks || []" :key="i">{{ r }}</div>
    </div>
  </div>
</template>
