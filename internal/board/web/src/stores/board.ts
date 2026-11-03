import { computed, reactive, readonly } from 'vue'
import {
  createRegistry,
  parseRoute,
  writeRoute,
  withRoute,
  sameRoute,
  resolveView,
} from '../lib/route'
import {
  normalizeLang,
  readLangPreference,
  persistLang,
  applyDocumentLang,
  createTranslator,
} from '../lib/i18n'
import { createDefaultSources, isStaticExport, stateForKind, LOADING_STATE } from '../lib/sources'

const registry = createRegistry()
const routeKnown = (id: string) => Boolean(registry.get(id))

function safeLocalStorage(): Storage | null {
  try {
    return typeof localStorage !== 'undefined' ? localStorage : null
  } catch {
    return null
  }
}

export type BoardRoute = {
  view: string
  project: string
  agent: string
  status: string
  search: string
  detail: string
  fromHash?: boolean
}

export type Snapshot = {
  projects?: Array<{ id: string; name?: string; path?: string; main_branch?: string }>
  agents?: Array<Record<string, unknown>>
  tasks?: Array<Record<string, unknown>>
  counts?: Record<string, number>
  total?: number
  done?: number
  active?: number
  blocked?: number
  percent?: number
  next_hint?: string
  risks?: string[]
  updated_at?: string
  filter_project?: string
  filter_agent?: string
  filter_status?: string
}

export type TaskDrawerSel = { tid: string; pid: string }

function countsFromTasks(tasks: Array<Record<string, unknown>> | undefined): Record<string, number> {
  const counts: Record<string, number> = {}
  for (const task of tasks || []) {
    const pid = String(task.project_id || '').trim()
    if (!pid) continue
    counts[pid] = (counts[pid] || 0) + 1
  }
  return counts
}

const state = reactive({
  route: parseRoute(
    typeof location !== 'undefined' ? location.search : '',
    typeof location !== 'undefined' ? location.hash : '',
    routeKnown,
  ) as BoardRoute,
  lang: normalizeLang(
    readLangPreference({
      search: typeof location !== 'undefined' ? location.search : '',
      storage: safeLocalStorage(),
      navigatorLanguage: typeof navigator !== 'undefined' ? navigator.language : '',
    }),
  ),
  snapshot: null as Snapshot | null,
  /** 各工程任务数（来自无筛选快照；有任务的在顶部下拉中标识）。 */
  projectTaskCounts: {} as Record<string, number>,
  syncOk: false,
  subtitle: 'Connecting…',
  payloads: {} as Record<string, unknown>,
  viewStates: {} as Record<string, unknown>,
  loadToken: 0,
  readOnly: typeof window !== 'undefined' ? isStaticExport(window) : false,
  /** 看板任务详情抽屉（与 vnext route.detail 分开，避免互相抢路由）。 */
  drawer: null as TaskDrawerSel | null,
  /** 正在等第二次确认的工程 id（两段式移除，避免一次误点就删）。 */
  removePending: '',
  /** 移除结果提示；kind=error 时界面必须显式红着说，不能静默。 */
  removeNotice: null as { kind: 'ok' | 'error'; text: string } | null,
})

const sources = createDefaultSources(typeof window !== 'undefined' ? window : null)

