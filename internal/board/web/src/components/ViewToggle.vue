<script setup lang="ts">
import { computed } from 'vue'
import { useBoard } from '../stores/board'

const board = useBoard()
const view = computed(() => board.state.route.view)

// 流程视图的四个入口。执行视图（EV-001）与看板并列：一个看流水，一个按迭代聚合，
// 两者读同一份快照与同一套筛选，切页不丢现场。
// 拓扑不再要求先选工程（BUX-FE-001）：未选工程时按多工程 band 出全局拓扑。
const items = [
  { id: 'board', labelKey: 'view.board' },
  { id: 'factory', labelKey: 'view.factory' },
  { id: 'topo', labelKey: 'view.topo' },
  { id: 'execution', labelKey: 'view.execution' },
]

function go(id: string) {
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
      @click="go(item.id)"
    >
      {{ board.t(item.labelKey) }}
    </button>
  </div>
</template>
