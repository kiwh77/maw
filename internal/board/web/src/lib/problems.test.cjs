// BUX-FE-002：问题审查视图的纯数据层契约 + 视图接线。
// 纯 ESM 模块，node --test 可直接跑；不依赖浏览器或真实中枢。
const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const { pathToFileURL } = require('node:url')

const moduleURL = pathToFileURL(path.join(__dirname, 'problems.mjs')).href
const src = (rel) => fs.readFileSync(path.join(__dirname, '..', rel), 'utf8')

function fixture() {
  return [
    { project_id: 'p1', project_name: 'goal-agents', task_id: 'T-1', kind: 'blocked', severity: 'high', message: 'needs_human', ts: '2026-11-04T01:00:00Z' },
    { project_id: 'p1', project_name: 'goal-agents', task_id: 'T-2', kind: 'conflict', severity: 'high', message: 'out of scope', ts: '2026-11-04T02:00:00Z' },
    { project_id: 'p1', project_name: 'goal-agents', task_id: 'T-3', kind: 'retry', severity: 'medium', message: 'attempts=2', ts: '2026-11-04T03:00:00Z' },
    { project_id: 'p1', project_name: 'goal-agents', task_id: 'T-4', kind: 'question', severity: 'medium', message: 'waiting on answer', ts: '2026-11-04T04:00:00Z' },
    { project_id: 'p1', project_name: 'goal-agents', task_id: 'T-5', kind: 'stalled', severity: 'medium', message: 'no heartbeat', ts: '2026-11-04T05:00:00Z' },
    { project_id: 'p1', project_name: 'goal-agents', task_id: 'T-6', kind: 'dep-wait', severity: 'low', message: 'depends on T-1', ts: '2026-11-04T06:00:00Z' },
    { project_id: 'p2', project_name: 'other', task_id: 'T-9', kind: 'blocked', severity: 'high', message: 'other project', ts: '2026-11-04T07:00:00Z' },
  ]
}

async function load() {
  return import(moduleURL)
}

test('按 severity → kind 分组，六类问题都有归属且顺序稳定', async () => {
  const { buildProblemGroups, SEVERITY_ORDER, KIND_ORDER } = await load()
  const out = buildProblemGroups(fixture(), { lang: 'zh' })
  assert.equal(out.total, 7)
  assert.equal(out.allCount, 7)
  assert.deepEqual(out.groups.map((g) => g.severity), ['high', 'medium', 'low'])
  assert.deepEqual(out.counts, { high: 3, medium: 3, low: 1, other: 0 })

  const kinds = out.groups.flatMap((g) => g.kinds.map((k) => k.kind))
  for (const kind of KIND_ORDER.filter((k) => k !== 'blocker')) {
    assert.ok(kinds.includes(kind), `缺少分组：${kind}`)
  }
  assert.equal(out.groups[0].label, '高优先级')
  const blocked = out.groups[0].kinds.find((k) => k.kind === 'blocked')
  assert.equal(blocked.label, '阻塞')
  assert.equal(blocked.items.length, 2)
  assert.ok(SEVERITY_ORDER.indexOf('high') < SEVERITY_ORDER.indexOf('low'))
})

test('工程筛选联动：只留该工程问题，空态文案点明筛选范围', async () => {
  const { buildProblemGroups, emptyMessage } = await load()
  const byID = buildProblemGroups(fixture(), { project: 'p1' })
  assert.equal(byID.total, 6)
  assert.equal(byID.allCount, 7)
  assert.equal(byID.counts.high, 2, 'p2 的 high 问题应被筛掉')
  assert.ok(byID.groups.every((g) => g.kinds.every((k) => k.items.every((i) => i.project_id === 'p1'))))

  const byName = buildProblemGroups(fixture(), { project: 'other' })
  assert.equal(byName.total, 1)
  assert.equal(byName.groups[0].kinds[0].items[0].task_id, 'T-9')

  const none = buildProblemGroups(fixture(), { project: 'p-missing' })
  assert.equal(none.total, 0)
  assert.deepEqual(none.groups, [])
  assert.match(emptyMessage('zh', 'p-missing'), /当前筛选范围内/)
  assert.match(emptyMessage('en', 'p-missing'), /p-missing/)
  assert.match(emptyMessage('zh', ''), /当前筛选范围内/)
})

test('没有 problems 数据时是空成功而不是报错，且空态可读', async () => {
  const { buildProblemGroups, normalizeProblems, emptyMessage } = await load()
  for (const raw of [undefined, null, {}, 'oops', []]) {
    const out = buildProblemGroups(raw)
    assert.equal(out.total, 0)
    assert.deepEqual(out.groups, [])
  }
  assert.deepEqual(normalizeProblems([null, 1, { message: 'orphan signal' }]).length, 1)
  assert.ok(emptyMessage('zh').length > 8)
  assert.ok(emptyMessage('en').length > 8)
})

test('未知 severity / kind 不丢题，落到未分级并保留原文', async () => {
  const { buildProblemGroups, kindLabel } = await load()
  const out = buildProblemGroups([
    { project_id: 'p1', task_id: 'T-x', kind: 'future-kind', severity: 'urgent', message: 'unknown signal' },
  ])
  assert.equal(out.total, 1)
  assert.equal(out.groups[0].severity, 'other')
  assert.equal(out.groups[0].label, '未分级')
  assert.equal(out.groups[0].kinds[0].kind, 'future-kind')
  assert.equal(kindLabel('future-kind', 'en'), 'future-kind')
})

test('行点击目标来自 task_id/project_id；无任务的问题不可点', async () => {
  const { drawerTarget, buildProblemGroups } = await load()
  assert.deepEqual(drawerTarget({ task_id: 'T-1', project_id: 'p1' }), { tid: 'T-1', pid: 'p1' })
  assert.equal(drawerTarget({ message: 'agent offline' }), null)
  assert.equal(drawerTarget(null), null)
  const out = buildProblemGroups([{ task_id: 'T-1', project_id: 'p1', kind: 'blocked', severity: 'high' }])
  assert.ok(drawerTarget(out.groups[0].kinds[0].items[0]))
})

test('Vue 壳接线：problems 路由走新视图、抽屉动作与内联样式都在', async () => {
  const app = src('App.vue')
  const view = src('views/ProblemsView.vue')
  const main = src('main.ts')
  assert.match(app, /import ProblemsView from '.\/views\/ProblemsView.vue'/)
  assert.match(app, /<ProblemsView v-else-if="board\.state\.route\.view === 'problems'" \/>/)
  assert.match(view, /buildProblemGroups\(board\.state\.snapshot\?\.problems/)
  assert.match(view, /board\.openTaskDrawer\(target\.tid, target\.pid\)/)
  assert.match(view, /data-problem-task/)
  assert.match(view, /id="problems-empty"/)
  assert.match(main, /problemsCSS/)
  // 旧挂载层映射保留，既有 page.test.cjs 断言不受影响。
  const host = src('components/VnextHost.vue')
  assert.match(host, /problems: renderProblems as never/)
})
