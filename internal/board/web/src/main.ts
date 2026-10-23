/**
 * Board 拓扑前端入口。
 *
 * - 波次依赖拓扑画布由 `topology.ts` 用 X6 渲染，布局来自 legacy `layoutTopoBands`；
 * - 构建产物为 IIFE bundle（全局名 `MawBoard`），由 `page.html` 以
 *   `<script id="board-web" src="assets/board-web.js" defer>` 加载；
 * - 未加载 bundle（或 X6 不可用）时页面回退到 legacy SVG 拓扑；
 * - VN-704 起 bundle 同时导出 Workbench 挂载层（注册表、路由、i18n、shell），
 *   并把共享组件样式以内联字符串注入页面，供 nodes/tasks/runs/deliveries 视图使用。
 *
 * 视觉沿用 `internal/board/page.html` 的 `:root` token，不在此处写死颜色。
 */

import componentsCSS from '../../assets/components/components.css?inline'
import shellCSS from '../../assets/shell/shell.css?inline'
import { createShell, mountShell as mountWorkbenchShell } from '../../assets/shell/board-shell.mjs'

export { Graph } from '@antv/x6'
export { renderTopology, disposeTopology } from './topology'
export type { TopoBand, TopologyHandle, TopologyOptions } from './topology'

/** 产物文件名，与 vite.config.ts 的 lib.fileName 保持一致。 */
export const BOARD_WEB_ENTRY = 'board-web.js'

/** 挂载容器上标记的 data 属性，便于冒烟与调试。 */
export const MOUNT_ATTR = 'data-maw-board-web'

export interface BoardMountOptions {
  /** 容器元素或选择器。 */
  container: HTMLElement | string
}

export interface BoardHandle {
  /** 已挂载的容器。 */
  readonly container: HTMLElement
  /** 卸载容器标记。 */
  unmount(): void
}

function resolveContainer(target: HTMLElement | string): HTMLElement {
  if (typeof target !== 'string') {
    return target
  }
  const found = document.querySelector<HTMLElement>(target)
  if (!found) {
    throw new Error(`board-web: container not found: ${target}`)
  }
  return found
}

/** 标记挂载容器；拓扑实例请用 `renderTopology`。 */
export function mountBoard(options: BoardMountOptions): BoardHandle {
  const container = resolveContainer(options.container)
  container.setAttribute(MOUNT_ATTR, BOARD_WEB_ENTRY)
  return {
    container,
    unmount() {
      container.removeAttribute(MOUNT_ATTR)
    },
  }
}

/**
 * 挂载统一布局的 Workbench：注册、路由、语言与四个 vnext 视图。
 * 共享组件样式与 shell 样式在此合并注入，页面自身不重复引用。
 */
export function mountShell(options: Record<string, unknown> = {}): unknown {
  const styles = typeof options.styles === 'string' ? options.styles : `${componentsCSS}\n${shellCSS}`
  return mountWorkbenchShell({ ...options, styles })
}

export { createShell }
export * from '../../assets/shell/view-registry.mjs'
export * from '../../assets/shell/router.mjs'
export * from '../../assets/i18n/board-i18n.mjs'

export { renderFactory, setFactoryVisible } from './factory/factory'
