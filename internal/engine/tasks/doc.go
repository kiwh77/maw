// Package tasks 是任务身份与定义版本的唯一应用服务：创建、增量规划、
// 归档、reset 与旧编号解析。
//
// 契约依据：
//
//	docs/contracts/vnext/task/task-instance-contract-v1.md §3–§5
//	docs/ARCHITECTURE_VNEXT.zh-CN.md §4（TaskInstance / TaskVersion）
//
// 本包只做决策与产出计划，不落库、不改旧命令调用点、不执行数据库维护：
//
//   - 所有创建、更新、归档都围绕稳定 InstanceID；DisplayID 只是人读别名，
//     reset 与重建时绝不复用旧实例。
//   - 已冻结版本不可变。定义变化只追加新版本并前进 active_version；活跃 Run
//     继续固定在启动时冻结的旧版本上。
//   - 默认增量保留既有实例与交付；reset 只把旧实例退役为快照，不释放占用、
//     不复活旧实例。
//   - 依赖解析顺序固定：instance 精确匹配优先，legacy 只用于读存量且歧义
//     时拒绝而不是取第一条。
package tasks
