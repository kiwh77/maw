import assert from 'node:assert/strict';
import test from 'node:test';

import {
  ToolDeniedError,
  assertMode,
  assertWriteAllowed,
  denialPayload,
  pathInScope,
  redactSecrets,
  scrubEnv,
  toRepoRelative,
  toolNamesFor,
} from '../src/policy.mjs';

test('三种模式是封闭集合', () => {
  assert.equal(assertMode('worker').writes, true);
  assert.equal(assertMode('lead').writes, false);
  assert.equal(assertMode('diagnose').writes, false);
  assert.throws(() => assertMode('admin'), /只允许 worker\/lead\/diagnose/);
});

test('工具按模式与配置启用', () => {
  assert.deepEqual(toolNamesFor('worker'), ['read', 'write', 'edit']);
  assert.deepEqual(toolNamesFor('worker', { allowBash: true }), ['read', 'write', 'edit', 'bash']);
  assert.deepEqual(toolNamesFor('lead'), ['read']);
  assert.deepEqual(toolNamesFor('diagnose'), ['read']);
});

test('写盘范围：前缀、通配与嵌套', () => {
  assert.equal(pathInScope('docs/a.md', ['docs/**']), true);
  assert.equal(pathInScope('docs/a/b.md', ['docs/**']), true);
  assert.equal(pathInScope('docs', ['docs/**']), true);
  assert.equal(pathInScope('docsx/a.md', ['docs/**']), false);
  assert.equal(pathInScope('internal/goal/lifecycle.go', ['internal/goal/**']), true);
  assert.equal(pathInScope('internal/app/x.go', ['internal/goal/**']), false);
  assert.equal(pathInScope('a/b/c.md', ['a/*/c.md']), true);
  assert.equal(pathInScope('a/b/d.md', ['a/*/c.md']), false);
  assert.equal(pathInScope('README.md', ['README.md']), true);
  assert.equal(pathInScope('README.md', []), false, '空范围不允许写任何文件');
});

test('写权限：模式、范围、工作区三重', () => {
  const cwd = '/repo';
  assert.equal(
    assertWriteAllowed({ mode: 'worker', cwd, writeScope: ['docs/**'], target: '/repo/docs/a.md' }),
    'docs/a.md',
  );
  assert.throws(
    () => assertWriteAllowed({ mode: 'worker', cwd, writeScope: ['docs/**'], target: '/repo/internal/a.go' }),
    (error) => error instanceof ToolDeniedError && error.reason === 'out_of_scope',
  );
  assert.throws(
    () => assertWriteAllowed({ mode: 'lead', cwd, writeScope: ['docs/**'], target: '/repo/docs/a.md' }),
    (error) => error instanceof ToolDeniedError && error.reason === 'mode_read_only',
  );
  assert.throws(
    () => assertWriteAllowed({ mode: 'worker', cwd, writeScope: ['docs/**'], target: '/etc/passwd' }),
    (error) => error instanceof ToolDeniedError && error.reason === 'outside_workspace',
  );
});

test('路径归一：反斜杠、./ 与越界', () => {
  assert.equal(toRepoRelative('/repo', './docs/../docs/a.md'), 'docs/a.md');
  assert.throws(() => toRepoRelative('/repo', '/repo/../etc/passwd'), /tool_denied/);
  assert.equal(denialPayload(new ToolDeniedError('out_of_scope', 'internal/x')).reason, 'out_of_scope');
});

test('工具子进程环境剔除凭据', () => {
  const env = scrubEnv({
    PATH: '/usr/bin',
    HOME: '/home/u',
    OPENAI_API_KEY: 'sk-secret-value',
    GITHUB_TOKEN: 'ghp_abcdefghijklmnopqrstuvwxyz',
    AWS_SECRET_ACCESS_KEY: 'aws-secret',
    MY_PASSWORD: 'hunter2',
  });
  assert.equal(env.PATH, '/usr/bin');
  assert.equal(env.HOME, '/home/u');
  assert.equal(env.OPENAI_API_KEY, undefined);
  assert.equal(env.GITHUB_TOKEN, undefined);
  assert.equal(env.AWS_SECRET_ACCESS_KEY, undefined);
  assert.equal(env.MY_PASSWORD, undefined);
});

test('事件与错误出口脱敏', () => {
  assert.equal(redactSecrets('key=sk-abcdefghijk12345'), 'key=[redacted]');
  assert.equal(redactSecrets('token ghp_abcdefghijklmnopqrst'), 'token [redacted]');
  assert.equal(redactSecrets('nothing to hide'), 'nothing to hide');
});
