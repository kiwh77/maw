/**
 * Vue 3 Workbench 入口。
 *
 * 产物：IIFE `board-web.js`，全局名 `MawBoard`；加载后自动挂载 `#app`。
 * 保留拓扑 / 工坊 API，供视图组件调用。
 */

import { createApp } from 'vue'
// tokens 必须先于组件／视图 CSS：components 与 views 只消费 var(--token)，不自带定义。
import tokensCSS from '../../assets/styles/tokens.css?inline'
import componentsCSS from '../../assets/components/components.css?inline'
import shellCSS from '../../assets/shell/shell.css?inline'
import timelineCSS from '../../assets/views/timeline/timeline.css?inline'
import activityCSS from '../../assets/views/activity/activity.css?inline'
import sessionsCSS from '../../assets/views/sessions/sessions.css?inline'
import analyticsCSS from '../../assets/views/analytics/analytics.css?inline'
import projectsCSS from '../../assets/views/projects/projects.css?inline'
import settingsCSS from '../../assets/views/settings/settings.css?inline'
import executionCSS from '../../assets/views/execution/execution.css?inline'
import nodesCSS from '../../assets/views/nodes/nodes.css?inline'
import App from './App.vue'

export { Graph } from '@antv/x6'
export { renderTopology, disposeTopology } from './topology'
export type { TopoBand, TopologyHandle, TopologyOptions } from './topology'
export { layoutTopoBands } from './layout'
export { renderFactory, setFactoryVisible } from './factory/factory'
export * from '../../assets/shell/view-registry.mjs'
export * from '../../assets/shell/router.mjs'
export * from '../../assets/i18n/board-i18n.mjs'

export const BOARD_WEB_ENTRY = 'board-web.js'
export const MOUNT_ATTR = 'data-maw-board-web'

/** 生产页注入的 vnext 样式链：tokens → components → shell → 视图局部。 */
export const VNEXT_STYLES = [
  tokensCSS,
  componentsCSS,
  shellCSS,
  timelineCSS,
  activityCSS,
  sessionsCSS,
  analyticsCSS,
  projectsCSS,
  settingsCSS,
  executionCSS,
  nodesCSS,
].join('\n')

function injectStyles() {
  if (typeof document === 'undefined') return
  if (document.getElementById('maw-vnext-styles')) return
  const style = document.createElement('style')
  style.id = 'maw-vnext-styles'
  style.textContent = VNEXT_STYLES
  ;(document.head || document.documentElement).appendChild(style)
}

export interface BoardMountOptions {
  container?: HTMLElement | string
}

export function mountApp(options: BoardMountOptions = {}) {
  injectStyles()
  let el: HTMLElement | null = null
  if (typeof options.container === 'string') {
    el = document.querySelector(options.container)
  } else if (options.container) {
    el = options.container
  } else {
    el = document.getElementById('app')
  }
  if (!el) throw new Error('board-web: #app mount point missing')
  el.setAttribute(MOUNT_ATTR, BOARD_WEB_ENTRY)
  const app = createApp(App)
  app.mount(el)
  return {
    app,
    unmount() {
      app.unmount()
      el?.removeAttribute(MOUNT_ATTR)
    },
  }
}

import { createShell, mountShell as mountLegacyShell } from '../../assets/shell/board-shell.mjs'
export { createShell }

/**
 * 兼容双路径：
 * - 无特殊选项 → Vue 整页挂载 `#app`
 * - 带 fixtures/sources/document（浏览器单测）→ 旧 shell 挂载层
 */
export function mountShell(options: Record<string, unknown> = {}) {
  if (options.fixtures || options.sources || options.document || options.registry) {
    const styles = typeof options.styles === 'string' ? options.styles : VNEXT_STYLES
    return mountLegacyShell({ ...options, styles })
  }
  return mountApp({})
}

export function mountBoard(options: { container: HTMLElement | string }) {
  const target =
    typeof options.container === 'string'
      ? document.querySelector<HTMLElement>(options.container)
      : options.container
  if (!target) throw new Error(`board-web: container not found: ${options.container}`)
  target.setAttribute(MOUNT_ATTR, BOARD_WEB_ENTRY)
  return {
    container: target,
    unmount() {
      target.removeAttribute(MOUNT_ATTR)
    },
  }
}

function boot() {
  if (!document.getElementById('app')) return
  if ((window as unknown as { mawApp?: unknown }).mawApp) return
  try {
    ;(window as unknown as { mawApp?: unknown }).mawApp = mountApp({})
  } catch (error) {
    console.error('maw board vue mount failed', error)
  }
}

// 延迟到 IIFE 导出完成后再挂载，避免 boot 抛错打断 `MawBoard.*` 赋值。
if (typeof document !== 'undefined') {
  const schedule = () => {
    if (typeof queueMicrotask === 'function') queueMicrotask(boot)
    else setTimeout(boot, 0)
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', schedule)
  } else {
    schedule()
  }
}
