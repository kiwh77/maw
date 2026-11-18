<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import SystemHealthView from './SystemHealthView.vue'
import { renderAnalytics } from '../../../assets/views/analytics/analytics-view.mjs'
// @ts-expect-error 纯 ESM 视图模块没有类型声明：由 Vite 打包，运行期导出见 attribution.mjs
import * as attributionModule from '../../../assets/views/analytics/attribution.mjs'

/**
 * 统计页宿主：两个页签。
 *
 * 「用量统计」保持 VnextHost 的原有渲染，但在宿主持有归属状态（PI-203）：
 * 四个维度（来源／节点／模型／用途）各自取数，切维度只换归集键，不重取主报表。
 * 为什么不在 VnextHost 里做：维度切换需要宿主持有选择；视图本身仍是纯渲染，
 * 新增内容全部落在 assets/views/analytics/** 与 analytics.css。
 *
 * 「自愈统计」是 SH-503 的只读视图，行为不变。路由 id 继续沿用已注册的
 * `analytics`（注册表不在本卡写域内），侧栏、深链与语言切换都不受影响。
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
const tab = ref<'usage' | 'health'>('usage')
const dimension = ref('source')
const attribution = ref<AttributionState>({ status: 'loading' })

const zh = () => board.state.lang === 'zh'

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
  // 当前维度取不到、别的维度有：落到第一个有数据的维度，避免停在空页。
  if (!next.reports?.[dimension.value]) {
    const first = (next.dimensions || []).find(dim => next.reports?.[dim])
    if (first) dimension.value = first
  }
}

/** 维度切换与重试都走事件委托：视图输出的是字符串，宿主负责状态。 */
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
  // 汇总下钻：SPA 内走 navigate，旧壳/静态页里保留 <a> 的默认跳转。
  const drill = target?.closest?.('[data-drill="runs"]')
  if (drill) {
    event.preventDefault()
    board.navigate('runs')
  }
}

onMounted(loadUsage)
// 工程切换：主报表与归属一起按新作用域重取，不复用上一工程的数字。
watch(() => board.state.route.project, loadUsage)
</script>

<template>
  <div class="analytics-host" id="analytics-host">
    <div class="analytics-tabs" id="analytics-tabs" role="tablist">
      <button
        type="button"
        id="analytics-tab-usage"
        class="analytics-tab"
        :class="{ active: tab === 'usage' }"
        role="tab"
        :aria-selected="tab === 'usage' ? 'true' : 'false'"
        data-analytics-tab="usage"
        @click="tab = 'usage'"
      >
        {{ zh() ? '用量统计' : 'Usage' }}
      </button>
      <button
        type="button"
        id="analytics-tab-health"
        class="analytics-tab"
        :class="{ active: tab === 'health' }"
        role="tab"
        :aria-selected="tab === 'health' ? 'true' : 'false'"
        data-analytics-tab="health"
        @click="tab = 'health'"
      >
        {{ zh() ? '自愈统计' : 'Self-heal' }}
      </button>
    </div>

    <div
      v-if="tab === 'usage'"
      id="analytics-usage"
      class="analytics-usage"
      data-analytics-panel="usage"
      @click="onUsageClick"
      v-html="usageHtml"
    />
    <SystemHealthView v-else />
  </div>
</template>
