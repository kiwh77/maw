#!/usr/bin/env node
// Pi 运行时的单次执行入口（PI-102 的节点适配器按该协议拉起本进程）。
//
// 用法（从 stdin 读一行 JSON 配置，stdout 输出 NDJSON 事件）：
//   echo '{"mode":"worker","cwd":"/repo","run_id":"run_1","generation":1,"prompt":"..."}' \
//     | node src/main.mjs
//
// 关键约束：
//   - 模型凭据只从进程环境读取（EnvAllowlist），不进 argv、不进输出；
//   - 事件先脱敏再输出；stdout 只放事件，诊断信息走 stderr；
//   - 配置类错误（模式未知、cwd 不是绝对路径、协议不支持）以非零退出码结束，不降级。

import process from 'node:process';

import { createPiExecutor } from './executor.mjs';
import { PROTOCOL } from './protocol.mjs';
import { redactSecrets } from './policy.mjs';

async function readConfig() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  const raw = Buffer.concat(chunks).toString('utf8').trim();
  if (!raw) throw new Error('stdin 为空：需要一行 JSON 配置');
  return JSON.parse(raw);
}

function emit(event) {
  process.stdout.write(`${JSON.stringify(event)}\n`);
}

/** 允许宿主用 snake_case 传配置（与 maw 的 JSON 字段一致），执行器内部用 camelCase。 */
function normalizeConfig(raw) {
  const config = { ...raw };
  const aliases = {
    run_id: 'runId',
    write_scope: 'writeScope',
    read_scope: 'readScope',
    allow_bash: 'allowBash',
    system_prompt: 'systemPrompt',
    source_ns: 'sourceNs',
  };
  for (const [from, to] of Object.entries(aliases)) {
    if (config[to] === undefined && config[from] !== undefined) config[to] = config[from];
    delete config[from];
  }
  return config;
}

async function probe() {
  const { VERSION } = await import('@earendil-works/pi-coding-agent');
  emit({ ok: true, protocol: PROTOCOL, node: process.version, sdk: VERSION });
  return 0;
}

async function main() {
  if (process.argv.includes('--probe')) return probe();

  let config;
  try {
    config = await readConfig();
  } catch (error) {
    process.stderr.write(`pi-runtime: 配置读取失败：${redactSecrets(String(error?.message ?? error))}\n`);
    return 2;
  }

  let executor;
  try {
    executor = createPiExecutor(normalizeConfig(config));
  } catch (error) {
    process.stderr.write(`pi-runtime: 启动被拒绝：${redactSecrets(String(error?.message ?? error))}\n`);
    return 2;
  }

  try {
    const result = await executor.run(config.prompt);
    for (const event of result.events) emit(event);
    if (result.outcome === 'completed') return 0;
    if (result.outcome === 'cancelled') return 65;
    return 1;
  } finally {
    executor.dispose();
  }
}

process.exitCode = await main();
