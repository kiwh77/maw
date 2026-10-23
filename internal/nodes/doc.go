// Package nodes 维护节点登记与就绪模型（VN-302）。
//
// 边界（与 docs/ARCHITECTURE_VNEXT.zh-CN.md §5.1 一致）：
//
//   - 节点身份（model.NodeID）与工作者身份（model.AgentID）是两套命名空间：
//     本包只认节点，不为工程 Agent 建记录，也不把 Agent 名当节点状态。
//   - 动态状态分四维表达：入口与运行时、认证与模型、连接与心跳、容量与接收策略。
//     不合并成一个 online 布尔值。
//   - 管理侧的 draining／disabled 与实时占用 occupied 分开：draining 只停止接新任务，
//     不打断已有执行；disabled 是人工停用；occupied 是槽位已满。
//   - 能力证据必须绑定版本、证据引用与验证时间。版本、入口或平台变化会让旧的
//     observed 证据失效；unknown／unobserved 永远不参与自动匹配。
//   - 不保管厂商秘密，不登录，不启动模型任务：这里只做登记、观测投影与判定。
package nodes
