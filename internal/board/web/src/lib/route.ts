/**
 * 路由状态：与 assets/shell/router.mjs 同构，供 Vue 壳读写 URL。
 */
export {
  PARAM_VIEW,
  PARAM_PROJECT,
  PARAM_AGENT,
  PARAM_STATUS,
  PARAM_SEARCH,
  PARAM_DETAIL,
  PARAM_ITERATION,
  parseRoute,
  serializeQuery,
  serializeHash,
  serializeRoute,
  sameRoute,
  writeRoute,
  withRoute,
  projectFromHash,
} from '../../../assets/shell/router.mjs'

export {
  DEFAULT_VIEW,
  DEFAULT_VIEW_NO_PROJECT,
  VIEW_DEFS,
  VIEW_GROUPS,
  SIDEBAR_PINNED_IDS,
  createRegistry,
  resolveView,
  groupLabelKey,
  navLabelKey,
  LEGACY_TOGGLES,
} from '../../../assets/shell/view-registry.mjs'
