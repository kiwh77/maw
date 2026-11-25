<script setup lang="ts">
/**
 * 工具栏「自动执行」开关。
 *
 * 产品语义只有一句：打开它就是「让中枢用这个执行节点自动领题并执行」。
 * 所以在界面上它必须是**一个**开关 + 一个节点下拉，而不是让用户自己去理解
 * auto_enabled / auto_select / allowed_nodes 这几个字段的组合。
 *
 * 三条硬规则：
 *  1. 未就绪时不允许打开（置灰 + 说明原因），杜绝「打开了却不动」的黑箱；
 *  2. 打开前二次确认——这会真的起本机 CLI 进程、写文件、烧 token；
 *  3. 服务端说 applied=false（中枢没在跑）时如实显示「已保存，中枢启动后生效」。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import { createDefaultSources } from '../lib/sources'
import type { AutoExecutionState } from '../lib/sources'

const board = useBoard()
const sources = createDefaultSources(typeof window !== 'undefined' ? window : null)

const state = ref<AutoExecutionState | null>(null)
const busy = ref(false)
const confirmOpen = ref(false)
const chosenNode = ref('')
const toastMessage = ref('')
const toastVisible = ref(false)
let toastTimer: ReturnType<typeof setTimeout> | null = null
let pollTimer: ReturnType<typeof setInterval> | null = null

const projectId = computed(() => String(board.state.route.project || '').trim())
const zh = computed(() => board.state.lang !== 'en')

/** 归一化后的三态：开着 / 关着可开 / 关着但开不起来。 */
const mode = computed<'on' | 'off' | 'blocked' | 'unknown'>(() => {
  const s = state.value
  if (!s) return 'unknown'
  if (s.enabled) return 'on'
  return s.ready ? 'off' : 'blocked'
})

const nodes = computed(() => state.value?.nodes || [])

/** 节点下拉的当前值：服务端给的就绪节点，或用户挑的。 */
const nodeValue = computed({
  get: () => chosenNode.value || state.value?.node_id || '',
  set: (v: string) => { chosenNode.value = v },
})

const reasonText = computed(() => {
  const s = state.value
  if (!s) return ''
  const reasons = s.reasons || []
  if (!reasons.length) return ''
  return reasons.map((r) => reasonLabel(r)).join('；')
})

function reasonLabel(code: string): string {
  const table: Record<string, [string, string]> = {
    serve_not_running: ['中枢未在运行：启动 maw serve 后会立即生效', 'Hub not running: starts once maw serve is up'],
    no_candidate_node: ['本工程还没有登记可用的执行节点', 'No execution node registered for this project'],
    node_stale: ['节点观测已过期：先点「测试环境」刷新证据', 'Node evidence is stale: run the node check first'],
    node_not_ready: ['节点未就绪：入口／认证／连接有一项不通过', 'Node not ready: entry/auth/connection failed'],
    node_disabled: ['节点已停用接收新任务', 'Node stopped accepting tasks'],
    node_draining: ['节点正在排空：先取消排空再接单', 'Node is draining: cancel draining first'],
    node_occupied: ['节点槽位已占满，等空槽后会自动接单', 'Node slots are full: it will pick up work when free'],
    platform_unsupported: ['当前平台不支持受管执行', 'Managed execution is unsupported on this platform'],
    managed_execution_limit: ['受管上限必须至少为 1', 'Managed limit must be at least 1'],
    capability_unknown: ['缺少已验证能力证据', 'Missing verified capability evidence'],
  }
  const hit = table[code]
  if (!hit) return code
  return zh.value ? hit[0] : hit[1]
}

const title = computed(() => {
  const s = state.value
  if (!projectId.value) return zh.value ? '先选择工程' : 'Pick a project first'
  if (!s) return zh.value ? '自动执行：读取中…' : 'Auto run: loading…'
  if (s.enabled) {
    return zh.value
      ? `自动执行已开启（节点 ${s.node_id || '自动'}）`
      : `Auto run on (node ${s.node_id || 'auto'})`
  }
  if (!s.ready) return zh.value ? `暂不能开启：${reasonText.value}` : `Cannot enable: ${reasonText.value}`
  return zh.value ? '打开后由执行节点自动领题并执行' : 'Enable unattended auto-claim and run'
})

const label = computed(() => {
  if (mode.value === 'on') return zh.value ? '自动执行中' : 'Auto running'
  if (mode.value === 'blocked') return zh.value ? '自动执行（未就绪）' : 'Auto run (not ready)'
  return zh.value ? '自动执行' : 'Auto run'
})

const canToggle = computed(() => Boolean(projectId.value) && !busy.value && statesReady())
function statesReady() {
  const s = state.value
  if (!s) return false
  // 开着随时能关；关着只有在就绪时才能开。
  return s.enabled || s.ready
}

function showToast(message: string) {
  toastMessage.value = message
  toastVisible.value = true
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    toastVisible.value = false
    toastMessage.value = ''
    toastTimer = null
  }, 3600)
}

