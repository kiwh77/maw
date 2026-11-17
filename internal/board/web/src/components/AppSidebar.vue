<script setup lang="ts">
import { computed } from 'vue'
import UiIcon from './UiIcon.vue'
import { useBoard } from '../stores/board'

const board = useBoard()
const emit = defineEmits<{ close: []; navigate: [] }>()
/** 置顶入口：工作台＝无 view 参数的默认页（看板），切走后从这里一键回来。 */
const pinnedNav = computed(() => board.registry.sidebarPinned())
const navGroups = computed(() => board.registry.sidebarNavModel())
const label = (item: Record<string, unknown>) =>
  board.t(String(item.navKey || item.titleKey || ''))
const agents = computed(() => {
  const list = board.state.snapshot?.agents || []
  return list.filter((a) => {
    const status = String(a.status || a.state || '')
    return status === 'working' || status === 'busy' || a.working === true
  })
})
function agentName(a: Record<string, unknown>) {
  return typeof a.name === 'string' && a.name.trim() ? a.name : String(a.id || 'Agent')
}
const viewId = computed(() => board.state.route.view)

function selectView(id: string) {
  board.navigate(id)
  emit('navigate')
}
</script>

<template>
  <aside class="sidebar" id="app-shell-aside" aria-label="Projects & Agents">
    <div class="brand">
      <button type="button" class="quiet-button sidebar-close"
        :aria-label="board.state.lang === 'zh' ? '收起菜单' : 'Close navigation'"
        @click="emit('close')"><UiIcon name="close" /></button>
      <span class="brand-mark" aria-hidden="true">›_</span>
      <div>
        <div class="brand-name">maw<span style="color:var(--accent)">.</span></div>
        <div class="brand-sub">AGENT WORKBENCH</div>
      </div>
    </div>
    <nav class="project-nav vnext-nav" id="vnext-nav" aria-label="工作台视图">
      <button
        v-for="item in pinnedNav"
        :key="item.id"
        type="button"
        class="nav-item nav-item--pinned"
        :class="{ active: viewId === item.id }"
        :data-view="item.id"
        :aria-current="viewId === item.id ? 'page' : undefined"
        @click="selectView(item.id)"
      >
        <UiIcon :name="String(item.id)" />
        <span>{{ label(item) }}</span>
      </button>
      <template v-for="group in navGroups" :key="group.id">
        <div class="section-label">{{ board.t(group.labelKey) }}</div>
        <button
          v-for="item in group.items"
          :key="item.id"
          type="button"
          class="nav-item"
          :class="{ active: viewId === item.id }"
          :data-view="item.id"
          :aria-current="viewId === item.id ? 'page' : undefined"
          @click="selectView(item.id)"
        >
          <UiIcon :name="String(item.id)" />
          <span>{{ label(item) }}</span>
        </button>
      </template>
    </nav>
    <section class="agents-rail" id="agents-rail" aria-label="Working Agents">
      <div class="section-label">
        AGENTS <span>{{ agents.length }}</span>
      </div>
      <div class="agents-scroll" id="agents">
        <p v-if="!agents.length" class="empty">
          {{ board.state.lang === 'zh' ? '暂无工作中 Agent' : 'No working agents' }}
        </p>
        <div v-for="(a, i) in agents" :key="String(a.id || i)" class="agent-row">
          <span class="agent-dot st-working" aria-hidden="true" />
          <span class="agent-row__name" :title="agentName(a)">{{ agentName(a) }}</span>
          <span v-if="a.task_id" class="agent-row__task">{{ a.task_id }}</span>
        </div>
      </div>
    </section>
    <div class="sidebar-foot">maw · Agent Workbench</div>
  </aside>
</template>
