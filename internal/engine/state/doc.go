// Package state 表达三类状态之间**合法**的转移，以及资源占用能否释放。
//
// 契约来源：
//
//	docs/contracts/vnext/task/task-instance-contract-v1.md       任务身份与版本（状态词汇）
//	docs/contracts/vnext/execution/runtime-state-contract-v1.md  运行阶段、取消竞争、占用释放
//	docs/contracts/vnext/delivery/delivery-manifest-contract-v1.md 验收通过与否
//	docs/ARCHITECTURE_VNEXT.zh-CN.md §7                           三套状态互不推导
//
// 三条边界：
//
//  1. **不做存储调用**。本包只回答「这一步合不合法」，落库由 app 层在事务里做。
//  2. **不合并三套状态**。任务状态、运行阶段、交付结论各有自己的规则；
//     Run 的 completed 不等于任务 done，Run failed 也不必然把任务打成 failed。
//  3. **任务词汇沿用现有实现**（backlog…done/cancelled 十一个），
//     转移矩阵的权威从 internal/machine 迁到这里，machine 变成薄封装，保证只有一套表。
//
// 错误分两种：任务转移用本包的哨兵错误（app 层负责翻成用户可读文案），
// 运行与交付规则用 model 的不变量码（E2xx／E3xx），便于调用方按码分类处理。
package state
