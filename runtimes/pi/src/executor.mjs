// 三模式共用的 Pi 执行器（契约 §3–§7）。
//
// 一个执行器实例只服务一次 Run、一种模式：模型请求经 SDK 的 Agent 循环跑完，
// 本文件负责把 SDK 事实翻译成 maw 事件，并在工具边界执行授权。
// 它不写数据库、不改任务状态、不判交付。

import fs from 'node:fs/promises';
import path from 'node:path';

import { Agent } from '@earendil-works/pi-agent-core';
import { VERSION as PI_CODING_AGENT_VERSION } from '@earendil-works/pi-coding-agent';
import { createReadTool, createWriteTool, createEditTool, createBashTool } from '@earendil-works/pi-coding-agent';
import { streamSimple } from '@earendil-works/pi-ai/compat';

import {
  MODES,
  PROTOCOL,
  envelope,
  errorEvidence,
  hasUsage,
  newCallId,
  normalizeUsage,
  terminalFromStopReason,
} from './protocol.mjs';

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
} from './policy.mjs';

const MODE_PROMPTS = {
  worker: [
    'You are a maw Worker executing exactly one task card in a managed git worktree.',
    'Stay inside the card write scope. Do not modify files outside it.',
    'Do not change task state, do not merge, do not push. maw owns those.',
    'Finish by stating what changed, what you verified, and anything you could not do.',
  ].join(' '),
  lead: [
    'You are a maw Lead planner. You may only read and return a structured plan.',
    'You have no write tools and must not claim one ran.',
    'Return a JSON array of task cards with deps, write_scope, acceptance, verify and origin evidence.',
  ].join(' '),
  diagnose: [
    'You are the maw diagnostic agent. You may only read and return a decision.',
    'Do not modify files. Answer with one action from the closed set and the evidence you used.',
  ].join(' '),
};

function defaultStreamFn() {
  return (model, context, options) => streamSimple(model, context, options);
}

function scopedReadOperations({ cwd, mode, readScope }) {
  const guard = (absolutePath) => {
    const rel = toRepoRelative(cwd, absolutePath);
    if (mode !== 'worker' && readScope.length > 0 && !pathInScope(rel, readScope)) {
      throw new ToolDeniedError('read_out_of_scope', rel);
    }
    return rel;
  };
  return {
    readFile: async (absolutePath) => {
      guard(absolutePath);
      return fs.readFile(absolutePath);
    },
    access: async (absolutePath) => {
      guard(absolutePath);
      await fs.access(absolutePath);
    },
  };
}

function scopedWriteOperations({ cwd, mode, writeScope }) {
  const guard = (absolutePath) => assertWriteAllowed({ mode, cwd, writeScope, target: absolutePath });
  return {
    writeFile: async (absolutePath, content) => {
      guard(absolutePath);
      await fs.writeFile(absolutePath, content, 'utf8');
    },
    mkdir: async (dir) => {
      guard(dir);
      await fs.mkdir(dir, { recursive: true });
    },
  };
}

function scopedEditOperations({ cwd, mode, writeScope }) {
  const guard = (absolutePath) => assertWriteAllowed({ mode, cwd, writeScope, target: absolutePath });
  return {
    readFile: async (absolutePath) => {
      guard(absolutePath);
      return fs.readFile(absolutePath);
    },
    writeFile: async (absolutePath, content) => {
      guard(absolutePath);
      await fs.writeFile(absolutePath, content, 'utf8');
    },
    access: async (absolutePath) => {
      guard(absolutePath);
      await fs.access(absolutePath);
    },
  };
}

/**
 * 按模式装配工具集合。
 *
 * 授权在这里落地：worker 才有写工具，且写操作被 write_scope 包住；
 * lead/diagnose 只有读工具，读也受 read_scope 限制（未给 read_scope 时不额外限制）。
 */
