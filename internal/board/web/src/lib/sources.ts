export {
  stateForKind,
  findDetail,
  needsProject,
  LOADING_STATE,
  NO_PROJECT_STATE,
  errorState,
  DATA_KINDS,
} from '../../../assets/shell/view-context.mjs'

const TASKS_SOURCE = '/api/board'

/** 节点是系统级资源：同一台机器上的 CLI 对任何工程都可用，不走工程过滤。 */
const NODES_SOURCE = '/api/v1/nodes'

const PROJECT_RESOURCES = {
  timeline: 'timeline',
  activity: 'activity',
  sessions: 'sessions',
  analytics: 'analytics',
} as const

/** 工程级控制资源：服务端只有按工程的路由，没有全局列表。 */
const CONTROL_RESOURCES = ['runs', 'deliveries'] as const
type ControlResource = (typeof CONTROL_RESOURCES)[number]

export function projectResourcePath(kind: string, projectId: string) {
  const resource = PROJECT_RESOURCES[kind as keyof typeof PROJECT_RESOURCES]
  if (!resource) return ''
  const id = String(projectId || '').trim()
  if (!id) return ''
  return `/api/v1/projects/${encodeURIComponent(id)}/${resource}`
}

/** 观测资源地址：有工程走工程路由，没有工程（「全部工程」）走全局聚合路由。 */
export function resourcePath(kind: string, projectId: string) {
  const resource = PROJECT_RESOURCES[kind as keyof typeof PROJECT_RESOURCES]
  if (!resource) return ''
  const id = String(projectId || '').trim()
  if (!id) return `/api/v1/${resource}`
  return projectResourcePath(kind, id)
}

export function controlResourcePath(kind: ControlResource, projectId: string) {
  const id = String(projectId || '').trim()
  if (!id) return ''
  return `/api/v1/projects/${encodeURIComponent(id)}/${kind}`
}

export function capabilitiesPath(projectId: string) {
  const id = String(projectId || '').trim()
  if (!id) return ''
  return `/api/v1/projects/${encodeURIComponent(id)}/observability/capabilities`
}

export function isStaticExport(win: Window | null | undefined) {
  const protocol = win?.location ? String(win.location.protocol || '') : ''
  return protocol === 'file:'
}

type RouteLike = { project?: string; agent?: string; status?: string; iteration?: string }

type JsonFail = { unavailable: true; status: number; unauthenticated?: boolean }

/** 写操作结果：成功也只认 HTTP 2xx + ok:true，不做乐观更新。 */
export type RemovalOutcome = {
  ok: boolean
  status: number
  code?: string
  message?: string
  unauthenticated?: boolean
}

/** 一次 CLI 环境自检的同步结果（POST /api/v1/nodes/{id}/checks）。 */
export type NodeCheckReport = {
  node_id: string
  ok: boolean
  version?: string
  latency_ms: number
  checked_at: string
  checks: Array<{ name: string; ok: boolean; detail?: string }>
  error?: string
  meta?: Record<string, unknown>
}

export type NodeCheckOutcome = RemovalOutcome & { report?: NodeCheckReport }

/** 会话写请求的双提交 Cookie（契约 §7.1）。 */
const CSRF_COOKIE = 'maw_csrf'

function readCookie(win: Window | null, name: string): string {
  const raw = win?.document?.cookie || ''
  if (!raw) return ''
  for (const part of raw.split(';')) {
    const i = part.indexOf('=')
    if (i < 0) continue
    if (part.slice(0, i).trim() === name) return decodeURIComponent(part.slice(i + 1).trim())
  }
  return ''
}

function isControlResource(kind: string): kind is ControlResource {
  return (CONTROL_RESOURCES as readonly string[]).includes(kind)
}

