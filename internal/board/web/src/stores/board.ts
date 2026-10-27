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
  projects?: Array<{ id: string; name?: string; path?: string }>
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
    const def = resolveView(registry, state.route.view)
    const patch: Partial<BoardRoute> = { project: id, detail: '' }
    if (id && !state.route.project) patch.view = 'board'
    if (!id && def.legacy) patch.view = 'nodes'
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
        state.subtitle = snap.updated_at
          ? `Updated ${snap.updated_at}`
          : state.lang === 'zh'
            ? '已同步'
            : 'Synced'
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
