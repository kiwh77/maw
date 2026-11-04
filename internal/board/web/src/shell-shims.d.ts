/**
 * Workbench 挂载层的模块声明。
 *
 * 源码是 assets/ 下的纯 ESM（可被 node --test 直接加载），
 * 由 Vite 打进 IIFE bundle；tsc 只做打包期最小类型校对，
 * 因此这里声明导出面而不是复制实现类型。
 */

declare module '*.css?inline' {
  const css: string
  export default css
}

declare module '*/assets/shell/board-shell.mjs' {
  export function createShell(options?: Record<string, unknown>): any
  export function mountShell(options?: Record<string, unknown>): any
  export const DATA_KINDS: readonly string[]
}

declare module '*/assets/shell/view-registry.mjs' {
  export const DEFAULT_VIEW: string
  export const DEFAULT_VIEW_NO_PROJECT: string
  export const VIEW_DEFS: ReadonlyArray<Record<string, unknown>>
  export const VIEW_GROUPS: ReadonlyArray<Record<string, unknown>>
  export const SIDEBAR_HIDDEN_GROUPS: readonly string[]
  export const SIDEBAR_PINNED_IDS: readonly string[]
  export const LEGACY_CONTAINERS: readonly string[]
  export const LEGACY_TOGGLES: Readonly<Record<string, string>>
  export function createRegistry(defs?: ReadonlyArray<Record<string, unknown>>): any
  export function resolveView(registry: any, id: string): any
  export function isLegacyView(registry: any, id: string): boolean
  export function navItems(registry: any, lang: string): ReadonlyArray<Record<string, unknown>>
  export function groupLabelKey(group: string): string
  export function navLabelKey(def: Record<string, unknown>): string
}

declare module '*/assets/shell/router.mjs' {
  export const PARAM_VIEW: string
  export const PARAM_PROJECT: string
  export const PARAM_AGENT: string
  export const PARAM_STATUS: string
  export const PARAM_SEARCH: string
  export const PARAM_DETAIL: string
  export const PARAM_ITERATION: string
  export function isLegacyProjectAnchor(hash?: string): boolean
  export function projectFromHash(hash?: string): string
  export function parseRoute(
    search?: string,
    hash?: string,
    isKnownView?: ((id: string) => boolean) | null,
  ): any
  export function serializeQuery(route?: Record<string, unknown>): string
  export function serializeHash(route?: Record<string, unknown>): string
  export function serializeRoute(route?: Record<string, unknown>, pathname?: string): string
  export function sameRoute(a?: Record<string, unknown>, b?: Record<string, unknown>): boolean
  export function writeRoute(
    route: Record<string, unknown>,
    options?: {
      history?: History | null
      location?: Location | null
      replace?: boolean
    },
  ): string
  export function withRoute(route?: Record<string, unknown>, patch?: Record<string, unknown>): any
}

declare module '*/assets/i18n/board-i18n.mjs' {
  export const LANGS: readonly string[]
  export const DEFAULT_LANG: string
  export const LANG_STORAGE_KEY: string
  export function normalizeLang(value?: string): string
  export function readLangPreference(options?: {
    search?: string
    storage?: Storage | null
    navigatorLanguage?: string
  }): string
  export function persistLang(lang: string, storage?: Storage | null): string
  export function translate(key: string, lang?: string, params?: Record<string, unknown>): string
  export function createTranslator(lang?: string): (key: string, params?: Record<string, unknown>) => string
  export function documentLang(lang: string): string
  export function applyDocumentLang(documentRef: Document | null, lang: string): string
}

declare module '*/assets/shell/view-context.mjs' {
  export const LOADING_STATE: Readonly<Record<string, unknown>>
  export const NO_PROJECT_STATE: Readonly<Record<string, unknown>>
  export const DATA_KINDS: readonly string[]
  export function needsProject(kind: string): boolean
  export function stateForKind(kind: string, payload: unknown, opts?: Record<string, unknown>): any
  export function findDetail(kind: string, payload: unknown, id: string): any
  export function errorState(reason?: string): any
}

declare module '*/assets/views/*/*-view.mjs' {
  export function renderNodeList(state: unknown, ctx: unknown): string
  export function renderNodeDetail(item: unknown, ctx: unknown): string
  export function renderTaskList(state: unknown, ctx: unknown): string
  export function renderRunList(state: unknown, ctx: unknown): string
  export function renderRunDetail(item: unknown, ctx: unknown): string
  export function renderDeliveryList(state: unknown, ctx: unknown): string
  export function renderDeliveryDetail(item: unknown, ctx: unknown): string
  export function renderTimeline(state: unknown, ctx: unknown): string
  export function renderActivity(state: unknown, ctx: unknown): string
  export function renderSessions(state: unknown, ctx: unknown): string
  export function renderAnalytics(state: unknown, ctx: unknown): string
  export function renderProjectList(state: unknown, ctx: unknown): string
  export function renderSettings(state: unknown, ctx: unknown): string
  export function renderExecution(state: unknown, ctx: unknown): string
}
