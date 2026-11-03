<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import { layoutTopoBands } from '../layout'
import { disposeTopology, renderTopology, type TopologyHandle, type TopoTask } from '../topology'

const board = useBoard()
const root = ref<HTMLElement | null>(null)
let handle: TopologyHandle | null = null
/** 换工程／语言时才自动适应画布；同工程 snapshot 刷新不得冲掉用户缩放。 */
let fitKey = ''

const STATUS_LABELS: Record<string, Record<string, string>> = {
  zh: {
    backlog: '待排期',
    ready: '可领取',
    claimed: '已领取',
    in_progress: '进行中',
    self_check: '自检中',
    in_review: '评审中',
    merging: '合并中',
    conflict: '冲突',
    blocked: '阻塞',
    done: '已完成',
    cancelled: '已取消',
  },
  en: {
    backlog: 'Backlog',
    ready: 'Ready',
    claimed: 'Claimed',
    in_progress: 'In progress',
    self_check: 'Self-check',
    in_review: 'In review',
    merging: 'Merging',
    conflict: 'Conflict',
    blocked: 'Blocked',
    done: 'Done',
    cancelled: 'Cancelled',
  },
}

function asTasks(raw: Array<Record<string, unknown>>): TopoTask[] {
  return raw.map((t) => ({
    id: String(t.id || ''),
    project_id: t.project_id != null ? String(t.project_id) : undefined,
    project_name: t.project_name != null ? String(t.project_name) : undefined,
    title: t.title != null ? String(t.title) : undefined,
    status: t.status != null ? String(t.status) : undefined,
    owner: t.owner != null ? String(t.owner) : undefined,
    priority: typeof t.priority === 'number' ? t.priority : undefined,
    deps: Array.isArray(t.deps) ? t.deps.map((d) => String(d)) : [],
  })).filter((t) => t.id)
}

function paint() {
  if (!root.value) return
  const zh = board.state.lang === 'zh'
  if (!board.state.route.project) {
    disposeTopology(root.value)
    handle = null
    fitKey = ''
    root.value.innerHTML = zh
      ? '<p class="empty">依赖拓扑需要先选择工程</p>'
      : '<p class="empty">Select a project to view topology</p>'
    return
  }
  const tasks = asTasks((board.state.snapshot?.tasks || []) as Array<Record<string, unknown>>)
  if (!tasks.length) {
    disposeTopology(root.value)
    handle = null
    fitKey = ''
    root.value.innerHTML = zh
      ? '<p class="empty">暂无任务，应用蓝图后可查看依赖拓扑</p>'
      : '<p class="empty">No tasks yet; apply a blueprint to see topology</p>'
    return
  }
  try {
    const bands = layoutTopoBands(tasks, zh ? '未知工程' : 'Unknown project')
    const edgeWord = zh ? ' 边' : ' edges'
    const taskWord = zh ? ' 任务 · ' : ' tasks · '
    for (const band of bands) {
      band.countText = band.tasks.length + taskWord + band.edgeCount + edgeWord
    }
    // 勿先 dispose：renderTopology 按 signature 复用实例，避免 SSE 刷新冲掉缩放。
    handle = renderTopology({
      container: root.value,
      bands,
      statusLabels: STATUS_LABELS[zh ? 'zh' : 'en'],
      strings: zh
        ? {
            fit: '适应画布',
            relayout: '重布局',
            legend: '依赖方向：前置 → 后续',
            legendCross: '虚线：当前视图外的依赖',
            stubTitle: '筛选外 / 历史依赖',
            stubHint: '不在当前视图',
          }
        : {
            fit: 'Fit to view',
            relayout: 'Relayout',
            legend: 'Dependency direction: prerequisite → dependent',
            legendCross: 'Dashed: dependency outside current view',
            stubTitle: 'Outside filter / historical dep',
            stubHint: 'Not in current view',
          },
      onOpen: (tid, pid) => board.openTaskDrawer(String(tid || ''), String(pid || '')),
    })
    const nextFitKey = String(board.state.route.project || '') + '|' + board.state.lang
    // 首屏交给 renderTopology 内部 fit；仅换工程／语言时再适应，避免二次 fit 冲掉缩放。
    if (fitKey && nextFitKey !== fitKey) handle.fitView()
    fitKey = nextFitKey
  } catch (err) {
    console.error('topology render failed', err)
    disposeTopology(root.value)
    handle = null
    fitKey = ''
    root.value.innerHTML = zh
      ? '<p class="empty">拓扑画布加载失败</p>'
      : '<p class="empty">Topology failed to load</p>'
  }
}

onMounted(paint)
watch(() => [board.state.snapshot, board.state.route.project, board.state.lang], paint, { deep: true })
onBeforeUnmount(() => {
  if (root.value) disposeTopology(root.value)
  handle = null
  fitKey = ''
})
</script>

<template>
  <div ref="root" class="topo" id="topology" aria-label="Task dependency topology" />
</template>
