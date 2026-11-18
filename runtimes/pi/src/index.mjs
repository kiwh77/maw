// Pi 执行器对 maw 暴露的唯一入口。
//
// 调用方（PI-102 的适配器）只需要：建执行器 → run(prompt) → 读 events/usage/outcome。
// 事件形状与用量口径见 docs/contracts/vnext/execution/pi-execution-contract-v1.md。

export {
  CONTRACT,
  PROTOCOL,
  MODES,
  ERROR_CATEGORIES,
  envelope,
  normalizeUsage,
  classifyError,
  terminalFromStopReason,
  hasUsage,
} from './protocol.mjs';

export {
  MODE_POLICIES,
  ToolDeniedError,
  assertMode,
  toolNamesFor,
  pathInScope,
  assertWriteAllowed,
  toRepoRelative,
  scrubEnv,
  redactSecrets,
} from './policy.mjs';

export { createPiExecutor, buildTools } from './executor.mjs';