export function createDefaultSources(win: Window | null, fetchImpl: typeof fetch | null = null) {
  const doFetch =
    fetchImpl || (win && typeof win.fetch === 'function' ? win.fetch.bind(win) : null)

  let sessionPromise: Promise<boolean> | null = null

  /** 浏览器读 /api/v1 需要本机会话 Cookie；幂等 POST，失败不挡住公开接口。 */
  async function ensureSession(): Promise<boolean> {
    if (!doFetch) return false
    if (!sessionPromise) {
      sessionPromise = (async () => {
        try {
          const res = await doFetch('/api/session', {
            method: 'POST',
            headers: { Accept: 'application/json' },
            credentials: 'same-origin',
          })
          return Boolean(res && res.ok)
        } catch {
          return false
        }
      })()
    }
    const ok = await sessionPromise
    if (!ok) sessionPromise = null
    return ok
  }

  async function getJSON(url: string, opts: { auth?: boolean } = {}): Promise<Record<string, unknown> | JsonFail> {
    if (!doFetch) return { unavailable: true, status: 0 }
    if (opts.auth) await ensureSession()
    try {
      let res = await doFetch(url, {
        headers: { Accept: 'application/json' },
        credentials: 'same-origin',
      })
      // 会话过期或首次未带 Cookie：补发一次会话再重试。
      if (res && res.status === 401 && opts.auth) {
        sessionPromise = null
        const issued = await ensureSession()
        if (issued) {
          res = await doFetch(url, {
            headers: { Accept: 'application/json' },
            credentials: 'same-origin',
          })
        }
      }
      if (!res || !res.ok) {
        const fail: JsonFail = { unavailable: true, status: res ? res.status : 0 }
        if (res && res.status === 401) fail.unauthenticated = true
        return fail
      }
      return (await res.json()) as Record<string, unknown>
    } catch {
      return { unavailable: true, status: 0 }
    }
  }

  /** 把契约的 items 对齐成视图认识的资源名字段。 */
  function withNamedList(kind: string, payload: Record<string, unknown> | JsonFail) {
    if (!payload || (payload as JsonFail).unavailable === true) return payload
    const body = payload as Record<string, unknown>
    if (!Array.isArray(body[kind]) && Array.isArray(body.items)) {
      return { ...body, [kind]: body.items }
    }
    return body
  }

  async function fetchControl(kind: ControlResource, projectId: string) {
    const url = controlResourcePath(kind, projectId)
    if (!url) return { unavailable: true as const, status: 0 }
    return withNamedList(kind, await getJSON(url, { auth: true }))
  }

  /**
   * 未选工程时：按看板登记的工程逐个拉并合并。
   * 控制类没有全局列表接口；空合并是「查过了没有」，不是加载失败。
   */
  async function fetchControlAll(kind: ControlResource) {
    const board = await getJSON(TASKS_SOURCE)
    if (!board || (board as JsonFail).unavailable === true) {
      return board || { unavailable: true as const, status: 0 }
    }
    const projects = Array.isArray((board as { projects?: unknown }).projects)
      ? ((board as { projects: Array<{ id?: string; project_id?: string }> }).projects)
      : []
    const ids = projects
      .map((p) => String(p?.id || p?.project_id || '').trim())
      .filter(Boolean)
    if (!ids.length) return { [kind]: [], items: [] }
    const parts = await Promise.all(ids.map((id) => fetchControl(kind, id)))
    if (parts.every((p) => p && (p as JsonFail).unauthenticated === true)) {
      return { unavailable: true as const, status: 401, unauthenticated: true }
    }
    if (parts.every((p) => p && (p as JsonFail).unavailable === true)) {
      return { unavailable: true as const, status: (parts[0] as JsonFail)?.status || 0 }
    }
    const items = parts.flatMap((p) => {
      if (!p || (p as JsonFail).unavailable === true) return []
      const body = p as Record<string, unknown>
      if (Array.isArray(body[kind])) return body[kind] as unknown[]
      if (Array.isArray(body.items)) return body.items as unknown[]
      return []
    })
    return { [kind]: items, items }
  }

  function projectSource(kind: string) {
    return async (route: RouteLike) => {
      const url = resourcePath(kind, route?.project || '')
      if (!url) return { unavailable: true, status: 0 }
      return getJSON(url, { auth: true })
    }
  }

  function controlSource(kind: ControlResource) {
    return async (route: RouteLike) => {
      const project = String(route?.project || '').trim()
      if (project) return fetchControl(kind, project)
      return fetchControlAll(kind)
    }
  }

  /** 看板快照（带当前筛选）。执行视图与任务视图共用同一个来源，抽屉才找得到行。 */
  async function fetchTasks(route: RouteLike) {
    const params = new URLSearchParams()
    if (route.project) params.set('project', route.project)
    if (route.agent) params.set('agent', route.agent)
    if (route.status) params.set('status', route.status)
    if (route.iteration) params.set('iteration', route.iteration)
    const query = params.toString()
    return getJSON(TASKS_SOURCE + (query ? `?${query}` : ''))
  }

  /** 从列表载荷里取数组：契约用资源名，分页外壳用 `items`。 */
  function listOf(payload: Record<string, unknown> | JsonFail, name: string) {
    if (!payload || (payload as JsonFail).unavailable === true) return null
    const body = payload as Record<string, unknown>
    if (Array.isArray(body[name])) return body[name] as unknown[]
    if (Array.isArray(body.items)) return body.items as unknown[]
    return null
  }

  return {
    tasks: fetchTasks,
    /**
     * 执行视图（EV-001 / EV-003）：任务、运行、交付三类一次取齐。
     *
     * 三类缺一类就整份判失败，不返回「有任务、零运行」这种半成品——
     * 渲染层据此把整页标成加载失败，而不是显示成「这批题没跑过」。
     *
     * 没有工程时不发「先选工程」的空转：运行与交付按登记工程逐个取再合并，
     * 与看板「全部工程」同一口径（迭代按工程分组由视图负责）。
     */
    async execution(route: RouteLike) {
      const project = String(route?.project || '').trim()
      const [taskPayload, runsPayload, deliveriesPayload] = await Promise.all([
        fetchTasks(route),
        project ? fetchControl('runs', project) : fetchControlAll('runs'),
        project ? fetchControl('deliveries', project) : fetchControlAll('deliveries'),
      ])
      const parts = [taskPayload, runsPayload, deliveriesPayload]
      if (parts.some((p) => p && (p as JsonFail).unauthenticated === true)) {
        return { unavailable: true as const, status: 401, unauthenticated: true }
      }
      const failed = parts.find((p) => !p || (p as JsonFail).unavailable === true) as
        | JsonFail
        | undefined
      if (failed) return { unavailable: true as const, status: failed.status || 0 }
      const tasks = listOf(taskPayload, 'tasks')
      const runs = listOf(runsPayload, 'runs')
      const deliveries = listOf(deliveriesPayload, 'deliveries')
      if (!tasks || !runs || !deliveries) return { unavailable: true as const, status: 0 }
      return { tasks, runs, deliveries }
    },
    // 节点是系统级资源：选中任何工程都读同一份全局节点，不再按工程合并。
    nodes: async () => withNamedList('nodes', await getJSON(NODES_SOURCE, { auth: true })),
    runs: controlSource('runs'),
    deliveries: controlSource('deliveries'),
    timeline: projectSource('timeline'),
    activity: projectSource('activity'),
    sessions: projectSource('sessions'),
    analytics: projectSource('analytics'),
    /** 问题页：从看板快照 + 运行列表提炼阻塞/异常信号。 */
    async problems(route: RouteLike) {
      const board = await fetchTasks(route)
      if (!board || (board as JsonFail).unavailable === true) {
        return board || { unavailable: true as const, status: 0 }
      }
      const project = String(route?.project || '').trim()
      const runsPayload = project
        ? await fetchControl('runs', project)
        : await fetchControlAll('runs')
      const tasks = listOf(board, 'tasks') || []
      const runs = listOf(runsPayload, 'runs') || []
      const agents = Array.isArray((board as { agents?: unknown[] }).agents)
        ? ((board as { agents: Array<Record<string, unknown>> }).agents)
        : []
      const items: Array<Record<string, string>> = []
      for (const task of tasks as Array<Record<string, unknown>>) {
        if (String(task.status || '') !== 'blocked') continue
        items.push({
          kind: 'blocked',
          ref: String(task.id || task.display_id || ''),
          detail: String(task.blocked_reason || task.title || ''),
        })
      }
      for (const run of runs as Array<Record<string, unknown>>) {
        const recon = String(run.reconciliation_state || run.reconciliation || '')
        const phase = String(run.phase || '')
        if (!recon && phase !== 'failed' && phase !== 'rejected') continue
        if (recon === 'confirmed' || recon === '') {
          if (phase !== 'failed' && phase !== 'rejected') continue
        }
        items.push({
          kind: 'run',
          ref: String(run.run_id || run.id || ''),
          detail: [phase, recon].filter(Boolean).join(' / '),
        })
      }
      for (const agent of agents) {
        if (String(agent.status || '') !== 'offline') continue
        const taskId = String(agent.task_id || '').trim()
        if (!taskId) continue
        items.push({
          kind: 'agent',
          ref: String(agent.id || ''),
          detail: 'offline while holding ' + taskId,
        })
      }
      return { items, problems: items }
    },
    /** 日志页：拉 /api/v1/events 最近事件。 */
    async logs(route: RouteLike) {
      const params = new URLSearchParams()
      params.set('limit', '100')
      if (route?.project) params.set('project_id', route.project)
      const payload = await getJSON('/api/v1/events?' + params.toString(), { auth: true })
      if (!payload || (payload as JsonFail).unavailable === true) {
        return payload || { unavailable: true as const, status: 0 }
      }
      const body = payload as Record<string, unknown>
      const items = Array.isArray(body.items)
        ? body.items
        : (Array.isArray(body.events) ? body.events : [])
      return { ...body, items, events: items }
    },
    async projects() {
      return getJSON(TASKS_SOURCE)
    },
    async settings() {
      const payload = await getJSON('/api/runtime')
      if (!payload || (payload as JsonFail).unavailable === true) {
        return payload || { unavailable: true, status: 0 }
      }
      const p = payload as { auto_execution?: unknown }
      return { ...p, policy: p.auto_execution || null }
    },
    /**
     * 移除工程登记（DELETE /api/v1/projects/{id}）。
     *
     * 返回结构而不是抛异常：界面要按 code 分辨「现场还在」与「会话过期」，
     * 这两种都不能被渲染成同一句「删除了」。
     */
    /**
     * 登记节点（POST /api/v1/projects/{id}/nodes）。
     * body 走契约 Registration；argv 由 entry_path + profile_ref 拼出。
     */
    async registerNode(
      projectId: string,
      input: {
        id: string
        name?: string
        entry: string
        slots?: number
        args?: string[]
        auto_probe?: boolean
        heartbeat_interval_sec?: number
        default_model?: string
      },
    ): Promise<RemovalOutcome> {
      const pid = String(projectId || '').trim()
      const id = String(input?.id || '').trim()
      const entry = String(input?.entry || '').trim()
      if (!pid) return { ok: false, status: 0, code: 'invalid_project_id' }
      if (!id) return { ok: false, status: 0, code: 'invalid_node_id' }
      if (!entry) return { ok: false, status: 0, code: 'invalid_entry_path' }
      if (!doFetch) return { ok: false, status: 0, code: 'network_error' }
      const authed = await ensureSession()
      if (!authed) return { ok: false, status: 401, code: 'unauthenticated', unauthenticated: true }
      const csrf = readCookie(win, CSRF_COOKIE)
      if (!csrf) return { ok: false, status: 403, code: 'csrf_failed' }
      const slots = Number(input?.slots) > 0 ? Number(input.slots) : 1
      const args = Array.isArray(input?.args) ? input.args.filter((s) => String(s || '').trim()) : []
      const interval = Number(input?.heartbeat_interval_sec)
      const platform =
        typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform || '')
          ? 'darwin'
          : 'linux'
      const body = {
        project_id: pid,
        auto_probe: input?.auto_probe !== false,
        heartbeat_interval_sec: interval >= 5 ? Math.min(3600, Math.floor(interval)) : 60,
        default_model: String(input?.default_model || '').trim(),
        config: {
          node_id: id,
          name: String(input?.name || id).trim() || id,
          adapter_kind: 'local_cli',
          entry_path: entry,
          profile_ref: JSON.stringify(args),
          mode: 'managed_cli',
          platform,
          slot_limit: slots,
          accept_new: true,
        },
      }
      const idem =
        'node-reg-' + id + '-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8)
      let res: Response
      try {
        res = await doFetch('/api/v1/projects/' + encodeURIComponent(pid) + '/nodes', {
          method: 'POST',
          headers: {
            Accept: 'application/json',
            'Content-Type': 'application/json',
            'X-CSRF-Token': csrf,
            'Idempotency-Key': idem.slice(0, 128),
          },
          credentials: 'same-origin',
          body: JSON.stringify(body),
        })
      } catch {
        return { ok: false, status: 0, code: 'network_error' }
      }
      let payload: Record<string, unknown> = {}
      try {
        payload = (await res.json()) as Record<string, unknown>
      } catch {
        payload = {}
      }
      if (!res.ok) {
        const detail = (payload?.error || null) as { code?: string; message?: string } | null
        return {
          ok: false,
          status: res.status,
          code: detail?.code,
          message: detail?.message,
          unauthenticated: res.status === 401,
        }
      }
      return { ok: true, status: res.status }
    },

    /** 删除节点登记（DELETE /api/v1/nodes/{id}）。 */
    async removeNode(nodeId: string): Promise<RemovalOutcome> {
      const id = String(nodeId || '').trim()
      if (!id) return { ok: false, status: 0, code: 'invalid_node_id' }
      if (!doFetch) return { ok: false, status: 0, code: 'network_error' }
      const authed = await ensureSession()
      if (!authed) return { ok: false, status: 401, code: 'unauthenticated', unauthenticated: true }
      const csrf = readCookie(win, CSRF_COOKIE)
      if (!csrf) return { ok: false, status: 403, code: 'csrf_failed' }
      let res: Response
      try {
        res = await doFetch('/api/v1/nodes/' + encodeURIComponent(id), {
          method: 'DELETE',
          headers: { Accept: 'application/json', 'X-CSRF-Token': csrf },
          credentials: 'same-origin',
        })
      } catch {
        return { ok: false, status: 0, code: 'network_error' }
      }
      let payload: Record<string, unknown> = {}
      try {
        payload = (await res.json()) as Record<string, unknown>
      } catch {
        payload = {}
      }
      if (!res.ok) {
        const detail = (payload?.error || null) as { code?: string; message?: string } | null
        return {
          ok: false,
          status: res.status,
          code: detail?.code,
          message: detail?.message,
          unauthenticated: res.status === 401,
        }
      }
      return { ok: true, status: res.status }
    },

    /**
     * 同步自检节点 CLI 环境（POST /api/v1/nodes/{id}/checks）。
     *
     * 服务端会真的起一次 `<cli> --version` 并回写节点 meta；返回的 report
     * 直接用于提示文案，列表重载后卡片上的版本／四维状态也会同步更新。
     */
    async checkNode(nodeId: string): Promise<NodeCheckOutcome> {
      const id = String(nodeId || '').trim()
      if (!id) return { ok: false, status: 0, code: 'invalid_node_id' }
      if (!doFetch) return { ok: false, status: 0, code: 'network_error' }
      const authed = await ensureSession()
      if (!authed) return { ok: false, status: 401, code: 'unauthenticated', unauthenticated: true }
      const csrf = readCookie(win, CSRF_COOKIE)
      if (!csrf) return { ok: false, status: 403, code: 'csrf_failed' }
      let res: Response
      try {
        res = await doFetch('/api/v1/nodes/' + encodeURIComponent(id) + '/checks', {
          method: 'POST',
          headers: {
            Accept: 'application/json',
            'Content-Type': 'application/json',
            'X-CSRF-Token': csrf,
          },
          credentials: 'same-origin',
          body: '{}',
        })
      } catch {
        return { ok: false, status: 0, code: 'network_error' }
      }
      let payload: Record<string, unknown> = {}
      try {
        payload = (await res.json()) as Record<string, unknown>
      } catch {
        payload = {}
      }
      if (!res.ok) {
        const detail = (payload?.error || null) as { code?: string; message?: string } | null
        return {
          ok: false,
          status: res.status,
          code: detail?.code,
          message: detail?.message,
          unauthenticated: res.status === 401,
        }
      }
      return { ok: true, status: res.status, report: payload as NodeCheckReport }
    },

    async updateProject(
      projectId: string,
      patch: Record<string, unknown>,
    ): Promise<RemovalOutcome> {
      const id = String(projectId || '').trim()
      if (!id) return { ok: false, status: 0, code: 'invalid_project_id' }
      if (!doFetch) return { ok: false, status: 0, code: 'network_error' }
      const authed = await ensureSession()
      if (!authed) return { ok: false, status: 401, code: 'unauthenticated', unauthenticated: true }
      const csrf = readCookie(win, CSRF_COOKIE)
      if (!csrf) return { ok: false, status: 403, code: 'csrf_failed' }
      const url = '/api/v1/projects/' + encodeURIComponent(id)
      let res: Response
      try {
        res = await doFetch(url, {
          method: 'PATCH',
          headers: {
            Accept: 'application/json',
            'Content-Type': 'application/json',
            'X-CSRF-Token': csrf,
          },
          credentials: 'same-origin',
          body: JSON.stringify(patch),
        })
      } catch {
        return { ok: false, status: 0, code: 'network_error' }
      }
      let body: Record<string, unknown> = {}
      try {
        body = (await res.json()) as Record<string, unknown>
      } catch {
        body = {}
      }
      if (!res.ok) {
        const detail = (body?.error || null) as { code?: string; message?: string } | null
        return {
          ok: false,
          status: res.status,
          code: detail?.code,
          message: detail?.message,
          unauthenticated: res.status === 401,
        }
      }
      return { ok: true, status: res.status }
    },

    async removeProject(projectId: string, opts: { force?: boolean } = {}): Promise<RemovalOutcome> {
      const id = String(projectId || '').trim()
      if (!id) return { ok: false, status: 0, code: 'invalid_project_id' }
      if (!doFetch) return { ok: false, status: 0, code: 'network_error' }

      const authed = await ensureSession()
      if (!authed) return { ok: false, status: 401, code: 'unauthenticated', unauthenticated: true }
      const csrf = readCookie(win, CSRF_COOKIE)
      if (!csrf) return { ok: false, status: 403, code: 'csrf_failed' }

      const url = '/api/v1/projects/' + encodeURIComponent(id) + (opts.force ? '?force=1' : '')
      let res: Response
      try {
        res = await doFetch(url, {
          method: 'DELETE',
          headers: { Accept: 'application/json', 'X-CSRF-Token': csrf },
          credentials: 'same-origin',
        })
      } catch {
        return { ok: false, status: 0, code: 'network_error' }
      }

      let body: Record<string, unknown> = {}
      try {
        body = (await res.json()) as Record<string, unknown>
      } catch {
        body = {}
      }
      if (!res.ok) {
        const detail = (body?.error || null) as { code?: string; message?: string } | null
        return {
          ok: false,
          status: res.status,
          code: detail?.code,
          message: detail?.message,
          unauthenticated: res.status === 401,
        }
      }
      return { ok: true, status: res.status }
    },

    async capabilities(projectId: string) {
      const url = capabilitiesPath(projectId)
      if (!url) return null
      const payload = await getJSON(url, { auth: true })
      if (!payload || (payload as JsonFail).unavailable === true) return null
      const list = Array.isArray((payload as { capabilities?: unknown }).capabilities)
        ? (payload as { capabilities: Array<{ name?: string; supported?: boolean; reason?: string }> }).capabilities
        : null
      if (!list) return null
      const map: Record<string, { supported: boolean; reason: string }> = {}
      list.forEach((item) => {
        if (!item || typeof item.name !== 'string') return
        map[item.name] = { supported: item.supported !== false, reason: item.reason || '' }
      })
      return map
    },
  }
}

export { isControlResource }
