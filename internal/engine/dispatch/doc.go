// Package dispatch 实现持久派发协调循环：按持久 Operation 推进准备与启动，
// 不依赖某个 HTTP 请求或浏览器连接是否存活。
//
// 契约依据：
//
//	docs/contracts/vnext/execution/runtime-state-contract-v1.md §3、§6、§7
//	docs/contracts/vnext/ports/app-ports-contract-v1.md §2.1、§2.7、§3
//	docs/ARCHITECTURE_VNEXT.zh-CN.md §3、§8、§10
//
// 四条边界：
//
//  1. **同一状态目录只有一个活动协调者**。协调循环启动前先拿状态目录下的排他锁；
//     第二个协调者拿到 ErrCoordinatorActive，不消费任何操作。`maw serve` 内由
//     Leader 持这一把锁同时驱动派发队列与系统 Agent 轮询，两套循环不会互相抢锁。
//  2. **先写意图，再动外部世界**。准备、启动、确认三个步骤都先落盘到本机意图文件，
//     再执行副作用；中枢重启后按意图恢复，而不是重新起一个工具进程。
//  3. **启动结果不确定不重发**。意图已进入 launching 但没有可核对的进程身份时，
//     恢复只标待核实（Uncertain），保持 Operation 与占用，绝不再次启动工具进程。
//  4. **排队意图可撤回**。尚未发出启动的意图可以 Withdraw：落盘 withdrawn 并把
//     Operation 标为 rejected；已进入 launching／confirmed 的意图拒绝撤回。
//
// 本包不选题、不建 worktree、不直接写数据库业务状态：它消费调用方给出的队列端口，
// 通过 Launcher／Preparer 端口推进副作用，并把结果写回 Queue 端口。
//
// 状态目录布局：
//
//	<state>/dispatch/coordinator.lock   协调者排他锁
//	<state>/dispatch/intents/<op-id>.json  每个操作一份可恢复意图（原子替换）
package dispatch
