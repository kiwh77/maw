<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useBoard } from './stores/board'
import { groupLabelKey, resolveView } from './lib/route'
import AppSidebar from './components/AppSidebar.vue'
import AppChrome from './components/AppChrome.vue'
import ProjectSelect from './components/ProjectSelect.vue'
import VnextHost from './components/VnextHost.vue'
import TaskDrawer from './components/TaskDrawer.vue'
import JoinPromptButton from './components/JoinPromptButton.vue'
import BoardView from './views/BoardView.vue'
import FactoryView from './views/FactoryView.vue'
import TopoView from './views/TopoView.vue'

const board = useBoard()
const def = computed(() => resolveView(board.registry, board.state.route.view))
const scopeLabel = computed(() => {
  const key = groupLabelKey(def.value?.group)
  return key ? board.t(key) : ''
})
/** 流程视图 + 已选工程：标题区展示醒目的接入提示词按钮。 */
const showJoinPrompt = computed(() => {
  const view = board.state.route.view
  return Boolean(
    board.state.route.project &&
      (view === 'board' || view === 'factory' || view === 'topo'),
  )
})

let pollTimer: ReturnType<typeof setInterval> | null = null
let eventStream: EventSource | null = null

function onPopState() {
  board.syncFromLocation()
}

function applyTheme(value: string) {
  const root = document.documentElement
  if (value === 'system') delete root.dataset.theme
  else root.dataset.theme = value
  try {
    localStorage.setItem('maw-theme', value)
  } catch {
    /* about:blank / 沙箱下可能拒绝 storage */
  }
}

function readTheme() {
  try {
    return localStorage.getItem('maw-theme') || 'system'
  } catch {
    return 'system'
  }
}

onMounted(async () => {
  board.setLang(board.state.lang)
  window.addEventListener('popstate', onPopState)
  window.addEventListener('hashchange', onPopState)
  await board.fetchSnapshot()
  // 有工程筛选时仍拉一份全量任务数，供顶部下拉标识「有任务」。
  if (board.state.route.project) void board.refreshProjectTaskCounts()
  eventStream = board.connectEvents()
  pollTimer = setInterval(() => void board.fetchSnapshot(), 3000)
  if (!def.value?.legacy && def.value?.dataKind) await board.loadView(def.value.dataKind || def.value.id)
})

onBeforeUnmount(() => {
  window.removeEventListener('popstate', onPopState)
  window.removeEventListener('hashchange', onPopState)
  if (pollTimer) clearInterval(pollTimer)
  eventStream?.close()
})

watch(
  () => [board.state.route.view, board.state.route.project],
  async () => {
    await board.fetchSnapshot()
    const d = def.value
    if (d && !d.legacy) await board.loadView(d.dataKind || d.id)
  },
)

const themeValue = readTheme()
</script>

<template>
  <div class="app" id="app-shell">
    <AppSidebar />
    <main class="workspace">
      <header class="topbar">
        <div class="breadcrumb">
          <span class="dim">{{ board.t('nav.workbench') }}</span>
          <span class="slash">/</span>
          <strong v-if="scopeLabel" id="crumb-scope">{{ scopeLabel }}</strong>
          <span v-if="scopeLabel" class="slash">/</span>
          <ProjectSelect />
          <!-- 兼容旧选择器：工程名由顶部下拉展示 -->
          <span id="workspace-name" hidden>{{ board.state.route.project || '' }}</span>
        </div>
        <div class="topbar-tools">
          <div class="sub" id="subtitle" role="status">{{ board.state.subtitle }}</div>
          <select
            id="lang-select"
            class="theme-select"
            :value="board.state.lang"
            aria-label="Language"
            @change="board.setLang(($event.target as HTMLSelectElement).value)"
          >
            <option value="en">EN</option>
            <option value="zh">中</option>
          </select>
          <select
            id="theme-select"
            class="theme-select"
            :value="themeValue"
            aria-label="Theme"
            @change="applyTheme(($event.target as HTMLSelectElement).value)"
          >
            <option value="system">System</option>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </select>
        </div>
      </header>
      <div class="content">
        <div class="page-heading">
          <div>
            <div class="eyebrow">{{ board.t(def?.eyebrowKey || 'eyebrow.board') }}</div>
            <h1 id="page-h1">{{ board.t(def?.headingKey || 'heading.board') }}</h1>
            <p class="page-description" id="page-desc">
              {{ board.t(def?.descKey || 'desc.board') }}
            </p>
          </div>
          <JoinPromptButton v-if="showJoinPrompt" variant="join" />
        </div>
        <AppChrome />
        <BoardView v-if="board.state.route.view === 'board'" />
        <FactoryView v-else-if="board.state.route.view === 'factory'" />
        <TopoView v-else-if="board.state.route.view === 'topo'" />
        <VnextHost v-else :view-id="board.state.route.view" />
      </div>
    </main>
    <TaskDrawer />
  </div>
</template>
