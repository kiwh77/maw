import assert from 'node:assert/strict';
import test from 'node:test';

import {
  CONTRACT,
  PROTOCOL,
  classifyError,
  envelope,
  hasUsage,
  normalizeUsage,
  terminalFromStopReason,
} from '../src/protocol.mjs';

test('协议标识与契约版本冻结', () => {
  assert.equal(CONTRACT, 'maw.pi.execution/v1');
  assert.deepEqual(PROTOCOL, { format: 'pi-stream-json', version: '1' });
});

test('用量：完整分量归一为 observed，total 重算、缓存独立', () => {
  const usage = normalizeUsage(
    { input: 120, output: 30, cacheRead: 40, cacheWrite: 5, totalTokens: 999 },
    { callId: 'pc_1', occurredAt: '2026-11-18T02:41:09Z' },
  );
  assert.equal(usage.input_tokens, 120);
  assert.equal(usage.output_tokens, 30);
  assert.equal(usage.cache_read_tokens, 40);
  assert.equal(usage.cache_write_tokens, 5);
  assert.equal(usage.total_tokens, 150, 'total 必须是 input+output，不含缓存');
  assert.equal(usage.quality, 'observed');
  assert.equal(usage.semantic, 'delta');
  assert.equal(usage.scope, 'call');
  assert.match(usage.basis, /不一致/);
  assert.equal(usage.source_total_tokens, 999);
  assert.equal(usage.call_id, 'pc_1');
});

test('用量：缺失即 null，绝不填 0', () => {
  const none = normalizeUsage(undefined);
  assert.equal(none.input_tokens, null);
  assert.equal(none.output_tokens, null);
  assert.equal(none.cache_read_tokens, null);
  assert.equal(none.cache_write_tokens, null);
  assert.equal(none.total_tokens, null);
  assert.equal(none.quality, 'unknown');
  assert.equal(hasUsage(none), false);

  const partial = normalizeUsage({ input: 10 });
  assert.equal(partial.input_tokens, 10);
  assert.equal(partial.output_tokens, null);
  assert.equal(partial.total_tokens, null, '缺一半就不算 total');
  assert.equal(partial.quality, 'unknown');
  assert.match(partial.basis, /缺失/);

  const negative = normalizeUsage({ input: -3, output: 1.9 });
  assert.equal(negative.input_tokens, null, '负数不是用量');
  assert.equal(negative.output_tokens, 1, '小数按整数截断');
});

test('用量：无缓存分解时降级为 estimated 并写明依据', () => {
  const usage = normalizeUsage({ input: 10, output: 2 });
  assert.equal(usage.quality, 'estimated');
  assert.equal(usage.total_tokens, 12);
  assert.match(usage.basis, /缓存/);
});

test('错误分类只落到封闭集合', () => {
  assert.equal(classifyError({ status: 401, message: 'Unauthorized' }), 'auth');
  assert.equal(classifyError(new Error('429 Too Many Requests')), 'rate_limit');
  assert.equal(classifyError(new Error('insufficient quota')), 'quota');
  assert.equal(classifyError(Object.assign(new Error('boom'), { code: 'ETIMEDOUT' })), 'timeout');
  assert.equal(classifyError(new Error('spawn failed: ENOENT')), 'env');
  assert.equal(classifyError({ code: 'tool_denied', message: 'out of scope' }), 'tool_denied');
  assert.equal(classifyError(new Error('something odd')), 'unknown');
});

test('终态映射：只由 stopReason 与中止标志决定', () => {
  assert.deepEqual(terminalFromStopReason('stop'), { outcome: 'completed', reason: 'result' });
  assert.deepEqual(terminalFromStopReason('length'), { outcome: 'completed', reason: 'truncated' });
  assert.deepEqual(terminalFromStopReason('aborted'), { outcome: 'cancelled', reason: 'abort_requested' });
  assert.equal(terminalFromStopReason('stop', { aborted: true }).outcome, 'cancelled');
  assert.equal(terminalFromStopReason('error').outcome, 'failed');
  assert.equal(terminalFromStopReason('toolUse').outcome, null, '还在跑，不是终态');
});

test('事件信封要求 run_id 与 generation', () => {
  assert.throws(() => envelope({ type: 'usage', runId: '', generation: 1 }), /run_id/);
  assert.throws(() => envelope({ type: 'usage', runId: 'run_1', generation: 0 }), /generation/);
  const ok = envelope({ type: 'usage', runId: 'run_1', generation: 2, clock: () => new Date('2026-11-18T00:00:00Z') });
  assert.equal(ok.contract, CONTRACT);
  assert.equal(ok.run_id, 'run_1');
  assert.equal(ok.generation, 2);
  assert.match(ok.event_id, /^pe_/);
});
