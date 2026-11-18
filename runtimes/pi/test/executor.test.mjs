import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { createAssistantMessageEventStream } from '@earendil-works/pi-ai';

import { buildTools, createPiExecutor } from '../src/executor.mjs';

const MODEL = {
  id: 'mock',
  provider: 'mock',
  api: 'mock',
  input: ['text'],
  output: ['text'],
  contextWindow: 4096,
  maxTokens: 512,
  reasoning: false,
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
};

const USAGE = {
  input: 120,
  output: 30,
  cacheRead: 40,
  cacheWrite: 0,
  totalTokens: 150,
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
};

function assistantMessage(text, { stopReason = 'stop', usage = USAGE } = {}) {
  return {
    role: 'assistant',
    content: [{ type: 'text', text }],
    api: 'mock',
    provider: 'mock',
    model: 'mock',
    usage,
    stopReason,
    timestamp: Date.now(),
  };
}

function streamOf(message, { delayMs = 0 } = {}) {
  return () => {
    const stream = createAssistantMessageEventStream();
    const emit = () => {
      stream.push({ type: 'start', partial: message });
      stream.push({ type: 'text_start', contentIndex: 0, partial: message });
      const text = message.content.map((part) => part.text ?? '').join('');
      stream.push({ type: 'text_delta', contentIndex: 0, delta: text, partial: message });
      stream.push({ type: 'text_end', contentIndex: 0, content: text, partial: message });
      stream.push({ type: 'done', reason: message.stopReason, message });
    };
    if (delayMs > 0) setTimeout(emit, delayMs);
    else emit();
    return stream;
  };
}

async function workspace(t) {
  const cwd = await fs.mkdtemp(path.join(os.tmpdir(), 'maw-pi-'));
  t.after(async () => {
    await fs.rm(cwd, { recursive: true, force: true });
  });
  return cwd;
}

test('三模式共用同一执行器：成功路径给出 completed 与逐次用量', async (t) => {
  const cwd = await workspace(t);
  const executor = createPiExecutor({
    mode: 'worker',
    cwd,
    runId: 'run_1',
    generation: 1,
    writeScope: ['docs/**'],
    model: MODEL,
    streamFn: streamOf(assistantMessage('done: docs/a.md')),
  });
  t.after(() => executor.dispose());

  const result = await executor.run('implement a.md');

  assert.equal(result.outcome, 'completed');
  assert.equal(result.reason, 'result');
  assert.equal(result.usage.length, 1);
  assert.equal(result.usage[0].quality, 'observed');
  assert.equal(result.usage[0].input_tokens, 120);
  assert.equal(result.usage[0].total_tokens, 150);
  assert.equal(result.usage[0].cache_read_tokens, 40);

  const types = result.events.map((event) => event.type);
  assert.deepEqual(types.slice(0, 2), ['version_report', 'session_started']);
  assert.ok(types.includes('usage'));
  assert.ok(types.includes('result'));
  assert.equal(types.at(-1), 'session_ended');

  const resultEvent = result.events.find((event) => event.type === 'result');
  assert.equal(resultEvent.payload.ok, true);
  assert.match(resultEvent.payload.evidence.join(','), /stop_reason:stop/);

  // 同一执行器只服务一种模式；worker 有写工具、没有 bash（未放开）。
  assert.deepEqual(executor.tools, ['read', 'write', 'edit']);
});

test('失败：模型报 401 → failed(auth)，结果不通过且带证据', async (t) => {
  const cwd = await workspace(t);
  const executor = createPiExecutor({
    mode: 'worker',
    cwd,
    runId: 'run_2',
    generation: 2,
    writeScope: ['docs/**'],
    model: MODEL,
    streamFn: () => {
      throw Object.assign(new Error('401 Unauthorized: invalid api key'), { status: 401 });
    },
  });
  t.after(() => executor.dispose());

  const result = await executor.run('go');
  assert.equal(result.outcome, 'failed');
  assert.equal(result.reason, 'auth');

  const errorEvent = result.events.find((event) => event.type === 'error');
  assert.equal(errorEvent.payload.category, 'auth');
  assert.equal(errorEvent.payload.retryable, false);
  // 错误原文可读，但凭据形态必须被脱敏（契约 §6）。
  assert.doesNotMatch(JSON.stringify(result.events), /sk-[A-Za-z0-9]{8,}|ghp_[A-Za-z0-9]{16,}/);

  const resultEvent = result.events.find((event) => event.type === 'result');
  assert.equal(resultEvent.payload.ok, false);
});

