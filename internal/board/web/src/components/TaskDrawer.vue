<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import { buildTaskPrompt, copyText } from '../lib/prompts'

const board = useBoard()
const open = computed(() => Boolean(board.state.drawer))
const loading = ref(false)
const events = ref<Array<Record<string, unknown>>>([])
const copyFlash = ref('')
let closeTimer: ReturnType<typeof setTimeout> | null = null
let detailToken = 0

const task = computed(() => {
  const d = board.state.drawer
  if (!d) return null
  const tasks = board.state.snapshot?.tasks || []
  return (
    tasks.find(
      (t) => String(t.id) === d.tid && String(t.project_id || '') === String(d.pid || ''),
    ) ||
    tasks.find((t) => String(t.id) === d.tid) ||
    null
  )
})

const project = computed(() => {
  const pid = String(task.value?.project_id || board.state.drawer?.pid || '')
  if (!pid) return null
  return (board.state.snapshot?.projects || []).find((p) => String(p.id) === pid) || null
})

const prompt = computed(() => {
  if (!task.value) return ''
  return buildTaskPrompt(task.value, project.value, {
    agents: board.state.snapshot?.agents || [],
    tasks: board.state.snapshot?.tasks || [],
    lang: board.state.lang,
  })
})

const showCmd = computed(() => (task.value?.id ? `maw task show ${task.value.id}` : ''))
const cdWt = computed(() => {
  const wt = task.value?.worktree
  if (!wt) return ''
  const base = String(project.value?.path || '<工程路径>').replace(/\\/g, '/')
  return `cd ${base}/${wt}`
})

const agent = computed(() => {
  const t = task.value
  if (!t) return null
  return (
    (board.state.snapshot?.agents || []).find(
      (a) =>
        String(a.task_id || '') === String(t.id) &&
        (!t.project_id || String(a.project_id || '') === String(t.project_id)),
    ) || null
  )
})

const zh = computed(() => board.state.lang !== 'en')

function bullets(arr: unknown, empty: string) {
  const list = Array.isArray(arr) ? arr.map(String) : []
  if (!list.length) return `<p class="empty">${empty}</p>`
  return '<ul>' + list.map((x) => `<li>${esc(x)}</li>`).join('') + '</ul>'
}

function esc(v: unknown) {
  return String(v ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function fmtTime(v: unknown) {
  if (!v) return '—'
  const d = new Date(String(v))
  if (Number.isNaN(d.getTime())) return String(v)
  return d.toLocaleString()
}

async function flashCopy(key: string, text: string, ok: string) {
  try {
    await copyText(text)
    copyFlash.value = key + ':' + ok
    setTimeout(() => {
      if (copyFlash.value.startsWith(key + ':')) copyFlash.value = ''
    }, 2000)
  } catch {
    copyFlash.value = key + ':' + (zh.value ? '复制失败' : 'Copy failed')
    setTimeout(() => {
      if (copyFlash.value.startsWith(key + ':')) copyFlash.value = ''
    }, 1400)
  }
}

function label(key: string) {
  return copyFlash.value.startsWith(key + ':')
    ? copyFlash.value.slice(key.length + 1)
    : ''
}

async function refreshDetail() {
  const d = board.state.drawer
  if (!d) return
  const token = ++detailToken
  loading.value = true
  try {
    const q = new URLSearchParams({ id: d.tid, t: String(Date.now()) })
    if (d.pid) q.set('project', d.pid)
    const res = await fetch('/api/task?' + q, { cache: 'no-store' })
    if (!res.ok) throw new Error('unavailable')
    const body = await res.json()
    if (token !== detailToken) return
    events.value = Array.isArray(body.events) ? body.events : []
    if (body.task) board.patchSnapshotTask(d.tid, d.pid, body.task as Record<string, unknown>)
  } catch {
    if (token === detailToken) events.value = []
  } finally {
    if (token === detailToken) loading.value = false
  }
}

function close() {
  board.closeTaskDrawer()
}

/** 抽屉内可聚焦元素（跳过禁用与不可见）。 */
function focusables(): HTMLElement[] {
  const el = document.getElementById('drawer')
  if (!el) return []
  return Array.from(
    el.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((node) => node.offsetParent !== null || node === document.activeElement)
}

function onKey(e: KeyboardEvent) {
  if (!open.value) return
  if (e.key === 'Escape') {
    e.preventDefault()
    close()
    return
  }
  // 焦点陷阱：模态抽屉打开时 Tab 不应把焦点送回背后页面。
  if (e.key === 'Tab') {
    const list = focusables()
    if (!list.length) {
      e.preventDefault()
      return
    }
    const first = list[0]
    const last = list[list.length - 1]
    const active = document.activeElement as HTMLElement | null
    if (e.shiftKey && (active === first || !active || !document.getElementById('drawer')?.contains(active))) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && (active === last || !active || !document.getElementById('drawer')?.contains(active))) {
      e.preventDefault()
      first.focus()
    }
  }
}

/** 打开前的焦点位置：关闭后要还给触发卡片，否则键盘用户会掉到页面顶部。 */
let lastFocus: HTMLElement | null = null

watch(
  () => board.state.drawer,
  (d) => {
    events.value = []
    if (d) {
      const active = document.activeElement
      lastFocus = active instanceof HTMLElement ? active : null
      void refreshDetail()
      // 等 inert 解除、遮罩挂上之后再接管焦点。
      void nextTick(() => {
        const target = document.getElementById('d-close') as HTMLElement | null
        target?.focus()
      })
    } else {
      lastFocus?.focus?.()
      lastFocus = null
    }
  },
)

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  if (closeTimer) clearTimeout(closeTimer)
})
</script>

