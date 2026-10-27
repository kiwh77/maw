<script setup lang="ts">
import { computed } from 'vue'
import { useBoard } from '../stores/board'

const board = useBoard()
const view = computed(() => board.state.route.view)
const hasProject = computed(() => Boolean(board.state.route.project))

const items = [
  { id: 'board', labelKey: 'view.board' },
  { id: 'factory', labelKey: 'view.factory' },
  { id: 'topo', labelKey: 'view.topo' },
]

function go(id: string) {
  if (id === 'topo' && !hasProject.value) return
  board.navigate(id)
}
</script>

<template>
  <div class="view-toggle" id="view-toggle" role="group" :aria-label="board.t('view.board')">
    <button
      v-for="item in items"
      :key="item.id"
      type="button"
      :id="'view-' + item.id"
      :data-view="item.id"
      :class="{ active: view === item.id }"
      :aria-pressed="view === item.id ? 'true' : 'false'"
      :disabled="item.id === 'topo' && !hasProject"
      @click="go(item.id)"
    >
      {{ board.t(item.labelKey) }}
    </button>
  </div>
</template>
