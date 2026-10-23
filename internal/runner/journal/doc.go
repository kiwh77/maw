// Package journal 实现每个 Run 的本机语义日志：冻结规格落盘、追加式语义记录、
// 补交游标与去重补交，以及有上限的保留裁剪。
//
// 契约依据：
//   - docs/contracts/vnext/execution/runtime-state-contract-v1.md §7、§10.4
//   - docs/ARCHITECTURE_VNEXT.zh-CN.md §8、§10.2、§10.4、§11.1
//
// Run 目录布局：
//
//	spec.json             冻结规格（已脱敏）；写入后不再改动
//	spec.meta.json        规格脱敏报告
//	journal.jsonl         追加式语义记录；每条带稳定序号与文件位置
//	cursor.json           补交游标：已确认交付的最高本地序号
//	journal.state.json    恢复与保留事实（撕裂尾部、裁剪量、是否超出预算）
//
// 本包不启动厂商进程，不写业务状态。数据库暂不可写时，记录先留在本机日志，
// 恢复后由 Resubmit 按稳定去重键补交；同键重复交付由提交方以
// ErrAlreadyStored 表达，日志据此推进游标而不重复计数。
package journal
