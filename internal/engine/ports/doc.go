// Package ports 是应用端口层：只有接口、调用请求与结果，没有实现。
//
// 契约来源：docs/contracts/vnext/ports/app-ports-contract-v1.md（maw.app.ports/v1）。
// 七个端口与契约 §2 的小节、ports_contract.py 的 PORTS 表一一对应，顺序也一致：
//
//	ClaimExecution  Store  Probe  Launch  Decode  Interpret  ProcessController
//
// 本包的三条自我约束：
//
//  1. 不 import 任何实现：没有 net/http、database/sql、modernc.org/sqlite、os/exec，
//     也不 import internal/db、internal/app、internal/gitops、internal/adapters、internal/runner。
//     唯一的 maw 内部依赖是 internal/engine/model（纯值对象，零副作用）。
//     端口层的 P121 判定的是跨层依赖：同层值对象允许，app／adapter／infra 禁止
//     （app-ports-contract-v1 修订记录 v1.1）。
//  2. 不做副作用。接口方法只描述契约，不内建重试循环、不缓存、不写日志。
//  3. 事务边界写在方法上：Claim 与 Store 的调用点在事务内，其余在事务外。
//     这条区分由调用方（app 层）与接线快照共同保证，端口本身不持有 *sql.Tx。
//
// 依赖白名单的唯一落点是 ForbiddenForPort 与 AllowedInternalImport：自检（imports_test.go）
// 与将来的接线校验都调用它们，不各写一份规则。
package ports
