const { test } = require('node:test');
const assert = require('node:assert/strict');

// router.mjs 是 ESM；node:test 的 CJS 入口用动态 import 取同一份实现。
function loadRouter() {
  return import('./router.mjs');
}

test('iteration 参数按「是否出现」解析：缺省为 null，空值为未归属', async () => {
  const { parseRoute, PARAM_ITERATION } = await loadRouter();
  assert.equal(PARAM_ITERATION, 'iteration');

  assert.equal(parseRoute('', '', null).iteration, null);
  assert.equal(parseRoute('?iteration=IT-001', '', null).iteration, 'IT-001');
  // `?iteration=` 是合法筛选值（只要未归属迭代的任务），不能读成「没筛」。
  assert.equal(parseRoute('?iteration=', '', null).iteration, '');
  assert.equal(parseRoute('?view=execution&iteration=IT-002', '', null).iteration, 'IT-002');
});

test('iteration 序列化保留空值，null/undefined 不写参数', async () => {
  const { serializeQuery, serializeRoute } = await loadRouter();
  assert.equal(serializeQuery({ iteration: null }), '');
  assert.equal(serializeQuery({}), '');
  assert.equal(serializeQuery({ iteration: 'IT-001' }), '?iteration=IT-001');
  assert.equal(serializeQuery({ iteration: '' }), '?iteration=');
  assert.equal(serializeRoute({ iteration: '' }, '/'), '/?iteration=');
});

test('iteration 往返不丢：解析后再序列化得到同一 URL', async () => {
  const { parseRoute, serializeQuery } = await loadRouter();
  for (const search of ['', '?iteration=', '?iteration=IT-001', '?iteration=IT-002&agent=a']) {
    const route = parseRoute(search, '', null);
    const round = serializeQuery(route);
    const again = parseRoute(round, '', null);
    assert.equal(again.iteration, route.iteration, `${search} → ${round}`);
  }
});

test('sameRoute 区分「不筛」与「筛未归属」', async () => {
  const { sameRoute } = await loadRouter();
  assert.equal(sameRoute({ iteration: null }, {}), true);
  assert.equal(sameRoute({ iteration: null }, { iteration: '' }), false);
  assert.equal(sameRoute({ iteration: '' }, { iteration: '' }), true);
  assert.equal(sameRoute({ iteration: 'IT-001' }, { iteration: 'IT-002' }), false);
});

test('withRoute 默认补 null，避免 undefined 混进序列化', async () => {
  const { withRoute } = await loadRouter();
  assert.equal(withRoute({}, {}).iteration, null);
  assert.equal(withRoute({ iteration: '' }, {}).iteration, '');
  assert.equal(withRoute({ iteration: 'IT-001' }, { iteration: null }).iteration, null);
});
