// Package db 是本机状态库（SQLite）的访问层。
//
// 它把三件事分开，不再像 0.4.0 那样「每次打开顺手建表」：
//
//  1. 迁移：schema 只由 Migrate／MigrateDB 推进，序列在 migrations.go 里显式登记，
//     每个版本执行一次并记进 schema_migrations 台账（含内容摘要）。
//  2. 读：OpenReadOnly／Store.Read／WithReadTx 走只读池——连接带 query_only(1)、
//     事务是 deferred BEGIN。查询不建表、不升级 schema、不加写锁。
//  3. 写：OpenStore／Store.Write 走写池——连接带 _txlock=immediate，
//     一次 Write 是一个 BEGIN IMMEDIATE 短事务，busy 时按 RetryPolicy 有界退避重试，
//     重试循环里没有建目录、DDL 或进程副作用。
//
// 旧的 Open／WithTx 保留为兼容入口，行为与调用方预期一致（会补齐迁移），
// 但它们属于写入口；新代码应按需使用 Store，而不是每个查询重开连接。
//
// 任务身份（契约 maw.task.instance/v1）与上面三条正交：
//
//   - tasks 物理主键仍是 (project_id, id)，id 继续当 display_id；instance_id 是稳定身份，
//     非空、唯一、永不复用，由 UpsertTask／bindTaskIdentity 保证；
//   - 定义按版本冻结在 task_versions，只能追加（库触发器拦截 UPDATE／DELETE）；
//   - 退役是「归档 + 释放别名」一件事：实例转 retired、版本快照保留、tasks 行同时删除，
//     编号重建因此拿到新 instance_id，历史按 instance_id 分开。
//
// 资源占用（契约 maw.runtime.state/v1 §4）也在这里落库：
//
//   - nodes 记节点与槽上限；账号额度只是展示字段，不参与空闲槽推断；
//   - resource_leases 按规范化资源键（节点槽／清理后的绝对目录／工程合入位）记占用，
//     未释放的行在 resource_key 上唯一，所以「唯一有效持有者」由库保证；
//   - 释放是条件写入：代际必须等于持有代际（E243），节点槽要求进程停稳（E241），
//     工作目录要求产物冻结（E242），并在同一事务写 lease.released 事件（E261）。
package db
