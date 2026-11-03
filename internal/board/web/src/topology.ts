/**
 * 波次依赖拓扑画布（X6）。
 *
 * 布局由 legacy `layoutTopoBands` 计算（波次列 = 完整依赖深度；纵轴并行轨道），本模块只负责渲染：
 * - 平滑曲线连接 + 中点圆点；
 * - 空白拖拽平移、Ctrl／⌘ + 滚轮缩放、适应画布／重布局；
 * - 悬停高亮相邻依赖，其余节点与边降透明度；
 * - 筛选外依赖以虚线 stub 表示；多工程 band 纵向堆叠（不显示工程名／波次标签）。
 * - 状态文案由调用方 `statusLabels` 注入（中文／英文 i18n）。
 *
 * 颜色全部来自 `board.css` 的语义 token，节点为 HTML 节点，主题切换即时生效。
 */

import { Graph, Shape } from '@antv/x6'

export interface TopoTask {
  id: string
  project_id?: string
  project_name?: string
  title?: string
  status?: string
  owner?: string
  priority?: number
  deps?: string[]
}

export interface TopoEdge {
  from: string
  to: string
  cross?: boolean
}

export interface TopoBand {
  pid: string
  name: string
  /** 由调用方格式化的 band 统计文案（任务数 / 边数）。 */
  countText?: string
  tasks: TopoTask[]
  byId: Map<string, TopoTask>
  positions: Map<string, { x: number; y: number; stub?: boolean }>
  edges: TopoEdge[]
  edgeCount: number
  width: number
  height: number
}

export interface TopologyStrings {
  fit: string
  relayout: string
  legend: string
  legendCross: string
  stubTitle: string
  stubHint: string
}

export interface TopologyOptions {
  container: HTMLElement
  bands: TopoBand[]
  selected?: { tid: string; pid: string } | null
  onOpen?: (tid: string, pid: string) => void
  strings?: Partial<TopologyStrings>
  /** 状态码 → 展示名（如 ready → 可领取）。 */
  statusLabels?: Record<string, string>
  /** 强制重建（重布局按钮）。 */
  force?: boolean
}

export interface TopologyHandle {
  fitView(): void
  relayout(): void
  destroy(): void
}

const DEFAULTS: TopologyStrings = {
  fit: '适应画布',
  relayout: '重布局',
  legend: '依赖方向：前置 → 后续',
  legendCross: '虚线：当前视图外的依赖',
  stubTitle: '筛选外 / 历史依赖',
  stubHint: '不在当前视图',
}

const BAND_GAP = 40

const CARD_SHAPE = 'maw-topo-card'

interface CardPayload {
  kind: 'card'
  id: string
  pid: string
  title: string
  /** 英文状态键，供 data-status / CSS。 */
  status: string
  /** 本地化展示名。 */
  statusLabel: string
  owner: string
  priority: string
  stub: boolean
  active: boolean
  stubTitle: string
  stubHint: string
}

function cardElement(payload: CardPayload): HTMLElement {
  const el = document.createElement('div')
  el.className = 'topo-x6-node' + (payload.stub ? ' stub' : ' clickable') + (payload.active ? ' active' : '')
  el.dataset.tid = payload.id
  el.dataset.pid = payload.pid
  if (!payload.stub) {
    el.dataset.status = payload.status
    el.tabIndex = 0
    el.setAttribute('role', 'button')
    el.setAttribute('aria-label', payload.id + ' ' + payload.title + '，' + payload.statusLabel)
  }
  el.innerHTML = payload.stub
    ? '<div class="node-head"><span class="id">' + esc(payload.id) + '</span></div>' +
      '<h4 class="task-title">' + esc(payload.stubTitle) + '</h4>' +
      '<div class="node-meta"><span>' + esc(payload.stubHint) + '</span></div>'
    : '<div class="node-head"><span class="id">' + esc(payload.id) + '</span>' +
      '<span class="status">' + esc(payload.statusLabel) + '</span></div>' +
      '<h4 class="task-title">' + esc(payload.title) + '</h4>' +
      '<div class="node-meta"><span class="owner">' + esc(payload.owner) + '</span>' +
      '<span class="priority">' + esc(payload.priority) + '</span></div>'
  return el
}

let shapesRegistered = false

function ensureShapes(): void {
  if (shapesRegistered) return
  shapesRegistered = true
  const render = (cell: unknown): HTMLElement => {
    const payload = (cell as { getData?: () => CardPayload }).getData?.()
    if (!payload || payload.kind !== 'card') return document.createElement('div')
    return cardElement(payload)
  }
  Shape.HTML.register({ shape: CARD_SHAPE, html: render })
}

