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
//     唯一的 maw 内部依赖是 internal/engine/model（纯值对象，零副作用），
//     这样端口既不会把 SQL 事务泄漏进签名，也不会把厂商进程语义写进接口。
//  2. 不做副作用。接口方法只描述契约，不内建重试循环、不缓存、不写日志。
//  3. 事务边界写在方法上：Claim 与 Store 的调用点在事务内，其余在事务外。
//     这条区分由调用方（app 层）与接线快照共同保证，端口本身不持有 *sql.Tx。
//
// 关于接线快照的一个已知张力：app-ports-contract-v1 的正例把
// internal/engine/ports 与 internal/engine/model 都声明为 layer=port 且 imports 为空，
// 而 Go 里「接口引用值对象」必然产生 ports → model 的 import。本包按内容实质处理
// （只依赖值对象），并在自检里把允许集写死为「标准库 + model」；若后续接线任务
// 生成快照，需要在契约层把 model 归入端口层内部，或放宽 P121 对同层值对象的判定。
// 这属于契约修订，不在 VN-201 的写盘范围内。
package ports
