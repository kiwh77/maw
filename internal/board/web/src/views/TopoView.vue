<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import { disposeTopology, renderTopology, type TopologyHandle } from '../topology'

const board = useBoard()
const root = ref<HTMLElement | null>(null)
let handle: TopologyHandle | null = null

function paint() {
  if (!root.value) return
  disposeTopology(root.value)
  handle = null
  if (!board.state.route.project) {
    root.value.innerHTML =
      board.state.lang === 'zh'
        ? '<p class="empty">依赖拓扑需要先选择工程</p>'
        : '<p class="empty">Select a project to view topology</p>'
    return
  }
  const tasks = (board.state.snapshot?.tasks || []) as never[]
  try {
    handle = renderTopology({
      container: root.value,
      tasks,
      lang: board.state.lang,
    } as never)
  } catch {
    root.value.innerHTML =
      board.state.lang === 'zh'
        ? '<p class="empty">拓扑画布加载失败</p>'
        : '<p class="empty">Topology failed to load</p>'
  }
}

onMounted(paint)
watch(() => [board.state.snapshot, board.state.route.project], paint, { deep: true })
onBeforeUnmount(() => {
  if (root.value) disposeTopology(root.value)
  handle = null
})
</script>

<template>
  <div ref="root" class="topo" id="topology" aria-label="Task dependency topology" />
</template>