function esc(value: unknown): string {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

function token(name: string, fallback: string): string {
  if (typeof getComputedStyle !== 'function') return fallback
  const raw = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return raw || fallback
}

function nodeKey(pid: string, id: string): string {
  return pid + '::' + id
}

interface Instance {
  graph: Graph
  sig: string
  options: TopologyOptions
  shell: HTMLElement
  canvas: HTMLElement
  observer?: MutationObserver
  /** 递增以作废进行中的 fitView rAF，避免晚到的 zoomToFit 冲掉用户缩放。 */
  fitGen: number
}

const instances = new WeakMap<HTMLElement, Instance>()

function signature(options: TopologyOptions): string {
  const parts: string[] = [
    document.documentElement.dataset.theme || '',
    options.selected ? options.selected.pid + '/' + options.selected.tid : '',
    'SL:' + JSON.stringify(options.statusLabels || {}),
  ]
  for (const band of options.bands) {
    parts.push('B:' + band.pid + '/' + band.name + '/' + band.width + 'x' + band.height)
    for (const task of band.tasks) {
      parts.push(
        [task.id, task.status || '', task.owner || '', task.priority ?? '', task.title || '', (task.deps || []).join('|')].join('~'),
      )
    }
    for (const edge of band.edges) parts.push('E:' + edge.from + '>' + edge.to + (edge.cross ? '~' : ''))
    for (const [id, pos] of band.positions) parts.push('P:' + id + '@' + pos.x + ',' + pos.y + (pos.stub ? '~' : ''))
  }
  return parts.join('\n')
}

function formatStatus(options: TopologyOptions, status: string): string {
  if (!status) return '—'
  const labels = options.statusLabels
  if (labels && labels[status]) return labels[status]
  return status
}

function buildShell(container: HTMLElement, strings: TopologyStrings): { shell: HTMLElement; canvas: HTMLElement; bar: HTMLElement } {
  container.textContent = ''
  const shell = document.createElement('div')
  shell.className = 'topo-x6-shell'
  const bar = document.createElement('div')
  bar.className = 'topo-x6-bar'
  const legend = document.createElement('div')
  legend.className = 'topo-x6-legend'
  legend.innerHTML =
    '<span><i></i>' + esc(strings.legend) + '</span><span><i class="cross"></i>' + esc(strings.legendCross) + '</span>'
  const canvas = document.createElement('div')
  canvas.className = 'topo-x6-canvas'
  shell.append(bar, legend, canvas)
  container.classList.add('topo-x6-host')
  container.append(shell)
  return { shell, canvas, bar }
}

function newGraph(canvas: HTMLElement): Graph {
  return new Graph({
    container: canvas,
    autoResize: true,
    background: { color: 'transparent' },
    grid: { visible: false },
    panning: { enabled: true, eventTypes: ['leftMouseDown', 'rightMouseDown'] },
    mousewheel: { enabled: true, modifiers: ['ctrl', 'meta'], factor: 1.1, minScale: 0.3, maxScale: 2.5 },
    interacting: { nodeMovable: false, edgeMovable: false, edgeLabelMovable: false, arrowheadMovable: false, vertexMovable: false },
    connecting: {
      connector: { name: 'smooth' },
    },
  })
}

function addNodes(instance: Instance, strings: TopologyStrings): void {
  ensureShapes()
  const { graph } = instance
  const selected = instance.options.selected
  let offsetY = 0
  for (const band of instance.options.bands) {
    for (const [id, pos] of band.positions) {
      const task = band.byId.get(id) || { id, project_id: band.pid }
      const stub = !!pos.stub || !band.byId.has(id)
      const active = !!selected && selected.tid === id && selected.pid === (task.project_id || band.pid)
      const status = task.status || ''
      graph.addNode({
        id: nodeKey(band.pid, id),
        shape: CARD_SHAPE,
        x: pos.x,
        y: offsetY + pos.y,
        width: 200,
        height: 98,
        zIndex: 2,
        data: {
          kind: 'card',
          id,
          title: task.title || '',
          status,
          statusLabel: formatStatus(instance.options, status),
          owner: task.owner || '',
          priority: task.priority === undefined ? 'P—' : 'P' + task.priority,
          stub,
          active,
          stubTitle: strings.stubTitle,
          stubHint: strings.stubHint,
          pid: band.pid,
        } satisfies CardPayload,
      })
    }

    offsetY += band.height + BAND_GAP
  }
}

function addEdges(instance: Instance): void {
  const { graph } = instance
  const stroke = token('--line-strong', '#4a5259')
  const accent = token('--accent', '#9cdbb0')
  for (const band of instance.options.bands) {
    for (const edge of band.edges) {
      graph.addEdge({
        source: { cell: nodeKey(band.pid, edge.from), anchor: { name: 'right' } },
        target: { cell: nodeKey(band.pid, edge.to), anchor: { name: 'left' } },
        connector: { name: 'smooth' },
        zIndex: 0,
        labels: [
          {
            markup: [{ tagName: 'circle', selector: 'dot' }],
            attrs: {
              dot: {
                r: 3.5,
                fill: stroke,
                strokeWidth: 0,
              },
            },
            position: { distance: 0.5 },
          },
        ],
        attrs: {
          line: {
            stroke: stroke,
            strokeWidth: 1.25,
            strokeDasharray: edge.cross ? '4 3' : null,
            opacity: edge.cross ? 0.75 : 1,
            targetMarker: { name: 'block', width: 8, height: 6, offset: 2 },
          },
        },
        data: { from: edge.from, to: edge.to, pid: band.pid, cross: !!edge.cross, highlight: accent },
      })
    }
  }
}

function elementOf(instance: Instance, cell: unknown): Element | null {
  // X6 3.x: cell.view 是字符串（视图名），DOM 需经 findViewByCell 或 data-cell-id 查询。
  const view = instance.graph.findViewByCell(cell as never) as unknown as { container?: Element } | null
  if (view && view.container) return view.container
  const id = (cell as { id?: string }).id
  if (!id) return null
  return instance.graph.container.querySelector('[data-cell-id="' + id.replace(/"/g, '\\"') + '"]')
}

function clearHighlight(instance: Instance): void {
  for (const cell of instance.graph.getCells()) {
    elementOf(instance, cell)?.classList.remove('topo-x6-hot', 'topo-x6-dim', 'topo-x6-edge-hot')
  }
}

function highlight(instance: Instance, cellId: string): void {
  const node = instance.graph.getCellById(cellId)
  if (!node || !node.isNode()) return
  clearHighlight(instance)
  const related = new Set<string>([cellId])
  const hotEdges = new Set(instance.graph.getConnectedEdges(node))
  for (const edge of hotEdges) {
    related.add(edge.getSourceCellId() ?? '')
    related.add(edge.getTargetCellId() ?? '')
  }
  for (const cell of instance.graph.getCells()) {
    const el = elementOf(instance, cell)
    if (!el) continue
    if (cell.isEdge()) {
      el.classList.add(hotEdges.has(cell as never) ? 'topo-x6-edge-hot' : 'topo-x6-dim')
      continue
    }
    el.classList.add(related.has(cell.id) ? 'topo-x6-hot' : 'topo-x6-dim')
  }
}

function wireInteractions(instance: Instance): void {
  const onOpen = instance.options.onOpen
  const cardOf = (target: EventTarget | null): HTMLElement | null => {
    const el = target as HTMLElement | null
    return el && typeof el.closest === 'function' ? el.closest('.topo-x6-node') : null
  }

  // HTML 节点（foreignObject）用 DOM 事件委托，避免 X6 画布事件在嵌套 DOM 上失效。
  instance.canvas.addEventListener('mouseover', (event) => {
    const card = cardOf(event.target)
    if (!card) return
    highlight(instance, nodeKey(card.dataset.pid || '', card.dataset.tid || ''))
  })
  instance.canvas.addEventListener('mouseleave', () => clearHighlight(instance))
  instance.canvas.addEventListener('click', (event) => {
    const card = cardOf(event.target)
    if (!card || !card.classList.contains('clickable') || !onOpen) {
      if (!card) clearHighlight(instance)
      return
    }
    onOpen(card.dataset.tid || '', card.dataset.pid || '')
  })

  // 键盘可达性：Enter／空格打开抽屉。
  instance.canvas.addEventListener('keydown', (event: KeyboardEvent) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    const card = cardOf(event.target)
    if (!card || !card.classList.contains('clickable') || !onOpen) return
    event.preventDefault()
    onOpen(card.dataset.tid || '', card.dataset.pid || '')
  })
}

const RUNNING_STATUSES = new Set(['claimed', 'in_progress', 'self_check'])
const READY_STATUSES = new Set(['ready', 'backlog'])

function pickFocusCell(instance: Instance): ReturnType<Graph['getCellById']> | null {
  const { graph, options } = instance
  const selected = options.selected
  if (selected) {
    const cell = graph.getCellById(nodeKey(selected.pid, selected.tid))
    if (cell) return cell
  }
  const pick = (want: Set<string>) => {
    for (const band of options.bands) {
      for (const task of band.tasks) {
        if (!want.has(task.status || '')) continue
        const cell = graph.getCellById(nodeKey(band.pid, task.id))
        if (cell) return cell
      }
    }
    return null
  }
  return pick(RUNNING_STATUSES) || pick(READY_STATUSES)
}

function fitView(instance: Instance): void {
  // 视图切换时容器可能仍是 display:none（0×0），等到有实际尺寸再定位。
  const gen = ++instance.fitGen
  let tries = 0
  const attempt = (): void => {
    if (instance.fitGen !== gen) return
    const { canvas, graph } = instance
    if (!canvas.isConnected) return
    if (canvas.clientWidth < 40 || canvas.clientHeight < 40) {
      if (tries++ < 40 && typeof requestAnimationFrame === 'function') requestAnimationFrame(attempt)
      return
    }
    // 与滚轮 minScale 对齐，避免大图缩成「毛线球」。
    graph.zoomToFit({ padding: 24, maxScale: 1, minScale: 0.3 })
    const focus = pickFocusCell(instance)
    if (focus) graph.centerCell(focus)
    else graph.centerContent()
  }
  attempt()
}

function handleFor(instance: Instance): TopologyHandle {
  return {
    fitView: () => fitView(instance),
    relayout: () => {
      const next = renderTopology({ ...instance.options, force: true })
      next.fitView()
    },
    destroy: () => destroyTopology(instance.options.container),
  }
}

function captureViewport(graph: Graph): { zoom: number; tx: number; ty: number } {
  const { tx, ty } = graph.translate()
  return { zoom: graph.zoom(), tx, ty }
}

function restoreViewport(graph: Graph, viewport: { zoom: number; tx: number; ty: number }): void {
  graph.zoomTo(viewport.zoom)
  graph.translate(viewport.tx, viewport.ty)
}

function destroyTopology(container: HTMLElement): void {
  const instance = instances.get(container)
  if (!instance) return
  instance.fitGen++
  instance.observer?.disconnect()
  instance.graph.dispose()
  instances.delete(container)
  container.textContent = ''
  container.classList.remove('topo-x6-host')
}

export function renderTopology(options: TopologyOptions): TopologyHandle {
  const strings: TopologyStrings = { ...DEFAULTS, ...(options.strings || {}) }
  const sig = signature(options)
  const existing = instances.get(options.container)
  if (existing && !options.force && existing.sig === sig) {
    existing.options = { ...options, strings }
    return handleFor(existing)
  }
  // 重建前记下缩放／平移：任务状态刷新、主题切换都不应把用户视图弹回默认。
  const viewport = existing ? captureViewport(existing.graph) : null
  if (existing) destroyTopology(options.container)

  const { shell, canvas, bar } = buildShell(options.container, strings)
  const instance: Instance = {
    graph: newGraph(canvas),
    sig,
    options: { ...options, strings },
    shell,
    canvas,
    fitGen: 0,
  }
  instances.set(options.container, instance)

  const handle = handleFor(instance)
  const fitButton = document.createElement('button')
  fitButton.type = 'button'
  fitButton.className = 'topo-x6-btn'
  fitButton.textContent = strings.fit
  fitButton.addEventListener('click', () => handle.fitView())
  const relayoutButton = document.createElement('button')
  relayoutButton.type = 'button'
  relayoutButton.className = 'topo-x6-btn'
  relayoutButton.textContent = strings.relayout
  relayoutButton.addEventListener('click', () => handle.relayout())
  bar.append(fitButton, relayoutButton)

  addNodes(instance, strings)
  addEdges(instance)
  wireInteractions(instance)
  if (viewport) restoreViewport(instance.graph, viewport)
  else fitView(instance)

  if (typeof MutationObserver === 'function') {
    const observer = new MutationObserver(() => {
      const current = instances.get(options.container)
      if (current) renderTopology({ ...current.options, force: true })
    })
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    instance.observer = observer
  }
  return handle
}

export function disposeTopology(container: HTMLElement): void {
  destroyTopology(container)
}