export function buildTools({ mode, cwd, writeScope = [], readScope = [], allowBash = false }) {
  assertMode(mode);
  const names = toolNamesFor(mode, { allowBash });
  const tools = [];
  for (const name of names) {
    if (name === 'read') {
      tools.push(createReadTool(cwd, { operations: scopedReadOperations({ cwd, mode, readScope }) }));
    } else if (name === 'write') {
      tools.push(createWriteTool(cwd, { operations: scopedWriteOperations({ cwd, mode, writeScope }) }));
    } else if (name === 'edit') {
      tools.push(createEditTool(cwd, { operations: scopedEditOperations({ cwd, mode, writeScope }) }));
    } else if (name === 'bash') {
      tools.push(
        createBashTool(cwd, {
          exposeSessionEnvironment: false,
          spawnHook: (context) => ({
            ...context,
            cwd,
            env: scrubEnv(process.env),
          }),
        }),
      );
    }
  }
  return tools;
}

function summarizeArgs(args) {
  const text = JSON.stringify(args ?? {});
  return redactSecrets(text.length > 1200 ? `${text.slice(0, 1200)}…` : text);
}

function messageText(message) {
  if (!message || !Array.isArray(message.content)) return '';
  return message.content
    .filter((part) => part?.type === 'text' && typeof part.text === 'string')
    .map((part) => part.text)
    .join('\n');
}

/**
 * 创建一个 Pi 执行器。
 *
 * options:
 *   mode        worker | lead | diagnose
 *   cwd         受管 worktree 绝对路径
 *   runId       本库 Run 身份
 *   generation  代际
 *   writeScope  写盘范围（仓库相对）
 *   readScope   只读范围（lead/diagnose）
 *   allowBash   节点是否允许终端工具（默认 false）
 *   model       模型描述符（宿主配置）
 *   streamFn    可选：注入的模型流（测试用；缺省走 pi-ai 的 streamSimple）
 *   clock       可选：时间源（测试用）
 */
