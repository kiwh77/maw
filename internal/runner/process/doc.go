// Package process 实现 macOS 上受管 Run 的排他执行锁、进程身份、无 shell 启动
// 与停止证据。
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
// 子进程以 Setpgid 独立成组（PGID == 启动时的 PID）。停止按整组执行：
// 先 TERM，等待父进程退出与组清空；组未清空则升级 KILL 再确认。只有在
// 进程组确认无存活成员时才给出 DescendantsConfirmed，Reusable 也只依据
// 这一事实——父进程退出、锁已释放、监督进程退出都不构成目录可复用证据。
// 无法确认时返回 OutcomeUnknown，由调用方隔离目录而不是宣布可复用。
//
// 进程控制能力按平台显式给出：当前只实现 macOS；其他平台返回 ErrUnsupported，
// 不默认继承 macOS 的行为。本包不重发模型任务，也不杀未知归属进程。
package process
