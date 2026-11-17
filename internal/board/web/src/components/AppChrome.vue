<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import { statusLabel } from '../lib/status'
import UiIcon from './UiIcon.vue'
import ViewToggle from './ViewToggle.vue'
import JoinPromptButton from './JoinPromptButton.vue'

const board = useBoard()
const snap = computed(() => board.state.snapshot)
/**
 * 工程筛选头服务流程视图（看板／工坊／拓扑／执行）。
 *
 * 执行视图也在内：它按工程的迭代聚合，读的正是这份快照与这套 agent/status 筛选，
 * 而且它是「流程」组的一员——把视图切换器藏起来，用户就只能靠侧栏回看板再切。
 */
const showChrome = computed(() => {
  const view = board.state.route.view
  return Boolean(
    (view === 'board' || view === 'factory' || view === 'topo' || view === 'execution'),
  )
})
/** 流程视图 + 已选工程：工具栏筛选左侧展示接入按钮。 */
const showJoinPrompt = computed(() => {
  const view = board.state.route.view
  return Boolean(
    board.state.route.project &&
      (view === 'board' || view === 'factory' || view === 'topo'),
  )
})
const filtersOpen = ref(!window.matchMedia('(max-width: 1100px)').matches)
const filterCount = computed(() => ['agent', 'status', 'iteration'].filter(k => Boolean(board.state.route[k as 'agent' | 'status' | 'iteration'])).length)
const search = ref(board.state.route.search || '')
let searchTimer: ReturnType<typeof setTimeout> | null = null

watch(
  () => board.state.route.search,
  (v) => {
    search.value = v || ''
  },
)

const stats = computed(() => {
  const s = snap.value
  if (!s) return []
  const pct = Math.max(0, Math.min(100, Number(s.percent) || 0))
  const zh = board.state.lang === 'zh'
  return [
    { label: zh ? '进度' : 'Progress', value: `${pct}%` },
    { label: zh ? '总数' : 'Total', value: String(s.total ?? 0) },
    { label: zh ? '进行中' : 'Active', value: String(s.active ?? 0) },
    { label: zh ? '阻塞' : 'Blocked', value: String(s.blocked ?? 0) },
    { label: zh ? '完成' : 'Done', value: String(s.done ?? 0) },
  ]
})

const agentOptions = computed(() => snap.value?.agents || [])
const statusOptions = computed(() => {
  const counts = snap.value?.counts || {}
  return Object.keys(counts).sort()
})
/** 迭代选项：快照列出的迭代 + 任务上出现过但未登记的 iteration_id。 */
const iterationOptions = computed(() => {
  const fromSnap = (snap.value?.iterations || []).map((it) => ({
    id: String(it.id || '').trim(),
    name: String(it.name || it.id || '').trim(),
  })).filter((it) => it.id)
  const seen = new Set(fromSnap.map((it) => it.id))
  for (const task of snap.value?.tasks || []) {
    const id = String((task as { iteration_id?: string }).iteration_id || '').trim()
    if (id && !seen.has(id)) {
      seen.add(id)
      fromSnap.push({ id, name: id })
    }
  }
  return fromSnap.sort((a, b) => a.id.localeCompare(b.id))
})

const taskCountLabel = computed(() => {
  const n = snap.value?.tasks?.length ?? 0
  const total = snap.value?.total ?? n
  return board.state.lang === 'zh' ? `显示 ${n} / 全部 ${total}` : `Showing ${n} / ${total}`
})

function onFilterChange(field: 'agent' | 'status' | 'iteration', event: Event) {
  const value = (event.target as HTMLSelectElement).value || ''
  board.patchFilters({ [field]: value })
}

function onReset() {
  board.patchFilters({ agent: '', status: '', iteration: '', search: '' })
  search.value = ''
}

function onSearchInput() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => board.patchFilters({ search: search.value }), 180)
}

