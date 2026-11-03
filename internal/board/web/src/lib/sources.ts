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

const PROJECT_RESOURCES = {
  timeline: 'timeline',
  activity: 'activity',
  sessions: 'sessions',
  analytics: 'analytics',
} as const

/** 控制类资源：服务端只有按工程的路由，没有全局 /api/v1/nodes 等。 */
const CONTROL_RESOURCES = ['nodes', 'runs', 'deliveries'] as const
type ControlResource = (typeof CONTROL_RESOURCES)[number]

export function projectResourcePath(kind: string, projectId: string) {
  const resource = PROJECT_RESOURCES[kind as keyof typeof PROJECT_RESOURCES]
  if (!resource) return ''
  const id = String(projectId || '').trim()
  if (!id) return ''
  return `/api/v1/projects/${encodeURIComponent(id)}/${resource}`
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

type RouteLike = { project?: string; agent?: string; status?: string }

type JsonFail = { unavailable: true; status: number; unauthenticated?: boolean }

/** 写操作结果：成功也只认 HTTP 2xx + ok:true，不做乐观更新。 */
export type RemovalOutcome = {
  ok: boolean
  status: number
  code?: string
  message?: string
  unauthenticated?: boolean
}

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
  function withNamedList(kind: ControlResource, payload: Record<string, unknown> | JsonFail) {
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
      const url = projectResourcePath(kind, route?.project || '')
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

  return {
    async tasks(route: RouteLike) {
      const params = new URLSearchParams()
      if (route.project) params.set('project', route.project)
      if (route.agent) params.set('agent', route.agent)
      if (route.status) params.set('status', route.status)
      const query = params.toString()
      return getJSON(TASKS_SOURCE + (query ? `?${query}` : ''))
    },
    nodes: controlSource('nodes'),
    runs: controlSource('runs'),
    deliveries: controlSource('deliveries'),
    timeline: projectSource('timeline'),
    activity: projectSource('activity'),
    sessions: projectSource('sessions'),
    analytics: projectSource('analytics'),
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
