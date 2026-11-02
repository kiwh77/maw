<script setup lang="ts">
import { computed } from 'vue'
import { useBoard } from '../stores/board'

const board = useBoard()

const LANES = [
  { id: 'ready', label: 'Ready', statuses: ['ready'], symbol: '▷', empty: '—' },
  { id: 'active', label: 'Active', statuses: ['claimed', 'in_progress', 'self_check'], symbol: '◉', empty: '—' },
  { id: 'review', label: 'Review', statuses: ['in_review', 'merging'], symbol: '◎', empty: '—' },
  { id: 'attention', label: 'Attention', statuses: ['blocked', 'conflict'], symbol: '!', empty: '—' },
  { id: 'done', label: 'Done', statuses: ['done'], symbol: '✓', empty: '—' },
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

const lanes = computed(() =>
  LANES.map((lane) => ({
    ...lane,
    items: tasks.value.filter((t) => lane.statuses.includes(String(t.status))),
  })).filter((lane) => lane.id !== 'attention' || lane.items.length > 0),
)

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
</script>

<template>
  <div class="board" id="columns" aria-label="Task flow">
    <section
      v-for="(lane, i) in lanes"
      :key="lane.id"
      class="col"
      :data-lane="lane.id"
      :aria-label="lane.label"
    >
      <h3>
        <span class="lane-index">{{ String(i + 1).padStart(2, '0') }}</span>
        {{ lane.label }}
        <span class="count">{{ lane.items.length }}</span>
        <span class="lane-symbol" aria-hidden="true">{{ lane.symbol }}</span>
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
            :data-tid="String(task.id)"
            :data-pid="String(task.project_id || '')"
            @click="openTask(task)"
            @keydown="onCardKey($event, task)"
          >
            <div class="card-top">
              <span class="id">{{ task.id }}</span>
              <span class="badge">{{ task.status }}</span>
            </div>
            <h4 class="task-title">{{ task.title }}</h4>
            <div class="card-project">{{ task.project_name || task.project_id || '—' }}</div>
            <div v-if="task.blocked_reason" class="task-reason">{{ task.blocked_reason }}</div>
            <div class="card-bottom">
              <span class="owner">{{ task.owner || (board.state.lang === 'zh' ? '未指派' : 'unassigned') }}</span>
              <span class="priority">P{{ task.priority ?? '—' }}</span>
            </div>
          </article>
        </template>
        <div v-else class="lane-empty">
          <span aria-hidden="true">{{ lane.symbol }}</span>{{ lane.empty }}
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