<template>
  <!-- 挂到 body，避免被 .content / .app 的层叠与 overflow 裁切。 -->
  <Teleport to="body">
  <div
    id="drawer-mask"
    class="drawer-mask"
    :class="{ open }"
    :hidden="!open"
    @click="close"
  />
  <aside
    id="drawer"
    class="drawer"
    :class="{ open }"
    role="dialog"
    aria-modal="true"
    :aria-hidden="open ? 'false' : 'true'"
    :inert="!open"
  >
    <div class="drawer-head">
      <div>
        <div class="id" id="d-id">{{ task?.id || '' }}</div>
        <h2 id="d-title">
          {{ task?.title || (zh ? '找不到该任务' : 'Task missing') }}
        </h2>
      </div>
      <button
        type="button"
        class="drawer-close"
        id="d-close"
        :aria-label="zh ? '关闭' : 'Close'"
        @click="close"
      >
        ×
      </button>
    </div>
    <div class="drawer-body" id="drawer-body">
      <template v-if="task">
        <div class="drawer-actions">
          <button
            type="button"
            class="copy primary"
            id="d-copy-prompt-top"
            @click="flashCopy('prompt', prompt, zh ? '已复制提示词' : 'Prompt copied')"
          >
            {{ label('prompt') || (zh ? '复制接入提示词' : 'Copy join prompt') }}
          </button>
          <button
            type="button"
            class="copy"
            id="d-copy-cmd-top"
            @click="flashCopy('cmd', showCmd, zh ? '已复制' : 'Copied')"
          >
            {{ label('cmd') || (zh ? '复制查看命令' : 'Copy show command') }}
          </button>
          <button
            v-if="cdWt"
            type="button"
            class="copy"
            id="d-copy-wt"
            @click="flashCopy('wt', cdWt, zh ? '已复制' : 'Copied')"
          >
            {{ label('wt') || (zh ? '复制进入 worktree' : 'Copy cd into worktree') }}
          </button>
        </div>

        <div class="drawer-section">
          <h3>{{ zh ? '基本信息' : 'Basics' }}</h3>
          <dl class="kv">
            <dt>{{ zh ? '状态' : 'Status' }}</dt>
            <dd>
              <span class="badge">{{ task.status }}</span>
              <code style="font-size:.68rem;color:var(--muted)">{{ task.status }}</code>
            </dd>
            <dt>{{ zh ? '优先级' : 'Priority' }}</dt>
            <dd>{{ task.priority ?? '—' }} · merge_order {{ task.merge_order ?? '—' }}</dd>
            <dt>{{ zh ? '负责人' : 'Owner' }}</dt>
            <dd>{{ task.owner || (zh ? '未指派' : 'unassigned') }}</dd>
            <dt>{{ zh ? '依赖' : 'Deps' }}</dt>
            <dd>{{ (task.deps && task.deps.length) ? task.deps.join(', ') : (zh ? '无' : 'none') }}</dd>
            <dt>{{ zh ? '尝试次数' : 'Attempts' }}</dt>
            <dd>{{ task.attempts ?? 0 }} · {{ zh ? '修订' : 'rev' }} {{ task.revision ?? 0 }}</dd>
            <dt>{{ zh ? '心跳' : 'Heartbeat' }}</dt>
            <dd>{{ fmtTime(task.heartbeat) }}</dd>
            <template v-if="task.blocked_reason">
              <dt>{{ zh ? '阻塞原因' : 'Blocked' }}</dt>
              <dd>{{ task.blocked_reason }}</dd>
            </template>
          </dl>
        </div>

        <div class="drawer-section">
          <h3>{{ zh ? '执行节点' : 'Execution' }}</h3>
          <dl class="kv">
            <dt>{{ zh ? '工程' : 'Project' }}</dt>
            <dd>{{ task.project_name || project?.name || '—' }}</dd>
            <dt>{{ zh ? '工程路径' : 'Path' }}</dt>
            <dd>{{ project?.path || '—' }}</dd>
            <dt>Worktree</dt>
            <dd>{{ task.worktree || '—' }}</dd>
            <dt>{{ zh ? '分支' : 'Branch' }}</dt>
            <dd>{{ task.branch || '—' }}</dd>
            <dt>Agent</dt>
            <dd>
              {{
                agent
                  ? `${agent.id} · ${agent.status || ''} · ${fmtTime(agent.last_seen)}`
                  : (task.owner || '—')
              }}
            </dd>
          </dl>
        </div>

        <div class="drawer-section">
          <h3>write_scope</h3>
          <div v-html="bullets(task.write_scope, zh ? '空' : 'empty')" />
        </div>
        <div class="drawer-section">
          <h3>read_scope</h3>
          <div v-html="bullets(task.read_scope, zh ? '无' : 'none')" />
        </div>
        <div class="drawer-section">
          <h3>acceptance</h3>
          <div v-html="bullets(task.acceptance, zh ? '无' : 'none')" />
        </div>

        <div class="drawer-section">
          <div class="sec-head">
            <h3>{{ zh ? '查看命令' : 'Show command' }}</h3>
            <button
              type="button"
              class="copy primary"
              id="d-copy-cmd"
              @click="flashCopy('cmd2', showCmd, zh ? '已复制' : 'Copied')"
            >
              {{ label('cmd2') || (zh ? '一键复制' : 'One-click copy') }}
            </button>
          </div>
          <div class="cmd-box">{{ showCmd }}</div>
        </div>

        <div class="drawer-section">
          <div class="sec-head">
            <h3>{{ zh ? '接入提示词' : 'Join prompt' }}</h3>
            <button
              type="button"
              class="copy primary"
              id="d-copy-prompt"
              @click="flashCopy('prompt2', prompt, zh ? '已复制提示词' : 'Prompt copied')"
            >
              {{ label('prompt2') || (zh ? '一键复制' : 'One-click copy') }}
            </button>
          </div>
          <div class="prompt-box" id="d-prompt">{{ prompt }}</div>
        </div>

        <div class="drawer-section">
          <h3>{{ zh ? '任务日志' : 'Task log' }}</h3>
          <p v-if="loading" class="empty">{{ zh ? '正在加载日志…' : 'Loading log…' }}</p>
          <ul v-else-if="events.length" class="timeline">
            <li v-for="(ev, i) in events" :key="i">
              <div class="ts">{{ fmtTime(ev.ts) }}</div>
              <div>
                <span class="ev-type">{{ ev.type }}</span>
                <span class="ev-actor">{{ ev.actor || '' }}</span>
              </div>
            </li>
          </ul>
          <p v-else class="empty">
            {{ zh ? '暂无任务日志（需 maw board --serve）' : 'No task log yet' }}
          </p>
        </div>
      </template>
      <p v-else class="empty">{{ zh ? '当前筛选下找不到该任务' : 'Task not found under current filters' }}</p>
    </div>
  </aside>
  </Teleport>
</template>
