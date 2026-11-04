<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import ViewToggle from './ViewToggle.vue'

const board = useBoard()
const snap = computed(() => board.state.snapshot)
const project = computed(() => board.state.route.project || '')
/**
 * 工程筛选头服务流程视图（看板／工坊／拓扑／执行）。
 *
 * 执行视图也在内：它按工程的迭代聚合，读的正是这份快照与这套 agent/status 筛选，
 * 而且它是「流程」组的一员——把视图切换器藏起来，用户就只能靠侧栏回看板再切。
 */
const showChrome = computed(() => {
  const view = board.state.route.view
  return Boolean(
    project.value &&
      (view === 'board' || view === 'factory' || view === 'topo' || view === 'execution'),
  )
})
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

/**
 * 迭代下拉的两个哨兵值。
 *
 * 路由里 `iteration` 是三态：null＝不筛、空串＝只看未归属、其余＝具体迭代。
 * `<select>` 的 value 只能是字符串，所以「不筛」与「未归属」各用一个哨兵，
 * 提交时再翻回路由值——否则选「未归属」会被当成「不筛」而丢筛选。
 */
const ALL_ITERATIONS = '__all__'
const UNASSIGNED_ITERATION = '__unassigned__'

const iterationOptions = computed(() =>
  (snap.value?.iterations || []).filter((it) => it && String(it.id || '').trim()),
)

const iterationValue = computed(() => {
  const value = board.state.route.iteration
  if (value === null || value === undefined) return ALL_ITERATIONS
  return value === '' ? UNASSIGNED_ITERATION : String(value)
})

function onIterationChange(event: Event) {
  const value = (event.target as HTMLSelectElement).value
  const iteration = value === ALL_ITERATIONS ? null : value === UNASSIGNED_ITERATION ? '' : value
  board.patchFilters({ iteration })
}

const taskCountLabel = computed(() => {
  const n = snap.value?.tasks?.length ?? 0
  const total = snap.value?.total ?? n
  return board.state.lang === 'zh' ? `${n} 个任务 · 共 ${total}` : `${n} tasks · ${total} total`
})

function onFilterChange(field: 'agent' | 'status', event: Event) {
  const value = (event.target as HTMLSelectElement).value || ''
  board.patchFilters({ [field]: value })
}

function onReset() {
  board.patchFilters({ agent: '', status: '', search: '', iteration: null })
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
    <div class="filters" id="filters">
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
          {{ a.name || a.id }}
        </option>
      </select>
      <select
        id="f-status"
        :value="board.state.route.status"
        aria-label="Status"
        @change="onFilterChange('status', $event)"
      >
        <option value="">{{ board.state.lang === 'zh' ? '全部状态' : 'All statuses' }}</option>
        <option v-for="st in statusOptions" :key="st" :value="st">{{ st }}</option>
      </select>
      <select
        id="f-iteration"
        :value="iterationValue"
        aria-label="Iteration"
        @change="onIterationChange"
      >
        <option :value="ALL_ITERATIONS">
          {{ board.state.lang === 'zh' ? '全部迭代' : 'All iterations' }}
        </option>
        <option
          v-for="it in iterationOptions"
          :key="String(it.project_id || '') + '/' + String(it.id)"
          :value="String(it.id)"
        >
          {{ it.name ? `${it.name} · ${it.id}` : it.id }}
        </option>
        <option :value="UNASSIGNED_ITERATION">
          {{ board.state.lang === 'zh' ? '未归属迭代' : 'No iteration' }}
        </option>
      </select>
      <button type="button" id="f-reset" class="quiet-button" @click="onReset">
        {{ board.state.lang === 'zh' ? '重置' : 'Reset' }}
      </button>
    </div>
    <div class="toolbar-right">
      <!--
        配置入口只留一个：主题／语言归顶栏，节点接入、容量、受管执行与保留归设置页。
        流程视图不再各自摆开关，这里只给一个去设置页的跳转，免得同一件事有三个入口。
      -->
      <button
        type="button"
        id="settings-entry"
        class="quiet-button"
        data-settings-entry="1"
        :aria-label="board.state.lang === 'zh' ? '打开设置' : 'Open settings'"
        :title="board.state.lang === 'zh' ? '设置' : 'Settings'"
        @click="board.navigate('settings')"
      >
        ⚙
      </button>
      <div class="board-meta-left">
        <ViewToggle />
        <span id="board-count">{{ taskCountLabel }}</span>
      </div>
      <label class="search-box">
        <span aria-hidden="true">⌕</span>
        <input
          id="task-search"
          v-model="search"
          type="search"
          :placeholder="board.state.lang === 'zh' ? '搜索任务…' : 'Search tasks…'"
          @input="onSearchInput"
        />
        <kbd aria-hidden="true">/</kbd>
      </label>
    </div>
  </div>
</template>
