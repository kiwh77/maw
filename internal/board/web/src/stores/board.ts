import { computed, reactive, readonly } from 'vue'
import {
  createRegistry,
  parseRoute,
  writeRoute,
  withRoute,
  sameRoute,
  resolveView,
  DEFAULT_VIEW,
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
  iteration: string
  search: string
  detail: string
  fromHash?: boolean
}

export type Snapshot = {
  projects?: Array<{ id: string; name?: string; path?: string; main_branch?: string }>
  agents?: Array<Record<string, unknown>>
  iterations?: Array<{ id: string; name?: string; status?: string }>
  tasks?: Array<Record<string, unknown>>
  counts?: Record<string, number>
  total?: number
  done?: number
  active?: number
  blocked?: number
  percent?: number
  next_hint?: string
  risks?: string[]
  /** 运行问题（BuildProblems 聚合结果）；问题审查视图只读消费，不在此派发动作。 */
  problems?: Array<Record<string, unknown>>
  updated_at?: string
  filter_project?: string
  filter_agent?: string
  filter_status?: string
  filter_iteration?: string | null
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
  /** 正在等第二次确认的节点 id。 */
  pendingNodeRemove: '',
  /** 节点表单预设：claude / codex / custom。 */
  nodeFormPreset: 'custom' as 'claude' | 'codex' | 'custom',
  /** 节点新增／编辑模态；关闭时为 null。 */
  nodeModal: null as null | {
    mode: 'create' | 'edit'
    draft: {
      id: string
      name: string
      entry: string
      slots: number
      auto_probe: boolean
      heartbeat_interval_sec: number
      default_model: string
    }
  },
  /** 工程编辑模态；关闭时为 null。策略字段快照不回显，留空即「保持不变」。 */
  projectModal: null as null | {
    projectId: string
    draft: {
      name: string
      path: string
      mainBranch: string
      maxParallel: string
      autoEnabled: '' | 'true' | 'false'
      limit: string
      allowedNodes: string
      workerNode: string
      llmNode: string
    }
    baseline: {
      name: string
      path: string
      mainBranch: string
      maxParallel: number
    }
  },
  /** 移除结果提示；kind=error 时界面必须显式红着说，不能静默。 */
  removeNotice: null as { kind: 'ok' | 'error'; text: string } | null,
  /** 正在自检的节点 id；非空时该节点按钮进「检测中」并禁用重复点击。 */
  nodeCheckPending: '',
})

const sources = createDefaultSources(typeof window !== 'undefined' ? window : null)

/**
 * 搜索框是本地过滤：接口不认 q 参数，只认 project/agent/status。
 * 看板与执行视图共用同一条匹配规则，免得同一个关键词在两个页面命中不同行。
 */
