<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import { renderAnalytics } from '../../../assets/views/analytics/analytics-view.mjs'
// @ts-expect-error 纯 ESM 视图模块没有类型声明：由 Vite 打包，运行期导出见 attribution.mjs
import * as attributionModule from '../../../assets/views/analytics/attribution.mjs'

/**
 * 统计页宿主：只保留用量统计。
 * 自愈 / System Health 随调度层一起下线。
 */
type AttributionState = {
  status: string
  dimension?: string
  dimensions?: string[]
  reports?: Record<string, unknown>
  errors?: Record<string, string>
}

const { fetchAttribution } = attributionModule as unknown as {
  fetchAttribution: (projectId: string, options?: Record<string, unknown>) => Promise<AttributionState>
}

const board = useBoard()
const dimension = ref('source')
const attribution = ref<AttributionState>({ status: 'loading' })

/** 主报表仍由中枢统一取数（/analytics 无参数）；归属是它的第二块数据。 */
const usageState = computed(() => board.state.viewStates.analytics ?? { status: 'loading' })
const usageHtml = computed(() => renderAnalytics(
  {
    ...(usageState.value as Record<string, unknown>),
    attribution: { ...attribution.value, dimension: dimension.value },
  },
  { lang: board.state.lang, project: board.state.route.project },
))

async function loadUsage() {
  const project = String(board.state.route.project || '').trim()
  attribution.value = { ...attribution.value, status: 'loading' }
  void board.loadView('analytics')
  const next = await fetchAttribution(project)
  attribution.value = next
  if (!next.reports?.[dimension.value]) {
    const first = (next.dimensions || []).find(dim => next.reports?.[dim])
    if (first) dimension.value = first
  }
}

function onUsageClick(event: MouseEvent) {
  const target = event.target as HTMLElement | null
  const dim = target?.closest?.('[data-attr-dimension]') as HTMLElement | null
  if (dim?.dataset.attrDimension) {
    dimension.value = dim.dataset.attrDimension
    return
  }
  if (target?.closest?.('[data-action="retry-attribution"]')) {
    void loadUsage()
    return
  }
  const drill = target?.closest?.('[data-drill="runs"]')
  if (drill) {
    event.preventDefault()
    board.navigate('runs')
  }
}

onMounted(loadUsage)
watch(() => board.state.route.project, loadUsage)
</script>

<template>
  <div class="analytics-host" id="analytics-host">
    <div
      id="analytics-usage"
      class="analytics-usage"
      data-analytics-panel="usage"
      @click="onUsageClick"
      v-html="usageHtml"
    />
  </div>
</template>
