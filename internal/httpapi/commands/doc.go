// Package commands 把领取、派发、取消、重试、交付、验证与评审的写动作发布为
// /api/v1 命令端点。
//
// 本包只做传输适配，不执行业务动作：
//
//   - 所有异步动作立即返回 202 + Location，步骤在 202 之后由后台完成
//     （http-api-contract-v1 §3）；只有幂等重放才允许返回 200 与终止态结果。
//   - 幂等键按 (project, actor, method, path) 作用域校验；同键不同请求返回
//     409 idempotency_key_reuse，绝不「以最新请求为准」（§4）。
//   - 写动作必须带 If-Match 或 body.expected_revision；缺失 422 revision_required，
//     不匹配 409 revision_mismatch（§5）。
//   - 用例错误由注入端口返回，本包只做状态码与错误体映射（§6）。
package commands