test('取消：请求中止后终态是 cancelled，不冒充完成', async (t) => {
  const cwd = await workspace(t);
  const abortedMessage = assistantMessage('partial', { stopReason: 'aborted' });
  const executor = createPiExecutor({
    mode: 'worker',
    cwd,
    runId: 'run_3',
    generation: 1,
    writeScope: ['docs/**'],
    model: MODEL,
    streamFn: streamOf(abortedMessage, { delayMs: 20 }),
  });
  t.after(() => executor.dispose());

  const pending = executor.run('long job');
  setTimeout(() => executor.abort(), 5);
  const result = await pending;

  assert.equal(result.outcome, 'cancelled');
  assert.equal(result.reason, 'abort_requested');
  const resultEvent = result.events.find((event) => event.type === 'result');
  assert.equal(resultEvent.payload.ok, false);
  assert.match(resultEvent.payload.evidence.join(','), /abort_requested:true/);
});

test('无密钥观测：事件与用量里不出现凭据', async (t) => {
  const cwd = await workspace(t);
  const previousKey = process.env.OPENAI_API_KEY;
  process.env.OPENAI_API_KEY = 'sk-this-must-not-leak-123456';
  t.after(() => {
    if (previousKey === undefined) delete process.env.OPENAI_API_KEY;
    else process.env.OPENAI_API_KEY = previousKey;
  });

  const executor = createPiExecutor({
    mode: 'diagnose',
    cwd,
    runId: 'run_4',
    generation: 1,
    model: MODEL,
    streamFn: streamOf(assistantMessage('decision: notify_only')),
  });
  t.after(() => executor.dispose());

  const result = await executor.run('diagnose');
  assert.equal(result.outcome, 'completed');
  const wire = JSON.stringify(result.events);
  assert.doesNotMatch(wire, /sk-this-must-not-leak/);
  assert.doesNotMatch(wire, /OPENAI_API_KEY/);
});

test('lead 只能读：工具集合没有写工具，写请求被授权边界拒绝', async (t) => {
  const cwd = await workspace(t);
  await fs.mkdir(path.join(cwd, 'docs'), { recursive: true });
  await fs.writeFile(path.join(cwd, 'docs', 'a.md'), '# a\n', 'utf8');

  const lead = createPiExecutor({
    mode: 'lead',
    cwd,
    runId: 'run_5',
    generation: 1,
    writeScope: ['docs/**'],
    model: MODEL,
    streamFn: streamOf(assistantMessage('plan')),
  });
  t.after(() => lead.dispose());
  assert.deepEqual(lead.tools, ['read']);

  const workerTools = buildTools({ mode: 'worker', cwd, writeScope: ['docs/**'] });
  const write = workerTools.find((tool) => tool.name === 'write');
  await write.execute('call_1', { path: 'docs/b.md', content: '# b\n' });
  const written = await fs.readFile(path.join(cwd, 'docs', 'b.md'), 'utf8');
  assert.equal(written, '# b\n');

  await assert.rejects(
    () => write.execute('call_2', { path: 'internal/x.go', content: 'x' }),
    /tool_denied/,
  );
});

test('配置错误在启动即拒绝：模式、cwd、run/generation', async (t) => {
  const cwd = await workspace(t);
  assert.throws(() => createPiExecutor({ mode: 'admin', cwd, runId: 'r', generation: 1 }), /只允许/);
  assert.throws(() => createPiExecutor({ mode: 'worker', cwd: 'relative', runId: 'r', generation: 1 }), /绝对路径/);
  assert.throws(() => createPiExecutor({ mode: 'worker', cwd, runId: '', generation: 1 }), /runId/);
  assert.throws(() => createPiExecutor({ mode: 'worker', cwd, runId: 'r', generation: 0 }), /generation/);
});
