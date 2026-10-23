// Package model 是冻结契约的 Go 值对象层：身份、封闭状态集合、类型化错误、不可变规格。
//
// 契约来源（只读，不得在本包改写含义）：
//
//	docs/contracts/vnext/task/task-instance-contract-v1.md      maw.task.instance/v1
//	docs/contracts/vnext/execution/runtime-state-contract-v1.md maw.runtime.state/v1
//	docs/contracts/vnext/delivery/delivery-manifest-contract-v1.md maw.delivery.manifest/v1
//	docs/contracts/vnext/ports/app-ports-contract-v1.md         maw.app.ports/v1
//
// 三条约束：
//
//  1. 零业务副作用。本包只做取值、判定与构造，不做 I/O，不开事务，不碰时钟以外的外部世界
//     （时间一律由调用方传入，便于重放与测试）。
//  2. 不 import 任何 maw 内部实现包。只允许标准库，因此可以被 app / adapter / infra 任意引用
//     而不形成反向依赖（对应 app-ports-contract-v1 §1 的依赖方向）。
//  3. 身份与错误码只有这一套定义。其他包需要「任务版本」「Run 身份」「端口错误」时，
//     必须引用本包类型，不得各自定义字符串常量。
//
// 每个 Validate 方法返回 []*Error，一条错误对应契约不变量登记表里的一个码；
// 调用方既能整体判定失败，也能按码分类处理。
package model
