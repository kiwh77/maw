// Package claim 是统一领取用例（契约 maw.app.ports/v1 §2.1 的 ClaimExecution）。
//
// 指定题、人工 next 与后续自动匹配都走同一个 Claim；它们不再各写一套领取规则。
//
// 三条边界：
//
//  1. **指定题不回落。** 给出 requested_task 时只解析该实例；它不 ready、被依赖或
//     资源挡住时直接返回错误，绝不在内部再按 next 规则改领另一题。
//  2. **一次短事务。** 验 revision、查依赖／写盘／共享文件、占节点槽与目录、建 Run
//     与 Operation、改任务状态、追加事件全部在同一写事务里，要么一起提交，要么
//     一起回滚——失败不会留下「占了目录却没有事件」的半状态。
//  3. **事务内不碰外部世界。** 不建 worktree、不跑 git、不起进程；这些由调用方在
//     提交之后执行，失败后走补偿事务（写新的持久事实，而不是撤销历史）。
//
// 两种领取形态：
//
//   - **受管领取**（NodeID 非空）：走 db.ClaimRun，占节点槽 + 工作目录，建 Run 与
//     Dispatch Operation，任务进入 claimed。节点槽与工作目录是两种资源，释放前置不同。
//   - **未绑定节点的人工领取**（NodeID 为空）：只占工作目录并记一条已完成的 Dispatch
//     Operation，不占节点槽、不虚构 Run。人工 next 不受节点容量约束，这正是
//     「不把 max_parallel 改成硬容量」的落地方式；但任务与目录的占用规则仍然一致。
//
// 错误语义（契约 §2.1）：没有可领取的题是 no_ready，有题但被依赖／写盘／共享文件／
// 节点挡住是 conflict，两者必须分开；预期 revision 不符是 revision_mismatch；节点或
// 受管执行达到上限是 capacity_exhausted；同幂等键同摘要重放返回 Replay=true 且不新写行。
package claim