function matchesSearch(task: unknown, needle: string) {
  const t = task as Record<string, unknown>
  return ['display_id', 'instance_id', 'id', 'title', 'owner'].some((key) =>
    String(t?.[key] ?? '').toLowerCase().includes(needle),
  )
}

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
    // 观测四类视图走全局聚合路由，未选工程也可进入（顶部即「全部工程」）。
    // 拓扑例外（BUX-FE-001）：未选工程时按多工程 band 出全局拓扑。
    if (def?.requiresProject && def.id !== 'topo' && !String(state.route.project || '').trim()) return
    const next = withRoute(state.route, { view: def.id, detail: '', ...patch }) as BoardRoute
    if (!sameRoute(next, state.route)) {
      writeRoute(next, { history: window.history, location: window.location, replace })
    }
    Object.assign(state.route, next)
  },

  setProject(projectId: string) {
    const id = String(projectId || '').trim()
    const patch: Partial<BoardRoute> = { project: id, detail: '' }
    // 从「全部工程」选到一个工程：回到默认视图（EV-003 起是执行视图）。
    // 以前这里写死 board，默认页一改就会把用户从执行视图弹回旧看板。
    if (id && !state.route.project) patch.view = DEFAULT_VIEW
    // 取消选择工程时保持当前视图：拓扑已支持全局多工程 band（BUX-FE-001），
    // 不再把用户从拓扑视图悄悄带回看板。
    const next = withRoute(state.route, patch) as BoardRoute
    writeRoute(next, { history: window.history, location: window.location, replace: true })
    Object.assign(state.route, next)
  },

  patchFilters(patch: Partial<Pick<BoardRoute, 'project' | 'agent' | 'status' | 'iteration' | 'search'>>) {
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

  applyNodePreset(preset: 'claude' | 'codex' | 'custom') {
    state.nodeFormPreset = preset
    state.removeNotice = null
  },

  openCreateNodeModal(preset: 'claude' | 'codex' | 'custom' = 'custom') {
    const base = {
      slots: 1,
      auto_probe: true,
      heartbeat_interval_sec: 60,
      default_model: '',
    }
    const p =
      preset === 'claude'
        ? { ...base, id: 'claude', name: 'Claude Code', entry: 'claude', default_model: 'sonnet' }
        : preset === 'codex'
          ? { ...base, id: 'codex', name: 'Codex CLI', entry: 'codex', default_model: 'gpt-5' }
          : { ...base, id: '', name: '', entry: '' }
    state.nodeFormPreset = preset
    state.removeNotice = null
    state.nodeModal = { mode: 'create', draft: p }
  },

  openEditNodeModal(node: Record<string, unknown>) {
    const meta = (node.meta && typeof node.meta === 'object' ? node.meta : {}) as Record<string, unknown>
    const argv = Array.isArray(meta.argv) ? (meta.argv as unknown[]) : []
    const entry = String(meta.entry_path || argv[0] || node.entry_path || '').trim()
    const slotsRaw = (node.slots as { total?: number } | undefined)?.total ?? node.slot_limit ?? 1
    const interval = Number(meta.heartbeat_interval_sec)
    state.nodeFormPreset = 'custom'
    state.removeNotice = null
    state.nodeModal = {
      mode: 'edit',
      draft: {
        id: String(node.id || node.node_id || '').trim(),
        name: String(node.name || node.label || '').trim(),
        entry,
        slots: Number(slotsRaw) > 0 ? Number(slotsRaw) : 1,
        auto_probe: meta.auto_probe !== false,
        heartbeat_interval_sec: interval >= 5 ? interval : 60,
        default_model: String(meta.default_model || '').trim(),
      },
    }
  },

  closeNodeModal() {
    state.nodeModal = null
  },

  requestRemoveNode(nodeId: string) {
    state.pendingNodeRemove = String(nodeId || '').trim()
    state.removeNotice = null
  },

  cancelRemoveNode() {
    state.pendingNodeRemove = ''
  },

  /** 节点页登记：写入当前工程（或快照首个工程）。 */
  async registerNode(input: {
    id: string
    name?: string
    entry: string
    slots?: number
    auto_probe?: boolean
    heartbeat_interval_sec?: number
    default_model?: string
  }) {
    const zh = state.lang === 'zh'
    let projectId = String(state.route.project || '').trim()
    if (!projectId) {
      const first = (state.snapshot?.projects || [])[0]
      projectId = String(first?.id || '').trim()
    }
    if (!projectId) {
      state.removeNotice = {
        kind: 'error',
        text: zh ? '请先在顶栏选择工程，再登记节点' : 'Select a project before registering a node',
      }
      return
    }
    const outcome = await sources.registerNode(projectId, input)
    if (!outcome.ok) {
      state.removeNotice = { kind: 'error', text: removalErrorText(outcome, state.lang) }
      return
    }
    state.removeNotice = {
      kind: 'ok',
      text: zh ? `已登记节点 ${input.id}` : `Registered node ${input.id}`,
    }
    state.nodeFormPreset = 'custom'
    state.nodeModal = null
    await boardStore.loadView('nodes')
  },

  async removeNode(nodeId: string) {
    const target = String(nodeId || '').trim()
    if (!target) return
    const outcome = await sources.removeNode(target)
    state.pendingNodeRemove = ''
    if (!outcome.ok) {
      state.removeNotice = { kind: 'error', text: removalErrorText(outcome, state.lang) }
      return
    }
    state.removeNotice = {
      kind: 'ok',
      text: state.lang === 'zh' ? `已删除节点 ${target}` : `Removed node ${target}`,
    }
    await boardStore.loadView('nodes')
  },

  /**
   * 「测试环境」：同步跑一次节点 CLI 自检。
   *
   * 自检可能持续十几秒，所以先置 pending 让按钮进「检测中」；完成后把
   * 版本／失败原因写进 removeNotice，并重载节点页以刷新 meta 与四维状态。
   */
  async checkNode(nodeId: string) {
    const target = String(nodeId || '').trim()
    if (!target || state.nodeCheckPending) return
    const zh = state.lang === 'zh'
    state.nodeCheckPending = target
    state.removeNotice = null
    const outcome = await sources.checkNode(target)
    state.nodeCheckPending = ''
    if (!outcome.ok) {
      state.removeNotice = { kind: 'error', text: removalErrorText(outcome, state.lang) }
      return
    }
    const report = outcome.report
    if (report?.ok) {
      const version = String(report.version || '').trim()
      state.removeNotice = {
        kind: 'ok',
        text: zh
          ? `${target} 环境正常${version ? ' · ' + version : ''}（${report.latency_ms} ms）`
          : `${target} environment OK${version ? ' · ' + version : ''} (${report.latency_ms} ms)`,
      }
    } else {
      const reason = String(report?.error || '').trim()
      state.removeNotice = {
        kind: 'error',
        text: zh
          ? `${target} 环境异常${reason ? '：' + reason : ''}`
          : `${target} environment failed${reason ? ': ' + reason : ''}`,
      }
    }
    await boardStore.loadView('nodes')
  },

  /**
   * 打开工程编辑模态。
   *
   * 快照能读到的字段（名称／路径／主分支／max_parallel）直接回填；执行策略
   * 写在工程 config.toml，快照不回显，所以留空交给用户按需填写。
   */
  openEditProjectModal(projectId: string) {
    const target = String(projectId || '').trim()
    if (!target) return
    const viewState = state.viewStates.projects as
      | { projects?: Array<Record<string, unknown>> }
      | undefined
    const rows: Array<Record<string, unknown>> = [
      ...(Array.isArray(viewState?.projects) ? viewState.projects : []),
      ...((state.snapshot?.projects || []) as Array<Record<string, unknown>>),
    ]
    const row = rows.find((p) => String(p?.id || p?.project_id || '') === target)
    const name = String(row?.name || target)
    const path = String(row?.path || '')
    const mainBranch = String(row?.main_branch || '')
    const refRaw = Number(row?.max_parallel_reference ?? row?.max_parallel)
    const ref = Number.isFinite(refRaw) && refRaw > 0 ? Math.trunc(refRaw) : 0
    state.removeNotice = null
    state.projectModal = {
      projectId: target,
      draft: {
        name,
        path,
        mainBranch,
        maxParallel: ref > 0 ? String(ref) : '',
        autoEnabled: '',
        limit: '',
        allowedNodes: '',
        workerNode: '',
        llmNode: '',
      },
      baseline: { name, path, mainBranch, maxParallel: ref },
    }
  },

  closeProjectModal() {
    state.projectModal = null
  },

  /**
   * 保存工程编辑：只 PATCH 真正改动的字段。
   *
   * 未填写的策略字段不进入请求，因此不会被误判成「清空」；主分支／并行数
   * 未改动时也不会触发 config.toml 回写（工程没初始化时就不会因此报错）。
   */
  async saveProjectEdit(draft: {
    name: string
    path: string
    mainBranch: string
    maxParallel: string
    autoEnabled: string
    limit: string
    allowedNodes: string
    workerNode: string
    llmNode: string
  }) {
    const modal = state.projectModal
    if (!modal) return
    const zh = state.lang === 'zh'
    const base = modal.baseline
    const patch: Record<string, unknown> = {}

    const name = String(draft.name || '').trim()
    if (!name) {
      state.removeNotice = {
        kind: 'error',
        text: zh ? '工程名称不能为空' : 'Project name is required',
      }
      return
    }
    if (name !== base.name) patch.name = name

    const path = String(draft.path || '').trim()
    if (path && path !== base.path) patch.path = path

    const branch = String(draft.mainBranch || '').trim()
    if (branch && branch !== base.mainBranch) patch.main_branch = branch

    const refRaw = String(draft.maxParallel || '').trim()
    const ref = refRaw === '' ? 0 : Number(refRaw)
    if (Number.isFinite(ref) && Math.trunc(ref) !== base.maxParallel) {
      patch.max_parallel = Math.max(0, Math.trunc(ref))
    }

    if (draft.autoEnabled === 'true' || draft.autoEnabled === 'false') {
      patch.auto_enabled = draft.autoEnabled === 'true'
    }

    const limitRaw = String(draft.limit || '').trim()
    if (limitRaw) {
      const limit = Number(limitRaw)
      if (Number.isFinite(limit) && limit >= 1) patch.limit = Math.trunc(limit)
    }

    const nodesRaw = String(draft.allowedNodes || '').trim()
    if (nodesRaw) {
      patch.allowed_nodes = nodesRaw
        .split(/[,，\s]+/)
        .map((s) => s.trim())
        .filter(Boolean)
    }

    const worker = String(draft.workerNode || '').trim()
    if (worker) patch.worker_node = worker

    const llm = String(draft.llmNode || '').trim()
    if (llm) patch.llm_node = llm

    if (!Object.keys(patch).length) {
      state.projectModal = null
      state.removeNotice = { kind: 'ok', text: zh ? '没有改动' : 'No changes' }
      return
    }

    const outcome = await sources.updateProject(modal.projectId, patch)
    if (!outcome.ok) {
      state.removeNotice = { kind: 'error', text: removalErrorText(outcome, state.lang) }
      return
    }
    state.projectModal = null
    state.removeNotice = {
      kind: 'ok',
      text: zh ? '工程已更新' : 'Project updated',
    }
    await boardStore.fetchSnapshot()
    await boardStore.loadView('projects')
  },

  syncFromLocation() {
    const next = parseRoute(location.search, location.hash, routeKnown) as BoardRoute
    Object.assign(state.route, next)
    // 前进后退改的是筛选，看板数据也得跟着换：否则地址栏变了、卡片还是旧的。
    void boardStore.fetchSnapshot()
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
    if (state.route.iteration) params.set('iteration', state.route.iteration)
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

  /**
   * 自愈统计（SH-503）单独取数：它不是一个注册视图，挂在上方 analytics 页签里，
   * 因此不走 loadView 的 viewStates 状态机（那个状态机按 DATA_KINDS 判类型）。
   */
  async fetchSystemHealth() {
    const payload = await sources.systemHealth({ project: state.route.project })
    state.payloads.system_health = payload
    return payload
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
        p.tasks = all.filter((task: unknown) => matchesSearch(task, needle))
      }
      // 执行视图的搜索同样只在本地过滤任务行；运行与交付不参与匹配，
      // 它们挂在任务上，任务被筛掉后自然不在这一屏。
      if (kind === 'execution' && payload && typeof payload === 'object' && route.search) {
        const p = payload as { tasks?: unknown[] }
        const all = Array.isArray(p.tasks) ? p.tasks : []
        const needle = String(route.search).trim().toLowerCase()
        p.tasks = all.filter((task: unknown) => matchesSearch(task, needle))
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
    case 'invalid_node_id':
      return zh ? '节点 id 不合法（需小写字母开头）' : 'Malformed node id'
    case 'invalid_entry_path':
      return zh ? '请填写可执行文件路径' : 'Executable path is required'
    case 'network_error':
      return zh ? '连不上本机中枢' : 'Cannot reach the local hub'
    default:
      return zh
        ? `移除失败（HTTP ${outcome.status}）`
        : `Removal failed (HTTP ${outcome.status})`
  }
}
