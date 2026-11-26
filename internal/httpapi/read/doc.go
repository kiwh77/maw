// Package read 把 internal/query 的权威只读模型发布为 /api/v1 读取资源。
//
// 本包只提供可挂载的 http.Handler，不注册到服务根路由（接线留给 VN-955）。
// 所有路径均为 GET／HEAD：不发起节点探测，不写业务状态，不跑 schema 迁移。
//
// 契约锚点：docs/contracts/vnext/http/http-api-contract-v1.md §1–§2、§6–§7。
package read
