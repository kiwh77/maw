// maw.pi.execution/v1 的运行时侧实现：消息信封、用量归一、错误分类。
//
// 契约：docs/contracts/vnext/execution/pi-execution-contract-v1.md
// 这里只做「把 Pi SDK 的事实翻译成 maw 能落库的形状」，不判断阶段、不写库。

export const CONTRACT = 'maw.pi.execution/v1';

/** 协议标识：与 CodeBuddy 的 stream-json/1 不同名，两条来源不得互相解释。 */
export const PROTOCOL = Object.freeze({ format: 'pi-stream-json', version: '1' });

export const MODES = Object.freeze(['worker', 'lead', 'diagnose']);

/** 错误类别封闭集合（契约 §7.2）。 */
export const ERROR_CATEGORIES = Object.freeze([
  'auth',
  'quota',
  'rate_limit',
  'timeout',
  'tool_denied',
  'invalid_output',
  'crash',
  'env',
  'unknown',
]);

let eventSeq = 0;

/** 事件身份：进程内单调递增 + 随机后缀，够用且可读。 */
export function newEventId() {
  eventSeq += 1;
  const rand = Math.random().toString(36).slice(2, 8);
  return `pe_${Date.now().toString(36)}${eventSeq.toString(36)}${rand}`;
}

export function newCallId() {
  eventSeq += 1;
  const rand = Math.random().toString(36).slice(2, 8);
  return `pc_${Date.now().toString(36)}${eventSeq.toString(36)}${rand}`;
}

function iso(value) {
  if (value instanceof Date) return value.toISOString();
  if (typeof value === 'string' && value !== '') return new Date(value).toISOString();
  return new Date().toISOString();
}

/**
 * 构造一条 maw 事件信封（契约 §5.2）。
 *
 * run_id 与 generation 缺失直接抛错：事件没有归属就不该被写出去，
 * 与其让它到下游被丢弃，不如在这里失败。
 */
export function envelope({ type, runId, generation, callId, payload = {}, clock = () => new Date() }) {
  if (typeof type !== 'string' || type === '') throw new Error('envelope: type 必填');
  if (typeof runId !== 'string' || runId === '') throw new Error('envelope: run_id 必填');
  if (!Number.isInteger(generation) || generation < 1) {
    throw new Error('envelope: generation 必须是 >=1 的整数');
  }
  const now = clock();
  const event = {
    contract: CONTRACT,
    event_id: newEventId(),
    type,
    run_id: runId,
    generation,
    occurred_at: iso(now),
    received_at: iso(now),
    payload,
  };
  if (callId) event.call_id = callId;
  return event;
}

function intOrNull(value) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return null;
  if (value < 0) return null;
  return Math.trunc(value);
}

/**
 * 把 Pi SDK 的 Usage 归一成 maw 的逐次调用用量（契约 §8、§10）。
 *
 * - 缺失即 null，绝不填 0；
 * - 缓存字段独立，不并入 input、不参与 total；
 * - total 一律重算为 input + output；来源给的值只作为对照证据；
 * - 只有四个 token 分量都给全才算 observed，否则 unknown 并写明缺口。
 */
export function normalizeUsage(usage, { callId, occurredAt, clock = () => new Date() } = {}) {
  const source = usage ?? {};
  const input = intOrNull(source.input);
  const output = intOrNull(source.output);
  const cacheRead = intOrNull(source.cacheRead);
  const cacheWrite = intOrNull(source.cacheWrite);

  const notes = [];
  let quality = 'observed';
  if (input === null && output === null && cacheRead === null && cacheWrite === null) {
    quality = 'unknown';
    notes.push('来源未给出任何 token 字段');
  } else if (input === null || output === null) {
    quality = 'unknown';
    notes.push(`token 分量缺失（input=${input}，output=${output}）`);
  }
  if (cacheRead === null && cacheWrite === null) {
    notes.push('来源未给出缓存分解');
    if (quality === 'observed') quality = 'estimated';
  }

  const total = input !== null && output !== null ? input + output : null;
  const sourceTotal = intOrNull(source.totalTokens);
  if (sourceTotal !== null && total !== null && sourceTotal !== total) {
    notes.push(`来源 totalTokens=${sourceTotal} 与 input+output=${total} 不一致，已按契约重算`);
  }

  return {
    scope: 'call',
    semantic: 'delta',
    input_tokens: input,
    output_tokens: output,
    cache_read_tokens: cacheRead,
    cache_write_tokens: cacheWrite,
    total_tokens: total,
    quality,
    ...(notes.length > 0 ? { basis: notes.join('；') } : {}),
    ...(sourceTotal !== null ? { source_total_tokens: sourceTotal } : {}),
    ...(callId ? { call_id: callId } : {}),
    occurred_at: iso(occurredAt ?? clock()),
  };
}