async function reload() {
  const pid = projectId.value
  if (!pid) {
    state.value = null
    return
  }
  const next = await sources.getAutoExecution(pid)
  if (next && next.project_id === pid) {
    state.value = next
    // 服务端没给节点时保留用户的选择，避免下拉框跳回空。
    if (!chosenNode.value && next.node_id) chosenNode.value = next.node_id
  }
}

async function apply(enabled: boolean) {
  const pid = projectId.value
  if (!pid) return
  busy.value = true
  try {
    const node = preferredNode()
    const out = await sources.setAutoExecution(pid, enabled, node)
    if (!out.ok) {
      showToast(
        out.unauthenticated
          ? (zh.value ? '需要先在本机中枢完成授权' : 'Authorize with the local hub first')
          : (zh.value ? `保存失败：${out.code || out.status}` : `Save failed: ${out.code || out.status}`),
      )
      return
    }
    if (out.state) state.value = out.state
    else await reload()
    const s = state.value
    if (enabled) {
      if (s && !s.ready && s.reasons?.length) {
        showToast(zh.value ? `未能开启：${reasonText.value}` : `Cannot enable: ${reasonText.value}`)
      } else if (s && !s.serve_running) {
        showToast(zh.value ? '已保存：中枢启动后开始自动领题' : 'Saved: starts when the hub is running')
      } else {
        showToast(zh.value ? '已开启：中枢会自动领题并执行' : 'Enabled: the hub will claim and run tasks')
      }
    } else {
      showToast(zh.value ? '已关闭自动执行' : 'Auto run disabled')
    }
  } finally {
    busy.value = false
    confirmOpen.value = false
  }
}

/** 优先用用户挑的节点；没挑就用服务端建议的；再没有就交给匹配层。 */
function preferredNode(): string {
  const want = nodeValue.value
  if (want) return want
  return ''
}

function onClick() {
  const s = state.value
  if (!s) return
  if (s.enabled) {
    void apply(false)
    return
  }
  if (!s.ready) {
    showToast(zh.value ? `暂不能开启：${reasonText.value}` : `Cannot enable: ${reasonText.value}`)
    return
  }
  confirmOpen.value = true
}

watch(projectId, () => {
  chosenNode.value = ''
  void reload()
})

onMounted(async () => {
  await reload()
  // 就绪结论会随节点探测／中枢启停变化：定期刷新，别让开关停在过期的绿状态上。
  pollTimer = setInterval(() => void reload(), 10000)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
  if (toastTimer) clearTimeout(toastTimer)
})
</script>

<template>
  <div class="auto-exec" :data-mode="mode">
    <select
      v-if="nodes.length > 1"
      class="auto-exec__node"
      :value="nodeValue"
      :aria-label="zh ? '执行节点' : 'Execution node'"
      :title="zh ? '自动执行使用的节点' : 'Node used for auto run'"
      :disabled="busy"
      @change="nodeValue = ($event.target as HTMLSelectElement).value"
    >
      <option value="">{{ zh ? '自动选节点' : 'Auto node' }}</option>
      <option v-for="n in nodes" :key="n.id" :value="n.id">
        {{ n.label || n.id }}{{ n.ready ? '' : ' ·' + reasonLabel(n.reason || '') }}
      </option>
    </select>
    <button
      type="button"
      id="f-auto-exec"
      class="auto-exec__toggle"
      :disabled="!canToggle"
      :title="title"
      :aria-label="title"
      :aria-pressed="mode === 'on'"
      @click="onClick"
    >
      <span class="auto-exec__dot" aria-hidden="true"></span>
      {{ label }}
    </button>
  </div>

  <Teleport to="body">
    <div v-if="confirmOpen" class="projects-modal-mask" @click="confirmOpen = false" />
    <div v-if="confirmOpen" class="projects-modal auto-exec-modal" role="dialog" aria-modal="true"
      :aria-label="zh ? '开启自动执行' : 'Enable auto run'">
      <div class="projects-modal__head">
        <h2>{{ zh ? '开启自动执行？' : 'Enable unattended auto run?' }}</h2>
      </div>
      <div class="projects-modal__body">
        <p class="field-hint">
          {{
            zh
              ? '开启后中枢会用所选执行节点自动领取可执行的任务并直接开跑：会启动本机 CLI 进程、写工作区文件、消耗 token。'
              : 'The hub will claim ready tasks and run them with the selected node: it spawns local CLI processes, writes workspace files and consumes tokens.'
          }}
        </p>
        <p v-if="nodeValue" class="field-hint">
          {{ zh ? '执行节点：' : 'Node: ' }}{{ nodeValue }}
        </p>
        <div class="auto-exec-modal__foot">
          <button type="button" class="quiet-button" @click="confirmOpen = false">
            {{ zh ? '取消' : 'Cancel' }}
          </button>
          <button type="button" class="quiet-button auto-exec-modal__ok" :disabled="busy" @click="apply(true)">
            {{ zh ? '确认开启' : 'Enable' }}
          </button>
        </div>
      </div>
    </div>
    <div v-if="toastVisible" class="ui-toast" role="status" aria-live="polite">{{ toastMessage }}</div>
  </Teleport>
</template>
