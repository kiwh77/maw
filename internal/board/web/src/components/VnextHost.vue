<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import { findDetail, needsProject, NO_PROJECT_STATE } from '../lib/sources'
import { renderNodeList, renderNodeDetail } from '../../../assets/views/nodes/nodes-view.mjs'
import { renderTaskList } from '../../../assets/views/tasks/tasks-view.mjs'
import { renderRunList, renderRunDetail } from '../../../assets/views/runs/runs-view.mjs'
import { renderDeliveryList, renderDeliveryDetail } from '../../../assets/views/deliveries/deliveries-view.mjs'
import { renderTimeline } from '../../../assets/views/timeline/timeline-view.mjs'
import { renderActivity } from '../../../assets/views/activity/activity-view.mjs'
import { renderSessions } from '../../../assets/views/sessions/sessions-view.mjs'
import { renderAnalytics } from '../../../assets/views/analytics/analytics-view.mjs'
import { renderProjectList } from '../../../assets/views/projects/projects-view.mjs'
import { renderSettings } from '../../../assets/views/settings/settings-view.mjs'

const props = defineProps<{ viewId: string }>()
const board = useBoard()
const root = ref<HTMLElement | null>(null)

const LIST: Record<string, (s: unknown, ctx: unknown) => string> = {
  nodes: renderNodeList as never,
  tasks: renderTaskList as never,
  runs: renderRunList as never,
  deliveries: renderDeliveryList as never,
  timeline: renderTimeline as never,
  activity: renderActivity as never,
  sessions: renderSessions as never,
  analytics: renderAnalytics as never,
  projects: renderProjectList as never,
  settings: renderSettings as never,
}

const DETAIL: Record<string, (item: unknown, ctx: unknown) => string> = {
  nodes: renderNodeDetail as never,
  runs: renderRunDetail as never,
  deliveries: renderDeliveryDetail as never,
}

const listHtml = computed(() => {
  const kind = props.viewId
  const render = LIST[kind]
  if (!render) return ''
  let viewState = board.state.viewStates[kind]
  if (needsProject(kind) && !board.state.route.project) viewState = NO_PROJECT_STATE
  const ctx = {
    lang: board.state.lang,
    t: board.t,
    route: board.state.route,
    navigate: board.navigate.bind(board),
    openDetail: board.openDetail.bind(board),
    closeDetail: board.closeDetail.bind(board),
    readOnly: board.state.readOnly,
    filters: {
      project: board.state.route.project,
      agent: board.state.route.agent,
      status: board.state.route.status,
      search: board.state.route.search,
    },
  }
  return render(viewState || { status: 'loading' }, ctx)
})

const detailHtml = computed(() => {
  const kind = props.viewId
  const id = board.state.route.detail
  if (!id || !DETAIL[kind]) return ''
  const item = findDetail(kind, board.state.payloads[kind], id)
  const ctx = {
    lang: board.state.lang,
    t: board.t,
    route: board.state.route,
    closeDetail: board.closeDetail.bind(board),
    readOnly: board.state.readOnly,
  }
  if (!item) return `<p class="vnext-status">${board.t('detail.missing')}</p>`
  return DETAIL[kind](item, ctx)
})

const showDetail = computed(() => Boolean(board.state.route.detail && DETAIL[props.viewId]))

async function reload() {
  await board.loadView(props.viewId)
}

onMounted(reload)
watch(() => [props.viewId, board.state.route.project, board.state.route.agent, board.state.route.status, board.state.route.search], reload)

function onListClick(event: MouseEvent) {
  const row = (event.target as HTMLElement)?.closest?.('tr[data-detail-id]') as HTMLElement | null
  if (row?.dataset.detailId) board.openDetail(row.dataset.detailId)
}
</script>

<template>
  <section
    v-show="!showDetail"
    class="vnext"
    id="vnext-view"
    aria-label="Workbench view"
    ref="root"
    @click="onListClick"
    v-html="listHtml"
  />
  <section
    v-if="showDetail"
    class="vnext-detail"
    id="vnext-detail"
    role="region"
    aria-label="Detail"
  >
    <div class="vnext-detail__head">
      <button type="button" class="vnext-detail__back" @click="board.closeDetail()">
        {{ board.t('detail.back') }}
      </button>
      <h2 class="vnext-detail__title">{{ board.state.route.detail }}</h2>
      <button type="button" class="vnext-detail__close" @click="board.closeDetail()">×</button>
    </div>
    <div class="vnext-detail__body" v-html="detailHtml" />
  </section>
</template>
