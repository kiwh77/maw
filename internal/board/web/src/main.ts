/**
 * Board 拓扑前端入口。
 *
 * 现状（VN-706）：只提供挂载 API 占位与 X6 运行时导出。
 * - 拓扑渲染与波次交互在 VN-708 实现；
 * - embed.FS 与 `/assets/*` 静态挂载在 VN-707 接入，本文件不参与 Go 侧装配。
 *
 * 视觉沿用 `internal/board/page.html` 的 `:root` token，不在此处写死颜色。
 * 构建产物为 IIFE bundle，全局名 `MawBoard`，可直接由 `<script>` 加载。
 */

export { Graph } from '@antv/x6'

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
  /** 卸载占位标记；拓扑实例清理随 VN-708 补齐。 */
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

/**
 * 挂载占位：只标记容器，不创建画布。
 * VN-708 在此返回真实 X6 Graph 句柄，保持签名不变。
 */
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
