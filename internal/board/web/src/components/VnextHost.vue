<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import { findDetail, needsProject, NO_PROJECT_STATE, stateForKind } from '../lib/sources'
import { renderNodeList, renderNodeDetail } from '../../../assets/views/nodes/nodes-view.mjs'
import { renderTaskList } from '../../../assets/views/tasks/tasks-view.mjs'
import { renderRunList, renderRunDetail } from '../../../assets/views/runs/runs-view.mjs'
import { renderDeliveryList, renderDeliveryDetail } from '../../../assets/views/deliveries/deliveries-view.mjs'
import { renderTimeline } from '../../../assets/views/timeline/timeline-view.mjs'
import {
  createDateContext,
  shiftWindow,
  zoomWindow,
} from '../../../assets/views/timeline/timeline-view.mjs'
import { renderActivity } from '../../../assets/views/activity/activity-view.mjs'
import { renderSessions } from '../../../assets/views/sessions/sessions-view.mjs'
import { renderAnalytics } from '../../../assets/views/analytics/analytics-view.mjs'
import { renderProjectList } from '../../../assets/views/projects/projects-view.mjs'
import { renderSettings, normalizePolicy } from '../../../assets/views/settings/settings-view.mjs'

const props = defineProps<{ viewId: string }>()
const board = useBoard()
const root = ref<HTMLElement | null>(null)

/**
 * 时间轴窗口状态的宿主层。
 *
 * 视图本身是纯渲染：工具栏按钮只标 `data-action`，真正换窗口要有人持有状态并
 * 重新取数。以前没人接这段，四个按钮点了什么也不会发生。
 */
const timelineCtx = ref<ReturnType<typeof createDateContext> | null>(null)
const localStates = ref<Record<string, unknown>>({})

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

/**
 * 策略状态归一化（只在渲染前做一次）。
 * 缺失字段保持 null：视图据此写「—」，不替服务端宣布默认值。
 */
function normalizeSettingsState(viewState: unknown) {
  const state = viewState as { status?: string; policy?: Record<string, unknown> } | null | undefined
  if (!state || state.status !== 'ready' || !state.policy) return viewState
  return { ...state, policy: normalizePolicy(state.policy) }
}

const listHtml = computed(() => {
  const kind = props.viewId
  const render = LIST[kind]
  if (!render) return ''
  let viewState = localStates.value[kind] ?? board.state.viewStates[kind]
  if (needsProject(kind) && !board.state.route.project) viewState = NO_PROJECT_STATE
  // 策略页的策略字段是 /api/runtime 的原始形状（managed_execution_limit、retention.days）；
  // 归一化只做一次：概念表与四个输入框读同一份值，回填的才是服务端当前值。
  if (kind === 'settings') viewState = normalizeSettingsState(viewState)
  const ctx = {
    lang: board.state.lang,
    t: board.t,
    route: board.state.route,
    navigate: board.navigate.bind(board),
    openDetail: board.openDetail.bind(board),
    closeDetail: board.closeDetail.bind(board),
    readOnly: board.state.readOnly,
    // 时间轴换窗口后由宿主给出显式日期上下文；其它视图忽略该字段。
    dateContext: kind === 'timeline' ? timelineCtx.value ?? undefined : undefined,
    // 视图不猜自己有没有写通道：由宿主显式声明，静态导出页自然拿不到按钮。
    canRemove: !board.state.readOnly,
    pendingRemove: board.state.removePending,
    removeNotice: board.state.removeNotice,
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
  localStates.value = {}
  timelineCtx.value = null
  await board.loadView(props.viewId)
}

/**
 * 换时间轴窗口：先立刻按新窗口重画（区间按新窗口裁），再按新窗口取数。
 * 取数走 /api/v1 的 start/end，视图渲染仍只认 payload 与 dateContext。
 */
async function applyTimelineCtx(next: ReturnType<typeof createDateContext>) {
  timelineCtx.value = next
  const pid = String(board.state.route.project || '').trim()
  if (!pid) return
  const url = `/api/v1/projects/${encodeURIComponent(pid)}/timeline` +
    `?start=${encodeURIComponent(next.start)}&end=${encodeURIComponent(next.end)}`
  const call = () => fetch(url, { headers: { Accept: 'application/json' }, credentials: 'same-origin' })
  let res = await call()
  if (res.status === 401) {
    await fetch('/api/session', { method: 'POST', credentials: 'same-origin' })
    res = await call()
  }
  if (!res.ok) return
  const payload = await res.json()
  localStates.value = { ...localStates.value, timeline: stateForKind('timeline', payload) }
}

function handleTimelineAction(action: string) {
  const known = (board.state.payloads.timeline as { window?: unknown } | undefined)?.window
  const base = timelineCtx.value ?? createDateContext((known as Record<string, unknown>) || {})
  if (action === 'prev') void applyTimelineCtx(shiftWindow(base, -1))
  else if (action === 'next') void applyTimelineCtx(shiftWindow(base, 1))
  else if (action === 'zoom-in') void applyTimelineCtx(zoomWindow(base, -1))
  else if (action === 'zoom-out') void applyTimelineCtx(zoomWindow(base, 1))
}

onMounted(reload)
watch(() => [props.viewId, board.state.route.project, board.state.route.agent, board.state.route.status, board.state.route.search], reload)

function onListClick(event: MouseEvent) {
  const target = event.target as HTMLElement | null

  // 时间轴工具栏：四个按钮以前没有接线，点了没有任何反应。
  const actionEl = target?.closest?.('[data-action]') as HTMLElement | null
  const action = actionEl?.dataset.action
  if (props.viewId === 'timeline' && action &&
      ['prev', 'next', 'zoom-in', 'zoom-out'].includes(action)) {
    handleTimelineAction(action)
    return
  }

  // 工程移除走两段式：第一次只切到「确认移除？」，第二次才发请求。
  const ask = target?.closest?.('[data-project-remove]') as HTMLElement | null
  if (ask?.dataset.projectRemove) {
    board.requestRemoveProject(ask.dataset.projectRemove)
    return
  }
  const confirm = target?.closest?.('[data-project-remove-confirm]') as HTMLElement | null
  if (confirm?.dataset.projectRemoveConfirm) {
    void board.removeProject(confirm.dataset.projectRemoveConfirm)
    return
  }
  const cancel = target?.closest?.('[data-project-remove-cancel]') as HTMLElement | null
  if (cancel) {
    board.cancelRemoveProject()
    return
  }

  const row = target?.closest?.('tr[data-detail-id]') as HTMLElement | null
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
    @submit.prevent
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