/** 该条用量是否具备「一轮对话」的资格：有任何 token 或明确给了 result。 */
export function hasUsage(usage) {
  if (!usage) return false;
  return (
    usage.input_tokens !== null ||
    usage.output_tokens !== null ||
    usage.cache_read_tokens !== null ||
    usage.cache_write_tokens !== null
  );
}

const ERROR_PATTERNS = [
  { category: 'auth', re: /(401|403|unauthoriz|invalid api key|authentication|permission denied: api)/i },
  { category: 'rate_limit', re: /(429|rate limit|too many requests)/i },
  { category: 'quota', re: /(quota|insufficient (funds|balance)|billing|credit balance)/i },
  { category: 'timeout', re: /(timed? ?out|ETIMEDOUT|deadline exceeded)/i },
  { category: 'env', re: /(ENOENT|EACCES|EPERM|not permitted|command not found|no such file)/i },
  { category: 'invalid_output', re: /(invalid (json|response|tool)|parse (error|failed)|schema validation)/i },
];

/**
 * 把 SDK／进程错误归类到封闭集合（契约 §7.2）。
 *
 * 先看显式 code，再看消息；都认不出就是 unknown——不把未知硬塞进 crash。
 */
export function classifyError(error) {
  const text = [
    error?.code,
    error?.name,
    error?.status,
    error?.message,
    typeof error === 'string' ? error : '',
  ]
    .filter((v) => typeof v === 'string' || typeof v === 'number')
    .join(' ');

  if (error?.code === 'tool_denied') return 'tool_denied';
  if (error?.name === 'AbortError') return 'timeout';
  for (const { category, re } of ERROR_PATTERNS) {
    if (re.test(text)) return category;
  }
  return 'unknown';
}

/**
 * 从最后一轮 assistant 消息的 stopReason 推导终态（契约 §7.1）。
 *
 * 返回 { outcome, reason }；outcome 为 null 表示「还没到终态」。
 * 注意：退出码与 stopReason 都不是交付证据，这里只给运行结论。
 */
export function terminalFromStopReason(stopReason, { aborted = false, error = null } = {}) {
  if (aborted) return { outcome: 'cancelled', reason: 'abort_requested' };
  if (error) return { outcome: 'failed', reason: classifyError(error) };
  switch (stopReason) {
    case 'stop':
      return { outcome: 'completed', reason: 'result' };
    case 'length':
      // 截断的回答仍是「有结果」，但要在证据里标出来，别让上游以为完整。
      return { outcome: 'completed', reason: 'truncated' };
    case 'error':
      return { outcome: 'failed', reason: 'unknown' };
    case 'aborted':
      return { outcome: 'cancelled', reason: 'abort_requested' };
    case 'toolUse':
    case 'pending':
    case 'deferred':
      return { outcome: null, reason: 'running' };
    default:
      return { outcome: null, reason: 'running' };
  }
}

/** 错误证据里要保留的最小事实；消息先脱敏。 */
export function errorEvidence(error, redact) {
  const category = classifyError(error);
  const raw = typeof error === 'string' ? error : error?.message ?? String(error ?? '');
  const message = typeof redact === 'function' ? redact(raw) : raw;
  return {
    category,
    message: message.slice(0, 2000),
    retryable: category === 'rate_limit' || category === 'timeout',
  };
}