export function createPiExecutor(options = {}) {
  const {
    mode,
    cwd,
    runId,
    generation,
    writeScope = [],
    readScope = [],
    allowBash = false,
    model,
    streamFn,
    clock = () => new Date(),
    sourceNs = 'pi',
  } = options;

  assertMode(mode);
  if (typeof cwd !== 'string' || !path.isAbsolute(cwd)) {
    throw new Error('createPiExecutor: cwd 必须是绝对路径');
  }
  if (typeof runId !== 'string' || runId === '') throw new Error('createPiExecutor: runId 必填');
  if (!Number.isInteger(generation) || generation < 1) {
    throw new Error('createPiExecutor: generation 必须是 >=1 的整数');
  }

  const tools = buildTools({ mode, cwd, writeScope, readScope, allowBash });
  const events = [];
  const callFacts = [];

  const agent = new Agent({
    initialState: {
      systemPrompt: options.systemPrompt ?? MODE_PROMPTS[mode],
      model,
      tools,
      messages: [],
    },
    streamFn: streamFn ?? defaultStreamFn(),
  });

  const emit = (type, payload, extra = {}) => {
    const event = envelope({ type, runId, generation, payload, clock, ...extra });
    events.push(event);
    return event;
  };

  let aborted = false;
  let lastError = null;
  let lastAssistant = null;
  let terminal = null;

  const unsubscribe = agent.subscribe((event) => {
    switch (event.type) {
      case 'message_end': {
        const message = event.message;
        if (!message || message.role !== 'assistant') return;
        lastAssistant = message;
        const callId = newCallId();
        if (message.stopReason === 'error') {
          // SDK 在错误结果上给的是全 0 占位，不是真实用量。按契约 §10.2：
          // 0 ≠ 未知，这里报 unknown 且不带 token，避免把失败计成「零消耗」。
          const unknown = normalizeUsage(null, { callId, occurredAt: clock(), clock });
          emit('usage', {
            ...unknown,
            basis: '来源在错误结果上给全 0 占位；按契约不当作真实用量',
            source_ns: sourceNs,
          }, { callId });
          return;
        }
        const usage = normalizeUsage(message.usage, { callId, occurredAt: clock(), clock });
        if (hasUsage(usage)) {
          callFacts.push({ ...usage, source_ns: sourceNs });
          emit('usage', { ...usage, source_ns: sourceNs }, { callId });
        }
        return;
      }
      case 'tool_execution_start':
        emit('tool_call', {
          tool_name: event.toolName,
          arguments: summarizeArgs(event.args),
          tool_call_id: event.toolCallId,
        });
        return;
      case 'tool_execution_end':
        emit('tool_result', {
          tool_name: event.toolName,
          tool_call_id: event.toolCallId,
          is_error: Boolean(event.isError),
          duration_ms: event.durationMs ?? null,
        });
        return;
      case 'agent_end':
        return;
      default:
        return;
    }
  });

  return {
    mode,
    runId,
    generation,
    protocol: PROTOCOL,
    tools: tools.map((tool) => tool.name),
    events,
    usage: callFacts,

    /** 执行一次提示并给出终态；模型失败只体现在返回值，不抛给调用方。 */
    async run(prompt) {
      emit('version_report', {
        sdk_version: PI_CODING_AGENT_VERSION,
        protocol: PROTOCOL,
        mode,
        tools: tools.map((tool) => tool.name),
      });
      emit('session_started', { mode, cwd, source_ns: sourceNs });

      try {
        await agent.prompt(prompt);
      } catch (error) {
        lastError = error;
        if (error instanceof ToolDeniedError) {
          emit('tool_denied', denialPayload(error));
        }
      }

      // SDK 的 Agent 不向调用方抛模型错误：它把错误写进 state.errorMessage，
      // 并给一条 stopReason=error 的消息。这里补成可分类的证据（契约 §7.1／§7.2）。
      if (!lastError && typeof agent.state?.errorMessage === 'string' && agent.state.errorMessage !== '') {
        lastError = Object.assign(new Error(agent.state.errorMessage), { name: 'PiModelError' });
      }
      if (lastError) {
        const evidence = errorEvidence(lastError, redactSecrets);
        emit('error', evidence);
        if (lastError instanceof ToolDeniedError) {
          emit('tool_denied', denialPayload(lastError));
        }
      }

      const derived = terminalFromStopReason(lastAssistant?.stopReason, {
        aborted,
        error: lastError,
      });
      terminal = {
        outcome: derived.outcome ?? 'failed',
        reason: derived.outcome ? derived.reason : 'no_result',
      };
      if (derived.outcome === null && lastAssistant === null && lastError === null) {
        terminal = { outcome: 'failed', reason: 'no_result' };
      }

      const text = redactSecrets(messageText(lastAssistant));
      const evidence = [];
      evidence.push(`stop_reason:${lastAssistant?.stopReason ?? 'none'}`);
      if (terminal.reason === 'truncated') evidence.push('truncated:true');
      if (lastError) evidence.push(`category:${terminal.reason}`);
      if (aborted) evidence.push('abort_requested:true');
      if (callFacts.length > 0) evidence.push(`calls_with_usage:${callFacts.length}`);

      const resultPayload = {
        ok: terminal.outcome === 'completed',
        outcome: terminal.outcome,
        reason: terminal.reason,
        text,
        evidence,
      };
      emit('result', resultPayload);
      emit('session_ended', {
        reason: aborted ? 'abort_requested' : 'settled',
        stop_reason: lastAssistant?.stopReason ?? null,
        aborted,
      });

      return {
        outcome: terminal.outcome,
        reason: terminal.reason,
        text,
        evidence,
        usage: callFacts,
        events,
      };
    },

    /** 请求取消：先置标志再中止，终态由 run() 统一给出。 */
    abort() {
      aborted = true;
      agent.abort();
    },

    dispose() {
      unsubscribe();
    },

    get state() {
      return {
        aborted,
        terminal,
        calls: callFacts.length,
      };
    },
  };
}

export { MODES };