/** 斜杠聚焦搜索：输入态不劫持，修饰键组合放行（避免和浏览器快捷键打架）。 */
function onShortcut(event: KeyboardEvent) {
  if (event.key !== '/' || event.metaKey || event.ctrlKey || event.altKey) return
  // 输入法组合中的斜杠是候选字符：抢焦点会把中文输入打断。
  if (event.isComposing) return
  // 模态抽屉打开时不抢焦点：那会把焦点从对话框里拽走。
  if (document.querySelector('#drawer.open')) return
  const el = event.target as HTMLElement | null
  // 输入态判定用 closest：事件目标可能是输入框里的子节点。
  const inField = Boolean(
    el?.closest?.('input, textarea, select, [contenteditable=""], [contenteditable="true"], [role="textbox"]'),
  )
  if (inField) return
  const input = document.getElementById('task-search') as HTMLInputElement | null
  if (!input) return
  event.preventDefault()
  input.focus()
  input.select()
}

onMounted(() => window.addEventListener('keydown', onShortcut))
onBeforeUnmount(() => window.removeEventListener('keydown', onShortcut))
</script>

<template>
  <section
    v-if="showChrome"
    class="stats"
    id="stats"
    aria-label="Task stats for current filters"
  >
    <div v-for="item in stats" :key="item.label" class="stat">
      <div class="stat-label">{{ item.label }}</div>
      <div class="stat-value">{{ item.value }}</div>
    </div>
  </section>
  <div v-if="showChrome" class="toolbar">

    <div class="toolbar-right">
      <div class="board-meta-left">
        <ViewToggle />
        <span id="board-count">{{ taskCountLabel }}</span>
      </div>
      <label class="search-box">
        <UiIcon name="search" />
        <input
          id="task-search"
          v-model="search"
          type="search"
          :aria-label="board.state.lang === 'zh' ? '搜索任务' : 'Search tasks'"
          :placeholder="board.state.lang === 'zh' ? '搜索任务…' : 'Search tasks…'"
          @input="onSearchInput"
        />
        <kbd aria-hidden="true">/</kbd>
      </label>
      <JoinPromptButton v-if="showJoinPrompt" variant="join" />
      <button id="filter-toggle" type="button" class="quiet-button filter-toggle"
        aria-controls="filters" :aria-expanded="filtersOpen" :class="{ active: filtersOpen || filterCount }"
        @click="filtersOpen = !filtersOpen">
        <UiIcon name="filters" /><span>{{ board.state.lang === 'zh' ? '筛选' : 'Filters' }}</span>
        <span v-if="filterCount" class="filter-count">{{ filterCount }}</span>
      </button>
    </div>
    <div v-show="filtersOpen" class="filters" id="filters">
      <select
        id="f-agent"
        :value="board.state.route.agent"
        aria-label="Agent"
        @change="onFilterChange('agent', $event)"
      >
        <option value="">{{ board.state.lang === 'zh' ? '全部 Agent' : 'All Agents' }}</option>
        <option
          v-for="a in agentOptions"
          :key="String(a.id)"
          :value="String(a.id)"
        >
          {{ typeof a.name === 'string' ? a.name : a.id }}
        </option>
      </select>
      <select
        id="f-status"
        :value="board.state.route.status"
        aria-label="Status"
        @change="onFilterChange('status', $event)"
      >
        <option value="">{{ board.state.lang === 'zh' ? '全部状态' : 'All statuses' }}</option>
        <option v-for="st in statusOptions" :key="st" :value="st">{{ statusLabel(st, board.state.lang) }}</option>
      </select>
      <select
        id="f-iteration"
        :value="board.state.route.iteration"
        aria-label="Iteration"
        @change="onFilterChange('iteration', $event)"
      >
        <option value="">{{ board.state.lang === 'zh' ? '全部迭代' : 'All iterations' }}</option>
        <option
          v-for="it in iterationOptions"
          :key="it.id"
          :value="it.id"
        >
          {{ it.name || it.id }}
        </option>
      </select>
      <button type="button" id="f-reset" class="quiet-button" @click="onReset">
        {{ board.state.lang === 'zh' ? '重置' : 'Reset' }}
      </button>
    </div>
  </div>
</template>
