<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, watch, ref, nextTick } from 'vue'
import { useBoard } from './stores/board'
import { groupLabelKey, resolveView } from './lib/route'
import UiIcon from './components/UiIcon.vue'
import AppSidebar from './components/AppSidebar.vue'
import AppChrome from './components/AppChrome.vue'
import ProjectSelect from './components/ProjectSelect.vue'
import VnextHost from './components/VnextHost.vue'
import TaskDrawer from './components/TaskDrawer.vue'
import NodeEditorModal from './components/NodeEditorModal.vue'
import ProjectEditorModal from './components/ProjectEditorModal.vue'
import BoardView from './views/BoardView.vue'
import FactoryView from './views/FactoryView.vue'
import TopoView from './views/TopoView.vue'
import ProblemsView from './views/ProblemsView.vue'
// 统计页承载两个页签：原有「用量统计」+ SH-503「自愈统计」。
// 注册表不在本卡写域内，因此沿用已注册的 analytics 路由，避免改侧栏/深链。
import AnalyticsView from './views/AnalyticsView.vue'

const board = useBoard()
const compactViewport = window.matchMedia('(max-width: 1100px)')
const sidebarCollapsed = ref(compactViewport.matches)
const sidebarToggle = ref<HTMLButtonElement | null>(null)

function onViewportChange() {
  sidebarCollapsed.value = compactViewport.matches
}

async function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value
  if (!sidebarCollapsed.value && compactViewport.matches) {
    await nextTick()
    document.querySelector<HTMLButtonElement>('#vnext-nav button')?.focus()
  }
}

function closeSidebar() {
  sidebarCollapsed.value = true
  sidebarToggle.value?.focus()
}

function onSidebarEscape(event: KeyboardEvent) {
  if (event.key === 'Escape' && !sidebarCollapsed.value && compactViewport.matches) {
    closeSidebar()
  }
}

watch(() => board.state.route.view, () => {
  if (compactViewport.matches && !sidebarCollapsed.value) closeSidebar()
})
const def = computed(() => resolveView(board.registry, board.state.route.view))
const scopeLabel = computed(() => {
  const key = groupLabelKey(def.value?.group)
  return key ? board.t(key) : ''
})
let pollTimer: ReturnType<typeof setInterval> | null = null
let eventStream: EventSource | null = null

function onPopState() {
  board.syncFromLocation()
}

function applyTheme(value: string) {
  themeValue.value = value
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
  window.addEventListener('keydown', onSidebarEscape)
  compactViewport.addEventListener('change', onViewportChange)
  window.addEventListener('hashchange', onPopState)
  await board.fetchSnapshot()
  // 有工程筛选时仍拉一份全量任务数，供顶部下拉标识「有任务」。
  if (board.state.route.project) void board.refreshProjectTaskCounts()
  eventStream = board.connectEvents()
  pollTimer = setInterval(() => void board.fetchSnapshot(), 3000)
  // 问题审查视图直接读 snapshot.problems，不需要旧 sources.problems() 再取一次。
  if (!def.value?.legacy && def.value?.dataKind && def.value.id !== 'problems') {
    await board.loadView(def.value.dataKind || def.value.id)
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('popstate', onPopState)
  window.removeEventListener('keydown', onSidebarEscape)
  compactViewport.removeEventListener('change', onViewportChange)
  window.removeEventListener('hashchange', onPopState)
  if (pollTimer) clearInterval(pollTimer)
  eventStream?.close()
})

watch(
  () => [board.state.route.view, board.state.route.project],
  async () => {
    await board.fetchSnapshot()
    const d = def.value
    if (d && !d.legacy && d.id !== 'problems') await board.loadView(d.dataKind || d.id)
  },
)

const themeValue = ref(readTheme())
</script>

<template>
  <div class="app" id="app-shell" :class="{ 'sidebar-collapsed': sidebarCollapsed }">
    <AppSidebar v-show="!sidebarCollapsed" @close="closeSidebar" @navigate="compactViewport.matches && closeSidebar()" />
    <button v-if="!sidebarCollapsed" class="sidebar-backdrop" tabindex="-1"
      :aria-label="board.state.lang === 'zh' ? '收起菜单' : 'Close navigation'"
      @click="closeSidebar" />
    <main class="workspace">
      <header class="topbar">
        <div class="breadcrumb">
          <button ref="sidebarToggle" id="sidebar-toggle" type="button" class="quiet-button sidebar-toggle"
            aria-controls="app-shell-aside" :aria-expanded="!sidebarCollapsed"
            :aria-label="board.state.lang === 'zh' ? (sidebarCollapsed ? '展开菜单' : '收起菜单') : (sidebarCollapsed ? 'Expand navigation' : 'Collapse navigation')"
            :title="board.state.lang === 'zh' ? (sidebarCollapsed ? '展开菜单' : '收起菜单') : (sidebarCollapsed ? 'Expand navigation' : 'Collapse navigation')"
            @click="toggleSidebar"><UiIcon name="menu" :size="18" /></button>
          <span class="dim">{{ board.t('nav.workbench') }}</span>
          <span class="slash">/</span>
          <strong v-if="scopeLabel" id="crumb-scope">{{ scopeLabel }}</strong>
          <span v-if="scopeLabel" class="slash">/</span>
          <h1 id="page-h1" class="page-title">{{ board.t(def?.headingKey || 'heading.board') }}</h1>
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
        <AppChrome />
        <BoardView v-if="board.state.route.view === 'board'" />
        <FactoryView v-else-if="board.state.route.view === 'factory'" />
        <TopoView v-else-if="board.state.route.view === 'topo'" />
        <!-- problems 走 Vue 问题审查视图（读 snapshot.problems）；VnextHost 里
             旧的 renderProblems 映射保留给旧挂载层与既有单测，不再由本壳渲染。 -->
        <ProblemsView v-else-if="board.state.route.view === 'problems'" />
        <AnalyticsView v-else-if="board.state.route.view === 'analytics'" />
        <VnextHost v-else :view-id="board.state.route.view" />
      </div>
    </main>
    <TaskDrawer />
    <NodeEditorModal />
    <ProjectEditorModal />
  </div>
</template>
