// Package process 实现 macOS 上受管 Run 的排他执行锁、进程身份与无 shell 启动。
//
// 契约依据：
//   - docs/contracts/vnext/execution/runtime-state-contract-v1.md §6
//   - docs/ARCHITECTURE_VNEXT.zh-CN.md §8（RunSpec、排他锁、进程身份与 nonce）
//
// 身份由三部分共同校验：PID、进程启动时间、本次 Run 的 nonce。只凭 PID 不能
// 判定归属：PID 会被回收复用。macOS 不再向非特权调用者暴露子进程环境，因此
// nonce 在启动时写入身份记录，并在校验时与该 Run 的 nonce 比对，而不是回读
// 子进程环境。nonce 仍通过 MAW_RUN_NONCE 注入，供子进程自我标识。
//
// 进程控制能力按平台显式给出：当前只实现 macOS；其他平台返回 ErrUnsupported，
// 不默认继承 macOS 的行为。本包不重发模型任务，也不自动确认未知子进程停止。
package process
