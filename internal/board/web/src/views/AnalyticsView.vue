<script setup lang="ts">
import { ref } from 'vue'
import { useBoard } from '../stores/board'
import VnextHost from '../components/VnextHost.vue'
import SystemHealthView from './SystemHealthView.vue'

/**
 * 统计页宿主：两个页签。
 *
 * 「用量统计」保持 VnextHost 的原有渲染；「自愈统计」是 SH-503 新增的只读视图。
 * 路由 id 继续沿用已注册的 `analytics`，因为注册表不在本卡写域内——这样侧栏、
 * 深链、工程筛选和语言切换都不需要改注册表，行为与 problems 视图同构。
 */
const board = useBoard()
const tab = ref<'usage' | 'health'>('usage')

const zh = () => board.state.lang === 'zh'
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

    <VnextHost v-if="tab === 'usage'" view-id="analytics" />
    <SystemHealthView v-else />
  </div>
</template>