export const boardStore = {
  registry,
  state: readonly(state),
  raw: state,
  t: (key: string, params?: Record<string, string>) =>
    createTranslator(state.lang)(key, params),

  hasProject: computed(() => Boolean(String(state.route.project || '').trim())),
  currentDef: computed(() => resolveView(registry, state.route.view)),
  sidebarNav: computed(() => registry.sidebarNavModel()),

  setLang(lang: string) {
    state.lang = normalizeLang(lang)
    persistLang(state.lang, safeLocalStorage())
    if (typeof document !== 'undefined') applyDocumentLang(document, state.lang)
  },

  navigate(view: string, patch: Partial<BoardRoute> = {}, { replace = false } = {}) {
    const def = resolveView(registry, view)
    const next = withRoute(state.route, { view: def.id, detail: '', ...patch }) as BoardRoute
    if (!sameRoute(next, state.route)) {
      writeRoute(next, { history: window.history, location: window.location, replace })
    }
    Object.assign(state.route, next)
  },

  setProject(projectId: string) {
    const id = String(projectId || '').trim()
    const patch: Partial<BoardRoute> = { project: id, detail: '' }
    if (id && !state.route.project) patch.view = 'board'
    // 选「全部工程」保持当前视图：契约要求多工程时回到看板并禁用拓扑，
    // 而不是把用户悄悄带去另一个视图。
    if (!id && state.route.view === 'topo') patch.view = 'board'
    const next = withRoute(state.route, patch) as BoardRoute
    writeRoute(next, { history: window.history, location: window.location, replace: true })
    Object.assign(state.route, next)
  },

  patchFilters(patch: Partial<Pick<BoardRoute, 'project' | 'agent' | 'status' | 'search'>>) {
    const next = withRoute(state.route, patch) as BoardRoute
    writeRoute(next, { history: window.history, location: window.location, replace: true })
    Object.assign(state.route, next)
  },

  openDetail(id: string) {
    const next = withRoute(state.route, { detail: String(id || '') }) as BoardRoute
    writeRoute(next, { history: window.history, location: window.location })
    Object.assign(state.route, next)
  },

  closeDetail() {
    const next = withRoute(state.route, { detail: '' }) as BoardRoute
    writeRoute(next, { history: window.history, location: window.location, replace: true })
    Object.assign(state.route, next)
  },

  openTaskDrawer(tid: string, pid = '') {
    const id = String(tid || '').trim()
    if (!id) return
    state.drawer = { tid: id, pid: String(pid || '').trim() }
  },

  closeTaskDrawer() {
    state.drawer = null
  },

  patchSnapshotTask(tid: string, pid: string, patch: Record<string, unknown>) {
    const tasks = state.snapshot?.tasks
    if (!tasks) return
    const idx = tasks.findIndex(
      (x) => String(x.id) === tid && String(x.project_id || '') === String(pid || ''),
    )
    if (idx < 0) return
    tasks[idx] = { ...tasks[idx], ...patch }
  },

  /** 第一段：把某一行切到「确认移除？」；同时清掉上一条结果提示。 */
  requestRemoveProject(projectId: string) {
    state.removePending = String(projectId || '').trim()
    state.removeNotice = null
  },

  cancelRemoveProject() {
    state.removePending = ''
  },

  /**
   * 第二段：真正发删除请求。
   *
   * 成功前不改任何本地列表：本地先删、服务端拒绝，会让人以为已经删掉了。
   * 只有 2xx 之后才刷新快照与工程视图。
   */
  async removeProject(projectId: string) {
    const target = String(projectId || '').trim()
    if (!target) return
    const outcome = await sources.removeProject(target)
    state.removePending = ''
    if (!outcome.ok) {
      state.removeNotice = { kind: 'error', text: removalErrorText(outcome, state.lang) }
      return
    }
    state.removeNotice = {
      kind: 'ok',
      text: state.lang === 'zh' ? '已移除工程登记' : 'Project registration removed',
    }
    // 被删的正是当前筛选工程：把筛选清掉，否则后续视图会一直空着。
    if (String(state.route.project || '') === target) {
      state.route.project = ''
    }
    await boardStore.fetchSnapshot()
    await boardStore.loadView('projects')
  },

  syncFromLocation() {
    const next = parseRoute(location.search, location.hash, routeKnown) as BoardRoute
    Object.assign(state.route, next)
  },

  applyProjectTaskCounts(snap: Snapshot, filtered: boolean) {
    const fromTasks = countsFromTasks(snap.tasks)
    if (!filtered) {
      state.projectTaskCounts = fromTasks
      return
    }
    const pid = String(state.route.project || '').trim()
    if (!pid) return
    state.projectTaskCounts = { ...state.projectTaskCounts, [pid]: fromTasks[pid] || 0 }
  },

  async refreshProjectTaskCounts() {
    const urls = ['/api/board', './snapshot.json']
    for (const url of urls) {
      try {
        const res = await fetch(url, { headers: { Accept: 'application/json' } })
        if (!res.ok) continue
        const snap = (await res.json()) as Snapshot
        boardStore.applyProjectTaskCounts(snap, false)
        return
      } catch {
        /* try next */
      }
    }
  },

  async fetchSnapshot() {
    const params = new URLSearchParams()
    if (state.route.project) params.set('project', state.route.project)
    if (state.route.agent) params.set('agent', state.route.agent)
    if (state.route.status) params.set('status', state.route.status)
    const q = params.toString()
    const urls = [`/api/board${q ? `?${q}` : ''}`, `./snapshot.json${q ? `?${q}` : ''}`]
    for (const url of urls) {
      try {
        const res = await fetch(url, { headers: { Accept: 'application/json' } })
        if (!res.ok) continue
        const snap = (await res.json()) as Snapshot
        state.snapshot = snap
        boardStore.applyProjectTaskCounts(snap, Boolean(state.route.project))
        state.syncOk = true
        // updated_at 是数据水位（最近事件时间）。没有事件时说「尚无事件」，
        // 而不是拿请求时刻冒充「刚刚更新」。
        state.subtitle = snap.updated_at
          ? `Updated ${snap.updated_at}`
          : state.lang === 'zh'
            ? '尚无事件'
            : 'No events yet'
        return snap
      } catch {
        /* try next */
      }
    }
    state.syncOk = false
    state.subtitle = state.lang === 'zh' ? '离线 / 无法连接' : 'Offline'
    return null
  },

  async loadView(kind: string) {
    if (!kind) return
    const token = ++state.loadToken
    state.viewStates[kind] = LOADING_STATE
    const route = { ...state.route }
    let payload: unknown = { unavailable: true }
    try {
      const src = (sources as Record<string, unknown>)[kind]
      if (typeof src === 'function') {
        payload = await (src as (r: BoardRoute) => Promise<unknown>)(route)
      }
      if (kind === 'tasks' && payload && typeof payload === 'object' && route.search) {
        const p = payload as { tasks?: unknown[]; allTasks?: unknown[] }
        const all = Array.isArray(p.tasks) ? p.tasks : []
        const needle = String(route.search).trim().toLowerCase()
        p.allTasks = all
        p.tasks = all.filter((task: unknown) => {
          const t = task as Record<string, unknown>
          return ['display_id', 'instance_id', 'id', 'title', 'owner'].some((key) =>
            String(t?.[key] ?? '').toLowerCase().includes(needle),
          )
        })
      }
    } catch {
      payload = { unavailable: true }
    }
    if (token !== state.loadToken) return
    state.payloads[kind] = payload
    state.viewStates[kind] = stateForKind(kind, payload, { readOnly: state.readOnly })
  },

  connectEvents() {
    if (typeof window === 'undefined' || typeof EventSource === 'undefined') return null
    if (state.readOnly) return null
    try {
      const stream = new EventSource('/api/events')
      stream.addEventListener('ready', () => {
        void boardStore.fetchSnapshot()
        void boardStore.refreshProjectTaskCounts()
      })
      stream.addEventListener('board-changed', () => {
        void boardStore.fetchSnapshot()
        void boardStore.refreshProjectTaskCounts()
      })
      return stream
    } catch {
      return null
    }
  },
}

