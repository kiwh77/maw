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
package db
