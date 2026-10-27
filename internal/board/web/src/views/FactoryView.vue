<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import { renderFactory, setFactoryVisible } from '../factory/factory'

const board = useBoard()
const root = ref<HTMLElement | null>(null)

function paint() {
  if (!root.value) return
  const tasks = board.state.snapshot?.tasks || []
  const agents = board.state.snapshot?.agents || []
  renderFactory(root.value, {
    tasks,
    agents,
    lang: board.state.lang,
    onTask: () => {},
  })
  setFactoryVisible(root.value, true)
}

onMounted(paint)
watch(() => board.state.snapshot, paint, { deep: true })
onBeforeUnmount(() => {
  if (root.value) setFactoryVisible(root.value, false)
})
</script>

<template>
  <section ref="root" class="factory" id="factory" aria-label="Workshop" />
</template>
