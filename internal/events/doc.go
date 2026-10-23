// Package events 把来源记录与旁路上报摄取成「有质量、有归属边界」的标准事件，
// 并在摄取这一层完成分域去重、乱序与补交的记账。
//
// 契约来源：
//
//	docs/contracts/vnext/observability/timeline-metering-contract-v1.md §2–§3
//	docs/contracts/vnext/execution/runtime-state-contract-v1.md §7（事件：身份、游标、双时间戳）
//	docs/contracts/vnext/ports/app-ports-contract-v1.md §2.5（Decode 只产生事件，不判定结果）
//	docs/ARCHITECTURE_VNEXT.zh-CN.md §12.1（旁路观测 POST /observations 不得绕过任务归属）
//
// 四条边界：
//
//  1. **只写事实，不做决定。** 本包输出 model.Event 与摄取注解，不落库、不改任务状态、
//     不判定运行结果、不创建 Run。落库（internal/db）与业务判定（internal/engine/state）
//     都归上层；本包的错误只说明「这条记录为什么没被接受」。
//  2. **按来源分域去重，不按内容去重。** 去重域是「接入域 + 接入器」，域内按来源稳定键
//     判定：同键同内容＝幂等重放，同键不同内容＝冲突（不覆盖、不新建），
//     键不同即独立事件——即便内容一字不差，只要发生时间不同就是两件事。
//     去重不是「内容指纹全局唯一」，否则会吃掉合法的重复发生。
//  3. **双时间戳只归位、不猜测。** 发生时间来自来源，接收时间由摄取方现取；
//     乱序到达只标 late_arrival，绝不回退已发的游标或发生时间前沿；
//     旧字段（legacy）的 ts 是当年写入时刻而非发生时刻，因此只能记 estimated。
//  4. **旁路上报没有所有权。** 观测域不能写控制类事实（运行阶段、任务状态、占用、领取），
//     也不能替他人声明归属。它可以提到某个任务或 Run，但那只是「来源说自己看到了什么」：
//     事件本身保持未归属，声明值原样留在载荷里，绝不据此伪造执行或 done。
package events
