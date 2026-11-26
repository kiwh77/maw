// Package resources 提供工程任务与节点配置的条件写入口（/api/v1）。
//
// 本包只调用注入的应用用例（任务身份规划、节点登记、探测受理），不直接访问
// SQLite，也不挂接服务根路由。原始凭据字段在进入用例前被拒绝。
package resources