export function useBoard() {
  return boardStore
}

/**
 * 把移除失败翻成人话。
 *
 * 按 code 而不是服务端 message 映射：message 是中文的，英文界面照搬会出现中文；
 * 未知 code 才回落到状态码，绝不把失败说成成功。
 */
export function removalErrorText(
  outcome: { code?: string; status: number },
  lang: string,
): string {
  const zh = lang !== 'en'
  switch (outcome.code) {
    case 'project_busy':
      return zh
        ? '工程还有未完成的任务或在线 Agent，先收尾再移除'
        : 'Tasks or Agents are still live; finish them before removing'
    case 'not_found':
      return zh ? '工程已不在中枢里（可能刚被移除）' : 'Project is no longer registered'
    case 'unauthenticated':
    case 'csrf_failed':
      return zh ? '会话已过期，刷新页面后重试' : 'Session expired; refresh the page and retry'
    case 'insufficient_scope':
      return zh ? '当前凭据没有写权限' : 'This credential has no write scope'
    case 'invalid_project_id':
      return zh ? '工程 id 形态不对' : 'Malformed project id'
    case 'network_error':
      return zh ? '连不上本机中枢' : 'Cannot reach the local hub'
    default:
      return zh
        ? `移除失败（HTTP ${outcome.status}）`
        : `Removal failed (HTTP ${outcome.status})`
  }
}
