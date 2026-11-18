// 三模式授权与写盘边界（契约 §3、§6）。
//
// 授权在**工具边界**执行：模型请求什么都行，能不能落地由 wrapper 决定。
// 这里不依赖 SDK 的提示词，也不相信模型自述。

import path from 'node:path';

export const MODE_POLICIES = Object.freeze({
  worker: Object.freeze({ label: '执行一张已领取的卡', writes: true, bash: true, reads: true }),
  lead: Object.freeze({ label: '规划与提案（只读）', writes: false, bash: false, reads: true }),
  diagnose: Object.freeze({ label: '系统诊断（只读）', writes: false, bash: false, reads: true }),
});

export class ToolDeniedError extends Error {
  constructor(reason, detail) {
    super(`tool_denied: ${reason}${detail ? ` (${detail})` : ''}`);
    this.name = 'ToolDeniedError';
    this.code = 'tool_denied';
    this.reason = reason;
    this.detail = detail ?? '';
  }
}

export function assertMode(mode) {
  if (!Object.hasOwn(MODE_POLICIES, mode)) {
    throw new Error(`未知模式 ${JSON.stringify(mode)}：只允许 worker/lead/diagnose`);
  }
  return MODE_POLICIES[mode];
}

/** 该模式允许启用的工具名（顺序固定，便于快照对比）。 */
export function toolNamesFor(mode, { allowBash = false } = {}) {
  const policy = assertMode(mode);
  if (!policy.writes) return ['read'];
  const names = ['read', 'write', 'edit'];
  if (policy.bash && allowBash) names.push('bash');
  return names;
}

export function normalizePath(p) {
  if (typeof p !== 'string' || p.trim() === '') return '';
  const unified = p.replaceAll('\\', '/');
  const cleaned = path.posix.normalize(unified.startsWith('./') ? unified.slice(2) : unified);
  return cleaned === '.' ? '' : cleaned;
}

function scopePrefix(pattern) {
  const normalized = normalizePath(pattern);
  if (normalized.endsWith('/**')) return normalized.slice(0, -3);
  if (normalized.includes('*')) return normalized.slice(0, normalized.search(/[*?[]/));
  return normalized;
}

function globToRegExp(pattern) {
  const escaped = normalizePath(pattern)
    .replace(/[.+^${}()|[\]\\]/g, '\\$&')
    .replaceAll('**', '\u0000')
    .replaceAll('*', '[^/]*')
    .replaceAll('?', '[^/]')
    .replaceAll('\u0000', '.*');
  return new RegExp(`^${escaped}$`);
}

/** 文件是否落在写盘范围内；scope 为空表示什么都不允许写。 */
export function pathInScope(file, scope = []) {
  const target = normalizePath(file);
  if (!target) return false;
  for (const raw of scope) {
    const pattern = normalizePath(raw);
    if (!pattern) continue;
    if (!pattern.includes('*')) {
      if (target === pattern || target.startsWith(`${pattern}/`)) return true;
      continue;
    }
    if (globToRegExp(pattern).test(target)) return true;
    const prefix = scopePrefix(pattern);
    if (prefix && (target === prefix || target.startsWith(`${prefix}/`))) return true;
  }
  return false;
}

/** 把工具给的绝对路径换成仓库相对路径；越出 cwd 一律拒绝。 */
export function toRepoRelative(cwd, absoluteOrRelative) {
  const absolute = path.isAbsolute(absoluteOrRelative)
    ? absoluteOrRelative
    : path.resolve(cwd, absoluteOrRelative);
  const rel = path.relative(cwd, absolute);
  if (rel === '' || rel.startsWith('..') || path.isAbsolute(rel)) {
    throw new ToolDeniedError('outside_workspace', normalizePath(absolute));
  }
  return normalizePath(rel);
}

/** 写操作前的授权判定：模式 + 范围，双重。 */
export function assertWriteAllowed({ mode, cwd, writeScope = [], target }) {
  const policy = assertMode(mode);
  if (!policy.writes) {
    throw new ToolDeniedError('mode_read_only', `${mode} 不得写盘`);
  }
  const rel = toRepoRelative(cwd, target);
  if (!pathInScope(rel, writeScope)) {
    throw new ToolDeniedError('out_of_scope', rel);
  }
  return rel;
}

const SECRET_KEY_RE = /(^|_)(API_?KEY|TOKEN|SECRET|PASSWORD|ACCESS_KEY|PRIVATE_KEY)$/i;

/** 工具子进程的环境：凭据一律剔除，只留执行所需的普通变量。 */
export function scrubEnv(env = process.env) {
  const out = {};
  for (const [key, value] of Object.entries(env)) {
    if (typeof value !== 'string') continue;
    if (SECRET_KEY_RE.test(key)) continue;
    out[key] = value;
  }
  return out;
}

const SECRET_VALUE_PATTERNS = [
  /\bsk-[A-Za-z0-9_-]{8,}\b/g,
  /\b(?:ghp|gho|ghs)_[A-Za-z0-9]{16,}\b/g,
  /\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}\b/g,
  /\b(AKIA|ASIA)[A-Z0-9]{12,}\b/g,
];

/** 事件、日志、错误消息出口统一走这里：宁可比对不齐，也不泄漏。 */
export function redactSecrets(text) {
  if (typeof text !== 'string' || text === '') return text;
  let out = text;
  for (const re of SECRET_VALUE_PATTERNS) out = out.replace(re, '[redacted]');
  return out;
}

/** 授权拒绝时的证据形状（进 journal 与事件，不改任务状态）。 */
export function denialPayload(error) {
  const denied = error instanceof ToolDeniedError ? error : null;
  return {
    reason: denied?.reason ?? 'unknown',
    detail: denied?.detail ?? '',
    message: redactSecrets(denied?.message ?? String(error)),
  };
}
