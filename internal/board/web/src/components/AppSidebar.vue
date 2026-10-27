<script setup lang="ts">
import { computed } from 'vue'
import { useBoard } from '../stores/board'

const board = useBoard()
const navGroups = computed(() => board.registry.sidebarNavModel())
const agents = computed(() => {
  const list = board.state.snapshot?.agents || []
  return list.filter((a) => {
    const status = String(a.status || a.state || '')
    return status === 'working' || status === 'busy' || a.working === true
  })
})
const viewId = computed(() => board.state.route.view)

function selectView(id: string) {
  board.navigate(id)
}
</script>

<template>
  <aside class="sidebar" id="app-shell-aside" aria-label="Projects & Agents">
    <div class="brand">
      <span class="brand-mark" aria-hidden="true">›_</span>
      <div>
        <div class="brand-name">maw<span style="color:var(--accent)">.</span></div>
        <div class="brand-sub">AGENT WORKBENCH</div>
      </div>
    </div>
    <nav class="project-nav vnext-nav" id="vnext-nav" aria-label="工作台视图">
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
          <span class="symbol" aria-hidden="true">{{ item.symbol }}</span>
          <span>{{ board.t(item.titleKey) }}</span>
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
          {{ a.name || a.id || 'agent' }}
        </div>
      </div>
    </section>
    <div class="sidebar-foot"><span>⌘</span> multi-agent / worktree</div>
  </aside>
</template>
